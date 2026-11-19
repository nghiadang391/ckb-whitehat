package detect

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type fakeHTTP func(*http.Request) (*http.Response, error)

func (f fakeHTTP) Do(r *http.Request) (*http.Response, error) { return f(r) }

type responseBody string

func (b responseBody) Read(p []byte) (int, error) { return strings.NewReader(string(b)).Read(p) }
func (b responseBody) Close() error               { return nil }

func TestDetectServices(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "pg_isready" {
			t.Fatalf("command %q", name)
		}
		if args[len(args)-1] == "5433" {
			return []byte("127.0.0.1:5433 - accepting connections\n"), nil
		}
		return nil, fmt.Errorf("down")
	}
	httpDo := fakeHTTP(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: responseBody(`{"result":{"version":"0.9.1"}}`)}, nil
	})
	got := Detect(context.Background(), Options{RunCommand: run, HTTP: httpDo, PGPorts: []int{5432, 5433}, FNNPorts: []int{8227}, Timeout: time.Second})
	want := []string{"- Postgres is running on 127.0.0.1:5433 (use a scratch database for PoC data)", "- A Fiber (FNN) node is running at http://127.0.0.1:8227 (v0.9.1)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestNodeVersionReportsPresence(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "node" {
			return []byte("v20.11.0\n"), nil
		}
		return nil, fmt.Errorf("absent")
	}
	if got := NodeVersion(context.Background(), Options{RunCommand: run}); got != "v20.11.0" {
		t.Fatalf("got %q", got)
	}
}

func TestNodeVersionMissingIsEmpty(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		return nil, fmt.Errorf("exec: not found")
	}
	if got := NodeVersion(context.Background(), Options{RunCommand: run}); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestProbeRejectsMalformedAndHTTPError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"error", 500, `{}`}, {"malformed", 200, `nope`}, {"missing version", 200, `{"result":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := probeFNN(context.Background(), fakeHTTP(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}), 8227)
			if ok {
				t.Fatal("expected failed probe")
			}
		})
	}
}
