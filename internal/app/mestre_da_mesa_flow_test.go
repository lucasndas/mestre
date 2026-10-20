package app_test

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/ghcli"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
)

// The table master as the PO of its epic, over the real CLI: the Citadel of the
// CLI worlds with the double of GitHub behind the commands `issue` and `pr` and
// behind the issues of the missions (docs/orquestracao/mestre-da-mesa.md).

const mmRepo = "lucas/meu-app"

// mm is the double of GitHub and the sending of the branch of a world.
type mm struct {
	w      *commWorld
	gh     *fakegithub.Server
	objs   *github.Objects
	hub    *mmHub
	git    *fakegit.Git
	mirror string
	remote string
	branch string
}

// setup is the option of newCommWorld that puts GitHub in the world.
func (m *mm) setup(w *commWorld, oo *app.OrchestrationOptions) {
	m.w = w
	gh, err := fakegithub.New(w.clk, w.t)
	if err != nil {
		w.t.Fatal(err)
	}
	gh.AddRepo("lucas", "meu-app")
	gh.IssueToken("tok_gm", "grand_master")
	gh.IssueToken("tok_mestre", "mestre")
	for i := 1; i < 42; i++ {
		gh.AddIssue(mmRepo, fmt.Sprintf("issue %d", i))
	}
	gh.AddIssue(mmRepo, "Login de usuários", github.EpicLabel) // #42, the epic the world's tables open on
	m.gh, m.objs = gh, &github.Objects{DB: oo.Guild, Clock: w.clk}
	m.hub = &mmHub{m: m}
	oo.QuestGitHub, oo.QuestIssues = m.hub, mmOwner{m}

	root := w.t.TempDir()
	m.git = fakegit.NewGit(w.clk)
	m.mirror, m.remote = filepath.Join(root, "espelho.git"), filepath.Join(root, "github.git")
	for _, d := range []string{m.mirror, m.remote} {
		if _, err := m.git.Init(w.ctx, d, gitx.InitOptions{Bare: true, InitialBranch: "main"}); err != nil {
			w.t.Fatal(err)
		}
	}
	m.git.Handle(m.mirror).Data().Commit("main", "chore: initial", map[string]string{"README.md": "# Meu App\n"})

	w.mounts = append(w.mounts, func(fwd *localchan.LocalForwarder) {
		h := &ghcli.Handlers{Characters: w.orc.Registry, Clients: mmClients{gh}, Repos: mmRepos{}, Tables: mmTables{w}, PRs: mmPRs{w}, Objects: m.objs, Clock: w.clk,
			Lease: mmLease{},
			Opened: func(ctx context.Context, key string, n int, head string) error {
				ws, id, _ := strings.Cut(key, "/")
				_, err := w.orc.Tables.PROpened(ctx, ws, id, n, head)
				return err
			},
			Send: github.Sending{Branches: mmBranches{m}, Auth: mmAuth{}, Workflows: mmWorkflows{},
				Criteria: quests.Acceptance{Store: &quests.Store{DB: oo.Guild}, Body: m.hub.IssueBody}}}
		h.Register(fwd)
	})
}

// work puts a commit of the table in the branch the engine sends.
func (m *mm) work() {
	m.w.t.Helper()
	m.branch = m.w.table.Branch
	d := m.git.Handle(m.mirror).Data()
	if err := d.Branch(m.branch, "main"); err != nil {
		m.w.t.Fatal(err)
	}
	d.Commit(m.branch, "feat: login", map[string]string{"src/login.ts": "export const timeout = 30\n"})
}

func (m *mm) pushed() bool {
	repo, err := m.git.Open(m.w.ctx, m.remote)
	if err != nil {
		return false
	}
	_, err = repo.ResolveRef(m.w.ctx, m.branch)
	return err == nil
}

// mmHub is the GitHub of the missions over the double: the App "Mestre" makes
// the issues under the epic and registers them for the table.
type mmHub struct {
	m       *mm
	mu      sync.Mutex
	created int
}

func (h *mmHub) rest() *github.REST {
	return &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(h.m.gh)},
		Token: func(context.Context, string) (string, error) { return "tok_mestre", nil }, App: "mestre"}
}

