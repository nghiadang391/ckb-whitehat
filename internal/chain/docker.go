package chain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	dockerNetworkName   = "ckb-whitehat-net"
	dockerNetworkSubnet = "172.29.0.0/24"
	ckbContainerIP      = "172.29.0.10"
	fnnAContainerIP     = "172.29.0.11"
	fnnBContainerIP     = "172.29.0.12"

	ckbContainerName  = "ckb-whitehat-ckb"
	fnnAContainerName = "ckb-whitehat-fnn-a"
	fnnBContainerName = "ckb-whitehat-fnn-b"

	fnnDevPassword = "ckb-whitehat-dev-password"

	// Channel funding amounts on devnet:
	// CKB channel: 1,000 CKB = 100_000_000_000 shannons = 0x174876e800
	devCKBChannelFunding = "0x174876e800"
	// xUDT channel: 100.00000000 TEST_XUDT = 10_000_000_000 base units = 0x2540be400
	devUDTChannelFunding = "0x2540be400"
)

// Harness orchestrates the local Docker CKB-devnet + 2-node FNN stack.
type Harness struct {
	Home       string
	CKBImage   string
	FiberImage string
	RPC        *RPCClient
	HTTP       *http.Client
}

// NewHarness returns a Harness rooted at ~/.ckb-whitehat (or home/.ckb-whitehat).
func NewHarness(home string) *Harness {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	ckbImg := os.Getenv("CKB_WHITEHAT_CKB_IMAGE")
	if ckbImg == "" {
		ckbImg = DefaultCKBImage
	}
	fiberImg := os.Getenv("CKB_WHITEHAT_FIBER_IMAGE")
	if fiberImg == "" {
		fiberImg = DefaultFiberImage
	}
	return &Harness{
		Home:       home,
		CKBImage:   ckbImg,
		FiberImage: fiberImg,
		RPC:        NewRPCClient(),
		HTTP:       &http.Client{Timeout: 30 * time.Second},
	}
}

func (h *Harness) baseDir() string      { return filepath.Join(h.Home, ".ckb-whitehat", "chain") }
func (h *Harness) contractsDir() string { return filepath.Join(h.baseDir(), "contracts") }
func (h *Harness) dataDir() string      { return filepath.Join(h.baseDir(), "data") }
func (h *Harness) ManifestPath() string { return ManifestPath(h.Home) }

// StatusReport summarizes the live state of the Docker devnet + 2-node FNN stack.
type StatusReport struct {
	DockerReady bool
	CKBRunning  bool
	CKBChain    string
	CKBTip      uint64
	NodeAReady  bool
	NodeA       NodeEndpoint
	NodeBReady  bool
	NodeB       NodeEndpoint
	Channels    []ChannelSummary
	Manifest    *Manifest
}

// Ready reports whether CKB devnet, both FNN nodes, and at least one ready
// channel are live.
func (s StatusReport) Ready() bool {
	if !s.CKBRunning || !s.NodeAReady || !s.NodeBReady {
		return false
	}
	for _, ch := range s.Channels {
		if IsChannelReady(ch.State) {
			return true
		}
	}
	return false
}

