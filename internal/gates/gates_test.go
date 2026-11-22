package gates

import (
	"os"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	got := Parse("noise\nCKB-WHITEHAT-GATE: triage hypothesis list\nCKB-WHITEHAT-GATE: permission\nCKB-WHITEHAT-GATE: publish ready\nCKB-WHITEHAT-GATE: arbitrary-evil payload\nCKB-WHITEHAT-GATE: triage-evil fake\n")
	if len(got) != 3 ||
		got[0].ID != "triage" || got[0].Details != "hypothesis list" ||
		got[1].ID != "permission" || got[1].Details != "" ||
		got[2].ID != "publish" || got[2].Details != "ready" {
		t.Fatalf("unexpected gates: %#v", got)
	}
}

func TestSanitizeTerminal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "CSI colors and screen erase",
			in:   "\x1b[31mRED\x1b[0m\x1b[2J",
			want: "RED",
		},
		{
			name: "CSI private mode hide cursor",
			in:   "\x1b[?25lhidden\x1b[?25h",
			want: "hidden",
		},
		{
			name: "OSC BEL terminated",
			in:   "\x1b]0;evil\x07ok",
			want: "ok",
		},
		{
			name: "OSC ST terminated",
			in:   "\x1b]52;c;cHduZWQ=\x1b\\hello",
			want: "hello",
		},
		{
			name: "C0 and DEL controls",
			in:   "\x00a\x07b\x08c\x1fd\x7fe",
			want: "abcde",
		},
		{
			name: "C1 controls and 8-bit CSI/OSC",
			in:   "\u0085a\u009b31mb\u009d0;evil\u009cc",
			want: "abc",
		},
		{
			name: "preserves printable text and newlines",
			in:   "line1\nline2",
			want: "line1\nline2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeTerminal(tc.in); got != tc.want {
				t.Errorf("SanitizeTerminal(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeSingleLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "flattens newlines and carriage returns",
			in:   "a\nb\rc",
			want: "a b c",
		},
		{
			name: "flattens tabs and strips ANSI escapes",
			in:   "\x1b[31mRED\x1b[0m\n\x1b]0;evil\x07ok\t\x1b]52;c;cHduZWQ=\x1b\\end",
			want: "RED ok end",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeSingleLine(tc.in); got != tc.want {
				t.Errorf("SanitizeSingleLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
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
