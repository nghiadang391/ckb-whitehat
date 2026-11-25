// tui.go is the pure-logic layer of the live panel shown by `ckb-whitehat
// run` when stdout is a TTY. It carries no platform-specific code: everything
// here builds on every OS. The TTY detection, terminal sizing, resize signal,
// and key-reading pieces live in tty_unix.go / tty_windows.go.
package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nghiadang391/ckb-whitehat/internal/gates"
)

// Pure ANSI 1-8 SGR palette matching no-mistakes/internal/tui/DESIGN.md so
// colors inherit the user's terminal theme with zero external dependencies.
const (
	ansiReset      = "\x1b[0m"
	ansiBold       = "\x1b[1m"
	ansiRed        = "\x1b[31m"
	ansiGreen      = "\x1b[32m"
	ansiYellow     = "\x1b[33m"
	ansiBoldYellow = "\x1b[1;33m"
	ansiBlue       = "\x1b[34m"
	ansiBoldBlue   = "\x1b[1;34m"
	ansiCyan       = "\x1b[36m"
	ansiBoldCyan   = "\x1b[1;36m"
	ansiDim        = "\x1b[90m"
)

// Responsive layout constants matching no-mistakes/internal/tui/layout.go.
const (
	responsiveMinWidth = 100
	responsiveGap      = 2
	responsiveLeftMin  = 38
	responsiveLeftMax  = 48
	responsiveRightMin = 48

	minRenderCols     = 40
	defaultRenderCols = 80
	defaultRenderRows = 24
)

func styleDim(s string) string        { return ansiDim + s + ansiReset }
func styleBold(s string) string       { return ansiBold + s + ansiReset }
func styleBoldCyan(s string) string   { return ansiBoldCyan + s + ansiReset }
func styleBlue(s string) string       { return ansiBlue + s + ansiReset }
func styleBoldBlue(s string) string   { return ansiBoldBlue + s + ansiReset }
func styleGreen(s string) string      { return ansiGreen + s + ansiReset }
func styleYellow(s string) string     { return ansiYellow + s + ansiReset }
func styleBoldYellow(s string) string { return ansiBoldYellow + s + ansiReset }
func styleRed(s string) string        { return ansiRed + s + ansiReset }
func styleCyan(s string) string       { return ansiCyan + s + ansiReset }

// promptLine is one styled line inside a custom confirmation box (e.g. init's
// per-command approval prompt). Text is sanitized on ingestion so untrusted
// agent strings can never emit terminal escapes.
type promptLine struct {
	text string
	tone string // "", "bold", "cyan", "yellow", "red", "dim"
}

// LivePanel is a live "what's running" panel. The orchestrator paints
// Render() to stdout on a timer and Teardown() when the panel is dismissed.
// All methods are goroutine-safe.
type LivePanel struct {
	mu sync.Mutex

	header     string
	maxRounds  int
	round      int
	stallLimit time.Duration // 0 = no stall warning ever

	stages        []pipelineStage // nil -> default audit pipelineStages
	stageOverride string          // non-empty -> explicit active stage ID

	cols int
	rows int

	history       []string
	startedAt     time.Time
	lastOutputAt  time.Time
	renderedLines int // line count of the last Render(), feeds Teardown()

	// gate view: when gateID != "" Render shows the checkpoint/confirmation panel.
	gateID            string
	gateDet           string
	gateTitle         string
	actionBarOverride string
	gateLines         []promptLine
}

const historyLimit = 200

// NewLivePanel returns a panel for an agent run with the given header (e.g.
// "running claude against /repo"), maximum gate-checkpoint rounds, and stall
// limit. A zero stallLimit disables the stall warning. The clock starts at
// construction, round starts at 1, and viewport defaults to 80x24 until
// SetSize is called.
func NewLivePanel(header string, maxRounds int, stallLimit time.Duration) *LivePanel {
	now := time.Now()
	return &LivePanel{
		header:       gates.SanitizeSingleLine(header),
		maxRounds:    maxRounds,
		round:        1,
		stallLimit:   stallLimit,
		cols:         defaultRenderCols,
		rows:         defaultRenderRows,
		startedAt:    now,
		lastOutputAt: now,
	}
}

