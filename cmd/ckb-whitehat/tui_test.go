package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestInferStagePrecedence(t *testing.T) {
	cases := []struct {
		name    string
		history []string
		want    string
	}{
		{"empty is triage", nil, "triage"},
		{"hypothesis marker only", []string{"scanning hypothes for attack surface"}, "hypotheses"},
		{"poc marker beats hypotheses", []string{"writing 3 hypothes", "writing a PoC: poc/f1.mjs"}, "poc"},
		{"fired verdict beats poc", []string{"writing poc/f1.mjs", "FINDING F1 FIRED"}, "firing"},
		{"result marker beats everything", []string{
			"FINDING F1 FIRED",
			"CKB-WHITEHAT-RESULT: findings=1 fired=1 report=report.md",
		}, "report"},
		{"higher marker later still wins", []string{
			"writing a poc",
			"hypothesis H1: overflow",
			"FINDING F2 UNPROVEN",
		}, "firing"},
		{"verdict words are exact tokens", []string{"FINDING F1 FIZZLED"}, "triage"},
		{"case-insensitive poc variants", []string{"writing a PoC: poc/f1.mjs"}, "poc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := InferStage(c.history); got != c.want {
				t.Errorf("InferStage(%q) = %q, want %q", c.history, got, c.want)
			}
		})
	}
}

func TestSpinFrameCycles(t *testing.T) {
	if got := SpinFrame(10); got != SpinFrame(0) {
		t.Errorf("SpinFrame(10) = %q, want same as SpinFrame(0) = %q", got, SpinFrame(0))
	}
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		f := SpinFrame(i)
		if seen[f] {
			t.Fatalf("frame %q repeated at index %d", f, i)
		}
		seen[f] = true
	}
	// Negative indices wrap: -1 ≡ 9 and -11 ≡ 9 (mod 10), both the last frame.
	last := SpinFrame(9)
	if got := SpinFrame(-1); got != last {
		t.Errorf("SpinFrame(-1) = %q, want last frame %q", got, last)
	}
	if got := SpinFrame(-11); got != last {
		t.Errorf("SpinFrame(-11) = %q, want frame 9 %q", got, last)
	}
}

