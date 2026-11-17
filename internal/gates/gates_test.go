package gates

import (
	"os"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	got := Parse("noise\nCKB-WHITEHAT-GATE: triage hypothesis list\nCKB-WHITEHAT-GATE: permission\n")
	if len(got) != 2 || got[0].ID != "triage" || got[0].Details != "hypothesis list" || got[1].ID != "permission" {
		t.Fatalf("unexpected gates: %#v", got)
	}
}

func TestPromptNoninteractiveDeniesWithoutReading(t *testing.T) {
	d, err := Prompt(strings.NewReader("approve\n"), nil, Gate{ID: "publish"}, false)
	if err != nil || d.Choice != "deny" {
		t.Fatalf("got decision=%#v err=%v", d, err)
	}
}

func TestPromptChoicesAndInvalidInput(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"approve\n", "approve"}, {"wat\n", "deny"}, {"stop\n", "stop"}} {
		var out strings.Builder
		d, err := Prompt(strings.NewReader(tc.input), &out, Gate{ID: "triage", Details: "three hypotheses"}, true)
		if err != nil || d.Choice != tc.want || !strings.Contains(out.String(), "triage") {
			t.Fatalf("input=%q got=%#v err=%v output=%q", tc.input, d, err, out.String())
		}
	}
}

func TestStoreRoundTrip(t *testing.T) {
	path := t.TempDir() + "/decisions.json"
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Decision{GateID: "permission", Choice: "deny"}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Decisions) != 1 || got.Decisions[0].Choice != "deny" {
		t.Fatalf("bad round trip: %#v", got)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("bad permissions: %v %v", info, err)
	}
}
