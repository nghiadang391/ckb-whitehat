package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	Agy    Kind = "agy"
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
	Activity          string
	Stalled, TimedOut bool
}

// Candidates defines auto-detection priority when --agent is omitted:
// claude -> agy -> codex.
var Candidates = []Spec{
	{Claude, "claude", []string{"-p"}, true, true},
	{Agy, "agy", []string{"-p"}, true, false},
	{Codex, "codex", []string{"exec"}, false, false},
}

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
	return Spec{}, errors.New("no agent CLI found (need `claude`, `agy`, or `codex` on PATH). We never ask for an API key - the agent binary carries its own auth.")
}

// DefaultDeniedCommands lists high-risk shell command prefixes blocked by
// default for both `agy` and `claude` runs (unless explicitly requested via
// `--allowed-tools`, e.g. `Bash(curl:*)`). Keeping a broad allow (`Bash(*)` /
// `command(*)`) paired with explicit deny rules lets audits work across any
// target stack (Node, Rust, Go, Python, shell utilities like `find`/`jq`) and
// prevents `agy` headless mode from aborting on unlisted-command Ask prompts,
// while still guarding against accidental destructive, remote-mutating, or
// exfiltration commands.
var DefaultDeniedCommands = []string{
	// Destructive filesystem / system / privilege escalation
	"rm -rf",
	"rm -fr",
	"sudo",
	"su",
	"dd",
	"mkfs",
	"chmod",
	"chown",
	"shutdown",
	"reboot",
	// Git remote & destructive history mutation
	"git push",
	"git reset --hard",
	"git clean",
	"git remote",
	// Network exfiltration, host DB probing, credential/env dumping
	"curl",
	"wget",
	"ssh",
	"scp",
	"rsync",
	"nc",
	"ncat",
	"psql",
	"env",
	"printenv",
}

// parseAllowedBash extracts wildcard Bash allowance ("Bash", "Bash(*)",
// "Bash(:*)") and any explicitly named `Bash(<cmd>:*)` commands from allowed.
func parseAllowedBash(allowed []string) (allowAll bool, specific []string, specificSet map[string]bool) {
	specificSet = map[string]bool{}
	for _, entry := range allowed {
		entry = strings.TrimSpace(entry)
		if entry == "Bash" || entry == "Bash(*)" || entry == "Bash(:*)" {
			allowAll = true
			continue
		}
		if !strings.HasPrefix(entry, "Bash(") || !strings.HasSuffix(entry, ")") {
			continue
		}
		inner := strings.TrimSpace(entry[len("Bash(") : len(entry)-1])
		inner = strings.TrimSuffix(inner, ":*")
		inner = strings.TrimSuffix(inner, "*")
		inner = strings.TrimSpace(inner)
		if inner == "" {
			allowAll = true
			continue
		}
		if !specificSet[inner] {
			specificSet[inner] = true
			specific = append(specific, inner)
		}
	}
	return allowAll, specific, specificSet
}

// DeniedCommands returns the effective list of denied shell command prefixes
// for a run given the caller's allowed-tools list (any command explicitly
// allowed by name, such as `Bash(curl:*)`, is exempted).
func DeniedCommands(allowed []string) []string {
	_, _, explicit := parseAllowedBash(allowed)
	out := make([]string, 0, len(DefaultDeniedCommands))
	for _, cmd := range DefaultDeniedCommands {
		if !explicit[cmd] {
			out = append(out, cmd)
		}
	}
	return out
}

// claudeTargetPathPattern formats target as a Claude Code absolute-path glob
// pattern anchored to the filesystem root (`//abs/path/**`). In Claude Code's
// permission syntax a single leading `/` is relative to the settings directory,
// whereas `//` anchors to filesystem root.
func claudeTargetPathPattern(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(target))
	cleaned = strings.TrimRight(cleaned, "/")
	if cleaned == "" {
		return "//**"
	}
	if strings.HasPrefix(cleaned, "/") {
		return "/" + cleaned + "/**"
	}
	return cleaned + "/**"
}

// claudeEmptyMCPConfig disables all user/project MCP servers when paired with
// `--strict-mcp-config`, matching agy's `mcp(*)` deny rule.
const claudeEmptyMCPConfig = `{"mcpServers":{}}`

