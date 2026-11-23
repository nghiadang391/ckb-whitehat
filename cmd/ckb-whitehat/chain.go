// chain.go wires `ckb-whitehat chain up|status|down`, automatic devnet startup
// for `--chain`, mainnet refusal checks, and CKB/Fiber/Rust stack helpers.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/nghiadang391/ckb-whitehat/internal/chain"
	"github.com/nghiadang391/ckb-whitehat/internal/setup"
)

func chainCmd(a []string) int {
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()

	sub := ""
	if len(a) > 0 {
		sub = a[0]
	}
	h := chain.NewHarness("")
	switch sub {
	case "up":
		m, err := h.Up(ctx, os.Stdout)
		if err != nil {
			die(err.Error(), 1)
		}
		printChainSummary(m, h.ManifestPath())
		return 0
	case "status":
		st := h.Status(ctx)
		printChainStatus(st, h.ManifestPath())
		if !st.Ready() {
			return 1
		}
		return 0
	case "down":
		if err := h.Down(ctx, os.Stdout); err != nil {
			die(err.Error(), 1)
		}
		return 0
	default:
		fmt.Fprintln(os.Stderr, "usage: ckb-whitehat chain <up|status|down>")
		return 2
	}
}

func printChainSummary(m *chain.Manifest, path string) {
	if m == nil {
		return
	}
	fmt.Printf("  network    : %s (%s)\n", m.Network, m.ChainHash)
	fmt.Printf("  CKB RPC    : %s\n", m.CKBRPCURL)
	fmt.Printf("  FNN Node A : %s (v%s, peer=%s)\n", m.NodeA.RPCURL, m.NodeA.Version, m.NodeA.PeerID)
	fmt.Printf("  FNN Node B : %s (v%s, peer=%s)\n", m.NodeB.RPCURL, m.NodeB.Version, m.NodeB.PeerID)
	if m.CKBChannel != nil {
		fmt.Printf("  CKB chan   : %s (%s, local=%s)\n", m.CKBChannel.ChannelID, m.CKBChannel.State, m.CKBChannel.LocalBalance)
	}
	if m.UDT != nil && m.UDTChannel != nil {
		fmt.Printf("  xUDT chan  : %s (%s, %s local=%s)\n", m.UDTChannel.ChannelID, m.UDTChannel.State, m.UDT.Name, m.UDTChannel.LocalBalance)
	}
	fmt.Printf("  manifest   : %s\n", path)
}

func printChainStatus(st chain.StatusReport, manifestPath string) {
	fmt.Printf("docker     : %v\n", st.DockerReady)
	if st.CKBRunning {
		fmt.Printf("ckb        : running (chain=%s, tip=%d)\n", st.CKBChain, st.CKBTip)
	} else {
		fmt.Println("ckb        : stopped")
	}
	if st.NodeAReady {
		fmt.Printf("fnn node-a : running (%s, v%s, peer=%s)\n", st.NodeA.RPCURL, st.NodeA.Version, st.NodeA.PeerID)
	} else {
		fmt.Println("fnn node-a : stopped")
	}
	if st.NodeBReady {
		fmt.Printf("fnn node-b : running (%s, v%s, peer=%s)\n", st.NodeB.RPCURL, st.NodeB.Version, st.NodeB.PeerID)
	} else {
		fmt.Println("fnn node-b : stopped")
	}
	fmt.Printf("channels   : %d\n", len(st.Channels))
	for _, ch := range st.Channels {
		kind := "CKB"
		if ch.UDTTypeScript != nil {
			kind = "xUDT"
		}
		fmt.Printf("  - %-4s %s  state=%s  local=%s  funding=%s\n", kind, ch.ChannelID, ch.State, ch.LocalBalance, ch.FundingTxHash)
	}
	if st.Manifest != nil {
		fmt.Printf("manifest   : %s\n", manifestPath)
	}
	if chain.ScratchPostgresReady(context.Background()) {
		fmt.Printf("scratch pg : running (%s)\n", chain.ScratchPGURL())
	} else {
		fmt.Println("scratch pg : stopped")
	}
}

// chainEndpoints returns the CKB and FNN RPC URLs to check for mainnet safety
// and post-run on-chain verification.
func chainEndpoints(m *chain.Manifest) (ckbURL string, fnnURLs []string) {
	ckbURL = os.Getenv("CKB_RPC_URL")
	if ckbURL == "" {
		if m != nil && m.CKBRPCURL != "" {
			ckbURL = m.CKBRPCURL
		} else {
			ckbURL = fmt.Sprintf("http://127.0.0.1:%d", chain.DefaultCKBPort)
		}
	}
	seen := map[string]bool{}
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		fnnURLs = append(fnnURLs, u)
	}
	if u := os.Getenv("FIBER_RPC_URL"); u != "" {
		add(u)
	}
	if u := os.Getenv("FIBER_PEER_RPC_URL"); u != "" {
		add(u)
	}
	if m != nil {
		add(m.NodeA.RPCURL)
		add(m.NodeB.RPCURL)
	}
	if len(fnnURLs) == 0 {
		add(fmt.Sprintf("http://127.0.0.1:%d", chain.DefaultNodeAPort))
		add(fmt.Sprintf("http://127.0.0.1:%d", chain.DefaultNodeBPort))
	}
	return ckbURL, fnnURLs
}

