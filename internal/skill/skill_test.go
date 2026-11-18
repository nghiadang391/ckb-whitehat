package skill

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDrift guards against the embedded copy diverging from the canonical
// repo skill. The check only runs when the file being tested lives inside a
// full repository checkout (the module tree plus a sibling .claude/ dir);
// in a standalone module build it is skipped, since the embedded copy is the
// sole source of truth there.
func TestDrift(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("getwd: %v", err)
	}
	candidates := []string{
		filepath.Join(wd, "..", "..", "..", ".claude", "skills", "ckb-whitehat", "SKILL.md"),
		filepath.Join(wd, "..", "..", ".claude", "skills", "ckb-whitehat", "SKILL.md"),
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if string(b) != text {
			t.Fatalf("embedded skill.md drifted from %s — re-copy it after editing the canonical skill", p)
		}
		t.Logf("drift check passed against %s", p)
		return
	}
	t.Skip("no canonical .claude/skills/ckb-whitehat/SKILL.md visible from this module layout")
}
