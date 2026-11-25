// Package chain manages the self-contained local Docker CKB-devnet + 2-node
// Fiber (FNN) harness, mainnet refusal guards, and JSON-RPC verification helpers.
package chain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// Pinned official container images (overridable via env vars).
	DefaultCKBImage   = "nervos/ckb:v0.210.0"
	DefaultFiberImage = "nervos/fiber:0.10.1"

	// Pinned Fiber tag for devnet contract binaries.
	DefaultFiberContractTag = "v0.10.1"

	// Fixed local ports for the managed devnet harness.
	DefaultCKBPort   = 8114
	DefaultNodeAPort = 8227
	DefaultNodeBPort = 8327

	// CKB mainnet genesis block hash (Lina). Used alongside chain == "ckb"
	// to hard-refuse any --chain run pointed at mainnet.
	MainnetGenesisHash = "0x92b197aa1fba0f63633922c61c92375c9c074a93e85963554f5499fe1450d0e5"

	// Deterministic throwaway devnet keys (same well-known devnet keys used by
	// nervosnetwork/fiber's own dev-chain tests — NEVER use on mainnet/testnet).
	DevNodeAPrivKey  = "d00c06bfd800d27397002dca6fb0993d5ba6399b4238b2f29ee9deb97593d2bc"
	DevNodeALockArgs = "0xc8328aabcd9b9e8e64fbc566c4385c3bdeb219d7"
	DevNodeALockHash = "0x32e555f3ff8e135cece1351a6a2971518392c1e30375c1e006ad0ce8eac07947"

	DevNodeBPrivKey  = "63d86723e08f0f813a36ce6aa123bb2289d90680ae1e99d4de8cdb334553f24d"
	DevNodeBLockArgs = "0x470dcdc5e44064909650113a274b3b36aecb6dc7"

	// CKB blake2b-256 data hashes for Fiber v0.10.1 devnet system contracts.
	SimpleUDTDataHash = "0xe1e354d6d643ad42724d40967e334984534e0367405c5ae42a9d7d63d77df419"
	XUDTDataHash      = "0x50bd8d6680b8b9cf98b73f3c08faf8b2a21914311954118ad6609be6e78a1b95"

	// Pre-minted devnet xUDT amount per genesis cell: 10,000.00000000 TEST_XUDT
	// (1_000_000_000_000 base units = 0x0010a5d4e80000000000000000000000 LE u128).
	DevUDTGenesisData = "0x0010a5d4e80000000000000000000000"
)

// ContractSpec pins one Fiber v0.10.1 devnet contract binary by SHA-256.
type ContractSpec struct {
	Name   string
	SHA256 string
}

// DevContracts lists the exact Fiber v0.10.1 devnet system cell binaries
// verified by SHA-256 before writing into dev.toml.
var DevContracts = []ContractSpec{
	{Name: "auth", SHA256: "51d06e5c5b088424c51a6205eeede1a33054fd752a101cad0d545f32e01da33d"},
	{Name: "funding-lock", SHA256: "f87a7287e17a6ef828686129c143a79e8651b275e884edacbaa718590c227c96"},
	{Name: "commitment-lock", SHA256: "a7762721ef239b15adbdddca8d772db6e1c001d9f81e23ccc74e1392329931d7"},
	{Name: "simple_udt", SHA256: "b1a962cb43d88e777d9df0aa4e67270ee758d9abdd18b1c56b106a5eed149af9"},
	{Name: "xudt_rce", SHA256: "e9b92e5783f692f6ee99ca20eeda5f3da282e0f4010eb4fbd3db4e3058239349"},
}

// Script represents a CKB lock or type script.
type Script struct {
	CodeHash string `json:"code_hash"`
	HashType string `json:"hash_type"`
	Args     string `json:"args"`
}

// OutPoint identifies a specific CKB cell output.
type OutPoint struct {
	TxHash string `json:"tx_hash"`
	Index  string `json:"index"`
}

// CellDep identifies a CKB transaction cell dependency.
type CellDep struct {
	OutPoint OutPoint `json:"out_point"`
	DepType  string   `json:"dep_type"`
}

// NodeEndpoint captures a running FNN node's RPC URL and identity.
type NodeEndpoint struct {
	Name      string   `json:"name"`
	RPCURL    string   `json:"rpc_url"`
	NodeID    string   `json:"node_id,omitempty"`
	PeerID    string   `json:"peer_id,omitempty"`
	Addresses []string `json:"addresses,omitempty"`
	Version   string   `json:"version,omitempty"`
}

