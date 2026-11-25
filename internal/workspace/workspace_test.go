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
	slug := filepath.Base(a)
	for _, d := range dirs {
		if strings.Contains(slug, filepath.Base(d)) {
			t.Fatalf("repo dir slug %q (%q) embeds a CWD component %q", slug, a, filepath.Base(d))
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

func TestRepoDirURLNormalization(t *testing.T) {
	_, m := twoDirs(t)
	variants := []string{
		"https://github.com/duongja/Dular",
		"https://github.com/duongja/Dular/",
		"https://github.com/duongja/Dular.git",
		"https://github.com/duongja/Dular.git/",
	}
	want := m.RepoDir(variants[0])
	for _, v := range variants[1:] {
		if got := m.RepoDir(v); got != want {
			t.Fatalf("RepoDir(%q) = %q, want %q", v, got, want)
		}
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

func TestIsManagedClone(t *testing.T) {
	h := t.TempDir()
	m := New(h)
	managed := m.RepoDir("https://github.com/duongja/Dular")
	if !m.IsManagedClone(managed) {
		t.Fatalf("expected %q to be a managed clone", managed)
	}
	external := filepath.Join(h, "src", "Dular")
	if m.IsManagedClone(external) {
		t.Fatalf("expected %q NOT to be a managed clone", external)
	}
	if m.IsManagedClone(filepath.Join(m.root(), "repos")) {
		t.Fatal("repos root itself must not count as a managed clone")
	}
}

func TestCleanWorkspacesPreservesChainDir(t *testing.T) {
	h := t.TempDir()
	m := New(h)
	chainFile := filepath.Join(m.root(), "chain", "contracts", "funding-lock")
	if err := os.MkdirAll(filepath.Dir(chainFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chainFile, []byte("contract"), 0644); err != nil {
		t.Fatal(err)
	}
	wsFile := filepath.Join(m.root(), "some-workspace", "FINDINGS.md")
	if err := os.MkdirAll(filepath.Dir(wsFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wsFile, []byte("report"), 0644); err != nil {
		t.Fatal(err)
	}
	repoFile := filepath.Join(m.root(), "repos", "demo", "README.md")
	if err := os.MkdirAll(filepath.Dir(repoFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repoFile, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := m.Clean(true); err != nil {
		t.Fatalf("Clean(true): %v", err)
	}
	if _, err := os.Stat(chainFile); err != nil {
		t.Fatalf("Clean(true) must preserve ~/.ckb-whitehat/chain/, got err=%v", err)
	}
	if _, err := os.Stat(wsFile); !os.IsNotExist(err) {
		t.Fatalf("Clean(true) should remove workspace dir, stat err=%v", err)
	}
	if _, err := os.Stat(repoFile); !os.IsNotExist(err) {
		t.Fatalf("Clean(true) should remove repos dir, stat err=%v", err)
	}
}