func (h *mmHub) make(ctx context.Context, parent int, title, body string) (int, error) {
	d, err := h.rest().CreateIssue(ctx, mmRepo, title, body, nil)
	if err != nil {
		return 0, err
	}
	if err := h.rest().AddSubIssue(ctx, mmRepo, parent, d.ID); err != nil {
		return 0, err
	}
	key := h.m.w.slug + "/" + h.m.w.table.ID
	err = h.m.objs.Register(ctx, github.Object{Repo: mmRepo, Kind: github.KindIssue, Number: d.Number, GitHubID: d.ID, Table: key, ParentNumber: parent, Op: github.NewOp()})
	return d.Number, err
}

func (h *mmHub) CreateIssue(ctx context.Context, _ string, epic int, title, body string) (int, error) {
	h.mu.Lock()
	h.created++
	h.mu.Unlock()
	return h.make(ctx, epic, title, body)
}

func (h *mmHub) CreateSubIssue(ctx context.Context, _ string, parent int, title, body string) (int, error) {
	return h.make(ctx, parent, title, body)
}

func (h *mmHub) UpdateBody(ctx context.Context, _ string, n int, body string) error {
	return h.rest().UpdateIssue(ctx, mmRepo, n, github.IssueEdit{Body: &body})
}

func (h *mmHub) Comment(ctx context.Context, _ string, n int, _, body string) error {
	_, err := h.rest().PostComment(ctx, mmRepo, n, body)
	return err
}

func (h *mmHub) Close(ctx context.Context, _ string, n int, _ quests.CloseReason) error {
	closed := "closed"
	return h.rest().UpdateIssue(ctx, mmRepo, n, github.IssueEdit{State: &closed})
}

func (h *mmHub) IssueBody(ctx context.Context, _ string, n int) (string, error) {
	d, err := h.rest().IssueData(ctx, mmRepo, n)
	return d.Body, err
}

// mmOwner says an issue is the table's when the engine registered it for it.
type mmOwner struct{ m *mm }

func (o mmOwner) Owns(ctx context.Context, workspace, table string, issue int) (bool, error) {
	obj, ok, err := o.m.objs.Get(ctx, mmRepo, github.KindIssue, issue)
	return ok && obj.Table == workspace+"/"+table, err
}

type mmClients struct{ gh *fakegithub.Server }

func (c mmClients) For(_ context.Context, ch characters.Character, _ string) (github.PullAPI, error) {
	app, tok := "mestre", "tok_mestre"
	if ch.Role == characters.RoleGrandMaster {
		app, tok = "grand_master", "tok_gm"
	}
	return &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(c.gh)},
		Token: func(context.Context, string) (string, error) { return tok, nil }, App: app}, nil
}

type mmRepos struct{}

func (mmRepos) Repo(context.Context, string) (string, error) { return mmRepo, nil }

type mmTables struct{ w *commWorld }

func (t mmTables) Epic(ctx context.Context, key string) (github.TableRef, bool, error) {
	ws, id, _ := strings.Cut(key, "/")
	tb, err := t.w.orc.Tables.Get(ctx, ws, id)
	if err != nil {
		return github.TableRef{}, false, nil
	}
	return github.TableRef{Repo: mmRepo, Epic: tb.Epic, Branch: tb.Branch}, true, nil
}

type mmPRs struct{ w *commWorld }

func (p mmPRs) PRNumber(ctx context.Context, key string) (int, bool, error) {
	ws, id, _ := strings.Cut(key, "/")
	tb, err := p.w.orc.Tables.Get(ctx, ws, id)
	if err != nil || tb.PRNumber == 0 {
		return 0, false, nil
	}
	return tb.PRNumber, true, nil
}

type mmLease struct{}

func (mmLease) Allow() error { return nil }
func (mmLease) Do(ctx context.Context, _ string, fn func(context.Context) error) (bool, error) {
	return false, fn(ctx)
}

type mmBranches struct{ m *mm }

func (b mmBranches) Prepare(ctx context.Context, _ string) (github.Branch, error) {
	repo, err := b.m.git.Open(ctx, b.m.mirror)
	if err != nil {
		return github.Branch{}, err
	}
	head, err := repo.ResolveRef(ctx, b.m.branch)
	if err != nil {
		return github.Branch{}, err
	}
	return github.Branch{Repo: repo, Name: b.m.branch, Base: "main", BaseBranch: "main", Head: string(head), Remote: b.m.remote}, nil
}

