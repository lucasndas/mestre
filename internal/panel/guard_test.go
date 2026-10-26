package panel_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// Attempts at the same time cannot all pass the limit before a failure is written:
// no more than LoginErrors passwords are looked at.
func TestParallelWrongPasswordsAreNotCheckedPastTheLimit(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.member("Ana", "ana", users.Approver)
	var wg sync.WaitGroup
	var mu sync.Mutex
	checked := 0
	for range 20 {
		wg.Go(func() {
			w := r.login("ana", "senha errada demais", opts{})
			if errCode(w) == panel.CodeInvalidCredentials {
				mu.Lock()
				checked++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if checked > panel.LoginErrors {
		t.Fatalf("%d passwords were looked at, the limit is %d", checked, panel.LoginErrors)
	}
	r.wantErr(r.login("ana", otherPass, opts{}), 429, panel.CodeLoginBlocked)
}

// What is no login name is nobody's, and is written nowhere.
func TestATextThatIsNoLoginNameIsNotStored(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.wantErr(r.login(strings.Repeat("x", 5000), "qualquer coisa longa", opts{}), 401, panel.CodeInvalidCredentials)
	var n int
	_ = r.db.Reader().QueryRowContext(r.ctx, `SELECT COUNT(*) FROM panel_attempts`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d attempts were stored", n)
	}
}

func TestAWrongCurrentPasswordCountsTowardsTheLoginLimit(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	tok := r.ownerSession()
	for range panel.LoginErrors {
		r.wantErr(r.call("POST", "/api/password", panel.PasswordRequest{Current: "errada errada errada", New: "uma senha nova longa"}, opts{cookie: tok}), 403, panel.CodePasswordWrong)
	}
	r.wantErr(r.call("POST", "/api/password", panel.PasswordRequest{Current: ownerPass, New: "uma senha nova longa"}, opts{cookie: tok}), 429, panel.CodeLoginBlocked)
}

func TestTheTransferOfTheOwnerEndsTheSessionsOfBoth(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ownerTok := r.ownerSession()
	adm, admTok := r.member("Bruno", "bruno", users.Administrator)
	pin := func(u users.User, action, secret string) string {
		req, err := r.users.StartPIN(r.ctx, u.ID, action)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.users.CheckPIN(r.ctx, req.ID, secret); err != nil {
			t.Fatal(err)
		}
		return req.ID
	}
	if err := r.users.StartTransfer(r.ctx, r.owner, adm.ID, pin(r.owner, users.ActionStartTransfer, ownerPIN)); err != nil {
		t.Fatal(err)
	}
	if err := r.users.AcceptTransfer(r.ctx, adm, pin(adm, users.ActionAcceptTransfer, "654321")); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{ownerTok, admTok} {
		r.wantErr(r.call("GET", "/api/me", nil, opts{cookie: tok}), 401, panel.CodeUnauthenticated)
	}
}
