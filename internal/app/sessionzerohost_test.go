package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/internal/vault"
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
		if err != nil || rq.Type != classes.RequestAgentsMD || !strings.Contains(rq.About, text) {
			t.Fatalf("request = %+v, %v; want the proposal with its whole text", rq, err)
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
	pub := szPullRequests{r: w.run, orc: w.orc}
	first, err := pub.Publish(w.ctx, pr)
	if err != nil || first == "" {
		t.Fatalf("Publish = %q, %v", first, err)
	}
	again, err := pub.Publish(w.ctx, pr) // the branch is rebuilt and sent again: still one pull request
	if err != nil || again != first {
		t.Fatalf("second Publish = %q, %v; want the same pull request %q", again, err, first)
	}
	if p := w.remote.Pushes(); len(p) != 2 || !strings.HasSuffix(p[0], sessionzero.PRBranch+":refs/heads/"+sessionzero.PRBranch+" force") {
		t.Errorf("pushes = %q, want the branch of the session zero, twice", p)
	}
	if _, open, err := openSessionZeroPR(w); err != nil || !open {
		t.Errorf("open PR = %v, %v", open, err)
	}
	if sig, err := pub.Author(w.ctx); err != nil || sig.Name == "" || sig.Email == "" {
		t.Errorf("author = %+v, %v; want the identity of the App Grão-Mestre", sig, err)
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
	if err != nil || !strings.Contains(said, "nenhum workspace") {
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
