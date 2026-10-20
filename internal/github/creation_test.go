package github_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/internal/vault"
)

// keepVault is the vault of the creation: it keeps what is put, gives it to Use
// and counts what is deleted.
type keepVault struct {
	mu   sync.Mutex
	keys map[string][]byte
	puts []string
	dels []string
}

func (v *keepVault) Use(_ context.Context, name, _ string, fn func([]byte) error) error {
	v.mu.Lock()
	k, ok := v.keys[name]
	v.mu.Unlock()
	if !ok {
		return context.Canceled
	}
	return fn(k)
}

func (v *keepVault) Put(_ context.Context, name string, _ vault.Type, value []byte, _ *vault.SiteMeta) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.keys == nil {
		v.keys = map[string][]byte{}
	}
	v.keys[name] = append([]byte(nil), value...)
	v.puts = append(v.puts, name)
	return nil
}

func (v *keepVault) Delete(_ context.Context, name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.keys, name)
	v.dels = append(v.dels, name)
	return nil
}

func (v *keepVault) has(name string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, ok := v.keys[name]
	return ok
}

type creationRig struct {
	*world
	c     *github.Creation
	kv    *keepVault
	logs  *bytes.Buffer
	owner users.User
	// beforeSend, when set, runs before each request leaves for GitHub.
	beforeSend atomic.Pointer[func(*http.Request)]
	mu         sync.Mutex
	told       []string
	ready      []string
	login      string
}

