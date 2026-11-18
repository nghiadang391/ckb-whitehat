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
