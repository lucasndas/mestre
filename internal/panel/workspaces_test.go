package panel_test

import (
	"context"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// fakeWorkspaces is the engine behind the screens of the workspaces.
type fakeWorkspaces struct {
	mu        sync.Mutex
	created   []panel.CreateWorkspaceRequest
	retried   []string
	paused    []string
	answers   []string
	confirmed [][2]string
	doc       string
	confirm   panel.VerificationConfirmed
	create    error
}

func (f *fakeWorkspaces) List(context.Context) ([]panel.WorkspaceRow, error) {
	return []panel.WorkspaceRow{
		{Slug: "loja", Repo: "acme/loja", State: "preparing", Stages: []panel.WorkspaceStage{{Key: "apps", State: panel.StageCurrent}}},
		{Slug: "blog", Repo: "acme/blog", State: "active"},
	}, nil
}

func (f *fakeWorkspaces) Sheets(context.Context) ([]panel.SheetOption, error) {
	return []panel.SheetOption{{Name: "gm-claude", Agent: "claude-code"}}, nil
}

func (f *fakeWorkspaces) Create(_ context.Context, _ users.User, req panel.CreateWorkspaceRequest) (panel.WorkspaceRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.create != nil {
		return panel.WorkspaceRow{}, f.create
	}
	f.created = append(f.created, req)
	return panel.WorkspaceRow{Slug: "novo", Repo: req.Repo, State: "preparing"}, nil
}

func (f *fakeWorkspaces) Retry(_ context.Context, slug string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.retried = append(f.retried, slug)
	return nil
}

func (f *fakeWorkspaces) Pause(_ context.Context, slug string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, slug)
	return nil
}

func (f *fakeWorkspaces) Resume(context.Context, string) error {
	return &panel.Refusal{Reason: "bad_state", Message: "o workspace não está pausado"}
}

func (f *fakeWorkspaces) AnswerVersioning(_ context.Context, _ users.User, _, answer string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, answer)
	return nil
}

func (f *fakeWorkspaces) Verification(context.Context, string) (panel.VerificationView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := panel.VerificationView{File: f.doc, Commands: []string{"go build ./..."}}
	if f.confirm.Hash != "" {
		c := f.confirm
		v.Confirmed = &c
	}
	return v, nil
}

func (f *fakeWorkspaces) ConfirmVerification(_ context.Context, by users.User, slug string, doc []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if string(doc) == "ruim" {
		return "", &panel.Refusal{Reason: "invalid_document", Message: "verification: the document does not hold"}
	}
	f.confirmed = append(f.confirmed, [2]string{by.ID, string(doc)})
	f.doc = string(doc)
	f.confirm = panel.VerificationConfirmed{Doc: string(doc), Hash: "h2", By: by.Name}
	return "h2", nil
}

// CA-WSP-03 (the part of the server).
// The workspaces the user sees are the ones the user acts in (the Owner sees all),
// and the screens answer unavailable without the engine.
func TestTheWorkspacesListIsTheOnesTheUserSees(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.wantErr(r.call("GET", "/api/workspaces", nil, opts{cookie: r.ownerSession()}), 503, panel.CodeUnavailable)
	r.panel.SetWorkspaces(&fakeWorkspaces{})
	_, obs := r.member("Beto", "beto", users.Observer)
	_, adm := r.member("Ana", "ana", users.Administrator)

	l := decodeInto[panel.WorkspaceList](t, r.call("GET", "/api/workspaces", nil, opts{cookie: obs}))
	if len(l.Workspaces) != 1 || l.Workspaces[0].Slug != "loja" || l.CanCreate || len(l.Sheets) != 0 {
		t.Errorf("an observer sees %+v", l)
	}
	if l.Workspaces[0].CanAct != true {
		t.Error("the row does not say the user acts in it")
	}
	l = decodeInto[panel.WorkspaceList](t, r.call("GET", "/api/workspaces", nil, opts{cookie: adm}))
	if !l.CanCreate || len(l.Sheets) != 1 {
		t.Errorf("an administrator sees %+v", l)
	}
	l = decodeInto[panel.WorkspaceList](t, r.call("GET", "/api/workspaces", nil, opts{cookie: r.ownerSession()}))
	if len(l.Workspaces) != 2 {
		t.Errorf("the Owner sees %d workspaces", len(l.Workspaces))
	}
}

