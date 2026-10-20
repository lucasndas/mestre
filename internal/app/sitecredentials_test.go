package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

const (
	siteValue = `{"url":"https://loja.example/login","user":"qa@x","password":"Abc123!senha-longa"}`
	siteName  = "site_loja"
)

// credWorld is a Citadel with its vault, its registry of characters and the broker
// of credentials, and a Tower connected to it over the protocol (the in-memory
// connection of the fast suite).
type credWorld struct {
	t      *testing.T
	ctx    context.Context
	db     *store.DB
	server *protocol.Server
	client *protocol.Client
	nw     protorig.Network
	stopS  context.CancelFunc
	wgS    sync.WaitGroup
	stopC  context.CancelFunc
	wgC    sync.WaitGroup
	// broker is the one of the Citadel; reg its registry and vault its vault.
	broker *CredentialBroker
	reg    characters.Registry
	vault  *vault.Vault
}

func newCredWorld(t *testing.T) *credWorld {
	t.Helper()
	ctx := t.Context()
	clk := clock.Real{} // the clock of the synctest bubble
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rk, err := vault.NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	mask := logging.NewMasker()
	v, err := vault.Create(ctx, vault.Config{DB: db, Keys: &memKeyStore{}, EnvelopePath: filepath.Join(t.TempDir(), "cofre-envelope.bin"),
		RecoveryPublicKey: rk.PublicKey(), Masker: mask, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	if err := v.Put(ctx, siteName, vault.TypeSiteCredential, []byte(siteValue),
		&vault.SiteMeta{OwnerUser: "ana", Classes: []string{"qa"}, Domains: []string{"loja.example"}, Kind: vault.KindAppPassword}); err != nil {
		t.Fatal(err)
	}
	// the characters of the Tower: one of a class that releases the credential, one
	// of a class that does not list it, one whose class lists it but the credential
	// does not list the class
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('loja', 'lucas/loja', 'lucas/loja', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	reg := characters.Registry{DB: db}
	for i, c := range []struct{ id, class string }{{"qa1", "qa"}, {"dev1", "dev"}, {"ops1", "ops"}} {
		if err := reg.Insert(ctx, characters.Character{ID: c.id, Name: c.id, Kind: characters.KindCharacter, Role: characters.RoleCharacter, Workspace: "loja", Table: "1-x",
			Machine: "tower1", TerminalID: fmt.Sprintf("t%d", i), Sheet: "Dev Claude", Base: "claude-code", Class: c.class, State: characters.Idle, Lifecycle: characters.Persistent,
			CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, CreatedAt: clk.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	broker := &CredentialBroker{Vault: v, Registry: reg, Clock: clk, Mask: mask,
		Rules: func(_ context.Context, id string) (*classes.Rules, error) {
			r := &classes.Rules{}
			switch id {
			case "qa1", "ops1":
				r.Network.Credentials = []string{siteName} // the class lists it in network.credentials
			}
			return r, nil
		}}
	w := &credWorld{t: t, ctx: ctx, db: db, nw: protorig.Network{Net: fakenet.New(clk)}, broker: broker, reg: reg, vault: v}
	cfgC, _ := protorig.Config(clk, "citadel", 1, "citadel")
	cfgC.Router = protocol.NewRouter()
	cfgC.Router.HandleFunc(protocol.TypeVaultRequest, broker.Handle)
	if w.server, err = protocol.NewServer(cfgC); err != nil {
		t.Fatal(err)
	}
	lis, err := w.nw.Listen("citadel:7443", protocol.Peer{Role: protocol.RoleMachine, ID: "citadel"})
	if err != nil {
		t.Fatal(err)
	}
	var sctx, cctx context.Context
	sctx, w.stopS = context.WithCancel(ctx)
	w.wgS.Go(func() { _ = w.server.Serve(sctx, lis) })
	cfgT, _ := protorig.Config(clk, "tower1", 1, "citadel")
	cfgT.Router = protocol.NewRouter()
	if w.client, err = protocol.NewClient(cfgT, w.nw.Dialer("tower1", "citadel:7443", protocol.Peer{Role: protocol.RoleMachine, ID: "tower1"})); err != nil {
		t.Fatal(err)
	}
	cctx, w.stopC = context.WithCancel(ctx)
	w.wgC.Go(func() { _ = w.client.Run(cctx) })
	synctest.Wait()
	return w
}

func (w *credWorld) stop() {
	w.stopC()
	w.stopS()
	w.wgC.Wait()
	w.wgS.Wait()
}

// audit are the rows of the requests, as "machine action detail".
func (w *credWorld) audit() []string {
	w.t.Helper()
	rows, err := w.db.Reader().QueryContext(w.ctx, `SELECT machine_id, action, detail FROM audit_log WHERE action = ? ORDER BY at, id`, AuditVaultRequest)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m, a, d string
		if err := rows.Scan(&m, &a, &d); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, m+" "+a+" "+d)
	}
	return out
}

// CA-COFRE-21
func TestATowerGetsASiteCredentialOnlyForACharacterWhoseClassReleasesItAndEverythingIsAudited(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newCredWorld(t)
		defer w.stop()
		link := w.client.Link()
		if !link.Connected() {
			t.Fatal("the Tower did not connect to the Citadel")
		}

		// a character whose class does not list the credential: refused, nothing given
		for _, who := range []string{"dev1", "ops1"} { // ops1: its class lists it, the credential does not list the class
			got, err := RequestSiteCredential(w.ctx, clock.Real{}, link, who, siteName, "login em loja.example")
			var ce *CredentialError
			if !errors.As(err, &ce) || ce.Code != protocol.CodeForbidden || got != "" {
				t.Fatalf("%s got %q, %v; want forbidden and no value", who, got, err)
			}
			if strings.Contains(ce.Message, "Abc123") {
				t.Fatalf("the refusal tells the value: %q", ce.Message)
			}
		}
		// a character of a class that releases it: the value, in memory
		got, err := RequestSiteCredential(w.ctx, clock.Real{}, link, "qa1", siteName, "login em loja.example")
		if err != nil || got.Reveal() != siteValue {
			t.Fatalf("qa1 got %v, %v; want the credential", got, err)
		}
		if fmt.Sprint(got) != logging.Redacted || fmt.Sprintf("%#v", got) == siteValue {
			t.Errorf("the value prints as %v", got)
		}
		// a Tower speaks only of its own characters: a character that is not on that machine
		if _, err := RequestSiteCredential(w.ctx, clock.Real{}, link, "ghost", siteName, "x"); err == nil {
			t.Error("a character the registry does not put on the machine got the credential")
		}

		// the audit has the four requests (machine, character, purpose, time) and no value
		rows := w.audit()
		if len(rows) != 4 {
			t.Fatalf("audit = %q, want 4 requests", rows)
		}
		for _, want := range []struct{ who, outcome string }{{"dev1", "refused"}, {"ops1", "refused"}, {"qa1", "released"}, {"ghost", "refused"}} {
			found := false
			for _, r := range rows { // the rows of one instant have no order of their own
				if strings.Contains(r, `"character":"`+want.who+`"`) {
					found = true
					if !strings.HasPrefix(r, "tower1 vault.request ") || !strings.Contains(r, `"outcome":"`+want.outcome+`"`) ||
						!strings.Contains(r, `"purpose":"`) || !strings.Contains(r, `"secret":"`+siteName+`"`) {
						t.Errorf("audit row of %s = %q, want %s by tower1", want.who, r, want.outcome)
					}
				}
			}
			if !found {
				t.Errorf("no audit row of %s: %q", want.who, rows)
			}
		}
		var timed int
		if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM audit_log WHERE action = ? AND at > 0`, AuditVaultRequest).Scan(&timed); err != nil || timed != 4 {
			t.Errorf("%d requests have the time, %v; want 4", timed, err)
		}
		// nowhere in the audit of the Citadel
		var n int
		if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM audit_log WHERE detail LIKE '%senha-longa%' OR detail LIKE '%Abc123%' OR action LIKE '%Abc123%'`).Scan(&n); err != nil || n != 0 {
			t.Errorf("the audit holds the value: %d, %v", n, err)
		}

		// with the Citadel away: citadel_unavailable
		w.stopS()
		w.wgS.Wait()
		synctest.Wait()
		_, err = RequestSiteCredential(w.ctx, clock.Real{}, link, "qa1", siteName, "login em loja.example")
		var ce *CredentialError
		if !errors.As(err, &ce) || ce.Code != localchan.CodeCitadelUnavailable {
			t.Fatalf("with the Citadel away: %v, want citadel_unavailable", err)
		}
		if n := len(w.audit()); n != 4 {
			t.Errorf("the request that never arrived is in the audit: %d rows", n)
		}
	})
}

// Part of CA-COFRE-21: a Tower gets nothing when its machine is excluded (it keeps no secrets), when the
// character is not alive (stopped or interrupted), when the credential expired or when no purpose
// is given; every request leaves a row of the audit, also the refusals and the errors of the engine,
// and the refusal says the same words whatever the reason.
func TestACredentialIsNotGivenToAnExcludedMachineADeadCharacterAnExpiredCredentialOrWithoutPurpose(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newCredWorld(t)
		defer w.stop()
		past := clock.Real{}.Now().Add(-time.Hour)
		if err := w.vault.Put(w.ctx, "site_velho", vault.TypeSiteCredential, []byte(siteValue),
			&vault.SiteMeta{OwnerUser: "ana", Classes: []string{"qa"}, Domains: []string{"loja.example"}, Kind: vault.KindAppPassword, ExpiresAt: &past}); err != nil {
			t.Fatal(err)
		}
		rules := w.broker.Rules
		w.broker.Rules = func(ctx context.Context, id string) (*classes.Rules, error) {
			r, err := rules(ctx, id)
			r.Network.Credentials = append(r.Network.Credentials, "site_velho")
			return r, err
		}
		ask := func(who, secret, purpose string) (string, error) {
			return w.broker.Release(w.ctx, "tower1", protocol.VaultRequest{AgentID: who, Secret: secret, Purpose: purpose})
		}
		words := map[string]bool{}
		check := func(what, who, secret, purpose string) {
			t.Helper()
			v, err := ask(who, secret, purpose)
			var pe *protocol.Error
			if err == nil || v != "" || !errors.As(err, &pe) || pe.Code != protocol.CodeForbidden {
				t.Errorf("%s: got %q, %v; want forbidden and no value", what, v, err)
				return
			}
			words[pe.Message] = true
		}
		check("the credential expired", "qa1", "site_velho", "login")
		check("no purpose", "qa1", siteName, "  ")
		w.broker.Excluded = func(context.Context, string) (bool, error) { return true, nil }
		check("an excluded machine", "qa1", siteName, "login")
		w.broker.Excluded = func(context.Context, string) (bool, error) { return false, nil }
		for _, st := range []characters.State{characters.Stopped, characters.Interrupted, characters.Stopping} {
			if _, _, err := w.reg.Transition(w.ctx, "qa1", st, characters.Change{Reason: "test", At: clock.Real{}.Now()}); err != nil {
				// the transition graph does not go there from idle: set it straight
				if _, err := w.db.Writer().ExecContext(w.ctx, `UPDATE characters SET state = ? WHERE id = 'qa1'`, string(st)); err != nil {
					t.Fatal(err)
				}
			}
			check("a character that is "+string(st), "qa1", siteName, "login")
		}
		if len(words) != 1 {
			t.Errorf("the refusals have %d different texts %v; want one", len(words), words)
		}
		if _, err := w.db.Writer().ExecContext(w.ctx, `UPDATE characters SET state = 'idle' WHERE id = 'qa1'`); err != nil {
			t.Fatal(err)
		}
		if v, err := ask("qa1", siteName, "login"); err != nil || v != siteValue {
			t.Fatalf("a live character of a machine that is not excluded: %q, %v", v, err)
		}
		// an error of the engine is a row too
		w.broker.Excluded = func(context.Context, string) (bool, error) { return false, errors.New("boom") }
		if _, err := ask("qa1", siteName, "login"); err == nil {
			t.Fatal("an error of the engine let the request through")
		}
		rows := w.audit() // the rows of one instant have no order of their own
		var errs, released, refused int
		for _, r := range rows {
			switch {
			case strings.Contains(r, `"outcome":"error"`):
				errs++
			case strings.Contains(r, `"outcome":"released"`):
				released++
			case strings.Contains(r, `"outcome":"refused"`):
				refused++
			}
		}
		if len(rows) != 8 || errs != 1 || released != 1 || refused != 6 {
			t.Errorf("audit = %q; want a row for each of the 8 requests: 6 refused, 1 released, 1 error", rows)
		}
	})
}
