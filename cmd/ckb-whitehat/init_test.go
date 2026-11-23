package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nghiadang391/ckb-whitehat/internal/setup"
	"github.com/nghiadang391/ckb-whitehat/internal/workspace"
)

func TestInitFailsFastWithoutAgentCLI(t *testing.T) {
	home := t.TempDir()
	emptyBin := t.TempDir()
	target := t.TempDir()

	// PATH has no claude or codex binary -> init must fail fast with exit 2.
	code, out := runBin(t, map[string]string{"HOME": home, "PATH": emptyBin}, "init", target)
	if code != 2 {
		t.Fatalf("init without agent CLI: code=%d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "no agent CLI found") {
		t.Fatalf("expected missing agent CLI error, got:\n%s", out)
	}
}

func TestInitReadOnlyAgentSavesSetupAndFeedsRunPrompt(t *testing.T) {
	home := t.TempDir()
	fakebin := t.TempDir()
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "package.json"), []byte(`{"name":"demo"}`), 0644); err != nil {
		t.Fatal(err)
	}

	argsLog := filepath.Join(t.TempDir(), "claude-args.txt")
	fake := filepath.Join(fakebin, "claude")
	script := `#!/bin/sh
printf '%s\n' "$*" > "` + argsLog + `"
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"package.json"}}]}}'
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"CKB-WHITEHAT-INIT: {\"stacks\":[\"node\",\"postgres\"],\"summary\":\"Demo app\",\"tools\":[{\"name\":\"node\",\"reason\":\"PoCs\"},{\"name\":\"definitely-not-installed-xyz\",\"reason\":\"demo check\",\"install\":\"brew install xyz\"}],\"services\":[{\"name\":\"redis\",\"reason\":\"cache\",\"hint\":\"redis-server\"}],\"commands\":[{\"command\":\"npm ci --ignore-scripts\",\"dir\":\".\",\"reason\":\"install demo deps\"}],\"notes\":[\"pure JS dependencies\"]}"}]}}'
`
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"HOME": home, "PATH": fakebin + string(os.PathListSeparator) + os.Getenv("PATH")}
	code, out := runBin(t, env, "init", target, "--agent", "claude")
	if code != 0 {
		t.Fatalf("init code=%d out=%s", code, out)
	}

	// Verify read-only tools were passed in CLI flags (before "--" prompt separator).
	argsBytes, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("reading captured claude args: %v", err)
	}
	flagsPart := strings.SplitN(string(argsBytes), " -- ", 2)[0]
	if !strings.Contains(flagsPart, "--allowedTools Read,Glob,Grep,LS") {
		t.Fatalf("init must pass strictly read-only tools, got flags: %q", flagsPart)
	}
	if strings.Contains(flagsPart, "Bash") || strings.Contains(flagsPart, "Write") || strings.Contains(flagsPart, "Edit") {
		t.Fatalf("init flags must NOT allow Bash, Write, or Edit, got flags: %q", flagsPart)
	}

	// Verify output reports detected stack, missing tool, service hint, and skips
	// execution in external local dir / non-TTY safely.
	for _, want := range []string{
		"stack     node, postgres",
		"missing   definitely-not-installed-xyz",
		"service   redis",
		"npm ci --ignore-scripts",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("init output missing %q:\n%s", want, out)
		}
	}

	// Verify setup.json was persisted and feeds into run's buildPrompt.
	ws := workspace.New(home).Workspace(target)
	rec, err := setup.LoadRecord(ws)
	if err != nil || rec == nil {
		t.Fatalf("setup.json not loaded: rec=%+v err=%v", rec, err)
	}
	if len(rec.Outcomes) != 1 || rec.Outcomes[0].Status != "skipped" {
		t.Fatalf("external local dir without --install-deps should skip command, got %+v", rec.Outcomes)
	}

	prompt := buildPrompt(ws, workspace.Config{Target: target, Scope: []string{"app"}}, false, nil, nil, "", false)
	for _, want := range []string{
		"Init environment inspection (from setup.json):",
		"detected stack: node, postgres",
		"missing host tools:",
		"definitely-not-installed-xyz",
		"setup command [skipped] (dir=.): npm ci --ignore-scripts",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("run buildPrompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestInitNonTTYManagedCloneAutoDeniesCommands(t *testing.T) {
	home := t.TempDir()
	fakebin := t.TempDir()
	m := workspace.New(home)
	managed := m.RepoDir("https://github.com/example/managed")
	if err := os.MkdirAll(managed, 0755); err != nil {
		t.Fatal(err)
	}

	fake := filepath.Join(fakebin, "claude")
	const script = `#!/bin/sh
printf '%s\n' 'CKB-WHITEHAT-INIT: {"stacks":["node"],"commands":[{"command":"npm ci --ignore-scripts","dir":".","reason":"install deps"}]}'
`
	if err := os.WriteFile(fake, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"HOME": home, "PATH": fakebin + string(os.PathListSeparator) + os.Getenv("PATH")}
	code, out := runBin(t, env, "init", managed, "--agent", "claude")
	if code != 0 {
		t.Fatalf("init code=%d out=%s", code, out)
	}
	if !strings.Contains(out, "auto-denied") {
		t.Fatalf("non-TTY managed clone must auto-deny suggested commands, got:\n%s", out)
	}
	ws := m.Workspace(managed)
	rec, err := setup.LoadRecord(ws)
	if err != nil || rec == nil || len(rec.Outcomes) != 1 || rec.Outcomes[0].Status != "denied" {
		t.Fatalf("expected denied outcome in setup.json, got rec=%+v err=%v", rec, err)
	}

	// --no-install-deps must mark commands skipped even on a managed clone.
	code, out = runBin(t, env, "init", managed, "--agent", "claude", "--no-install-deps")
	if code != 0 {
		t.Fatalf("init --no-install-deps code=%d out=%s", code, out)
	}
	rec, err = setup.LoadRecord(ws)
	if err != nil || rec == nil || len(rec.Outcomes) != 1 || rec.Outcomes[0].Status != "skipped" {
		t.Fatalf("expected skipped outcome with --no-install-deps, got rec=%+v err=%v", rec, err)
	}
}

func TestLivePanelInitCommandPromptFitsViewport(t *testing.T) {
	p := NewLivePanel("init · claude · dular", 0, 120)
	p.SetStages(initPipelineStages)
	p.SetStage("confirm")
	p.SetSize(110, 26)
	p.SetCommandPrompt("Setup Command (1/2)", []promptLine{
		{text: "$ npm ci --ignore-scripts", tone: "cyan"},
		{text: "dir: .", tone: "dim"},
		{text: "why: install server deps", tone: ""},
		{text: "⚠ missing --ignore-scripts: \x1b[31mattack\x1b[0m", tone: "yellow"},
	}, renderSetupActionBar(110))

	rendered := p.Render()
	plain := stripANSI(rendered)
	for _, want := range []string{"Setup Command (1/2)", "npm ci --ignore-scripts", "approve", "deny", "skip all", "Inspect", "Setup"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered init panel missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "\x1b") {
		t.Fatalf("unsanitized escape leaked into panel: %q", plain)
	}
	for i, line := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n") {
		if w := displayWidth(line); w > 110 {
			t.Fatalf("line %d exceeded 110 cols (%d): %q", i, w, stripANSI(line))
		}
	}
}

