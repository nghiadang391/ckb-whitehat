// ckb-whitehat update — self-update from GitHub Releases.
//
// The binary is stamped with its version at release time (release.yml
// passes -X main.version); this command fetches a release, verifies the
// asset's sha256 against the release's sha256sums.txt before touching the
// binary, and replaces it atomically. The API base and owner/repo are
// overridable via env so tests and mirrors can point elsewhere:
//
//	CKB_WHITEHAT_UPDATE_API   default https://api.github.com
//	CKB_WHITEHAT_UPDATE_REPO  default nghiadang391/ckb-whitehat

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

func updateCmd(a []string) int {
	check := hasFlag(a, "check")
	var target string
	for _, s := range a {
		if s == "--check" {
			continue
		}
		if strings.HasPrefix(s, "--") {
			die("unknown flag: "+s, 2)
		}
		if target != "" {
			die("unexpected argument: "+s, 2)
		}
		target = s
	}
	if target != "" && !strings.HasPrefix(target, "v") {
		target = "v" + target
	}

	repo := os.Getenv("CKB_WHITEHAT_UPDATE_REPO")
	if repo == "" {
		repo = "nghiadang391/ckb-whitehat"
	}
	apiBase := os.Getenv("CKB_WHITEHAT_UPDATE_API")
	if apiBase == "" {
		apiBase = "https://api.github.com"
	}
	// A non-HTTPS API base would let a mirror serve release metadata over an
	// open channel — the checksums would be untrusted too (audit M2.1).
	// Allow plain http only for a localhost test mirror.
	if !strings.HasPrefix(apiBase, "http") {
		apiBase = "https://" + apiBase
	}
	if !strings.HasPrefix(apiBase, "https://") && !strings.Contains(apiBase, "127.0.0.1") && !strings.Contains(apiBase, "localhost") {
		die("update: CKB_WHITEHAT_UPDATE_API must be https (or a localhost test mirror)", 1)
	}

	url := apiBase + "/repos/" + repo + "/releases/"
	if target == "" {
		url += "latest"
	} else {
		url += "tags/" + target
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	res, err := client.Get(url)
	if err != nil {
		die("update: cannot reach the release API: "+err.Error(), 1)
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound:
		die("update: no such release: "+url, 1)
	case res.StatusCode != http.StatusOK:
		die("update: release API returned "+res.Status, 1)
	}
	var rel releaseInfo
	if err := json.NewDecoder(res.Body).Decode(&rel); err != nil {
		die("update: malformed release payload: "+err.Error(), 1)
	}
	if rel.TagName == "" {
		rel.TagName = target
	}

	if check {
		if updateStatus(version, rel.TagName, target != "") == "same" {
			fmt.Printf("update: already up to date (%s)\n", rel.TagName)
		} else {
			fmt.Printf("update: current %s, target %s - run: ckb-whitehat update %s\n", version, rel.TagName, rel.TagName)
		}
		return 0
	}
	if target == "" && updateStatus(version, rel.TagName, false) == "same" {
		fmt.Printf("update: already up to date (%s)\n", rel.TagName)
		return 0
	}

	name := "ckb-whitehat-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var binURL, sumsURL string
	for _, asset := range rel.Assets {
		switch asset.Name {
		case name:
			binURL = asset.BrowserDownloadURL
		case "sha256sums.txt":
			sumsURL = asset.BrowserDownloadURL
		}
	}
	if binURL == "" || sumsURL == "" {
		die("update: release "+rel.TagName+" is missing the "+name+" or sha256sums.txt asset", 1)
	}

	sumsBody, err := download(client, ensureHTTPS(sumsURL))
	if err != nil {
		die("update: download "+sumsURL+": "+err.Error(), 1)
	}
	want, err := shaEntry(sumsBody, name)
	if err != nil {
		die("update: "+err.Error(), 1)
	}

	exe, err := os.Executable()
	if err != nil {
		die("update: "+err.Error(), 1)
	}
	fmt.Printf("update: current %s -> target %s\n", version, rel.TagName)
	staged, err := installVerified(client, binURL, want, exe, name)
	if err != nil {
		die("update: "+err.Error(), 1)
	}
	if staged {
		// A running Windows process locks its own exe; os.Rename over it
		// would fail, so we staged next to it — hand the move to the user.
		fmt.Printf("staged %s (running binary cannot replace itself)\nmove it once this process exits:\n  move \"%s\" \"%s\"\n", exe+".new", exe+".new", exe)
	} else {
		fmt.Printf("updated %s to %s\n", exe, rel.TagName)
	}
	return 0
}

func installVerified(client *http.Client, binURL, wantHash, exe, assetName string) (staged bool, err error) {
	body, err := download(client, ensureHTTPS(binURL))
	if err != nil {
		return false, fmt.Errorf("download %s: %w", binURL, err)
	}
	got, err := shaOf(body)
	if err != nil {
		return false, err
	}
	if got != wantHash {
		return false, fmt.Errorf("sha256 mismatch for %s: got %s want %s - binary not replaced", assetName, got, wantHash)
	}
	// Resolve the running binary's real path so a symlinked install updates
	// the target, not the link (audit M2.4).
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return false, err
	}
	fmt.Printf("verified sha256 of %s\n", assetName)

	// Windows locks a running process's own .exe, so we cannot rename over
	// it. Stage next to it and let the user move it once the process exits.
	if runtime.GOOS == "windows" {
		dest := exe + ".new"
		if err := os.WriteFile(dest, body, 0755); err != nil {
			return false, fmt.Errorf("cannot write %s: %w", dest, err)
		}
		return true, nil
	}

	// Create the staging file unpredictably in the exe's directory rather
	// than at a fixed <exe>.new name (audit M2.4).
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".ckb-whitehat-*.new")
	if err != nil {
		return false, fmt.Errorf("cannot create a temp staging file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return false, fmt.Errorf("cannot write the staged binary: %w", err)
	}
	tmp.Close()
	if err := os.Chmod(tmpName, 0755); err != nil {
		os.Remove(tmpName)
		return false, err
	}
	if err := os.Rename(tmpName, exe); err != nil {
		os.Remove(tmpName)
		return false, err
	}
	return false, nil
}

