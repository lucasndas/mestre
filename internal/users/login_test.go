package users_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/users"
)

// access makes a user with a login, a password and a PIN, by the first access.
func (w *world) access(name, login, password string) users.User {
	w.t.Helper()
	u := w.add(name, users.Approver)
	c, err := w.svc.NewAccessCode(w.ctx, w.owner, u.ID, users.AccessFirst)
	if err != nil {
		w.t.Fatal(err)
	}
	got, err := w.svc.RedeemAccessCode(w.ctx, c.Code, users.SetupInput{Login: login, Password: password, PIN: "123456"})
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

func TestPasswordsAreArgon2idWithASaltPerUser(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.access("Ana", "ana", "mesma senha longa")
	w.access("Bia", "bia", "mesma senha longa")
	rows, err := w.db.Reader().QueryContext(w.ctx, `SELECT password_hash FROM users WHERE password_hash <> ''`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var hashes []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	if len(hashes) != 2 || hashes[0] == hashes[1] {
		t.Fatalf("hashes = %v", hashes)
	}
	for _, h := range hashes {
		if !strings.HasPrefix(h, "$argon2id$") || strings.Contains(h, "mesma senha") {
			t.Fatalf("hash = %q", h)
		}
	}
	if _, res, _ := w.svc.CheckPassword(w.ctx, " ANA ", "mesma senha longa"); res != users.CredOK {
		t.Fatalf("the login is read in any case: %v", res)
	}
}

func TestALoginNameBelongsToOneUser(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.access("Ana", "ana", "uma senha bem longa")
	b := w.add("Bia", users.Observer)
	c, _ := w.svc.NewAccessCode(w.ctx, w.owner, b.ID, users.AccessFirst)
	_, err := w.svc.RedeemAccessCode(w.ctx, c.Code, users.SetupInput{Login: "Ana", Password: "uma senha bem longa", PIN: "123456"})
	if !errors.Is(err, users.ErrLoginTaken) {
		t.Fatalf("err = %v", err)
	}
	// the code was not spent by the failure
	if _, err := w.svc.RedeemAccessCode(w.ctx, c.Code, users.SetupInput{Login: "bia", Password: "uma senha bem longa", PIN: "123456"}); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyTheOwnerMakesCodesOfAccessAndAResetNeedsAPassword(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	adm := w.add("Adm", users.Administrator)
	other := w.add("Bia", users.Observer)
	if _, err := w.svc.NewAccessCode(w.ctx, adm, other.ID, users.AccessFirst); !errors.Is(err, users.ErrLevel) {
		t.Fatalf("err = %v", err)
	}
	if _, err := w.svc.NewAccessCode(w.ctx, w.owner, other.ID, users.AccessReset); err == nil {
		t.Fatal("a reset was made for a user with no password")
	}
	if _, err := w.svc.NewAccessCode(w.ctx, w.owner, other.ID, "outro"); err == nil {
		t.Fatal("a kind that does not exist was accepted")
	}
}

func TestTheSessionsEndInTheSameTransactionAndTheHookIsTold(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	u := w.access("Ana", "ana", "uma senha bem longa")
	var told []string
	w.svc.OnSessionsEnded(func(_ context.Context, id string) { told = append(told, id) })
	if _, err := w.db.Writer().ExecContext(w.ctx, `INSERT INTO panel_sessions (id, token_hash, user_id, created_at, last_used_at) VALUES ('s1', 'h1', ?, 1, 1)`, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Suspend(w.ctx, w.owner, u.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM panel_sessions`).Scan(&n)
	if n != 0 || len(told) != 1 || told[0] != u.ID {
		t.Fatalf("sessions left %d, told %v", n, told)
	}
}
