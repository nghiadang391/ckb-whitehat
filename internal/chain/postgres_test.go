package chain

import (
	"context"
	"io"
	"testing"
)

func TestEnsureScratchPostgresHonorsEnvOverride(t *testing.T) {
	const custom = "postgres://custom:pw@127.0.0.1:5439/testdb"
	t.Setenv("CKB_WHITEHAT_POSTGRES_URL", custom)
	got, managed, err := EnsureScratchPostgres(context.Background(), io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != custom || managed {
		t.Fatalf("got=(%q, managed=%v), want=(%q, false)", got, managed, custom)
	}
}

func TestScratchPGURLFormat(t *testing.T) {
	want := "postgres://whitehat:whitehat@127.0.0.1:5439/whitehat"
	if got := ScratchPGURL(); got != want {
		t.Fatalf("ScratchPGURL() = %q, want %q", got, want)
	}
}
