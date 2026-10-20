package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/testkit/fakelock"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// CA-COFRE-22
// What needs the CA, which the Citadel opens when it first acts, is answered
// to a Tower with a retryable error while the Citadel has not tried (it will
// act, and the Tower sends the message again). Once the vault is closed it is
// answered with why, and not retryable: no wait will change it until the Owner
// fixes the key, so the message must not stay in the outbox of the Tower, which
// would send it again at every connection (a renewal makes a new one each minute).
func TestAHandlerThatNeedsTheCASaysWhyTheCitadelCannotAct(t *testing.T) {
	t.Parallel()
	e := &Engine{joins: &joinMux{h: map[string]http.Handler{}}}
	run := &citadelRun{}
	handle := run.needsCitadel(func(*guild.Citadel) protocol.HandlerFunc {
		t.Error("the handler ran with no Citadel")
		return nil
	})
	ask := func() *protocol.Error {
		t.Helper()
		_, err := handle(t.Context(), &protocol.Message{Peer: protocol.Peer{ID: "tower"}})
		var pe *protocol.Error
		if !errors.As(err, &pe) {
			t.Fatalf("answer = %v; want a protocol error", err)
		}
		return pe
	}

	pe := ask()
	if pe.Code != protocol.CodeUnavailable || !pe.Retryable || !strings.Contains(pe.Message, "does not act yet") || !run.refused["tower"] {
		t.Errorf("before it tries: %+v, and the Tower was remembered: %v", pe, run.refused["tower"])
	}

	e.vaultClosed(run)
	run.refused = nil
	pe = ask()
	if pe.Code != protocol.CodeForbidden || pe.Retryable || !strings.Contains(pe.Message, "cannot act") || !strings.Contains(pe.Message, "vault is closed") {
		t.Errorf("with the vault closed: %+v; want the reason, not retryable", pe)
	}
	if len(run.refused) != 0 {
		t.Errorf("the Tower was remembered for a resend that never comes: %v", run.refused)
	}
}

// CA-COFRE-22
// The routes of the entry say why they answer 503 while the vault is closed, and
// say it only for that period: the next one, a Tower or a Citadel whose vault
// opens, answers as before.
func TestTheRoutesOfTheEntryForgetWhyTheyWereDownWhenThePeriodEnds(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	j := &joinMux{m: mux, h: map[string]http.Handler{}}
	j.Handle("POST /join", nil)
	say := func() string {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/join", nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status %d, want 503", w.Code)
		}
		return w.Body.String()
	}
	if got := say(); !strings.Contains(got, "not the Citadel now") {
		t.Errorf("before the vault closed: %s", got)
	}
	(&Engine{joins: j}).vaultClosed(&citadelRun{})
	if got := say(); !strings.Contains(got, "vault is closed") || !strings.Contains(got, `"error":"unavailable"`) {
		t.Errorf("with the vault closed: %s", got)
	}
	j.clear() // the period ends
	if got := say(); !strings.Contains(got, "not the Citadel now") || strings.Contains(got, "vault") {
		t.Errorf("in the next period: %s", got)
	}
}