func newCreation(t *testing.T) *creationRig {
	t.Helper()
	w := newWorld(t)
	r := &creationRig{world: w, kv: &keepVault{}, logs: &bytes.Buffer{}, login: "lucas",
		owner: users.User{ID: "owner", Name: "Lucas", Level: users.Owner, State: users.Active, GitHubLogin: "lucas"}}
	w.tokens = &github.Tokens{Apps: w.apps, Keys: r.kv, Clock: w.clk, BaseURL: w.srv.URL}
	c, err := github.NewCreation(github.CreationOptions{
		Apps: w.apps, Tokens: w.tokens, Vault: r.kv, API: w.srv.URL, Web: w.srv.URL, HTTP: &http.Client{Transport: hookTransport{r: r, next: w.srv.Client().Transport}},
		Panel:      func() string { return "https://mestre.local:8443" },
		OwnerLogin: func(context.Context) (string, error) { return r.login, nil },
		Tell:       func(_ context.Context, text string) { r.mu.Lock(); r.told = append(r.told, text); r.mu.Unlock() },
		OnReady:    func(_ context.Context, a github.App) { r.mu.Lock(); r.ready = append(r.ready, a.Name); r.mu.Unlock() },
		Logger:     slog.New(slog.NewTextHandler(r.logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	r.c = c
	return r
}

type hookTransport struct {
	r    *creationRig
	next http.RoundTripper
}

func (h hookTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if f := h.r.beforeSend.Load(); f != nil {
		(*f)(req)
	}
	return h.next.RoundTrip(req)
}

func (r *creationRig) tells() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.told...)
}

func (r *creationRig) request(app github.App) github.App {
	r.t.Helper()
	a, err := r.apps.Request(r.ctx, app)
	if err != nil {
		r.t.Fatal(err)
	}
	return a
}

func tokenOf(link github.Link) string { return link.URL[strings.LastIndexByte(link.URL, '/')+1:] }

// opened takes the App to the moment the Owner has the page and GitHub is about
// to give the code: it answers the state of the request.
func (r *creationRig) opened(app github.App) (token, state string) {
	r.t.Helper()
	link, err := r.c.Begin(r.ctx, app.Name, "gm")
	if err != nil {
		r.t.Fatal(err)
	}
	token = tokenOf(link)
	page, err := r.c.Open(r.ctx, token, r.owner)
	if err != nil {
		r.t.Fatal(err)
	}
	i := strings.Index(page.FormAction, "state=")
	return token, page.FormAction[i+len("state="):]
}

func (r *creationRig) conversion(code string, app github.App, appID int64, owner string) {
	r.gh.AddManifestCode(code, fakegithub.Conversion{Name: app.Name, Slug: github.Slug(app.Name), AppID: appID, ClientID: "Iv1." + github.Slug(app.Name), Owner: owner, Key: key(r.t)})
}

var dev = github.App{Name: "lucas-mestre-dev", Role: github.RoleClass, Class: "Dev"}

// CA-GH-APPS-01
func TestACallbackWithAnotherStateIsRefusedAndTheCodeIsNotExchanged(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "lucas")
	for _, bad := range []string{"outro", "", state + "x", strings.ToUpper(state)} {
		if _, err := r.c.Callback(r.ctx, "c1", bad, r.owner); !errors.Is(err, github.ErrBadState) {
			t.Errorf("state %q: %v, want ErrBadState", bad, err)
		}
	}
	if n := r.gh.Conversions(); n != 0 {
		t.Fatalf("the code was exchanged %d times with a state that was not the request's", n)
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State != classes.AppAwaitingCreation {
		t.Errorf("the App is %s", got.State)
	}
	// the state of a request that a newer link replaced does not work either
	if _, err := r.c.Begin(r.ctx, app.Name, "gm"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); !errors.Is(err, github.ErrBadState) {
		t.Errorf("the state of a replaced request: %v", err)
	}
	if r.gh.Conversions() != 0 {
		t.Error("the code was exchanged for a replaced request")
	}
}

// CA-GH-APPS-02
func TestTheCodeIsExchangedTheKeyGoesToTheVaultAndOnlyThere(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "lucas")
	install, err := r.c.Callback(r.ctx, "c1", state, r.owner)
	if err != nil {
		t.Fatal(err)
	}
	if want := r.srv.URL + "/apps/lucas-mestre-dev/installations/new"; install != want {
		t.Errorf("install address %q, want %q", install, want)
	}
	got, _ := r.apps.Get(r.ctx, app.Name)
	if got.State != classes.AppAwaitingInstall || got.AppID != 31 || got.Slug != "lucas-mestre-dev" {
		t.Fatalf("App after the exchange: %+v", got)
	}
	if !r.kv.has(github.KeySecret(app.Name)) {
		t.Fatal("the key is not in the vault")
	}
	pemText := string(pemOf(key(t)))
	body := strings.TrimSpace(strings.SplitN(pemText, "\n", 3)[1])[:40]
	if dbContains(t, r.world, body) {
		t.Error("the private key is in the database")
	}
	if strings.Contains(r.logs.String(), body) || strings.Contains(strings.Join(r.tells(), "\n"), body) {
		t.Error("the private key reached a log or a notice")
	}
	// the code is spent: the same callback does nothing more
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); !errors.Is(err, github.ErrBadState) || r.gh.Conversions() != 1 {
		t.Errorf("a second callback: %v after %d conversions", err, r.gh.Conversions())
	}
}

func TestTheClientSecretOfTheGrandMasterGoesToTheVaultAndOnlyThereAndNoOtherAppKeepsOne(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	gm := r.request(github.App{Name: "Grão-Mestre", Role: github.RoleGrandMaster})
	_, state := r.opened(gm)
	r.conversion("c1", gm, 41, "lucas")
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); err != nil {
		t.Fatal(err)
	}
	if !r.kv.has(github.ClientSecretName(gm.Name)) {
		t.Fatal("the client secret of the App is not in the vault")
	}
	if dbContains(t, r.world, "fake-client-secret") || strings.Contains(r.logs.String(), "fake-client-secret") {
		t.Error("the client secret reached the database or a log")
	}
	app := r.request(dev)
	_, state = r.opened(app)
	r.conversion("c2", app, 42, "lucas")
	if _, err := r.c.Callback(r.ctx, "c2", state, r.owner); err != nil {
		t.Fatal(err)
	}
	if r.kv.has(github.ClientSecretName(app.Name)) {
		t.Error("an App without the device flow kept a client secret")
	}
}

