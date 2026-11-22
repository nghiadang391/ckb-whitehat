// tui.go is the pure-logic layer of the live panel shown by `ckb-whitehat
// run` when stdout is a TTY. It carries no platform-specific code: everything
// here builds on every OS. The TTY detection and key-reading pieces live in
// tty_unix.go / tty_windows.go.
package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nghiadang391/ckb-whitehat/internal/gates"
)

// LivePanel is a live "what's running" panel. The orchestrator paints
// Render() to stdout on a timer and Teardown() when the panel is dismissed.
// All methods are goroutine-safe.
type LivePanel struct {
	mu sync.Mutex

	header     string
	maxRounds  int
	round      int
	stallLimit time.Duration // 0 = no stall warning ever

	history       []string
	startedAt     time.Time
	lastOutputAt  time.Time
	renderedLines int // line count of the last Render(), feeds Teardown()

	// gate view: when gateID != "" Render shows the question lower panel.
	gateID  string
	gateDet string
}

const historyLimit = 200

// NewLivePanel returns a panel for an agent run with the given header (e.g.
// "running claude against /repo"), maximum gate-checkpoint rounds, and stall
// limit. A zero stallLimit disables the stall warning. The clock starts at
// construction and round starts at 1.
func NewLivePanel(header string, maxRounds int, stallLimit time.Duration) *LivePanel {
	now := time.Now()
	return &LivePanel{
		header:       gates.SanitizeSingleLine(header),
		maxRounds:    maxRounds,
		round:        1,
		stallLimit:   stallLimit,
		startedAt:    now,
		lastOutputAt: now,
	}
}

// SetRound records the current gate-checkpoint round (1-based).
func (p *LivePanel) SetRound(r int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.round = r
}

// Restart is called when a new agent round spawns: it resets the elapsed
// clock and the last-output reference to now, keeping the line history.
func (p *LivePanel) Restart() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	p.startedAt = now
	p.lastOutputAt = now
}

// OnLine is called from the agent-observer goroutine for each new output
// line. It appends to a bounded history (last 200 lines) and refreshes the
// last-output time. Agent output is untrusted target content: it is
// sanitized on ingestion so no escape sequence can ever reach the TUI
// (audit H4.2 — a hostile repo could otherwise drive the user's terminal).
func (p *LivePanel) OnLine(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	line = gates.SanitizeSingleLine(line)
	p.history = append(p.history, line)
	if len(p.history) > historyLimit {
		p.history = p.history[len(p.history)-historyLimit:]
	}
	p.lastOutputAt = time.Now()
}

// SetGate puts the box in the gate-question view when gateID != "", or
// clears it when gateID == "". The orchestrator calls this when a gate
// checkpoint opens and closes again; mutex-guarded like the rest. Details
// is agent-supplied and is sanitized here so the question view can't
// carry terminal escapes or embedded newlines to the screen.
func (p *LivePanel) SetGate(gateID, detail string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gateID = gates.SanitizeSingleLine(gateID)
	p.gateDet = gates.SanitizeSingleLine(detail)
}

// ForceIdleForTest backdates lastOutputAt by d so tests can exercise the
// stall lower panel without waiting for real idle time. Test-only hook.
func (p *LivePanel) ForceIdleForTest(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastOutputAt = time.Now().Add(-d)
}

// formatElapsed renders a duration as plain human time: "12s", "4m05s", "4m12s".
func formatElapsed(d time.Duration) string {
	total := int(d.Seconds())
	if total < 0 {
		total = 0
	}
	m, s := total/60, total%60
	if m == 0 {
		return fmt.Sprintf("%ds", s)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}

// runeLen counts the display runes of s (one space per box row of padding).
func runeLen(s string) int { return len([]rune(s)) }

// Render returns the full bordered box to write to stdout. Layout:
//
//	┌ <header> <pad> ┐
//	│ <spinner> running · <elapsed> · idle <n>s · stage: <stage> · round <r>/<max> │
//	│ <lower panel>                                  │
//	└ <pad> ┘
//
// The lower panel is chosen by precedence: (1) the gate question when a gate
// is active, else (2) the stall warning when stallLimit > 0 and idle time has
// reached stallLimit/2, else (3) the agent tail: the last up-to-3 non-empty
// history lines. The line count is remembered so Teardown erases exactly
// what Render painted.
func (p *LivePanel) Render() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	elapsed := time.Since(p.startedAt)
	idleDur := time.Since(p.lastOutputAt)
	idle := int(idleDur.Seconds())

	status := fmt.Sprintf("running · %s · idle %ds · stage: %s · round %d/%d",
		formatElapsed(elapsed), idle, InferStage(p.history), p.round, p.maxRounds)
	spin := SpinFrame(int(elapsed.Milliseconds()) / 100)

	// Lower panel, by precedence: gate > stall > agent tail. The mode
	// decision lives in tui_state.go (ChooseLowerMode); this box only
	// renders whatever mode wins.
	var lower []string
	switch ChooseLowerMode(p.gateID != "", idleDur, p.stallLimit) {
	case modeGate:
		line := p.gateID
		if p.gateDet != "" {
			line += "  " + p.gateDet
		}
		lower = []string{"── checkpoint ─", line, "[a]pprove  [s]kip  [d]eny  [x]stop"}
	case modeStall:
		lower = append([]string{"── stalled? ─"}, strings.Split(StallWarning(idleDur), "\n")...)
	default:
		tail := make([]string, 0, 3)
		for i := len(p.history) - 1; i >= 0 && len(tail) < 3; i-- {
			if p.history[i] == "" {
				continue
			}
			tail = append(tail, p.history[i])
		}
		// tail was collected newest-first; restore chronological order.
		for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
			tail[i], tail[j] = tail[j], tail[i]
		}
		lower = []string{"── agent ─"}
		for _, l := range tail {
			lower = append(lower, "  · "+l)
		}
	}

	// All box rows share one inner width: the widest content row.
	inner := append([]string{p.header, spin + " " + status}, lower...)
	width := 0
	for _, s := range inner {
		if n := runeLen(s); n > width {
			width = n
		}
	}
	padSpace := func(s string) string { return s + strings.Repeat(" ", width-runeLen(s)) }
	padDash := func(s string) string { return s + strings.Repeat("─", width-runeLen(s)) }

	lines := make([]string, 0, len(inner)+2)
	lines = append(lines, "┌ "+padSpace(p.header)+" ┐")
	lines = append(lines, "│ "+padSpace(spin+" "+status)+" │")
	for _, s := range lower {
		if strings.HasPrefix(s, "──") {
			lines = append(lines, "│ "+padDash(s)+" │")
		} else {
			lines = append(lines, "│ "+padSpace(s)+" │")
		}
	}
	lines = append(lines, "└ "+strings.Repeat(" ", width)+" ┘")

	p.renderedLines = len(lines)
	return strings.Join(lines, "\n") + "\n"
}

