package app_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/sheetfake"
	"github.com/lucasndas/mestre/pkg/clock"
)

// An engine whose period fails returns the error: the goroutines it started
// end with its context, which Run cancels before it waits for them, and not
// after (it would wait for them forever).
func TestRunReturnsTheErrorOfAPeriodInsteadOfWaitingForItsOwnContext(t *testing.T) {
	t.Parallel()
	clk := clock.Real{}
	dir := t.TempDir()
	cfg := app.Config{Instance: "a", Name: "a", Hostname: "a", Bind: "a", EnginePort: 7443, NoDiscovery: true, Synchronous: "OFF"}
	paths := app.DirPaths(dir)
	paths.CLISocket = ""
	seedGuild(t, paths)
	_, rk, err := app.Found(t.Context(), cfg, paths, clk)
	if err != nil {
		t.Fatal(err)
	}
	rk.Destroy()
	// A Guild whose recovery key is not one: the vault is misconfigured, which
	// is not a vault that fails to open, and the Citadel cannot start.
	raw, err := sql.Open("sqlite", filepath.Join(dir, "banco", "mestre.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.ExecContext(t.Context(), `UPDATE guild SET recovery_public_key = x'00' WHERE id = 1`)
	if cerr := raw.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}

	e, err := app.New(cfg, paths, app.Deps{Clock: clk, Network: fakenet.New(clk).Host("a"), Sheet: sheetfake.Linux("a").Sources()})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { done <- e.Run(ctx) }()
	deadline := clk.NewTimer(20 * time.Second)
	defer deadline.Stop()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "RecoveryPublicKey") {
			t.Fatalf("Run = %v, want the error of the vault configuration", err)
		}
	case <-deadline.C():
		t.Fatal("Run did not return the error of its period")
	}
}
