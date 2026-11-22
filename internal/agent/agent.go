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
	switch {
	case s.Name == Claude || s.ClaudeFlags:
		if skip {
			a = append([]string{"--dangerously-skip-permissions"}, a...)
		} else {
			// dontAsk ensures Claude never blocks on an interactive permission
			// prompt in headless -p mode; disallowed tools fail fast cleanly.
			a = append(a, "--permission-mode", "dontAsk")
			if s.AllowedTools && len(allowed) > 0 {
				a = append(a, "--allowedTools", strings.Join(allowed, ","))
			}
		}
		a = append(a, "--verbose", "--output-format", "stream-json", "--setting-sources", "user", "--add-dir", target)
	case s.Name == Codex:
		if skip {
			a = append([]string{"--dangerously-bypass-approvals-and-sandbox"}, a...)
		} else {
			a = append(a, "--full-auto")
		}
		a = append(a, "--json", "--color", "never", "-c", "project_doc_max_bytes=0")
	default:
		if skip {
			a = append([]string{"--dangerously-skip-permissions"}, a...)
		}
		if s.AllowedTools && len(allowed) > 0 {
			a = append(a, "--allowedTools", strings.Join(allowed, ","))
		}
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
		// Release oversized backing arrays once a huge JSON line has been
		// consumed so a single big tool event doesn't pin megabytes of RAM.
		if len(f.pending) == 0 {
			if cap(f.pending) > 64*1024 {
				f.pending = nil
			} else {
				f.pending = f.pending[:0]
			}
		} else if cap(f.pending) > 64*1024 && len(f.pending) <= 64*1024 {
			rem := make([]byte, len(f.pending))
			copy(rem, f.pending)
			f.pending = rem
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
// agent's stdout. Stdout is parsed incrementally through StreamParser so
// Claude `stream-json` and Codex `--json` events emit concise live activity
// lines to onLine while keeping Result.Stdout as clean assistant prose (and
// plain-text agents pass through unchanged). onLine is invoked in emission
// order while holding the feeder mutex, so it must be a short, non-blocking
// state update. Passing nil disables observation while still decoding
// structured streams into Result.Stdout. Any trailing partial line is
// delivered after the child exits.
func SpawnWithObserver(ctx context.Context, s Spec, argv []string, env []string, cwd string, stall, max time.Duration, onLine func(string)) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("empty agent args")
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(cctx, s.Bin, argv...)
	cmd.Dir, cmd.Env = cwd, env
	// Explicit closed stdin ensures neither claude nor codex can ever hang
	// waiting for interactive terminal input.
	cmd.Stdin = strings.NewReader("")
	setProcAttr(cmd)
	var out, errbuf bytes.Buffer
	var mu sync.Mutex
	last := time.Now()
	parser := NewStreamParser(onLine)
	outF := &feeder{mu: &mu, last: &last, storage: &out, onLine: parser.FeedLine}
	errF := &feeder{mu: &mu, last: &last, storage: &errbuf}
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

	// snapshot returns the decoded plain-text stdout (falling back to raw
	// stdout if nothing plain-text was extracted so crash diagnostics are
	// never lost) and stderr, safely synchronized under mu.
	snapshot := func() (string, string) {
		mu.Lock()
		defer mu.Unlock()
		stdout := parser.PlainText()
		if stdout == "" {
			stdout = out.String()
		}
		return stdout, errbuf.String()
	}

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
			stdout, stderr := snapshot()
			return Result{Code: code, Stdout: stdout, Stderr: stderr}, nil
		case <-tick.C:
			if outF.idle() > stall {
				killProcess(cmd)
				drainDone(done)
				stdout, stderr := snapshot()
				return Result{Code: 1, Stdout: stdout, Stderr: stderr, Stalled: true}, nil
			}
			if time.Since(started) > max {
				killProcess(cmd)
				drainDone(done)
				stdout, stderr := snapshot()
				return Result{Code: 1, Stdout: stdout, Stderr: stderr, TimedOut: true}, nil
			}
		case <-deadline.C:
			killProcess(cmd)
			drainDone(done)
			stdout, stderr := snapshot()
			return Result{Code: 1, Stdout: stdout, Stderr: stderr, TimedOut: true}, nil
		case <-cctx.Done():
			killProcess(cmd)
			drainDone(done)
			stdout, stderr := snapshot()
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
