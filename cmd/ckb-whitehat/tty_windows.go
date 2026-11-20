//go:build windows

package main

import "errors"

// IsTerminal is a stub on Windows: the live panel stays disabled and
// `ckb-whitehat run` uses its plain-text output path.
func IsTerminal() bool { return false }

// ReadSingleKey is unsupported on Windows (raw-mode console input is a
// separate API); the orchestrator treats the error as "no interactive gate
// answer available" and falls back to its non-interactive behavior.
func ReadSingleKey() (rune, error) {
	return 0, errors.New("interactive key input not supported on windows")
}
