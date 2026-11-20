//go:build !windows

package main

import "testing"

// TestIsTerminalFalseWhenPiped asserts the panel-stay-disabled condition:
// under `go test` stdout is always piped (captured by the test harness or
// redirected by CI), so IsTerminal() must report false. It would only be
// flaky if someone ran the test binary attached to an interactive TTY,
// which is not how go test / CI invoke it.
func TestIsTerminalFalseWhenPiped(t *testing.T) {
	if IsTerminal() {
		t.Error("IsTerminal() = true under go test; stdout is piped, so the live panel must stay disabled")
	}
}
