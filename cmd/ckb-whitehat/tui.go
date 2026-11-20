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
)

// LivePanel is a live "what's running" panel. The orchestrator paints
// Render() to stdout on a timer and Teardown() when the panel is dismissed.
// All methods are goroutine-safe.
type LivePanel struct {
	mu sync.Mutex

	header    string
	maxRounds int
	round     int

	history       []string
	startedAt     time.Time
	lastOutputAt  time.Time
	renderedLines int // line count of the last Render(), feeds Teardown()
}

const historyLimit = 200

// NewLivePanel returns a panel for an agent run with the given header (e.g.
// "running claude against /repo") and maximum gate-checkpoint rounds. The
// clock starts at construction and round starts at 1.
func NewLivePanel(header string, maxRounds int) *LivePanel {
	now := time.Now()
	return &LivePanel{
		header:       header,
		maxRounds:    maxRounds,
		round:        1,
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
// last-output time.
func (p *LivePanel) OnLine(line string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.history = append(p.history, line)
	if len(p.history) > historyLimit {
		p.history = p.history[len(p.history)-historyLimit:]
	}
	p.lastOutputAt = time.Now()
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

// Render returns the full panel block to write to stdout: one status line
// followed by up to the last 3 non-empty history lines. With empty history
// the panel is exactly one line. The line count is remembered so Teardown
// can erase exactly what Render painted.
func (p *LivePanel) Render() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	elapsed := time.Since(p.startedAt)
	idle := int(time.Since(p.lastOutputAt).Seconds())

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

	lines := []string{fmt.Sprintf("%s %s · %s · idle %ds · stage: %s [round %d/%d]",
		SpinFrame(int(elapsed.Milliseconds())/100), p.header,
		formatElapsed(elapsed), idle, InferStage(p.history), p.round, p.maxRounds)}
	for _, l := range tail {
		lines = append(lines, "  · "+l)
	}
	p.renderedLines = len(lines)
	return strings.Join(lines, "\n") + "\n"
}

// Teardown returns ANSI escape sequences that erase the currently rendered
// panel: cursor-up N followed by N per-line clears, where N is the line
// count of the last Render call.
func (p *LivePanel) Teardown() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ClearLines(p.renderedLines)
}

// Question renders the lower question panel for a gate checkpoint:
//
//	── checkpoint ────────────────────────────────────
//	<gateID>  <detail (may be empty)>
//	[a]pprove  [s]kip  [d]eny  [x]top
//
// Key reading is NOT part of this panel; the caller uses ReadSingleKey.
func (p *LivePanel) Question(gateID, detail string) string {
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

// ClearLines returns ANSI sequences that erase the last n lines: cursor-up
// n followed by n "erase whole line" sequences. It returns "" when n <= 0.
func ClearLines(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%dA", n) + strings.Repeat("\x1b[2K", n)
}
