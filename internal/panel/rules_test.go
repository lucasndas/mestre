package panel_test

import (
	"crypto/sha256"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// CA-ADM-04: "ampliar" the ceiling creates a request of decree that asks the PIN, and the
// ceiling in force does not change before the decree.
func TestWideningTheCeilingOpensARequestAndTheCeilingStaysUntilTheDecree(t *testing.T) {
	t.Parallel()
	r := newRules(t)
	tok := r.ownerSession()
	file := r.writeRule("teto.yaml", "version: 1\npermissions:\n  network:\n    domains: [\"github.com\"]\n")
	before, _ := os.ReadFile(file)

	w := r.call("POST", "/api/workspaces/loja/ceiling/widen", panel.WidenInput{Field: "domains", Value: "registry.npmjs.org", Text: "libere o registry do npm no teto"}, opts{cookie: tok})
	if w.Code != 201 {
		t.Fatalf("widen = %d %s", w.Code, w.Body)
	}
	prop := decodeInto[panel.DecreeProposal](t, w)
	if !prop.PIN || prop.ID == "" || !strings.Contains(prop.Text, "Confirma?") {
		t.Fatalf("proposal = %+v, want one that asks the PIN", prop)
	}
	view := decodeInto[panel.CeilingView](t, r.call("GET", "/api/workspaces/loja/ceiling", nil, opts{cookie: tok}))
	if !view.Valid || len(view.Widenings) != 0 || view.Text != string(before) {
		t.Fatalf("ceiling in force changed before the decree: %+v", view)
	}
	// the click without the PIN makes nothing; with it, the decree exists and the file is still not touched
	r.wantErr(r.call("POST", "/api/decrees/proposals/"+prop.ID+"/confirm", nil, opts{cookie: tok}), 403, panel.CodePINRequired)
	r.wantErr(r.call("POST", "/api/decrees/proposals/"+prop.ID+"/confirm", nil, opts{cookie: tok, pin: "000000"}), 403, panel.CodePINInvalid)
	if w := r.call("POST", "/api/decrees/proposals/"+prop.ID+"/confirm", nil, opts{cookie: tok, pin: ownerPIN}); w.Code != 201 {
		t.Fatalf("confirm = %d %s", w.Code, w.Body)
	}
	view = decodeInto[panel.CeilingView](t, r.call("GET", "/api/workspaces/loja/ceiling", nil, opts{cookie: tok}))
	if len(view.Widenings) != 1 || view.Widenings[0].Effect != "ceiling" {
		t.Fatalf("widenings = %+v", view.Widenings)
	}
	if after, _ := os.ReadFile(file); string(after) != string(before) {
		t.Error("the panel edited the ceiling file")
	}
	if r.audited("panel.ceiling.widen_proposed") != 1 || r.audited("panel.decree.created") != 1 {
		t.Error("the proposal and the decree are not in the audit")
	}
	// an Approver does not widen: the level required comes back
	_, apv := r.member("Carla", "carla", users.Approver)
	r.wantErr(r.call("POST", "/api/workspaces/loja/ceiling/widen", panel.WidenInput{Field: "domains", Value: "x.com", Text: "x"}, opts{cookie: apv}), 403, panel.CodeForbiddenLevel)
	// a field that is not one of the four is no request at all
	r.wantErr(r.call("POST", "/api/workspaces/loja/ceiling/widen", panel.WidenInput{Field: "operator", Value: "true", Text: "x"}, opts{cookie: tok}), 400, panel.CodeBadRequest)
}

// CA-ADM-07: a policy with an error saves nothing and the error says the line.
func TestAnInvalidPolicySavesNothingAndTheErrorShowsTheLine(t *testing.T) {
	t.Parallel()
	r := newRules(t)
	tok := r.ownerSession()
	cur := decodeInto[panel.PolicyFile](t, r.call("GET", "/api/workspaces/loja/policy", nil, opts{cookie: tok}))
	if !cur.Default || cur.Version == "" || !strings.Contains(cur.Text, "versao: 1") {
		t.Fatalf("policy = %+v, want the default content before any file", cur)
	}
	path := filepath.Join(r.eng.dir, ".mestre", "regras", "politica-de-aprovacao.yaml")

	broken := strings.Replace(cur.Text, "quorum:", "quorum: [", 1) // a YAML that does not read
	w := r.call("PUT", "/api/workspaces/loja/policy", panel.PolicyInput{Text: broken, Version: cur.Version}, opts{cookie: tok})
	r.wantErr(w, 422, panel.CodeInvalidPolicy)
	var e panel.ErrorBody
	decodeJSON(t, w, &e)
	line, _ := e.Error.Params["line"].(float64)
	want := 1 + strings.Count(broken[:strings.Index(broken, "quorum: [")], "\n")
	if int(line) < want-1 || int(line) > want+1 {
		t.Errorf("line = %v, want about %d (the line of the quorum)", e.Error.Params["line"], want)
	}
	// a file that reads as YAML and breaks a rule of the policy names the key
	wrong := strings.Replace(cur.Text, "expiracao: 24h", "expiracao: um-dia", 1)
	w = r.call("PUT", "/api/workspaces/loja/policy", panel.PolicyInput{Text: wrong, Version: cur.Version}, opts{cookie: tok})
	r.wantErr(w, 422, panel.CodeInvalidPolicy)
	decodeJSON(t, w, &e)
	if l, _ := e.Error.Params["line"].(float64); l < 2 {
		t.Errorf("line of a broken rule = %v, want the line of expiracao", e.Error.Params["line"])
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("an invalid policy was written")
	}

	// a valid one is saved whole, with the lines that changed in the audit
	good := strings.Replace(cur.Text, "max_arquivos: 25", "max_arquivos: 30", 1)
	w = r.call("PUT", "/api/workspaces/loja/policy", panel.PolicyInput{Text: good, Version: cur.Version}, opts{cookie: tok})
	if w.Code != 200 {
		t.Fatalf("save = %d %s", w.Code, w.Body)
	}
	if got, _ := os.ReadFile(path); string(got) != good {
		t.Error("the file is not what was saved")
	}
	if !strings.Contains(r.auditText(), "max_arquivos: 30") || !strings.Contains(r.auditText(), "max_arquivos: 25") {
		t.Error("the audit lacks the before and the after")
	}
	// the page that was open before the save does not overwrite it
	r.wantErr(r.call("PUT", "/api/workspaces/loja/policy", panel.PolicyInput{Text: cur.Text, Version: cur.Version}, opts{cookie: tok}), 409, panel.CodeFileChanged)
	// only the Owner edits the policy
	_, adm := r.member("Bruno", "bruno", users.Administrator)
	r.wantErr(r.call("PUT", "/api/workspaces/loja/policy", panel.PolicyInput{Text: good, Version: cur.Version}, opts{cookie: adm}), 403, panel.CodeForbiddenLevel)
	r.wantErr(r.call("GET", "/api/workspaces/loja/policy", nil, opts{cookie: adm}), 403, panel.CodeForbiddenLevel)
	// a file bigger than the engine reads is refused before anything
	r.wantErr(r.call("PUT", "/api/workspaces/loja/policy", panel.PolicyInput{Text: strings.Repeat("# x\n", 100_000), Version: "v"}, opts{cookie: tok}), 413, panel.CodeTooLarge)
}

func TestAPolicyFileThatIsALinkIsNotFollowed(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("a link needs a privilege there; the refusal is proved by safefile")
	}
	r := newRules(t)
	tok := r.ownerSession()
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.eng.dir, ".mestre", "regras", "politica-de-aprovacao.yaml")); err != nil {
		t.Fatal(err)
	}
	r.wantErr(r.call("GET", "/api/workspaces/loja/policy", nil, opts{cookie: tok}), 409, panel.CodeFileRefused)
	cur := sha256.Sum256([]byte("x"))
	r.wantErr(r.call("PUT", "/api/workspaces/loja/policy", panel.PolicyInput{Text: "versao: 1\n", Version: string(cur[:])}, opts{cookie: tok}), 409, panel.CodeFileRefused)
	if b, _ := os.ReadFile(outside); string(b) != "secret" {
		t.Error("the write went through the link")
	}
}

