package main

import (
	"context"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// The workspace the smoke test of the browser lists: an active one, with the proposal
// of the verification commands waiting for an Administrator. The engine behind it is a
// double that keeps its state in memory, so the panel, the socket and the screens are
// the real ones.
type fakeWorkspaces struct {
	mu        sync.Mutex
	file      string
	confirmed *panel.VerificationConfirmed
}

var _ panel.WorkspaceSource = (*fakeWorkspaces)(nil)

func newFakeWorkspaces() *fakeWorkspaces {
	return &fakeWorkspaces{file: "version: 1\ncomandos: [\"go build ./...\"]\ntimeout: 15m\n"}
}

func (f *fakeWorkspaces) List(context.Context) ([]panel.WorkspaceRow, error) {
	stages := []panel.WorkspaceStage{}
	for _, k := range []string{"apps", "mirror", "files", "analysis", "approval_test"} {
		stages = append(stages, panel.WorkspaceStage{Key: k, State: panel.StageDone})
	}
	return []panel.WorkspaceRow{{Slug: fakeWorkspace, Repo: "acme/e2e", DefaultBranch: "main", State: "active", Stages: stages,
		Versioning: "local", VersioningAsked: true, GrandMasterSheet: "Grão-Mestre", GrandMaster: "idle", CreatedAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)}}, nil
}

func (f *fakeWorkspaces) Sheets(context.Context) ([]panel.SheetOption, error) {
	return []panel.SheetOption{{Name: "Grão-Mestre", Agent: "claude-code"}}, nil
}

func (f *fakeWorkspaces) Create(context.Context, users.User, panel.CreateWorkspaceRequest) (panel.WorkspaceRow, error) {
	return panel.WorkspaceRow{}, &panel.Refusal{Reason: "creation_failed", Message: "the e2e Guild has no GitHub"}
}

func (f *fakeWorkspaces) Retry(context.Context, string) error  { return nil }
func (f *fakeWorkspaces) Pause(context.Context, string) error  { return nil }
func (f *fakeWorkspaces) Resume(context.Context, string) error { return nil }

func (f *fakeWorkspaces) AnswerVersioning(context.Context, users.User, string, string) error {
	return nil
}

func (f *fakeWorkspaces) Verification(_ context.Context, slug string) (panel.VerificationView, error) {
	if slug != fakeWorkspace {
		return panel.VerificationView{}, panel.ErrWorkspaceNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	v := panel.VerificationView{File: f.file, Commands: []string{"go build ./..."}}
	if f.confirmed != nil {
		c := *f.confirmed
		v.Confirmed = &c
	}
	return v, nil
}

func (f *fakeWorkspaces) ConfirmVerification(_ context.Context, by users.User, slug string, doc []byte) (string, error) {
	if slug != fakeWorkspace {
		return "", panel.ErrWorkspaceNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.file = string(doc)
	f.confirmed = &panel.VerificationConfirmed{Doc: string(doc), Hash: "e2e", By: by.Name, At: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)}
	return "e2e", nil
}
