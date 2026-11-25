package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nghiadang391/ckb-whitehat/internal/chain"
	"github.com/nghiadang391/ckb-whitehat/internal/setup"
	"github.com/nghiadang391/ckb-whitehat/internal/workspace"
)

func TestEffectiveAllowedToolsBroadDefaultAndExplicitOverride(t *testing.T) {
	rustDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rustDir, "Cargo.toml"), []byte("[package]\nname=\"lock\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := effectiveAllowedTools(nil, rustDir, nil)
	if !contains(got, "Bash(*)") {
		t.Fatalf("expected broad Bash(*) in default effectiveAllowedTools, got %v", got)
	}

	// Explicit --allowed-tools overrides everything verbatim.
	explicit := effectiveAllowedTools([]string{"--allowed-tools=Read,Write"}, rustDir, nil)
	if len(explicit) != 2 || contains(explicit, "Bash(*)") {
		t.Fatalf("explicit --allowed-tools must not be modified: %v", explicit)
	}
}

func TestDetectCKBFiberStackAndRustPromptGuidance(t *testing.T) {
	rec := &setup.Record{
		Plan: setup.Plan{
			Stacks: []string{"rust", "ckb-script", "fiber"},
		},
	}
	if !detectCKBFiberStack(rec) {
		t.Fatal("expected CKB/Fiber stack detected")
	}
	ws := t.TempDir()
	if err := setup.SaveRecord(ws, *rec); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	prompt := buildPrompt(ws, workspace.Config{Target: target, Scope: []string{"app"}}, false, nil, nil, "", false)
	if !strings.Contains(prompt, "cargo test") {
		t.Fatalf("expected Rust cargo test guidance in prompt:\n%s", prompt)
	}
}

func TestChainUsageExitsTwo(t *testing.T) {
	code, out := runBin(t, nil, "chain", "unknown")
	if code != 2 || !strings.Contains(out, "usage: ckb-whitehat chain") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestNeedsPostgresAndWithEnvVar(t *testing.T) {
	dir := t.TempDir()
	if needsPostgres(dir, nil) {
		t.Fatal("empty dir should not need Postgres")
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"pg":"^8.0.0"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if !needsPostgres(dir, nil) {
		t.Fatal("package.json with pg dep should need Postgres")
	}
	if !needsPostgres(t.TempDir(), &setup.Record{Plan: setup.Plan{Stacks: []string{"node", "postgres"}}}) {
		t.Fatal("setup.json with postgres stack should need Postgres")
	}

	env := withEnvVar([]string{"A=1", "DATABASE_URL=old"}, "DATABASE_URL", "postgres://whitehat:whitehat@127.0.0.1:5439/whitehat")
	if len(env) != 2 || env[1] != "DATABASE_URL=postgres://whitehat:whitehat@127.0.0.1:5439/whitehat" {
		t.Fatalf("withEnvVar replace failed: %v", env)
	}
	env = withEnvVar([]string{"A=1"}, "DATABASE_URL", "postgres://new")
	if len(env) != 2 || env[1] != "DATABASE_URL=postgres://new" {
		t.Fatalf("withEnvVar append failed: %v", env)
	}
}

func TestRunChainEndpointsPrefersLiveManifestOverStaleEnv(t *testing.T) {
	t.Setenv("CKB_RPC_URL", "http://127.0.0.1:9999")
	t.Setenv("FIBER_RPC_URL", "http://127.0.0.1:9998")
	m := &chain.Manifest{
		ChainHash: "0x1111111111111111111111111111111111111111111111111111111111111111",
		CKBRPCURL: "http://127.0.0.1:8114",
		NodeA:     chain.NodeEndpoint{RPCURL: "http://127.0.0.1:8227"},
		NodeB:     chain.NodeEndpoint{RPCURL: "http://127.0.0.1:8327"},
		CKBChannel: &chain.ChannelSummary{
			ChannelID:     "0x2222222222222222222222222222222222222222222222222222222222222222",
			FundingTxHash: "0x3333333333333333333333333333333333333333333333333333333333333333",
		},
		UDT: &chain.UDTConfig{
			Script: chain.Script{
				CodeHash: "0x4444444444444444444444444444444444444444444444444444444444444444",
				Args:     "0x5555555555555555555555555555555555555555555555555555555555555555",
			},
		},
	}
	ckbURL, fnnURLs := runChainEndpoints(m)
	if ckbURL != "http://127.0.0.1:8114" || len(fnnURLs) != 2 || fnnURLs[0] != "http://127.0.0.1:8227" || fnnURLs[1] != "http://127.0.0.1:8327" {
		t.Fatalf("runChainEndpoints should prefer managed manifest over stale env: ckb=%q fnn=%v", ckbURL, fnnURLs)
	}
	ignore, bootstrap := manifestVerificationHashes(m)
	if len(ignore) != 3 || len(bootstrap) != 2 {
		t.Fatalf("manifestVerificationHashes unexpected: ignore=%v bootstrap=%v", ignore, bootstrap)
	}
}
