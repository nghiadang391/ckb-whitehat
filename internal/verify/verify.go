package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/nghiadang391/ckb-whitehat/internal/chain"
)

type Check struct {
	What string
	OK   bool
}

type Marker struct {
	Findings int
	Fired    int
	Report   string
}

// ChainCheckOpts configures strict post-run on-chain RPC verification.
type ChainCheckOpts struct {
	Enabled bool
	CKBURL  string
	FNNURLs []string
	RPC     *chain.RPCClient
}

var markerRE = regexp.MustCompile(`CKB-WHITEHAT-RESULT: findings=([0-9]+) fired=([0-9]+) report=(.+)`)
var verdictRE = regexp.MustCompile(`(?m)^FINDING\s+(\S+)\s+(FIRED|UNPROVEN|STRUCTURAL)\b`)
var onchainHeadingRE = regexp.MustCompile(`(?mi)^##\s+on-chain\b[^\n]*$`)
var nextH2RE = regexp.MustCompile(`(?m)^##\s+`)
var hash32RE = regexp.MustCompile(`0x[0-9a-fA-F]{64}`)

func LastMarker(stdout string) *Marker {
	matches := markerRE.FindAllStringSubmatch(stdout, -1)
	if len(matches) == 0 {
		return nil
	}
	m := matches[len(matches)-1]
	findings, _ := strconv.Atoi(m[1])
	fired, _ := strconv.Atoi(m[2])
	return &Marker{Findings: findings, Fired: fired, Report: m[3]}
}

func VerifyRun(workspace, stdout string, marker *Marker) []Check {
	return VerifyRunWithChain(context.Background(), workspace, stdout, marker, ChainCheckOpts{})
}

// VerifyRunWithChain runs standard completeness checks plus, when chainOpts.Enabled
// is true and findings fired, strict post-run on-chain RPC verification:
//  1. `## On-chain evidence` section exists in FINDINGS.md and is not "N/A".
//  2. At least one 32-byte 0x... hash is reported (in FINDINGS.md's on-chain
//     section or poc/chain-evidence.json).
//  3. Every reported hash is independently verified against the local CKB/FNN
//     JSON-RPC endpoints so hallucinated hashes cannot pass completeness.
func VerifyRunWithChain(ctx context.Context, workspace, stdout string, marker *Marker, chainOpts ChainCheckOpts) []Check {
	reportPath := workspace + string(os.PathSeparator) + "FINDINGS.md"
	pocDir := workspace + string(os.PathSeparator) + "poc"
	reportBytes, _ := os.ReadFile(reportPath)
	report := string(reportBytes)
	checks := []Check{
		{What: "report exists and is non-trivial: " + reportPath, OK: len([]rune(trimSpace(report))) > 200},
		{What: "agent printed the CKB-WHITEHAT-RESULT: marker", OK: marker != nil},
	}
	if marker != nil && (marker.Findings > 0 || marker.Fired > 0) {
		entries, _ := os.ReadDir(pocDir)
		files := 0
		for _, entry := range entries {
			if !entry.IsDir() && entry.Name() != "" && entry.Name()[0] != '.' {
				files++
			}
		}
		checks = append(checks,
			Check{What: "at least one PoC file in " + pocDir, OK: files > 0},
			Check{What: "a FINDING <id> verdict line was printed (" + strconv.Itoa(len(verdictRE.FindAllStringSubmatch(stdout, -1))) + ")", OK: verdictRE.MatchString(stdout)},
			Check{What: "the report carries FINDING verdict lines (" + strconv.Itoa(len(verdictRE.FindAllStringSubmatch(report, -1))) + ")", OK: verdictRE.MatchString(report)},
		)
	}
	if chainOpts.Enabled && marker != nil && (marker.Findings > 0 || marker.Fired > 0) {
		checks = append(checks, verifyOnChainSection(ctx, workspace, report, chainOpts)...)
	}
	return checks
}

func extractOnChainSection(report string) (string, bool) {
	loc := onchainHeadingRE.FindStringIndex(report)
	if loc == nil {
		return "", false
	}
	rest := report[loc[1]:]
	if next := nextH2RE.FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	return strings.TrimSpace(rest), true
}

func isNASection(body string) bool {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return true
	}
	upper := strings.ToUpper(trimmed)
	return strings.HasPrefix(upper, "N/A") || strings.HasPrefix(upper, "NONE")
}

func collectEvidenceHashes(workspace, section string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	for _, h := range hash32RE.FindAllString(section, -1) {
		add(h)
	}
	// Also read structured poc/chain-evidence.json if written by PoCs/packs.
	evPath := filepath.Join(workspace, "poc", "chain-evidence.json")
	if b, err := os.ReadFile(evPath); err == nil {
		var entries []struct {
			TxHash      string `json:"tx_hash"`
			PaymentHash string `json:"payment_hash"`
			ChannelID   string `json:"channel_id"`
		}
		if json.Unmarshal(b, &entries) == nil {
			for _, e := range entries {
				for _, cand := range []string{e.TxHash, e.PaymentHash, e.ChannelID} {
					if hash32RE.MatchString(cand) {
						add(cand)
					}
				}
			}
		} else {
			for _, h := range hash32RE.FindAllString(string(b), -1) {
				add(h)
			}
		}
	}
	return out
}

func verifyOnChainSection(ctx context.Context, workspace, report string, opts ChainCheckOpts) []Check {
	section, found := extractOnChainSection(report)
	if !found || isNASection(section) {
		return []Check{
			{What: "non-N/A ## On-chain evidence section in FINDINGS.md (--chain scope)", OK: false},
		}
	}
	hashes := collectEvidenceHashes(workspace, section)
	if len(hashes) == 0 {
		return []Check{
			{What: "non-N/A ## On-chain evidence section in FINDINGS.md (--chain scope)", OK: true},
			{What: "at least one 32-byte on-chain tx/payment hash reported in ## On-chain evidence or poc/chain-evidence.json", OK: false},
		}
	}

	rpc := opts.RPC
	if rpc == nil {
		rpc = chain.NewRPCClient()
	}
	var verified, unverified []string
	for _, h := range hashes {
		if kind, ok := chain.VerifyHashOnChain(ctx, rpc, h, opts.CKBURL, opts.FNNURLs); ok {
			verified = append(verified, h[:12]+"…("+kind+")")
		} else {
			unverified = append(unverified, h)
		}
	}
	checks := []Check{
		{What: "non-N/A ## On-chain evidence section in FINDINGS.md (--chain scope)", OK: true},
	}
	if len(unverified) == 0 {
		checks = append(checks, Check{
			What: fmt.Sprintf("all %d reported on-chain hash(es) verified via local CKB/FNN RPC (%s)", len(verified), strings.Join(verified, ", ")),
			OK:   true,
		})
	} else {
		checks = append(checks, Check{
			What: fmt.Sprintf("on-chain RPC verification failed for %d/%d hash(es): %s", len(unverified), len(hashes), strings.Join(unverified, ", ")),
			OK:   false,
		})
	}
	return checks
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\r' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