// DockerAvailable checks whether `docker info` succeeds on the host.
func DockerAvailable(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "docker", "info", "--format", "{{.ServerVersion}}")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker is not available (%v): %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Status probes the live CKB devnet and 2 FNN nodes without mutating anything.
func (h *Harness) Status(ctx context.Context) StatusReport {
	var rep StatusReport
	rep.DockerReady = DockerAvailable(ctx) == nil
	rep.Manifest, _ = LoadManifest(h.ManifestPath())

	ckbURL := fmt.Sprintf("http://127.0.0.1:%d", DefaultCKBPort)
	nodeAURL := fmt.Sprintf("http://127.0.0.1:%d", DefaultNodeAPort)
	nodeBURL := fmt.Sprintf("http://127.0.0.1:%d", DefaultNodeBPort)
	if rep.Manifest != nil {
		if rep.Manifest.CKBRPCURL != "" {
			ckbURL = rep.Manifest.CKBRPCURL
		}
		if rep.Manifest.NodeA.RPCURL != "" {
			nodeAURL = rep.Manifest.NodeA.RPCURL
		}
		if rep.Manifest.NodeB.RPCURL != "" {
			nodeBURL = rep.Manifest.NodeB.RPCURL
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if info, err := h.RPC.CKBGetBlockchainInfo(probeCtx, ckbURL); err == nil && info.Chain != "" {
		rep.CKBRunning = true
		rep.CKBChain = info.Chain
		if tip, terr := h.RPC.CKBGetTipBlockNumber(probeCtx, ckbURL); terr == nil {
			rep.CKBTip = tip
		}
	}
	if na, err := h.RPC.FNNNodeInfo(probeCtx, nodeAURL); err == nil && na.Version != "" {
		rep.NodeAReady = true
		rep.NodeA = NodeEndpoint{
			Name:      "node-a",
			RPCURL:    nodeAURL,
			NodeID:    na.IdentityPubkey(),
			PeerID:    PeerIDFromAddrs(na.Addresses),
			Addresses: na.Addresses,
			Version:   na.Version,
		}
		if chs, cerr := h.RPC.FNNListChannels(probeCtx, nodeAURL); cerr == nil {
			for _, ch := range chs {
				rep.Channels = append(rep.Channels, ChannelSummary{
					ChannelID:     ch.ChannelID,
					State:         ch.StateName(),
					FundingTxHash: ch.FundingTxHash(),
					LocalBalance:  ch.LocalBalance,
					RemoteBalance: ch.RemoteBalance,
					UDTTypeScript: ch.FundingUDT,
				})
			}
		}
	}
	if nb, err := h.RPC.FNNNodeInfo(probeCtx, nodeBURL); err == nil && nb.Version != "" {
		rep.NodeBReady = true
		rep.NodeB = NodeEndpoint{
			Name:      "node-b",
			RPCURL:    nodeBURL,
			NodeID:    nb.IdentityPubkey(),
			PeerID:    PeerIDFromAddrs(nb.Addresses),
			Addresses: nb.Addresses,
			Version:   nb.Version,
		}
	}
	return rep
}

// Up ensures the local Docker CKB-devnet + 2-node FNN harness is running with
// pre-funded CKB and TEST_XUDT channels, writing ~/.ckb-whitehat/chain.json.
// Idempotent: if the harness is already running and healthy, returns its Manifest.
func (h *Harness) Up(ctx context.Context, log io.Writer) (*Manifest, error) {
	if log == nil {
		log = io.Discard
	}
	if err := DockerAvailable(ctx); err != nil {
		return nil, err
	}

	// Fast path: already up and healthy on devnet.
	st := h.Status(ctx)
	if st.Ready() && st.Manifest != nil && st.CKBChain == "ckb_dev" {
		fmt.Fprintf(log, "local CKB/Fiber devnet harness is already running (tip=%d, channels=%d)\n", st.CKBTip, len(st.Channels))
		return st.Manifest, nil
	}

	fmt.Fprintln(log, "starting local CKB devnet + 2-node Fiber (FNN) harness...")
	if err := h.EnsureContracts(ctx, log); err != nil {
		return nil, fmt.Errorf("fetch Fiber devnet contracts: %w", err)
	}

	// Clean any stale partial containers/data from a prior interrupted run.
	_ = h.Down(ctx, io.Discard)

	ckbDir := filepath.Join(h.dataDir(), "ckb")
	fnnADir := filepath.Join(h.dataDir(), "fnn-a")
	fnnBDir := filepath.Join(h.dataDir(), "fnn-b")
	for _, d := range []string{
		ckbDir,
		filepath.Join(ckbDir, "specs"),
		filepath.Join(fnnADir, "ckb"),
		filepath.Join(fnnBDir, "ckb"),
	} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return nil, err
		}
	}

	// 1. Initialize CKB devnet config inside container.
	fmt.Fprintf(log, "  [1/5] initializing CKB devnet (%s)...\n", h.CKBImage)
	if err := runDocker(ctx, "network", "create", "--subnet", dockerNetworkSubnet, dockerNetworkName); err != nil && !strings.Contains(err.Error(), "already exists") {
		return nil, err
	}
	if err := runDocker(ctx, "run", "--rm",
		"-v", ckbDir+":/var/lib/ckb",
		h.CKBImage,
		"init", "-C", "/var/lib/ckb", "--chain", "dev", "--force",
		"--ba-arg", DevNodeALockArgs,
	); err != nil {
		return nil, fmt.Errorf("ckb init: %w", err)
	}

	// Write custom dev.toml with Fiber system cells + pre-funded CKB & xUDT cells.
	devTomlCKB := RenderDevToml("/contracts")
	if err := os.WriteFile(filepath.Join(ckbDir, "specs", "dev.toml"), []byte(devTomlCKB), 0644); err != nil {
		return nil, err
	}
	if err := patchCKBToml(filepath.Join(ckbDir, "ckb.toml")); err != nil {
		return nil, err
	}

	// 2. Start CKB devnet node (with indexer + IntegrationTest RPC module).
	if err := runDocker(ctx, "run", "-d",
		"--name", ckbContainerName,
		"--network", dockerNetworkName,
		"--ip", ckbContainerIP,
		"-p", fmt.Sprintf("127.0.0.1:%d:8114", DefaultCKBPort),
		"-v", ckbDir+":/var/lib/ckb",
		"-v", h.contractsDir()+":/contracts:ro",
		h.CKBImage,
		"run", "-C", "/var/lib/ckb", "--indexer",
	); err != nil {
		return nil, fmt.Errorf("start ckb container: %w", err)
	}

	ckbURL := fmt.Sprintf("http://127.0.0.1:%d", DefaultCKBPort)
	if err := h.waitCKBReady(ctx, ckbURL, 60*time.Second); err != nil {
		return nil, err
	}
	if err := VerifyMainnetSafe(ctx, h.RPC, ckbURL, nil); err != nil {
		return nil, err
	}

	chainHash, genesisTxHash, err := h.RPC.CKBGenesisInfo(ctx, ckbURL)
	if err != nil {
		return nil, fmt.Errorf("read genesis block: %w", err)
	}

	// Mine initial blocks past bootstrap window so genesis/coinbase cells are spendable.
	for i := 0; i < 12; i++ {
		if _, err := h.RPC.CKBGenerateBlock(ctx, ckbURL); err != nil {
			return nil, fmt.Errorf("generate initial devnet block %d: %w", i+1, err)
		}
	}

	udtScript := Script{
		CodeHash: XUDTDataHash,
		HashType: "data2",
		Args:     DevNodeALockHash,
	}
	udtCellDep := CellDep{
		OutPoint: OutPoint{TxHash: genesisTxHash, Index: "0x9"},
		DepType:  "code",
	}

	// 3. Configure and start FNN Node A (operator) and Node B (receiver/attacker).
	// Each node binds RPC to its private RFC1918 bridge IP (172.29.0.11 / .12)
	// because FNN v0.10+ refuses 0.0.0.0 without biscuit auth, while still
	// allowing Docker -p 127.0.0.1:<port>:8227 forwarding from the host.
	fmt.Fprintf(log, "  [2/5] starting 2 Fiber (FNN) nodes (%s)...\n", h.FiberImage)
	devTomlFNN := RenderDevToml("/contracts")
	for _, n := range []struct {
		name      string
		dir       string
		privKey   string
		p2pPort   int
		rpcHost   int
		ip        string
		container string
	}{
		{"fiber-a", fnnADir, DevNodeAPrivKey, 8228, DefaultNodeAPort, fnnAContainerIP, fnnAContainerName},
		{"fiber-b", fnnBDir, DevNodeBPrivKey, 8328, DefaultNodeBPort, fnnBContainerIP, fnnBContainerName},
	} {
		if err := os.WriteFile(filepath.Join(n.dir, "dev.toml"), []byte(devTomlFNN), 0644); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(n.dir, "ckb", "key"), []byte(n.privKey+"\n"), 0600); err != nil {
			return nil, err
		}
		cfgYML := RenderFNNConfig(n.name, n.ip, n.p2pPort, n.ip+":8227", "http://"+ckbContainerIP+":8114", genesisTxHash)
		if err := os.WriteFile(filepath.Join(n.dir, "config.yml"), []byte(cfgYML), 0644); err != nil {
			return nil, err
		}
		if err := runDocker(ctx, "run", "-d",
			"--name", n.container,
			"--network", dockerNetworkName,
			"--ip", n.ip,
			"-e", "FIBER_SECRET_KEY_PASSWORD="+fnnDevPassword,
			"-e", "RUST_LOG=info",
			"-p", fmt.Sprintf("127.0.0.1:%d:8227", n.rpcHost),
			"-v", n.dir+":/fiber",
			"-v", h.contractsDir()+":/contracts:ro",
			h.FiberImage,
		); err != nil {
			return nil, fmt.Errorf("start %s container: %w", n.container, err)
		}
	}

	nodeAURL := fmt.Sprintf("http://127.0.0.1:%d", DefaultNodeAPort)
	nodeBURL := fmt.Sprintf("http://127.0.0.1:%d", DefaultNodeBPort)
	infoA, err := h.waitFNNReady(ctx, nodeAURL, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("FNN Node A ready: %w", err)
	}
	infoB, err := h.waitFNNReady(ctx, nodeBURL, 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("FNN Node B ready: %w", err)
	}
	if err := VerifyMainnetSafe(ctx, h.RPC, ckbURL, []string{nodeAURL, nodeBURL}); err != nil {
		return nil, err
	}

	peerA := PeerIDFromAddrs(infoA.Addresses)
	peerB := PeerIDFromAddrs(infoB.Addresses)
	if peerB == "" {
		return nil, fmt.Errorf("could not extract peer_id from Node B addresses: %v", infoB.Addresses)
	}

	// 4. Peer Node A -> Node B over the Docker bridge IP.
	fmt.Fprintln(log, "  [3/5] peering Node A <-> Node B...")
	addrB := fmt.Sprintf("/ip4/%s/tcp/8328/p2p/%s", fnnBContainerIP, peerB)
	if err := h.RPC.FNNConnectPeer(ctx, nodeAURL, addrB); err != nil {
		return nil, fmt.Errorf("connect_peer A->B (%s): %w", addrB, err)
	}
	time.Sleep(1 * time.Second)

	// 5. Open & confirm CKB channel + TEST_XUDT channel A -> B.
	fmt.Fprintln(log, "  [4/5] opening & confirming pre-funded CKB channel (1,000 CKB)...")
	ckbCh, err := h.openAndConfirmChannel(ctx, ckbURL, nodeAURL, infoB.IdentityPubkey(), devCKBChannelFunding, nil)
	if err != nil {
		return nil, fmt.Errorf("open CKB channel: %w", err)
	}

	fmt.Fprintln(log, "  [5/5] opening & confirming pre-funded TEST_XUDT channel (100 TEST_XUDT)...")
	udtCh, err := h.openAndConfirmChannel(ctx, ckbURL, nodeAURL, infoB.IdentityPubkey(), devUDTChannelFunding, &udtScript)
	if err != nil {
		return nil, fmt.Errorf("open TEST_XUDT channel: %w", err)
	}

	manifest := &Manifest{
		Network:   "ckb_dev",
		ChainHash: chainHash,
		CKBRPCURL: ckbURL,
		NodeA: NodeEndpoint{
			Name:      "node-a",
			RPCURL:    nodeAURL,
			NodeID:    infoA.IdentityPubkey(),
			PeerID:    peerA,
			Addresses: infoA.Addresses,
			Version:   infoA.Version,
		},
		NodeB: NodeEndpoint{
			Name:      "node-b",
			RPCURL:    nodeBURL,
			NodeID:    infoB.IdentityPubkey(),
			PeerID:    peerB,
			Addresses: infoB.Addresses,
			Version:   infoB.Version,
		},
		CKBChannel: ckbCh,
		UDT: &UDTConfig{
			Name:     "TEST_XUDT",
			Decimals: 8,
			Script:   udtScript,
			CellDep:  udtCellDep,
		},
		UDTChannel: udtCh,
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if err := SaveManifest(h.ManifestPath(), *manifest); err != nil {
		return nil, err
	}
	fmt.Fprintf(log, "local CKB/Fiber devnet harness ready -> %s\n", h.ManifestPath())
	return manifest, nil
}

