package workspace

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// twoDirs returns two (well, three) temp dirs to chdir between and a Manager
// rooted in a fourth, so tests can assert CWD-independence of URL slugs.
func twoDirs(t *testing.T) ([]string, Manager) {
	t.Helper()
	dirs := make([]string, 3)
	for i := range dirs {
		dirs[i] = t.TempDir()
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	return dirs, New(t.TempDir())
}

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

func TestRepoDirURLIsCWDIndependent(t *testing.T) {
	dirs, m := twoDirs(t)
	if err := os.Chdir(dirs[0]); err != nil {
		t.Fatal(err)
	}
	a := m.RepoDir("https://github.com/duongja/Dular")
	if err := os.Chdir(dirs[1]); err != nil {
		t.Fatal(err)
	}
	b := m.RepoDir("https://github.com/duongja/Dular")
	if a != b {
		t.Fatalf("RepoDir URL slug depends on CWD: %q vs %q", a, b)
	}
	for _, d := range dirs {
		if strings.Contains(a, filepath.Base(d)) {
			t.Fatalf("repo dir %q embeds a CWD component %q", a, filepath.Base(d))
		}
	}
	if err := os.Chdir(dirs[2]); err != nil {
		t.Fatal(err)
	}
}

func TestRepoDirURLCollision(t *testing.T) {
	_, m := twoDirs(t)
	a := m.RepoDir("github.com/orgA/Dular")
	b := m.RepoDir("github.com/orgB/Dular")
	c := m.RepoDir("gitlab.com/orgA/Dular")
	if a == b || a == c || b == c {
		t.Fatalf("URL slugs must be distinct: %q %q %q", a, b, c)
	}
	if !regexp.MustCompile(`-[0-9a-f]{6}$`).MatchString(a) {
		t.Fatalf("expected 6-hex collision suffix, got %q", a)
	}
}

func TestRepoDirLocalPathUnchanged(t *testing.T) {
	_, m := twoDirs(t)
	parent := t.TempDir()
	local := filepath.Join(parent, "target")
	if err := os.MkdirAll(local, 0755); err != nil {
		t.Fatal(err)
	}
	if got := m.RepoDir(local); got != filepath.Join(m.root(), "repos", Slug(local)) {
		t.Fatalf("local-path repo dir changed: %q", got)
	}
	// the slug still embeds the absolute-path components, so existing
	// local workspaces (e.g. a dular clone under a fixed home) keep their dirs
	slug := Slug(local)
	if !strings.Contains(slug, filepath.Base(parent)) || !strings.Contains(slug, "target") {
		t.Fatalf("local slug %q lost absolute-path components of %q", slug, local)
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