type mmAuth struct{}

func (mmAuth) PushAuth(context.Context, string, bool) (gitx.Auth, error) {
	return gitx.BasicAuth("https://github.com/", "x-access-token", "ghs_pushTokenOfTheTestRig0123456789"), nil
}

type mmWorkflows struct{}

func (mmWorkflows) Policy(context.Context, string) (classes.Workflows, error) {
	return classes.WorkflowsNever, nil
}
func (mmWorkflows) Approved(context.Context, string, string, string) (bool, int64, error) {
	return false, 0, nil
}

// --- the helpers of the scenes

func (w *commWorld) questFile(name, def string) string {
	path := "/work/" + name + ".yaml"
	w.files[path] = []byte(def)
	return path
}

// createQuest is `mestre quest create` by the table master, with the definition in a file.
func (w *commWorld) createQuest(name, def string) result {
	return w.run(w.master, "quest", "create", name, "--file", w.questFile(strings.ReplaceAll(name, " ", "-"), def), "--json")
}

func (w *commWorld) questNamed(name string) quests.Quest {
	w.t.Helper()
	qs, err := w.orc.Quests.Store().OfTable(w.ctx, w.slug, w.table.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, q := range qs {
		if q.Name == name {
			return q
		}
	}
	w.t.Fatalf("no quest %q", name)
	return quests.Quest{}
}

func (w *commWorld) stepCharacter(q quests.Quest, step string) characters.Character {
	w.t.Helper()
	st, err := w.orc.Quests.Store().Step(w.ctx, q.ID+"/"+step)
	if err != nil {
		w.t.Fatal(err)
	}
	c, err := w.orc.Registry.Get(w.ctx, st.Character)
	if err != nil {
		w.t.Fatalf("the character of %s: %v", step, err)
	}
	return c
}

// stepDone is the done of the character of a step, with the proofs it gives; its turn ends after.
func (w *commWorld) stepDone(q quests.Quest, step string, proofs ...string) {
	w.t.Helper()
	c := w.stepCharacter(q, step)
	args := []string{"done", "feito", "--json"}
	for _, p := range proofs {
		args = append(args, "--proof", p)
	}
	if r := w.run(c, args...); r.code != 0 {
		w.t.Fatalf("done of %s in %s = %+v", step, q.Name, r)
	}
	w.mach.Report(w.ctx, c.ID, characters.Idle)
}

func (w *commWorld) issueNumber(r result) int {
	w.t.Helper()
	v := decodeJSON(w.t, r)
	n, ok := v.Data["number"].(float64)
	if !v.OK || !ok {
		w.t.Fatalf("issue create = %+v", r)
	}
	return int(n)
}

func (w *commWorld) tableState() tables.State {
	w.t.Helper()
	tb, err := w.orc.Tables.Get(w.ctx, w.slug, w.table.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return tb.State
}

const mmCriteria = "\n## Critérios\n- [ ] C1 aceita a senha correta\n- [ ] C2 recusa a senha vazia\n- [ ] C3 bloqueia depois de cinco erros\n"

// CA-MM-09
func TestTheTableMasterMakesTheBugASubIssueOfTheEpicAndTheMissionTakesItWhileTheTableStaysInReview(t *testing.T) {
	t.Parallel()
	m := &mm{}
	w := newCommWorld(t, m.setup)
	if _, err := w.orc.Tables.PROpened(w.ctx, w.slug, w.table.ID, 7, "abc"); err != nil {
		t.Fatal(err)
	}
	if got := w.tableState(); got != tables.StateInReview {
		t.Fatalf("the table is %s, want in_review", got)
	}

	// the bug as the user wrote it, passed on by the Grand Master; the issue keeps it byte for byte
	original := "O login aceita senha vazia.\n\n  Só acontece com e-mail — não com OAuth.\r\nObrigado!\n"
	n := w.issueNumber(w.run(w.master, "issue", "create", "--title", "Bug: senha vazia", "--body", original+mmCriteria, "--json"))
	title, body, state, _, ok := m.gh.Issue(mmRepo, n)
	if !ok || title != "Bug: senha vazia" || state != "open" || !strings.HasPrefix(body, original) || !strings.Contains(body, "- [ ] C1 aceita a senha correta") ||
		!strings.Contains(body, "- [ ] C3 bloqueia depois de cinco erros") {
		t.Fatalf("the issue on GitHub: %q %q %q", title, state, body)
	}
	if parent := m.gh.Parent(mmRepo, n); parent != w.table.Epic {
		t.Errorf("the issue is under #%d, want the epic #%d", parent, w.table.Epic)
	}
	obj, found, err := m.objs.Get(w.ctx, mmRepo, github.KindIssue, n)
	if err != nil || !found || obj.Table != w.slug+"/"+w.table.ID {
		t.Errorf("the issue is registered as %+v (%v, %v), want the table", obj, found, err)
	}

	// the mission takes that issue and makes no other; the table stays in review until it starts
	def := fmt.Sprintf("name: Correção do bug\nissue: %d\nsteps:\n  - {id: dev, sheet: Dev Claude, instructions: \"Corrija\"}\n", n)
	if r := w.createQuest("Correção do bug", def); r.code != 0 {
		t.Fatalf("quest create = %+v", r)
	}
	if got := w.tableState(); got != tables.StateInReview {
		t.Errorf("after the mission was made the table is %s, want in_review", got)
	}
	if r := w.run(w.master, "quest", "run", "Correção do bug", "--json"); r.code != 0 {
		t.Fatalf("quest run = %+v", r)
	}
	if got := w.tableState(); got != tables.StateWorking {
		t.Errorf("after the mission began the table is %s, want working", got)
	}
	if q := w.questNamed("Correção do bug"); q.Issue != n || m.hub.created != 0 {
		t.Errorf("the mission has the issue #%d and %d issues were made for it; want #%d and none", q.Issue, m.hub.created, n)
	}

	// an issue of another table is not one the mission may take
	other := m.gh.AddIssue(mmRepo, "de outra mesa")
	if err := m.objs.Register(w.ctx, github.Object{Repo: mmRepo, Kind: github.KindIssue, Number: other, Table: w.slug + "/43-outra", Op: github.NewOp()}); err != nil {
		t.Fatal(err)
	}
	r := w.createQuest("Roubo", fmt.Sprintf("name: Roubo\nissue: %d\nsteps:\n  - {id: dev, sheet: Dev Claude}\n", other))
	if v := decodeJSON(t, r); r.code != 3 || v.Error.Code != "outside_epic" {
		t.Errorf("a mission on the issue of another table = %d %+v, want exit 3 outside_epic", r.code, v.Error)
	}
}

// CA-MM-10
func TestThePullRequestWaitsForTheProofOfEveryCriterionAndTheFourthFixMissionIsAskedToTheGrandMaster(t *testing.T) {
	t.Parallel()
	m := &mm{}
	w := newCommWorld(t, m.setup)
	m.work()
	n := w.issueNumber(w.run(w.master, "issue", "create", "--title", "Login", "--body", "Login com senha."+mmCriteria, "--json"))
	def := fmt.Sprintf("name: Login\nissue: %d\nsteps:\n  - {id: dev, sheet: Dev Claude}\n  - {id: rev, sheet: Revisor Codex, after: [dev]}\n", n)
	if r := w.createQuest("Login", def); r.code != 0 {
		t.Fatalf("quest create = %+v", r)
	}
	if r := w.run(w.master, "quest", "run", "Login", "--json"); r.code != 0 {
		t.Fatalf("quest run = %+v", r)
	}
	q := w.questNamed("Login")
	w.stepDone(q, "dev")
	w.stepDone(q, "rev", "C1=login_test.go:TestCorreta", "C2=login_test.go:TestVazia") // two of the three
	if got := w.questNamed("Login"); got.State != quests.Completed {
		t.Fatalf("the mission is %s, want completed", got.State)
	}

	// the engine refuses the pull request and sends nothing
	r := w.run(w.master, "pr", "open", "--title", "Login", "--json")
	if v := decodeJSON(t, r); r.code != 7 || v.Error.Code != "criteria_unproven" || !strings.Contains(v.Error.Message, "C3") ||
		strings.Contains(v.Error.Message, "C1") || strings.Contains(v.Error.Message, "C2") {
		t.Fatalf("pr open with C3 unproved = %d %+v, want exit 7 criteria_unproven citing only C3", r.code, v.Error)
	}
	if m.pushed() {
		t.Error("the branch was sent although a criterion has no proof")
	}

	// the fix mission, with the same issue and only the step that is missing
	if r := w.run(w.master, "issue", "comment", fmt.Sprint(n), "Aceite: C3 sem prova; missão de correção criada.", "--json"); r.code != 0 {
		t.Fatalf("issue comment = %+v", r)
	}
	fix := "name: %s\nissue: %d\nsteps:\n  - {id: rev, sheet: Revisor Codex}\n"
	if r := w.createQuest("Correção 1", fmt.Sprintf(fix, "Correção 1", n)); r.code != 0 {
		t.Fatalf("fix mission = %+v", r)
	}
	if r := w.run(w.master, "quest", "run", "Correção 1", "--json"); r.code != 0 {
		t.Fatalf("fix run = %+v", r)
	}
	w.stepDone(w.questNamed("Correção 1"), "rev", "C3=login_test.go:TestBloqueio")

	r = w.run(w.master, "pr", "open", "--title", "Login", "--json")
	v := decodeJSON(t, r)
	if r.code != 0 || !v.OK {
		t.Fatalf("pr open with every criterion proved = %d %s", r.code, r.stdout)
	}
	pr := int(v.Data["number"].(float64))
	pull, err := m.hub.rest().PullData(w.ctx, mmRepo, pr)
	if err != nil {
		t.Fatal(err)
	}
	head := pull.HeadSHA
	if r := w.run(w.master, "pr", "update", "--block", "criterios", "C1, C2 e C3 provados (aceite do Mestre da mesa).", "--expect-head", head, "--json"); r.code != 0 {
		t.Fatalf("pr update --block criterios = %+v", r)
	}

	// the result of the acceptance: in the comment of the issue and in the block of the pull request;
	// the two old marks stay, and the third is added
	if cs := m.gh.Comments(mmRepo, n); len(cs) == 0 || !strings.Contains(strings.Join(cs, "\n"), "Aceite: C3 sem prova") {
		t.Errorf("the comments of the issue: %q", cs)
	}
	if body := m.gh.PullBody(mmRepo, pr); !strings.Contains(body, "<!-- mestre:bloco=criterios -->") || !strings.Contains(body, "C1, C2 e C3 provados") {
		t.Errorf("the body of the pull request: %q", body)
	}
	_, issue, _, _, _ := m.gh.Issue(mmRepo, n)
	for _, want := range []string{"- [x] C1 aceita a senha correta", "- [x] C2 recusa a senha vazia", "- [x] C3 bloqueia depois de cinco erros",
		"prova (declarada por", "login_test.go:TestCorreta", "login_test.go:TestBloqueio"} {
		if !strings.Contains(issue, want) {
			t.Errorf("the issue lacks %q:\n%s", want, issue)
		}
	}
	if !m.pushed() {
		t.Error("the branch was not sent")
	}

	// the fourth fix mission is not made: the Master of the table asks the Grand Master
	for i := 2; i <= 3; i++ {
		if r := w.createQuest(fmt.Sprintf("Correção %d", i), fmt.Sprintf(fix, fmt.Sprintf("Correção %d", i), n)); r.code != 0 {
			t.Fatalf("fix mission %d = %+v", i, r)
		}
	}
	r = w.createQuest("Correção 4", fmt.Sprintf(fix, "Correção 4", n))
	if v := decodeJSON(t, r); r.code != 7 || v.Error.Code != "correction_limit" || !strings.Contains(v.Error.Hint, `mestre ask "Grão-Mestre"`) {
		t.Fatalf("the fourth fix mission = %d %+v, want exit 7 correction_limit", r.code, v.Error)
	}
	if qs, _ := w.orc.Quests.Store().OfTable(w.ctx, w.slug, w.table.ID); len(qs) != 4 {
		t.Errorf("the table has %d missions, want 4 (the first and three fixes)", len(qs))
	}
	const ask = "A quarta correção do C3 não coube: o que falta e por quê."
	if r := w.run(w.master, "ask", "Grão-Mestre", ask, "--no-wait", "--json"); r.code != 0 {
		t.Fatalf("ask the Grand Master = %+v", r)
	}
	items, err := w.orc.Inbox.Store().All(w.ctx, w.gm.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range items {
		found = found || strings.Contains(it.Text, ask)
	}
	if !found {
		t.Error("the Grand Master did not receive the escalation")
	}
}
