package panel_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// CA-ADM-01: an Administrator who calls the API of the users is refused with the
// level that is required, and nothing changes.
func TestAnAdministratorIsRefusedTheUsersAPIWithTheLevelRequired(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	adm, tok := a.member("Bruno", "bruno", users.Administrator)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/users"},
		{"POST", "/api/users"},
		{"PATCH", "/api/users/" + adm.ID},
		{"POST", "/api/users/" + adm.ID + "/suspend"},
		{"POST", "/api/users/" + adm.ID + "/remove"},
		{"POST", "/api/users/" + adm.ID + "/pairing-code"},
		{"POST", "/api/users/" + adm.ID + "/access-code"},
		{"POST", "/api/transfer"},
	} {
		w := a.call(c.method, c.path, panel.CreateUserRequest{Name: "Intruso", Level: "observer"}, opts{cookie: tok, pin: "654321"})
		a.wantErr(w, 403, panel.CodeForbiddenLevel)
		var b panel.ErrorBody
		decodeJSON(t, w, &b)
		if b.Error.Params["need"] != "owner" || b.Error.Params["have"] != "administrator" {
			t.Errorf("%s %s: params = %v, want the level required (owner) and the one he has", c.method, c.path, b.Error.Params)
		}
	}
	list, _ := a.users.List(a.ctx)
	if len(list) != 2 {
		t.Fatalf("%d users, want the Owner and Bruno: the refusals changed something", len(list))
	}
}

func TestTheOwnerRegistersAUserWithThePINAndSeesTheFirstAccessCodeOnce(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	body := panel.CreateUserRequest{Name: "Ana Lima", Level: "approver", Workspaces: []string{"loja"}, GitHubLogin: "ana-gh"}
	a.wantErr(a.call("POST", "/api/users", body, opts{cookie: tok}), 403, panel.CodePINRequired)
	if list, _ := a.users.List(a.ctx); len(list) != 1 {
		t.Fatalf("a user was made without the PIN")
	}
	w := a.call("POST", "/api/users", body, opts{cookie: tok, pin: ownerPIN})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	got := decodeInto[panel.CreateUserResponse](t, w)
	if got.User.Name != "Ana Lima" || got.User.Level != "approver" || got.Code.Kind != users.AccessFirst || len(got.Code.Code) != users.AccessCodeLength {
		t.Fatalf("created = %+v", got)
	}
	// the code is the one that opens her first access, and is in no list nor audit line
	if strings.Contains(a.call("GET", "/api/users", nil, opts{cookie: tok}).Body.String(), got.Code.Code) {
		t.Error("the code of first access is in the list")
	}
	if strings.Contains(a.auditText(), got.Code.Code) {
		t.Error("the code of first access is in the audit")
	}
	if w := a.call("POST", "/api/first-access", panel.FirstAccessRequest{Code: got.Code.Code, Login: "ana", Password: otherPass, PIN: "654321"}, opts{}); w.Code != 200 {
		t.Fatalf("first access with the code = %d %s", w.Code, w.Body)
	}
	if a.audited("panel.user.created") != 1 {
		t.Error("the registration is not in the audit")
	}
	// a second Owner is not made
	a.wantErr(a.call("POST", "/api/users", panel.CreateUserRequest{Name: "Outro", Level: "owner"}, opts{cookie: tok, pin: ownerPIN}), 409, panel.CodeOwnerExists)
	a.wantErr(a.call("POST", "/api/users", panel.CreateUserRequest{Name: "Outro", Level: "chefe"}, opts{cookie: tok, pin: ownerPIN}), 422, panel.CodeBadLevel)
	a.wantErr(a.call("POST", "/api/users", panel.CreateUserRequest{Name: "Outro", Level: "observer", Workspaces: []string{"nao-existe"}}, opts{cookie: tok, pin: ownerPIN}), 422, panel.CodeInvalidWorkspace)
}

