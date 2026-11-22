package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMain builds the binary once; exit-code tests exec it.
var binPath string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "ckb-whitehat-bin")
	if err != nil {
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)
	binPath = filepath.Join(tmp, "ckb-whitehat")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// --- flag parsing rules (spec §5.2) ----------------------------------------

func TestFlagValEqualsForm(t *testing.T) {
	v, ok := flagVal([]string{"--target=https://x/y"}, "target")
	if !ok || v != "https://x/y" {
		t.Fatalf("got %q ok=%v", v, ok)
	}
}

func TestFlagValSpaceFormAndValueConsumption(t *testing.T) {
	// --target consumes the next arg even when it starts with '-'? No: value
	// must not look like a flag.
	if v, ok := flagVal([]string{"--target", "/a/b"}, "target"); !ok || v != "/a/b" {
		t.Fatalf("got %q ok=%v", v, ok)
	}
	// a bare --allowed-tools with no value is "present, empty".
	if v, ok := flagVal([]string{"--allowed-tools"}, "allowed-tools"); ok != true || v != "" {
		t.Fatalf("bare flag must be present+empty, got %q ok=%v", v, ok)
	}
	if _, ok := flagVal([]string{"--agent", "claude"}, "scope"); ok {
		t.Fatal("scope must be absent")
	}
}

func TestPositionalSkipsValueFlags(t *testing.T) {
	// "run /a/b --scope app" → positional /a/b
	if got := positional([]string{"/a/b", "--scope", "app"}); got != "/a/b" {
		t.Fatalf("got %q", got)
	}
	// the value of --scope must not be reported as a positional
	if got := positional([]string{"--scope", "app"}); got != "" {
		t.Fatalf("flag value leaked as positional: %q", got)
	}
	if got := positional([]string{"--target=/a/b"}); got != "" {
		t.Fatalf("equals-form flag leaked as positional: %q", got)
	}
	if got := positional([]string{"--agent", "claude", "x"}); got != "x" {
		t.Fatalf("got %q", got)
	}
}

func TestPositionalsCollectsAll(t *testing.T) {
	got := positionals([]string{"a.md", "--scope", "app", "b.md"})
	if len(got) != 2 || got[0] != "a.md" || got[1] != "b.md" {
		t.Fatalf("got %v", got)
	}
}

// Node parity: a bare --allowed-tools (present, no value) keeps the default
// list; --allowed-tools= (empty value) allows nothing, like Node's
// String("").split(",").filter(Boolean); an explicit value overrides.
func TestAllowedToolsBareFlagKeepsDefault(t *testing.T) {
	got := allowedToolsList([]string{"--allowed-tools"})
	if len(got) != len(defaultAllowedTools) || got[0] != defaultAllowedTools[0] {
		t.Fatalf("bare --allowed-tools must keep the default list, got %v", got)
	}
	if got := allowedToolsList([]string{"--allowed-tools=Bash(node:*)"}); len(got) != 1 || got[0] != "Bash(node:*)" {
		t.Fatalf("explicit value must override, got %v", got)
	}
	if got := allowedToolsList([]string{"--allowed-tools="}); len(got) != 0 {
		t.Fatalf("--allowed-tools= must allow nothing, got %v", got)
	}
	// space form: Node flag() returns the next single arg as the value, so
	// the list is that one arg split on commas. The following arg is a
	// separate positional, not a list member.
	if got := allowedToolsList([]string{"--allowed-tools", "Bash(node:*),Read"}); len(got) != 2 || got[0] != "Bash(node:*)" || got[1] != "Read" {
		t.Fatalf("space form must split the single value on commas, got %v", got)
	}
}

func TestDefaultAllowedToolsSafeOfflineSet(t *testing.T) {
	for _, want := range []string{
		"Bash(node:*)",
		"Bash(pg_isready:*)",
		"Bash(git:*)",
		"Bash(grep:*)",
		"Bash(ls:*)",
		"Bash(mkdir:*)",
		"Read",
		"Write",
		"Edit",
		"Grep",
		"Glob",
		"LS",
	} {
		if !contains(defaultAllowedTools, want) {
			t.Errorf("defaultAllowedTools missing %q: %v", want, defaultAllowedTools)
		}
	}
	for _, forbidden := range []string{"curl", "wget", "psql"} {
		for _, tool := range defaultAllowedTools {
			if strContains(tool, forbidden) {
				t.Errorf("defaultAllowedTools must exclude %q, found %q", forbidden, tool)
			}
		}
	}
}

func TestHasFlag(t *testing.T) {
	if !hasFlag([]string{"--yolo"}, "yolo") || !hasFlag([]string{"--fresh"}, "fresh") {
		t.Fatal("bare flag not detected")
	}
	if !hasFlag([]string{"--fresh=true"}, "fresh") {
		t.Fatal("equals-form flag must count as present (Node parity)")
	}
	if hasFlag([]string{"--fresh"}, "stale") {
		t.Fatal("absent flag detected")
	}
}

// --- exit-code matrix, commands that need no agent --------------------------

func runBin(t *testing.T, env map[string]string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Env = append(os.Environ(), envKVs(env)...)
	var out string
	b, _ := cmd.CombinedOutput()
	out = string(b)
	if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
		code := cmd.ProcessState.ExitCode()
		return code, out
	}
	return 0, out
}