// CA-GH-APPS-03
func TestALinkNobodyOpenedExpiresAfter24HoursAndTheOwnerCanAskAnother(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	link, err := r.c.Begin(r.ctx, app.Name, "gm")
	if err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(23*time.Hour + 59*time.Minute)
	if err := r.c.Sweep(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State != classes.AppAwaitingCreation || len(r.tells()) != 0 {
		t.Fatalf("before 24 h the App is %s and %d notices were sent", got.State, len(r.tells()))
	}
	r.clk.Advance(2 * time.Minute)
	if err := r.c.Sweep(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State != classes.AppExpired {
		t.Fatalf("the App is %s, want expirado", got.State)
	}
	if _, err := r.c.Open(r.ctx, tokenOf(link), r.owner); !errors.Is(err, github.ErrLinkGone) {
		t.Errorf("the expired link opened: %v", err)
	}
	if told := r.tells(); len(told) != 1 || !strings.Contains(told[0], app.Name) {
		t.Errorf("the Owner was told %q", told)
	}
	again, err := r.c.Begin(r.ctx, app.Name, "gm")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State != classes.AppAwaitingCreation {
		t.Errorf("after a new link the App is %s", got.State)
	}
	if _, err := r.c.Open(r.ctx, tokenOf(again), r.owner); err != nil {
		t.Errorf("the new link: %v", err)
	}
}

// CA-GH-APPS-07
func TestAnInstallationOfAnotherAppIsRefusedAndTheAppKeepsWaiting(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "lucas")
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); err != nil {
		t.Fatal(err)
	}
	// an App that is not the Mestre's, installed in the account of the Owner
	r.gh.AddApp("foreign", 99, 900, &otherKey(t).PublicKey, false, "lucas/meu-app")
	r.gh.Install("foreign", 900, "lucas", "lucas/meu-app")
	// ours, installed in another account
	r.gh.Install("lucas-mestre-dev", 31001, "someone-else", "someone-else/repo")
	for _, id := range []int64{900, 31001, 12345, 0, -1} {
		if _, err := r.c.Installed(r.ctx, id, r.owner); !errors.Is(err, github.ErrWrongInstallation) {
			t.Errorf("installation %d: %v, want ErrWrongInstallation", id, err)
		}
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State != classes.AppAwaitingInstall {
		t.Fatalf("the App is %s, want aguardando_instalacao", got.State)
	}
	if _, err := r.apps.Installation(r.ctx, app.Name, "lucas/meu-app"); !errors.Is(err, github.ErrNotInstalled) {
		t.Errorf("a refused installation was recorded: %v", err)
	}
}

// CA-GH-APPS-08
func TestAnAppMadeInAnotherAccountIsNotUsedAndLeavesNoKey(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "intruder")
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); !errors.Is(err, github.ErrWrongOwner) {
		t.Fatalf("callback: %v, want ErrWrongOwner", err)
	}
	if r.kv.has(github.KeySecret(app.Name)) {
		t.Error("the key of an App of another account is in the vault")
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State == classes.AppAwaitingInstall || got.State == classes.AppReady || got.AppID != 0 {
		t.Errorf("the App is %+v", got)
	}
	if told := r.tells(); len(told) != 1 || !strings.Contains(told[0], "intruder") {
		t.Errorf("the Owner was told %q", told)
	}
}

