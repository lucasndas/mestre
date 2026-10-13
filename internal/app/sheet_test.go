package app_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/testkit/fakelock"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/sheetfake"
	"github.com/lucasndas/mestre/pkg/clock"
)

// CA-FMAQ-01: the Citadel's own sheet is checked every five minutes like the
// others, not written once.
func TestTheCitadelKeepsItsOwnSheetFreshEveryFiveMinutes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		clk := clock.Real{} // the bubble's
		dir := t.TempDir()
		cfg := app.Config{Instance: "a", Name: "a", Hostname: "a", Bind: "a", EnginePort: 7443, NoDiscovery: true, Synchronous: "OFF"}
		paths := app.DirPaths(dir)
		paths.CLISocket = ""
		founded, rk, err := app.Found(ctx, cfg, paths, clk)
		if err != nil {
			t.Fatal(err)
		}
		rk.Destroy()

		machine := sheetfake.Linux("a")
		lock := fakelock.New(1, founded.MachineID)
		e, err := app.New(cfg, paths, app.Deps{
			Clock: clk, Network: fakenet.New(clk).Host("a"), Lock: lock.For(founded.MachineID), Sheet: machine.Sources(),
		})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- e.Run(ctx) }()
		defer func() { cancel(); <-done }()

		// What the Guild database says about the transcription of this machine.
		transcribes := func() (on, known bool) {
			db, err := sql.Open("sqlite", filepath.Join(paths.Banco, "mestre.db")+"?_pragma=query_only(1)")
			if err != nil {
				return false, false
			}
			defer db.Close()
			var raw sql.NullString
			if err := db.QueryRowContext(ctx, `SELECT sheet FROM machines WHERE machine_id = ?`, founded.MachineID).Scan(&raw); err != nil || !raw.Valid || raw.String == "" {
				return false, false
			}
			var sheet struct {
				Transcricao bool `json:"transcricao"`
			}
			if json.Unmarshal([]byte(raw.String), &sheet) != nil {
				return false, false
			}
			return sheet.Transcricao, true
		}
		// A pause for the file system makes SQLite sleep, which in the bubble is a
		// sleep of the clock: between tries the clock moves a little.
		eventually := func(what string, ok func() bool) {
			t.Helper()
			for range 600 {
				synctest.Wait()
				if ok() {
					return
				}
				_ = clock.Sleep(ctx, clk, 2*time.Millisecond)
			}
			t.Fatalf("timed out waiting for %s: %+v", what, e.Status())
		}
		eventually("the Citadel to act", func() bool { s := e.Status(); return s.Role == app.RoleCitadel && s.Acting })
		eventually("the first sheet", func() bool { _, known := transcribes(); return known })
		if on, _ := transcribes(); on {
			t.Fatal("the fake machine does not transcribe yet")
		}

		machine.SetTranscription(true)
		_ = clock.Sleep(ctx, clk, 5*time.Minute+time.Second)
		eventually("the sheet to follow the machine", func() bool { on, _ := transcribes(); return on })
	})
}