// Down stops and removes all managed devnet containers, Docker network,
// devnet data dir, and chain.json (preserving SHA-256-verified cached contracts).
func (h *Harness) Down(ctx context.Context, log io.Writer) error {
	if log == nil {
		log = io.Discard
	}
	for _, name := range []string{fnnBContainerName, fnnAContainerName, ckbContainerName} {
		_ = runDocker(ctx, "rm", "-f", name)
	}
	_ = runDocker(ctx, "network", "rm", dockerNetworkName)
	// Clean root-owned container files inside dataDir via a tiny helper if needed,
	// falling back to os.RemoveAll.
	if _, err := os.Stat(h.dataDir()); err == nil {
		if err := os.RemoveAll(h.dataDir()); err != nil {
			_ = runDocker(ctx, "run", "--rm", "--network=none", "--user", "0:0", "--entrypoint", "sh", "-v", h.dataDir()+":/clean",
				h.CKBImage, "-c", "rm -rf /clean/* /clean/.[!.]* /clean/..?* 2>/dev/null || true")
			_ = os.RemoveAll(h.dataDir())
		}
	}
	_ = os.Remove(h.ManifestPath())
	fmt.Fprintln(log, "stopped local CKB/Fiber devnet harness")
	return nil
}

// EnsureContracts downloads and SHA-256 verifies the 5 official Fiber v0.10.1
// devnet contract binaries into ~/.ckb-whitehat/chain/contracts/.
func (h *Harness) EnsureContracts(ctx context.Context, log io.Writer) error {
	if err := os.MkdirAll(h.contractsDir(), 0755); err != nil {
		return err
	}
	client := h.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	for _, spec := range DevContracts {
		dst := filepath.Join(h.contractsDir(), spec.Name)
		if b, err := os.ReadFile(dst); err == nil && sha256Hex(b) == spec.SHA256 {
			continue
		}
		url := fmt.Sprintf("https://raw.githubusercontent.com/nervosnetwork/fiber/%s/tests/deploy/contracts/%s", DefaultFiberContractTag, spec.Name)
		if log != nil {
			fmt.Fprintf(log, "  downloading verified Fiber contract %s...\n", spec.Name)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		res, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("download %s: %w", spec.Name, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024))
		res.Body.Close()
		if readErr != nil {
			return readErr
		}
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("download %s: HTTP %d", spec.Name, res.StatusCode)
		}
		if got := sha256Hex(body); got != spec.SHA256 {
			return fmt.Errorf("contract %s SHA-256 mismatch: got %s, want %s", spec.Name, got, spec.SHA256)
		}
		if err := os.WriteFile(dst, body, 0644); err != nil {
			return err
		}
	}
	return nil
}

