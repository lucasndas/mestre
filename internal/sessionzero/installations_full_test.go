//go:build full

package sessionzero_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/testkit/fakegithub"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/internal/vault"
)

// memVault is the vault of these tests: what is put is given back to Use.
type memVault struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (v *memVault) Use(_ context.Context, name, _ string, fn func([]byte) error) error {
	v.mu.Lock()
	b, ok := v.data[name]
	v.mu.Unlock()
	if !ok {
		return vault.ErrNotFound
	}
	return fn(b)
}

func (v *memVault) Put(_ context.Context, name string, _ vault.Type, value []byte, _ *vault.SiteMeta) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.data == nil {
		v.data = map[string][]byte{}
	}
	v.data[name] = append([]byte(nil), value...)
	return nil
}

func (v *memVault) Delete(_ context.Context, name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.data, name)
	return nil
}

// ghEnv is the session zero over the real Installations: the two fixed Apps are
// ready and installed in another repository of the Owner, over the double of
// GitHub; the repository of the workspace is on none of the installations.
type ghEnv struct {
	*env
	gh       *fakegithub.Server
	personal *github.Personal
	owner    users.User
	slugs    []string
}

func newGHEnv(t *testing.T) *ghEnv {
	t.Helper()
	e := newEnv(t, nil)
	g := &ghEnv{env: e}
	var err error
	if g.gh, err = fakegithub.New(e.clk, t); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(g.gh)
	t.Cleanup(srv.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	kv := &memVault{}
	apps := &github.Registry{DB: e.db, Clock: e.clk}
	us, err := users.New(users.Options{DB: e.db, Clock: e.clk, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := us.CreateOwner(e.ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	login := "lucas"
	if g.owner, err = us.Edit(e.ctx, owner, owner.ID, users.Edit{GitHubLogin: &login}); err != nil {
		t.Fatal(err)
	}
	g.gh.AddRepo("lucas", "meu_app")
	g.gh.AddRepo("lucas", "other")
	g.gh.SetRepoID("lucas/meu_app", 777)
	for i, name := range []string{sessionzero.AppGrandMaster, sessionzero.AppMestre} {
		role := github.RoleGrandMaster
		if name == sessionzero.AppMestre {
			role = github.RoleMestre
		}
		app := github.App{Name: name, Role: role, Slug: github.Slug(name)}
		if _, err := apps.Request(e.ctx, app); err != nil {
			t.Fatal(err)
		}
		appID, inst := int64(10+i), int64(110+i)
		if err := apps.Created(e.ctx, name, app.Slug, appID, "Iv1."+app.Slug); err != nil {
			t.Fatal(err)
		}
		if err := apps.Installed(e.ctx, name, inst, []string{"lucas/other"}); err != nil {
			t.Fatal(err)
		}
		_ = kv.Put(e.ctx, github.KeySecret(name), "", pemKey, nil)
		g.gh.AddApp(app.Slug, appID, inst, &key.PublicKey, false, "lucas/other")
		g.slugs = append(g.slugs, app.Slug)
		if role == github.RoleGrandMaster {
			g.gh.SetClientID(app.Slug, "Iv1."+app.Slug)
			g.gh.SetDeviceFlow(app.Slug, true)
			g.gh.SetClientSecret("Iv1."+app.Slug, "client-secret-gm")
			_ = kv.Put(e.ctx, github.ClientSecretName(name), "", []byte("client-secret-gm"), nil)
		}
	}
	tokens := &github.Tokens{Apps: apps, Keys: kv, Clock: e.clk, BaseURL: srv.URL}
	hc := &http.Client{Transport: srv.Client().Transport}
	if g.personal, err = github.NewPersonal(github.PersonalOptions{DB: e.db, Clock: e.clk, Apps: apps, Users: us, Vault: kv, API: srv.URL, Web: srv.URL, HTTP: hc}); err != nil {
		t.Fatal(err)
	}
	installs := &github.Installs{Apps: apps, Tokens: tokens, Personal: g.personal, Owner: func(context.Context) (string, error) { return g.owner.ID, nil },
		API: srv.URL, Web: "https://github.com", HTTP: hc}
	e.f.real = sessionzero.GitHubInstallations{Source: installs}
	return g
}

// connect has the Owner connect the personal credential by the device flow.
func (g *ghEnv) connect() {
	g.t.Helper()
	ch, err := g.personal.Begin(g.ctx, g.owner.ID)
	if err != nil {
		g.t.Fatal(err)
	}
	g.gh.AuthorizeDevice(ch.UserCode, "lucas")
	g.clk.Advance(ch.Interval)
	if res, err := g.personal.Poll(g.ctx, g.owner.ID); err != nil || res != github.PollConnected {
		g.t.Fatalf("connect = %q, %v", res, err)
	}
}

// CA-GH-APPS-05
func TestANewWorkspaceGetsItsRepositoryInTheInstallationByTheApiWithThePersonalCredentialOrByTheLinkOtherwise(t *testing.T) {
	t.Parallel()
	t.Run("with the personal credential the engine uses the PUT of the API", func(t *testing.T) {
		t.Parallel()
		g := newGHEnv(t)
		g.connect()
		w := g.create()
		if err := g.ws.Prepare(g.ctx, w.Slug); err != nil {
			t.Fatalf("Prepare = %v", err)
		}
		if got := g.get(w.Slug); got.State != workspaces.StateActive {
			t.Fatalf("workspace = %s/%s, want active", got.State, got.Stage)
		}
		var puts []string
		for _, a := range g.gh.Actions() {
			if a.Method == http.MethodPut && strings.HasPrefix(a.Path, "/user/installations/") {
				if a.App != "lucas" {
					t.Errorf("the PUT acted as %q, want the person", a.App)
				}
				puts = append(puts, a.Path)
			}
		}
		slices.Sort(puts)
		if len(puts) != 2 || puts[0] != "/user/installations/110/repositories/777" || puts[1] != "/user/installations/111/repositories/777" {
			t.Fatalf("PUTs = %v, want one per App, for the repository 777", puts)
		}
		if len(g.f.ownerLinks) != 0 {
			t.Fatalf("the Owner got %d messages with links; the API did it", len(g.f.ownerLinks))
		}
	})
	t.Run("without it the Owner gets the link and the engine goes on when it sees the repository", func(t *testing.T) {
		t.Parallel()
		g := newGHEnv(t)
		w := g.create()
		if err := g.ws.Prepare(g.ctx, w.Slug); err == nil {
			t.Fatal("Prepare went through with the Apps not covering the repository")
		}
		if got := g.get(w.Slug); got.Stage != workspaces.StageApps || got.StageFailure != nil {
			t.Fatalf("workspace = %s/%s failure=%v, want waiting at apps", got.State, got.Stage, got.StageFailure)
		}
		for _, a := range g.gh.Actions() {
			if a.Method == http.MethodPut {
				t.Fatalf("a PUT %s without the credential", a.Path)
			}
		}
		if len(g.f.ownerLinks) != 1 || len(g.f.ownerLinks[0]) != 2 {
			t.Fatalf("links to the Owner = %+v, want one message with two", g.f.ownerLinks)
		}
		for _, l := range g.f.ownerLinks[0] {
			if !strings.HasPrefix(l.URL, "https://github.com/settings/installations/11") {
				t.Errorf("link %q is not the settings of the installation", l.URL)
			}
		}
		// the Owner ticks the repository on github.com
		for _, slug := range g.slugs {
			g.gh.AddInstalledRepo(slug, "lucas/meu_app", true)
		}
		g.clk.Advance(sessionzero.AppsPoll)
		if _, _, err := g.ws.Tick(g.ctx); err != nil {
			t.Fatal(err)
		}
		if got := g.get(w.Slug); got.State != workspaces.StateActive {
			t.Fatalf("workspace = %s/%s after the Owner ticked the repository, want active", got.State, got.Stage)
		}
		if len(g.f.ownerLinks) != 1 {
			t.Fatalf("the Owner was asked %d times", len(g.f.ownerLinks))
		}
	})
}
