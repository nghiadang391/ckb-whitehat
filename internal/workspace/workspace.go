package workspace

import (
	"crypto/sha256"
	"encoding/hex"
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

// RepoDir returns the managed clone dir for a target spec.
//
// URL inputs — containing "://", starting with "git+" or "git@", or a bare
// host/owner/repo form (github.com/x/y) — are CWD-independent: the slug is
// derived from the URL host and path segments plus a 6-hex-char SHA-256
// suffix of the normalized host+path, so github.com/orgA/Dular and
// github.com/orgB/Dular never collide, and the result does not depend on the
// process working directory. Local path inputs keep the legacy behavior (slug
// of the absolute path), so existing local workspaces keep their directories.
func (m Manager) RepoDir(spec string) string {
	var slug string
	if isURLSpec(spec) {
		slug = urlSlug(spec)
	} else {
		slug = Slug(spec)
	}
	if slug == "" {
		slug = "repo"
	}
	return filepath.Join(m.root(), "repos", slug)
}

// isURLSpec reports whether s is a URL-like git remote spec rather than a
// local path. URL shapes: contains "://", starts with "git+" or "git@", or
// is a bare host/owner/repo form (github.com/x/y, gitlab.com/x/y) — the form
// a user types to init. Anything rooted ("/x") or clearly relative ("./x",
// "../x") is a local path; a bare word with no slash is a local path too.
// The URL branch never touches the CWD.
func isURLSpec(s string) bool {
	if strings.Contains(s, "://") || strings.HasPrefix(s, "git+") || strings.HasPrefix(s, "git@") {
		return true
	}
	if s == "" || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "..") {
		return false
	}
	i := strings.IndexByte(s, '/')
	if i < 0 {
		return false
	}
	return strings.ContainsAny(s[:i], ".")
}

// urlSlug builds a stable slug from a URL or git remote spec.
// git+ prefixes are stripped, then host and path segments are slugified
// (lowercased, non-alphanumeric runs become single dashes) and a 6-hex-char
// SHA-256 prefix of the normalized scheme-less host+path (case-folded, no
// .git suffix) is appended, so URLs that survive to the same slug body but
// are not the same repo get distinct suffixes. No CWD involvement. E.g.
// github.com/duongja/Dular → "github-com-duongja-dular-<6hex>".
func urlSlug(spec string) string {
	s := strings.TrimPrefix(spec, "git+")
	var host, path string
	if i := strings.Index(s, "://"); i >= 0 {
		rest := s[i+len("://"):]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			host, path = rest[:j], rest[j+1:]
		} else {
			host = rest
		}
		if j := strings.IndexByte(host, '@'); j >= 0 {
			host = host[j+1:] // drop userinfo, e.g. ssh://git@github.com/...
		}
	} else {
		// scp-like git remote (git@github.com:org/repo.git) or a bare
		// host/owner/repo form (github.com/org/repo).
		if i := strings.LastIndex(s, "@"); i >= 0 {
			s = s[i+1:]
		}
		if i := strings.LastIndex(s, ":"); i >= 0 {
			host, path = s[:i], s[i+1:]
		} else if i := strings.IndexByte(s, '/'); i >= 0 {
			host, path = s[:i], s[i+1:]
		} else {
			host = s
		}
	}
	parts := []string{slugify(host)}
	segs := strings.Split(path, "/")
	if len(segs) > 0 {
		segs[len(segs)-1] = strings.TrimSuffix(segs[len(segs)-1], ".git")
	}
	for _, seg := range segs {
		if seg = slugify(seg); seg != "" && seg != ".git" {
			parts = append(parts, seg)
		}
	}
	base := strings.Join(parts, "-")
	if base == "" {
		base = "repo"
	}
	np := strings.TrimSuffix(strings.TrimSuffix(path, ".git"), "/")
	sum := sha256.Sum256([]byte(strings.ToLower(host) + "/" + strings.ToLower(np)))
	return base + "-" + hex.EncodeToString(sum[:])[:6]
}

// slugify normalizes one host or path segment: lowercased, runs of
// non-alphanumeric characters collapsed to single dashes.
func slugify(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
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
		return p, Config{Target: p, TargetOrigin: "local", Services: []string{}}, nil
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
		// H1.4: harden against an untrusted target repo's .git — disable
		// hook execution (post-checkout hooks), the fsmonitor binary, and
		// symlink creation from cloned content.
		args := []string{"clone", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.symlinks=false"}
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
	cfg := Config{Target: dir, TargetOrigin: "clone", CloneURL: spec, Commit: strings.TrimSpace(string(commit)), Services: []string{}}
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
	cfg := Config{Version: Version, Target: resolved, CreatedAt: time.Now().UTC().Format(time.RFC3339), Scope: cleaned, Onchain: chain || contains(cleaned, "chain"), Services: origin.Services, TargetOrigin: origin.TargetOrigin, CloneURL: origin.CloneURL, Commit: origin.Commit, DepsInstalled: origin.DepsInstalled}
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