// claudeReadOnlyDisallowedTools explicitly denies every mutating, shell, and
// network tool during `ckb-whitehat init` so user-level `~/.claude/settings.json`
// allow rules (loaded via `--setting-sources user`) can never override the
// read-only inspection boundary.
const claudeReadOnlyDisallowedTools = "Bash,Write,Edit,MultiEdit,NotebookEdit,WebFetch,WebSearch"

func claudeDisallowedTools(allowed []string, target string) []string {
	denied := DeniedCommands(allowed)
	out := make([]string, 0, len(denied)+6)
	for _, cmd := range denied {
		out = append(out, fmt.Sprintf("Bash(%s:*)", cmd))
	}
	// Block writes/edits inside the target repository (matching agy's
	// write_file(<target>) deny rule).
	if pat := claudeTargetPathPattern(target); pat != "" {
		for _, tool := range []string{"Write", "Edit", "MultiEdit", "NotebookEdit"} {
			out = append(out, fmt.Sprintf("%s(%s)", tool, pat))
		}
	}
	// Block live web fetches/searches unless explicitly requested in allowed.
	allowWeb := map[string]bool{}
	for _, entry := range allowed {
		allowWeb[strings.TrimSpace(entry)] = true
	}
	for _, webTool := range []string{"WebFetch", "WebSearch"} {
		if !allowWeb[webTool] {
			out = append(out, webTool)
		}
	}
	return out
}

// agySettings represents the ephemeral JSON passed to `agy --settings`.
type agySettings struct {
	AgentMode               string         `json:"agentMode"`
	ToolPermission          string         `json:"toolPermission"`
	AllowNonWorkspaceAccess bool           `json:"allowNonWorkspaceAccess"`
	Permissions             agyPermissions `json:"permissions"`
}

