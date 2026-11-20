package main

import (
	"fmt"
	"strings"
	"testing"
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
	if got := ClearLines(0); got != "" {
		t.Errorf("ClearLines(0) = %q, want empty", got)
	}
	if got := ClearLines(-3); got != "" {
		t.Errorf("ClearLines(-3) = %q, want empty", got)
	}
	got := ClearLines(3)
	wantUp := "\x1b[3A"
	if !strings.Contains(got, wantUp) {
		t.Errorf("ClearLines(3) = %q, want cursor-up %q", got, wantUp)
	}
	if n := strings.Count(got, "\x1b[2K"); n != 3 {
		t.Errorf("ClearLines(3) has %d line-clears, want 3", n)
	}
}

func TestLivePanelRenderGrows(t *testing.T) {
	p := NewLivePanel("running claude against /t", 3)

	first := p.Render()
	if n := strings.Count(first, "\n"); n != 1 {
		t.Fatalf("empty-history Render = %q, want exactly 1 line (1 trailing newline)", first)
	}

	p.OnLine("triage started")
	p.OnLine("writing poc/f1.mjs")
	p.OnLine("FINDING F1 FIRED")

	got := p.Render()
	for _, want := range []string{"stage: firing", "[round 1/3]", "  · triage started", "  · writing poc/f1.mjs", "  · FINDING F1 FIRED"} {
		if !strings.Contains(got, want) {
			t.Errorf("Render missing %q in %q", want, got)
		}
	}
	if n := strings.Count(got, "\n"); n != 4 {
		t.Errorf("Render after 3 lines has %d lines, want 4 (status + 3 tails)", n)
	}
	// Teardown must erase exactly what the last Render painted.
	if teardown := p.Teardown(); !strings.Contains(teardown, "\x1b[4A") || strings.Count(teardown, "\x1b[2K") != 4 {
		t.Errorf("Teardown after 4-line render = %q, want cursor-up 4 + 4 line-clears", teardown)
	}
}

func TestLivePanelTailIsLastThreeInOrder(t *testing.T) {
	p := NewLivePanel("h", 1)
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
	p := NewLivePanel("h", 2)
	// Fresh panel: nothing rendered yet.
	if got := p.Teardown(); got != "" {
		t.Errorf("Teardown before any Render = %q, want empty", got)
	}
	p.OnLine("a")
	p.OnLine("b")
	p.OnLine("c")
	p.OnLine("d")
	p.Render() // 4 lines
	got := p.Teardown()
	if n := strings.Count(got, "\x1b[2K"); n != 4 {
		t.Errorf("Teardown after 4-line Render has %d line-clears, want 4", n)
	}
	if !strings.Contains(got, "\x1b[4A") {
		t.Errorf("Teardown after 4-line Render missing cursor-up 4: %q", got)
	}
}

func TestQuestionPanel(t *testing.T) {
	p := NewLivePanel("h", 1)
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