func (h *Harness) waitCKBReady(ctx context.Context, url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if info, err := h.RPC.CKBGetBlockchainInfo(ctx, url); err == nil && info.Chain != "" {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for CKB RPC at %s", url)
}

func (h *Harness) waitFNNReady(ctx context.Context, url string, timeout time.Duration) (FNNNodeInfoResp, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return FNNNodeInfoResp{}, ctx.Err()
		}
		if info, err := h.RPC.FNNNodeInfo(ctx, url); err == nil && info.Version != "" {
			return info, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return FNNNodeInfoResp{}, fmt.Errorf("timed out waiting for FNN RPC at %s", url)
}

func (h *Harness) openAndConfirmChannel(ctx context.Context, ckbURL, nodeAURL, peerB, amountHex string, udt *Script) (*ChannelSummary, error) {
	before := map[string]bool{}
	if existing, err := h.RPC.FNNListChannels(ctx, nodeAURL); err == nil {
		for _, ch := range existing {
			before[ch.ChannelID] = true
		}
	}
	if _, err := h.RPC.FNNOpenChannel(ctx, nodeAURL, peerB, amountHex, udt); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		_, _ = h.RPC.CKBGenerateBlock(ctx, ckbURL)
		time.Sleep(700 * time.Millisecond)
		chs, err := h.RPC.FNNListChannels(ctx, nodeAURL)
		if err != nil {
			continue
		}
		for _, ch := range chs {
			if before[ch.ChannelID] {
				continue
			}
			if IsChannelReady(ch.StateName()) {
				return &ChannelSummary{
					ChannelID:     ch.ChannelID,
					State:         ch.StateName(),
					FundingTxHash: ch.FundingTxHash(),
					LocalBalance:  ch.LocalBalance,
					RemoteBalance: ch.RemoteBalance,
					UDTTypeScript: ch.FundingUDT,
				}, nil
			}
		}
	}
	return nil, errors.New("timed out waiting for Fiber channel to reach CHANNEL_READY")
}