// CA-ADM-12: the four things show for an Administrator, who revokes a temporary rule but
// has no emergency button and is refused with the level; the Owner's button takes all.
func TestDelegationsShowToAnAdministratorWhoRevokesARuleButNotAll(t *testing.T) {
	t.Parallel()
	r := newRules(t)
	owner := r.ownerSession()
	dec := r.delegation(owner)
	r.addSub(dec)
	r.addRule(1, "dev")
	r.addRule(2, "dev")
	_, adm := r.member("Bruno", "bruno", users.Administrator)

	list := decodeInto[panel.DelegationList](t, r.call("GET", "/api/delegations", nil, opts{cookie: adm}))
	g := list.Workspaces[0]
	if len(g.Delegations) != 1 || len(g.Subdelegations) != 1 || len(g.Rules) != 2 {
		t.Fatalf("Administrator sees %d delegations, %d subdelegations, %d rules, want 1, 1, 2", len(g.Delegations), len(g.Subdelegations), len(g.Rules))
	}
	if list.CanRevokeAll || !list.CanRevoke {
		t.Errorf("buttons = revoke all %v, revoke %v", list.CanRevokeAll, list.CanRevoke)
	}
	if d := g.Delegations[0]; d.Type != "envio_workflows" || d.AuthorName != "Lucas" || d.Until == nil || d.Text == "" {
		t.Errorf("delegation row = %+v", d.DecreeRow)
	}
	if w := r.call("POST", "/api/table-rules/1/revoke", nil, opts{cookie: adm}); w.Code != 204 {
		t.Fatalf("revoke rule = %d %s", w.Code, w.Body)
	}
	w := r.call("POST", "/api/delegations/revoke-all", nil, opts{cookie: adm})
	r.wantErr(w, 403, panel.CodeForbiddenLevel)
	var e panel.ErrorBody
	decodeJSON(t, w, &e)
	if e.Error.Params["need"] != "owner" || e.Error.Params["have"] != "administrator" {
		t.Errorf("params = %v, want the level required", e.Error.Params)
	}
	if n := len(decodeInto[panel.DelegationList](t, r.call("GET", "/api/delegations", nil, opts{cookie: adm})).Workspaces[0].Rules); n != 1 {
		t.Fatalf("%d rules after the revocation, want 1", n)
	}
	// the delegation is the Owner's: an Administrator does not revoke it
	r.wantErr(r.call("POST", "/api/decrees/"+num(dec)+"/revoke", nil, opts{cookie: adm}), 403, panel.CodeForbiddenLevel)

	// the Owner's button takes the delegation, the subdelegation and the rule that is left
	w = r.call("POST", "/api/delegations/revoke-all", nil, opts{cookie: owner})
	if w.Code != 200 {
		t.Fatalf("revoke all = %d %s", w.Code, w.Body)
	}
	if res := decodeInto[panel.RevokeAllResult](t, w); res.Delegations != 1 || res.Subdelegations != 1 || res.Rules != 1 {
		t.Fatalf("result = %+v", res)
	}
	g = decodeInto[panel.DelegationList](t, r.call("GET", "/api/delegations", nil, opts{cookie: owner})).Workspaces[0]
	if len(g.Delegations)+len(g.Subdelegations)+len(g.Rules) != 0 {
		t.Fatalf("still in force: %+v", g)
	}
	if r.audited("panel.delegations.revoked_all") != 1 || r.audited("panel.table_rule.revoked") != 1 {
		t.Error("the revocations are not in the audit")
	}
}

