// tui_state.go holds the pure lower-panel decision logic for the live box:
// which of the three lower modes is active, and the stall warning text.
// No rendering, no platform code — the box frame itself is tui.go's job.
package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// lowerMode is which content the box's lower panel shows.
type lowerMode int

const (
	// modeTail: the agent's last 3 output lines (normal running view).
	modeTail lowerMode = iota
	// modeStall: the "agent looks blocked" warning.
	modeStall
	// modeGate: the gate question (approve/skip/deny/stop).
	modeGate
)

// ChooseLowerMode picks the active lower-panel mode.
// Precedence: gate > stall > tail.
//   - gateActive true            -> modeGate
//   - else stallLimit>0 && idle >= stallLimit/2 -> modeStall
//   - else                         -> modeTail
//
// (stallLimit of 0 means the stall mode is never chosen.)
func ChooseLowerMode(gateActive bool, idle, stallLimit time.Duration) lowerMode {
	if gateActive {
		return modeGate
	}
	if stallLimit > 0 && idle >= stallLimit/2 {
		return modeStall
	}
	return modeTail
}

// StallWarning returns the two-line warning text for the lower panel, given
// how long the agent has been quiet. formatElapsed is the same mmss helper
// the box renderer uses, so the number matches the elapsed column exactly.
// The stop hint is Ctrl-C (not a keypress): while the agent is running,
// nothing is reading the keyboard, so the only mid-run stop is the shell
// signal. A keypress answer ("x") only works once the run pauses at a
// checkpoint gate (audit M3.3).
func StallWarning(idle time.Duration) string {
	return strings.Join([]string{
		fmt.Sprintf("⚠ no output for %s — agent likely blocked on a", formatElapsed(idle)),
		"  permission prompt (allowlist). Widen it, or Ctrl-C to stop.",
	}, "\n")
}

// pipelineStage describes one ordered stage in the Pipeline box.
type pipelineStage struct {
	id    string
	label string
}

// pipelineStages is the fixed 5-stage audit progression rendered in the
// Pipeline box, matching InferStage's return values in order.
var pipelineStages = []pipelineStage{
	{id: "triage", label: "Triage"},
	{id: "hypotheses", label: "Hypotheses"},
	{id: "poc", label: "PoCs"},
	{id: "firing", label: "Firing"},
	{id: "report", label: "Report"},
}

// stageIndex returns the 0-based index of stage in pipelineStages, defaulting
// to 0 ("triage") for unrecognized stage names.
func stageIndex(stage string) int {
	for i, s := range pipelineStages {
		if s.id == stage {
			return i
		}
	}
	return 0
}

// firedFindingRe matches agent lines reporting a confirmed FIRED finding.
var firedFindingRe = regexp.MustCompile(`FINDING\s+\S+\s+FIRED\b`)

// countFired returns the number of FIRED finding lines in history.
func countFired(history []string) int {
	n := 0
	for _, line := range history {
		if firedFindingRe.MatchString(line) {
			n++
		}
	}
	return n
}

// lastPermissionDenied returns the most recent history line containing a
// permission-denied marker, or "" if none has been observed.
func lastPermissionDenied(history []string) string {
	for i := len(history) - 1; i >= 0; i-- {
		if strings.Contains(history[i], "⚠ permission denied:") {
			return history[i]
		}
	}
	return ""
}

// statusBadgeText returns the plain status badge text for the Pipeline header.
func statusBadgeText(mode lowerMode) string {
	switch mode {
	case modeGate:
		return "awaiting"
	case modeStall:
		return "stalled?"
	default:
		return "running"
	}
}
