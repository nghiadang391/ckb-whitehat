//go:build !windows

package main

import (
	"os"
	"syscall"
	"unicode"
)

// IsTerminal reports whether os.Stdout is attached to a TTY. Any error (or a
// non-character device) means the live panel stays disabled.
func IsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&syscall.S_IFCHR != 0
}

// ReadSingleKey blocks reading one byte from /dev/tty and returns it
// lowercased when it is an ASCII letter. Any error (no TTY, EOF, ...) is
// returned with the zero rune.
func ReadSingleKey() (rune, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	buf := make([]byte, 1)
	_, err = f.Read(buf)
	if err != nil {
		return 0, err
	}
	r := rune(buf[0])
	if unicode.Is(unicode.L, r) {
		return unicode.ToLower(r), nil
	}
	return r, nil
}
