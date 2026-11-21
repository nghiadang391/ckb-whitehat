// tui_state.go holds the pure lower-panel decision logic for the live box:
// which of the three lower modes is active, and the stall warning text.
// No rendering, no platform code — the box frame itself is tui.go's job.
package main

import (
	"fmt"
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
func StallWarning(idle time.Duration) string {
	return strings.Join([]string{
		fmt.Sprintf("⚠ no output for %s — agent likely blocked on a", formatElapsed(idle)),
		"  permission prompt (allowlist). Widen it, or press x to stop.",
	}, "\n")
}