func num(n int64) string { return strconv.FormatInt(n, 10) }

// CA-ADM-13: an Approver pauses a table whose queue is 3 open requests, the oldest 40
// minutes old; the screen shows the pause with who and when and the metrics. An Observer
// sees it but has no button and the API refuses; the pause of the Grand Master is not
// resumed from the panel.
func TestAnApproverPausesTheAutonomyAndTheScreenShowsWhoWhenAndTheQueue(t *testing.T) {
	t.Parallel()
	r := newRules(t)
	now := r.clk.Now()
	r.eng.opened["42-login"] = []time.Time{now.Add(-40 * time.Minute), now.Add(-30 * time.Minute), now.Add(-5 * time.Minute)}
	apvUser, apv := r.member("Carla", "carla", users.Approver)
	_, obs := r.member("Davi", "davi", users.Observer)

	// the Observer reads, does not see the button, and is refused by the API
	st := decodeInto[panel.ChainStatus](t, r.call("GET", "/api/chain", nil, opts{cookie: obs}))
	if st.CanPause {
		t.Error("the Observer is offered the button")
	}
	r.wantErr(r.call("POST", "/api/chain/loja/tables/42-login/pause", panel.PauseInput{Reason: "x"}, opts{cookie: obs}), 403, panel.CodeForbiddenLevel)
	if len(r.eng.asking()) != 0 {
		t.Fatal("the supervision was asked by an Observer")
	}

	r.wantErr(r.call("POST", "/api/chain/loja/tables/42-login/pause", panel.PauseInput{Reason: "  "}, opts{cookie: apv}), 422, panel.CodeReasonRequired)
	if w := r.call("POST", "/api/chain/loja/tables/42-login/pause", panel.PauseInput{Reason: "muitas falhas"}, opts{cookie: apv}); w.Code != 204 {
		t.Fatalf("pause = %d %s", w.Code, w.Body)
	}
	asked := r.eng.asking()
	if len(asked) != 1 || asked[0].By.User != apvUser.ID || asked[0].By.UserLevel != "approver" || asked[0].Table != "42-login" {
		t.Fatalf("asked = %+v, want the Approver as the actor", asked)
	}

	st = decodeInto[panel.ChainStatus](t, r.call("GET", "/api/chain", nil, opts{cookie: apv}))
	var tab panel.ChainTable
	for _, x := range st.Workspaces[0].Tables {
		if x.Table == "42-login" {
			tab = x
		}
	}
	if tab.Pause == nil || tab.Pause.ByName != "Carla" || tab.Pause.Reason != "muitas falhas" || !tab.Pause.Since.Equal(now) || !tab.CanResume {
		t.Fatalf("pause = %+v", tab.Pause)
	}
	if len(tab.Queues) != 1 || tab.Queues[0].Open != 3 || tab.Queues[0].OldestSeconds != 40*60 {
		t.Fatalf("queues = %+v, want 3 open and 40 minutes", tab.Queues)
	}
	if len(st.Workspaces[0].Queues) != 1 || st.Workspaces[0].Queues[0].Level != "people" {
		t.Errorf("queues of the workspace = %+v", st.Workspaces[0].Queues)
	}
	if r.audited("panel.chain.paused") != 1 {
		t.Error("the pause is not in the audit")
	}

	// the pause of the Grand Master is resumed by him alone
	r.eng.mu.Lock()
	r.eng.pauses["loja/43-busca"] = chainPause("43-busca", "gm-loja", "character", now)
	r.eng.mu.Unlock()
	r.wantErr(r.call("POST", "/api/chain/loja/tables/43-busca/resume", panel.PauseInput{Reason: "voltem"}, opts{cookie: apv}), 403, panel.CodeGrandMasterOnly)
	st = decodeInto[panel.ChainStatus](t, r.call("GET", "/api/chain", nil, opts{cookie: apv}))
	for _, x := range st.Workspaces[0].Tables {
		if x.Table == "43-busca" && (x.Pause == nil || x.CanResume) {
			t.Errorf("the table paused by the Grand Master offers the resume: %+v", x)
		}
	}
	// the pause of a person is resumed by an Approver
	if w := r.call("POST", "/api/chain/loja/tables/42-login/resume", panel.PauseInput{Reason: "consertado"}, opts{cookie: apv}); w.Code != 204 {
		t.Fatalf("resume = %d %s", w.Code, w.Body)
	}
	if r.audited("panel.chain.resumed") != 1 {
		t.Error("the resume is not in the audit")
	}
}