// ChannelSummary records a pre-bootstrapped Fiber channel between Node A and B.
type ChannelSummary struct {
	ChannelID      string `json:"channel_id"`
	State          string `json:"state"`
	FundingTxHash  string `json:"funding_tx_hash,omitempty"`
	LocalBalance   string `json:"local_balance,omitempty"`
	RemoteBalance  string `json:"remote_balance,omitempty"`
	UDTTypeScript  *Script `json:"udt_type_script,omitempty"`
}

// UDTConfig describes the pre-minted local devnet xUDT token.
type UDTConfig struct {
	Name     string  `json:"name"`
	Decimals int     `json:"decimals"`
	Script   Script  `json:"script"`
	CellDep  CellDep `json:"cell_dep"`
}

// Manifest is persisted at ~/.ckb-whitehat/chain.json when the local devnet
// harness is running, giving PoCs and `run` exact RPC URLs, channels, and UDTs.
type Manifest struct {
	Network    string          `json:"network"`
	ChainHash  string          `json:"chain_hash"`
	CKBRPCURL  string          `json:"ckb_rpc_url"`
	NodeA      NodeEndpoint    `json:"node_a"`
	NodeB      NodeEndpoint    `json:"node_b"`
	CKBChannel *ChannelSummary `json:"ckb_channel,omitempty"`
	UDT        *UDTConfig      `json:"udt,omitempty"`
	UDTChannel *ChannelSummary `json:"udt_channel,omitempty"`
	UpdatedAt  string          `json:"updated_at"`
}

// ManifestPath returns ~/.ckb-whitehat/chain.json for the given home dir.
func ManifestPath(home string) string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return filepath.Join(home, ".ckb-whitehat", "chain.json")
}

// SaveManifest writes m to path.
func SaveManifest(path string, m Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}

// LoadManifest reads path, returning (nil, nil) when the file does not exist.
func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// EnvVars returns environment variables exposing the running devnet harness to
// PoC scripts and agent subprocesses.
func (m *Manifest) EnvVars(manifestPath string) []string {
	if m == nil {
		return nil
	}
	env := []string{
		"CKB_WHITEHAT_CHAIN_CONFIG=" + manifestPath,
		"CKB_RPC_URL=" + m.CKBRPCURL,
		"FIBER_RPC_URL=" + m.NodeA.RPCURL,
		"FIBER_PEER_RPC_URL=" + m.NodeB.RPCURL,
	}
	if m.UDT != nil {
		if b, err := json.Marshal(m.UDT.Script); err == nil {
			env = append(env, "CKB_WHITEHAT_UDT_SCRIPT="+string(b))
		}
	}
	return env
}

// FormatForPrompt renders a concise summary of the live devnet harness for
// `ckb-whitehat run --chain`'s agent prompt.
func (m *Manifest) FormatForPrompt(manifestPath string) string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nLocal CKB/Fiber devnet harness (ready — ZERO pre-funded testnet addresses needed):\n")
	b.WriteString(fmt.Sprintf("- chain manifest : %s (also in $CKB_WHITEHAT_CHAIN_CONFIG)\n", manifestPath))
	b.WriteString(fmt.Sprintf("- CKB devnet RPC : %s (network=%s, chain_hash=%s, IntegrationTest generate_block enabled)\n", m.CKBRPCURL, m.Network, m.ChainHash))
	b.WriteString(fmt.Sprintf("- FNN Node A     : %s (operator node, $FIBER_RPC_URL, peer_id=%s)\n", m.NodeA.RPCURL, m.NodeA.PeerID))
	b.WriteString(fmt.Sprintf("- FNN Node B     : %s (receiver/attacker node, $FIBER_PEER_RPC_URL, peer_id=%s)\n", m.NodeB.RPCURL, m.NodeB.PeerID))
	if m.CKBChannel != nil {
		b.WriteString(fmt.Sprintf("- CKB channel    : %s (state=%s, local=%s, funding_tx=%s)\n", m.CKBChannel.ChannelID, m.CKBChannel.State, m.CKBChannel.LocalBalance, m.CKBChannel.FundingTxHash))
	}
	if m.UDT != nil {
		b.WriteString(fmt.Sprintf("- Test xUDT      : %s (%d decimals, code_hash=%s, hash_type=%s, args=%s)\n", m.UDT.Name, m.UDT.Decimals, m.UDT.Script.CodeHash, m.UDT.Script.HashType, m.UDT.Script.Args))
	}
	if m.UDTChannel != nil {
		b.WriteString(fmt.Sprintf("- xUDT channel   : %s (state=%s, local=%s, funding_tx=%s)\n", m.UDTChannel.ChannelID, m.UDTChannel.State, m.UDTChannel.LocalBalance, m.UDTChannel.FundingTxHash))
	}
	return b.String()
}