func TestClearLines(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, ""},
		{-3, ""},
		{1, "\x1b[1A\x1b[J"},
		{3, "\x1b[3A\x1b[J"},
		{7, "\x1b[7A\x1b[J"},
	}
	for _, c := range cases {
		if got := ClearLines(c.n); got != c.want {
			t.Errorf("ClearLines(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestDisplayWidthAndTruncate(t *testing.T) {
	if got := displayWidth("hello"); got != 5 {
		t.Errorf("displayWidth(hello) = %d, want 5", got)
	}
	if got := displayWidth(styleBoldCyan("Pipeline")); got != 8 {
		t.Errorf("displayWidth(styled Pipeline) = %d, want 8", got)
	}
	if got := displayWidth("╭─│╰╯⠋→✓✗○"); got != 10 {
		t.Errorf("displayWidth(box/braille/arrows) = %d, want 10", got)
	}
	if got := displayWidth("⚠"); got != 2 {
		t.Errorf("displayWidth(⚠) = %d, want 2", got)
	}
	if got := displayWidth("日本語"); got != 6 {
		t.Errorf("displayWidth(日本語) = %d, want 6", got)
	}
	if got := displayWidth("🔥"); got != 2 {
		t.Errorf("displayWidth(🔥) = %d, want 2", got)
	}
	if got := truncateDisplay("abcdef", 0); got != "" {
		t.Errorf("truncateDisplay(..., 0) = %q, want empty", got)
	}
	if got := truncateDisplay("abcdef", -5); got != "" {
		t.Errorf("truncateDisplay(..., -5) = %q, want empty", got)
	}
	if got := truncateDisplay("abcdef", 4); got != "abc…" || displayWidth(got) != 4 {
		t.Errorf("truncateDisplay(abcdef, 4) = %q (w=%d), want abc… (w=4)", got, displayWidth(got))
	}
	if got := truncateDisplay("日本語テスト", 5); got != "日本…" || displayWidth(got) != 5 {
		t.Errorf("truncateDisplay(日本語テスト, 5) = %q (w=%d), want 日本… (w=5)", got, displayWidth(got))
	}
	if got := truncateDisplay("日本語テスト", 4); displayWidth(got) > 4 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncateDisplay(日本語テスト, 4) = %q (w=%d), want <= 4 ending in …", got, displayWidth(got))
	}
	padded := padDisplay(styleGreen("ok"), 6)
	if displayWidth(padded) != 6 {
		t.Errorf("displayWidth(padDisplay(ok, 6)) = %d, want 6", displayWidth(padded))
	}
}

func TestNewLivePanelStallLimit(t *testing.T) {
	// Both limits construct cleanly.
	if p := NewLivePanel("h", 3, 0); p == nil {
		t.Fatal("NewLivePanel with zero stallLimit must construct")
	}
	p := NewLivePanel("h", 3, 300*time.Second)
	if p.stallLimit != 300*time.Second {
		t.Errorf("stallLimit = %s, want 300s", p.stallLimit)
	}

	// With stallLimit 0 the stall lower panel never triggers, even after
	// forcing a huge idle.
	none := NewLivePanel("h", 3, 0)
	none.OnLine("x")
	none.ForceIdleForTest(10 * time.Hour)
	if got := stripANSI(none.Render()); strings.Contains(got, "⚠ no output") {
		t.Errorf("stallLimit 0 must never show the stall warning: %q", got)
	}
}

func TestBoxGateView(t *testing.T) {
	p := NewLivePanel("h", 3, 300*time.Second)
	p.OnLine("a")
	p.OnLine("b")
	p.OnLine("c")
	p.SetGate("permission", "agent wants X")

	got := p.Render()
	plain := stripANSI(got)
	for _, want := range []string{
		"╭─ Pipeline", "awaiting", "⏸",
		"╭─ Checkpoint", "permission", "agent wants X",
		"a approve", "s skip", "d deny", "x stop", "╰",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("gate-view Render missing %q in:\n%s", want, plain)
		}
	}
	// The gate view suppresses the Log box.
	if strings.Contains(plain, "╭─ Log") {
		t.Errorf("gate view must not show the Log box:\n%s", plain)
	}

	// Clearing the gate returns the lower panel to the Log view.
	p.SetGate("", "")
	plain = stripANSI(p.Render())
	if !strings.Contains(plain, "╭─ Log") || !strings.Contains(plain, "c") || !strings.Contains(plain, "running") {
		t.Errorf("after SetGate(\"\",\"\"), panel must return to Log view:\n%s", plain)
	}
}

func TestBoxStallWarning(t *testing.T) {
	p := NewLivePanel("h", 3, 300*time.Second)
	p.OnLine("a")
	p.OnLine("⚠ permission denied: Bash(curl:*)")
	p.OnLine("c")
	p.ForceIdleForTest(160 * time.Second) // >= stallLimit/2 = 150s

	plain := stripANSI(p.Render())
	for _, want := range []string{
		"╭─ Warning", "stalled?", "⚠ no output",
		"permission prompt (allowlist)", "Ctrl-C to stop",
		"⚠ permission denied: Bash(curl:*)",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("stalled Render missing %q in:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "╭─ Log") {
		t.Errorf("stalled Render must not show the Log box:\n%s", plain)
	}

	// With stallLimit 0 the warning never appears even at the same idle.
	none := NewLivePanel("h", 3, 0)
	none.OnLine("a")
	none.ForceIdleForTest(160 * time.Second)
	if got := stripANSI(none.Render()); strings.Contains(got, "⚠ no output") {
		t.Errorf("stallLimit 0 must suppress the stall warning:\n%s", got)
	}
}

func TestBoxTeardownLineCount(t *testing.T) {
	p := NewLivePanel("h", 3, 300*time.Second)
	p.OnLine("a")
	p.SetGate("g", "detail")
	got := p.Render()
	n := strings.Count(got, "\n")
	if n <= 0 {
		t.Fatalf("Render produced no lines: %q", got)
	}
	teardown := p.Teardown()
	if want := fmt.Sprintf("\x1b[%dA\x1b[J", n); teardown != want {
		t.Errorf("Teardown = %q, want %q", teardown, want)
	}
}

func TestLivePanelRenderGrows(t *testing.T) {
	p := NewLivePanel("running claude against /t", 3, 0)

	first := p.Render()
	firstPlain := stripANSI(first)
	if !strings.Contains(firstPlain, "waiting for agent activity…") {
		t.Fatalf("empty-history Render missing waiting placeholder:\n%s", firstPlain)
	}
	firstLines := strings.Count(first, "\n")

	p.OnLine("triage started")
	p.OnLine("writing poc/f1.mjs")
	p.OnLine("FINDING F1 FIRED")

	got := p.Render()
	plain := stripANSI(got)
	for _, want := range []string{
		"╭─ Pipeline", "╭─ Log", "1 fired", "round 1/3",
		"triage started", "writing poc/f1.mjs", "FINDING F1 FIRED",
		"╰", "Ctrl-C stop",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("Render missing %q in:\n%s", want, plain)
		}
	}
	gotLines := strings.Count(got, "\n")
	if gotLines <= firstLines {
		t.Errorf("Render after 3 lines has %d lines, expected growth beyond %d", gotLines, firstLines)
	}
	// Teardown must erase exactly what the last Render painted.
	if teardown, want := p.Teardown(), fmt.Sprintf("\x1b[%dA\x1b[J", gotLines); teardown != want {
		t.Errorf("Teardown = %q, want %q", teardown, want)
	}
}

