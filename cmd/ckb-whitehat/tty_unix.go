//go:build !windows

package main

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"unicode"

	"golang.org/x/term"
)

const (
	defaultTermCols = 80
	defaultTermRows = 24
)

// IsTerminal reports whether os.Stdout is an actual terminal (TTY). Any error
// or non-TTY descriptor keeps the live panel disabled. We use term.IsTerminal
// (which performs a TCGETS ioctl) rather than os.ModeCharDevice because
// /dev/null is also a character device and `ckb-whitehat run >/dev/null` must
// NOT enable the live TTY box.
func IsTerminal() bool {
	return fileIsTerminal(os.Stdout)
}

// fileIsTerminal is the testable core of IsTerminal.
func fileIsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// TerminalSize returns the current terminal width and height in character
// cells. When stdout is not a TTY or its size cannot be queried, it falls back
// to a standard 80x24 viewport.
func TerminalSize() (cols, rows int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return defaultTermCols, defaultTermRows
	}
	return w, h
}

// WatchResize listens for SIGWINCH terminal resize signals and invokes
// onResize with the new terminal dimensions. The returned stop function
// unregisters the signal handler, terminates the listener goroutine cleanly,
// and is safe to call multiple times.
func WatchResize(onResize func(cols, rows int)) (stop func()) {
	sig := make(chan os.Signal, 1)
	done := make(chan struct{})
	exited := make(chan struct{})
	signal.Notify(sig, syscall.SIGWINCH)
	go func() {
		defer close(exited)
		for {
			select {
			case <-done:
				return
			case <-sig:
				if onResize != nil {
					cols, rows := TerminalSize()
					onResize(cols, rows)
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(sig)
			close(done)
			<-exited
		})
	}
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
