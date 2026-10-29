package panel_test

import (
	"context"
	"encoding/json"
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

// unknownEffect turns every proposal into one with an effect the engine does not know, which it
// refuses with a message that has a parameter ("effect").
type unknownEffect struct{ *fakeEngine }

func (s unknownEffect) Propose(ctx context.Context, p decrees.Proposal) (decrees.Pending, error) {
	p.Effect = decrees.Effect{Kind: "bogus"}
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
	// the message has no parameter: the body carries the code and the text, and no "params"
	if len(i18n.M("decree.scope_required").Params) != 0 {
		t.Fatal("decree.scope_required is expected to have no parameter in this test")
	}
	var raw struct {
		Error map[string]any `json:"error"`
	}
	rw := r.call("POST", "/api/decrees", rule, opts{cookie: owner})
	r.wantErr(rw, 422, "decree.scope_required")
	if err := json.Unmarshal(rw.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, has := raw.Error["params"]; has || raw.Error["code"] != "decree.scope_required" || raw.Error["message"] != want {
		t.Errorf("error object = %v, want code, message %q and no params", raw.Error, want)
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

// CA-MSG-04
// A refusal with a parameter carries it in "params", and the text is made from the code and the
// params in the language of the user: en and pt-BR differ and both have the value.
func TestARefusalWithAParamCarriesItAndTheTextHasTheValueInBothLanguages(t *testing.T) {
	t.Parallel()
	r := newRules(t)
	r.panel.SetRules(unknownEffect{r.eng})
	owner := r.ownerSession()
	rule := panel.DecreeInput{Workspace: "loja", Kind: "rule", Effect: "permission", TargetKind: "class", TargetName: "dev", Field: "commands", Side: "allow", Value: "npm test*", Text: "dev pode rodar npm test"}
	var texts []string
	for _, lang := range []string{"en", "pt-BR"} {
		if w := r.call("PUT", "/api/me/language", panel.LanguageRequest{Language: lang}, opts{cookie: owner}); w.Code != 200 {
			t.Fatalf("language = %d %s", w.Code, w.Body)
		}
		w := r.call("POST", "/api/decrees", rule, opts{cookie: owner})
		r.wantErr(w, 422, "decree.effect_unknown")
		e := decodeInto[panel.ErrorBody](t, w).Error
		if e.Params["effect"] != "bogus" || len(e.Params) != 1 {
			t.Errorf("%s: params = %v, want {effect: bogus}", lang, e.Params)
		}
		if want := i18n.Render(lang, i18n.M("decree.effect_unknown", "effect", "bogus")); e.Message != want || !strings.Contains(e.Message, `"bogus"`) {
			t.Errorf("%s: message = %q, want %q", lang, e.Message, want)
		}
		texts = append(texts, e.Message)
	}
	if texts[0] == texts[1] {
		t.Errorf("the text is the same in both languages: %q", texts[0])
	}
}