// RenderDevToml produces a CKB devnet chain spec with Fiber system contracts
// (outputs 5..9: auth, funding-lock, commitment-lock, simple_udt, xudt_rce)
// and pre-funded CKB + TEST_XUDT genesis cells for Node A and Node B.
func RenderDevToml(contractsMount string) string {
	return fmt.Sprintf(`name = "ckb_dev"

[genesis]
version = 0
parent_hash = "0x0000000000000000000000000000000000000000000000000000000000000000"
timestamp = 0
compact_target = 0x20010000
uncles_hash = "0x0000000000000000000000000000000000000000000000000000000000000000"
nonce = "0x0"

[genesis.genesis_cell]
message = "ckb_dev"

[genesis.genesis_cell.lock]
code_hash = "0x0000000000000000000000000000000000000000000000000000000000000000"
args = "0x"
hash_type = "data"

[[genesis.system_cells]]
file = { bundled = "specs/cells/secp256k1_blake160_sighash_all" }
create_type_id = true
capacity = 100_000_0000_0000
[[genesis.system_cells]]
file = { bundled = "specs/cells/dao" }
create_type_id = true
capacity = 16_000_0000_0000
[[genesis.system_cells]]
file = { bundled = "specs/cells/secp256k1_data" }
create_type_id = false
capacity = 1_048_617_0000_0000
[[genesis.system_cells]]
file = { bundled = "specs/cells/secp256k1_blake160_multisig_all" }
create_type_id = true
capacity = 100_000_0000_0000
[[genesis.system_cells]]
file = { file = "%s/auth" }
create_type_id = false
capacity = 200_000_0000_0000
[[genesis.system_cells]]
file = { file = "%s/funding-lock" }
create_type_id = false
capacity = 200_000_0000_0000
[[genesis.system_cells]]
file = { file = "%s/commitment-lock" }
create_type_id = false
capacity = 200_000_0000_0000
[[genesis.system_cells]]
file = { file = "%s/simple_udt" }
create_type_id = false
capacity = 200_000_0000_0000
[[genesis.system_cells]]
file = { file = "%s/xudt_rce" }
create_type_id = false
capacity = 200_000_0000_0000

[genesis.system_cells_lock]
code_hash = "0x0000000000000000000000000000000000000000000000000000000000000000"
args = "0x"
hash_type = "data"

[[genesis.dep_groups]]
name = "secp256k1_blake160_sighash_all"
files = [
  { bundled = "specs/cells/secp256k1_data" },
  { bundled = "specs/cells/secp256k1_blake160_sighash_all" },
]
[[genesis.dep_groups]]
name = "secp256k1_blake160_multisig_all"
files = [
  { bundled = "specs/cells/secp256k1_data" },
  { bundled = "specs/cells/secp256k1_blake160_multisig_all" },
]

[genesis.bootstrap_lock]
code_hash = "0x0000000000000000000000000000000000000000000000000000000000000000"
args = "0x"
hash_type = "type"

# Burn cell
[[genesis.issued_cells]]
capacity = 8_400_000_000_00000000
lock.code_hash = "0x0000000000000000000000000000000000000000000000000000000000000000"
lock.args = "0x62e907b15cbf27d5425399ebf6f0fb50ebb88f18"
lock.hash_type = "data"

# Node A (operator) — multiple ready CKB cells
[[genesis.issued_cells]]
capacity = 5_000_000_000_00000000
lock.code_hash = "0x9bd7e06f3ecf4be0f2fcd2188b23f1b9fcc88e5d4b65a8637b17723bbda3cce8"
lock.args = "%s"
lock.hash_type = "type"

[[genesis.issued_cells]]
capacity = 5_000_000_000_00000000
lock.code_hash = "0x9bd7e06f3ecf4be0f2fcd2188b23f1b9fcc88e5d4b65a8637b17723bbda3cce8"
lock.args = "%s"
lock.hash_type = "type"

# Node A — pre-minted TEST_XUDT cells (10,000.00000000 TEST_XUDT each)
[[genesis.issued_cells]]
capacity = 500_00000000
lock.code_hash = "0x9bd7e06f3ecf4be0f2fcd2188b23f1b9fcc88e5d4b65a8637b17723bbda3cce8"
lock.args = "%s"
lock.hash_type = "type"
type.code_hash = "%s"
type.args = "%s"
type.hash_type = "data2"
data = "%s"

[[genesis.issued_cells]]
capacity = 500_00000000
lock.code_hash = "0x9bd7e06f3ecf4be0f2fcd2188b23f1b9fcc88e5d4b65a8637b17723bbda3cce8"
lock.args = "%s"
lock.hash_type = "type"
type.code_hash = "%s"
type.args = "%s"
type.hash_type = "data2"
data = "%s"

# Node B (receiver/attacker) — multiple ready CKB cells
[[genesis.issued_cells]]
capacity = 2_500_000_000_00000000
lock.code_hash = "0x9bd7e06f3ecf4be0f2fcd2188b23f1b9fcc88e5d4b65a8637b17723bbda3cce8"
lock.args = "%s"
lock.hash_type = "type"

[[genesis.issued_cells]]
capacity = 2_500_000_000_00000000
lock.code_hash = "0x9bd7e06f3ecf4be0f2fcd2188b23f1b9fcc88e5d4b65a8637b17723bbda3cce8"
lock.args = "%s"
lock.hash_type = "type"

# Node B — pre-minted TEST_XUDT cell (10,000.00000000 TEST_XUDT)
[[genesis.issued_cells]]
capacity = 500_00000000
lock.code_hash = "0x9bd7e06f3ecf4be0f2fcd2188b23f1b9fcc88e5d4b65a8637b17723bbda3cce8"
lock.args = "%s"
lock.hash_type = "type"
type.code_hash = "%s"
type.args = "%s"
type.hash_type = "data2"
data = "%s"

[params]
initial_primary_epoch_reward = 1_917_808_21917808
secondary_epoch_reward = 613_698_63013698
max_block_cycles = 10_000_000_000
cellbase_maturity = 0
primary_epoch_reward_halving_interval = 8760
epoch_duration_target = 80
genesis_epoch_length = 10
permanent_difficulty_in_dummy = true
starting_block_limiting_dao_withdrawing_lock = 0

[params.hardfork]
ckb2023 = 0

[pow]
func = "Dummy"
`,
		contractsMount, contractsMount, contractsMount, contractsMount, contractsMount,
		DevNodeALockArgs, DevNodeALockArgs,
		DevNodeALockArgs, XUDTDataHash, DevNodeALockHash, DevUDTGenesisData,
		DevNodeALockArgs, XUDTDataHash, DevNodeALockHash, DevUDTGenesisData,
		DevNodeBLockArgs, DevNodeBLockArgs,
		DevNodeBLockArgs, XUDTDataHash, DevNodeALockHash, DevUDTGenesisData,
	)
}

