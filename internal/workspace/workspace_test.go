package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSlugStableAndDistinct(t *testing.T) {
	a := Slug("/tmp/a/app")
	b := Slug("/tmp/b/app")
	if a == b || a == "" || b == "" {
		t.Fatalf("slugs must distinguish targets: %q %q", a, b)
	}
	if got := Slug("///"); got != "" {
		t.Fatalf("root-like path slug = %q", got)
	}
}

func TestURLDetection(t *testing.T) {
	for _, s := range []string{"https://github.com/a/b", "git@github.com:a/b.git", "ssh://git@example/a"} {
		if !IsURL(s) {
			t.Errorf("expected URL: %s", s)
		}
	}
	if IsURL("./repo") {
		t.Error("local path detected as URL")
	}
}

func TestWorkspaceLegacyFallback(t *testing.T) {
	h := t.TempDir()
	m := New(h)
	target := filepath.Join(h, "src", "app")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	legacy := m.LegacyWorkspace(target)
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	if got := m.Workspace(target); got != legacy {
		t.Fatalf("got %q want legacy %q", got, legacy)
	}
}

func TestInitConfig(t *testing.T) {
	h := t.TempDir()
	target := filepath.Join(h, "repo")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	m := New(filepath.Join(h, "home"))
	ws, cfg, err := m.Init(target, []string{"app, chain"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Onchain || len(cfg.Scope) != 2 || ws == "" {
		t.Fatalf("bad config: %#v", cfg)
	}
	if _, err := os.Stat(filepath.Join(ws, "poc")); err != nil {
		t.Fatal(err)
	}
}
