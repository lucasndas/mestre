package github_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/fakegithub"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/pkg/clock"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

// key is one RSA key for the whole binary: generating it is the slow part.
func key(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		testKey = k
	})
	return testKey
}

func pemOf(k *rsa.PrivateKey) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

// vaultFake gives the PEM of each App by the name of its secret and counts the reads.
type vaultFake struct {
	mu    sync.Mutex
	keys  map[string][]byte
	reads int
}

func (v *vaultFake) Use(_ context.Context, name, _ string, fn func([]byte) error) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.reads++
	k, ok := v.keys[name]
	if !ok {
		return context.Canceled
	}
	return fn(k)
}

type world struct {
	t      *testing.T
	ctx    context.Context
	clk    *clock.Fake
	db     *store.DB
	apps   *github.Registry
	gh     *fakegithub.Server
	srv    *httptest.Server
	vault  *vaultFake
	tokens *github.Tokens
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gh, err := fakegithub.New(clk, t)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gh)
	t.Cleanup(srv.Close)
	w := &world{t: t, ctx: ctx, clk: clk, db: db, gh: gh, srv: srv, apps: &github.Registry{DB: db, Clock: clk}, vault: &vaultFake{keys: map[string][]byte{}}}
	w.tokens = &github.Tokens{Apps: w.apps, Keys: w.vault, Clock: clk, BaseURL: srv.URL}
	return w
}

// ready makes an App that is installed in the repositories and ready, with the
// bot known, in the Registry, the vault and the double.
func (w *world) ready(a github.App, appID, installation, bot int64, repoCreation bool, repos ...string) github.App {
	w.t.Helper()
	a.Slug = github.Slug(a.Name)
	if _, err := w.apps.Request(w.ctx, a); err != nil {
		w.t.Fatal(err)
	}
	if err := w.apps.Created(w.ctx, a.Name, a.Slug, appID, "Iv1."+a.Slug); err != nil {
		w.t.Fatal(err)
	}
	if err := w.apps.BotID(w.ctx, a.Name, bot); err != nil {
		w.t.Fatal(err)
	}
	if err := w.apps.Installed(w.ctx, a.Name, installation, repos); err != nil {
		w.t.Fatal(err)
	}
	w.vault.keys[github.KeySecret(a.Name)] = pemOf(key(w.t))
	w.gh.AddApp(a.Slug, appID, installation, &key(w.t).PublicKey, repoCreation, repos...)
	w.gh.AddBot(a.Slug, bot)
	got, err := w.apps.Get(w.ctx, a.Name)
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

var _ = classes.AppReady

var (
	otherOnce sync.Once
	other     *rsa.PrivateKey
)

// otherKey is a second key, not the one registered in the double.
func otherKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	otherOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		other = k
	})
	return other
}