func TestTheOwnerHandsTheCodeBackInAnHourOrTheRequestFails(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "lucas")
	r.clk.Advance(github.CodeLife + time.Second)
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); !errors.Is(err, github.ErrCodeLate) {
		t.Fatalf("callback: %v, want ErrCodeLate", err)
	}
	if r.gh.Conversions() != 0 {
		t.Error("a late code was exchanged")
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State != classes.AppFailed {
		t.Errorf("the App is %s, want falhou", got.State)
	}
	if told := r.tells(); len(told) != 1 || !strings.Contains(told[0], "exclua") {
		t.Errorf("the Owner was told %q", told)
	}
	// and a sweep alone does the same for a request nobody came back to
	r2 := newCreation(t)
	app2 := r2.request(dev)
	r2.opened(app2)
	r2.clk.Advance(github.CodeLife + time.Second)
	if err := r2.c.Sweep(r2.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := r2.apps.Get(r2.ctx, app2.Name); got.State != classes.AppFailed {
		t.Errorf("after the sweep the App is %s", got.State)
	}
}

func TestOnlyTheOwnerOpensTheLinkOrHandsTheCodeBack(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	link, err := r.c.Begin(r.ctx, app.Name, "gm")
	if err != nil {
		t.Fatal(err)
	}
	admin := users.User{ID: "adm", Level: users.Administrator, State: users.Active}
	suspended := r.owner
	suspended.State = users.Suspended
	for _, u := range []users.User{admin, suspended, {}} {
		if _, err := r.c.Open(r.ctx, tokenOf(link), u); !errors.Is(err, github.ErrOwnerOnly) {
			t.Errorf("%+v opened the link: %v", u.Level, err)
		}
		if _, err := r.c.Callback(r.ctx, "c", "s", u); !errors.Is(err, github.ErrOwnerOnly) {
			t.Errorf("%+v handed a code: %v", u.Level, err)
		}
		if _, err := r.c.Installed(r.ctx, 1, u); !errors.Is(err, github.ErrOwnerOnly) {
			t.Errorf("%+v confirmed an installation: %v", u.Level, err)
		}
	}
	// the refusal did not use the link
	if _, err := r.c.Open(r.ctx, tokenOf(link), r.owner); err != nil {
		t.Errorf("the Owner after the refusals: %v", err)
	}
}

func TestANewLinkReplacesTheOlderOne(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	old, err := r.c.Begin(r.ctx, app.Name, "gm")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := r.c.Begin(r.ctx, app.Name, "gm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.c.Open(r.ctx, tokenOf(old), r.owner); !errors.Is(err, github.ErrLinkGone) {
		t.Errorf("the older link still opens: %v", err)
	}
	if _, err := r.c.Open(r.ctx, tokenOf(fresh), r.owner); err != nil {
		t.Errorf("the newer link: %v", err)
	}
	if !strings.HasPrefix(fresh.URL, "https://mestre.local:8443/github/apps/novo/") {
		t.Errorf("the link is %q: it must be the name of the panel", fresh.URL)
	}
	// an App that is not waiting for its creation has no link
	if _, err := r.c.Begin(r.ctx, "nobody", "gm"); !errors.Is(err, github.ErrNoApp) {
		t.Errorf("a link for no App: %v", err)
	}
}

func TestTheManifestAsksForTheMinimumAndNoWebhook(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	link, _ := r.c.Begin(r.ctx, app.Name, "gm")
	page, err := r.c.Open(r.ctx, tokenOf(link), r.owner)
	if err != nil {
		t.Fatal(err)
	}
	m := page.Manifest
	if m.Public || m.Name != app.Name || m.URL != "https://github.com/lucas" || len(m.DefaultEvents) != 0 || !m.SetupOnUpdate || m.RequestOAuth {
		t.Errorf("manifest: %+v", m)
	}
	if m.HookAttributes["active"] != false || m.HookAttributes["url"] != "https://example.invalid" {
		t.Errorf("the webhook: %v", m.HookAttributes)
	}
	if m.RedirectURL != "https://mestre.local:8443/github/manifest/callback" || m.SetupURL != "https://mestre.local:8443/github/apps/instalado" {
		t.Errorf("the addresses: %q %q", m.RedirectURL, m.SetupURL)
	}
	want := github.RolePermissions(github.RoleClass, false)
	for k, v := range want {
		if m.DefaultPermissions[k] != v {
			t.Errorf("permission %s = %q, want %q", k, m.DefaultPermissions[k], v)
		}
	}
	if len(m.DefaultPermissions) != len(want) || strings.Contains(page.ManifestJSON, "client_secret") {
		t.Errorf("permissions %v", m.DefaultPermissions)
	}
}

func TestALimitedConversionKeepsTheRequestOpenForTheRestOfTheHour(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "lucas")
	r.gh.FailConversion(http.StatusTooManyRequests, map[string]string{"Retry-After": "120"})
	var lim *github.LimitedError
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); !errors.As(err, &lim) || lim.RetryAfter != 120*time.Second {
		t.Fatalf("callback: %v, want a limit of 120 s", err)
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State != classes.AppAwaitingCreation {
		t.Fatalf("the App is %s after a limit", got.State)
	}
	r.clk.Advance(2 * time.Minute)
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); err != nil {
		t.Fatalf("the code, tried again: %v", err)
	}
	// a code GitHub does not know leaves the request open too
	app2 := r.request(github.App{Name: "lucas-mestre-qa", Role: github.RoleClass, Class: "QA"})
	_, state2 := r.opened(app2)
	if _, err := r.c.Callback(r.ctx, "unknown", state2, r.owner); err == nil {
		t.Fatal("a code GitHub does not know was taken")
	}
	r.conversion("c2", app2, 32, "lucas")
	if _, err := r.c.Callback(r.ctx, "c2", state2, r.owner); err != nil {
		t.Fatalf("the right code after a wrong one: %v", err)
	}
}

