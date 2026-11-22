package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
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
// be killed just because it keeps printing). The stdout feeder additionally
// hands every completed stdout line to onLine, calling it while holding the
// mutex; onLine must be a short, non-blocking state update.
type feeder struct {
	mu      *sync.Mutex
	last    *time.Time
	storage *bytes.Buffer
	onLine  func(string)
	pending []byte
}

func (f *feeder) Write(p []byte) (int, error) {
	f.mu.Lock()
	*f.last = time.Now()
	n, err := f.storage.Write(p)
	if f.onLine != nil {
		f.pending = append(f.pending, p...)
		for {
			i := bytes.IndexByte(f.pending, '\n')
			if i < 0 {
				break
			}
			f.onLine(string(f.pending[:i]))
			f.pending = f.pending[i+1:]
		}
	}
	f.mu.Unlock()
	return n, err
}

// flushTail delivers any trailing line that never received a newline, so a
// final line without a newline still reaches the observer.
func (f *feeder) flushTail() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onLine == nil || len(f.pending) == 0 {
		return
	}
	f.onLine(string(f.pending))
	f.pending = nil
}

// idle returns seconds since the last output byte.
func (f *feeder) idle() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return time.Since(*f.last)
}

// Spawn is SpawnWithObserver with no observer.
func Spawn(ctx context.Context, s Spec, argv []string, env []string, cwd string, stall, max time.Duration) (Result, error) {
	return SpawnWithObserver(ctx, s, argv, env, cwd, stall, max, nil)
}

// SpawnWithObserver is Spawn with an optional per-line observer on the
// agent's stdout. onLine is invoked once per completed line (newline
// stripped), in emission order, as the agent emits it. It must be
// non-blocking (record state, never wait) and may assume the feeder mutex
// is held while it runs, so keep it a short state update. Passing nil
// disables observation; nil onLine => behavior identical to Spawn.
// Any trailing partial line is delivered as one final onLine call after
// the child exits, so slow last-line output still reaches the observer.
func SpawnWithObserver(ctx context.Context, s Spec, argv []string, env []string, cwd string, stall, max time.Duration, onLine func(string)) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("empty agent args")
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(cctx, s.Bin, argv...)
	cmd.Dir, cmd.Env = cwd, env
	setProcAttr(cmd)
	var out, errbuf bytes.Buffer
	var mu sync.Mutex
	last := time.Now()
	outF, errF := &feeder{mu: &mu, last: &last, storage: &out, onLine: onLine}, &feeder{mu: &mu, last: &last, storage: &errbuf}
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
			outF.flushTail()
			return Result{Code: code, Stdout: out.String(), Stderr: errbuf.String()}, nil
		case <-tick.C:
			if outF.idle() > stall {
				killProcess(cmd)
				drainDone(done)
				mu.Lock()
				stdout, stderr := out.String(), errbuf.String()
				mu.Unlock()
				return Result{Code: 1, Stdout: stdout, Stderr: stderr, Stalled: true}, nil
			}
			if time.Since(started) > max {
				killProcess(cmd)
				drainDone(done)
				mu.Lock()
				stdout, stderr := out.String(), errbuf.String()
				mu.Unlock()
				return Result{Code: 1, Stdout: stdout, Stderr: stderr, TimedOut: true}, nil
			}
		case <-deadline.C:
			killProcess(cmd)
			drainDone(done)
			mu.Lock()
			stdout, stderr := out.String(), errbuf.String()
			mu.Unlock()
			return Result{Code: 1, Stdout: stdout, Stderr: stderr, TimedOut: true}, nil
		case <-cctx.Done():
			killProcess(cmd)
			drainDone(done)
			mu.Lock()
			stdout, stderr := out.String(), errbuf.String()
			mu.Unlock()
			return Result{Code: 1, Stdout: stdout, Stderr: stderr}, cctx.Err()
		}
	}
}

// drainDone waits, with a bound, for cmd.Wait to return (i.e. the stdio-copy
// goroutines to have finished) after a killProcess. The group kill makes the
// child die and Wait return promptly; the 10s bound guards a pathological
// kill so a caller can never block indefinitely. Until this returns, the
// copy goroutine may still be mid-Write into out/errbuf, so callers must not
// read those buffers concurrently (audit M1: the old code read
// out.String() here racing the copy's Write under -race).
func drainDone(done <-chan error) {
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
}
