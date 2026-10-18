package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
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

// CA-COFRE-22
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
	wrongKey(t, paths, cfg)
	if v := open(); v.Err() == nil {
		t.Fatal("the key of another Guild opens the vault")
	} else if cp := e.primaryVault(v); cp != nil {
		t.Errorf("a closed vault is sent to the Towers: %+v", cp)
	}
}

// wrongKey puts in place of the key of the vault of paths the key of another
// Guild: it loads, it has the same version, and it opens nothing.
func wrongKey(t *testing.T, paths Paths, cfg Config) {
	t.Helper()
	otherPaths := DirPaths(t.TempDir())
	_, rk, err := Found(t.Context(), cfg, otherPaths, clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	rk.Destroy()
	data, err := vault.NewFileKeyStore(otherPaths.vaultKey()).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.NewFileKeyStore(paths.vaultKey()).Save(t.Context(), data); err != nil {
		t.Fatal(err)
	}
}

// CA-COFRE-22
// A Tower whose copy of the vault does not open is refused the promotion before
// it asks for the epoch in the lock: after, there would be no certificate to
// sign with, the epoch would stay without a Citadel, and the old Citadel of a
// planned handover, which sees the new epoch, would have stepped down. The lock
// is not asked at all.
func TestAPromotionWhoseVaultDoesNotOpenIsRefusedBeforeTheEpochIsClaimed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	clk := clock.Real{}
	cfg := Config{Instance: "a", Name: "a", Hostname: "a", EnginePort: 7443, NoDiscovery: true, Synchronous: "OFF"}
	paths := DirPaths(t.TempDir())
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
		t.Fatalf("a copy that opens the vault: %v", err)
	}

	wrongKey(t, paths, cfg)
	p := &promotion{done: make(chan error, 1)}
	if next := e.promote(ctx, p); next.to != RoleTower {
		t.Fatalf("the promotion went on: %+v", next)
	}
	if err := <-p.done; !errors.Is(err, vault.ErrClosed) {
		t.Errorf("the promotion failed with %v; want the closed vault", err)
	}
	if n := lock.Claims("tower"); n != 0 {
		t.Errorf("the epoch was claimed %d times; want none", n)
	}
	if n := lock.Highest(); n != 1 {
		t.Errorf("the lock is at the epoch %d; want it untouched at 1", n)
	}
}
