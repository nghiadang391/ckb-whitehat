// Package skill embeds the ckb-whitehat methodology so the binary is
// self-contained: a bare download needs no separately installed skill.
//
// Sync rule: skill.md is a copy of .claude/skills/ckb-whitehat/SKILL.md.
// The release workflow and TestDrift keep the two in lockstep; the canonical
// editable source of truth is the repo copy under .claude/.
package skill

import _ "embed"

//go:embed skill.md
var text string

// SKILL returns the embedded methodology text.
func SKILL() string { return text }