func TestTheInstallationIsFoundWhenTheBrowserDoesNotComeBack(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "lucas")
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(github.InstallPoll)
	if err := r.c.Sweep(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State != classes.AppAwaitingInstall {
		t.Fatalf("nothing was installed and the App is %s", got.State)
	}
	r.gh.Install("lucas-mestre-dev", 5001, "lucas", "lucas/meu-app", "lucas/outro")
	r.clk.Advance(github.InstallPoll)
	if err := r.c.Sweep(r.ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := r.apps.Get(r.ctx, app.Name)
	if got.State != classes.AppReady || got.BotUserID != 1031 {
		t.Fatalf("after the installation the App is %+v", got)
	}
	for _, repo := range []string{"lucas/meu-app", "lucas/outro"} {
		if id, err := r.apps.Installation(r.ctx, app.Name, repo); err != nil || id != 5001 {
			t.Errorf("installation of %s: %d, %v", repo, id, err)
		}
	}
	r.mu.Lock()
	ready := append([]string(nil), r.ready...)
	r.mu.Unlock()
	if len(ready) != 1 || ready[0] != app.Name {
		t.Errorf("OnReady told %v", ready)
	}
	// the key of the vault does work for the token of the new installation
	if _, err := r.tokens.Installation(r.ctx, github.Request{App: app.Name, Repo: "lucas/meu-app", Permissions: github.RolePermissions(github.RoleClass, false)}); err != nil {
		t.Errorf("a token with the App that was just made: %v", err)
	}
}

func TestTheAppGrandMasterIsReadyOnlyWithTheDeviceFlowEnabled(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	gm := r.request(github.App{Name: "lucas-mestre-grao-mestre", Role: github.RoleGrandMaster})
	_, state := r.opened(gm)
	r.conversion("c1", gm, 11, "lucas")
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); err != nil {
		t.Fatal(err)
	}
	r.gh.Install("lucas-mestre-grao-mestre", 6001, "lucas", "lucas/meu-app")
	if _, err := r.c.Installed(r.ctx, 6001, r.owner); !errors.Is(err, github.ErrDeviceFlowOff) {
		t.Fatalf("without the device flow: %v", err)
	}
	if got, _ := r.apps.Get(r.ctx, gm.Name); got.State != classes.AppAwaitingInstall {
		t.Fatalf("the App is %s without the device flow", got.State)
	}
	r.gh.SetDeviceFlow("lucas-mestre-grao-mestre", true)
	got, err := r.c.Installed(r.ctx, 6001, r.owner)
	if err != nil || got.State != classes.AppReady {
		t.Fatalf("with the device flow: %+v, %v", got, err)
	}
}

// dbContains says whether any text of the database has the needle.
func dbContains(t *testing.T, w *world, needle string) bool {
	t.Helper()
	tabs, err := w.db.Reader().QueryContext(w.ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tabs.Next() {
		var n string
		if err := tabs.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	_ = tabs.Close()
	for _, n := range names {
		rows, err := w.db.Reader().QueryContext(w.ctx, `SELECT * FROM "`+n+`"`)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				switch x := v.(type) {
				case string:
					if strings.Contains(x, needle) {
						_ = rows.Close()
						return true
					}
				case []byte:
					if bytes.Contains(x, []byte(needle)) {
						_ = rows.Close()
						return true
					}
				}
			}
		}
		_ = rows.Close()
	}
	return false
}

