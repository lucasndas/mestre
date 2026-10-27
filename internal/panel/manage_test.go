package panel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/accounts"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/orchestration/usage"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/users"
)

// fakeClasses is the engine side of the classes screen.
type fakeClasses struct {
	mu       sync.Mutex
	approved []panel.ApproveProposalRequest
	err      error
}

func (f *fakeClasses) Classes(_ context.Context, ws []string, all bool) ([]panel.ClassRow, []panel.ProposalRow, error) {
	return []panel.ClassRow{{Name: "revisor", Scope: "workspace", Workspace: "loja", State: "approved", App: "novo", AppState: "em criação", Problems: []string{}}},
		[]panel.ProposalRow{{ID: 7, Workspace: "loja", Name: "qa", Scope: "workspace", Origin: "disk", Hash: "abc", Problems: []string{}}}, nil
}

func (f *fakeClasses) Approve(_ context.Context, ws string, id int64, hash string, _ users.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.approved = append(f.approved, panel.ApproveProposalRequest{Workspace: ws, Hash: hash})
	return nil
}

type fakeProfiles struct {
	mu   sync.Mutex
	list []accounts.Profile
}

func (f *fakeProfiles) Profiles(context.Context) ([]accounts.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]accounts.Profile(nil), f.list...), nil
}

func (f *fakeProfiles) Add(_ context.Context, name string, p providers.ID) (accounts.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr := accounts.Profile{Name: name, Provider: p, Dir: "/contas/" + name, Default: len(f.list) == 0, Step: accounts.StepInstall}
	f.list = append(f.list, pr)
	return pr, nil
}
func (f *fakeProfiles) Remove(context.Context, string) error     { return accounts.ErrUnknown }
func (f *fakeProfiles) SetDefault(context.Context, string) error { return nil }
func (f *fakeProfiles) SetupAvailable() bool                     { return false }
func (f *fakeProfiles) Advance(context.Context, string) (accounts.Status, error) {
	return accounts.Status{}, accounts.ErrNoProfile
}
func (f *fakeProfiles) Providers() []string { return []string{"claude-code", "codex"} }

type manageRig struct {
	*adminRig
	apps    *github.Registry
	classes *fakeClasses
	cat     *presets.Catalog
}

func newManage(t *testing.T) *manageRig {
	t.Helper()
	a := newAdmin(t)
	m := &manageRig{adminRig: a, classes: &fakeClasses{}}
	m.apps = &github.Registry{DB: a.db, Clock: a.clk}
	tokens := &github.Tokens{Apps: m.apps, Keys: a.vault, Clock: a.clk, BaseURL: "http://127.0.0.1:1"}
	creation, err := github.NewCreation(github.CreationOptions{Apps: m.apps, Tokens: tokens, Vault: a.vault, Panel: func() string { return "https://mestre.local:8443" },
		OwnerLogin: func(context.Context) (string, error) { return "lucas-gh", nil }})
	if err != nil {
		t.Fatal(err)
	}
	m.cat = &presets.Catalog{DB: a.db, Clock: a.clk}
	a.panel.Close()
	a.panel, err = panel.New(panel.Options{DB: a.db, Clock: a.clk, Users: a.users, Alerts: a.al, Assets: a.assets, Machine: "m1", Vault: a.vault, Creation: creation})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.panel.Close)
	a.panel.SetManage(func() panel.Manage {
		return panel.Manage{Classes: m.classes, Presets: m.cat, Profiles: &fakeProfiles{}}
	})
	return m
}