// Teardown returns ANSI escape sequences that erase the currently rendered
// panel: cursor-up N followed by N per-line clears, where N is the line
// count of the last Render call. It is idempotent — after a Teardown the
// line count is reset to 0, so a second Teardown (e.g. the next round
// re-clearing an already-cleared box) is a no-op rather than a double erase.
func (p *LivePanel) Teardown() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.renderedLines
	p.renderedLines = 0
	return ClearLines(n)
}

// Question renders the lower question panel for a gate checkpoint:
//
//	── checkpoint ────────────────────────────────────
//	<gateID>  <detail (may be empty)>
//	[a]pprove  [s]kip  [d]eny  [x]top
//
// Key reading is NOT part of this panel; the caller uses ReadSingleKey.
// Render reuses the same question text in its gate lower panel; Question
// remains for callers that paint the question as a plain block.
func (p *LivePanel) Question(gateID, detail string) string {
	gateID = gates.SanitizeSingleLine(gateID)
	detail = gates.SanitizeSingleLine(detail)
	var b strings.Builder
	b.WriteString("── checkpoint " + strings.Repeat("─", 30) + "\n")
	b.WriteString(gateID)
	if detail != "" {
		b.WriteString("  " + detail)
	}
	b.WriteString("\n")
	b.WriteString("[a]pprove  [s]kip  [d]eny  [x]top\n")
	return b.String()
}

// FindingRe matches agent output announcing a hypothesis verdict:
// "FINDING <id> FIRED|UNPROVEN|STRUCTURAL".
var FindingRe = regexp.MustCompile(`FINDING\s+\S+\s+(FIRED|UNPROVEN|STRUCTURAL)`)

// InferStage scans the whole history and returns the highest-precedence
// stage marker found anywhere in it:
//
//  1. "CKB-WHITEHAT-RESULT:" in any line -> "report"
//  2. "FINDING <id> FIRED|UNPROVEN|STRUCTURAL" in any line -> "firing"
//  3. "poc/" or "writing a PoC" in any line, case-insensitive -> "poc"
//  4. "hypothes" in any line, case-insensitive -> "hypotheses"
//  5. otherwise -> "triage"
func InferStage(history []string) string {
	for _, line := range history {
		if strings.Contains(line, "CKB-WHITEHAT-RESULT:") {
			return "report"
		}
	}
	for _, line := range history {
		if FindingRe.MatchString(line) {
			return "firing"
		}
	}
	for _, line := range history {
		l := strings.ToLower(line)
		if strings.Contains(l, "poc/") || strings.Contains(l, "writing a poc") {
			return "poc"
		}
	}
	for _, line := range history {
		if strings.Contains(strings.ToLower(line), "hypothes") {
			return "hypotheses"
		}
	}
	return "triage"
}

var spinFrames = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"

// SpinFrame returns the i-th braille spinner frame. i may be negative: it is
// wrapped with modulo semantics, so SpinFrame(-1) deterministically returns
// the last frame ("⠏"), SpinFrame(10) == SpinFrame(0), etc.
func SpinFrame(i int) string {
	runes := []rune(spinFrames)
	j := i % len(runes)
	if j < 0 {
		j += len(runes)
	}
	return string(runes[j])
}

// ClearLines returns ANSI sequences that erase the last n lines: cursor-up n,
// then clear-to-end-of-screen (J), which covers the whole block even when the
// new frame is shorter than the old one, with no ghost rows. Returns "" when
// n <= 0. The panel is always the last thing painted above the screen's end,
// so clearing to the end of the screen is safe.
func ClearLines(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%dA\x1b[J", n)
}