func (r *creationRig) web(who users.User, ok bool) http.Handler {
	return (&github.Web{Creation: r.c, Who: func(*http.Request) (users.User, bool) { return who, ok }}).Handler()
}

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// Part of CA-GH-APPS-04 (the login of the Administrator is the panel's, M6): who
// is not the Owner gets "exige Dono" and no form is built.
func TestTheLinkPageRefusesWhoIsNotTheOwnerAndBuildsNoForm(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	link, _ := r.c.Begin(r.ctx, app.Name, "gm")
	path := strings.TrimPrefix(link.URL, "https://mestre.local:8443")
	admin := users.User{ID: "adm", Level: users.Administrator, State: users.Active}
	rec := get(r.web(admin, true), path)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Exige Dono") || strings.Contains(rec.Body.String(), "<form") {
		t.Errorf("administrator: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(r.web(users.User{}, false), path); rec.Code != http.StatusUnauthorized || strings.Contains(rec.Body.String(), "<form") {
		t.Errorf("nobody logged in: %d", rec.Code)
	}
	for _, p := range []string{"/github/manifest/callback?code=c&state=s", "/github/apps/instalado?installation_id=1"} {
		if rec := get(r.web(admin, true), p); rec.Code != http.StatusForbidden {
			t.Errorf("%s as administrator: %d", p, rec.Code)
		}
	}
	if r.gh.Conversions() != 0 {
		t.Error("something went to GitHub")
	}
}

func TestTheWholeFlowThroughThePagesOfTheOwner(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	link, _ := r.c.Begin(r.ctx, app.Name, "gm")
	h := r.web(r.owner, true)
	rec := get(h, strings.TrimPrefix(link.URL, "https://mestre.local:8443"))
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `<form method="post" action="`+r.srv.URL+`/settings/apps/new?state=`) ||
		!strings.Contains(body, "Criar no GitHub") || !strings.Contains(body, "lucas-mestre-dev") {
		t.Fatalf("the page: %d %s", rec.Code, body)
	}
	for k, want := range map[string]string{"Cache-Control": "no-store", "Referrer-Policy": "no-referrer"} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "form-action "+r.srv.URL) {
		t.Errorf("CSP %q", csp)
	}
	i := strings.Index(body, "state=") + len("state=")
	state := body[i : strings.IndexByte(body[i:], '"')+i]
	r.conversion("c1", app, 31, "lucas")
	rec = get(h, "/github/manifest/callback?code=c1&state="+state)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != r.srv.URL+"/apps/lucas-mestre-dev/installations/new" {
		t.Fatalf("callback: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	r.gh.Install("lucas-mestre-dev", 5001, "lucas", "lucas/meu-app")
	if rec = get(h, "/github/apps/instalado?installation_id=oops"); rec.Code != http.StatusBadRequest {
		t.Errorf("a bad installation id: %d", rec.Code)
	}
	if rec = get(h, "/github/apps/instalado?installation_id=5001"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "pronto") {
		t.Fatalf("installed: %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State != classes.AppReady {
		t.Errorf("the App is %s", got.State)
	}
	// the same link does not work twice
	if rec = get(h, strings.TrimPrefix(link.URL, "https://mestre.local:8443")); rec.Code != http.StatusGone {
		t.Errorf("the link again: %d", rec.Code)
	}
}

func TestAnOwnerLoginThatIsNotKnownSpendsNoCode(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "lucas")
	r.login = ""
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); err == nil || errors.Is(err, github.ErrWrongOwner) {
		t.Fatalf("callback without the login of the Owner: %v", err)
	}
	if r.gh.Conversions() != 0 {
		t.Fatal("the code was spent without knowing whose account must own the App")
	}
	r.login = "lucas"
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); err != nil {
		t.Fatalf("the request was not left open: %v", err)
	}
}

