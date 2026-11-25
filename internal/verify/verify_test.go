package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLastMarker(t *testing.T) {
	m := LastMarker("old CKB-WHITEHAT-RESULT: findings=1 fired=1 report=old\nCKB-WHITEHAT-RESULT: findings=0 fired=0 report=new")
	if m == nil || m.Findings != 0 || m.Report != "new" {
		t.Fatalf("%+v", m)
	}
}
func TestVerifyCleanSkipsPoCChecks(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "FINDINGS.md"), []byte("x"), 0600)
	c := VerifyRun(d, "CKB-WHITEHAT-RESULT: findings=0 fired=0 report=x", LastMarker("CKB-WHITEHAT-RESULT: findings=0 fired=0 report=x"))
	if len(c) != 2 {
		t.Fatalf("checks=%d", len(c))
	}
}
func TestVerifyFindingsNeedsVerdicts(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "FINDINGS.md"), []byte("long report"), 0600)
	os.Mkdir(filepath.Join(d, "poc"), 0700)
	m := LastMarker("CKB-WHITEHAT-RESULT: findings=1 fired=1 report=x")
	c := VerifyRun(d, "CKB-WHITEHAT-RESULT: findings=1 fired=1 report=x", m)
	if len(c) != 5 || c[2].OK || c[3].OK || c[4].OK {
		t.Fatalf("%+v", c)
	}
}
func TestVerifyFiredWithoutFindingsNeedsVerdicts(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "FINDINGS.md"), []byte("long report"), 0600)
	os.Mkdir(filepath.Join(d, "poc"), 0700)
	stdout := "CKB-WHITEHAT-RESULT: findings=0 fired=1 report=x"
	m := LastMarker(stdout)
	c := VerifyRun(d, stdout, m)
	if len(c) != 5 || c[2].OK || c[3].OK || c[4].OK {
		t.Fatalf("expected 5 completeness checks with PoC/verdicts failing, got %+v", c)
	}
}

func TestVerifyStructuralOnlyDoesNotRequirePoCFiles(t *testing.T) {
	d := t.TempDir()
	report := strings.Repeat("static finding details ", 15) + "\n- **FINDING F1 STRUCTURAL**\n"
	os.WriteFile(filepath.Join(d, "FINDINGS.md"), []byte(report), 0600)
	os.Mkdir(filepath.Join(d, "poc"), 0700)
	stdout := "  FINDING F1 STRUCTURAL\nCKB-WHITEHAT-RESULT: findings=1 fired=0 report=x"
	m := LastMarker(stdout)
	c := VerifyRun(d, stdout, m)
	if len(c) != 4 {
		t.Fatalf("expected 4 checks (no PoC file check when fired=0), got %+v", c)
	}
	for _, chk := range c {
		if !chk.OK {
			t.Fatalf("expected all checks OK for STRUCTURAL-only run with verdicts, failed: %+v", chk)
		}
	}
}

func TestVerifyExcludesChainEvidenceJSONFromPoCCount(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "FINDINGS.md"), []byte(strings.Repeat("x", 250)+"\nFINDING F1 FIRED\n"), 0600)
	os.Mkdir(filepath.Join(d, "poc"), 0700)
	os.WriteFile(filepath.Join(d, "poc", "chain-evidence.json"), []byte("[]"), 0600)
	stdout := "FINDING F1 FIRED\nCKB-WHITEHAT-RESULT: findings=1 fired=1 report=x"
	c := VerifyRun(d, stdout, LastMarker(stdout))
	if c[2].OK {
		t.Fatalf("chain-evidence.json alone must not satisfy PoC file check: %+v", c[2])
	}
}
