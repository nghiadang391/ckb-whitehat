package chain

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mockRPCServer(t *testing.T, handler func(method string, params json.RawMessage) any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		res := handler(req.Method, req.Params)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": res})
	}))
}

func TestVerifyMainnetSafeRefusesMainnet(t *testing.T) {
	mainnetCKB := mockRPCServer(t, func(method string, _ json.RawMessage) any {
		switch method {
		case "get_blockchain_info":
			return map[string]any{"chain": "ckb"}
		case "get_block_by_number":
			return map[string]any{"header": map[string]any{"hash": MainnetGenesisHash}, "transactions": []any{map[string]any{"hash": "0x11"}}}
		}
		return nil
	})
	defer mainnetCKB.Close()

	if err := VerifyMainnetSafe(context.Background(), NewRPCClient(), mainnetCKB.URL, nil); err == nil {
		t.Fatal("expected mainnet CKB node to be refused")
	}

	mainnetFNN := mockRPCServer(t, func(method string, _ json.RawMessage) any {
		if method == "node_info" {
			return map[string]any{"version": "0.10.1", "chain_hash": MainnetGenesisHash}
		}
		return nil
	})
	defer mainnetFNN.Close()

	if err := VerifyMainnetSafe(context.Background(), NewRPCClient(), "", []string{mainnetFNN.URL}); err == nil {
		t.Fatal("expected mainnet FNN node to be refused")
	}

	devCKB := mockRPCServer(t, func(method string, _ json.RawMessage) any {
		switch method {
		case "get_blockchain_info":
			return map[string]any{"chain": "ckb_dev"}
		case "get_block_by_number":
			return map[string]any{"header": map[string]any{"hash": "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, "transactions": []any{map[string]any{"hash": "0x11"}}}
		}
		return nil
	})
	defer devCKB.Close()

	if err := VerifyMainnetSafe(context.Background(), NewRPCClient(), devCKB.URL, nil); err != nil {
		t.Fatalf("devnet CKB should pass mainnet guard, got %v", err)
	}
}

func TestVerifyHashOnChainCKBAndFiber(t *testing.T) {
	const (
		validTxHash  = "0x1111111111111111111111111111111111111111111111111111111111111111"
		validPayHash = "0x2222222222222222222222222222222222222222222222222222222222222222"
		validChID    = "0x3333333333333333333333333333333333333333333333333333333333333333"
		fakeHash     = "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	)

	ckbSrv := mockRPCServer(t, func(method string, params json.RawMessage) any {
		if method == "get_transaction" && strings.Contains(string(params), validTxHash) {
			return map[string]any{"tx_status": map[string]any{"status": "committed"}}
		}
		return nil
	})
	defer ckbSrv.Close()

	fnnSrv := mockRPCServer(t, func(method string, params json.RawMessage) any {
		switch method {
		case "get_payment":
			if strings.Contains(string(params), validPayHash) {
				return map[string]any{"payment_hash": validPayHash, "status": "Success"}
			}
			return map[string]any{}
		case "list_channels":
			return map[string]any{
				"channels": []any{
					map[string]any{
						"channel_id":       validChID,
						"channel_outpoint": validTxHash + "00000000",
						"state":            map[string]any{"state_name": "CHANNEL_READY"},
					},
				},
			}
		}
		return nil
	})
	defer fnnSrv.Close()

	rpc := NewRPCClient()
	if kind, ok := VerifyHashOnChain(context.Background(), rpc, validTxHash, ckbSrv.URL, []string{fnnSrv.URL}); !ok || !strings.Contains(kind, "committed") {
		t.Fatalf("expected validTxHash verified, got kind=%q ok=%v", kind, ok)
	}
	if kind, ok := VerifyHashOnChain(context.Background(), rpc, validPayHash, ckbSrv.URL, []string{fnnSrv.URL}); !ok || !strings.Contains(kind, "Success") {
		t.Fatalf("expected validPayHash verified, got kind=%q ok=%v", kind, ok)
	}
	if kind, ok := VerifyHashOnChain(context.Background(), rpc, validChID, ckbSrv.URL, []string{fnnSrv.URL}); !ok || !strings.Contains(kind, "CHANNEL_READY") {
		t.Fatalf("expected validChID verified, got kind=%q ok=%v", kind, ok)
	}
	if kind, ok := VerifyHashOnChain(context.Background(), rpc, fakeHash, ckbSrv.URL, []string{fnnSrv.URL}); ok {
		t.Fatalf("hallucinated hash must fail verification, got kind=%q", kind)
	}
}

func TestManifestPersistenceAndRenderConfigs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chain.json")
	m := Manifest{
		Network:   "ckb_dev",
		ChainHash: "0xabc",
		CKBRPCURL: "http://127.0.0.1:8114",
		NodeA:     NodeEndpoint{Name: "node-a", RPCURL: "http://127.0.0.1:8227", PeerID: "QmA"},
		NodeB:     NodeEndpoint{Name: "node-b", RPCURL: "http://127.0.0.1:8327", PeerID: "QmB"},
		CKBChannel: &ChannelSummary{
			ChannelID: "0x1111", State: "CHANNEL_READY", LocalBalance: "0x174876e800",
		},
		UDT: &UDTConfig{
			Name:     "TEST_XUDT",
			Decimals: 8,
			Script:   Script{CodeHash: XUDTDataHash, HashType: "data2", Args: DevNodeALockHash},
		},
	}
	if err := SaveManifest(path, m); err != nil {
		t.Fatalf("SaveManifest: %v", err)
	}
	loaded, err := LoadManifest(path)
	if err != nil || loaded == nil || loaded.Network != "ckb_dev" || loaded.UDT.Name != "TEST_XUDT" {
		t.Fatalf("LoadManifest mismatch: %+v err=%v", loaded, err)
	}
	prompt := loaded.FormatForPrompt(path)
	for _, want := range []string{"ckb_dev", "http://127.0.0.1:8227", "TEST_XUDT", "0x1111"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("FormatForPrompt missing %q:\n%s", want, prompt)
		}
	}

	devToml := RenderDevToml("/contracts")
	for _, want := range []string{`"/contracts/funding-lock"`, `"/contracts/xudt_rce"`, XUDTDataHash, DevUDTGenesisData} {
		if !strings.Contains(devToml, want) {
			t.Fatalf("RenderDevToml missing %q", want)
		}
	}

	fnnYML := RenderFNNConfig("fiber-a", "172.29.0.11", 8228, "172.29.0.11:8227", "http://ckb:8114", "0xdead")
	for _, want := range []string{"chain: /fiber/dev.toml", "172.29.0.11:8227", "/ip4/172.29.0.11/tcp/8228", "TEST_XUDT", "tx_hash: 0xdead", "index: 0x9", "auto_accept_amount: 0"} {
		if !strings.Contains(fnnYML, want) {
			t.Fatalf("RenderFNNConfig missing %q:\n%s", want, fnnYML)
		}
	}
}

func TestPatchCKBTomlModifiesActualModulesLineNotComment(t *testing.T) {
	p := filepath.Join(t.TempDir(), "ckb.toml")
	src := `listen_address = "127.0.0.1:8114"
# List of API modules: ["Net", "Pool", "Debug", "Indexer"]
modules = ["Net", "Pool", "Debug"]
`
	if err := os.WriteFile(p, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	if err := patchCKBToml(p); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(p)
	got := string(out)
	if !strings.Contains(got, `listen_address = "0.0.0.0:8114"`) {
		t.Fatalf("listen_address not patched:\n%s", got)
	}
	if !strings.Contains(got, `modules = ["Net", "Pool", "Debug", "Indexer", "IntegrationTest"]`) {
		t.Fatalf("modules line not patched accurately:\n%s", got)
	}
}