func TestLivePanelTailIsLastThreeInOrder(t *testing.T) {
	p := NewLivePanel("h", 1, 0)
	for i := 0; i < 250; i++ { // 250 lines also exercises the 200-line cap
		p.OnLine(fmt.Sprintf("L%d", i))
	}
	if got := len(p.history); got != 200 {
		t.Fatalf("history keeps %d lines, want the 200-line cap", got)
	}
	// Default 80x24 stacked mode caps Log tail at 8 lines (L242..L249).
	plain := stripANSI(p.Render())
	i247, i248, i249 := strings.Index(plain, "L247"), strings.Index(plain, "L248"), strings.Index(plain, "L249")
	if i247 == -1 || i248 == -1 || i249 == -1 || !(i247 < i248 && i248 < i249) {
		t.Errorf("tail out of chronological order or missing in:\n%s", plain)
	}
	if !strings.Contains(plain, "L242") {
		t.Errorf("expected L242 in 8-line stacked tail:\n%s", plain)
	}
	if strings.Contains(plain, "L241") {
		t.Errorf("L241 should be excluded from 8-line stacked tail:\n%s", plain)
	}
}

func TestLivePanelTeardownErasesRenderedLines(t *testing.T) {
	p := NewLivePanel("h", 2, 0)
	// Fresh panel: nothing rendered yet.
	if got := p.Teardown(); got != "" {
		t.Errorf("Teardown before any Render = %q, want empty", got)
	}
	p.OnLine("a")
	p.OnLine("b")
	p.OnLine("c")
	p.OnLine("d")
	got := p.Render()
	n := strings.Count(got, "\n")
	if n <= 0 {
		t.Fatalf("Render produced %d lines, want > 0: %q", n, got)
	}
	teardown := p.Teardown()
	if want := fmt.Sprintf("\x1b[%dA\x1b[J", n); teardown != want {
		t.Errorf("Teardown after %d-line Render = %q, want %q", n, teardown, want)
	}
	// Idempotent second Teardown is a no-op.
	if second := p.Teardown(); second != "" {
		t.Errorf("second Teardown = %q, want empty", second)
	}
}

func TestQuestionPanel(t *testing.T) {
	p := NewLivePanel("h", 1, 0)
	got := p.Question("g2", "agent fired 2 of 5 findings")
	for _, want := range []string{
		"── checkpoint", "[a]pprove", "[s]kip", "[d]eny", "[x]top", "g2", "agent fired 2 of 5 findings",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Question missing %q in %q", want, got)
		}
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("Question must end with newline: %q", got)
	}

	// Empty detail: the middle line still carries the gate id alone.
	got = p.Question("g3", "")
	for _, want := range []string{"\n[a]pprove  [s]kip  [d]eny  [x]top\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("Question with empty detail missing %q in %q", want, got)
		}
	}
	if !strings.Contains(got, "\ng3\n") {
		t.Errorf("Question with empty detail should have the gate id on its own line: %q", got)
	}
}

