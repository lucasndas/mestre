package chain_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
)

// devCatalog knows the sheet the gate tests use for the Dev of the table.
type devCatalog struct{}

func (devCatalog) Sheet(_ context.Context, name string) (quests.SheetInfo, bool, error) {
	if name == "Dev Claude" {
		return quests.SheetInfo{Name: name, Class: "dev"}, true, nil
	}
	return quests.SheetInfo{}, false, nil
}

func (devCatalog) ClassAllowed(context.Context, string, string) error { return nil }

// fakeRules is what the base branch of the repository asks of a PR: the rule
// "branch atualizada" is upToDate.
type fakeRules struct {
	mu       sync.Mutex
	upToDate bool
	err      error
	reads    int
}

func (f *fakeRules) ReadRules(_ context.Context, _, _, _ string) (github.ReviewRules, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return github.ReviewRules{UpToDate: f.upToDate}, f.err
}

func (f *fakeRules) set(upToDate bool) {
	f.mu.Lock()
	f.upToDate = upToDate
	f.mu.Unlock()
}

// fakeVerifier is the executor of the verification as the gate sees it
// (quests.Verifier): it records every run and answers by run.
type fakeVerifier struct {
	mu        sync.Mutex
	confirmed bool
	calls     []quests.VerifyRequest
	// run answers a verification; nil is green.
	run func(quests.VerifyRequest) quests.VerifyResult
}

func (v *fakeVerifier) Confirmed(context.Context, string) (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.confirmed, nil
}

func (v *fakeVerifier) Verify(_ context.Context, r quests.VerifyRequest) (quests.VerifyResult, error) {
	v.mu.Lock()
	run := v.run
	v.calls = append(v.calls, r)
	v.mu.Unlock()
	if run == nil {
		return quests.VerifyResult{OK: true}, nil
	}
	return run(r), nil
}

func (v *fakeVerifier) runs() []quests.VerifyRequest {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]quests.VerifyRequest(nil), v.calls...)
}

func (v *fakeVerifier) confirm(on bool) {
	v.mu.Lock()
	v.confirmed = on
	v.mu.Unlock()
}

func (v *fakeVerifier) setRun(f func(quests.VerifyRequest) quests.VerifyResult) {
	v.mu.Lock()
	v.run = f
	v.mu.Unlock()
}

// mirror is the mirror of the workspace, where the branch of the table lives.
func (w *world) mirror() *fakegit.Handle {
	w.t.Helper()
	m, err := w.orc.Workspaces.OpenMirror(w.ctx, w.slug)
	if err != nil {
		w.t.Fatal(err)
	}
	return m.(*fakegit.Handle)
}

func (w *world) call(agent, cmd string, args ...string) localchan.Response {
	w.t.Helper()
	return w.fwd.Forward(w.ctx, &localchan.Call{
		Request: localchan.Request{Cmd: cmd, AgentID: agent, Args: localchan.TextArgs(args...), Flags: map[string]any{}},
		Caller:  localchan.Caller{Identified: true, AgentID: agent},
	})
}

// devWorks makes the Dev of the table: the character that changed code in a
// mission, whose commit is in the branch of the table.
func (w *world) devWorks() characters.Character {
	w.t.Helper()
	dev := w.recruitSheet("Dev Claude", "Dev Claude 1")
	v, err := w.orc.Quests.Create(w.ctx, quests.CreateRequest{Workspace: w.slug, Table: w.table.ID, Designer: w.master.ID,
		Data: []byte("name: Login\nsteps:\n  - {id: dev, character: Dev Claude 1}\n")})
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.orc.Quests.Run(w.ctx, v.ID, ""); err != nil {
		w.t.Fatal(err)
	}
	w.mirror().Data().Commit(w.table.Branch, "feat: login", map[string]string{"login.go": "package app\n"})
	r := w.call(dev.ID, "done", "login feito")
	if !r.OK {
		w.t.Fatalf("done of the Dev: %+v", r.Error)
	}
	w.machines.Report(w.ctx, dev.ID, characters.Idle)
	return dev
}

func (w *world) recruitSheet(sheet, name string) characters.Character {
	w.t.Helper()
	res, err := w.orc.Characters.Create(w.ctx, characters.CreateRequest{Name: name, Sheet: sheet, Workspace: w.slug, Table: w.table.ID, Role: characters.RoleCharacter,
		Creator: characters.Creator{Kind: characters.CreatedByCharacter, ID: w.master.ID}})
	if err != nil {
		w.t.Fatal(err)
	}
	return res.Character
}

// moveBase puts a commit in the main branch of GitHub and has the mirror
// fetched, as the loop of the workspace does.
func (w *world) moveBase(file string) {
	w.t.Helper()
	w.git.Handle(w.remote).Data().Commit("main", "feat: "+file, map[string]string{file: "x\n"})
	if err := w.orc.Workspaces.Fetch(w.ctx, w.slug); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) refTip(ref string) string {
	w.t.Helper()
	h, err := w.mirror().ResolveRef(w.ctx, ref)
	if err != nil {
		w.t.Fatal(err)
	}
	return string(h)
}

func (w *world) baseTip() string { return w.refTip("refs/remotes/origin/main") }

// openPR asks the approval of the PR of the table, at the head the branch has,
// as the Grão-Mestre's request is born: the target is the exact PR.
func (w *world) openPR(number int) (int64, error) {
	w.t.Helper()
	head := w.refTip("refs/heads/" + w.table.Branch)
	w.prs.set(number, green(3))
	return w.ch.Request(w.ctx, chain.OpenRequest{Type: classes.RequestPR, Workspace: w.slug, Table: w.table.ID, About: "PR do épico",
		PR: &chain.PRRef{Repo: "lucas/Meu_App", Number: number, SHA: head}, Target: approvals.PRTarget("lucas/Meu_App", number, head, "main")})
}

func (w *world) mustOpenPR(number int) int64 {
	w.t.Helper()
	id, err := w.openPR(number)
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

// targetOf is the target the request was bound to.
func (w *world) targetOf(id int64) map[string]any {
	w.t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.request(id).Target, &m); err != nil {
		w.t.Fatal(err)
	}
	return m
}

// syncMissions lists the missions of the step sync of the table.
func (w *world) syncMissions() []quests.Quest {
	w.t.Helper()
	all, err := w.orc.Quests.Store().OfTable(w.ctx, w.slug, w.table.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	var out []quests.Quest
	for _, q := range all {
		if quests.IsSyncName(q.Name) {
			out = append(out, q)
		}
	}
	return out
}

// integrate puts the tip of the base in the branch of the table, as the merge
// of the Dev does, and commits the resolution.
func (w *world) integrate() {
	w.t.Helper()
	m := w.mirror()
	if err := m.UpdateRef(w.ctx, "refs/heads/"+w.table.Branch, gitx.Hash(w.baseTip())); err != nil {
		w.t.Fatal(err)
	}
	m.Data().Commit(w.table.Branch, "merge: base", map[string]string{"merged.go": "package app\n"})
}

func asFreshnessError(err error) (*chain.FreshnessError, bool) {
	var fe *chain.FreshnessError
	return fe, errors.As(err, &fe)
}
