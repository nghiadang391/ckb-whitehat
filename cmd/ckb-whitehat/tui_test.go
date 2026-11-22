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
	if got := none.Render(); strings.Contains(got, "⚠ no output") {
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
	for _, want := range []string{"┌", "permission", "agent wants X", "[a]pprove  [s]kip  [d]eny  [x]stop", "└"} {
		if !strings.Contains(got, want) {
			t.Errorf("gate-view Render missing %q in %q", want, got)
		}
	}
	// The gate view suppresses the agent tail.
	if strings.Contains(got, "── agent ─") {
		t.Errorf("gate view must not show the agent tail: %q", got)
	}

	// Clearing the gate returns the lower panel to the agent-tail view.
	p.SetGate("", "")
	got = p.Render()
	if !strings.Contains(got, "── agent ─") || !strings.Contains(got, "· c") {
		t.Errorf("after SetGate(\"\",\"\"), lower panel must be the agent tail: %q", got)
	}
}

func TestBoxStallWarning(t *testing.T) {
	p := NewLivePanel("h", 3, 300*time.Second)
	p.OnLine("a")
	p.OnLine("b")
	p.OnLine("c")
	p.ForceIdleForTest(160 * time.Second) // >= stallLimit/2 = 150s

	got := p.Render()
	if !strings.Contains(got, "⚠ no output") {
		t.Errorf("stalled Render missing the warning: %q", got)
	}
	if strings.Contains(got, "── agent ─") {
		t.Errorf("stalled Render must not show the agent tail: %q", got)
	}
	for _, want := range []string{"permission prompt (allowlist)", "Ctrl-C to stop"} {
		if !strings.Contains(got, want) {
			t.Errorf("stalled Render missing %q in %q", want, got)
		}
	}

	// With stallLimit 0 the warning never appears even at the same idle.
	none := NewLivePanel("h", 3, 0)
	none.OnLine("a")
	none.ForceIdleForTest(160 * time.Second)
	if got := none.Render(); strings.Contains(got, "⚠ no output") {
		t.Errorf("stallLimit 0 must suppress the stall warning: %q", got)
	}
}

func TestBoxTeardownLineCount(t *testing.T) {
	p := NewLivePanel("h", 3, 300*time.Second)
	p.OnLine("a")
	p.SetGate("g", "detail")
	got := p.Render()
	n := len(strings.Split(strings.TrimRight(got, "\n"), "\n"))
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
	// Empty history: top border, status row, agent-tail divider, bottom border.
	if n := len(strings.Split(strings.TrimRight(first, "\n"), "\n")); n != 4 {
		t.Fatalf("empty-history Render = %q, want exactly 4 lines (2 borders + status + divider)", first)
	}

	p.OnLine("triage started")
	p.OnLine("writing poc/f1.mjs")
	p.OnLine("FINDING F1 FIRED")

	got := p.Render()
	for _, want := range []string{
		"stage: firing", "round 1/3",
		"· triage started", "· writing poc/f1.mjs", "· FINDING F1 FIRED",
		"┌", "└",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Render missing %q in %q", want, got)
		}
	}
	// Borders + status + 3 tail lines = 7 lines.
	if n := len(strings.Split(strings.TrimRight(got, "\n"), "\n")); n != 7 {
		t.Errorf("Render after 3 lines has %d lines, want 7 (2 borders + status + 3 tails)", n)
	}
	// Teardown must erase exactly what the last Render painted.
	if teardown := p.Teardown(); teardown != "\x1b[7A\x1b[J" {
		t.Errorf("Teardown after 7-line render = %q, want cursor-up 7 + clear-to-end", teardown)
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
	got := p.Render()
	// Tail shows the last 3 lines, in chronological order.
	i247, i248 := strings.Index(got, "L247"), strings.Index(got, "L248")
	if i247 == -1 || i248 == -1 || i247 > i248 {
		t.Errorf("tail out of order or missing in %q", got)
	}
	if !strings.Contains(got, "L249") {
		t.Errorf("newest line L249 missing from %q", got)
	}
	if strings.Contains(got, "L246") {
		t.Errorf("L246 should not be in the 3-line tail: %q", got)
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
	got := p.Render() // 2 borders + status + 3 tails = 7 lines
	n := len(strings.Split(strings.TrimRight(got, "\n"), "\n"))
	if n != 7 {
		t.Fatalf("Render produced %d lines, want 7: %q", n, got)
	}
	got = p.Teardown()
	if want := "\x1b[7A\x1b[J"; got != want {
		t.Errorf("Teardown after 7-line Render = %q, want %q", got, want)
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

	rendered := p.Render()
	for _, forbidden := range []string{"\x1b", "31m", "2J", "0;evil", "52;c;cHduZWQ="} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("Render leaked escape fragment %q in %q", forbidden, rendered)
		}
	}
	for _, want := range []string{"running claude", "RED line2", "ok tail"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("Render missing sanitized content %q in %q", want, rendered)
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
	for _, forbidden := range []string{"\x1b", "31m", "0;title", "52;c;cHduZWQ=", "?25l"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("gate Render leaked escape fragment %q in %q", forbidden, rendered)
		}
	}
	for _, want := range []string{"triage extra", "check now"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("gate Render missing sanitized content %q in %q", want, rendered)
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