func TestAWorkspaceThatIsNotTheirsIsNotFoundAndEveryRouteAsksForASession(t *testing.T) {
	t.Parallel()
	r := newRules(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/decrees"}, {"POST", "/api/decrees"}, {"POST", "/api/decrees/proposals/x/confirm"}, {"POST", "/api/decrees/1/revoke"},
		{"POST", "/api/decrees/1/renew"}, {"GET", "/api/delegations"}, {"POST", "/api/subdelegations/1/revoke"}, {"POST", "/api/table-rules/1/revoke"},
		{"POST", "/api/delegations/revoke-all"}, {"GET", "/api/chain"}, {"POST", "/api/chain/loja/tables/t/pause"}, {"POST", "/api/chain/loja/tables/t/resume"},
		{"GET", "/api/workspaces/loja/ceiling"}, {"POST", "/api/workspaces/loja/ceiling/widen"}, {"GET", "/api/workspaces/loja/policy"},
		{"PUT", "/api/workspaces/loja/policy"}, {"GET", "/api/workspaces/loja/instructions"}, {"PUT", "/api/workspaces/loja/instructions"},
	} {
		if w := r.call(c.method, c.path, map[string]any{}, opts{}); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", c.method, c.path, w.Code)
		}
	}
	tok := r.ownerSession()
	r.wantErr(r.call("PUT", "/api/workspaces/nao-existe/policy", panel.PolicyInput{}, opts{cookie: tok}), 404, panel.CodeNotFound)
	r.wantErr(r.call("POST", "/api/chain/nao-existe/tables/t/pause", panel.PauseInput{Reason: "x"}, opts{cookie: tok}), 404, panel.CodeNotFound)
	// a person of another workspace does not see this one
	if _, err := r.db.Writer().ExecContext(r.ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at) VALUES ('outra', 'o/outra', 'o/outra', 'u', 'main', 'active', 'done', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	_, adm := r.member("Bruno", "bruno", users.Administrator)
	r.wantErr(r.call("POST", "/api/chain/outra/tables/t/pause", panel.PauseInput{Reason: "x"}, opts{cookie: adm}), 404, panel.CodeNotFound)
	r.wantErr(r.call("GET", "/api/workspaces/outra/ceiling", nil, opts{cookie: adm}), 404, panel.CodeNotFound)
	if len(r.eng.asking()) != 0 {
		t.Error("the supervision was asked about a workspace that is not theirs")
	}
}