// --- JSON-RPC Client & Mainnet Guard ----------------------------------------

// RPCClient talks to local CKB and Fiber JSON-RPC endpoints.
type RPCClient struct {
	HTTP *http.Client
}

// NewRPCClient returns an RPCClient with sensible timeouts.
func NewRPCClient() *RPCClient {
	return &RPCClient{HTTP: &http.Client{Timeout: 10 * time.Second}}
}

type jsonRPCReq struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type jsonRPCResp struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Call executes one JSON-RPC 2.0 POST call and decodes result into out.
func (c *RPCClient) Call(ctx context.Context, url, method string, params any, out any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(jsonRPCReq{JSONRPC: "2.0", ID: 1, Method: method, Params: params})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4*1024*1024))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("rpc %s HTTP %d: %s", method, res.StatusCode, strings.TrimSpace(string(raw)))
	}
	var env jsonRPCResp
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("rpc %s decode: %w", method, err)
	}
	if env.Error != nil {
		return fmt.Errorf("rpc %s error %d: %s", method, env.Error.Code, env.Error.Message)
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("rpc %s unmarshal result: %w", method, err)
		}
	}
	return nil
}

// BlockchainInfo is the subset of CKB get_blockchain_info used by ckb-whitehat.
type BlockchainInfo struct {
	Chain  string `json:"chain"`
	Epoch  string `json:"epoch"`
	Median string `json:"median_time"`
}

func (c *RPCClient) CKBGetBlockchainInfo(ctx context.Context, url string) (BlockchainInfo, error) {
	var info BlockchainInfo
	err := c.Call(ctx, url, "get_blockchain_info", []any{}, &info)
	return info, err
}

func (c *RPCClient) CKBGetTipBlockNumber(ctx context.Context, url string) (uint64, error) {
	var hexNum string
	if err := c.Call(ctx, url, "get_tip_block_number", []any{}, &hexNum); err != nil {
		return 0, err
	}
	return parseHexUint64(hexNum)
}

type ckbBlock struct {
	Header struct {
		Hash   string `json:"hash"`
		Number string `json:"number"`
	} `json:"header"`
	Transactions []struct {
		Hash string `json:"hash"`
	} `json:"transactions"`
}

func (c *RPCClient) CKBGenesisInfo(ctx context.Context, url string) (blockHash, genesisTxHash string, err error) {
	var blk ckbBlock
	if err := c.Call(ctx, url, "get_block_by_number", []any{"0x0"}, &blk); err != nil {
		return "", "", err
	}
	if blk.Header.Hash == "" || len(blk.Transactions) == 0 {
		return "", "", errors.New("unexpected empty genesis block response")
	}
	return blk.Header.Hash, blk.Transactions[0].Hash, nil
}

func (c *RPCClient) CKBGenerateBlock(ctx context.Context, url string) (string, error) {
	var hash string
	err := c.Call(ctx, url, "generate_block", []any{}, &hash)
	return hash, err
}

// CKBTransactionStatus is the minimal result of CKB get_transaction.
type CKBTransactionStatus struct {
	TxStatus struct {
		Status    string `json:"status"`
		BlockHash string `json:"block_hash"`
	} `json:"tx_status"`
}

func (c *RPCClient) CKBGetTransaction(ctx context.Context, url, txHash string) (*CKBTransactionStatus, error) {
	var raw json.RawMessage
	if err := c.Call(ctx, url, "get_transaction", []any{txHash}, &raw); err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(raw)) == "null" || len(raw) == 0 {
		return nil, nil
	}
	var st CKBTransactionStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	if st.TxStatus.Status == "" || st.TxStatus.Status == "unknown" {
		return nil, nil
	}
	return &st, nil
}