func TestAutoDetectOrderClaudeThenAgyThenCodex(t *testing.T) {
	t.Run("prefers claude over agy", func(t *testing.T) {
		home := t.TempDir()
		fakebin := t.TempDir()
		target := t.TempDir()

		fakeClaude := filepath.Join(fakebin, "claude")
		const claudeScript = `#!/bin/sh
printf '%s\n' '{"type":"assistant","message":{"content":[{"type":"text","text":"CKB-WHITEHAT-INIT: {\"stacks\":[\"node\"],\"summary\":\"Claude inspected\"}"}]}}'
`
		if err := os.WriteFile(fakeClaude, []byte(claudeScript), 0755); err != nil {
			t.Fatal(err)
		}
		fakeAgy := filepath.Join(fakebin, "agy")
		if err := os.WriteFile(fakeAgy, []byte("#!/bin/sh\necho 'WRONG_AGENT_AGY' >&2; exit 99\n"), 0755); err != nil {
			t.Fatal(err)
		}

		env := map[string]string{"HOME": home, "PATH": fakebin + string(os.PathListSeparator) + os.Getenv("PATH")}
		code, out := runBin(t, env, "init", target)
		if code != 0 {
			t.Fatalf("init with claude+agy on PATH failed: code=%d\n%s", code, out)
		}
		if !strings.Contains(out, "agent  : claude (read-only inspection)") {
			t.Fatalf("expected auto-detection to pick claude over agy, got:\n%s", out)
		}
		if !strings.Contains(out, "summary   Claude inspected") {
			t.Fatalf("expected claude init plan parsed, got:\n%s", out)
		}
	})

	t.Run("prefers agy over codex and streams agy init", func(t *testing.T) {
		home := t.TempDir()
		fakebin := t.TempDir()
		target := t.TempDir()

		// Isolated PATH with only fake agy + fake codex (plus /usr/bin:/bin for sh/printf,
		// ensuring host claude is NOT on PATH).
		fakeCodex := filepath.Join(fakebin, "codex")
		if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\necho 'WRONG_AGENT_CODEX' >&2; exit 99\n"), 0755); err != nil {
			t.Fatal(err)
		}
		fakeAgy := filepath.Join(fakebin, "agy")
		const agyScript = `#!/bin/sh
printf '%s\n' '{"event":"init","conversation_id":"c1","init":{"permission_mode":"request-review"}}'
printf '%s\n' '{"event":"step_update","step_update":{"step_index":1,"state":"ACTIVE","step_type":"tool","tool_name":"read_file","tool_info":{"name":"read_file","parameters":{"Path":"package.json"}}}}'
printf '%s\n' '{"event":"step_update","step_update":{"step_index":2,"state":"DONE","step_type":"agent_response","text_delta":"CKB-WHITEHAT-INIT: {\"stacks\":[\"node\"],\"summary\":\"Agy inspected\"}\n"}}'
printf '%s\n' '{"event":"result","result":{"status":"SUCCESS","response":"CKB-WHITEHAT-INIT: {\"stacks\":[\"node\"],\"summary\":\"Agy inspected\"}\n"}}'
`
		if err := os.WriteFile(fakeAgy, []byte(agyScript), 0755); err != nil {
			t.Fatal(err)
		}

		env := map[string]string{"HOME": home, "PATH": fakebin + ":/usr/bin:/bin"}
		code, out := runBin(t, env, "init", target)
		if code != 0 {
			t.Fatalf("init with agy+codex on PATH failed: code=%d\n%s", code, out)
		}
		if !strings.Contains(out, "agent  : agy (read-only inspection)") {
			t.Fatalf("expected auto-detection to pick agy over codex, got:\n%s", out)
		}
		if !strings.Contains(out, "summary   Agy inspected") {
			t.Fatalf("expected agy stream-json init plan parsed, got:\n%s", out)
		}
	})
}