type agyPermissions struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// agySettingsJSON builds the ephemeral --settings payload for `agy`.
// Crucially, we NEVER pass `--add-dir <target>` to `agy` because that would
// trigger discovery/execution of untrusted `<target>/.agents/hooks.json`,
// `mcp_config.json`, and `AGENTS.md`. Instead we keep cwd = workspace and
// grant read-only access to `<target>` via `permissions.allow: ["read_file(<target>)"]`
// paired with `permissions.deny: ["write_file(<target>)"]`.
//
// In `agy`, explicit `deny` rules take strict precedence over `allow`
// (`Deny > Ask > Allow`) and return a recoverable tool error, whereas any
// unlisted command falling back to `Ask` aborts headless mode immediately.
func agySettingsJSON(allowed []string, target string, readOnly bool) string {
	var allowRules []string
	if target != "" {
		allowRules = append(allowRules, fmt.Sprintf("read_file(%s)", target))
	}
	if readOnly {
		cfg := agySettings{
			AgentMode:               "",
			ToolPermission:          "request-review",
			AllowNonWorkspaceAccess: false,
			Permissions: agyPermissions{
				Allow: allowRules,
				Deny:  []string{"command(*)", "write_file(*)", "read_url(*)", "execute_url(*)", "mcp(*)"},
			},
		}
		b, _ := json.Marshal(cfg)
		return string(b)
	}

	allowAllBash, specificCmds, _ := parseAllowedBash(allowed)
	if allowAllBash {
		allowRules = append(allowRules, "command(*)")
	} else {
		for _, cmd := range specificCmds {
			allowRules = append(allowRules, fmt.Sprintf("command(%s)", cmd))
		}
	}

	var denyRules []string
	for _, cmd := range DeniedCommands(allowed) {
		denyRules = append(denyRules, fmt.Sprintf("command(%s)", cmd))
	}
	if target != "" {
		denyRules = append(denyRules, fmt.Sprintf("write_file(%s)", target))
	}
	denyRules = append(denyRules, "read_url(*)", "execute_url(*)", "mcp(*)")

	cfg := agySettings{
		AgentMode:               "accept-edits",
		ToolPermission:          "request-review",
		AllowNonWorkspaceAccess: false,
		Permissions: agyPermissions{
			Allow: allowRules,
			Deny:  denyRules,
		},
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

func Args(s Spec, allowed []string, skip bool, target, prompt string) []string {
	a := append([]string{}, s.Base...)
	switch {
	case s.Name == Agy:
		// Put prompt immediately after `-p` and omit `--add-dir <target>` so
		// agy never discovers or executes untrusted `<target>/.agents/hooks.json`.
		a = append(a, prompt, "--output-format", "stream-json", "--disable-slash-commands")
		if skip {
			a = append(a, "--dangerously-skip-permissions")
		} else {
			a = append(a, "--settings", agySettingsJSON(allowed, target, false))
		}
		return a
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
			if disallowed := claudeDisallowedTools(allowed, target); len(disallowed) > 0 {
				a = append(a, "--disallowedTools", strings.Join(disallowed, ","))
			}
			// Disable user/project MCP servers unless --yolo was requested.
			// --mcp-config is variadic so --strict-mcp-config follows it.
			a = append(a, "--mcp-config", claudeEmptyMCPConfig, "--strict-mcp-config")
		}
		// --add-dir <directories...> is variadic in Claude Code's CLI parser,
		// so it MUST appear before non-variadic flags (never directly before
		// the positional prompt, or Commander.js swallows the prompt as a
		// second directory) and "--" terminates option parsing before prompt.
		a = append(a, "--add-dir", target, "--setting-sources", "user", "--verbose", "--output-format", "stream-json", "--")
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

// ReadOnlyArgs builds CLI arguments for a strictly read-only inspection pass
// (used by `ckb-whitehat init`): Agy gets `--settings` denying all commands,
// writes, URLs, and MCP while allowing `read_file(<target>)`; Claude gets
// `--permission-mode dontAsk` with read-only tools only (`Read,Glob,Grep,LS`);
// and Codex gets `--sandbox read-only`.
func ReadOnlyArgs(s Spec, allowed []string, target, prompt string) []string {
	a := append([]string{}, s.Base...)
	switch {
	case s.Name == Agy:
		return append(a, prompt, "--output-format", "stream-json", "--disable-slash-commands", "--settings", agySettingsJSON(nil, target, true))
	case s.Name == Claude || s.ClaudeFlags:
		a = append(a, "--permission-mode", "dontAsk")
		if s.AllowedTools && len(allowed) > 0 {
			a = append(a, "--allowedTools", strings.Join(allowed, ","))
		}
		a = append(a,
			"--disallowedTools", claudeReadOnlyDisallowedTools,
			"--mcp-config", claudeEmptyMCPConfig,
			"--strict-mcp-config",
			"--add-dir", target,
			"--setting-sources", "user",
			"--verbose",
			"--output-format", "stream-json",
			"--",
		)
	case s.Name == Codex:
		a = append(a, "--sandbox", "read-only", "--json", "--color", "never", "-c", "project_doc_max_bytes=0")
	default:
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
	// never lost), stderr, and structured tool/permission activity, safely
	// synchronized under mu.
	snapshot := func() (string, string, string) {
		mu.Lock()
		defer mu.Unlock()
		stdout := parser.PlainText()
		if stdout == "" {
			stdout = out.String()
		}
		return stdout, errbuf.String(), parser.Activity()
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
			stdout, stderr, activity := snapshot()
			return Result{Code: code, Stdout: stdout, Stderr: stderr, Activity: activity}, nil
		case <-tick.C:
			if outF.idle() > stall {
				killProcess(cmd)
				drainDone(done)
				stdout, stderr, activity := snapshot()
				return Result{Code: 1, Stdout: stdout, Stderr: stderr, Activity: activity, Stalled: true}, nil
			}
			if time.Since(started) > max {
				killProcess(cmd)
				drainDone(done)
				stdout, stderr, activity := snapshot()
				return Result{Code: 1, Stdout: stdout, Stderr: stderr, Activity: activity, TimedOut: true}, nil
			}
		case <-deadline.C:
			killProcess(cmd)
			drainDone(done)
			stdout, stderr, activity := snapshot()
			return Result{Code: 1, Stdout: stdout, Stderr: stderr, Activity: activity, TimedOut: true}, nil
		case <-cctx.Done():
			killProcess(cmd)
			drainDone(done)
			stdout, stderr, activity := snapshot()
			return Result{Code: 1, Stdout: stdout, Stderr: stderr, Activity: activity}, cctx.Err()
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
