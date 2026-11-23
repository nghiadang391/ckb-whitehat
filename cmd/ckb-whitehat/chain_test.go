package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nghiadang391/ckb-whitehat/internal/setup"
	"github.com/nghiadang391/ckb-whitehat/internal/workspace"
)

func TestEffectiveAllowedToolsAddsCargoForRustTarget(t *testing.T) {
	rustDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(rustDir, "Cargo.toml"), []byte("[package]\nname=\"lock\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := effectiveAllowedTools(nil, rustDir, nil)
	if !contains(got, "Bash(cargo:*)") {
		t.Fatalf("expected Bash(cargo:*) for Rust target, got %v", got)
	}
	for _, forbidden := range []string{"curl", "wget", "psql", "env"} {
		for _, tool := range got {
			if strings.Contains(tool, forbidden) {
				t.Fatalf("forbidden tool %q leaked into Rust allowlist: %v", forbidden, got)
			}
		}
	}

	// Non-Rust target must NOT include Bash(cargo:*).
	jsDir := t.TempDir()
	gotJS := effectiveAllowedTools(nil, jsDir, nil)
	if contains(gotJS, "Bash(cargo:*)") {
		t.Fatalf("non-Rust target should not include Bash(cargo:*): %v", gotJS)
	}

	// Explicit --allowed-tools overrides everything verbatim.
	explicit := effectiveAllowedTools([]string{"--allowed-tools=Read,Write"}, rustDir, nil)
	if len(explicit) != 2 || contains(explicit, "Bash(cargo:*)") {
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