// CA-ADM-02: with 90 Apps in the account the classes screen shows the band; with 89
// it does not. The count is the Apps of the Mestre plus the others the Owner informed.
func TestTheClassesScreenShowsTheBandFromNinetyApps(t *testing.T) {
	t.Parallel()
	m := newManage(t)
	_, adm := m.member("Bruno", "bruno", users.Administrator)
	owner := m.ownerSession()
	get := func() panel.ClassesPage {
		w := m.call("GET", "/api/classes", nil, opts{cookie: adm})
		m.wantOK(w)
		return decodeInto[panel.ClassesPage](t, w)
	}
	if p := get(); p.Apps.Near || p.Apps.Total != 0 || len(p.Classes) != 1 || p.Classes[0].Name != "revisor" || len(p.Proposals) != 1 || p.Proposals[0].Origin != "disk" || p.Proposals[0].Tested {
		t.Fatalf("classes = %+v", p)
	}
	if _, err := m.apps.Request(m.ctx, github.App{Name: "mestre-grao-mestre", Role: github.RoleGrandMaster}); err != nil {
		t.Fatal(err)
	}
	m.wantOK(m.call("PUT", "/api/github/apps/others", panel.OthersRequest{Count: 88}, opts{cookie: owner}))
	if p := get(); p.Apps.Near || p.Apps.Total != 89 || p.Apps.Mine != 1 || p.Apps.Others != 88 {
		t.Fatalf("with 89 Apps the band is %+v", p.Apps)
	}
	m.wantOK(m.call("PUT", "/api/github/apps/others", panel.OthersRequest{Count: 89}, opts{cookie: owner}))
	if p := get(); !p.Apps.Near || p.Apps.Total != 90 || p.Apps.Limit != 100 {
		t.Fatalf("with 90 Apps the band is %+v", p.Apps)
	}
	// who may and who may not: an Administrator does not change the count; nobody lower reads the screen
	m.wantErr(m.call("PUT", "/api/github/apps/others", panel.OthersRequest{Count: 0}, opts{cookie: adm}), 403, panel.CodeForbiddenLevel)
	m.wantErr(m.call("PUT", "/api/github/apps/others", panel.OthersRequest{Count: -1}, opts{cookie: owner}), 400, panel.CodeBadRequest)
	_, obs := m.member("Olga", "olga", users.Observer)
	m.wantErr(m.call("GET", "/api/classes", nil, opts{cookie: obs}), 403, panel.CodeForbiddenLevel)
}

// Part of CA-CLASSES-06: the approval of a proposal asks the PIN, is for an Administrator or
// above, goes to the library with the hash the approver saw, and is in the audit.
func TestApprovingAProposalAsksThePINAndGoesToTheLibraryWithTheHash(t *testing.T) {
	t.Parallel()
	m := newManage(t)
	_, adm := m.member("Bruno", "bruno", users.Administrator)
	_, appr := m.member("Ana", "ana", users.Approver)
	body := panel.ApproveProposalRequest{Workspace: "loja", Hash: "abc"}
	m.wantErr(m.call("POST", "/api/classes/proposals/7/approve", body, opts{cookie: appr, pin: "654321"}), 403, panel.CodeForbiddenLevel)
	m.wantErr(m.call("POST", "/api/classes/proposals/7/approve", body, opts{cookie: adm}), 403, panel.CodePINRequired)
	m.wantErr(m.call("POST", "/api/classes/proposals/7/approve", panel.ApproveProposalRequest{Workspace: "loja"}, opts{cookie: adm, pin: "654321"}), 400, panel.CodeBadRequest)
	// a workspace the Administrator does not act in is not his to approve in
	m.wantErr(m.call("POST", "/api/classes/proposals/7/approve", panel.ApproveProposalRequest{Workspace: "outra", Hash: "abc"}, opts{cookie: adm, pin: "654321"}), 404, panel.CodeNotFound)
	if len(m.classes.approved) != 0 {
		t.Fatalf("approved without the right conditions: %v", m.classes.approved)
	}
	m.wantOK(m.call("POST", "/api/classes/proposals/7/approve", body, opts{cookie: adm, pin: "654321"}))
	if len(m.classes.approved) != 1 || m.classes.approved[0] != body {
		t.Fatalf("approved = %v", m.classes.approved)
	}
	if !strings.Contains(m.auditText(), "panel.class.approved") {
		t.Error("the approval is not in the audit")
	}
	// the package changed since the approver saw it
	m.classes.err = classes.ErrClassChanged
	m.wantErr(m.call("POST", "/api/classes/proposals/7/approve", body, opts{cookie: adm, pin: "654321"}), 409, panel.CodeProposalStale)
}