func envKVs(env map[string]string) []string {
	var out []string
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

func TestVersionExitsZero(t *testing.T) {
	code, out := runBin(t, nil, "version")
	if code != 0 || out != version+"\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestUnknownCommandExitsTwo(t *testing.T) {
	code, _ := runBin(t, nil, "frobnicate")
	if code != 2 {
		t.Fatalf("code=%d, want 2", code)
	}
}

func TestCompareUsageExitsTwo(t *testing.T) {
	code, _ := runBin(t, nil, "compare", "only-one.md")
	if code != 2 {
		t.Fatalf("code=%d, want 2", code)
	}
}

func TestCompareReportsAndExitsZero(t *testing.T) {
	tmp := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := write("base.md", "## F1 — forged callback (HIGH)\n## At a glance\n## On-chain evidence\n## Layer boundary\n## Honest limitations\n")
	newR := write("new.md", "## F1 — forged callback (HIGH)\n## S1 — new thing (MEDIUM)\n## At a glance\n## On-chain evidence\n## Layer boundary\n## Honest limitations\n")
	code, out := runBin(t, nil, "compare", newR, base)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out)
	}
	for _, want := range []string{"REDISCOVERED (1/1)", "NEW in the new run", "coverage: 1/1"} {
		if !strContains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func strContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- init → run with a fake agent (fake "claude" emits a fired finding) -----

func TestRunFakeAgentFiringExitsOne(t *testing.T) {
	home := t.TempDir()
	fakebin := t.TempDir()
	target := t.TempDir() // a local non-git target is fine

	fake := filepath.Join(fakebin, "claude")
	const script = `#!/bin/sh
ws="$CKB_WHITEHAT_WORKSPACE"
mkdir -p "$ws/poc"
echo 'console.log(1)' > "$ws/poc/f1.js"
cat > "$ws/FINDINGS.md" <<'EOF'
# ckb-whitehat — fake
Target: fake repo. No network.

## At a glance
| # | Finding | Sev | Status |
|---|---|---|---|
| **F1** | forged callback | HIGH | PROVEN (fired) |

## F1 — forged callback (HIGH) — PROVEN
PoC output:
FINDING F1 FIRED
Impact: ledger credit with zero vendor calls.
Patch: verify signature.

## On-chain evidence
N/A — no on-chain interaction.

## Layer boundary
n/a

## Honest limitations
n/a
EOF
echo "FINDING F1 FIRED"
echo "CKB-WHITEHAT-RESULT: findings=1 fired=1 report=$ws/FINDINGS.md"
`
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	// init
	code, out := runBin(t, map[string]string{"HOME": home, "PATH": fakebin + string(os.PathListSeparator) + os.Getenv("PATH")},
		"init", target, "--scope", "app")
	if code != 0 {
		t.Fatalf("init code=%d out=%s", code, out)
	}

	// run: a run that found something is non-zero, by design (spec §3).
	code, out = runBin(t, map[string]string{"HOME": home, "PATH": fakebin + string(os.PathListSeparator) + os.Getenv("PATH")},
		"run", target, "--agent", "claude", "--stall-timeout", "60")
	if code != 1 {
		t.Fatalf("run code=%d, want 1 (finding fired):\n%s", code, out)
	}
	// The marker check passed and the summary line is present. We don't assert
	// the exact "ok" padding is a display detail; the marker and summary
	// lines are the behavioral contract.
	for _, want := range []string{"agent printed the CKB-WHITEHAT-RESULT: marker", "findings=1 fired=1"} {
		if !strContains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRunFakeAgentCleanExitsZero(t *testing.T) {
	home := t.TempDir()
	fakebin := t.TempDir()
	target := t.TempDir()

	fake := filepath.Join(fakebin, "claude")
	const script = `#!/bin/sh
ws="$CKB_WHITEHAT_WORKSPACE"
cat > "$ws/FINDINGS.md" <<'EOF'
# ckb-whitehat — fake-clean
Target: fake repo (local, non-git). No network, no live service, vendors stubbed.

## At a glance
No findings. Triage covered keygen/storage, amount parsing, signing, the
network/vendor trust boundary, the balance/ledger, and secrets-in-config;
none produced a hypothesis with a deterministic offline PoC that terminates
in observable state. All money invariants held under the fuzz corpus.

## On-chain evidence
N/A — target has no on-chain/persistent-asset interaction; findings would
terminate in code state, not txs.

## Layer boundary
we tested how the target uses the chain, not the chain protocol itself.

## Honest limitations
- static-only pass: no PoC was needed, so no exploit was executed
- fuzz corpus was small; a larger corpus might surface edge-case parsing bugs
EOF
echo "CKB-WHITEHAT-RESULT: findings=0 fired=0 report=$ws/FINDINGS.md"
`
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	runBin(t, map[string]string{"HOME": home}, "init", target)
	code, out := runBin(t, map[string]string{"HOME": home, "PATH": fakebin + string(os.PathListSeparator) + os.Getenv("PATH")},
		"run", target, "--agent", "claude", "--stall-timeout", "60")
	if code != 0 {
		t.Fatalf("clean run code=%d, want 0:\n%s", code, out)
	}
}
