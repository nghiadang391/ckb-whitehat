package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCompareVersionsNumeric(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.1.9", "0.1.10", -1}, // the case string compare gets wrong
		{"0.1.10", "0.1.9", 1},
		{"v0.2.0", "0.2.0", 0},
		{"dev", "v0.1.0", -1}, // dev counts as 0, so any release is newer
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.1", 0}, // missing fields pad with 0
		{"0.1.0", "0.1.0.0", 0},
		{"0.2.0", "0.1.9", 1},
	} {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestUpdateStatus(t *testing.T) {
	if updateStatus("v0.1.0", "v0.1.0", false) != "same" {
		t.Error("latest equal must be same")
	}
	if updateStatus("dev", "v0.1.0", false) != "update" {
		t.Error("dev must be older than any release")
	}
	if updateStatus("v0.2.0", "v0.1.0", false) != "same" {
		t.Error("newer current than latest target must be same")
	}
	if updateStatus("v0.2.0", "v0.1.0", true) != "update" {
		t.Error("an explicit version arg always updates (pin/rollback)")
	}
}

// assetNameFor is what the fixture release offers for the test's platform.
func assetNameFor() string {
	name := "ckb-whitehat-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

// releaseFixture is the release payload the fake server offers.
func releaseFixture(tag string) releaseInfo {
	asset := assetNameFor()
	base := "https://download.local"
	_ = base
	return releaseInfo{TagName: tag, Assets: []releaseAsset{
		{Name: asset, BrowserDownloadURL: "https://download.local/" + asset},
		{Name: "sha256sums.txt", BrowserDownloadURL: "https://download.local/sha256sums.txt"},
	}}
}

// serveRelease writes rel, with asset download URLs rewritten to the
// requesting host so the update command fetches assets from the fake server.
func serveRelease(w http.ResponseWriter, r *http.Request, rel releaseInfo) {
	payload := rel
	for i := range payload.Assets {
		payload.Assets[i].BrowserDownloadURL = "http://" + r.Host + "/download/" + payload.Assets[i].Name
	}
	json.NewEncoder(w).Encode(payload)
}

// fakeReleaseServer serves /releases/latest, /releases/tags/<v>, and
// /download/<asset>.
func fakeReleaseServer(t *testing.T, rel releaseInfo, binary, sums string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/ckb-whitehat/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		serveRelease(w, r, rel)
	})
	// any pinned tag (used by the explicit/rollback path)
	mux.HandleFunc("/repos/acme/ckb-whitehat/releases/tags/", func(w http.ResponseWriter, r *http.Request) {
		serveRelease(w, r, rel)
	})
	for _, asset := range rel.Assets {
		mux.HandleFunc("/download/"+asset.Name, func(w http.ResponseWriter, r *http.Request) {
			if filepath.Base(r.URL.Path) == "sha256sums.txt" {
				fmt.Fprint(w, sums)
			} else {
				fmt.Fprint(w, binary)
			}
		})
	}
	return httptest.NewServer(mux)
}

// runUpdate copies the built binary and runs `update` on the copy so the
// self-replace never touches binPath, which the other tests share.
func runUpdate(t *testing.T, rel releaseInfo, binary, sums string, args ...string) (int, string, string) {
	t.Helper()
	s := fakeReleaseServer(t, rel, binary, sums)
	defer s.Close()

	tmp := t.TempDir()
	exe := filepath.Join(tmp, "ckb-whitehat")
	src, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("read binPath: %v", err)
	}
	if err := os.WriteFile(exe, src, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(exe, append([]string{"update"}, args...)...)
	cmd.Env = append(os.Environ(),
		"CKB_WHITEHAT_UPDATE_REPO=acme/ckb-whitehat",
		"CKB_WHITEHAT_UPDATE_API="+s.URL,
	)
	outBytes, _ := cmd.CombinedOutput()
	out := string(outBytes)
	code := 0
	if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
		code = cmd.ProcessState.ExitCode()
	}
	return code, out, exe
}

func TestUpdateReplacesBinary(t *testing.T) {
	binary := "FAKE-NEW-BINARY"
	asset := assetNameFor()
	sum, _ := shaOf([]byte(binary))
	sums := sum + "  " + asset + "\n"
	rel := releaseFixture("v0.1.2")

	code, out, exe := runUpdate(t, rel, binary, sums)
	if code != 0 {
		t.Fatalf("update code=%d out=%s", code, out)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != binary {
		t.Fatalf("binary not replaced: %q", string(b))
	}
	if !containsLine(out, "verified sha256") {
		t.Fatalf("missing verification line in:\n%s", out)
	}
}

func TestUpdateRejectsShaMismatch(t *testing.T) {
	binary := "FAKE-NEW-BINARY"
	asset := assetNameFor()
	// wrong hash: the served sums entry does not match the served binary
	sums := "0000000000000000000000000000000000000000000000000000000000000000  " + asset + "\n"
	rel := releaseFixture("v0.1.2")

	code, out, exe := runUpdate(t, rel, binary, sums)
	if code != 1 {
		t.Fatalf("want code 1 on mismatch, got %d out=%s", code, out)
	}
	if b, _ := os.ReadFile(exe); string(b) == binary {
		t.Fatal("binary was replaced despite sha mismatch")
	}
	if !containsLine(out, "sha256 mismatch") {
		t.Fatalf("want a mismatch message, got:\n%s", out)
	}
}

// An explicit older version is a rollback: it must install even though it is
// older than the running binary.
func TestUpdateExplicitRollback(t *testing.T) {
	binary := "FAKE-OLD-BINARY"
	asset := assetNameFor()
	sum, _ := shaOf([]byte(binary))
	sums := sum + "  " + asset + "\n"
	rel := releaseFixture("v0.0.9")

	code, out, exe := runUpdate(t, rel, binary, sums, "v0.0.9")
	if code != 0 {
		t.Fatalf("rollback code=%d out=%s", code, out)
	}
	if b, _ := os.ReadFile(exe); string(b) != binary {
		t.Fatal("rollback binary not installed")
	}
}

func containsLine(s, sub string) bool {
	for _, l := range splitLines(s) {
		if strContains(l, sub) {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(c)
	}
	out = append(out, cur)
	return out
}