// CA-WSP-02 (the part of the server).
// The creation, the retry and the pause are an Administrator's, in the workspaces
// the Administrator acts in, whatever the buttons said.
func TestCreatingAndRunningAWorkspaceNeedsAnAdministratorWhoActsInIt(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	f := &fakeWorkspaces{}
	r.panel.SetWorkspaces(f)
	_, appr := r.member("Cris", "cris", users.Approver)
	_, adm := r.member("Ana", "ana", users.Administrator)
	body := panel.CreateWorkspaceRequest{Repo: "acme/novo", GrandMasterSheet: "gm-claude"}

	r.wantErr(r.call("POST", "/api/workspaces", body, opts{cookie: appr}), 403, panel.CodeForbiddenLevel)
	r.wantErr(r.call("POST", "/api/workspaces/loja/retry", nil, opts{cookie: appr}), 403, panel.CodeForbiddenLevel)
	r.wantErr(r.call("POST", "/api/workspaces", panel.CreateWorkspaceRequest{Repo: "acme/novo"}, opts{cookie: adm}), 400, panel.CodeBadRequest)
	if w := r.call("POST", "/api/workspaces", body, opts{cookie: adm}); w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if len(f.created) != 1 || f.created[0].GrandMasterSheet != "gm-claude" {
		t.Errorf("the engine was asked %+v", f.created)
	}
	if r.audited("panel.workspace.created") != 1 {
		t.Error("the creation was not audited")
	}
	// a workspace the Administrator does not act in is not found
	r.wantErr(r.call("POST", "/api/workspaces/blog/retry", nil, opts{cookie: adm}), 404, panel.CodeNotFound)
	if w := r.call("POST", "/api/workspaces/loja/retry", nil, opts{cookie: adm}); w.Code != 204 {
		t.Fatalf("retry: %d %s", w.Code, w.Body)
	}
	if w := r.call("POST", "/api/workspaces/loja/pause", nil, opts{cookie: adm}); w.Code != 204 {
		t.Fatalf("pause: %d %s", w.Code, w.Body)
	}
	if len(f.retried) != 1 || len(f.paused) != 1 {
		t.Errorf("retried %v, paused %v", f.retried, f.paused)
	}
	// what the engine refuses is answered with the reason, and not audited as done
	r.wantErr(r.call("POST", "/api/workspaces/loja/resume", nil, opts{cookie: adm}), 409, panel.CodeRefused)
	if r.audited("panel.workspace.resume") != 0 {
		t.Error("a refused resume was audited as done")
	}
	r.wantErr(r.call("POST", "/api/workspaces/loja/versioning", panel.VersioningRequest{Answer: "talvez"}, opts{cookie: adm}), 400, panel.CodeBadRequest)
	if w := r.call("POST", "/api/workspaces/loja/versioning", panel.VersioningRequest{Answer: "versioned"}, opts{cookie: adm}); w.Code != 204 {
		t.Fatalf("versioning: %d %s", w.Code, w.Body)
	}
	if len(f.answers) != 1 || r.audited("panel.workspace.versioning") != 1 {
		t.Errorf("answers %v", f.answers)
	}
}

// CA-ADM-15: an Administrator edits and confirms the verification commands, the
// confirmed version is the one in force and the confirmation is in the audit; an
// Approver is refused by the server.
func TestAnAdministratorConfirmsTheVerificationCommands(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	f := &fakeWorkspaces{doc: "version: 1\ncomandos: [\"go build ./...\"]\n"}
	r.panel.SetWorkspaces(f)
	_, appr := r.member("Cris", "cris", users.Approver)
	_, adm := r.member("Ana", "ana", users.Administrator)
	path := "/api/workspaces/loja/verification"

	if v := decodeInto[panel.VerificationView](t, r.call("GET", path, nil, opts{cookie: appr})); v.File == "" || v.Confirmed != nil {
		t.Errorf("an approver reads %+v", v)
	}
	r.wantErr(r.call("PUT", path, panel.ConfirmVerificationRequest{Doc: "version: 1"}, opts{cookie: appr}), 403, panel.CodeForbiddenLevel)
	r.wantErr(r.call("PUT", path, panel.ConfirmVerificationRequest{Doc: "ruim"}, opts{cookie: adm}), 409, panel.CodeRefused)
	if len(f.confirmed) != 0 {
		t.Fatalf("confirmed %v", f.confirmed)
	}
	edited := "version: 1\ncomandos: [\"go build ./...\", \"go test ./...\"]\n"
	w := r.call("PUT", path, panel.ConfirmVerificationRequest{Doc: edited}, opts{cookie: adm})
	if w.Code != 200 {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	if v := decodeInto[panel.VerificationView](t, w); v.Confirmed == nil || v.Confirmed.Doc != edited || v.Confirmed.By != "Ana" {
		t.Errorf("the answer is %+v", v)
	}
	if len(f.confirmed) != 1 {
		t.Fatalf("confirmed %v", f.confirmed)
	}
	if r.audited("panel.workspace.verification_confirmed") != 1 || !contains(r.auditText(), "go test") {
		t.Error("the edit is not in the audit with the document")
	}
}
