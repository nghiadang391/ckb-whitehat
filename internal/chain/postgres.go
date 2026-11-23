package chain

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	// Pinned lightweight official Postgres image for isolated scratch DB PoCs.
	DefaultScratchPGImage = "postgres:16-alpine"

	// Dedicated localhost port so scratch Postgres never touches host port 5432.
	DefaultScratchPGPort = 5439

	scratchPGContainerName = "ckb-whitehat-pg"
	scratchPGUser          = "whitehat"
	scratchPGPass          = "whitehat"
	scratchPGDB            = "whitehat"
)

// ScratchPGURL returns the connection string for the managed Docker scratch Postgres.
func ScratchPGURL() string {
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s",
		scratchPGUser, scratchPGPass, DefaultScratchPGPort, scratchPGDB)
}

func scratchPGImage() string {
	if img := strings.TrimSpace(os.Getenv("CKB_WHITEHAT_POSTGRES_IMAGE")); img != "" {
		return img
	}
	return DefaultScratchPGImage
}

// ScratchPostgresReady reports whether the managed Docker scratch Postgres
// container is currently running and accepting connections.
func ScratchPostgresReady(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return runDocker(cctx, "exec", scratchPGContainerName,
		"pg_isready", "-U", scratchPGUser, "-d", scratchPGDB) == nil
}

// EnsureScratchPostgres returns a ready Postgres connection URL for PoCs:
//  1. If CKB_WHITEHAT_POSTGRES_URL is set, returns it directly.
//  2. Otherwise, ensures the isolated Docker `ckb-whitehat-pg` container
//     (127.0.0.1:5439, whitehat:whitehat) is running and ready.
//
// This prevents runs from advertising an un-credentialed host Postgres on
// port 5432 or leaking host DB credentials into agent runs.
func EnsureScratchPostgres(ctx context.Context, log io.Writer) (string, bool, error) {
	if log == nil {
		log = io.Discard
	}
	if override := strings.TrimSpace(os.Getenv("CKB_WHITEHAT_POSTGRES_URL")); override != "" {
		return override, false, nil
	}
	if err := DockerAvailable(ctx); err != nil {
		return "", false, err
	}
	if ScratchPostgresReady(ctx) {
		return ScratchPGURL(), true, nil
	}
	img := scratchPGImage()
	fmt.Fprintf(log, "starting isolated Docker scratch Postgres (%s on 127.0.0.1:%d)...\n", img, DefaultScratchPGPort)
	_ = runDocker(ctx, "rm", "-f", scratchPGContainerName)
	if err := runDocker(ctx, "run", "-d",
		"--name", scratchPGContainerName,
		"-e", "POSTGRES_USER="+scratchPGUser,
		"-e", "POSTGRES_PASSWORD="+scratchPGPass,
		"-e", "POSTGRES_DB="+scratchPGDB,
		"-p", fmt.Sprintf("127.0.0.1:%d:5432", DefaultScratchPGPort),
		img,
	); err != nil {
		return "", false, fmt.Errorf("start scratch Postgres container: %w", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		if ScratchPostgresReady(ctx) {
			fmt.Fprintf(log, "scratch Postgres ready -> %s\n", ScratchPGURL())
			return ScratchPGURL(), true, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", false, fmt.Errorf("timed out waiting for scratch Postgres container on 127.0.0.1:%d", DefaultScratchPGPort)
}

// StopScratchPostgres stops and removes the managed Docker scratch Postgres container.
func StopScratchPostgres(ctx context.Context) error {
	return runDocker(ctx, "rm", "-f", scratchPGContainerName)
}
