//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

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

// TestIsTerminalFalseForDevNullAndRegularFile asserts that non-TTY character
// devices such as /dev/null and regular files are rejected by fileIsTerminal
// (which uses term.IsTerminal / TCGETS ioctl rather than os.ModeCharDevice),
// so `ckb-whitehat run >/dev/null` does not enable the live TTY box.
func TestIsTerminalFalseForDevNullAndRegularFile(t *testing.T) {
	if fileIsTerminal(nil) {
		t.Error("fileIsTerminal(nil) = true, want false")
	}

	f, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("/dev/null unavailable: %v", err)
	}
	defer f.Close()
	if fileIsTerminal(f) {
		t.Error("fileIsTerminal(/dev/null) = true, want false: /dev/null is a character device, not a TTY")
	}

	reg, err := os.Create(filepath.Join(t.TempDir(), "regular"))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	if fileIsTerminal(reg) {
		t.Error("fileIsTerminal(regular file) = true, want false")
	}
}

// TestReadSingleKeyNoTTY covers the error path when /dev/tty is absent
// (CI has no controlling terminal): ReadSingleKey must return the zero rune
// and an error rather than block or read a queued newline. It is skipped
// when /dev/tty IS present (local dev shells), where the error path does
// not apply.
func TestReadSingleKeyNoTTY(t *testing.T) {
	probe, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err == nil {
		probe.Close()
		t.Skip("/dev/tty is present; the no-TTY error path only applies without a controlling terminal (CI)")
	}
	r, err := ReadSingleKey()
	if r != 0 {
		t.Errorf("ReadSingleKey() rune = %q, want zero rune", r)
	}
	if err == nil {
		t.Error("ReadSingleKey() error = nil, want an error when /dev/tty is absent")
	}
}
