package verify

import (
	"os"
	"path/filepath"
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