// CA-GH-APPS-04: an Administrator who opens the link of creation gets "exige Senhor da Guilda" and no
// form is made; nobody logged in is asked to enter; the Owner gets the form that goes to GitHub.
func TestOnlyTheOwnerOpensTheLinkOfCreationAndNoFormIsMadeForAnAdministrator(t *testing.T) {
	t.Parallel()
	m := newManage(t)
	_, adm := m.member("Bruno", "bruno", users.Administrator)
	owner := m.ownerSession()
	if _, err := m.apps.Request(m.ctx, github.App{Name: "mestre-dev", Role: github.RoleClass, Class: "dev"}); err != nil {
		t.Fatal(err)
	}
	// the Owner makes the link, with the PIN, and sees it once
	m.wantErr(m.call("POST", "/api/github/apps/mestre-dev/link", nil, opts{cookie: owner}), 403, panel.CodePINRequired)
	m.wantErr(m.call("POST", "/api/github/apps/mestre-dev/link", nil, opts{cookie: adm, pin: "654321"}), 403, panel.CodeForbiddenLevel)
	w := m.call("POST", "/api/github/apps/mestre-dev/link", nil, opts{cookie: owner, pin: ownerPIN})
	m.wantOK(w)
	link := decodeInto[panel.AppLinkResponse](t, w)
	const prefix = "https://mestre.local:8443"
	if !strings.HasPrefix(link.URL, prefix+"/github/apps/novo/") {
		t.Fatalf("link = %q", link.URL)
	}
	path := strings.TrimPrefix(link.URL, prefix)
	open := func(cookie string) *httptest.ResponseRecorder {
		req := m.request("GET", path, nil, opts{cookie: cookie})
		rec := httptest.NewRecorder()
		m.panel.ServeHTTP(rec, req)
		return rec
	}
	got := open(adm)
	if got.Code != http.StatusForbidden || !strings.Contains(got.Body.String(), "Exige Senhor da Guilda") || strings.Contains(got.Body.String(), "<form") {
		t.Fatalf("Administrator opening the link: %d %s", got.Code, got.Body)
	}
	if got := open(""); got.Code != http.StatusUnauthorized || strings.Contains(got.Body.String(), "<form") {
		t.Fatalf("nobody logged in: %d", got.Code)
	}
	got = open(owner)
	csp := got.Header().Get("Content-Security-Policy")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "<form") || !strings.Contains(csp, "form-action https://github.com") {
		t.Fatalf("Owner opening the link: %d csp=%q %s", got.Code, csp, got.Body)
	}
	// the screen of the Apps: the request waits for the Owner only, with "novo link"
	apps := decodeInto[panel.AppsPage](t, m.call("GET", "/api/github/apps", nil, opts{cookie: owner}))
	if len(apps.Apps) != 1 || apps.Apps[0].StateLabel != "em criação" || len(apps.Requests) != 1 || apps.Requests[0].Status != "opened" || !apps.Apps[0].CanLink {
		t.Fatalf("Apps for the Owner = %+v", apps)
	}
	if a := decodeInto[panel.AppsPage](t, m.call("GET", "/api/github/apps", nil, opts{cookie: adm})); len(a.Requests) != 0 || len(a.Apps) != 0 || a.Count.Total != 1 {
		t.Fatalf("Apps for the Administrator = %+v", a)
	}
	if strings.Contains(m.auditText(), strings.TrimPrefix(path, "/github/apps/novo/")) {
		t.Error("the token of the link is in the audit")
	}
}

const (
	baseYAML  = "name: claude-code\ncommand: claude\nplatforms:\n  - os: linux\nready: hook\n"
	sheetYAML = "name: Dev Claude\nbase: claude-code\nagent: claude-code\nlifecycle: persistent\nmax_idle: 240m\n"
)