// SetSize updates the viewport dimensions used by Render. Non-positive values
// are ignored so transient/invalid ioctl readings cannot collapse the layout.
func (p *LivePanel) SetSize(cols, rows int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cols > 0 {
		p.cols = cols
	}
	if rows > 0 {
		p.rows = rows
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

// SetStages overrides the ordered stages shown in the Pipeline box (used by
// `init` for its Target -> Inspect -> Setup -> Ready flow).
func (p *LivePanel) SetStages(stages []pipelineStage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stages = append([]pipelineStage(nil), stages...)
}

// SetStage explicitly pins the active stage ID (pass "" to resume automatic
// InferStage detection from agent history).
func (p *LivePanel) SetStage(stageID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stageOverride = stageID
}

// SetGate puts the panel in the gate-question view when gateID != "", or
// clears it when gateID == "". The orchestrator calls this when a gate
// checkpoint opens and closes again; mutex-guarded like the rest. Details
// is agent-supplied and is sanitized here so the question view can't
// carry terminal escapes or embedded newlines to the screen.
func (p *LivePanel) SetGate(gateID, detail string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gateID = gates.SanitizeSingleLine(gateID)
	p.gateDet = gates.SanitizeSingleLine(detail)
	p.gateTitle = ""
	p.actionBarOverride = ""
	p.gateLines = nil
}

// SetCommandPrompt puts the panel into a custom multi-line confirmation box
// (used by `init` for per-command approval). Pass title == "" to clear.
// Every line is sanitized via SanitizeSingleLine so untrusted agent text
// cannot inject terminal escapes.
func (p *LivePanel) SetCommandPrompt(title string, lines []promptLine, actionBar string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if title == "" {
		p.gateID = ""
		p.gateDet = ""
		p.gateTitle = ""
		p.actionBarOverride = ""
		p.gateLines = nil
		return
	}
	clean := make([]promptLine, 0, len(lines))
	for _, l := range lines {
		clean = append(clean, promptLine{
			text: gates.SanitizeSingleLine(l.text),
			tone: l.tone,
		})
	}
	p.gateID = gates.SanitizeSingleLine(title)
	p.gateDet = ""
	p.gateTitle = gates.SanitizeSingleLine(title)
	p.actionBarOverride = actionBar
	p.gateLines = clean
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

// ansiSGRRe matches ANSI SGR styling sequences emitted by this TUI.
var ansiSGRRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes ANSI SGR escape sequences from s.
func stripANSI(s string) string {
	return ansiSGRRe.ReplaceAllString(s, "")
}

// isWideRune reports whether r should be treated as occupying 2 terminal cells.
// Conservatively covers CJK/fullwidth, supplementary planes (emoji/CJK Ext B+),
// and ambiguous warning/emoji symbols such as ⚠ (U+26A0) so rendered rows never
// wrap on any terminal emulator.
func isWideRune(r rune) bool {
	if r == 0x26A0 { // ⚠ warning sign (2 cols on many terminals)
		return true
	}
	if r >= 0x10000 { // supplementary planes (emoji, pictographs, CJK Ext B+)
		return true
	}
	return (r >= 0x1100 && r <= 0x115F) ||
		r == 0x2329 || r == 0x232A ||
		(r >= 0x231A && r <= 0x231B) ||
		(r >= 0x23E9 && r <= 0x23F3) ||
		(r >= 0x2E80 && r <= 0xA4CF && r != 0x303F) ||
		(r >= 0xAC00 && r <= 0xD7A3) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0xFE10 && r <= 0xFE19) ||
		(r >= 0xFE30 && r <= 0xFE6F) ||
		(r >= 0xFF00 && r <= 0xFF60) ||
		(r >= 0xFFE0 && r <= 0xFFE6)
}

// runeWidth returns the conservative terminal cell width of r (0, 1, or 2).
func runeWidth(r rune) int {
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	if isWideRune(r) {
		return 2
	}
	return 1
}

// displayWidth returns the visible terminal column count of s, ignoring ANSI
// SGR escape sequences.
func displayWidth(s string) int {
	w := 0
	for _, r := range stripANSI(s) {
		w += runeWidth(r)
	}
	return w
}

// truncateDisplay truncates s so that displayWidth(out) <= maxCols, appending
// "…" (1 col) when truncated. Returns "" when maxCols <= 0. ANSI SGR escape
// sequences are preserved without counting toward width; if a styled string is
// truncated, an ANSI reset is appended so color never bleeds past the cut.
func truncateDisplay(s string, maxCols int) string {
	if maxCols <= 0 {
		return ""
	}
	if displayWidth(s) <= maxCols {
		return s
	}
	if maxCols == 1 {
		return "…"
	}
	limit := maxCols - 1
	var b strings.Builder
	w := 0
	hadANSI := false
	for i := 0; i < len(s); {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && ((s[j] >= '0' && s[j] <= '9') || s[j] == ';') {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				b.WriteString(s[i : j+1])
				hadANSI = true
				i = j + 1
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		rw := runeWidth(r)
		if w+rw > limit {
			break
		}
		b.WriteRune(r)
		w += rw
		i += size
	}
	b.WriteRune('…')
	if hadANSI {
		b.WriteString(ansiReset)
	}
	return b.String()
}

// padDisplay pads a possibly ANSI-styled string on the right with spaces to
// exact targetCols visible columns. If s exceeds targetCols, it is truncated
// first so the output width is strictly == targetCols.
func padDisplay(s string, targetCols int) string {
	if targetCols <= 0 {
		return ""
	}
	w := displayWidth(s)
	if w > targetCols {
		s = truncateDisplay(s, targetCols)
		w = displayWidth(s)
	}
	if w < targetCols {
		return s + strings.Repeat(" ", targetCols-w)
	}
	return s
}

// wrapDisplay wraps plain text s into at most maxLines lines, each having
// displayWidth <= maxCols. If the text exceeds maxLines, the final line is
// truncated with "…".
func wrapDisplay(s string, maxCols, maxLines int) []string {
	if maxCols <= 0 || maxLines <= 0 {
		return nil
	}
	if displayWidth(s) <= maxCols {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := ""
	for idx, word := range words {
		if cur == "" {
			if displayWidth(word) <= maxCols {
				cur = word
			} else if len(lines) == maxLines-1 {
				lines = append(lines, truncateDisplay(word, maxCols))
				return lines
			} else {
				cur = truncateDisplay(word, maxCols)
			}
			continue
		}
		candidate := cur + " " + word
		if displayWidth(candidate) <= maxCols {
			cur = candidate
			continue
		}
		if len(lines) == maxLines-1 {
			rest := cur + " " + strings.Join(words[idx:], " ")
			lines = append(lines, truncateDisplay(rest, maxCols))
			return lines
		}
		lines = append(lines, cur)
		if displayWidth(word) <= maxCols {
			cur = word
		} else {
			cur = truncateDisplay(word, maxCols)
		}
	}
	if cur != "" {
		lines = append(lines, truncateDisplay(cur, maxCols))
	}
	return lines
}

// renderRoundedBox renders body inside a rounded-border box with a bold cyan
// title embedded in the top border, per no-mistakes/internal/tui/DESIGN.md.
// Every single output line has exact displayWidth == width.
func renderRoundedBox(title, body string, width int) string {
	if width < 6 {
		width = 6
	}

	// Top border: ╭─ <title> ────╮  (3 + titleW + 1 + fillW + 1 == width).
	maxTitleW := width - 6
	if maxTitleW < 0 {
		maxTitleW = 0
	}
	titleText := truncateDisplay(title, maxTitleW)
	titleW := displayWidth(titleText)
	fillW := width - 5 - titleW
	if fillW < 1 {
		fillW = 1
	}
	topBorder := styleDim("╭─ ") + styleBoldCyan(titleText) + " " + styleDim(strings.Repeat("─", fillW)+"╮")

	// Content lines: │ <padded to width-4> │.
	contentWidth := width - 4
	contentLines := strings.Split(body, "\n")
	if len(contentLines) > 0 && contentLines[len(contentLines)-1] == "" {
		contentLines = contentLines[:len(contentLines)-1]
	}
	var lines []string
	for _, cl := range contentLines {
		lines = append(lines, styleDim("│")+" "+padDisplay(cl, contentWidth)+" "+styleDim("│"))
	}

	bottomBorder := styleDim("╰" + strings.Repeat("─", width-2) + "╯")
	if len(lines) == 0 {
		return topBorder + "\n" + bottomBorder
	}
	return topBorder + "\n" + strings.Join(lines, "\n") + "\n" + bottomBorder
}

// responsiveColumnWidths computes left and right column widths for wide
// viewports (cols >= responsiveMinWidth), matching no-mistakes layout rules.
func responsiveColumnWidths(cols int) (leftWidth, rightWidth int) {
	leftWidth = cols / 3
	if leftWidth < responsiveLeftMin {
		leftWidth = responsiveLeftMin
	}
	if leftWidth > responsiveLeftMax {
		leftWidth = responsiveLeftMax
	}
	rightWidth = cols - leftWidth - responsiveGap
	if rightWidth < responsiveRightMin {
		rightWidth = responsiveRightMin
		leftWidth = cols - rightWidth - responsiveGap
	}
	return leftWidth, rightWidth
}

// joinColumns joins two multi-line blocks side by side with exact display-width
// padding so every joined row has exact width leftWidth + gap + rightWidth.
func joinColumns(left, right string, leftWidth, rightWidth, gap int) string {
	if right == "" {
		return left
	}
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	maxLines := len(leftLines)
	if len(rightLines) > maxLines {
		maxLines = len(rightLines)
	}
	gapStr := strings.Repeat(" ", gap)
	out := make([]string, maxLines)
	for i := 0; i < maxLines; i++ {
		l := ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		r := ""
		if i < len(rightLines) {
			r = rightLines[i]
		}
		out[i] = padDisplay(l, leftWidth) + gapStr + padDisplay(r, rightWidth)
	}
	return strings.Join(out, "\n")
}

// styleStatusBadge styles the right-aligned Pipeline header status badge.
func styleStatusBadge(mode lowerMode) string {
	text := statusBadgeText(mode)
	switch mode {
	case modeGate, modeStall:
		return styleBoldYellow(text)
	default:
		return styleBoldBlue(text)
	}
}

// renderPipelineBox builds the rounded Pipeline box showing header + badge,
// the ordered stages with optional vertical connectors, and the bottom
// elapsed/idle/round metadata row.
func renderPipelineBox(header string, mode lowerMode, stages []pipelineStage, stage string, spinIdx int, firedCount int, elapsed, idleDur time.Duration, round, maxRounds, boxWidth, rows int) string {
	if len(stages) == 0 {
		stages = pipelineStages
	}
	contentWidth := boxWidth - 4
	if contentWidth < 1 {
		contentWidth = 1
	}

	var b strings.Builder

	// Row 1: truncated target/header (dim) on the left, bold status badge on the right.
	badge := styleStatusBadge(mode)
	badgeW := displayWidth(badge)
	headerMax := contentWidth - badgeW - 1
	if headerMax < 1 {
		headerMax = 1
	}
	headerText := truncateDisplay(header, headerMax)
	spacing := contentWidth - displayWidth(headerText) - badgeW
	if spacing < 1 {
		spacing = 1
	}
	b.WriteString(styleDim(headerText) + strings.Repeat(" ", spacing) + badge + "\n")

	// Blank separator line omitted on compact terminals (< 20 rows).
	if rows >= 20 {
		b.WriteString("\n")
	}

	// Ordered stages with vertical dim connectors when rows >= 26.
	activeIdx := 0
	for i, s := range stages {
		if s.id == stage {
			activeIdx = i
			break
		}
	}
	showConnectors := rows >= 26
	for i, st := range stages {
		var icon string
		switch {
		case i < activeIdx:
			icon = styleGreen("✓")
		case i == activeIdx:
			if mode == modeGate {
				icon = styleYellow("⏸")
			} else {
				icon = styleBlue(SpinFrame(spinIdx))
			}
		default:
			icon = styleDim("○")
		}

		line := icon + " " + st.label
		if st.id == "firing" && firedCount > 0 {
			right := styleDim(fmt.Sprintf("%d fired", firedCount))
			gap := contentWidth - displayWidth(line) - displayWidth(right)
			if gap >= 1 {
				line += strings.Repeat(" ", gap) + right
			}
		}
		b.WriteString(line + "\n")

		if showConnectors && i < len(stages)-1 {
			b.WriteString(styleDim("│") + "\n")
		}
	}

	// Extra visual breathing room before metadata when connectors are visible.
	if showConnectors {
		b.WriteString("\n")
	}

	idle := int(idleDur.Seconds())
	if idle < 0 {
		idle = 0
	}
	var meta string
	if maxRounds > 0 {
		meta = fmt.Sprintf("elapsed %s · idle %ds · round %d/%d",
			formatElapsed(elapsed), idle, round, maxRounds)
	} else {
		meta = fmt.Sprintf("elapsed %s · idle %ds",
			formatElapsed(elapsed), idle)
	}
	b.WriteString(styleDim(truncateDisplay(meta, contentWidth)))

	return renderRoundedBox("Pipeline", b.String(), boxWidth)
}

// styleLogLine truncates and styles a single sanitized log line according to
// its semantic prefix/content.
func styleLogLine(line string, contentWidth int) string {
	t := truncateDisplay(line, contentWidth)
	switch {
	case strings.HasPrefix(line, "✗"):
		return styleRed(t)
	case strings.HasPrefix(line, "⚠"):
		return styleYellow(t)
	case firedFindingRe.MatchString(line):
		return styleGreen(t)
	case strings.HasPrefix(line, "→ "):
		return styleCyan(t)
	default:
		return styleDim(t)
	}
}

// renderLogBox builds the rounded Log box showing up to maxLines recent
// non-empty history lines in chronological order.
func renderLogBox(history []string, boxWidth, maxLines int) string {
	contentWidth := boxWidth - 4
	if contentWidth < 1 {
		contentWidth = 1
	}
	if maxLines < 1 {
		maxLines = 1
	}

	tail := make([]string, 0, maxLines)
	for i := len(history) - 1; i >= 0 && len(tail) < maxLines; i-- {
		if history[i] == "" {
			continue
		}
		tail = append(tail, history[i])
	}
	// Restore chronological order.
	for i, j := 0, len(tail)-1; i < j; i, j = i+1, j-1 {
		tail[i], tail[j] = tail[j], tail[i]
	}

	var lines []string
	if len(tail) == 0 {
		lines = []string{styleDim(truncateDisplay("waiting for agent activity…", contentWidth))}
	} else {
		lines = make([]string, 0, len(tail))
		for _, l := range tail {
			lines = append(lines, styleLogLine(l, contentWidth))
		}
	}
	return renderRoundedBox("Log", strings.Join(lines, "\n"), boxWidth)
}

// renderWarningBox builds the rounded Warning box for stalled runs, wrapping
// StallWarning(idleDur) to fit and appending the most recent permission-denied
// line when present.
func renderWarningBox(idleDur time.Duration, denied string, boxWidth, maxBodyLines int) string {
	contentWidth := boxWidth - 4
	if contentWidth < 1 {
		contentWidth = 1
	}
	if maxBodyLines < 1 {
		maxBodyLines = 1
	}

	reserveDenied := denied != "" && maxBodyLines >= 2
	warnMax := maxBodyLines
	if reserveDenied {
		warnMax = maxBodyLines - 1
	}

	rawLines := strings.Split(StallWarning(idleDur), "\n")
	fitsAsIs := len(rawLines) <= warnMax
	if fitsAsIs {
		for _, rl := range rawLines {
			if displayWidth(rl) > contentWidth {
				fitsAsIs = false
				break
			}
		}
	}

	var plainWarn []string
	if fitsAsIs {
		plainWarn = rawLines
	} else {
		joined := strings.TrimSpace(rawLines[0])
		for _, rl := range rawLines[1:] {
			joined += " " + strings.TrimSpace(rl)
		}
		plainWarn = wrapDisplay(joined, contentWidth, warnMax)
	}

	styled := make([]string, 0, len(plainWarn)+1)
	for _, wl := range plainWarn {
		styled = append(styled, styleYellow(truncateDisplay(wl, contentWidth)))
	}
	if reserveDenied && len(styled) < maxBodyLines {
		styled = append(styled, styleYellow(truncateDisplay(denied, contentWidth)))
	}
	return renderRoundedBox("Warning", strings.Join(styled, "\n"), boxWidth)
}

// renderActionBar renders the no-mistakes-style horizontal checkpoint action
// bar with bold keys and 2-space separation, truncated strictly to width.
func renderActionBar(width int) string {
	bar := " " + styleBold("a") + " approve  " +
		styleBold("s") + " skip  " +
		styleBold("d") + " deny  " +
		styleBold("x") + " stop"
	return truncateDisplay(bar, width)
}

func renderSetupActionBar(width int) string {
	bar := " " + styleBold("a") + " approve  " +
		styleBold("d") + " deny  " +
		styleBold("s") + " skip all"
	return truncateDisplay(bar, width)
}

func stylePromptLine(l promptLine, contentWidth int) []string {
	maxLines := 2
	if l.tone == "cyan" || l.tone == "yellow" {
		// Allow setup commands and risk warnings more room before clipping.
		maxLines = 6
	}
	wrapped := wrapDisplay(l.text, contentWidth, maxLines)
	out := make([]string, 0, len(wrapped))
	for _, w := range wrapped {
		t := truncateDisplay(w, contentWidth)
		switch l.tone {
		case "bold":
			out = append(out, styleBold(t))
		case "cyan":
			out = append(out, styleBoldCyan(t))
		case "yellow":
			out = append(out, styleBoldYellow(t))
		case "red":
			out = append(out, styleRed(t))
		case "dim":
			out = append(out, styleDim(t))
		default:
			out = append(out, t)
		}
	}
	return out
}

// renderGateSection renders the checkpoint Action Bar directly above the
// rounded Checkpoint (or custom confirmation) box containing wrapped/truncated
// details.
func renderGateSection(gateID, gateDet, gateTitle, actionBarOverride string, gateLines []promptLine, boxWidth, maxBoxBodyLines int) string {
	contentWidth := boxWidth - 4
	if contentWidth < 1 {
		contentWidth = 1
	}
	if maxBoxBodyLines < 1 {
		maxBoxBodyLines = 1
	}

	title := "Checkpoint"
	if gateTitle != "" {
		title = gateTitle
	}

	var bodyLines []string
	if len(gateLines) > 0 {
		for _, pl := range gateLines {
			for _, styled := range stylePromptLine(pl, contentWidth) {
				if len(bodyLines) >= maxBoxBodyLines {
					break
				}
				bodyLines = append(bodyLines, styled)
			}
			if len(bodyLines) >= maxBoxBodyLines {
				break
			}
		}
	} else if maxBoxBodyLines == 1 {
		combined := gateID
		if gateDet != "" {
			combined += "  " + gateDet
		}
		bodyLines = []string{styleBoldYellow(truncateDisplay(combined, contentWidth))}
	} else {
		bodyLines = append(bodyLines, styleBoldYellow(truncateDisplay(gateID, contentWidth)))
		if gateDet != "" {
			wrapped := wrapDisplay(gateDet, contentWidth, maxBoxBodyLines-1)
			bodyLines = append(bodyLines, wrapped...)
		}
	}

	bar := renderActionBar(boxWidth)
	if actionBarOverride != "" {
		bar = truncateDisplay(actionBarOverride, boxWidth)
	}
	box := renderRoundedBox(title, strings.Join(bodyLines, "\n"), boxWidth)
	return bar + "\n" + box
}

// renderFooter renders the minimal dim footer outside all boxes, strictly
// truncated to cols. Action keys for checkpoints live in renderActionBar
// directly above the Checkpoint box (per no-mistakes DESIGN.md), so the footer
// stays a single minimal stop hint without duplicating them.
func renderFooter(_ lowerMode, cols int) string {
	footer := "  " + styleBold("Ctrl-C") + " " + styleDim("stop")
	return truncateDisplay(footer, cols)
}

// Render returns the full ANSI-styled live panel to write to stdout.
//
// Layout adapts responsively to the viewport set via SetSize (default 80x24):
//   - Wide (cols >= 100): 2-column layout with Pipeline on the left and
//     Log / Warning / Checkpoint on the right.
//   - Narrow (cols < 100): stacked layout with Pipeline above Log / Warning /
//     Checkpoint, followed by a minimal dim footer outside the boxes.
//
// Every line is strictly clamped to displayWidth(line) <= cols, and total
// rendered lines fit within rows-1 so terminals never wrap or accumulate ghost
// lines. The exact newline count is stored in renderedLines so Teardown erases
// precisely what Render painted.
func (p *LivePanel) Render() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	cols := p.cols
	if cols <= 0 {
		cols = defaultRenderCols
	}
	if cols < minRenderCols {
		cols = minRenderCols
	}
	rows := p.rows
	if rows <= 0 {
		rows = defaultRenderRows
	}

	elapsed := time.Since(p.startedAt)
	idleDur := time.Since(p.lastOutputAt)
	mode := ChooseLowerMode(p.gateID != "", idleDur, p.stallLimit)
	stage := p.stageOverride
	if stage == "" {
		stage = InferStage(p.history)
	}
	spinIdx := int(elapsed.Milliseconds()) / 100
	fired := countFired(p.history)
	denied := lastPermissionDenied(p.history)
	footer := renderFooter(mode, cols)

	maxTotalLines := rows - 1
	if maxTotalLines < 1 {
		maxTotalLines = 1
	}

	var out string
	if cols >= responsiveMinWidth {
		leftW, rightW := responsiveColumnWidths(cols)
		left := renderPipelineBox(p.header, mode, p.stages, stage, spinIdx, fired, elapsed, idleDur, p.round, p.maxRounds, leftW, rows)
		leftH := strings.Count(left, "\n") + 1
		maxRightH := maxTotalLines - 1 // leave 1 row for footer
		if maxRightH < 3 {
			maxRightH = 3
		}

		var right string
		switch mode {
		case modeGate:
			maxBody := maxRightH - 3 // 1 action bar + 2 box borders
			if maxBody < 1 {
				maxBody = 1
			}
			right = renderGateSection(p.gateID, p.gateDet, p.gateTitle, p.actionBarOverride, p.gateLines, rightW, maxBody)
		case modeStall:
			maxBody := maxRightH - 2
			if maxBody < 1 {
				maxBody = 1
			}
			right = renderWarningBox(idleDur, denied, rightW, maxBody)
		default:
			// Expand Log box line count to match Pipeline box height, bounded
			// by rows-4 total box height (rows-6 body lines), minimum 3 lines.
			logLines := leftH - 2
			if capByRows := rows - 6; capByRows > 0 && logLines > capByRows {
				logLines = capByRows
			}
			if logLines < 3 {
				logLines = 3
			}
			if maxBody := maxRightH - 2; maxBody > 0 && logLines > maxBody {
				logLines = maxBody
			}
			right = renderLogBox(p.history, rightW, logLines)
		}

		joined := joinColumns(left, right, leftW, rightW, responsiveGap)
		out = joined + "\n" + footer + "\n"
	} else {
		pipeline := renderPipelineBox(p.header, mode, p.stages, stage, spinIdx, fired, elapsed, idleDur, p.round, p.maxRounds, cols, rows)
		pipeH := strings.Count(pipeline, "\n") + 1
		remaining := maxTotalLines - pipeH - 1 // leave 1 row for footer

		var lower string
		gap := "\n\n"
		switch mode {
		case modeGate:
			if remaining >= 4 {
				gapLines := 1
				if remaining < 6 {
					gap = "\n"
					gapLines = 0
				}
				maxBody := remaining - gapLines - 3 // 1 action bar + 2 borders
				if maxBody < 1 {
					maxBody = 1
				}
				lower = renderGateSection(p.gateID, p.gateDet, p.gateTitle, p.actionBarOverride, p.gateLines, cols, maxBody)
			} else if remaining >= 1 {
				gap = "\n"
				if p.actionBarOverride != "" {
					lower = truncateDisplay(p.actionBarOverride, cols)
				} else {
					lower = renderActionBar(cols)
				}
			}
		case modeStall:
			if remaining >= 3 {
				wantBody := 2
				if denied != "" {
					wantBody = 3
				}
				gapLines := 1
				if remaining-1-2 < wantBody {
					gap = "\n"
					gapLines = 0
				}
				maxBody := remaining - gapLines - 2
				if maxBody < 1 {
					maxBody = 1
				}
				lower = renderWarningBox(idleDur, denied, cols, maxBody)
			}
		default:
			if rows >= 14 && remaining >= 3 {
				gapLines := 1
				if remaining < 4 {
					gap = "\n"
					gapLines = 0
				}
				logLines := remaining - gapLines - 2
				if logLines > 8 {
					logLines = 8
				}
				if logLines >= 1 {
					lower = renderLogBox(p.history, cols, logLines)
				}
			}
		}

		if lower != "" {
			out = pipeline + gap + lower + "\n" + footer + "\n"
		} else {
			out = pipeline + "\n" + footer + "\n"
		}
	}

	p.renderedLines = strings.Count(out, "\n")
	return out
}

// Teardown returns ANSI escape sequences that erase the currently rendered
// panel: cursor-up N followed by clear-to-end-of-screen, where N is the exact
// line count of the last Render call. It is idempotent — after a Teardown the
// line count is reset to 0, so a second Teardown is a no-op.
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
// Question remains for callers that paint the question as a plain block.
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