func TestLivePanelSanitizesEscapesAndNewlines(t *testing.T) {
	p := NewLivePanel("running\n\x1b[31mclaude\x1b[0m", 3, 0)
	p.OnLine("\x1b[31mRED\x1b[0m\x1b[2J\nline2")
	p.OnLine("\x1b]0;evil\x07ok\r\x1b]52;c;cHduZWQ=\x1b\\tail")

	// Stored state itself must never retain escape sequences or newlines.
	for _, stored := range append([]string{p.header}, p.history...) {
		if strings.ContainsAny(stored, "\x1b\n\r\x07") {
			t.Errorf("stored TUI state retained control/escape char: %q", stored)
		}
	}

	rendered := p.Render()
	for _, forbidden := range []string{"\x1b[31m", "2J", "0;evil", "52;c;cHduZWQ=", "\x1b]", "\x07"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("Render leaked untrusted escape fragment %q in %q", forbidden, rendered)
		}
	}
	plain := stripANSI(rendered)
	if strings.Contains(plain, "\x1b") {
		t.Errorf("stripANSI(Render) still contains ESC: %q", plain)
	}
	for _, want := range []string{"running claude", "RED line2", "ok tail"} {
		if !strings.Contains(plain, want) {
			t.Errorf("Render missing sanitized content %q in:\n%s", want, plain)
		}
	}
	actualLines := strings.Count(rendered, "\n")
	if actualLines != p.renderedLines {
		t.Errorf("strings.Count(rendered, \\n) = %d, renderedLines = %d", actualLines, p.renderedLines)
	}
	if teardown, want := p.Teardown(), fmt.Sprintf("\x1b[%dA\x1b[J", actualLines); teardown != want {
		t.Errorf("Teardown = %q, want %q", teardown, want)
	}

	p.SetGate("triage\x1b[31m\nextra", "\x1b]0;title\x07check\n\x1b]52;c;cHduZWQ=\x1b\\\x1b[?25lnow")
	rendered = p.Render()
	for _, forbidden := range []string{"\x1b[31m", "0;title", "52;c;cHduZWQ=", "?25l", "\x1b]", "\x07"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("gate Render leaked untrusted escape fragment %q in %q", forbidden, rendered)
		}
	}
	plain = stripANSI(rendered)
	if strings.Contains(plain, "\x1b") {
		t.Errorf("stripANSI(gate Render) still contains ESC: %q", plain)
	}
	for _, want := range []string{"triage extra", "check now"} {
		if !strings.Contains(plain, want) {
			t.Errorf("gate Render missing sanitized content %q in:\n%s", want, plain)
		}
	}
	actualLines = strings.Count(rendered, "\n")
	if actualLines != p.renderedLines {
		t.Errorf("gate strings.Count(rendered, \\n) = %d, renderedLines = %d", actualLines, p.renderedLines)
	}
	if teardown, want := p.Teardown(), fmt.Sprintf("\x1b[%dA\x1b[J", actualLines); teardown != want {
		t.Errorf("gate Teardown = %q, want %q", teardown, want)
	}

	q := p.Question("triage\x1b[31m\nbad", "\x1b]52;c;cHduZWQ=\x1b\\ok\nline2")
	for _, forbidden := range []string{"\x1b", "31m", "52;c;cHduZWQ="} {
		if strings.Contains(q, forbidden) {
			t.Errorf("Question leaked escape fragment %q in %q", forbidden, q)
		}
	}
	if gotLines := strings.Count(q, "\n"); gotLines != 3 {
		t.Errorf("Question line count = %d, want 3: %q", gotLines, q)
	}
}

