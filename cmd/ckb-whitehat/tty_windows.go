//go:build windows

package main

import "errors"

// IsTerminal is a stub on Windows: the live panel stays disabled and
// `ckb-whitehat run` uses its plain-text output path.
func IsTerminal() bool { return false }

// TerminalSize returns the default 80x24 fallback size on Windows.
func TerminalSize() (cols, rows int) { return 80, 24 }

// WatchResize is a no-op on Windows where SIGWINCH is unavailable.
func WatchResize(onResize func(cols, rows int)) (stop func()) {
	return func() {}
}

// ReadSingleKey is unsupported on Windows (raw-mode console input is a
// separate API); the orchestrator treats the error as "no interactive gate
// answer available" and falls back to its non-interactive behavior.
func ReadSingleKey() (rune, error) {
	return 0, errors.New("interactive key input not supported on windows")
}
