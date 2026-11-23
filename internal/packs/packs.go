// Package packs embeds reusable CKB/Fiber security invariant & attack harness
// modules materialized into a workspace for `--chain` and CKB/Fiber audits.
package packs

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

//go:embed files/*.mjs
var packFS embed.FS

// Info describes one embedded CKB/Fiber invariant/attack pack.
type Info struct {
	File    string
	Purpose string
}

// Catalog lists the embedded packs materialized into <workspace>/packs/.
var Catalog = []Info{
	{File: "chain-client.mjs", Purpose: "Zero-dep CKB + 2-node FNN JSON-RPC client reading $CKB_WHITEHAT_CHAIN_CONFIG and recording poc/chain-evidence.json"},
	{File: "pack-payment-idempotency.mjs", Purpose: "Fiber concurrent double-settle & payment-replay invariant harness (RCS)"},
	{File: "pack-hold-invoice-htlc.mjs", Purpose: "Fiber hold-invoice / HTLC settlement, amount mismatch, and force-close boundary harness (RCS)"},
	{File: "pack-udt-decimals.mjs", Purpose: "UDT decimal, BigInt vs IEEE-754 Number precision, shannons/base-unit overflow fuzz corpus (RCS)"},
	{File: "pack-self-custody.mjs", Purpose: "Browser/server self-custody key storage, weak KDF/PIN, and default secret audit helpers (BSS)"},
}

// Materialize writes all embedded packs into <workspace>/packs/ and returns
// the written file paths in deterministic order.
func Materialize(ws string) ([]string, error) {
	dstDir := filepath.Join(ws, "packs")
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(packFS, "files")
	if err != nil {
		return nil, err
	}
	var written []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := packFS.ReadFile("files/" + e.Name())
		if err != nil {
			return nil, err
		}
		p := filepath.Join(dstDir, e.Name())
		if err := os.WriteFile(p, data, 0644); err != nil {
			return nil, err
		}
		written = append(written, p)
	}
	sort.Strings(written)
	return written, nil
}
