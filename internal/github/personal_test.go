package github_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/users"
)

// personalRig is the Guild with the App "Grão-Mestre" ready, its device flow on,
// the Owner registered as "lucas" on GitHub, and the personal credentials over
// the double of GitHub.
type personalRig struct {
	*world
	p     *github.Personal
	kv    *keepVault
	logs  *bytes.Buffer
	users *users.Service
	owner users.User
	app   github.App
}

type noApprovals struct{}

func (noApprovals) InvalidateBy(context.Context, string) error { return nil }

func newPersonal(t *testing.T) *personalRig {
	t.Helper()
	w := newWorld(t)
	r := &personalRig{world: w, kv: &keepVault{}, logs: &bytes.Buffer{}}
	var err error
	r.users, err = users.New(users.Options{DB: w.db, Clock: w.clk, Approvals: noApprovals{}, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := r.users.CreateOwner(w.ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	login := "lucas"
	if r.owner, err = r.users.Edit(w.ctx, owner, owner.ID, users.Edit{GitHubLogin: &login}); err != nil {
		t.Fatal(err)
	}
	r.app = w.ready(github.App{Name: "Grão-Mestre", Role: github.RoleGrandMaster}, 11, 111, 99, false, "lucas/meu-app")
	w.gh.SetClientID(r.app.Slug, r.app.ClientID)
	w.gh.SetDeviceFlow(r.app.Slug, true)
	w.gh.SetClientSecret(r.app.ClientID, "client-secret-gm")
	if err := r.kv.Put(w.ctx, github.ClientSecretName(r.app.Name), "github_app_key", []byte("client-secret-gm"), nil); err != nil {
		t.Fatal(err)
	}
	r.p, err = github.NewPersonal(github.PersonalOptions{DB: w.db, Clock: w.clk, Apps: w.apps, Users: r.users, Vault: r.kv, API: w.srv.URL, Web: w.srv.URL,
		HTTP: &http.Client{Transport: w.srv.Client().Transport}, Logger: slog.New(slog.NewTextHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// connect walks the whole device flow of the Owner as the account `as`.
func (r *personalRig) connect(as string) error {
	r.t.Helper()
	ch, err := r.p.Begin(r.ctx, r.owner.ID)
	if err != nil {
		return err
	}
	r.gh.AuthorizeDevice(ch.UserCode, as)
	r.clk.Advance(ch.Interval)
	res, err := r.p.Poll(r.ctx, r.owner.ID)
	if err == nil && res != github.PollConnected {
		r.t.Fatalf("poll = %q, want connected", res)
	}
	return err
}

func (r *personalRig) token() string {
	r.t.Helper()
	var raw string
	if err := r.kv.Use(r.ctx, github.UserTokenSecret(r.owner.ID), "test", func(b []byte) error { raw = string(b); return nil }); err != nil {
		r.t.Fatal(err)
	}
	return raw
}

// everythingElse is what the engine wrote anywhere but the vault: the logs and
// the rows of the credential and of the audit.
func (r *personalRig) everythingElse() string {
	r.t.Helper()
	var b strings.Builder
	b.WriteString(r.logs.String())
	for _, q := range []string{`SELECT user_id||login||state||secret FROM github_user_credentials`, `SELECT app||action||user_id||detail FROM github_app_audit`} {
		rows, err := r.db.Reader().QueryContext(r.ctx, q)
		if err != nil {
			r.t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				r.t.Fatal(err)
			}
			b.WriteString(s + "\n")
		}
		rows.Close()
	}
	return b.String()
}

func TestTheDeviceFlowKeepsTheTokensOnlyInTheVaultAndActsAsThePerson(t *testing.T) {
	t.Parallel()
	r := newPersonal(t)
	ch, err := r.p.Begin(r.ctx, r.owner.ID)
	if err != nil || ch.UserCode == "" || !strings.HasPrefix(ch.VerificationURI, "https://") {
		t.Fatalf("begin = %+v, %v", ch, err)
	}
	// too soon to ask: nothing is sent, and the answer is "not yet"
	if res, err := r.p.Poll(r.ctx, r.owner.ID); err != nil || res != github.PollPending {
		t.Fatalf("poll before the interval = %q, %v", res, err)
	}
	r.clk.Advance(ch.Interval)
	if res, err := r.p.Poll(r.ctx, r.owner.ID); err != nil || res != github.PollPending {
		t.Fatalf("poll before the user authorized = %q, %v", res, err)
	}
	r.gh.AuthorizeDevice(ch.UserCode, "Lucas") // GitHub spells the login its way
	r.clk.Advance(ch.Interval)
	if res, err := r.p.Poll(r.ctx, r.owner.ID); err != nil || res != github.PollConnected {
		t.Fatalf("poll after the authorization = %q, %v", res, err)
	}
	st, err := r.p.Status(r.ctx, r.owner.ID)
	if err != nil || st.State != github.CredentialConnected || !strings.EqualFold(st.Login, "lucas") || st.ExpiresAt.IsZero() {
		t.Fatalf("status = %+v, %v", st, err)
	}
	cl, err := r.p.Client(r.ctx, r.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if login, err := cl.CurrentLogin(r.ctx); err != nil || !strings.EqualFold(login, "lucas") {
		t.Fatalf("the client acts as %q, %v", login, err)
	}
	// the tokens are in the vault, and nowhere else
	tok := r.token()
	for _, never := range []string{"fake-device-", "client-secret-gm"} {
		if strings.Contains(r.everythingElse(), never) {
			t.Fatalf("%q reached the logs or the database", never)
		}
	}
	for _, secret := range []string{tok} {
		for _, part := range strings.FieldsFunc(secret, func(c rune) bool { return c == '"' || c == ',' || c == ':' || c == '{' || c == '}' }) {
			if strings.HasPrefix(part, "ghu_") || strings.HasPrefix(part, "ghr_") {
				if strings.Contains(r.everythingElse(), part) {
					t.Fatalf("a token reached the logs or the database: %s", part[:8])
				}
			}
		}
	}
	if !strings.Contains(tok, "ghu_") || !strings.Contains(tok, "ghr_") {
		t.Fatalf("the vault holds %q, want the access and the refresh token", tok)
	}
	// a flow is spent by its token
	if _, err := r.p.Poll(r.ctx, r.owner.ID); !errors.Is(err, github.ErrNoDeviceFlow) {
		t.Fatalf("poll of a spent flow = %v", err)
	}
}

// CA-GH-APROV-06
func TestATokenOfAnotherAccountThanTheRegisteredLoginIsDiscarded(t *testing.T) {
	t.Parallel()
	r := newPersonal(t)
	err := r.connect("mallory")
	if !errors.Is(err, github.ErrLoginMismatch) {
		t.Fatalf("connect as another account = %v, want ErrLoginMismatch", err)
	}
	if r.kv.has(github.UserTokenSecret(r.owner.ID)) {
		t.Fatal("the token of another account was kept")
	}
	if st, _ := r.p.Status(r.ctx, r.owner.ID); st.State != "" {
		t.Fatalf("status = %+v, want none", st)
	}
	if _, err := r.p.Client(r.ctx, r.owner.ID); !errors.Is(err, github.ErrNoCredential) {
		t.Fatalf("client = %v, want no credential", err)
	}
	if len(r.gh.RevokedTokens()) != 1 {
		t.Fatalf("revoked = %v, want the discarded token taken back at GitHub", r.gh.RevokedTokens())
	}
	for _, p := range r.gh.RevokePaths() {
		if !strings.HasSuffix(p, "/token") {
			t.Errorf("a discarded token was revoked by %s: that takes the whole grant of the account", p)
		}
	}
}

func TestAnotherUserWithTheSameLoginDoesNotTakeTheCredential(t *testing.T) {
	t.Parallel()
	r := newPersonal(t)
	ana, err := r.users.Create(r.ctx, r.owner, users.NewUser{Name: "Ana", Level: users.Administrator, GitHubLogin: "LUCAS"})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := r.p.Begin(r.ctx, ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	r.gh.AuthorizeDevice(ch.UserCode, "lucas")
	r.clk.Advance(ch.Interval)
	if _, err := r.p.Poll(r.ctx, ana.ID); !errors.Is(err, github.ErrLoginMismatch) {
		t.Fatalf("poll = %v, want the token refused: the login is the Owner's too", err)
	}
	if r.kv.has(github.UserTokenSecret(ana.ID)) {
		t.Fatal("a token of a shared login was kept")
	}
}

func TestTheDeviceFlowRefusalsAreTold(t *testing.T) {
	t.Parallel()
	r := newPersonal(t)
	// slow down: the interval grows and the poll is not spent
	ch, _ := r.p.Begin(r.ctx, r.owner.ID)
	r.gh.SlowDown(ch.UserCode, 1)
	r.clk.Advance(ch.Interval)
	if res, err := r.p.Poll(r.ctx, r.owner.ID); err != nil || res != github.PollPending {
		t.Fatalf("slow_down = %q, %v", res, err)
	}
	r.clk.Advance(ch.Interval) // the interval is longer now: still too soon
	r.gh.AuthorizeDevice(ch.UserCode, "lucas")
	if res, _ := r.p.Poll(r.ctx, r.owner.ID); res != github.PollPending {
		t.Fatalf("poll inside the longer interval = %q", res)
	}
	r.clk.Advance(10 * time.Second)
	if res, err := r.p.Poll(r.ctx, r.owner.ID); err != nil || res != github.PollConnected {
		t.Fatalf("poll after slow_down = %q, %v", res, err)
	}
	// denied
	ch, _ = r.p.Begin(r.ctx, r.owner.ID)
	r.gh.DenyDevice(ch.UserCode)
	r.clk.Advance(ch.Interval)
	if _, err := r.p.Poll(r.ctx, r.owner.ID); !errors.Is(err, github.ErrDeviceDenied) {
		t.Fatalf("denied = %v", err)
	}
	// expired
	if _, err := r.p.Begin(r.ctx, r.owner.ID); err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(16 * time.Minute)
	if _, err := r.p.Poll(r.ctx, r.owner.ID); !errors.Is(err, github.ErrDeviceExpired) {
		t.Fatalf("expired = %v", err)
	}
	// the device flow off in the App
	r.gh.SetDeviceFlow(r.app.Slug, false)
	if _, err := r.p.Begin(r.ctx, r.owner.ID); !errors.Is(err, github.ErrDeviceFlowOff) {
		t.Fatalf("begin with the flow off = %v", err)
	}
}

func TestWithoutALoginRegisteredNothingIsBegun(t *testing.T) {
	t.Parallel()
	r := newPersonal(t)
	ana, err := r.users.Create(r.ctx, r.owner, users.NewUser{Name: "Ana", Level: users.Administrator})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Begin(r.ctx, ana.ID); !errors.Is(err, github.ErrNoGitHubLogin) {
		t.Fatalf("begin = %v, want ErrNoGitHubLogin", err)
	}
	obs, _ := r.users.Create(r.ctx, r.owner, users.NewUser{Name: "Bia", Level: users.Observer, GitHubLogin: "bia"})
	if _, err := r.p.Begin(r.ctx, obs.ID); !errors.Is(err, github.ErrNoCredential) {
		t.Fatalf("begin of an Observer = %v", err)
	}
}

func TestTheTokenIsRenewedBeforeItEndsAndARefusedRenewalExpiresTheCredential(t *testing.T) {
	t.Parallel()
	r := newPersonal(t)
	r.gh.SetUserTokenLife(30 * time.Minute)
	if err := r.connect("lucas"); err != nil {
		t.Fatal(err)
	}
	before := r.token()
	r.clk.Advance(10 * time.Minute) // far from the end: the same token
	if _, err := r.p.Client(r.ctx, r.owner.ID); err != nil || r.token() != before {
		t.Fatalf("client early = %v, token changed %v", err, r.token() != before)
	}
	r.clk.Advance(17 * time.Minute) // inside the last 5 minutes
	cl, err := r.p.Client(r.ctx, r.owner.ID)
	if err != nil || r.token() == before {
		t.Fatalf("client near the end = %v, token renewed %v", err, r.token() != before)
	}
	if _, err := cl.CurrentLogin(r.ctx); err != nil {
		t.Fatalf("the renewed token does not work: %v", err)
	}
	if strings.Contains(r.everythingElse(), "ghr_") || strings.Contains(r.everythingElse(), "ghu_") {
		t.Fatal("a token reached the logs or the database")
	}
	// the renewal is refused: the credential expires and stays so
	r.gh.RefuseRefresh(true)
	r.clk.Advance(26 * time.Minute)
	if _, err := r.p.Client(r.ctx, r.owner.ID); !errors.Is(err, github.ErrNoCredential) {
		t.Fatalf("client after a refused renewal = %v", err)
	}
	if st, _ := r.p.Status(r.ctx, r.owner.ID); st.State != github.CredentialExpired {
		t.Fatalf("status = %+v, want expired", st)
	}
	r.gh.RefuseRefresh(false)
	if _, err := r.p.Client(r.ctx, r.owner.ID); !errors.Is(err, github.ErrNoCredential) {
		t.Fatalf("an expired credential came back by itself: %v", err)
	}
	// connecting again makes it work
	if err := r.connect("lucas"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Client(r.ctx, r.owner.ID); err != nil {
		t.Fatalf("after connecting again = %v", err)
	}
}

func TestASuspendedUserOrAChangedLoginHasNoCredentialInUseAndRemovingErasesIt(t *testing.T) {
	t.Parallel()
	r := newPersonal(t)
	ana, err := r.users.Create(r.ctx, r.owner, users.NewUser{Name: "Ana", Level: users.Administrator, GitHubLogin: "ana", Workspaces: []string{"meu-app"}})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := r.p.Begin(r.ctx, ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	r.gh.AuthorizeDevice(ch.UserCode, "ana")
	r.clk.Advance(ch.Interval)
	if _, err := r.p.Poll(r.ctx, ana.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Client(r.ctx, ana.ID); err != nil {
		t.Fatal(err)
	}
	other := "ana2"
	if _, err := r.users.Edit(r.ctx, r.owner, ana.ID, users.Edit{GitHubLogin: &other}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Client(r.ctx, ana.ID); !errors.Is(err, github.ErrNoCredential) {
		t.Fatalf("after the login changed = %v", err)
	}
	back := "ana"
	if _, err := r.users.Edit(r.ctx, r.owner, ana.ID, users.Edit{GitHubLogin: &back}); err != nil {
		t.Fatal(err)
	}
	if err := r.users.Suspend(r.ctx, r.owner, ana.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.Client(r.ctx, ana.ID); !errors.Is(err, github.ErrNoCredential) {
		t.Fatalf("suspended = %v", err)
	}
	if err := r.p.Remove(r.ctx, ana.ID); err != nil {
		t.Fatal(err)
	}
	if r.kv.has(github.UserTokenSecret(ana.ID)) {
		t.Fatal("the tokens of a removed credential are still in the vault")
	}
	if st, _ := r.p.Status(r.ctx, ana.ID); st.State != "" {
		t.Fatalf("status = %+v", st)
	}
	if len(r.gh.RevokedTokens()) != 1 {
		t.Fatalf("revoked = %v, want the token taken back at GitHub", r.gh.RevokedTokens())
	}
	if err := r.p.Remove(r.ctx, ana.ID); err != nil {
		t.Fatalf("removing twice = %v", err)
	}
}