// FNNNodeInfoResp holds the relevant fields of Fiber's node_info RPC
// (supporting both v0.10+ `pubkey` and legacy v0.8/v0.9 `node_id`).
type FNNNodeInfoResp struct {
	Version    string   `json:"version"`
	Pubkey     string   `json:"pubkey"`
	NodeID     string   `json:"node_id"`
	Addresses  []string `json:"addresses"`
	ChainHash  string   `json:"chain_hash"`
	ChannelCnt string   `json:"channel_count"`
}

// IdentityPubkey returns the node's compressed secp256k1 pubkey across FNN versions.
func (r FNNNodeInfoResp) IdentityPubkey() string {
	if r.Pubkey != "" {
		return r.Pubkey
	}
	return r.NodeID
}

func (c *RPCClient) FNNNodeInfo(ctx context.Context, url string) (FNNNodeInfoResp, error) {
	var resp FNNNodeInfoResp
	err := c.Call(ctx, url, "node_info", []any{}, &resp)
	return resp, err
}

func (c *RPCClient) FNNConnectPeer(ctx context.Context, url, address string) error {
	return c.Call(ctx, url, "connect_peer", []any{map[string]any{"address": address, "save": true}}, nil)
}

type FNNChannel struct {
	ChannelID       string          `json:"channel_id"`
	ChannelOutpoint string          `json:"channel_outpoint"`
	State           json.RawMessage `json:"state"`
	LocalBalance    string          `json:"local_balance"`
	RemoteBalance   string          `json:"remote_balance"`
	FundingUDT      *Script         `json:"funding_udt_type_script"`
}

// StateName extracts the channel state string whether FNN returns `"CHANNEL_READY"`
// or `{"state_name":"CHANNEL_READY"}` (FNN v0.8–v0.10 compatibility).
func (ch FNNChannel) StateName() string {
	var s string
	if json.Unmarshal(ch.State, &s) == nil && s != "" {
		return s
	}
	var obj struct {
		StateName string `json:"state_name"`
	}
	if json.Unmarshal(ch.State, &obj) == nil {
		return obj.StateName
	}
	return ""
}

// IsChannelReady reports whether a Fiber channel state string represents an
// operational channel (matching both legacy `CHANNEL_READY` and v0.10+ `ChannelReady`).
func IsChannelReady(state string) bool {
	norm := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(state), "_", ""))
	return norm == "channelready"
}

// FundingTxHash extracts the 32-byte tx hash prefix from channel_outpoint
// (which is 36 bytes: 32-byte tx_hash + 4-byte LE index).
func (ch FNNChannel) FundingTxHash() string {
	h := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ch.ChannelOutpoint)), "0x")
	if len(h) >= 64 {
		return "0x" + h[:64]
	}
	return ""
}

func (c *RPCClient) FNNListChannels(ctx context.Context, url string) ([]FNNChannel, error) {
	var resp struct {
		Channels []FNNChannel `json:"channels"`
	}
	err := c.Call(ctx, url, "list_channels", []any{map[string]any{"include_closed": true}}, &resp)
	return resp.Channels, err
}

func (c *RPCClient) FNNOpenChannel(ctx context.Context, url, pubkey, fundingAmountHex string, udt *Script) (string, error) {
	params := map[string]any{
		"pubkey":         pubkey,
		"funding_amount": fundingAmountHex,
		"public":         true,
	}
	if udt != nil {
		params["funding_udt_type_script"] = udt
	}
	var resp struct {
		TemporaryChannelID string `json:"temporary_channel_id"`
	}
	err := c.Call(ctx, url, "open_channel", []any{params}, &resp)
	return resp.TemporaryChannelID, err
}

// FNNPaymentStatus holds the minimal fields of Fiber's get_payment RPC.
type FNNPaymentStatus struct {
	PaymentHash string `json:"payment_hash"`
	Status      string `json:"status"`
}

func (c *RPCClient) FNNGetPayment(ctx context.Context, url, paymentHash string) (*FNNPaymentStatus, error) {
	var resp FNNPaymentStatus
	if err := c.Call(ctx, url, "get_payment", []any{map[string]any{"payment_hash": paymentHash}}, &resp); err != nil {
		return nil, err
	}
	if resp.Status == "" {
		return nil, nil
	}
	return &resp, nil
}