// CA-COFRE-23
// A Citadel whose vault is closed sends no copy of its key to the Towers: the
// key that did not open the vault may be the wrong one, with the versions of the
// envelope, and a Tower takes any ring of those versions and puts it in place of
// its own, so the Guild would lose the good copies it has left.
func TestACitadelWithAClosedVaultSendsNoCopyOfItsKeyToTheTowers(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	clk := clock.Real{}
	cfg := Config{Instance: "a", Name: "a", Hostname: "a", EnginePort: 7443, NoDiscovery: true, Synchronous: "OFF"}
	paths := DirPaths(t.TempDir())
	seedGuild(t, paths)
	_, rk, err := Found(ctx, cfg, paths, clk)
	if err != nil {
		t.Fatal(err)
	}
	rk.Destroy()
	e, err := New(cfg, paths, Deps{Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	db, err := guilddb.Open(ctx, paths.guildDB(), store.Options{Clock: clk, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	open := func() *vault.Vault {
		t.Helper()
		v, err := e.openVault(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(v.Close)
		return v
	}

	if cp := e.primaryVault(open()); cp == nil {
		t.Fatal("an open vault is not sent to the Towers")
	}
	wrongKey(t, paths)
	if v := open(); v.Err() == nil {
		t.Fatal("the key of another Guild opens the vault")
	} else if cp := e.primaryVault(v); cp != nil {
		t.Errorf("a closed vault is sent to the Towers: %+v", cp)
	}
}

// wrongKey changes a byte of the key material in the key file of paths: it still
// loads, it has the same version as the envelope, and it opens nothing.
func wrongKey(t *testing.T, paths Paths) {
	t.Helper()
	keys := vault.NewFileKeyStore(paths.vaultKey())
	data, err := keys.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xff // the last byte of the key; the header and the version stay
	if err := keys.Save(t.Context(), data); err != nil {
		t.Fatal(err)
	}
}

// CA-COFRE-22
// A Tower whose copy of the vault does not serve is refused the promotion before
// it asks for the epoch in the lock: after, there would be no CA key to sign
// with, the epoch would stay without a Citadel, and the old Citadel of a planned
// handover, which sees the new epoch, would have stepped down. The lock is not
// asked at all. The key can be the wrong one, or the right one in front of a
// replica that has no CA key yet (the full copy after an exclusion was undone is
// still on its way).
func TestAPromotionWhoseVaultDoesNotServeIsRefusedBeforeTheEpochIsClaimed(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		spoil func(t *testing.T, paths Paths)
		want  error
	}{
		"the key is wrong":              {spoil: wrongKey, want: vault.ErrClosed},
		"the replica has no CA key yet": {spoil: noSecrets, want: vault.ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			clk := clock.Real{}
			cfg := Config{Instance: "a", Name: "a", Hostname: "a", EnginePort: 7443, NoDiscovery: true, Synchronous: "OFF"}
			paths := DirPaths(t.TempDir())
			seedGuild(t, paths)
			founded, rk, err := Found(ctx, cfg, paths, clk) // the folders of a Tower hold the same files
			if err != nil {
				t.Fatal(err)
			}
			rk.Destroy()
			lock := fakelock.New(1, founded.MachineID)
			e, err := New(cfg, paths, Deps{Clock: clk, Lock: lock.For("tower")})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.vaultOpens(ctx); err != nil {
				t.Fatalf("a copy that serves: %v", err)
			}

			tc.spoil(t, paths)
			p := &promotion{done: make(chan error, 1)}
			if next := e.promote(ctx, p); next.to != RoleTower {
				t.Fatalf("the promotion went on: %+v", next)
			}
			if err := <-p.done; !errors.Is(err, tc.want) {
				t.Errorf("the promotion failed with %v; want %v", err, tc.want)
			}
			if n := lock.Claims("tower"); n != 0 {
				t.Errorf("the epoch was claimed %d times; want none", n)
			}
			if n := lock.Highest(); n != 1 {
				t.Errorf("the lock is at the epoch %d; want it untouched at 1", n)
			}
		})
	}
}

// noSecrets empties the secrets of the Guild database of paths: what the replica of
// a machine whose exclusion was undone holds until the full copy arrives.
func noSecrets(t *testing.T, paths Paths) {
	t.Helper()
	db, err := guilddb.Open(t.Context(), paths.guildDB(), store.Options{Clock: clock.Real{}, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(t.Context(), `DELETE FROM secrets`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// CA-COFRE-22
// A Tower is promotable only with a vault that serves: the key loads and opens
// the rows of the replica, and the CA key is there. The reason shows (a key that is
// refused or wrong is not "no copy of the vault"), and the Owner is not asked about
// a machine that would fail after it won the epoch.
func TestATowerIsPromotableOnlyWithAVaultThatServes(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	clk := clock.Real{}
	cfg := Config{Instance: "a", Name: "a", Hostname: "a", EnginePort: 7443, NoDiscovery: true, Synchronous: "OFF"}
	paths := DirPaths(t.TempDir())
	seedGuild(t, paths)
	founded, rk, err := Found(ctx, cfg, paths, clk) // a replica holds the same rows
	if err != nil {
		t.Fatal(err)
	}
	rk.Destroy()
	e, err := New(cfg, paths, Deps{Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	e.id.MachineID = founded.MachineID
	db, err := guilddb.Open(ctx, paths.guildDB(), store.Options{Clock: clk, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if err := e.promotable(ctx, db); err != nil {
		t.Fatalf("a vault that serves: %v", err)
	}
	// the replica of a machine whose exclusion was undone has no rows until the full copy arrives
	if _, err := db.Writer().ExecContext(ctx, `DELETE FROM secrets`); err != nil {
		t.Fatal(err)
	}
	if err := e.promotable(ctx, db); !errors.Is(err, guild.ErrNotPromotable) || !errors.Is(err, vault.ErrNotFound) {
		t.Errorf("with no CA key in the replica: %v; want not promotable, the secret not found", err)
	}
	wrongKey(t, paths)
	if err := e.promotable(ctx, db); !errors.Is(err, guild.ErrNotPromotable) {
		t.Errorf("with a wrong key: %v; want not promotable", err)
	}
	if runtime.GOOS != "windows" { // a mode is not an ACL
		if err := os.Chmod(paths.vaultKey(), 0o644); err != nil {
			t.Fatal(err)
		}
		var unsafe *vault.UnsafeKeyFileError
		if err := e.promotable(ctx, db); !errors.Is(err, guild.ErrNotPromotable) || !errors.As(err, &unsafe) || unsafe.Check != "LIN-05" {
			t.Errorf("with a key open to others: %v; want not promotable, saying LIN-05", err)
		}
	}
}