func TestTheDecreesScreenCreatesARuleAndAnExceptionAndRevokesThem(t *testing.T) {
	t.Parallel()
	r := newRules(t)
	_, adm := r.member("Bruno", "bruno", users.Administrator)
	_, apv := r.member("Carla", "carla", users.Approver)
	owner := r.ownerSession()

	// a permanent rule that only loosens needs no PIN beyond what the engine says
	rule := panel.DecreeInput{Workspace: "loja", Kind: "rule", Effect: "permission", TargetKind: "class", TargetName: "dev", Field: "commands", Side: "allow", Value: "npm test*", Text: "dev pode rodar npm test sem perguntar"}
	w := r.call("POST", "/api/decrees", rule, opts{cookie: adm})
	if w.Code != 201 {
		t.Fatalf("propose = %d %s", w.Code, w.Body)
	}
	prop := decodeInto[panel.DecreeProposal](t, w)
	// nothing is a decree before the click
	if n := len(decodeInto[panel.DecreeList](t, r.call("GET", "/api/decrees", nil, opts{cookie: adm})).Workspaces[0].Decrees); n != 0 {
		t.Fatalf("%d decrees before the confirmation", n)
	}
	// only the author confirms
	r.wantErr(r.call("POST", "/api/decrees/proposals/"+prop.ID+"/confirm", nil, opts{cookie: owner, pin: ownerPIN}), 403, panel.CodeForbiddenLevel)
	pin := ""
	if prop.PIN {
		pin = "654321"
	}
	if w := r.call("POST", "/api/decrees/proposals/"+prop.ID+"/confirm", nil, opts{cookie: adm, pin: pin}); w.Code != 201 {
		t.Fatalf("confirm = %d %s", w.Code, w.Body)
	}
	list := decodeInto[panel.DecreeList](t, r.call("GET", "/api/decrees", nil, opts{cookie: adm})).Workspaces[0].Decrees
	if len(list) != 1 || list[0].Text != rule.Text || list[0].AuthorName != "Bruno" || list[0].Kind != "rule" || list[0].State != "active" || !list[0].CanRevoke {
		t.Fatalf("list = %+v", list)
	}
	// an Approver does not make a permanent rule (the engine says the level) but makes an exception
	r.wantErr(r.call("POST", "/api/decrees", rule, opts{cookie: apv}), 422, "level.required_or_above")
	exc := panel.DecreeInput{Workspace: "loja", Kind: "exception", Effect: "permission", TargetKind: "workspace", Command: "make deploy-teste", Text: "libere make deploy-teste uma vez"}
	w = r.call("POST", "/api/decrees", exc, opts{cookie: apv})
	if w.Code != 201 {
		t.Fatalf("exception = %d %s", w.Code, w.Body)
	}
	ep := decodeInto[panel.DecreeProposal](t, w)
	if w := r.call("POST", "/api/decrees/proposals/"+ep.ID+"/confirm", nil, opts{cookie: apv, pin: "654321"}); w.Code != 201 {
		t.Fatalf("confirm exception = %d %s", w.Code, w.Body)
	}
	// an Approver revokes her own exception, not the Administrator's rule
	rows := decodeInto[panel.DecreeList](t, r.call("GET", "/api/decrees", nil, opts{cookie: apv})).Workspaces[0].Decrees
	if len(rows) != 2 {
		t.Fatalf("%d decrees, want 2", len(rows))
	}
	for _, d := range rows {
		if d.CanRevoke != (d.Kind == "exception") {
			t.Errorf("decree %d (%s): can_revoke = %v for the Approver", d.Number, d.Kind, d.CanRevoke)
		}
		if d.Kind == "rule" {
			r.wantErr(r.call("POST", "/api/decrees/"+num(d.Number)+"/revoke", nil, opts{cookie: apv}), 403, panel.CodeForbiddenLevel)
		}
	}
	// the Owner revokes the rule; the audit keeps the before and the after
	for _, d := range rows {
		if d.Kind != "rule" {
			continue
		}
		if w := r.call("POST", "/api/decrees/"+num(d.Number)+"/revoke", nil, opts{cookie: owner, pin: ownerPIN}); w.Code != 204 {
			t.Fatalf("revoke = %d %s", w.Code, w.Body)
		}
	}
	if r.audited("panel.decree.revoked") != 1 || !strings.Contains(r.auditText(), "revoked") {
		t.Error("the revocation is not in the audit with before and after")
	}
	// an Observer does not propose
	_, obs := r.member("Davi", "davi", users.Observer)
	r.wantErr(r.call("POST", "/api/decrees", rule, opts{cookie: obs}), 403, panel.CodeForbiddenLevel)
	// a rule the form could not have made is a bad request
	bad := rule
	bad.Side = "maybe"
	r.wantErr(r.call("POST", "/api/decrees", bad, opts{cookie: adm}), 400, panel.CodeBadRequest)
}

