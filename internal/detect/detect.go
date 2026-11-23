// Package detect discovers local services used by a target's PoCs.
package detect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// CommandRunner runs a local command and returns combined output.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// HTTPDoer is the subset of http.Client used by Detect.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Options makes Detect deterministic and testable.
type Options struct {
	RunCommand CommandRunner
	HTTP       HTTPDoer
	Timeout    time.Duration
	PGPorts    []int
	CKBPorts   []int
	FNNPorts   []int
}

// Detect reports accepting Postgres, CKB, and Fiber nodes. Probe failures are
// intentionally silent: a service that is not running is simply omitted.
func Detect(ctx context.Context, opts Options) []string {
	run := opts.RunCommand
	if run == nil {
		run = defaultCommand
	}
	doer := opts.HTTP
	if doer == nil {
		doer = &http.Client{}
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	pgPorts := opts.PGPorts
	if pgPorts == nil {
		pgPorts = []int{5432, 5433, 5434}
	}
	ckbPorts := opts.CKBPorts
	if ckbPorts == nil {
		ckbPorts = []int{8114}
	}
	fnnPorts := opts.FNNPorts
	if fnnPorts == nil {
		// 8227/8327 are the classic 2-node setup ports; 8386/8387 are the
		// alternate pair our own testnet stacks use.
		fnnPorts = []int{8227, 8327, 8386, 8387}
	}
	out := []string{}
	for _, port := range pgPorts {
		b, err := run(ctx, "pg_isready", "-h", "127.0.0.1", "-p", strconv.Itoa(port))
		if err == nil && bytes.Contains(b, []byte("accepting connections")) {
			out = append(out, fmt.Sprintf("- Postgres is running on 127.0.0.1:%d (use a scratch database for PoC data)", port))
			break
		}
	}
	for _, port := range ckbPorts {
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		chainName, ok := probeCKB(probeCtx, doer, port)
		cancel()
		if ok {
			out = append(out, fmt.Sprintf("- A CKB node is running at http://127.0.0.1:%d (chain=%s)", port, chainName))
		}
	}
	for _, port := range fnnPorts {
		probeCtx, cancel := context.WithTimeout(ctx, timeout)
		info, ok := probeFNN(probeCtx, doer, port)
		cancel()
		if ok && info.Version != "" {
			out = append(out, fmt.Sprintf("- A Fiber (FNN) node is running at http://127.0.0.1:%d (v%s)", port, info.Version))
		}
	}
	return out
}

func probeCKB(ctx context.Context, client HTTPDoer, port int) (string, bool) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"get_blockchain_info","params":[]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(port), bytes.NewReader(body))
	if err != nil {
		return "", false
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", false
	}
	var raw struct {
		Result struct {
			Chain string `json:"chain"`
		} `json:"result"`
	}
	if json.NewDecoder(res.Body).Decode(&raw) != nil || raw.Result.Chain == "" {
		return "", false
	}
	return raw.Result.Chain, true
}

type rpcResponse struct {
	Result rpcResponseResult `json:"result"`
}

func probeFNN(ctx context.Context, client HTTPDoer, port int) (rpcResponseResult, bool) {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"node_info","params":[]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(port), bytes.NewReader(body))
	if err != nil {
		return rpcResponseResult{}, false
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return rpcResponseResult{}, false
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return rpcResponseResult{}, false
	}
	var raw rpcResponse
	if json.NewDecoder(res.Body).Decode(&raw) != nil {
		return rpcResponseResult{}, false
	}
	return raw.Result, raw.Result.Version != ""
}

type rpcResponseResult struct {
	Version string `json:"version"`
}

func defaultCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Output()
}

// NodeVersion returns the `node --version` output (e.g. "v20.11.0"), or ""
// when node is not on PATH. Target PoCs are agent-written and run under
// Node, so init reports whether the prerequisite is present. It honors
// Options.RunCommand so tests can stub it.
func NodeVersion(ctx context.Context, opts Options) string {
	run := opts.RunCommand
	if run == nil {
		run = defaultCommand
	}
	b, err := run(ctx, "node", "--version")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// FormatServices turns detected entries into the prompt block used by the
// CLI. It is kept here so callers can persist and render the same values.
func FormatServices(services []string) string { return strings.Join(services, "\n") }