// RenderFNNConfig generates a Fiber Node config.yml targeting our devnet.
func RenderFNNConfig(nodeName, nodeIP string, p2pPort int, rpcListenAddr, ckbRPCURL, genesisTxHash string) string {
	return fmt.Sprintf(`fiber:
  listening_addr: "/ip4/0.0.0.0/tcp/%d"
  announced_node_name: "%s"
  announce_listening_addr: true
  announced_addrs:
    - "/ip4/%s/tcp/%d"
  auto_announce_node: true
  announce_private_addr: true
  chain: /fiber/dev.toml
  watchtower_check_interval_seconds: 1

rpc:
  listening_addr: "%s"
  enabled_modules:
    - channel
    - payment
    - graph
    - info
    - invoice
    - peer
    - watchtower

ckb:
  rpc_url: "%s"
  tx_tracing_polling_interval_ms: 300
  udt_whitelist:
    - name: TEST_XUDT
      script:
        code_hash: %s
        hash_type: data2
        args: %s
      cell_deps:
        - cell_dep:
            out_point:
              tx_hash: %s
              index: 0x9
            dep_type: code
      auto_accept_amount: 0

services:
  - fiber
  - rpc
  - ckb
`, p2pPort, nodeName, nodeIP, p2pPort, rpcListenAddr, ckbRPCURL, XUDTDataHash, DevNodeALockHash, genesisTxHash)
}

var ckbModulesLineRe = regexp.MustCompile(`(?m)^modules\s*=\s*\[([^\]]*)\]`)

func patchCKBToml(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := string(b)
	s = strings.ReplaceAll(s, `listen_address = "127.0.0.1:8114"`, `listen_address = "0.0.0.0:8114"`)
	s = ckbModulesLineRe.ReplaceAllStringFunc(s, func(line string) string {
		for _, mod := range []string{`"Indexer"`, `"IntegrationTest"`} {
			if !strings.Contains(line, mod) {
				line = strings.TrimSuffix(line, "]") + ", " + mod + "]"
			}
		}
		return line
	})
	return os.WriteFile(path, []byte(s), 0644)
}

func runDocker(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(buf.String()))
	}
	return nil
}
