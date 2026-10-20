package app

import (
	"database/sql"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/pkg/clock"
)

// connectedAna is a world with Ana connected.
func connectedAna(t *testing.T) (*szWorld, users.User) {
	t.Helper()
	w := newSZWorld(t)
	u := w.member("Ana", "ana-gh", users.Administrator, w.ws.Slug)
	ch, err := w.run.Personal().Begin(w.ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	w.gh.AuthorizeDevice(ch.UserCode, u.GitHubLogin)
	w.clk.Advance(ch.Interval)
	if res, err := w.run.Personal().Poll(w.ctx, u.ID); err != nil || res != github.PollConnected {
		t.Fatalf("connect = %q, %v", res, err)
	}
	return w, u
}

// A user who acts in no workspace has no credential in use, even before the
// removal ran (it runs after the commit and can fail): Client refuses him, and the
// periodic sweep takes the credential away.
func TestAUserWhoActsInNoWorkspaceHasNoPersonalClientAndTheSweepRemovesHisCredential(t *testing.T) {
	t.Parallel()
	w, u := connectedAna(t)
	if _, err := w.run.Personal().Client(w.ctx, u.ID); err != nil {
		t.Fatalf("connected user: %v", err)
	}
	// the change committed and the removal after it did not run
	if err := w.db.WriteDurable(w.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(w.ctx, `DELETE FROM user_workspaces WHERE user_id = ?`, u.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.run.Personal().Client(w.ctx, u.ID); !errors.Is(err, github.ErrNoCredential) {
		t.Fatalf("Client of a user in no workspace = %v; want ErrNoCredential", err)
	}
	if w.status(u) != github.CredentialConnected {
		t.Fatal("the credential was removed before the sweep")
	}
	w.run.sweepPersonal(w.ctx)
	if got := w.status(u); got != "" || len(w.gh.RevokedTokens()) == 0 {
		t.Fatalf("after the sweep: credential %q, revoked %v; want it erased and revoked", got, w.gh.RevokedTokens())
	}
}

// A second /github replaces the wait of the first; the first wait ending must not
// take the second out of the map, or nothing could end it any more (the removal of
// the user cancels it).
func TestTheWaitThatReplacedAnotherIsStillTheOneThatTheRemovalOfTheUserCancels(t *testing.T) {
	t.Parallel()
	w := newSZWorld(t)
	w.withBot()
	ana := w.member("Ana", "ana-gh", users.Administrator, w.ws.Slug)
	w.connecting(ana)
	if _, err := w.github(ana); err != nil { // a new code replaces the first wait
		t.Fatal(err)
	}
	for range 1000 { // let the cancelled first wait run its cleanup
		runtime.Gosched()
	}
	// the right is taken away on the database, then the removal that follows the change runs
	if err := w.db.WriteDurable(w.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(w.ctx, `DELETE FROM user_workspaces WHERE user_id = ?`, ana.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.run.dropPersonal(w.ctx, ana.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { w.run.gh.Wait(); close(done) }()
	tm := clock.Real{}.NewTimer(10 * time.Second)
	defer tm.Stop()
	select {
	case <-done:
	case <-tm.C():
		t.Fatal("the wait of a user who lost the right was not cancelled")
	}
}

// The expiry of the code is shown in the time zone of the Guild, never in the one
// of the server.
func TestTheExpiryOfTheGitHubCodeIsShownInTheTimeZoneOfTheGuild(t *testing.T) {
	t.Parallel()
	w := newSZWorld(t)
	w.withBot()
	if err := w.db.WriteDurable(w.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(w.ctx, `INSERT OR REPLACE INTO guild (id, guild_id, ca_cert, epoch, epoch_certificate, timezone, recovery_public_key, created_at)
			VALUES (1, '01M4ZPXYG0PNPDZ5JN7697ZSCH', x'00', 1, '{}', 'America/Sao_Paulo', x'07', 0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ana := w.member("Ana", "ana-gh", users.Administrator, w.ws.Slug)
	said := w.connecting(ana)
	if !strings.Contains(said, "America/Sao_Paulo, UTC-03:00") {
		t.Fatalf("answer = %q; want the expiry in the zone of the Guild", said)
	}
}