// ensureDevnetChain enforces the mainnet refusal guard and, when autoStart is
// true and the local devnet harness is down, starts it automatically via Docker.
func ensureDevnetChain(ctx context.Context, home string, autoStart bool) (*chain.Manifest, string, error) {
	h := chain.NewHarness(home)
	existing, _ := chain.LoadManifest(h.ManifestPath())
	ckbURL, fnnURLs := chainEndpoints(existing)
	if err := chain.VerifyMainnetSafe(ctx, h.RPC, ckbURL, fnnURLs); err != nil {
		return nil, h.ManifestPath(), err
	}
	st := h.Status(ctx)
	if st.Ready() && st.Manifest != nil {
		return st.Manifest, h.ManifestPath(), nil
	}
	if !autoStart {
		return existing, h.ManifestPath(), nil
	}
	m, err := h.Up(ctx, os.Stdout)
	if err != nil {
		return nil, h.ManifestPath(), err
	}
	return m, h.ManifestPath(), nil
}

// detectCKBFiberStack reports whether the read-only init agent identified a
// CKB or Fiber stack/service in the target repository.
func detectCKBFiberStack(rec *setup.Record) bool {
	if rec == nil {
		return false
	}
	for _, s := range rec.Plan.Stacks {
		l := strings.ToLower(s)
		if strings.Contains(l, "fiber") || strings.Contains(l, "ckb") || strings.Contains(l, "fnn") {
			return true
		}
	}
	for _, svc := range rec.Plan.Services {
		l := strings.ToLower(svc.Name)
		if strings.Contains(l, "fiber") || strings.Contains(l, "ckb") || strings.Contains(l, "fnn") {
			return true
		}
	}
	return false
}

// needsPostgres reports whether the target repo uses Postgres (detected by
// init's setup.json, a `pg` dependency in package.json, or a schema.sql file).
func needsPostgres(target string, rec *setup.Record) bool {
	if rec != nil {
		for _, s := range rec.Plan.Stacks {
			l := strings.ToLower(s)
			if strings.Contains(l, "postgres") || l == "pg" {
				return true
			}
		}
		for _, svc := range rec.Plan.Services {
			l := strings.ToLower(svc.Name)
			if strings.Contains(l, "postgres") || l == "pg" {
				return true
			}
		}
	}
	if target != "" {
		if b, err := os.ReadFile(filepath.Join(target, "package.json")); err == nil {
			if strings.Contains(string(b), `"pg"`) {
				return true
			}
		}
		for _, rel := range []string{"schema.sql", filepath.Join("server", "schema.sql")} {
			if st, err := os.Stat(filepath.Join(target, rel)); err == nil && !st.IsDir() {
				return true
			}
		}
	}
	return false
}

// withEnvVar returns a copy of env with key=val replaced (or appended if absent).
func withEnvVar(env []string, key, val string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			out = append(out, prefix+val)
			found = true
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, prefix+val)
	}
	return out
}

// isRustStack reports whether the target is a Rust / CKB-script project
// (either reported by setup.json or indicated by a Cargo.toml in target).
func isRustStack(target string, rec *setup.Record) bool {
	if rec != nil {
		for _, s := range rec.Plan.Stacks {
			l := strings.ToLower(s)
			if l == "rust" || strings.Contains(l, "cargo") || strings.Contains(l, "ckb-script") {
				return true
			}
		}
	}
	if target != "" {
		if st, err := os.Stat(filepath.Join(target, "Cargo.toml")); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

// effectiveAllowedTools returns the tool allowlist for `run`: explicit flags
// or CKB_WHITEHAT_ALLOWED_TOOLS take precedence verbatim; otherwise starts
// from defaultAllowedTools and adds `Bash(cargo:*)` when auditing a Rust /
// CKB-script target (keeping curl/wget/psql/env strictly excluded).
func effectiveAllowedTools(a []string, target string, rec *setup.Record) []string {
	for _, s := range a {
		if strings.HasPrefix(s, "--allowed-tools=") {
			return allowedToolsList(a)
		}
	}
	if raw, ok := flagVal(a, "allowed-tools"); ok && raw != "" {
		return allowedToolsList(a)
	}
	if _, ok := os.LookupEnv("CKB_WHITEHAT_ALLOWED_TOOLS"); ok {
		return allowedToolsList(a)
	}
	out := append([]string{}, defaultAllowedTools...)
	if isRustStack(target, rec) && !contains(out, "Bash(cargo:*)") {
		out = append(out, "Bash(cargo:*)")
	}
	return out
}
