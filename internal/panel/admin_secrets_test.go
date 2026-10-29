package panel_test

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/internal/vault"
)

const (
	sitePassword = "SENHA-ULTRA-SECRETA-123"
	siteUser     = "usuario-do-site@exemplo.com"
)

func siteInput(name string) panel.SiteCredentialInput {
	return panel.SiteCredentialInput{
		Name: name, URL: "https://exemplo.com/entrar", User: siteUser, Password: sitePassword,
		Selectors: map[string]string{"user": "#u"}, Success: "Painel",
		Classes: []string{"pesquisador"}, Domains: []string{"exemplo.com"}, Kind: "senha_de_app",
	}
}

func readSecret(t *testing.T, a *adminRig, name string) string {
	t.Helper()
	var got string
	if err := a.vault.Use(a.ctx, name, "test", func(b []byte) error { got = string(b); return nil }); err != nil {
		t.Fatal(err)
	}
	return got
}

// CA-ADM-03 and CA-COFRE-07: the answer of the API of the secrets has the name, the
// type, the fingerprint, the dates and the metadata, never the value nor a part
// of it, for the Owner too; the others are refused with the level.
func TestTheSecretsAPIAnswersWithNoValueNorPartOfItEvenToTheOwner(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	if err := a.vault.Put(a.ctx, "gmail", vault.TypeSiteCredential, []byte(`{"url":"https://mail.exemplo.com","user":"`+siteUser+`","password":"`+sitePassword+`"}`),
		&vault.SiteMeta{OwnerUser: a.owner.ID, Classes: []string{"pesquisador"}, Domains: []string{"mail.exemplo.com"}, Kind: vault.KindMainPassword}); err != nil {
		t.Fatal(err)
	}
	if err := a.vault.Put(a.ctx, "provedor", vault.TypeProviderAPIKey, []byte("sk-chave-de-provedor-qwxz"), nil); err != nil {
		t.Fatal(err)
	}
	w := a.call("GET", "/api/secrets", nil, opts{cookie: tok})
	if w.Code != 200 {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	// the fragment has letters outside hex, so the fingerprint cannot hold it by chance
	for _, leak := range []string{sitePassword, siteUser, "SECRETA", "sk-chave", "qwxz", "mail.exemplo.com\",\"user"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("the list has %q: %s", leak, w.Body)
		}
	}
	list := decodeInto[panel.SecretList](t, w)
	if len(list.Secrets) != 2 {
		t.Fatalf("list = %+v", list)
	}
	site := list.Secrets[0]
	if site.Name != "gmail" || site.Type != "site_credential" || site.Fingerprint == "" || site.Site == nil || site.Site.Domains[0] != "mail.exemplo.com" || site.Site.Kind != "senha_principal" {
		t.Fatalf("the credential of site is shown as %+v", site)
	}
	// the other doors of the secrets do not give the value back either
	for _, path := range []string{"/api/secrets/gmail", "/api/secrets/gmail/value", "/api/secrets?reveal=1"} {
		if w := a.call("GET", path, nil, opts{cookie: tok}); strings.Contains(w.Body.String(), sitePassword) {
			t.Errorf("%s gives the value: %s", path, w.Body)
		}
	}
	_, admTok := a.member("Bruno", "bruno", users.Administrator)
	a.wantErr(a.call("GET", "/api/secrets", nil, opts{cookie: admTok}), 403, panel.CodeForbiddenLevel)
	a.wantErr(a.call("POST", "/api/secrets/site", siteInput("x"), opts{cookie: admTok, pin: "654321"}), 403, panel.CodeForbiddenLevel)
	a.wantErr(a.call("DELETE", "/api/secrets/gmail", nil, opts{cookie: admTok, pin: "654321"}), 403, panel.CodeForbiddenLevel)
}

