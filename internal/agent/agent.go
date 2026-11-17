package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Kind string

const (
	Claude Kind = "claude"
	Codex  Kind = "codex"
)

type Spec struct {
	Name         Kind
	Bin          string
	Base         []string
	AllowedTools bool
	ClaudeFlags  bool
}

type Result struct {
	Code              int
	Stdout, Stderr    string
	Stalled, TimedOut bool
}

var Candidates = []Spec{{Claude, "claude", []string{"-p"}, true, true}, {Codex, "codex", []string{"exec"}, false, false}}

func Detect() []Spec {
	var out []Spec
	for _, s := range Candidates {
		if _, err := exec.LookPath(s.Bin); err == nil {
			out = append(out, s)
		}
	}
	return out
}

func Find(found []Spec, name string) (Spec, error) {
	if name == "" && len(found) > 0 {
		return found[0], nil
	}
	for _, s := range found {
		if string(s.Name) == name {
			return s, nil
		}
	}
	if name != "" {
		return Spec{}, fmt.Errorf("unknown agent: %s", name)
	}
	return Spec{}, errors.New("no agent CLI found (need `claude` or `codex` on PATH). We never ask for an API key - the agent binary carries its own auth.")
}

func Args(s Spec, allowed []string, skip bool, target, prompt string) []string {
	a := append([]string{}, s.Base...)
	if skip {
		flag := "--dangerously-skip-permissions"
		if s.Name == Codex {
			flag = "--dangerously-bypass-approvals-and-sandbox"
		}
		a = append([]string{flag}, a...)
	}
	if s.AllowedTools && len(allowed) > 0 {
		a = append(a, "--allowedTools", strings.Join(allowed, ","))
	}
	if s.ClaudeFlags {
		a = append(a, "--permission-mode", "acceptEdits", "--add-dir", target, "--output-format", "text")
	}
	return append(a, prompt)
}

func Env(base []string, workspace, target string) []string {
	e := append([]string{}, base...)
	e = append(e, "CKB_WHITEHAT_WORKSPACE="+workspace, "CKB_WHITEHAT_TARGET="+target)
	return e
}

// feeder captures an agent's output stream and records when the last byte
// arrived, so the stall timer resets on any output (a chatty agent must not
// be killed just because it keeps printing).
type feeder struct {
	mu      *sync.Mutex
	last    *time.Time
	storage *bytes.Buffer
}

func (f *feeder) Write(p []byte) (int, error) {
	f.mu.Lock()
	*f.last = time.Now()
	defer f.mu.Unlock()
	return f.storage.Write(p)
}

// idle returns seconds since the last output byte.
func (f *feeder) idle() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return time.Since(*f.last)
}

func Spawn(ctx context.Context, s Spec, argv []string, env []string, cwd string, stall, max time.Duration) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("empty agent args")
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(cctx, s.Bin, argv...)
	cmd.Dir, cmd.Env = cwd, env
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	var out, errbuf bytes.Buffer
	var mu sync.Mutex
	last := time.Now()
	outF, errF := &feeder{&mu, &last, &out}, &feeder{&mu, &last, &errbuf}
	cmd.Stdout, cmd.Stderr = outF, errF
	if err := cmd.Start(); err != nil {
		return Result{}, err
	}
	started := time.Now()
	// cmd.Wait() only returns once the piped stdio has been fully closed,
	// so no output is lost when the child exits.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	deadline := time.NewTimer(max)
	defer deadline.Stop()
	for {
		select {
		case err := <-done:
			code := 0
			if err != nil {
				var exit *exec.ExitError
				if errors.As(err, &exit) {
					code = exit.ExitCode()
				} else {
					code = 1
				}
			}
			return Result{Code: code, Stdout: out.String(), Stderr: errbuf.String()}, nil
		case <-tick.C:
			if outF.idle() > stall {
				kill(cmd)
				return Result{Code: 1, Stdout: out.String(), Stderr: errbuf.String(), Stalled: true}, nil
			}
			if time.Since(started) > max {
				kill(cmd)
				return Result{Code: 1, Stdout: out.String(), Stderr: errbuf.String(), TimedOut: true}, nil
			}
		case <-deadline.C:
			kill(cmd)
			return Result{Code: 1, Stdout: out.String(), Stderr: errbuf.String(), TimedOut: true}, nil
		case <-cctx.Done():
			kill(cmd)
			return Result{Code: 1, Stdout: out.String(), Stderr: errbuf.String()}, cctx.Err()
		}
	}
}

func kill(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if runtime.GOOS != "windows" {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	_ = cmd.Process.Kill()
}