// CA-ADM-05: a sheet exported in YAML and imported back is equal to the original; the Owner
// chooses what to do with a name that already exists; an invalid file writes nothing.
func TestASheetExportedAsYAMLImportsBackEqual(t *testing.T) {
	t.Parallel()
	m := newManage(t)
	owner := m.ownerSession()
	_, adm := m.member("Bruno", "bruno", users.Administrator)
	m.wantErr(m.call("GET", "/api/presets", nil, opts{cookie: adm}), 403, panel.CodeForbiddenLevel)
	m.wantErr(m.call("PUT", "/api/presets/bases/novo", panel.PresetSave{YAML: baseYAML}, opts{cookie: owner}), 403, panel.CodePINRequired)
	m.wantOK(m.call("PUT", "/api/presets/bases/novo", panel.PresetSave{YAML: baseYAML}, opts{cookie: owner, pin: ownerPIN}))
	m.wantOK(m.call("PUT", "/api/presets/sheets/novo", panel.PresetSave{YAML: sheetYAML}, opts{cookie: owner, pin: ownerPIN}))
	before := decodeInto[panel.PresetsPage](t, m.call("GET", "/api/presets", nil, opts{cookie: owner}))
	if len(before.Bases) != 1 || len(before.Sheets) != 1 || before.Sheets[0].Lifecycle != "persistent" {
		t.Fatalf("presets = %+v", before)
	}
	exp := m.call("GET", "/api/presets/export?format=yaml&sheets=Dev+Claude", nil, opts{cookie: owner})
	m.wantOK(exp)
	file := exp.Body.String()
	if !strings.Contains(file, "kind: mestre/presets") || !strings.Contains(exp.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("export = %q", file)
	}
	// drop the sheet and import the file into the database that lacks it
	m.wantOK(m.call("DELETE", "/api/presets/sheets/Dev%20Claude", nil, opts{cookie: owner, pin: ownerPIN}))
	plan := decodeInto[panel.ImportPlan](t, m.call("POST", "/api/presets/import/plan", panel.ImportFile{Content: file}, opts{cookie: owner}))
	if plan.Sheets != 1 || len(plan.Conflicts) != 1 || plan.Conflicts[0] != (panel.ImportItem{Kind: "base", Name: "claude-code"}) {
		t.Fatalf("plan = %+v; want the base as the only conflict", plan)
	}
	m.wantErr(m.call("POST", "/api/presets/import", panel.ImportFile{Content: file}, opts{cookie: owner, pin: ownerPIN}), 422, panel.CodePresetInvalid) // a conflict without a choice
	res := decodeInto[panel.ImportResult](t, m.call("POST", "/api/presets/import",
		panel.ImportFile{Content: file, Choices: []panel.ImportChoice{{Kind: "base", Name: "claude-code", Action: "skip"}}}, opts{cookie: owner, pin: ownerPIN}))
	if res.Added != 1 || res.Skipped != 1 {
		t.Fatalf("import = %+v", res)
	}
	after := decodeInto[panel.PresetsPage](t, m.call("GET", "/api/presets", nil, opts{cookie: owner}))
	if len(after.Sheets) != 1 || after.Sheets[0] != before.Sheets[0] || len(after.Bases) != 1 || after.Bases[0].YAML != before.Bases[0].YAML {
		t.Fatalf("after the import:\n%+v\nbefore:\n%+v", after, before)
	}
	// JSON exports the same content
	if j := m.call("GET", "/api/presets/export?format=json", nil, opts{cookie: owner}); !strings.HasPrefix(strings.TrimSpace(j.Body.String()), "{") {
		t.Errorf("json export = %q", j.Body)
	}
	// a file with an unknown field, or a newer version, writes nothing
	m.wantErr(m.call("POST", "/api/presets/import/plan", panel.ImportFile{Content: "kind: mestre/presets\nversion: 1\nbases: []\nextra: 1\n"}, opts{cookie: owner}), 422, panel.CodePresetInvalid)
	bad := m.call("POST", "/api/presets/import/plan", panel.ImportFile{Content: "kind: mestre/presets\nversion: 9\n"}, opts{cookie: owner})
	m.wantErr(bad, 422, panel.CodePresetInvalid)
	if !strings.Contains(bad.Body.String(), "1") {
		t.Errorf("a newer version does not say the one expected: %s", bad.Body)
	}
	// an invalid base is refused with the reason, and a base used by a sheet is not deleted
	m.wantErr(m.call("PUT", "/api/presets/bases/novo", panel.PresetSave{YAML: "name: ruim\ncommand: x\nenv:\n  MESTRE_AGENT_ID: x\nplatforms: [{os: linux}]\nready: hook\n"}, opts{cookie: owner, pin: ownerPIN}), 422, panel.CodePresetInvalid)
	m.wantErr(m.call("DELETE", "/api/presets/bases/claude-code", nil, opts{cookie: owner, pin: ownerPIN}), 409, panel.CodePresetInUse)
	if !strings.Contains(m.auditText(), "panel.presets.imported") {
		t.Error("the import is not in the audit")
	}
}

func TestTheProfilesAreTheOwnersAndTheSetupSaysWhenItIsNotThereYet(t *testing.T) {
	t.Parallel()
	m := newManage(t)
	owner := m.ownerSession()
	_, adm := m.member("Bruno", "bruno", users.Administrator)
	m.wantErr(m.call("GET", "/api/profiles", nil, opts{cookie: adm}), 403, panel.CodeForbiddenLevel)
	m.wantErr(m.call("POST", "/api/profiles", panel.ProfileAdd{Name: "claude-trabalho", Provider: "claude-code"}, opts{cookie: owner}), 403, panel.CodePINRequired)
	m.wantOK(m.call("POST", "/api/profiles", panel.ProfileAdd{Name: "claude-trabalho", Provider: "claude-code"}, opts{cookie: owner, pin: ownerPIN}))
	page := decodeInto[panel.ProfilesPage](t, m.call("GET", "/api/profiles", nil, opts{cookie: owner}))
	if page.Machine != "m1" || page.Setup || len(page.Profiles) != 0 && page.Profiles[0].Step != "install" {
		t.Fatalf("profiles = %+v", page)
	}
	m.wantErr(m.call("POST", "/api/profiles/claude-trabalho/advance", nil, opts{cookie: owner}), 503, panel.CodeUnavailable)
	m.wantErr(m.call("DELETE", "/api/profiles/nenhum", nil, opts{cookie: owner, pin: ownerPIN}), 404, panel.CodeNotFound)
}