// ensureHTTPS forces an https URL unless it already points at a localhost
// mirror (used by tests). Downloading a release asset over plain http is a
// supply-chain risk (audit M2.1).
func ensureHTTPS(u string) string {
	if strings.HasPrefix(u, "http://") && !strings.Contains(u, "127.0.0.1") && !strings.Contains(u, "localhost") {
		return "https://" + strings.TrimPrefix(u, "http://")
	}
	return u
}

// download fetches url and returns the body; non-200 is an error.
func download(client *http.Client, url string) ([]byte, error) {
	res, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("got %s", res.Status)
	}
	return io.ReadAll(res.Body)
}

// shaEntry extracts the expected sha256 hex for asset name from a
// sha256sums.txt body ("hash  name" lines, as `sha256sum` writes). It
// tolerates the `sha256sum -b` binary-mode `*name` prefix and the `*` that
// marks binary output (audit M2.3), and rejects a hash that is not 64
// lowercase hex so a malformed/trimmed entry can't be trusted.
func shaEntry(sums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		// Fields are the hex hash (possibly with a leading '*') and the
		// name (possibly with a leading '*' in binary mode).
		hash := strings.TrimPrefix(f[0], "*")
		file := strings.TrimPrefix(f[1], "*")
		if file != name {
			continue
		}
		if len(hash) != 64 {
			return "", fmt.Errorf("sha256sums.txt entry for %s is not a 64-char hash: %q", name, hash)
		}
		for _, c := range hash {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return "", fmt.Errorf("sha256sums.txt entry for %s is not hex: %q", name, hash)
			}
		}
		return strings.ToLower(hash), nil
	}
	return "", fmt.Errorf("sha256sums.txt has no entry for %s", name)
}

func shaOf(b []byte) (string, error) {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// updateStatus decides whether an install is wanted:
//   - an explicit version argument pins/rolls back, so always "update"
//   - for "latest", install only when the target is strictly newer
func updateStatus(current, target string, explicit bool) string {
	if explicit {
		return "update"
	}
	if compareVersions(target, current) > 0 {
		return "update"
	}
	return "same"
}

// compareVersions compares dot-separated numeric versions (so 0.1.9 < 0.1.10).
// "dev" and unparsable parts count as 0, so any real release is newer.
func compareVersions(a, b string) int {
	pa, pb := numericVersion(a), numericVersion(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		x, y := 0, 0
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}

func numericVersion(s string) []int {
	s = strings.TrimPrefix(s, "v")
	if s == "" || s == "dev" {
		return nil
	}
	var out []int
	for _, p := range strings.Split(s, ".") {
		n, err := strconv.Atoi(p)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}