// CA-COFRE-07: replacing and deleting ask for the PIN, and nothing changes without it.
func TestReplacingAndDeletingASecretAsksForThePINAndTheAuditNeverHasTheValue(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	in := siteInput("Banco do Lucas")
	a.wantErr(a.call("POST", "/api/secrets/site", in, opts{cookie: tok}), 403, panel.CodePINRequired)
	w := a.call("POST", "/api/secrets/site", in, opts{cookie: tok, pin: ownerPIN})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), sitePassword) || strings.Contains(w.Body.String(), siteUser) {
		t.Fatalf("the answer of the registration has the value: %s", w.Body)
	}
	row := decodeInto[panel.SecretRow](t, w)
	if row.Site == nil || row.Site.OwnerUser != a.owner.ID || row.Managed { // the default owner of the credential is the Owner
		t.Fatalf("row = %+v", row)
	}
	a.wantErr(a.call("POST", "/api/secrets/site", in, opts{cookie: tok, pin: ownerPIN}), 409, panel.CodeSecretExists)

	newOne := siteInput("ignorado")
	newOne.Password = "OUTRA-SENHA-NOVA-456"
	path := "/api/secrets/" + "Banco%20do%20Lucas"
	a.wantErr(a.call("PUT", path, panel.ReplaceSecretRequest{Site: &newOne}, opts{cookie: tok}), 403, panel.CodePINRequired)
	if !strings.Contains(readSecret(t, a, "Banco do Lucas"), sitePassword) {
		t.Fatal("the credential changed without the PIN")
	}
	w = a.call("PUT", path, panel.ReplaceSecretRequest{Site: &newOne}, opts{cookie: tok, pin: ownerPIN})
	if w.Code != 200 || strings.Contains(w.Body.String(), "OUTRA-SENHA") {
		t.Fatalf("replace = %d %s", w.Code, w.Body)
	}
	if !strings.Contains(readSecret(t, a, "Banco do Lucas"), "OUTRA-SENHA-NOVA-456") {
		t.Fatal("the credential was not replaced")
	}
	a.wantErr(a.call("DELETE", path, nil, opts{cookie: tok}), 403, panel.CodePINRequired)
	if _, err := a.vault.Info(a.ctx, "Banco do Lucas"); err != nil {
		t.Fatal("the credential was deleted without the PIN")
	}
	if w := a.call("DELETE", path, nil, opts{cookie: tok, pin: ownerPIN}); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", w.Code, w.Body)
	}
	if _, err := a.vault.Info(a.ctx, "Banco do Lucas"); err == nil {
		t.Fatal("the credential is still there")
	}
	// the audit has who and before/after, and no value
	all := a.auditText()
	for _, leak := range []string{sitePassword, "OUTRA-SENHA", siteUser} {
		if strings.Contains(all, leak) {
			t.Errorf("the audit has %q", leak)
		}
	}
	for _, act := range []string{"panel.secret.created", "panel.secret.replaced", "panel.secret.deleted"} {
		if a.audited(act) != 1 {
			t.Errorf("%s is not in the audit", act)
		}
	}
	var detail string
	if err := a.db.Reader().QueryRowContext(a.ctx, `SELECT detail FROM audit_log WHERE action = 'panel.secret.replaced'`).Scan(&detail); err != nil ||
		!strings.Contains(detail, a.owner.ID) || !strings.Contains(detail, `"before":{`) || !strings.Contains(detail, `"after":{`) || !strings.Contains(detail, `"fingerprint"`) {
		t.Fatalf("the audit line of the replacement = %s (%v)", detail, err)
	}
}

func TestSiteCredentialsAreCheckedAndTheSecretsTheMestreKeepsAreNotChanged(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	pinned := opts{cookie: tok, pin: ownerPIN}
	for field, mutate := range map[string]func(*panel.SiteCredentialInput){
		"url":      func(i *panel.SiteCredentialInput) { i.URL = "javascript:alert(1)" },
		"password": func(i *panel.SiteCredentialInput) { i.Password = "" },
		"kind":     func(i *panel.SiteCredentialInput) { i.Kind = "outra" },
		"classes":  func(i *panel.SiteCredentialInput) { i.Classes = []string{" "} },
		"domains":  func(i *panel.SiteCredentialInput) { i.Domains = nil },
		"name":     func(i *panel.SiteCredentialInput) { i.Name = "github_user_token:abc" },
	} {
		in := siteInput("ok")
		mutate(&in)
		w := a.call("POST", "/api/secrets/site", in, pinned)
		a.wantErr(w, 422, panel.CodeInvalidSecret)
		if !strings.Contains(w.Body.String(), `"`+field+`"`) {
			t.Errorf("the refusal does not name %s: %s", field, w.Body)
		}
	}
	in := siteInput("com validade")
	exp := a.clk.Now().Add(30 * 24 * time.Hour)
	in.ExpiresAt = &exp
	a.wantOK(a.call("POST", "/api/secrets/site", in, pinned))
	// a secret that the Mestre keeps is listed and is not replaced nor deleted by the panel
	if err := a.vault.Put(a.ctx, "github_app_key:grao-mestre", vault.TypeGitHubAppKey, []byte("-----BEGIN KEY-----\nabc\n-----END KEY-----"), nil); err != nil {
		t.Fatal(err)
	}
	managed := "/api/secrets/github_app_key:grao-mestre"
	a.wantErr(a.call("PUT", managed, panel.ReplaceSecretRequest{Value: "x"}, pinned), 409, panel.CodeSecretManaged)
	a.wantErr(a.call("DELETE", managed, nil, pinned), 409, panel.CodeSecretManaged)
	a.wantErr(a.call("DELETE", "/api/secrets/nao-existe", nil, pinned), 404, panel.CodeNotFound)
	list := decodeInto[panel.SecretList](t, a.call("GET", "/api/secrets", nil, opts{cookie: tok}))
	var found bool
	for _, s := range list.Secrets {
		if s.Name == "github_app_key:grao-mestre" {
			found = s.Managed
		}
	}
	if !found {
		t.Error("the secret of the Mestre is not listed as managed")
	}
}

