package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func candidateByKind(t *testing.T, kind Kind) Spec {
	t.Helper()
	for _, c := range Candidates {
		if c.Name == kind {
			return c
		}
	}
	t.Fatalf("candidate %q not found", kind)
	return Spec{}
}

func TestCandidatesDefaultOrder(t *testing.T) {
	want := []Kind{Agy, Claude, Codex}
	if len(Candidates) != len(want) {
		t.Fatalf("len(Candidates) = %d, want %d", len(Candidates), len(want))
	}
	for i, k := range want {
		if Candidates[i].Name != k {
			t.Fatalf("Candidates[%d].Name = %q, want %q (auto-detect order: agy -> claude -> codex)", i, Candidates[i].Name, k)
		}
	}
}

func TestArgs(t *testing.T) {
	claude := candidateByKind(t, Claude)
	wantClaudeDisallowed := strings.Join(claudeDisallowedTools(nil), ",")

	// Claude default (skip=false) with allowed tools + strict disallowedTools.
	// --add-dir <directories...> is variadic, so non-variadic flags and "--"
	// must separate it from the positional prompt.
	got := Args(claude, []string{"Bash(*)", "Read", "Write"}, false, "/target", "prompt")
	want := []string{
		"-p",
		"--permission-mode", "dontAsk",
		"--allowedTools", "Bash(*),Read,Write",
		"--disallowedTools", wantClaudeDisallowed,
		"--add-dir", "/target",
		"--setting-sources", "user",
		"--verbose",
		"--output-format", "stream-json",
		"--",
		"prompt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claude skip=false:\ngot:  %#v\nwant: %#v", got, want)
	}
	for _, mustDeny := range []string{"Bash(rm -rf:*)", "Bash(git push:*)", "Bash(sudo:*)", "Bash(curl:*)", "WebFetch", "WebSearch"} {
		if !strings.Contains(wantClaudeDisallowed, mustDeny) {
			t.Fatalf("claude disallowedTools missing %q: %s", mustDeny, wantClaudeDisallowed)
		}
	}

	// Claude skip=false with explicit Bash(curl:*) exempts curl from disallowedTools.
	gotCurl := Args(claude, []string{"Bash(*)", "Bash(curl:*)", "Read"}, false, "/target", "prompt")
	for i := 0; i < len(gotCurl)-1; i++ {
		if gotCurl[i] == "--disallowedTools" && strings.Contains(gotCurl[i+1], "Bash(curl:*)") {
			t.Fatalf("explicit Bash(curl:*) should be exempted from disallowedTools: %s", gotCurl[i+1])
		}
	}

	// Claude skip=false with no allowed tools omits --allowedTools but keeps --disallowedTools.
	got = Args(claude, nil, false, "/target", "prompt")
	want = []string{
		"-p",
		"--permission-mode", "dontAsk",
		"--disallowedTools", wantClaudeDisallowed,
		"--add-dir", "/target",
		"--setting-sources", "user",
		"--verbose",
		"--output-format", "stream-json",
		"--",
		"prompt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claude skip=false no allowed:\ngot:  %#v\nwant: %#v", got, want)
	}

	// Claude skip=true prepends --dangerously-skip-permissions and omits dontAsk/allowedTools/disallowedTools.
	got = Args(claude, []string{"Read", "Write"}, true, "/target", "prompt")
	want = []string{
		"--dangerously-skip-permissions",
		"-p",
		"--add-dir", "/target",
		"--setting-sources", "user",
		"--verbose",
		"--output-format", "stream-json",
		"--",
		"prompt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claude skip=true:\ngot:  %#v\nwant: %#v", got, want)
	}

	codex := candidateByKind(t, Codex)

	// Codex default (skip=false) uses --full-auto and --json streaming.
	got = Args(codex, nil, false, "/target", "p")
	want = []string{
		"exec",
		"--full-auto",
		"--json",
		"--color", "never",
		"-c", "project_doc_max_bytes=0",
		"p",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codex skip=false:\ngot:  %#v\nwant: %#v", got, want)
	}

	// Codex skip=true prepends --dangerously-bypass-approvals-and-sandbox.
	got = Args(codex, nil, true, "/target", "p")
	want = []string{
		"--dangerously-bypass-approvals-and-sandbox",
		"exec",
		"--json",
		"--color", "never",
		"-c", "project_doc_max_bytes=0",
		"p",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codex skip=true:\ngot:  %#v\nwant: %#v", got, want)
	}

	// Agy default (Bash(*)) maps to command(*) in allow + full high-risk deny list.
	agy := candidateByKind(t, Agy)
	got = Args(agy, []string{"Bash(*)", "Read", "Write"}, false, "/target", "prompt")
	if len(got) != 7 || got[0] != "-p" || got[1] != "prompt" || got[2] != "--output-format" || got[3] != "stream-json" || got[4] != "--disable-slash-commands" || got[5] != "--settings" {
		t.Fatalf("agy skip=false args shape unexpected: %#v", got)
	}
	for _, a := range got {
		if a == "--add-dir" {
			t.Fatalf("agy args must NEVER pass --add-dir (prevents untrusted target hooks/AGENTS.md execution): %#v", got)
		}
	}
	var cfg agySettings
	if err := json.Unmarshal([]byte(got[6]), &cfg); err != nil {
		t.Fatalf("invalid agy --settings JSON: %v (%s)", err, got[6])
	}
	if cfg.AgentMode != "accept-edits" || cfg.ToolPermission != "request-review" || cfg.AllowNonWorkspaceAccess {
		t.Fatalf("unexpected agy modes: %+v", cfg)
	}
	wantAllow := []string{"read_file(/target)", "command(*)"}
	if !reflect.DeepEqual(cfg.Permissions.Allow, wantAllow) {
		t.Fatalf("agy allow:\ngot:  %#v\nwant: %#v", cfg.Permissions.Allow, wantAllow)
	}
	for _, mustDeny := range []string{
		"command(rm -rf)", "command(rm -fr)", "command(git push)", "command(git reset --hard)",
		"command(sudo)", "command(curl)", "command(wget)", "command(psql)", "command(env)",
		"write_file(/target)", "read_url(*)", "execute_url(*)", "mcp(*)",
	} {
		found := false
		for _, d := range cfg.Permissions.Deny {
			if d == mustDeny {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("agy deny missing %q: %#v", mustDeny, cfg.Permissions.Deny)
		}
	}

	// Agy with explicit narrow Bash list preserves narrow command allow rules.
	gotNarrow := Args(agy, []string{"Bash(node:*)", "Bash(curl:*)", "Read"}, false, "/target", "prompt")
	var narrowCfg agySettings
	if err := json.Unmarshal([]byte(gotNarrow[6]), &narrowCfg); err != nil {
		t.Fatalf("invalid narrow agy settings JSON: %v", err)
	}
	wantNarrowAllow := []string{"read_file(/target)", "command(node)", "command(curl)"}
	if !reflect.DeepEqual(narrowCfg.Permissions.Allow, wantNarrowAllow) {
		t.Fatalf("agy narrow allow:\ngot:  %#v\nwant: %#v", narrowCfg.Permissions.Allow, wantNarrowAllow)
	}
	for _, d := range narrowCfg.Permissions.Deny {
		if d == "command(curl)" {
			t.Fatalf("explicit Bash(curl:*) must be exempted from agy deny: %#v", narrowCfg.Permissions.Deny)
		}
	}

	got = Args(agy, []string{"Bash(*)"}, true, "/target", "prompt")
	want = []string{"-p", "prompt", "--output-format", "stream-json", "--disable-slash-commands", "--dangerously-skip-permissions"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("agy skip=true:\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestReadOnlyArgs(t *testing.T) {
	claude := candidateByKind(t, Claude)
	got := ReadOnlyArgs(claude, []string{"Read", "Glob", "Grep", "LS"}, "/target", "inspect")
	want := []string{
		"-p",
		"--permission-mode", "dontAsk",
		"--allowedTools", "Read,Glob,Grep,LS",
		"--add-dir", "/target",
		"--setting-sources", "user",
		"--verbose",
		"--output-format", "stream-json",
		"--",
		"inspect",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("claude read-only:\ngot:  %#v\nwant: %#v", got, want)
	}

	codex := candidateByKind(t, Codex)
	got = ReadOnlyArgs(codex, []string{"Read"}, "/target", "inspect")
	want = []string{
		"exec",
		"--sandbox", "read-only",
		"--json",
		"--color", "never",
		"-c", "project_doc_max_bytes=0",
		"inspect",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codex read-only:\ngot:  %#v\nwant: %#v", got, want)
	}

	agy := candidateByKind(t, Agy)
	got = ReadOnlyArgs(agy, []string{"Read", "Glob", "Grep", "LS"}, "/target", "inspect")
	if len(got) != 7 || got[0] != "-p" || got[1] != "inspect" || got[5] != "--settings" {
		t.Fatalf("agy read-only args unexpected: %#v", got)
	}
	var cfg agySettings
	if err := json.Unmarshal([]byte(got[6]), &cfg); err != nil {
		t.Fatalf("invalid agy read-only settings JSON: %v", err)
	}
	if cfg.AgentMode != "" || cfg.ToolPermission != "request-review" || cfg.AllowNonWorkspaceAccess {
		t.Fatalf("unexpected agy read-only modes: %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.Permissions.Allow, []string{"read_file(/target)"}) {
		t.Fatalf("agy read-only allow: %#v", cfg.Permissions.Allow)
	}
	wantRODeny := []string{"command(*)", "write_file(*)", "read_url(*)", "execute_url(*)", "mcp(*)"}
	if !reflect.DeepEqual(cfg.Permissions.Deny, wantRODeny) {
		t.Fatalf("agy read-only deny: %#v", cfg.Permissions.Deny)
	}
}

func TestEnv(t *testing.T) {
	got := Env([]string{"A=B"}, "/w", "/t")
	if len(got) != 3 || got[1] != "CKB_WHITEHAT_WORKSPACE=/w" {
		t.Fatal(got)
	}
}

// TestSpawnObserverReceivesLinesInOrder: the observer sees stdout lines as
// they are emitted, in order, with newlines stripped.
func TestSpawnObserverReceivesLinesInOrder(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	var mu sync.Mutex
	var lines []string
	res, err := SpawnWithObserver(context.Background(), s,
		[]string{"-c", "echo a-line; sleep 0.1; echo b-line; sleep 0.1; echo c-line"}, nil, ".",
		60*time.Second, 60*time.Second,
		func(line string) {
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
		})
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 0 {
		t.Fatalf("want exit 0, got %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"a-line", "b-line", "c-line"}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %#v", len(lines), len(want), lines)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d = %q, want %q (all: %#v)", i, lines[i], want[i], lines)
		}
	}
}

// TestSpawnNoObserverUnchanged: Spawn (nil observer) still returns the full
// captured stdout and exit code.
func TestSpawnNoObserverUnchanged(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	res, err := Spawn(context.Background(), s,
		[]string{"-c", "echo plain"}, nil, ".",
		60*time.Second, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 0 || res.Stdout != "plain\n" || res.Stalled || res.TimedOut {
		t.Fatalf("want unchanged Spawn result {Code:0 Stdout:plain\n}, got %+v", res)
	}
}

// TestSpawnObserverFlushTrailingLine: a final line without a newline is
// still delivered to the observer after the child exits.
func TestSpawnObserverFlushTrailingLine(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	var mu sync.Mutex
	var lines []string
	observe := func(line string) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	}
	res, err := SpawnWithObserver(context.Background(), s,
		[]string{"-c", "printf 'tail-no-newline'"}, nil, ".",
		60*time.Second, 60*time.Second, observe)
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 0 {
		t.Fatalf("want exit 0, got %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 || lines[0] != "tail-no-newline" {
		t.Fatalf("want %#v, got %#v", []string{"tail-no-newline"}, lines)
	}
}

// TestSpawnStallKillsSilentChild: a child that never prints is killed at the
// stall timeout (spec §4.5).
func TestSpawnStallKillsSilentChild(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	res, err := Spawn(context.Background(), s,
		[]string{"-c", "sleep 300"}, nil, ".",
		2*time.Second, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Stalled || res.Code != 1 {
		t.Fatalf("want stalled, got %+v", res)
	}
}

// TestSpawnMaxRuntimeKills: the wall-clock cap fires regardless of output.
func TestSpawnMaxRuntimeKills(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	res, err := Spawn(context.Background(), s,
		[]string{"-c", "sleep 300"}, nil, ".",
		60*time.Second, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.Code != 1 {
		t.Fatalf("want timedOut, got %+v", res)
	}
}

// TestSpawnKillPathChattyChildNoRace: a chatty child is killed at the
// wall-clock boundary while a stdio-copy goroutine may be mid-Write, which
// is exactly the pair that raced in audit M1 (out.String() vs feeder.Write).
// The child is bounded (25s) so it can't leak, and max=1s guarantees the
// kill path with output still flowing. Run under `go test -race` to catch
// the String()/Write() race: without the drainDone + mutex guard this fails
// the race detector.
func TestSpawnKillPathChattyChildNoRace(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	res, err := Spawn(context.Background(), s,
		[]string{"-c", "i=0; while [ $i -lt 500 ]; do echo tick; i=$((i+1)); sleep 0.05; done"}, nil, ".",
		60*time.Second, 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.Code != 1 {
		t.Fatalf("want a killed chatty child (timedOut, code 1), got %+v", res)
	}
	if res.Stdout == "" {
		t.Fatal("expected captured output from the chatty child up to the kill")
	}
}

// TestSpawnStallResetsOnOutput: continuous output must keep re-arming the
// stall timer (the parity fix from the Node CLI — a chatty agent is not
// silent). The child emits a tick every 200ms for 4s; a 1s stall budget
// would kill it almost immediately unless the timer resets on each byte.
func TestSpawnStallResetsOnOutput(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	res, err := Spawn(context.Background(), s,
		[]string{"-c", "i=0; while [ $i -lt 20 ]; do echo tick; i=$((i+1)); sleep 0.2; done; echo done"}, nil, ".",
		1*time.Second, 60*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stalled || res.TimedOut || res.Code != 0 {
		t.Fatalf("chatty child must survive a shorter stall budget; got %+v", res)
	}
	if res.Stdout == "" {
		t.Fatal("expected captured output from the chatty child")
	}
}

// TestSpawnWithObserverClaudeStreamJSON verifies end-to-end streaming of Claude
// `--output-format stream-json` lines through SpawnWithObserver: the live
// observer receives concise tool activity and assistant text as it streams,
// while res.Stdout holds clean plain-text assistant output containing findings
// and the CKB-WHITEHAT-RESULT marker without raw JSON or tool-activity noise.
func TestSpawnWithObserverClaudeStreamJSON(t *testing.T) {
	s := Spec{Name: Claude, Bin: "/bin/sh", Base: []string{"-c"}}
	script := `
printf '%s\n' '{"type":"system","subtype":"init","session_id":"sess-1"}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"cargo test --nocapture"}}]}}'
sleep 0.05
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"FINDING F1 FIRED\nCKB-WHITEHAT-RESULT: {\"verdict\":\"vulnerable\"}"}]}}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"result":"FINDING F1 FIRED\nCKB-WHITEHAT-RESULT: {\"verdict\":\"vulnerable\"}"}'
`
	var mu sync.Mutex
	var lines []string
	res, err := SpawnWithObserver(context.Background(), s,
		[]string{"-c", script}, nil, ".",
		60*time.Second, 60*time.Second,
		func(line string) {
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
		})
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 0 || res.Stalled || res.TimedOut {
		t.Fatalf("want clean exit 0, got %+v", res)
	}
	if !strings.Contains(res.Stdout, "FINDING F1 FIRED") {
		t.Fatalf("res.Stdout missing FINDING F1 FIRED: %q", res.Stdout)
	}
	if !strings.Contains(res.Stdout, `CKB-WHITEHAT-RESULT: {"verdict":"vulnerable"}`) {
		t.Fatalf("res.Stdout missing CKB-WHITEHAT-RESULT marker: %q", res.Stdout)
	}
	if strings.Contains(res.Stdout, "→ Bash") || strings.Contains(res.Stdout, `"type":"system"`) {
		t.Fatalf("res.Stdout should contain clean prose only, got %q", res.Stdout)
	}
	if res.Activity != "→ Bash cargo test --nocapture\n" {
		t.Fatalf("res.Activity:\ngot:  %q\nwant: %q", res.Activity, "→ Bash cargo test --nocapture\n")
	}

	mu.Lock()
	defer mu.Unlock()
	wantLines := []string{
		"→ Bash cargo test --nocapture",
		"FINDING F1 FIRED",
		`CKB-WHITEHAT-RESULT: {"verdict":"vulnerable"}`,
	}
	if !reflect.DeepEqual(lines, wantLines) {
		t.Fatalf("observer lines:\ngot:  %#v\nwant: %#v", lines, wantLines)
	}
}

// TestSpawnWithObserverFallbackRawStdoutOnEmptyPlainText ensures that if an
// agent emits only protocol metadata and crashes before producing assistant
// text, Result.Stdout falls back to raw stdout so crash diagnostics are kept.
func TestSpawnWithObserverFallbackRawStdoutOnEmptyPlainText(t *testing.T) {
	s := Spec{Name: Claude, Bin: "/bin/sh", Base: []string{"-c"}}
	script := `printf '%s\n' '{"type":"system","subtype":"init"}'; exit 1`
	res, err := SpawnWithObserver(context.Background(), s,
		[]string{"-c", script}, nil, ".",
		60*time.Second, 60*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 1 {
		t.Fatalf("want exit 1, got %+v", res)
	}
	if !strings.Contains(res.Stdout, `"type":"system"`) {
		t.Fatalf("expected raw stdout fallback when PlainText is empty, got %q", res.Stdout)
	}
}

// TestSpawnClosesStdin verifies that SpawnWithObserver closes child stdin so
// an agent CLI can never hang waiting for interactive terminal input.
func TestSpawnClosesStdin(t *testing.T) {
	s := Spec{Name: "sh", Bin: "/bin/sh", Base: []string{"-c"}}
	res, err := Spawn(context.Background(), s,
		[]string{"-c", "cat; echo eof-ok"}, nil, ".",
		5*time.Second, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != 0 || res.Stalled || res.TimedOut || strings.TrimSpace(res.Stdout) != "eof-ok" {
		t.Fatalf("expected immediate stdin EOF and eof-ok, got %+v", res)
	}
}