// VerifyMainnetSafe checks any reachable CKB and FNN RPC endpoints and returns
// a non-nil error if ANY endpoint is connected to CKB mainnet ("ckb" / Lina
// genesis hash). Unreachable endpoints are ignored (caller checks readiness).
func VerifyMainnetSafe(ctx context.Context, rpc *RPCClient, ckbURL string, fnnURLs []string) error {
	if rpc == nil {
		rpc = NewRPCClient()
	}
	if ckbURL != "" {
		if info, err := rpc.CKBGetBlockchainInfo(ctx, ckbURL); err == nil {
			if strings.EqualFold(strings.TrimSpace(info.Chain), "ckb") {
				return fmt.Errorf("REFUSING --chain run: CKB endpoint %s is CKB MAINNET (chain=%q); use local devnet (`ckb-whitehat chain up`) instead", ckbURL, info.Chain)
			}
		}
		if hash, _, err := rpc.CKBGenesisInfo(ctx, ckbURL); err == nil {
			if strings.EqualFold(strings.TrimSpace(hash), MainnetGenesisHash) {
				return fmt.Errorf("REFUSING --chain run: CKB endpoint %s matches CKB MAINNET genesis hash %s", ckbURL, MainnetGenesisHash)
			}
		}
	}
	for _, u := range fnnURLs {
		if u == "" {
			continue
		}
		if info, err := rpc.FNNNodeInfo(ctx, u); err == nil {
			if strings.EqualFold(strings.TrimSpace(info.ChainHash), MainnetGenesisHash) {
				return fmt.Errorf("REFUSING --chain run: Fiber node %s is connected to CKB MAINNET (chain_hash=%s)", u, info.ChainHash)
			}
		}
	}
	return nil
}

// CKBGetBlock queries CKB `get_block` for a 32-byte block hash (used to
// distinguish legitimate cited block hashes from hallucinated tx hashes).
func (c *RPCClient) CKBGetBlock(ctx context.Context, url, hash string) (bool, error) {
	var raw json.RawMessage
	if err := c.Call(ctx, url, "get_block", []any{hash}, &raw); err != nil {
		return false, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false, nil
	}
	var blk ckbBlock
	if err := json.Unmarshal(raw, &blk); err != nil {
		return false, nil
	}
	return strings.TrimSpace(blk.Header.Hash) != "", nil
}

// VerifyHashOnChain checks whether a 32-byte 0x-prefixed hash corresponds to a
// real committed/proposed CKB transaction, a settled Fiber payment (`Success`),
// a known Fiber channel/funding tx, or a CKB block on the local CKB/FNN endpoints.
func VerifyHashOnChain(ctx context.Context, rpc *RPCClient, hash, ckbURL string, fnnURLs []string) (kind string, ok bool) {
	if rpc == nil {
		rpc = NewRPCClient()
	}
	norm := strings.ToLower(strings.TrimSpace(hash))
	if ckbURL != "" {
		if st, err := rpc.CKBGetTransaction(ctx, ckbURL, norm); err == nil && st != nil {
			s := strings.ToLower(st.TxStatus.Status)
			if s == "committed" || s == "proposed" {
				return "ckb_tx(" + s + ")", true
			}
		}
	}
	for _, u := range fnnURLs {
		if u == "" {
			continue
		}
		if pay, err := rpc.FNNGetPayment(ctx, u, norm); err == nil && pay != nil {
			if strings.EqualFold(pay.Status, "Success") {
				return "fiber_payment(" + pay.Status + ")", true
			}
		}
		if chs, err := rpc.FNNListChannels(ctx, u); err == nil {
			for _, ch := range chs {
				if strings.EqualFold(ch.ChannelID, norm) {
					return "fiber_channel(" + ch.StateName() + ")", true
				}
				if ftx := ch.FundingTxHash(); ftx != "" && strings.EqualFold(ftx, norm) {
					return "fiber_funding_tx(" + ch.StateName() + ")", true
				}
			}
		}
	}
	if ckbURL != "" {
		if found, err := rpc.CKBGetBlock(ctx, ckbURL, norm); err == nil && found {
			return "ckb_block", true
		}
	}
	return "", false
}

// PeerIDFromAddrs extracts the trailing `/p2p/<peer_id>` component from a
// Fiber multiaddr list.
func PeerIDFromAddrs(addrs []string) string {
	for _, a := range addrs {
		if idx := strings.LastIndex(a, "/p2p/"); idx >= 0 {
			if id := strings.TrimSpace(a[idx+len("/p2p/"):]); id != "" {
				return id
			}
		}
	}
	return ""
}

func parseHexUint64(s string) (uint64, error) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	if s == "" {
		return 0, nil
	}
	return strconv.ParseUint(s, 16, 64)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
