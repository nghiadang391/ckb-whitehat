package main

import (
	"strings"
	"testing"
	"time"
)

func TestChooseLowerModePrecedence(t *testing.T) {
	cases := []struct {
		name       string
		gateActive bool
		idle       time.Duration
		stallLimit time.Duration
		want       lowerMode
	}{
		{"tail when idle low", false, 5 * time.Second, 300 * time.Second, modeTail},
		{"stall at exactly half", false, 150 * time.Second, 300 * time.Second, modeStall},
		{"tail just under half", false, 149 * time.Second, 300 * time.Second, modeTail},
		{"stall when idle high", false, 600 * time.Second, 300 * time.Second, modeStall},
		{"gate wins with zero idle", true, 0, 0, modeGate},
		{"gate beats stall", true, 150 * time.Second, 300 * time.Second, modeGate},
		{"zero stallLimit disables stall", false, 999999 * time.Second, 0, modeTail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ChooseLowerMode(c.gateActive, c.idle, c.stallLimit); got != c.want {
				t.Fatalf("ChooseLowerMode(%v, %v, %v) = %v, want %v",
					c.gateActive, c.idle, c.stallLimit, got, c.want)
			}
		})
	}
}

func TestStallWarningText(t *testing.T) {
	w := StallWarning(150 * time.Second)
	lines := strings.Split(w, "\n")
	if len(lines) != 2 {
		t.Fatalf("StallWarning(150s) = %q, want exactly 2 lines", w)
	}
	wantFirst := "⚠ no output for 2m30s — agent likely blocked on a"
	if lines[0] != wantFirst {
		t.Errorf("first line = %q, want %q", lines[0], wantFirst)
	}
	const wantSecond = "  permission prompt (allowlist). Widen it, or press x to stop."
	if lines[1] != wantSecond {
		t.Errorf("second line = %q, want %q", lines[1], wantSecond)
	}
	// The human time must match formatElapsed's mmss convention.
	if formatElapsed(150*time.Second) != "2m30s" {
		t.Errorf("formatElapsed(150s) = %q, want \"2m30s\"", formatElapsed(150*time.Second))
	}

	short := StallWarning(30 * time.Second)
	if !strings.HasPrefix(short, "⚠ no output for 30s") {
		t.Errorf("StallWarning(30s) = %q, want it to start with \"⚠ no output for 30s\"", short)
	}
	if !strings.Contains(short, "permission prompt") {
		t.Errorf("StallWarning(30s) = %q, want it to contain \"permission prompt\"", short)
	}
}