func TestTheExtraInstructionsAreWrittenByAnAdministratorWithinTheLimit(t *testing.T) {
	t.Parallel()
	r := newRules(t)
	_, adm := r.member("Bruno", "bruno", users.Administrator)
	_, apv := r.member("Carla", "carla", users.Approver)
	if w := r.call("PUT", "/api/workspaces/loja/instructions", panel.InstructionsInput{Role: "grand_master", Text: "prefira Codex para revisão"}, opts{cookie: adm}); w.Code != 200 {
		t.Fatalf("save = %d %s", w.Code, w.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(r.eng.dir, ".mestre", "regras", "instrucoes", "grao-mestre.md")); string(b) != "prefira Codex para revisão" {
		t.Errorf("file = %q", b)
	}
	got := decodeInto[panel.Instructions](t, r.call("GET", "/api/workspaces/loja/instructions", nil, opts{cookie: adm}))
	if got.GrandMaster != "prefira Codex para revisão" || got.TableMasters != "" || got.MaxBytes != 4096 {
		t.Fatalf("instructions = %+v", got)
	}
	if !strings.Contains(r.auditText(), "prefira Codex") || r.audited("panel.instructions.saved") != 1 {
		t.Error("the audit lacks the text")
	}
	big := strings.Repeat("a", 4097)
	r.wantErr(r.call("PUT", "/api/workspaces/loja/instructions", panel.InstructionsInput{Role: "table_masters", Text: big}, opts{cookie: adm}), 413, panel.CodeTooLarge)
	r.wantErr(r.call("PUT", "/api/workspaces/loja/instructions", panel.InstructionsInput{Role: "../x", Text: "a"}, opts{cookie: adm}), 400, panel.CodeBadRequest)
	r.wantErr(r.call("PUT", "/api/workspaces/loja/instructions", panel.InstructionsInput{Role: "table_masters", Text: "a\x00b"}, opts{cookie: adm}), 400, panel.CodeBadRequest)
	r.wantErr(r.call("PUT", "/api/workspaces/loja/instructions", panel.InstructionsInput{Role: "grand_master", Text: "x"}, opts{cookie: apv}), 403, panel.CodeForbiddenLevel)
}

// CA-ADM-07: Validar says whether a text would be accepted, with the line, and writes nothing.
func TestValidatingAPolicyChecksItWithoutSaving(t *testing.T) {
	t.Parallel()
	r := newRules(t)
	tok := r.ownerSession()
	cur := decodeInto[panel.PolicyFile](t, r.call("GET", "/api/workspaces/loja/policy", nil, opts{cookie: tok}))
	path := filepath.Join(r.eng.dir, ".mestre", "regras", "politica-de-aprovacao.yaml")

	broken := strings.Replace(cur.Text, "quorum:", "quorum: [", 1)
	w := r.call("POST", "/api/workspaces/loja/policy/validate", panel.PolicyInput{Text: broken, Version: cur.Version}, opts{cookie: tok})
	r.wantErr(w, 422, panel.CodeInvalidPolicy)
	var e panel.ErrorBody
	decodeJSON(t, w, &e)
	if l, _ := e.Error.Params["line"].(float64); l < 1 {
		t.Errorf("line = %v, want the line of the quorum", e.Error.Params["line"])
	}
	good := strings.Replace(cur.Text, "max_arquivos: 25", "max_arquivos: 30", 1)
	if w := r.call("POST", "/api/workspaces/loja/policy/validate", panel.PolicyInput{Text: good, Version: cur.Version}, opts{cookie: tok}); w.Code != 204 {
		t.Fatalf("validate = %d %s, want 204", w.Code, w.Body)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("validating wrote the file")
	}
	// the level of the policy applies to the check as well
	_, apv := r.member("Carla", "carla", users.Approver)
	r.wantErr(r.call("POST", "/api/workspaces/loja/policy/validate", panel.PolicyInput{Text: good}, opts{cookie: apv}), 403, panel.CodeForbiddenLevel)
}
