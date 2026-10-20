package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

type appKeys struct {
	mu   sync.Mutex
	keys map[string][]byte
}

func (k *appKeys) Use(_ context.Context, name, _ string, fn func([]byte) error) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.keys[name]
	if !ok {
		return errors.New("no such secret")
	}
	return fn(v)
}

// githubWorld is a stdRun over the double of GitHub, with the App
// "Grão-Mestre" installed in lucas/meu-app.
type githubWorld struct {
	gh  *fakegithub.Server
	srv *httptest.Server
	g   grandMasterGitHub
	api *github.REST
}

func newGitHubWorld(t *testing.T) *githubWorld {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	double, err := fakegithub.New(clk, t)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(double)
	t.Cleanup(srv.Close)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	apps := &github.Registry{DB: db, Clock: clk}
	a := github.App{Name: "lucas-mestre-grao-mestre", Role: github.RoleGrandMaster, Slug: github.Slug("lucas-mestre-grao-mestre")}
	if _, err := apps.Request(ctx, a); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return apps.Created(ctx, a.Name, a.Slug, 1, "Iv1."+a.Slug) },
		func() error { return apps.BotID(ctx, a.Name, 100) },
		func() error { return apps.Installed(ctx, a.Name, 10, []string{"lucas/meu-app"}) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	double.AddApp(a.Slug, 1, 10, &key.PublicKey, false, "lucas/meu-app")
	double.AddBot(a.Slug, 100)
	double.AddRepo("lucas", "meu-app")
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	http := &http.Client{Timeout: 10 * time.Second}
	r := &stdRun{s: StandardIntegration{GitHubBase: srv.URL, HTTP: http}, env: IntegrationEnv{DB: db, Clock: clk}, quota: &github.Quota{Clock: clk}}
	r.tokens = &github.Tokens{Apps: apps, Keys: &appKeys{keys: map[string][]byte{github.KeySecret(a.Name): pemKey}}, Clock: clk, BaseURL: srv.URL, HTTP: http}
	w := &githubWorld{gh: double, srv: srv, g: grandMasterGitHub{r}}
	w.api, err = w.g.api(ctx, repoReadPermissions)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// The GitHub of production, with the App Grão-Mestre, answers the workspaces
// (docs/desenvolvimento/ligacao-de-producao.md).
func TestTheGrandMasterAppAnswersTheWorkspace(t *testing.T) {
	t.Parallel()
	w := newGitHubWorld(t)
	ctx := t.Context()

	info, err := w.g.Repository(ctx, workspaces.Repo{Owner: "lucas", Name: "meu-app"})
	if err != nil || info.FullName != "lucas/meu-app" || info.DefaultBranch == "" || !strings.HasSuffix(info.CloneURL, "lucas/meu-app.git") {
		t.Fatalf("the repository: %+v, %v", info, err)
	}
	if _, err := w.g.Repository(ctx, workspaces.Repo{Owner: "lucas", Name: "outro"}); !errors.Is(err, workspaces.ErrInvalidRepo) {
		t.Errorf("a repository the App is not installed in: %v", err)
	}
	// GitHub spells the name one way and the requests go in lower case: the installation is found either way
	if got, err := w.g.Repository(ctx, workspaces.Repo{Owner: "Lucas", Name: "Meu-App"}); err != nil || got.FullName != "lucas/meu-app" {
		t.Errorf("the repository in another case: %+v, %v", got, err)
	}
	if a, err := w.g.Auth(ctx, workspaces.Repo{Owner: "lucas", Name: "meu-app"}); err != nil || a == nil || a.Validate() != nil {
		t.Errorf("the auth of the fetch: %+v, %v", a, err)
	}

	// each App acts as itself: the requests are those of the Grão-Mestre
	for _, r := range w.gh.Requests() {
		if !strings.HasPrefix(r.Path, "/repos/lucas/") {
			t.Errorf("a request outside the repository of the workspace: %+v", r)
		}
	}
}

// The integration of production puts the GitHub of the Grão-Mestre where the
// host left the ones that refuse, and leaves alone what a test gave.
func TestTheStandardIntegrationPutsTheRealGitHubWhereTheHostRefuses(t *testing.T) {
	t.Parallel()
	w := newGitHubWorld(t)
	var o OrchestrationOptions
	o.GitHub = unavailableGitHub{}
	w.g.r.Extend(&o)
	if _, ok := o.GitHub.(grandMasterGitHub); !ok {
		t.Errorf("the GitHub of the workspaces is %T", o.GitHub)
	}
	if o.Credentials == nil {
		t.Error("the fetches carry no credentials")
	}
	own := workspaces.GitHub(ownGitHub{})
	o2 := OrchestrationOptions{GitHub: own}
	w.g.r.Extend(&o2)
	if o2.GitHub != own {
		t.Errorf("the GitHub of a test was replaced by %T", o2.GitHub)
	}
}

type ownGitHub struct{}

func (ownGitHub) Repository(context.Context, workspaces.Repo) (workspaces.RepoInfo, error) {
	return workspaces.RepoInfo{}, nil
}
