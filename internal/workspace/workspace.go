package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const Version = "0.1.0"

type Config struct {
	Version       string   `json:"version"`
	Target        string   `json:"target"`
	CreatedAt     string   `json:"created_at"`
	Scope         []string `json:"scope"`
	Onchain       bool     `json:"onchain"`
	Services      []string `json:"services"`
	TargetOrigin  string   `json:"target_origin,omitempty"`
	CloneURL      string   `json:"clone_url,omitempty"`
	Commit        string   `json:"commit,omitempty"`
	DepsInstalled bool     `json:"deps_installed,omitempty"`
}

type Manager struct{ Home string }

func New(home string) Manager {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return Manager{Home: home}
}

func IsURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "ssh://")
}

func Slug(target string) string {
	resolved, err := filepath.Abs(target)
	if err == nil {
		target = resolved
	}
	target = strings.ToLower(target)
	var b strings.Builder
	lastDash := false
	for _, r := range target {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func (m Manager) root() string { return filepath.Join(m.Home, ".ckb-whitehat") }
func (m Manager) RepoDir(url string) string {
	s := strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
	base := filepath.Base(s)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "repo"
	}
	slug := Slug(base)
	if slug == "" {
		slug = "repo"
	}
	return filepath.Join(m.root(), "repos", slug)
}
func (m Manager) LegacyWorkspace(target string) string {
	return filepath.Join(m.root(), filepath.Base(target))
}
func (m Manager) Workspace(target string) string {
	resolved, err := filepath.Abs(target)
	if err == nil {
		target = resolved
	}
	dir := filepath.Join(m.root(), Slug(target))
	if Slug(target) == "" {
		dir = filepath.Join(m.root(), "root")
	}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		legacy := m.LegacyWorkspace(target)
		if _, e := os.Stat(legacy); e == nil {
			return legacy
		}
	}
	return dir
}

// ResolveOpts carries the URL-target options for a single resolution.
type ResolveOpts struct {
	Fresh   bool
	Depth   int
	Install bool
}

func (m Manager) ResolveTarget(spec string, fresh bool, depth int, install bool) (string, Config, error) {
	return m.ResolveTargetOpts(spec, ResolveOpts{Fresh: fresh, Depth: depth, Install: install})
}

// ResolveTargetOpts resolves a target spec (local path or URL) to a local
// directory. URL specs are cloned into the managed repos/ dir unless a clone
// already exists (or fresh forces a re-clone).
func (m Manager) ResolveTargetOpts(spec string, ro ResolveOpts) (string, Config, error) {
	if spec == "" {
		spec = "."
	}
	if !IsURL(spec) {
		p, err := filepath.Abs(spec)
		if err != nil {
			return "", Config{}, err
		}
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			return "", Config{}, fmt.Errorf("not a directory: %s", p)
		}
		return p, Config{Target: p, TargetOrigin: "local"}, nil
	}
	dir := m.RepoDir(spec)
	if ro.Fresh {
		if err := os.RemoveAll(dir); err != nil {
			return "", Config{}, err
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
			return "", Config{}, err
		}
		args := []string{"clone"}
		if ro.Depth > 0 {
			args = append(args, "--depth", fmt.Sprint(ro.Depth))
		}
		args = append(args, spec, dir)
		cmd := exec.Command("git", args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return "", Config{}, fmt.Errorf("git clone: %w", err)
		}
	}
	commitCmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	commit, err := commitCmd.Output()
	if err != nil {
		return "", Config{}, err
	}
	cfg := Config{Target: dir, TargetOrigin: "clone", CloneURL: spec, Commit: strings.TrimSpace(string(commit))}
	if ro.Install {
		if err := InstallDeps(dir); err != nil {
			return "", Config{}, err
		}
		cfg.DepsInstalled = true
	}
	return dir, cfg, nil
}

func InstallDeps(dir string) error {
	fmt.Fprintf(os.Stderr, "WARNING: --install-deps executes lifecycle scripts from the target clone\n")
	args := []string{"install"}
	if _, err := os.Stat(filepath.Join(dir, "package-lock.json")); err == nil {
		args = []string{"ci"}
	}
	cmd := exec.Command("npm", args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func (m Manager) Init(target string, scope []string, chain bool) (string, Config, error) {
	return m.InitOpts(target, scope, chain, ResolveOpts{})
}

// InitOpts resolves the target (cloning URL specs per ro) and creates the
// workspace: poc/ dir, config.json, and a placeholder FINDINGS.md.
func (m Manager) InitOpts(target string, scope []string, chain bool, ro ResolveOpts) (string, Config, error) {
	resolved, origin, err := m.ResolveTargetOpts(target, ro)
	if err != nil {
		return "", Config{}, err
	}
	if _, err := os.Stat(filepath.Join(resolved, ".git")); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "warning: %s is not a git repo — findings cannot be diffed against a baseline\n", resolved)
	}
	cleaned := cleanScope(scope)
	cfg := Config{Version: Version, Target: resolved, CreatedAt: time.Now().UTC().Format(time.RFC3339), Scope: cleaned, Onchain: chain || contains(cleaned, "chain"), Services: origin.Services, TargetOrigin: origin.TargetOrigin, CloneURL: origin.CloneURL, Commit: origin.Commit}
	ws := m.Workspace(resolved)
	if err := os.MkdirAll(filepath.Join(ws, "poc"), 0755); err != nil {
		return "", Config{}, err
	}
	if err := writeJSON(filepath.Join(ws, "config.json"), cfg); err != nil {
		return "", Config{}, err
	}
	if err := os.WriteFile(filepath.Join(ws, "FINDINGS.md"), []byte(fmt.Sprintf("# ckb-whitehat — %s\n\n(not started)\n", filepath.Base(resolved))), 0644); err != nil {
		return "", Config{}, err
	}
	return ws, cfg, nil
}

func (m Manager) ReadConfig(target string) (string, Config, error) {
	ws := m.Workspace(target)
	var cfg Config
	if err := readJSON(filepath.Join(ws, "config.json"), &cfg); err != nil {
		return ws, cfg, err
	}
	return ws, cfg, nil
}
func (m Manager) SaveConfig(ws string, cfg Config) error {
	return writeJSON(filepath.Join(ws, "config.json"), cfg)
}
func (m Manager) Report(target string) (string, error) {
	ws := m.Workspace(target)
	b, err := os.ReadFile(filepath.Join(ws, "FINDINGS.md"))
	return string(b), err
}
func (m Manager) Clean(includeWorkspaces bool) error {
	if err := os.RemoveAll(filepath.Join(m.root(), "repos")); err != nil {
		return err
	}
	if includeWorkspaces {
		entries, err := os.ReadDir(m.root())
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		for _, e := range entries {
			if e.IsDir() && e.Name() != "repos" {
				if err := os.RemoveAll(filepath.Join(m.root(), e.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func cleanScope(in []string) []string {
	out := []string{}
	for _, s := range in {
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}
func contains(xs []string, want string) bool {
	for _, x := range xs {
		if strings.TrimSpace(x) == want {
			return true
		}
	}
	return false
}
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}
func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