func TestTheValidityNoticesShowWhatExpiresAndWhatIsOldAndNeverExpires(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	put := func(name string, exp *time.Time) {
		in := siteInput(name)
		in.ExpiresAt = exp
		a.wantOK(a.call("POST", "/api/secrets/site", in, opts{cookie: tok, pin: ownerPIN}))
	}
	now := a.clk.Now()
	in3, past, far := now.Add(3*24*time.Hour), now.Add(-2*24*time.Hour), now.Add(60*24*time.Hour)
	put("vence-logo", &in3)
	put("venceu", &past)
	put("longe", &far)
	put("sem-validade", nil)
	a.clk.Advance(91 * 24 * time.Hour)
	tok = loginAs(a.rig, ownerLogin, ownerPass) // the session of before is over
	put("recente", nil)
	got := map[string]*panel.SecretNotice{}
	for _, s := range decodeInto[panel.SecretList](t, a.call("GET", "/api/secrets", nil, opts{cookie: tok})).Secrets {
		got[s.Name] = s.Notice
	}
	if n := got["venceu"]; n == nil || n.Kind != "expired" || n.Days != 93 {
		t.Errorf("venceu: %+v", n)
	}
	if n := got["sem-validade"]; n == nil || n.Kind != "stale" || n.Days != 91 {
		t.Errorf("sem-validade: %+v", n)
	}
	if n := got["recente"]; n != nil {
		t.Errorf("recente: %+v", n)
	}
	if n := got["longe"]; n == nil || n.Kind != "expired" { // 91 days later, it expired too
		t.Errorf("longe: %+v", n)
	}
}

func TestExpiringNoticeCountsTheDaysLeft(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	in := siteInput("vence-logo")
	exp := a.clk.Now().Add(3*24*time.Hour - time.Hour)
	in.ExpiresAt = &exp
	a.wantOK(a.call("POST", "/api/secrets/site", in, opts{cookie: tok, pin: ownerPIN}))
	s := decodeInto[panel.SecretList](t, a.call("GET", "/api/secrets", nil, opts{cookie: tok})).Secrets[0]
	if s.Notice == nil || s.Notice.Kind != "expiring" || s.Notice.Days != 3 {
		t.Fatalf("notice = %+v", s.Notice)
	}
}

// CA-COFRE-20: the new recovery key asks for the PIN and for the reading of the
// warning about the old backups, and the envelope of the next backup opens only with
// the new key.
func TestANewRecoveryKeyNeedsThePINAndTheWarningAndTheNextEnvelopeOpensOnlyWithIt(t *testing.T) {
	t.Parallel()
	a := newAdmin(t)
	tok := a.ownerSession()
	if err := a.vault.Put(a.ctx, "provedor", vault.TypeProviderAPIKey, []byte("sk-chave-de-provedor-0987"), nil); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(a.env)
	if err != nil {
		t.Fatal(err)
	}
	a.wantErr(a.call("POST", "/api/recovery-key", panel.RecoveryKeyRequest{Acknowledged: true}, opts{cookie: tok}), 403, panel.CodePINRequired)
	a.wantErr(a.call("POST", "/api/recovery-key", panel.RecoveryKeyRequest{}, opts{cookie: tok, pin: ownerPIN}), 422, panel.CodeAckRequired)
	if a.told.Load() != 0 {
		t.Fatal("the Towers were told before the key was made")
	}
	if now, _ := os.ReadFile(a.env); string(now) != string(before) {
		t.Fatal("the envelope changed before the Owner confirmed")
	}
	_, admTok := a.member("Bruno", "bruno", users.Administrator)
	a.wantErr(a.call("POST", "/api/recovery-key", panel.RecoveryKeyRequest{Acknowledged: true}, opts{cookie: admTok, pin: "654321"}), 403, panel.CodeForbiddenLevel)

	w := a.call("POST", "/api/recovery-key", panel.RecoveryKeyRequest{Acknowledged: true}, opts{cookie: tok, pin: ownerPIN})
	if w.Code != 200 {
		t.Fatalf("new recovery key = %d %s", w.Code, w.Body)
	}
	fresh, err := vault.ParseRecoveryKey(decodeInto[panel.RecoveryKeyResponse](t, w).Key)
	if err != nil {
		t.Fatalf("the key shown does not parse: %v", err)
	}
	env, err := os.ReadFile(a.env)
	if err != nil {
		t.Fatal(err)
	}
	if err := vault.RestoreKey(a.ctx, env, a.rk, &memKeys{}); err == nil {
		t.Fatal("the old recovery key opens the envelope of the next backup")
	}
	if a.told.Load() != 1 {
		t.Errorf("the Towers were told %d times that the vault changed, want 1", a.told.Load())
	}
	if err := vault.RestoreKey(a.ctx, env, fresh, &memKeys{}); err != nil {
		t.Fatalf("the new recovery key does not open it: %v", err)
	}
	if strings.Contains(a.auditText(), fresh.Reveal()) || a.audited("panel.recovery_key.renewed") != 1 {
		t.Error("the audit has the key, or does not have the fact")
	}
}
