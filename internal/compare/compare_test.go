package compare

import "testing"

func TestParseAndMatchByContent(t *testing.T) {
	base := "## F4 — Wildcard CORS leak (HIGH)\n## F5 — ledger race"
	fresh := "## F9 — Wildcard CORS error leak\n## F5 — unrelated issue"
	r := Compare(fresh, base)
	if len(r.Covered) != 1 || len(r.Missed) != 1 || len(r.Novel) != 1 {
		t.Fatalf("%+v", r)
	}
}
func TestSameIDNeedsMinimumOverlap(t *testing.T) {
	r := Compare("## F1 — payment callback", "## F1 — unrelated database issue")
	if len(r.Covered) != 0 {
		t.Fatalf("%+v", r)
	}
}
func TestRequiredSections(t *testing.T) {
	r := Compare("## At a glance\nOn-chain evidence\nLayer boundary", "")
	if !r.Sections[0].Present || !r.Sections[1].Present || !r.Sections[2].Present || r.Sections[3].Present {
		t.Fatalf("%+v", r.Sections)
	}
}
func TestParseFindingsPreservesInnerParens(t *testing.T) {
	md := "## F2 — Double-spend race in `ledger.js debit()` (HIGH, PROVEN)"
	got := ParseFindings(md)
	if len(got) != 1 {
		t.Fatalf("expected 1 finding, got %+v", got)
	}
	wantTitle := "Double-spend race in `ledger.js debit()`"
	if got[0].ID != "F2" || got[0].Title != wantTitle {
		t.Fatalf("got %+v, want ID=F2 Title=%q", got[0], wantTitle)
	}
}
func TestParseFindingsMatchesAllCIDs(t *testing.T) {
	md := "## C1 — baseline control\n## C4.2 — forged callback"
	got := ParseFindings(md)
	if len(got) != 2 {
		t.Fatalf("expected 2 findings, got %+v", got)
	}
	if got[0].ID != "C1" || got[0].Title != "baseline control" {
		t.Fatalf("first finding = %+v", got[0])
	}
	if got[1].ID != "C4.2" || got[1].Title != "forged callback" {
		t.Fatalf("second finding = %+v", got[1])
	}
}