func TestOverflowContract(t *testing.T) {
	widths := []int{40, 60, 80, 100, 120, 160}
	heights := []int{16, 20, 24, 30, 40}
	modes := []string{"tail", "stall", "gate"}

	longASCII := strings.Repeat("abcdefghij", 30) // 300 chars
	longWide := "⚠ " + strings.Repeat("日本語テスト🔥", 45)
	longANSI := "\x1b[31m" + strings.Repeat("→ Read /very/long/path/src/handler.go ", 10) + "\x1b[0m"
	longDenied := "⚠ permission denied: Bash(curl:" + strings.Repeat("x", 280) + ")"
	longHeader := "claude · " + strings.Repeat("super-long-target-repo-name-", 12)
	longGateDet := "⚠ " + strings.Repeat("日本語detail-token ", 25)

	for _, mode := range modes {
		for _, cols := range widths {
			for _, rows := range heights {
				name := fmt.Sprintf("%s_%dx%d", mode, cols, rows)
				t.Run(name, func(t *testing.T) {
					p := NewLivePanel(longHeader, 4, 300*time.Second)
					p.SetSize(cols, rows)
					p.SetRound(2)
					p.OnLine("→ Read src/app.js")
					p.OnLine("✗ error: " + longASCII)
					p.OnLine(longWide)
					p.OnLine(longANSI)
					p.OnLine("FINDING F1 FIRED " + longASCII)
					p.OnLine(longDenied)
					longWidePrefix := string([]rune(longWide)[:20])
					for i := 0; i < 15; i++ {
						p.OnLine(fmt.Sprintf("line-%d %s %s", i, longWidePrefix, longASCII[:120]))
					}

					switch mode {
					case "stall":
						p.ForceIdleForTest(200 * time.Second)
					case "gate":
						p.SetGate("permission_"+strings.Repeat("g", 60), longGateDet)
					}

					out := p.Render()
					if !strings.HasSuffix(out, "\n") {
						t.Fatalf("Render output must end with newline")
					}
					totalLines := strings.Count(out, "\n")
					if totalLines > rows-1 {
						t.Errorf("total rendered lines = %d, exceeds rows-1 = %d\n%s", totalLines, rows-1, stripANSI(out))
					}

					lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
					if len(lines) != totalLines {
						t.Errorf("split line count %d != newline count %d", len(lines), totalLines)
					}
					for idx, line := range lines {
						if w := displayWidth(line); w > cols {
							t.Errorf("line %d overflows cols=%d (displayWidth=%d): %q", idx, cols, w, stripANSI(line))
						}
					}

					wantTeardown := fmt.Sprintf("\x1b[%dA\x1b[J", totalLines)
					if gotTeardown := p.Teardown(); gotTeardown != wantTeardown {
						t.Errorf("Teardown() = %q, want %q", gotTeardown, wantTeardown)
					}
				})
			}
		}
	}
}

func TestResponsiveLayoutBreakpoint(t *testing.T) {
	setup := func(cols, rows int) []string {
		p := NewLivePanel("claude · target", 4, 0)
		p.SetSize(cols, rows)
		p.OnLine("→ Read src/index.js")
		p.OnLine("scanning hypotheses")
		plain := stripANSI(p.Render())
		return strings.Split(strings.TrimSuffix(plain, "\n"), "\n")
	}

	// Width 99: stacked layout (Pipeline above Log, NOT on the same top line).
	lines99 := setup(99, 24)
	if !strings.Contains(lines99[0], "╭─ Pipeline") {
		t.Fatalf("width=99 line 0 missing Pipeline box: %q", lines99[0])
	}
	if strings.Contains(lines99[0], "╭─ Log") {
		t.Errorf("width=99 must be stacked, not side-by-side: %q", lines99[0])
	}
	foundLogBelow := false
	for _, l := range lines99[1:] {
		if strings.Contains(l, "╭─ Log") {
			foundLogBelow = true
			break
		}
	}
	if !foundLogBelow {
		t.Errorf("width=99 missing stacked Log box below Pipeline:\n%s", strings.Join(lines99, "\n"))
	}

	// Width 100 and 120: side-by-side 2-column layout (Pipeline and Log share line 0).
	for _, w := range []int{100, 120} {
		lines := setup(w, 24)
		if !strings.Contains(lines[0], "╭─ Pipeline") || !strings.Contains(lines[0], "╭─ Log") {
			t.Errorf("width=%d must render Pipeline and Log side-by-side on line 0, got: %q", w, lines[0])
		}
	}

	// Compact height < 14 in stacked mode hides Log box cleanly.
	linesTiny := setup(80, 13)
	if strings.Contains(strings.Join(linesTiny, "\n"), "╭─ Log") {
		t.Errorf("rows=13 stacked mode must hide Log box cleanly:\n%s", strings.Join(linesTiny, "\n"))
	}

	// Vertical connectors │ appear when rows >= 26 and are suppressed when rows < 26.
	hasConnector := func(lines []string) bool {
		for _, l := range lines {
			if strings.HasPrefix(l, "│ │") {
				return true
			}
		}
		return false
	}
	if hasConnector(setup(80, 24)) {
		t.Errorf("rows=24 must suppress vertical stage connectors")
	}
	if !hasConnector(setup(80, 26)) {
		t.Errorf("rows=26 must render vertical stage connectors")
	}
}