func TestTheBrowserLeavingDuringTheExchangeDoesNotLoseTheApp(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "lucas")
	gone, cancel := context.WithCancel(r.ctx)
	hook := func(req *http.Request) {
		if strings.Contains(req.URL.Path, "/conversions") {
			cancel() // the Owner's phone went to sleep while GitHub answers
		}
	}
	r.beforeSend.Store(&hook)
	if _, err := r.c.Callback(gone, "c1", state, r.owner); err != nil {
		t.Fatalf("the exchange of a request whose browser left: %v", err)
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State != classes.AppAwaitingInstall || !r.kv.has(github.KeySecret(app.Name)) {
		t.Errorf("the App is %s, key in the vault %v", got.State, r.kv.has(github.KeySecret(app.Name)))
	}
}

func TestARequestLeftInTheMiddleOfTheExchangeIsFailedByTheSweepAndBlocksNewLinksUntilThen(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	r.opened(app)
	if _, err := r.db.Writer().ExecContext(r.ctx, `UPDATE github_app_requests SET status = 'exchanging'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.c.Begin(r.ctx, app.Name, "gm"); !errors.Is(err, github.ErrAppState) {
		t.Errorf("a link while the code is being exchanged: %v", err)
	}
	r.clk.Advance(github.CodeLife + time.Second)
	if err := r.c.Sweep(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State != classes.AppFailed {
		t.Errorf("the App is %s, want falhou", got.State)
	}
	if told := r.tells(); len(told) != 1 || !strings.Contains(told[0], "exclua") {
		t.Errorf("the Owner was told %q", told)
	}
}

func TestATokenRefusedWhileInstallingDoesNotPutANewAppInError(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "lucas")
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); err != nil {
		t.Fatal(err)
	}
	r.gh.Install("lucas-mestre-dev", 5001, "lucas", "lucas/meu-app")
	r.gh.FailTokens(http.StatusNotFound, nil)
	if _, err := r.c.Installed(r.ctx, 5001, r.owner); err == nil {
		t.Fatal("installed with a refused token")
	}
	if got, _ := r.apps.Get(r.ctx, app.Name); got.State != classes.AppAwaitingInstall {
		t.Fatalf("the App is %s after one refused token", got.State)
	}
	if got, err := r.c.Installed(r.ctx, 5001, r.owner); err != nil || got.State != classes.AppReady {
		t.Fatalf("the retry: %+v, %v", got, err)
	}
}

func TestTheReturnAndThePollMeetingMakeTheAppReadyAndTellOnce(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "lucas")
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); err != nil {
		t.Fatal(err)
	}
	r.gh.Install("lucas-mestre-dev", 5001, "lucas", "lucas/meu-app")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { _, _ = r.c.Installed(r.ctx, 5001, r.owner) })
	}
	r.clk.Advance(github.InstallPoll)
	wg.Go(func() { _ = r.c.Sweep(r.ctx) })
	wg.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.ready) != 1 {
		t.Errorf("OnReady was called %d times", len(r.ready))
	}
	n := 0
	for _, s := range r.told {
		if strings.Contains(s, "está pronto") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the Owner was told it was ready %d times", n)
	}
}

func TestAReadyAppGetsItsRepositoriesRefreshedWhenGitHubSendsTheInstallationBack(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "lucas")
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); err != nil {
		t.Fatal(err)
	}
	r.gh.Install("lucas-mestre-dev", 5001, "lucas", "lucas/a")
	if _, err := r.c.Installed(r.ctx, 5001, r.owner); err != nil {
		t.Fatal(err)
	}
	r.gh.Install("lucas-mestre-dev", 5001, "lucas", "lucas/a", "lucas/b")
	if _, err := r.c.Installed(r.ctx, 5001, r.owner); err != nil {
		t.Fatal(err)
	}
	if id, err := r.apps.Installation(r.ctx, app.Name, "lucas/b"); err != nil || id != 5001 {
		t.Errorf("the new repository: %d, %v", id, err)
	}
	if len(r.ready) != 1 {
		t.Errorf("OnReady was called %d times", len(r.ready))
	}
}

func TestTheDeviceFlowReminderIsSentOnceAndTheWatchGoesOnPastTheHour(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	gm := r.request(github.App{Name: "lucas-mestre-grao-mestre", Role: github.RoleGrandMaster})
	_, state := r.opened(gm)
	r.conversion("c1", gm, 11, "lucas")
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); err != nil {
		t.Fatal(err)
	}
	r.gh.Install("lucas-mestre-grao-mestre", 6001, "lucas", "lucas/meu-app")
	for range 10 {
		r.clk.Advance(github.InstallPoll)
		if err := r.c.Sweep(r.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if told := r.tells(); len(told) != 1 || !strings.Contains(told[0], "Enable Device Flow") {
		t.Fatalf("the reminders: %q", told)
	}
	r.clk.Advance(2 * time.Hour) // past the hour of the polling: the Owner is still on the way
	_ = r.c.Sweep(r.ctx)
	r.gh.SetDeviceFlow("lucas-mestre-grao-mestre", true)
	r.clk.Advance(github.InstallPoll)
	if err := r.c.Sweep(r.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.apps.Get(r.ctx, gm.Name); got.State != classes.AppReady {
		t.Errorf("the App is %s after the device flow was enabled", got.State)
	}
}

func TestTheInstallationIsLookedForEvery30SecondsForAnHourAndThenTheOwnerIsToldOnce(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	_, state := r.opened(app)
	r.conversion("c1", app, 31, "lucas")
	if _, err := r.c.Callback(r.ctx, "c1", state, r.owner); err != nil {
		t.Fatal(err)
	}
	_ = r.c.Sweep(r.ctx)
	r.clk.Advance(10 * time.Second)
	_ = r.c.Sweep(r.ctx)
	if n := r.gh.InstallationLookups(); n != 0 {
		t.Fatalf("looked %d times before the first 30 s", n)
	}
	r.clk.Advance(20 * time.Second)
	_ = r.c.Sweep(r.ctx)
	r.clk.Advance(10 * time.Second)
	_ = r.c.Sweep(r.ctx)
	if n := r.gh.InstallationLookups(); n != 1 {
		t.Fatalf("looked %d times in 40 s, want 1", n)
	}
	r.clk.Advance(github.CodeLife)
	_ = r.c.Sweep(r.ctx)
	before := r.gh.InstallationLookups()
	r.clk.Advance(github.InstallPoll)
	_ = r.c.Sweep(r.ctx)
	if r.gh.InstallationLookups() != before {
		t.Error("the poll went on after the hour")
	}
	if told := r.tells(); len(told) != 1 || !strings.Contains(told[0], "Não vi a instalação") {
		t.Errorf("the Owner was told %q", told)
	}
	// the return of the browser, late, still finishes it
	r.gh.Install("lucas-mestre-dev", 5001, "lucas", "lucas/meu-app")
	if got, err := r.c.Installed(r.ctx, 5001, r.owner); err != nil || got.State != classes.AppReady {
		t.Errorf("the late return: %+v, %v", got, err)
	}
}

func TestOnlyWhoOpenedTheLinkOpensItAgainOrHandsTheCodeBack(t *testing.T) {
	t.Parallel()
	r := newCreation(t)
	app := r.request(dev)
	token, state := r.opened(app)
	second := r.owner
	second.ID = "owner-2" // another session with the same level
	if _, err := r.c.Open(r.ctx, token, second); !errors.Is(err, github.ErrOwnerOnly) {
		t.Errorf("another user reopened the link: %v", err)
	}
	r.conversion("c1", app, 31, "lucas")
	if _, err := r.c.Callback(r.ctx, "c1", state, second); !errors.Is(err, github.ErrOwnerOnly) || r.gh.Conversions() != 0 {
		t.Errorf("another user handed the code: %v", err)
	}
	if _, err := r.c.Open(r.ctx, token, r.owner); err != nil {
		t.Errorf("the one who opened it, again: %v", err)
	}
}
