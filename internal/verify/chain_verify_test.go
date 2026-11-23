package verify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nghiadang391/ckb-whitehat/internal/chain"
)

func TestVerifyRunWithChainStrictRPC(t *testing.T) {
	const (
		goodTx  = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		goodPay = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		fakeTx  = "0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	)

	ckbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var res any
		if req.Method == "get_transaction" && strings.Contains(string(req.Params), goodTx) {
			res = map[string]any{"tx_status": map[string]any{"status": "committed"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
	}))
	defer ckbSrv.Close()

	fnnSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var res any
		if req.Method == "get_payment" && strings.Contains(string(req.Params), goodPay) {
			res = map[string]any{"payment_hash": goodPay, "status": "Success"}
		} else if req.Method == "list_channels" {
			res = map[string]any{"channels": []any{}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
	}))
	defer fnnSrv.Close()

	makeWS := func(onchainBody string) string {
		ws := t.TempDir()
		if err := os.MkdirAll(filepath.Join(ws, "poc"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws, "poc", "c1.mjs"), []byte("console.log(1)\n"), 0644); err != nil {
			t.Fatal(err)
		}
		report := "# ckb-whitehat\n\n## At a glance\n" + strings.Repeat("finding details ", 20) +
			"\n\n## F1 — double spend\nFINDING F1 FIRED\n\n## On-chain evidence\n" + onchainBody + "\n\n## Layer boundary\napp only\n"
		if err := os.WriteFile(filepath.Join(ws, "FINDINGS.md"), []byte(report), 0644); err != nil {
			t.Fatal(err)
		}
		return ws
	}

	stdout := "FINDING F1 FIRED\nCKB-WHITEHAT-RESULT: findings=1 fired=1 report=FINDINGS.md\n"
	marker := LastMarker(stdout)
	opts := ChainCheckOpts{Enabled: true, CKBURL: ckbSrv.URL, FNNURLs: []string{fnnSrv.URL}, RPC: chain.NewRPCClient()}

	// 1. Valid hashes pass all checks.
	wsGood := makeWS("Funding tx " + goodTx + " and payment " + goodPay)
	checks := VerifyRunWithChain(context.Background(), wsGood, stdout, marker, opts)
	for _, c := range checks {
		if !c.OK {
			t.Fatalf("expected all checks OK for verified hashes, failed: %+v", c)
		}
	}

	// 2. N/A section fails strict --chain verification.
	wsNA := makeWS("N/A — no chain interaction")
	checks = VerifyRunWithChain(context.Background(), wsNA, stdout, marker, opts)
	if checks[len(checks)-1].OK {
		t.Fatalf("expected N/A section to fail --chain check: %+v", checks)
	}

	// 3. Hallucinated hash fails RPC verification.
	wsFake := makeWS("Settled tx " + fakeTx)
	checks = VerifyRunWithChain(context.Background(), wsFake, stdout, marker, opts)
	if checks[len(checks)-1].OK {
		t.Fatalf("expected hallucinated hash to fail RPC verification: %+v", checks)
	}
}