func TestEditingALevelEndsHisSessionsAndTheAuditKeepsBeforeAndAfter(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	adm, admTok := a.member("Bruno", "bruno", users.Administrator)
	lvl := "observer"
	w := a.call("PATCH", "/api/users/"+adm.ID, panel.EditUserRequest{Level: &lvl}, opts{cookie: tok, pin: ownerPIN})
	if w.Code != 200 {
		t.Fatalf("edit = %d %s", w.Code, w.Body)
	}
	if row := decodeInto[panel.UserRow](t, w); row.Level != "observer" {
		t.Fatalf("row = %+v", row)
	}
	a.wantErr(a.call("GET", "/api/me", nil, opts{cookie: admTok}), 401, panel.CodeUnauthenticated)
	var detail string
	if err := a.db.Reader().QueryRowContext(a.ctx, `SELECT detail FROM audit_log WHERE action = 'panel.user.edited'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, `"before":{`) || !strings.Contains(detail, `"level":"administrator"`) || !strings.Contains(detail, `"level":"observer"`) || !strings.Contains(detail, a.owner.ID) {
		t.Errorf("the audit line does not have the user and the before and after: %s", detail)
	}
	ws := []string{"loja", "nao-existe"}
	a.wantErr(a.call("PATCH", "/api/users/"+adm.ID, panel.EditUserRequest{Workspaces: &ws}, opts{cookie: tok, pin: ownerPIN}), 422, panel.CodeInvalidWorkspace)
	owner := "owner"
	a.wantErr(a.call("PATCH", "/api/users/"+adm.ID, panel.EditUserRequest{Level: &owner}, opts{cookie: tok, pin: ownerPIN}), 409, panel.CodeOwnerExists)
	a.wantErr(a.call("PATCH", "/api/users/nao-existe", panel.EditUserRequest{Level: &lvl}, opts{cookie: tok, pin: ownerPIN}), 404, panel.CodeNotFound)
}

func TestSuspendReactivateAndRemoveEndTheSessionsAndTheOwnerIsNotHisOwnTarget(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	bruno, brunoTok := a.member("Bruno", "bruno", users.Approver)
	pinned := opts{cookie: tok, pin: ownerPIN}
	a.wantErr(a.call("POST", "/api/users/"+a.owner.ID+"/suspend", nil, pinned), 409, panel.CodeSelfAction)
	a.wantErr(a.call("POST", "/api/users/"+a.owner.ID+"/remove", nil, pinned), 409, panel.CodeSelfAction)

	if w := a.call("POST", "/api/users/"+bruno.ID+"/suspend", nil, pinned); w.Code != 200 || decodeInto[panel.UserRow](t, w).State != "suspended" {
		t.Fatalf("suspend = %d %s", w.Code, w.Body)
	}
	a.wantErr(a.call("GET", "/api/me", nil, opts{cookie: brunoTok}), 401, panel.CodeUnauthenticated)
	a.wantErr(a.login("bruno", otherPass, opts{}), 401, panel.CodeInvalidCredentials) // a suspended user does not get in
	if w := a.call("POST", "/api/users/"+bruno.ID+"/reactivate", nil, pinned); w.Code != 200 || decodeInto[panel.UserRow](t, w).State != "active" {
		t.Fatalf("reactivate = %d %s", w.Code, w.Body)
	}
	if w := a.call("POST", "/api/users/"+bruno.ID+"/remove", nil, pinned); w.Code != http.StatusNoContent {
		t.Fatalf("remove = %d %s", w.Code, w.Body)
	}
	list := decodeInto[panel.UserList](t, a.call("GET", "/api/users", nil, opts{cookie: tok}))
	if len(list.Users) != 1 || list.Users[0].ID != a.owner.ID || len(list.Workspaces) != 1 {
		t.Fatalf("list = %+v", list)
	}
	for _, act := range []string{"suspend", "reactivate", "remove"} {
		if a.audited("panel.user."+act) != 1 {
			t.Errorf("%s is not in the audit", act)
		}
	}
}

func TestThePairingCodeAndTheResetCodeAreShownOnceAndNeedThePIN(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	bruno, brunoTok := a.member("Bruno", "bruno", users.Approver)
	a.wantErr(a.call("POST", "/api/users/"+bruno.ID+"/pairing-code", nil, opts{cookie: tok}), 403, panel.CodePINRequired)
	w := a.call("POST", "/api/users/"+bruno.ID+"/pairing-code", nil, opts{cookie: tok, pin: ownerPIN})
	if w.Code != 200 || len(decodeInto[panel.PairingCodeResponse](t, w).Code) != users.CodeLength {
		t.Fatalf("pairing code = %d %s", w.Code, w.Body)
	}
	w = a.call("POST", "/api/users/"+bruno.ID+"/access-code", panel.AccessCodeRequest{Kind: users.AccessReset}, opts{cookie: tok, pin: ownerPIN})
	if w.Code != 200 || decodeInto[panel.AccessCodeResponse](t, w).Kind != users.AccessReset {
		t.Fatalf("reset code = %d %s", w.Code, w.Body)
	}
	a.wantErr(a.call("GET", "/api/me", nil, opts{cookie: brunoTok}), 401, panel.CodeUnauthenticated) // a reset ends his sessions
}

func TestTheOwnerPassesThePostAndTheChosenUserAcceptsWithHisPIN(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	adm, admTok := a.member("Bruno", "bruno", users.Administrator)
	a.wantErr(a.call("POST", "/api/transfer", panel.TransferRequest{To: adm.ID}, opts{cookie: tok}), 403, panel.CodePINRequired)
	a.wantErr(a.call("POST", "/api/transfer", panel.TransferRequest{To: a.owner.ID}, opts{cookie: tok, pin: ownerPIN}), 409, panel.CodeTransferBad)
	w := a.call("POST", "/api/transfer", panel.TransferRequest{To: adm.ID}, opts{cookie: tok, pin: ownerPIN})
	if w.Code != 200 {
		t.Fatalf("start = %d %s", w.Code, w.Body)
	}
	if st := decodeInto[panel.TransferState](t, w); st.Transfer == nil || st.Transfer.ToID != adm.ID || st.Transfer.ToMe {
		t.Fatalf("transfer = %+v", st.Transfer)
	}
	// the chosen user sees it, and the Owner can take it back
	if st := decodeInto[panel.TransferState](t, a.call("GET", "/api/transfer", nil, opts{cookie: admTok})); st.Transfer == nil || !st.Transfer.ToMe {
		t.Fatalf("what the chosen user sees = %+v", st.Transfer)
	}
	if w := a.call("DELETE", "/api/transfer", nil, opts{cookie: tok, pin: ownerPIN}); w.Code != http.StatusNoContent {
		t.Fatalf("cancel = %d %s", w.Code, w.Body)
	}
	a.wantErr(a.call("POST", "/api/transfer/accept", nil, opts{cookie: admTok, pin: "654321"}), 404, panel.CodeNoTransfer)
	if w := a.call("POST", "/api/transfer", panel.TransferRequest{To: adm.ID}, opts{cookie: tok, pin: ownerPIN}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	// who was not chosen does not accept, nor does the Owner who asked
	a.wantErr(a.call("POST", "/api/transfer/accept", nil, opts{cookie: tok, pin: ownerPIN}), 404, panel.CodeNoTransfer)
	a.wantErr(a.call("POST", "/api/transfer/accept", nil, opts{cookie: admTok}), 403, panel.CodePINRequired)
	if w := a.call("POST", "/api/transfer/accept", nil, opts{cookie: admTok, pin: "654321"}); w.Code != http.StatusNoContent {
		t.Fatalf("accept = %d %s", w.Code, w.Body)
	}
	a.wantErr(a.call("GET", "/api/me", nil, opts{cookie: tok}), 401, panel.CodeUnauthenticated)
	a.wantErr(a.call("GET", "/api/me", nil, opts{cookie: admTok}), 401, panel.CodeUnauthenticated)
	now, _ := a.users.Get(a.ctx, adm.ID)
	old, _ := a.users.Get(a.ctx, a.owner.ID)
	if now.Level != users.Owner || old.Level != users.Administrator {
		t.Fatalf("levels after the transfer: new %s, old %s", now.Level, old.Level)
	}
	for _, act := range []string{"started", "cancelled", "accepted"} {
		if a.audited("panel.transfer."+act) < 1 {
			t.Errorf("the transfer %s is not in the audit", act)
		}
	}
}

func TestConnectGitHubShowsTheCodeAndTheStateWithoutAnyToken(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	me := decodeInto[panel.GitHubMe](t, a.call("GET", "/api/me/github", nil, opts{cookie: tok}))
	if !me.May || me.GitHub.State != "" {
		t.Fatalf("before = %+v", me)
	}
	w := a.call("POST", "/api/me/github/begin", nil, opts{cookie: tok})
	if w.Code != 200 {
		t.Fatalf("begin = %d %s", w.Code, w.Body)
	}
	ch := decodeInto[panel.GitHubChallenge](t, w)
	if ch.UserCode != "WDJB-MJHT" || ch.IntervalSeconds != 5 || !strings.HasPrefix(ch.VerificationURI, "https://") {
		t.Fatalf("challenge = %+v", ch)
	}
	if res := decodeInto[panel.GitHubPoll](t, a.call("POST", "/api/me/github/poll", nil, opts{cookie: tok})); res.State != "pending" {
		t.Fatalf("poll = %+v", res)
	}
	a.gh.poll = []pollAnswer{{res: github.PollConnected}}
	if res := decodeInto[panel.GitHubPoll](t, a.call("POST", "/api/me/github/poll", nil, opts{cookie: tok})); res.State != "connected" {
		t.Fatalf("poll = %+v", res)
	}
	list := decodeInto[panel.UserList](t, a.call("GET", "/api/users", nil, opts{cookie: tok}))
	if g := list.Users[0].GitHub; g.State != "connected" || g.Login != "lucas-gh" || g.ExpiresAt == nil {
		t.Fatalf("the list shows %+v", g)
	}
	if a.audited("panel.github.begun") != 1 || a.audited("panel.github.connected") != 1 {
		t.Error("the connection is not in the audit")
	}
}

// CA-GH-APROV-06, the half of the panel: a login that is not the registered one
// makes the engine discard the token (internal/github), and the panel shows the
// error, never "connected".
func TestAnotherGitHubLoginThanTheRegisteredOneShowsTheError(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	a.wantOK(a.call("POST", "/api/me/github/begin", nil, opts{cookie: tok}))
	a.gh.poll = []pollAnswer{{err: github.ErrLoginMismatch}}
	a.wantErr(a.call("POST", "/api/me/github/poll", nil, opts{cookie: tok}), 409, panel.CodeGitHubMismatch)
	if g := decodeInto[panel.GitHubMe](t, a.call("GET", "/api/me/github", nil, opts{cookie: tok})).GitHub; g.State != "" {
		t.Fatalf("the state after the refusal = %+v, want none", g)
	}
	var detail string
	if err := a.db.Reader().QueryRowContext(a.ctx, `SELECT detail FROM audit_log WHERE action = 'panel.github.failed'`).Scan(&detail); err != nil || !strings.Contains(detail, "login_mismatch") {
		t.Fatalf("audit = %q, %v", detail, err)
	}
	for _, c := range []struct {
		err  error
		code panel.Code
		st   int
	}{
		{github.ErrDeviceDenied, panel.CodeGitHubDenied, 403},
		{github.ErrDeviceExpired, panel.CodeGitHubExpired, 409},
		{github.ErrNoDeviceFlow, panel.CodeGitHubNoFlow, 409},
		{github.ErrNoGitHubLogin, panel.CodeGitHubNoLogin, 422},
	} {
		a.gh.poll = []pollAnswer{{err: c.err}}
		a.wantErr(a.call("POST", "/api/me/github/poll", nil, opts{cookie: tok}), c.st, c.code)
	}
	a.gh.begin = github.ErrDeviceFlowOff
	a.wantErr(a.call("POST", "/api/me/github/begin", nil, opts{cookie: tok}), 503, panel.CodeGitHubFlowOff)
}

func TestConnectGitHubIsForTheLevelsAndWorkspacesThatMayHaveIt(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	_, obsTok := a.member("Olga", "olga", users.Observer)
	a.wantErr(a.call("POST", "/api/me/github/begin", nil, opts{cookie: obsTok}), 403, panel.CodeForbiddenLevel)
	if me := decodeInto[panel.GitHubMe](t, a.call("GET", "/api/me/github", nil, opts{cookie: obsTok})); me.May {
		t.Error("an Observer may connect")
	}
	ap, _ := a.member("Ana", "ana", users.Approver)
	none := []string{}
	if _, err := a.users.Edit(a.ctx, a.owner, ap.ID, users.Edit{Workspaces: &none}); err != nil {
		t.Fatal(err)
	}
	apTok := loginAs(a.rig, "ana", otherPass) // the edit ended his session
	a.wantErr(a.call("POST", "/api/me/github/begin", nil, opts{cookie: apTok}), 403, panel.CodeGitHubNoRight)
	if len(a.gh.begun) != 0 {
		t.Error("the flow began for a user who may not have the credential")
	}
}
