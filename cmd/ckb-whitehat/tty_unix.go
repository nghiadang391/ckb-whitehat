//go:build !windows

package main

import (
	"os"
	"unicode"

	"golang.org/x/term"
)

// IsTerminal reports whether os.Stdout is a character device (a TTY). Any
// error means the live panel stays disabled. NOTE: os.FileMode packs the
// char-device bit as os.ModeCharDevice; it is NOT the raw stat S_IFCHR value,
// so the check must use os.ModeCharDevice.
func IsTerminal() bool {
	return fileIsTerminal(os.Stdout)
}

// fileIsTerminal is the testable core of IsTerminal.
func fileIsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// ReadSingleKey blocks reading one byte from /dev/tty in raw mode and returns
// it lowercased when it is an ASCII letter. Raw mode means the byte arrives
// as pressed — no Enter needed, and no newline is left queued for the next
// call. The terminal mode is restored before returning. Any error (no TTY,
// EOF, ...) is returned with the zero rune.
func ReadSingleKey() (rune, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	old, err := term.MakeRaw(int(f.Fd()))
	if err != nil {
		return 0, err
	}
	defer term.Restore(int(f.Fd()), old)

	buf := make([]byte, 1)
	if _, err := f.Read(buf); err != nil {
		return 0, err
	}
	r := rune(buf[0])
	if unicode.Is(unicode.L, r) {
		return unicode.ToLower(r), nil
	}
	return r, nil
}
