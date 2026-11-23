package packs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeWritesAllCatalogPacks(t *testing.T) {
	ws := t.TempDir()
	written, err := Materialize(ws)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(written) != len(Catalog) {
		t.Fatalf("wrote %d files, catalog has %d", len(written), len(Catalog))
	}
	for _, item := range Catalog {
		p := filepath.Join(ws, "packs", item.File)
		st, err := os.Stat(p)
		if err != nil || st.Size() == 0 {
			t.Fatalf("pack %s missing or empty: %v", p, err)
		}
	}
}