type fakeLimits struct{}

func (fakeLimits) Limits(context.Context) ([]usage.Limit, error) {
	return []usage.Limit{{Scope: usage.ScopeTable, Key: "loja/mesa-1", Unit: "usd", Value: 10}, {Scope: usage.ScopeDayTotal, Key: "", Unit: "usd", Value: 50}}, nil
}
func (fakeLimits) View(_ context.Context, l usage.Limit, _ string) (usage.LimitView, error) {
	return usage.LimitView{Limit: l, Effective: l.Value, Used: 8, HasData: true, Percent: 80}, nil
}
func (fakeLimits) Today(context.Context) string { return "2026-10-26" }

// Part of CA-USO-08: the screen shows the use of the table and of each character; the
// Owner also sees the accounts and the total of the day, the others only their workspaces,
// and a datum the provider did not give is a gap, never a zero.
func TestTheUsageScreenShowsTheTableAndTheCharactersWithGapsAndByWorkspace(t *testing.T) {
	t.Parallel()
	m := newManage(t)
	m.panel.SetManage(func() panel.Manage { return panel.Manage{Usage: fakeLimits{}} })
	for _, q := range []string{
		`INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at) VALUES ('loja2', 'o/loja2', 'o/loja2', 'u', 'main', 'active', 'done', 0, 0)`,
	} {
		if _, err := m.db.Writer().ExecContext(m.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	st := &usage.Store{DB: m.db}
	cost := 2.5
	now := m.clk.Now().Add(-time.Hour)
	rec := func(ws, table, char, profile string, c *float64, tok bool) usage.Record {
		return usage.Record{Character: char, Workspace: ws, Table: table, Profile: profile, Provider: providers.ClaudeCode, At: now, Origin: usage.OriginStep, Source: "turn",
			Tokens: usage.Tokens{Input: 100, Output: 50, Known: tok}, CostUSD: c, Active: 90 * time.Second, Turns: 1}
	}
	for _, r := range []usage.Record{rec("loja", "mesa-1", "c1", "", &cost, true), rec("loja", "mesa-1", "c2", "", nil, false), rec("loja2", "mesa-9", "c9", "claude-x", &cost, true)} {
		if err := st.Insert(m.ctx, r, "2026-10-26"); err != nil {
			t.Fatal(err)
		}
	}
	owner := m.ownerSession()
	own := decodeInto[panel.UsagePage](t, m.call("GET", "/api/usage", nil, opts{cookie: owner}))
	if own.Today != "2026-10-26" || len(own.Workspaces) != 2 || len(own.Tables) != 2 || len(own.Characters) != 3 || len(own.Accounts) != 2 || len(own.Days) != 1 || len(own.Limits) != 2 {
		t.Fatalf("Owner usage = %+v", own)
	}
	var c2 *panel.UsageTotals
	for i := range own.Characters {
		if own.Characters[i].Key == "c2" {
			c2 = &own.Characters[i]
		}
	}
	if c2 == nil || c2.CostUSD != nil || c2.Tokens != nil || c2.Turns != 1 || c2.ActiveSeconds != 90 {
		t.Fatalf("character without cost nor tokens = %+v; want the gap", c2)
	}
	if l := own.Limits[0]; l.Left != 2 || l.Percent != 80 {
		t.Errorf("limit = %+v; want 2 left", l)
	}
	_, obs := m.member("Olga", "olga", users.Observer) // acts in loja only
	ob := decodeInto[panel.UsagePage](t, m.call("GET", "/api/usage", nil, opts{cookie: obs}))
	if len(ob.Workspaces) != 1 || ob.Workspaces[0].Key != "loja" || len(ob.Characters) != 2 || len(ob.Accounts) != 0 || len(ob.Limits) != 1 || ob.Limits[0].Scope != "table" {
		t.Fatalf("Observer usage = %+v; want only loja, no accounts, no total of the day", ob)
	}
	m.wantErr(m.call("GET", "/api/usage?days=0", nil, opts{cookie: owner}), 400, panel.CodeBadRequest)
}

// Each user sees only what belongs to the workspaces they act in: an Administrator of "loja"
// sees the fixed Apps with the repository of "loja" alone, and not the class App that is
// installed only on another workspace's repository; the Owner sees everything.
func TestTheAppsAreScopedToTheWorkspacesOfTheUser(t *testing.T) {
	t.Parallel()
	m := newManage(t)
	if _, err := m.db.Writer().ExecContext(m.ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at) VALUES ('outra', 'o/outra', 'o/outra', 'u', 'main', 'active', 'done', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	for _, a := range []struct {
		app   github.App
		repos []string
	}{
		{github.App{Name: "mestre-grao-mestre", Role: github.RoleGrandMaster}, []string{"o/loja", "o/outra"}},
		{github.App{Name: "mestre-dev", Role: github.RoleClass, Class: "dev"}, []string{"o/outra"}},
		{github.App{Name: "mestre-qa", Role: github.RoleClass, Class: "qa"}, []string{"o/loja"}},
	} {
		if _, err := m.apps.Request(m.ctx, a.app); err != nil {
			t.Fatal(err)
		}
		if err := m.apps.Created(m.ctx, a.app.Name, a.app.Name, 1, "cid"); err != nil {
			t.Fatal(err)
		}
		if err := m.apps.Installed(m.ctx, a.app.Name, 1, a.repos); err != nil {
			t.Fatal(err)
		}
	}
	_, adm := m.member("Bruno", "bruno", users.Administrator) // acts in loja
	names := func(p panel.AppsPage) map[string][]string {
		out := map[string][]string{}
		for _, a := range p.Apps {
			out[a.Name] = a.Repos
		}
		return out
	}
	got := names(decodeInto[panel.AppsPage](t, m.call("GET", "/api/github/apps", nil, opts{cookie: adm})))
	if len(got) != 2 || strings.Join(got["mestre-grao-mestre"], ",") != "o/loja" || strings.Join(got["mestre-qa"], ",") != "o/loja" {
		t.Fatalf("Administrator sees %v; want the fixed App and mestre-qa, both only with o/loja", got)
	}
	if all := names(decodeInto[panel.AppsPage](t, m.call("GET", "/api/github/apps", nil, opts{cookie: m.ownerSession()}))); len(all) != 3 || len(all["mestre-grao-mestre"]) != 2 {
		t.Fatalf("Owner sees %v", all)
	}
}

// The session cookie is SameSite=Strict: the return of GitHub arrives without it. The panel
// answers that request with a page that continues by itself and exchanges nothing; the same
// address, now with the session, reaches the page of the return.
func TestTheReturnOfGitHubWithoutTheCookieContinuesInThePanelAndExchangesNothing(t *testing.T) {
	t.Parallel()
	m := newManage(t)
	owner := m.ownerSession()
	get := func(path, cookie, site string) *httptest.ResponseRecorder {
		req := m.request("GET", path, nil, opts{cookie: cookie})
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		rec := httptest.NewRecorder()
		m.panel.ServeHTTP(rec, req)
		return rec
	}
	for _, path := range []string{"/github/manifest/callback?code=c&state=s", "/github/apps/instalado?installation_id=7", "/github/apps/novo/abc"} {
		w := get(path, "", "cross-site")
		body := w.Body.String()
		if w.Code != http.StatusOK || !strings.Contains(body, `http-equiv="refresh"`) || !strings.Contains(body, "Continuar") || strings.Contains(body, "<form") ||
			!strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s without the cookie: %d %v %s", path, w.Code, w.Header(), body)
		}
		// the second navigation starts in the panel: it is not continued again, and without a session it is refused
		if again := get(path, "", "same-origin"); again.Code != http.StatusUnauthorized || strings.Contains(again.Body.String(), "refresh") {
			t.Fatalf("%s again without the cookie: %d %s", path, again.Code, again.Body)
		}
		// with the session it reaches the page (an unknown return is refused by it, not continued)
		if got := get(path, owner, "same-origin"); got.Code == http.StatusOK && strings.Contains(got.Body.String(), "refresh") || got.Code == http.StatusUnauthorized {
			t.Fatalf("%s with the cookie: %d %s", path, got.Code, got.Body)
		}
	}
	if w := get("/github/manifest/callback", "", "cross-site"); strings.Contains(w.Body.String(), "refresh") {
		t.Error("a return with no code was continued")
	}
}