func TestPipelineStageProgression(t *testing.T) {
	isSpin := func(r rune) bool {
		return strings.ContainsRune(spinFrames, r)
	}
	stageRow := func(plain, label string) string {
		for _, line := range strings.Split(plain, "\n") {
			if strings.Contains(line, " "+label+" ") || strings.HasSuffix(strings.TrimRight(line, "│ "), " "+label) {
				return line
			}
		}
		return ""
	}
	rowIcon := func(row string) rune {
		// Inside a box row "│ <icon> <label> ... │", strip leading "│ ".
		trimmed := strings.TrimPrefix(row, "│ ")
		for _, r := range trimmed {
			return r
		}
		return 0
	}

	stages := []struct {
		name      string
		feed      []string
		activeIdx int
	}{
		{"triage", nil, 0},
		{"hypotheses", []string{"forming hypothesis H1"}, 1},
		{"poc", []string{"forming hypothesis H1", "writing poc/h1.mjs"}, 2},
		{"firing", []string{"forming hypothesis H1", "writing poc/h1.mjs", "FINDING H1 FIRED"}, 3},
		{"report", []string{"forming hypothesis H1", "writing poc/h1.mjs", "FINDING H1 FIRED", "CKB-WHITEHAT-RESULT: findings=1 fired=1 report=FINDINGS.md"}, 4},
	}
	labels := []string{"Triage", "Hypotheses", "PoCs", "Firing", "Report"}

	for _, sc := range stages {
		t.Run(sc.name, func(t *testing.T) {
			p := NewLivePanel("claude · repo", 4, 0)
			p.SetSize(80, 24)
			for _, line := range sc.feed {
				p.OnLine(line)
			}
			plain := stripANSI(p.Render())
			for i, label := range labels {
				row := stageRow(plain, label)
				if row == "" {
					t.Fatalf("missing stage row %q in:\n%s", label, plain)
				}
				icon := rowIcon(row)
				switch {
				case i < sc.activeIdx:
					if icon != '✓' {
						t.Errorf("stage %s (before active): icon = %q, want '✓' (row: %q)", label, icon, row)
					}
				case i == sc.activeIdx:
					if !isSpin(icon) {
						t.Errorf("stage %s (active): icon = %q, want spinner frame (row: %q)", label, icon, row)
					}
				default:
					if icon != '○' {
						t.Errorf("stage %s (pending): icon = %q, want '○' (row: %q)", label, icon, row)
					}
				}
			}
			if sc.activeIdx >= 3 {
				firingRow := stageRow(plain, "Firing")
				if !strings.Contains(firingRow, "1 fired") {
					t.Errorf("Firing row missing '1 fired' badge: %q", firingRow)
				}
			}
		})
	}

	// Gate active replaces spinner on active stage with ⏸.
	p := NewLivePanel("claude · repo", 4, 0)
	p.SetSize(80, 24)
	p.OnLine("writing poc/h1.mjs")
	p.SetGate("triage", "confirm PoCs")
	plain := stripANSI(p.Render())
	pocRow := stageRow(plain, "PoCs")
	if got := rowIcon(pocRow); got != '⏸' {
		t.Errorf("active stage under gate: icon = %q, want '⏸' (row: %q)", got, pocRow)
	}
}
