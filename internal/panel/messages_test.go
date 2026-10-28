package panel_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/panel"
)

// scopeless turns every proposal of the form into a delegation with no scope, which the
// real engine refuses: the panel only has to carry the refusal.
type scopeless struct{ *fakeEngine }

func (s scopeless) Propose(ctx context.Context, p decrees.Proposal) (decrees.Pending, error) {
	p.Effect = decrees.Effect{Kind: decrees.EffectDelegation, Setting: "envio_workflows"}
	p.Scope = ""
	return s.Service.Propose(ctx, p)
}

// CA-MSG-04
func TestARefusalIsAnsweredWithItsCodeAndParamsAndTheTextInTheLanguageOfWhoAsked(t *testing.T) {
	t.Parallel()
	r := newRules(t)
	r.panel.SetRules(scopeless{r.eng})
	owner := r.ownerSession()
	rule := panel.DecreeInput{Workspace: "loja", Kind: "rule", Effect: "permission", TargetKind: "class", TargetName: "dev", Field: "commands", Side: "allow", Value: "npm test*", Text: "dev pode rodar npm test"}
	body := func(o opts) panel.APIError {
		t.Helper()
		w := r.call("POST", "/api/decrees", rule, o)
		r.wantErr(w, 422, "decree.scope_required")
		return decodeInto[panel.ErrorBody](t, w).Error
	}

	// a user whose language is en
	if w := r.call("PUT", "/api/me/language", panel.LanguageRequest{Language: "en"}, opts{cookie: owner}); w.Code != 200 {
		t.Fatalf("language = %d %s", w.Code, w.Body)
	}
	e := body(opts{cookie: owner})
	want := i18n.Render("en", i18n.M("decree.scope_required"))
	if e.Message != want || !strings.Contains(want, "scope") {
		t.Errorf("message = %q, want %q", e.Message, want)
	}
	if strings.Contains(e.Message, "escopo") {
		t.Errorf("a sentence in Portuguese for a user in English: %q", e.Message)
	}
	if e.Params == nil && len(i18n.M("decree.scope_required").Params) != 0 {
		t.Errorf("no params: %+v", e)
	}

	// the language of the user beats the header of the browser
	if got := body(opts{cookie: owner, acceptLanguage: "pt-BR"}).Message; got != want {
		t.Errorf("with a pt-BR header: %q", got)
	}
	// the user chose pt-BR: Portuguese, whatever the header says
	_ = r.call("PUT", "/api/me/language", panel.LanguageRequest{Language: "pt-BR"}, opts{cookie: owner})
	if got := body(opts{cookie: owner, acceptLanguage: "en-US"}).Message; got != i18n.Render("pt-BR", i18n.M("decree.scope_required")) {
		t.Errorf("a user in pt-BR: %q", got)
	}
}

// CA-MSG-04 (the languages of the errors that come before the session, and the default)
func TestAnErrorWithNoSessionUsesTheHeaderOfTheBrowserAndThenPortuguese(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	msg := func(o opts) string {
		t.Helper()
		w := r.login("ninguem", ownerPass, o)
		r.wantErr(w, 401, panel.CodeInvalidCredentials)
		return decodeInto[panel.ErrorBody](t, w).Error.Message
	}
	if got := msg(opts{acceptLanguage: "en-US,en;q=0.9"}); got != "Invalid user or password." {
		t.Errorf("en header = %q", got)
	}
	if got := msg(opts{acceptLanguage: "fr"}); got != "Usuário ou senha inválidos." {
		t.Errorf("unknown header = %q", got)
	}
	if got := msg(opts{}); got != "Usuário ou senha inválidos." {
		t.Errorf("no header = %q", got)
	}
}
