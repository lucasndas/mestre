package app

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/testkit/fakegit"
	"github.com/mestre-ia/mestre/internal/testkit/fakegithub"
	"github.com/mestre-ia/mestre/internal/testkit/orcfakes"
	"github.com/mestre-ia/mestre/internal/vault"
)

// ghCompose is the standard integration over the double of GitHub, with the
// Apps "Grão-Mestre" and "Mestre" ready (with the prefix of the Owner in their
// names), the real orchestration of the Citadel and one table with its PR.
type ghCompose struct {
	*integ
	gh     *fakegithub.Server
	run    *stdRun
	orc    *Orchestration
	remote *fakegit.Git
	rdir   string
	ws     workspaces.Workspace
	table  tables.Table
	master characters.Character
	pr     int
	// machines are the machines of the orchestration (their start can be made to check).
	machines *orcfakes.Machines
	gm       characters.Character
	// root is the folder of the test (the workspaces are in root/workspaces).
	root string
}

func newGHCompose(t *testing.T) *ghCompose { return newGHComposeWith(t, false) }

// newGHComposeWith can put the chain of approval in the orchestration.
func newGHComposeWith(t *testing.T, withChain bool) *ghCompose {
	return newGHComposeCfg(t, withChain, false)
}

// newGHComposeCfg is newGHComposeWith that can leave the people and the channel of the
// inbox to the integration, as `mestre run` does (no double of them).
func newGHComposeCfg(t *testing.T, withChain, integrationPeople bool) *ghCompose {
	t.Helper()
	i := newInteg(t, false)
	double, err := fakegithub.New(i.clk, t)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(double)
	t.Cleanup(srv.Close)
	key := testRSAKey(t) // one for the binary: generating a key is the slow part
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	apps := &github.Registry{DB: i.db, Clock: i.clk}
	for n, a := range []github.App{{Name: "lucas-mestre-grao-mestre", Role: github.RoleGrandMaster}, {Name: "lucas-mestre-mestre", Role: github.RoleMestre}} {
		a.Slug = github.Slug(a.Name)
		if _, err := apps.Request(i.ctx, a); err != nil {
			t.Fatal(err)
		}
		appID, inst := int64(1+n), int64(10+n)
		if err := apps.Created(i.ctx, a.Name, a.Slug, appID, "Iv1."+a.Slug); err != nil {
			t.Fatal(err)
		}
		if err := apps.BotID(i.ctx, a.Name, int64(100+n)); err != nil {
			t.Fatal(err)
		}
		if err := apps.Installed(i.ctx, a.Name, inst, []string{"lucas/meu_app"}); err != nil {
			t.Fatal(err)
		}
		double.AddApp(a.Slug, appID, inst, &key.PublicKey, false, "lucas/meu_app")
		double.AddBot(a.Slug, int64(100+n))
		if err := i.vault.Put(i.ctx, github.KeySecret(a.Name), vault.TypeGitHubAppKey, pemKey, nil); err != nil {
			t.Fatal(err)
		}
	}
	double.AddRepo("lucas", "meu_app")
	i.std.GitHubBase, i.std.GitHubWeb, i.std.HTTP = srv.URL, srv.URL, &http.Client{Timeout: 10 * time.Second}

	c := &ghCompose{integ: i, gh: double, run: i.open()}
	t.Cleanup(c.run.Close)
	root := t.TempDir()
	c.root = root
	c.remote = fakegit.NewGit(i.clk)
	remote, err := c.remote.Init(i.ctx, filepath.Join(root, "remote", "meu-app"), gitx.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.rdir = remote.Dir()
	c.remote.Handle(c.rdir).Data().Commit("main", "chore: initial commit", map[string]string{"README.md": "# Meu App\n"})
	machines := orcfakes.NewMachines(i.clk)
	c.machines = machines
	machines.Chars = func() *characters.Service { return c.orc.Characters }
	o := OrchestrationOptions{Guild: i.db, Machine: "cidadela", Clock: i.clk, WorkspacesDir: filepath.Join(root, "workspaces"), TempDir: filepath.Join(root, "tmp"),
		Git: c.remote, Machines: machines, Presets: orcfakes.Presets{Persistent: map[string]bool{"Dev Claude": true}}, Builder: orcfakes.Builder{}, GitHub: orcfakes.Hub{URL: c.rdir},
		Epics: orcfakes.Epics{}, Alerts: orcfakes.Alerts{}, Files: orcfakes.Files{}, Messages: orcfakes.Messages{}, Peers: &orcfakes.Peers{Root: filepath.Join(root, "towers")},
		Users: &orcfakes.Users{}, Destructive: orcfakes.Destructive{}}
	if integrationPeople {
		o.Users = nil
	}
	if withChain {
		o.ChainClasses = stubClasses{}
	}
	c.run.Extend(&o)
	if c.orc, err = NewOrchestration(o); err != nil {
		t.Fatal(err)
	}
	machines.Confirm = c.orc.Inbox.Confirmed

	ws, err := c.orc.Workspaces.Create(i.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range workspaces.Stages {
		if st != workspaces.StageMirror {
			if err := c.orc.Workspaces.Register(st, func(context.Context, workspaces.Workspace) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := c.orc.Workspaces.Prepare(i.ctx, ws.Slug); err != nil {
		t.Fatal(err)
	}
	if c.ws, err = c.orc.Workspaces.Get(i.ctx, ws.Slug); err != nil {
		t.Fatal(err)
	}
	gm, err := c.orc.Characters.Create(i.ctx, characters.CreateRequest{Sheet: "Grão-Mestre", Workspace: ws.Slug, Role: characters.RoleGrandMaster, Creator: characters.Creator{Kind: characters.CreatedByEngine}})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := c.orc.Tables.Open(i.ctx, tables.OpenRequest{By: gm.Character, Epic: double.AddIssue("lucas/meu_app", "Login de usuários", "epico")})
	if err != nil {
		t.Fatal(err)
	}
	c.table, c.master, c.gm = opened.Table, opened.Master, gm.Character
	machines.Report(i.ctx, c.master.ID, characters.Idle)
	machines.Report(i.ctx, gm.Character.ID, characters.Idle)
	// the PR of the table, opened after the workspace
	i.clk.Advance(time.Minute)
	c.pr = double.AddPR("lucas/meu_app", "Login", c.table.Branch, "lucas/meu_app")
	if _, err := c.orc.Tables.PROpened(i.ctx, ws.Slug, c.table.ID, c.pr, "head"); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTheStandardIntegrationGivesTheGateTheRulesOfTheBaseBranchAsTheGrandMaster(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	var o OrchestrationOptions
	c.run.Extend(&o)
	if o.FreshnessRules == nil {
		t.Fatal("the gate of freshness has no rules to read")
	}
	rules, err := o.FreshnessRules.ReadRules(c.ctx, c.ws.Slug, "lucas/Meu_App", "main")
	if err != nil || rules.UpToDate {
		t.Fatalf("rules = %+v, %v; want none that ask the branch to be up to date", rules, err)
	}
	c.gh.SetBranchRules("lucas/meu_app", "main", fakegithub.Rule{Type: "required_status_checks", Parameters: map[string]any{"strict_required_status_checks_policy": true}})
	if rules, err = o.FreshnessRules.ReadRules(c.ctx, c.ws.Slug, "lucas/Meu_App", "main"); err != nil || !rules.UpToDate {
		t.Fatalf("rules = %+v, %v; want the rule 'branch atualizada'", rules, err)
	}
	if c.orc.Gate != nil {
		t.Error("the orchestration made a gate with no chain of approval to gate")
	}
}

func TestTheEventsOfGitHubReachTheGrandMasterAndAChangedPRWakesTheStepSync(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	poll, err := c.run.poller(c.orc)
	if err != nil {
		t.Fatal(err)
	}
	// the base moves and nobody has noticed: the branch of the table is behind it
	c.remote.Handle(c.rdir).Data().Commit("main", "feat: other", map[string]string{"other.txt": "x\n"})
	mirror, err := c.orc.Workspaces.OpenMirror(c.ctx, c.ws.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if err := mirror.Fetch(c.ctx, "origin", workspaces.FetchRefspec); err != nil {
		t.Fatal(err)
	}
	// somebody changes the PR on GitHub
	c.clk.Advance(time.Minute)
	c.gh.AddIssueComment("lucas/meu_app", c.pr, "ana", "olha o titulo")
	if err := poll.Tick(c.ctx); err != nil {
		t.Fatal(err)
	}
	gm, _, err := c.orc.Registry.GrandMaster(c.ctx, c.ws.Slug)
	if err != nil {
		t.Fatal(err)
	}
	var fromGitHub, system []string
	for _, id := range []string{gm.ID, c.master.ID} {
		items, err := c.orc.Inbox.Store().All(c.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			switch it.Kind {
			case inbox.KindGitHub:
				fromGitHub = append(fromGitHub, it.Text)
			case inbox.KindSystem:
				system = append(system, it.Text)
			}
		}
	}
	if len(fromGitHub) == 0 || !strings.Contains(strings.Join(fromGitHub, "\n"), "comentário") {
		t.Fatalf("the Grand Master got %q from GitHub; want the comment", fromGitHub)
	}
	if !strings.Contains(strings.Join(system, "\n"), "andou") {
		t.Fatalf("system items = %q; want the table told the base moved (the step sync was asked by the event)", system)
	}
}

// Part of CA-DEPMESA-05: an epic closed straight on GitHub is read by the poller of the
// standard integration and tells the dependencies: closed as completed it releases
// the epic that waited for it, closed as not planned it keeps it held for the Grand
// Master to decide.
func TestAnEpicClosedOnGitHubReachesTheDependenciesThroughThePoller(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	poll, err := c.run.poller(c.orc)
	if err != nil {
		t.Fatal(err)
	}
	gm, _, err := c.orc.Registry.GrandMaster(c.ctx, c.ws.Slug)
	if err != nil {
		t.Fatal(err)
	}
	const repo = "lucas/meu_app"
	c.clk.Advance(time.Minute)
	done, dropped := c.gh.AddIssue(repo, "Login", "epico"), c.gh.AddIssue(repo, "Busca", "epico")
	waitsDone, waitsDropped := c.gh.AddIssue(repo, "Checkout", "epico"), c.gh.AddIssue(repo, "Painel", "epico")
	for _, d := range [][2]int{{waitsDone, done}, {waitsDropped, dropped}} {
		if _, err := c.orc.EpicDeps.Depend(c.ctx, gm, d[0], d[1]); err != nil {
			t.Fatal(err)
		}
		if held, err := c.orc.EpicDeps.Held(c.ctx, c.ws.Slug, d[0]); err != nil || !held {
			t.Fatalf("epic #%d waiting for #%d: held = %v, %v", d[0], d[1], held, err)
		}
	}
	c.clk.Advance(time.Minute)
	c.gh.CloseIssueAs(repo, done, "completed")
	c.gh.CloseIssueAs(repo, dropped, "not_planned")
	if err := poll.Tick(c.ctx); err != nil {
		t.Fatal(err)
	}
	if held, _ := c.orc.EpicDeps.Held(c.ctx, c.ws.Slug, waitsDone); held {
		t.Errorf("the epic #%d is still held after the one it waited for was closed as completed", waitsDone)
	}
	if held, _ := c.orc.EpicDeps.Held(c.ctx, c.ws.Slug, waitsDropped); !held {
		t.Errorf("the epic #%d was released by one closed as not planned: it stays held for the Grand Master", waitsDropped)
	}
}

func TestWhatThePollerReadsWithStopsWorkingWhenThePeriodEnds(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	rest := c.run.gmRest("", pollPermissions)
	if tok, err := rest.Token(c.ctx, "lucas/meu_app"); err != nil || tok == "" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	c.run.Close()
	if tok, err := rest.Token(c.ctx, "lucas/meu_app"); err == nil || tok != "" {
		t.Fatalf("token after the period ended = %q, %v; want it refused", tok, err)
	}
}

func TestAMergeIsConfirmedAgainstTheBranchOfTheBaseOfTheMirror(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	m := ghMerges{c.orc}
	mirror, err := c.orc.Workspaces.OpenMirror(c.ctx, c.ws.Slug)
	if err != nil {
		t.Fatal(err)
	}
	head, err := mirror.ResolveRef(c.ctx, "refs/remotes/origin/main")
	if err != nil {
		t.Fatal(err)
	}
	w := github.Watch{Workspace: c.ws.Slug}
	if ok, err := m.Reachable(c.ctx, w, string(head)); err != nil || !ok {
		t.Fatalf("reachable = %v, %v; want the tip of the default branch reachable", ok, err)
	}
	if ok, err := m.Reachable(c.ctx, w, strings.Repeat("0", 40)); err == nil && ok {
		t.Fatal("a commit that is nowhere is reachable")
	}
}

func TestThePersonalCredentialsAndTheInstallationsAreMadeByTheIntegration(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	if c.run.Personal() == nil || c.run.Installations() == nil {
		t.Fatal("the integration does not give the personal credentials and the installations")
	}
	// the fixed Apps are asked for by role: their names carry the prefix of the Owner
	missing, err := c.run.installs.Missing(c.ctx, "lucas/Meu_App", []string{github.RoleGrandMaster, github.RoleMestre})
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing = %v, %v; want none: both Apps cover the repository", missing, err)
	}
	missing, err = c.run.installs.Missing(c.ctx, "lucas/outro", []string{github.RoleGrandMaster, github.RoleMestre})
	if err != nil || len(missing) != 2 || missing[0] != "lucas-mestre-grao-mestre" {
		t.Fatalf("missing = %v, %v; want both, by name", missing, err)
	}
	// no Owner credential: the API is not tried, the link is the way
	if _, err := c.run.Installations().Missing(c.ctx, workspaces.Repo{Owner: "lucas", Name: "outro"}); err != nil {
		t.Fatal(err)
	}
}

// stubClasses gives every character the same class: it asks for what it does not
// list. The chain of approval needs it to decide who may decide.
type stubClasses struct{}

const stubYAML = "version: 1\nfiles:\n  read: [\"**\"]\n  write: [\"**\"]\ncommands:\n  allow: [\"npm test\"]\n  default: ask\nnetwork:\n  domains: [\"registry.npmjs.org\"]\ntools:\n  default: ask\n"

func (stubClasses) Rules(_ context.Context, id string) (*classes.Rules, error) {
	p, err := classes.ParsePermissions([]byte(stubYAML))
	if err != nil {
		return nil, err
	}
	return classes.Build(classes.Layers{AgentID: id, Class: "dev", Permissions: p, Now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)})
}

func (stubClasses) Env(_ context.Context, id string) (classes.Env, error) {
	return classes.Env{AgentID: id, Worktree: "/work/42-login", OS: "linux"}, nil
}

func (stubClasses) Domains(context.Context, string) ([]string, error) {
	return []string{"registry.npmjs.org"}, nil
}

// Part of CA-CLI-GH-05 (the command and the exit 3 are in internal/ghcli): the
// refusal of the sending that the standard integration hands to the handlers
// reaches the inbox of the Grand Master of the workspace, with the code and the
// table, and nobody else's.
func TestTheRefusalOfTheSendingOfAGitHubReachesTheInboxOfTheGrandMaster(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	gm, _, err := c.orc.Registry.GrandMaster(c.ctx, c.ws.Slug)
	if err != nil {
		t.Fatal(err)
	}
	master, err := c.orc.Registry.Get(c.ctx, c.master.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.run.ghHandlers(c.orc).Refused(c.ctx, master, "workflow_permission", "o App Mestre não tem a permissão Workflows")
	system := func(id string) []string {
		items, err := c.orc.Inbox.Store().All(c.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range items {
			if it.Kind == inbox.KindSystem && strings.Contains(it.Text, "workflow_permission") {
				out = append(out, it.Text)
			}
		}
		return out
	}
	got := system(gm.ID)
	if len(got) != 1 || !strings.Contains(got[0], master.Table) || !strings.Contains(got[0], "Workflows") {
		t.Fatalf("the Grand Master got %q, want the refusal with the table and the reason", got)
	}
	if other := system(master.ID); len(other) != 0 {
		t.Errorf("the table master got the word meant for the Grand Master: %q", other)
	}
}

// Part of CA-SYNC-02: the hook that the standard integration gives to `pr open`
// and `pr update` takes the table in work to review and keeps its PR.
func TestThePRCommandsTellTheTableAndATableInWorkGoesToReview(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	if _, err := c.orc.Tables.MissionStarted(c.ctx, c.ws.Slug, c.table.ID); err != nil {
		t.Fatal(err)
	}
	if now, err := c.orc.Tables.Get(c.ctx, c.ws.Slug, c.table.ID); err != nil || now.State != tables.StateWorking {
		t.Fatalf("the table is %+v (%v), want working", now.State, err)
	}
	if err := c.run.ghHandlers(c.orc).Opened(c.ctx, c.ws.Slug+"/"+c.table.ID, c.pr, "newhead"); err != nil {
		t.Fatal(err)
	}
	now, err := c.orc.Tables.Get(c.ctx, c.ws.Slug, c.table.ID)
	if err != nil || now.State != tables.StateInReview || now.PRNumber != c.pr || now.PRHead != "newhead" {
		t.Fatalf("the table is %s with PR #%d head %q (%v); want in_review with the new head", now.State, now.PRNumber, now.PRHead, err)
	}
	if err := c.run.ghHandlers(c.orc).Opened(c.ctx, "no-slash", 1, ""); err == nil {
		t.Error("a key that is no table was taken")
	}
}

// A table that can not take its PR (it is closing) does not turn `pr open` or
// `pr update` into a failure: the PR is on GitHub already, so the hook tells the
// log and the command stands.
func TestATableThatCannotTakeItsPRDoesNotFailThePRCommands(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	if _, err := c.db.Writer().ExecContext(c.ctx, `UPDATE tables SET state = 'closing' WHERE workspace = ? AND id = ?`, c.ws.Slug, c.table.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.run.ghHandlers(c.orc).Opened(c.ctx, c.ws.Slug+"/"+c.table.ID, c.pr, "h"); err != nil {
		t.Fatalf("a closing table failed the command: %v", err)
	}
}
