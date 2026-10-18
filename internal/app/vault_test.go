package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/sheetfake"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// CA-COFRE-02
// CA-COFRE-22
// A vault that does not open does not take the Citadel down
// (docs/plataforma/cofre.md#regras-e-erros): the engine raises the grave alert
// and goes on as the Citadel, and what needs the vault (the CA, so the entry of
// machines) answers why it does not work instead of waiting for it.
func TestACitadelWhoseVaultDoesNotOpenGoesOnAndSaysWhatStopped(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		spoil func(t *testing.T, key string)
		check string // the item of the checklist the alert names, where the cause has one
		unix  bool   // a mode is not an ACL: the Windows file has its own tests in internal/vault
	}{
		"the key is missing": {spoil: func(t *testing.T, key string) {
			t.Helper()
			if err := os.Remove(key); err != nil {
				t.Fatal(err)
			}
		}},
		"the key file is open to others": {check: "LIN-05", unix: true, spoil: func(t *testing.T, key string) {
			t.Helper()
			if err := os.Chmod(key, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if tc.unix && runtime.GOOS == "windows" {
				t.Skip("a mode is not an ACL")
			}
			clk := clock.Real{}
			dir := t.TempDir()
			cfg := app.Config{Instance: "a", Name: "a", Hostname: "a", Bind: "a", EnginePort: 7443, NoDiscovery: true, Synchronous: "OFF"}
			paths := app.DirPaths(dir)
			paths.CLISocket = ""
			ctx, cancel := context.WithCancel(t.Context())
			_, rk, err := app.Found(ctx, cfg, paths, clk)
			if err != nil {
				t.Fatal(err)
			}
			rk.Destroy()
			tc.spoil(t, filepath.Join(dir, "config", "cofre.key"))

			network := fakenet.New(clk)
			alerts := make(chan vault.Alert, 4)
			e, err := app.New(cfg, paths, app.Deps{
				Clock: clk, Network: network.Host("a"), Sheet: sheetfake.Linux("a").Sources(),
				OnVaultAlert: func(a vault.Alert) {
					select {
					case alerts <- a:
					default:
					}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			stopped := make(chan struct{})
			var runErr error // read only once stopped is closed
			go func() { runErr = e.Run(ctx); close(stopped) }()
			t.Cleanup(func() { cancel(); <-stopped })
			deadline := clk.NewTimer(20 * time.Second)
			defer deadline.Stop()

			// the grave alert comes, and the engine does not stop for it
			select {
			case a := <-alerts:
				if a.Severity != vault.SeverityGrave || !errors.Is(a.Err, vault.ErrClosed) || a.Check != tc.check {
					t.Errorf("alert = %+v; want a grave one that says the vault is closed, check %q", a, tc.check)
				}
			case <-stopped:
				t.Fatalf("the engine stopped instead of raising the alert: %v", runErr)
			case <-deadline.C():
				t.Fatal("no alert for a vault that does not open")
			}

			// A machine asks to enter: that needs the CA, which is in the vault. The
			// Citadel says why it cannot, from the start of its period.
			var last error
			tick := clk.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for !strings.Contains(fmtErr(last), "vault is closed") {
				select {
				case <-stopped:
					t.Fatalf("the engine stopped: %v", runErr)
				case <-deadline.C():
					t.Fatalf("the entry of a machine never said the vault is closed; last answer: %v", last)
				case <-tick.C():
				}
				ectx, stop := clock.WithTimeout(ctx, clk, 5*time.Second)
				_, last = guild.Join(ectx, guild.JoinConfig{
					Addr: "a:7443", Network: network.Host("b"), Clock: clk, Name: "b", Hostname: "b", OS: "linux", Instance: "b", EnginePort: 7443,
				})
				stop()
			}
			if !errors.Is(last, guild.ErrCitadelUnavailable) {
				t.Errorf("the entry answered %v; want it to be the retryable \"does not take entries now\"", last)
			}
			// The entry says it from the start of the period, a little before the
			// status shows the role of the period.
			for e.Status().Role != app.RoleCitadel {
				select {
				case <-stopped:
					t.Fatalf("the engine stopped: %v", runErr)
				case <-deadline.C():
					t.Fatalf("the engine is not the Citadel: %+v", e.Status())
				case <-tick.C():
				}
			}
			select {
			case <-stopped:
				t.Fatalf("the engine stopped: %v", runErr)
			default:
			}
		})
	}
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
