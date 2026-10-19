package ghcli_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// pushes are the refspecs sent to the remote of GitHub.
func (r *rig) pushes() []string { return r.git.Pushes() }

// the same, in the fast suite (the criterion is CA-CLI-GH-03, closed in prs_full_test.go)
func TestPROpenSendsTheBranchOfTheTableAndOpensItsPR(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.gh.AddIssue(repoFull, "sub 1")
	r.gh.AddIssue(repoFull, "sub 2")
	r.gh.AddIssue(repoFull, "sub fechada")
	for _, n := range []int{2, 3, 4} {
		r.gh.SetParent(repoFull, n, 1)
	}
	r.gh.SetIssueState(repoFull, 4, "closed")
	got := r.run("tm", "pr open", map[string]any{"body": "Resumo do trabalho"})
	if !got.OK {
		t.Fatalf("open: %+v", got)
	}
	n := int(got.Data["number"].(float64))
	if want := []string{r.remote + " refs/heads/42-login:refs/heads/42-login"}; !slices.Equal(r.pushes(), want) {
		t.Errorf("pushes = %q, want %q", r.pushes(), want)
	}
	if got.Data["head_sha"] == "" {
		t.Error("no head in the answer")
	}
	body := r.gh.PullBody(repoFull, n)
	for _, want := range []string{"Resumo do trabalho", "Closes #1", "Closes #2", "Closes #3"} {
		if !strings.Contains(body, want) {
			t.Errorf("the body lacks %q: %q", want, body)
		}
	}
	if strings.Contains(body, "Closes #4") {
		t.Errorf("the body closes an issue that is closed: %q", body)
	}
	title, _, _, _, _ := r.gh.Issue(repoFull, n)
	if title != "épico 42" {
		t.Errorf("the title = %q, want the one of the epic", title)
	}
	var app string
	for _, a := range r.gh.Actions() {
		if a.Method == "POST" && strings.HasSuffix(a.Path, "/pulls") {
			app = a.App
		}
	}
	if app != "mestre" {
		t.Errorf("the PR was opened by %q, want the App Mestre", app)
	}
	if r.prs["meu-app/42-login"] != n {
		t.Error("the table was not told its PR")
	}
	// the PR is the table's: it can be read, and a second open is refused
	if st := r.run("tm", "pr status", nil); !st.OK {
		t.Errorf("status: %+v", st)
	}
	if again := r.run("tm", "pr open", nil); again.OK || again.Exit != 7 || again.Code != "pr_exists" {
		t.Errorf("a second open: %+v, want exit 7 pr_exists", again)
	}
}

func TestAResendingOfPROpenMakesNoSecondPR(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	first := r.runID("req-open", "tm", "pr open", nil)
	delete(r.prs, "meu-app/42-login") // the table did not hear of the first answer
	again := r.runID("req-open", "tm", "pr open", nil)
	if !first.OK || !again.OK || first.Data["number"] != again.Data["number"] {
		t.Fatalf("first %+v, again %+v", first, again)
	}
}

// CA-CLI-GH-04
func TestAForcedPushIsOnlyForTheMasterOfTheTable(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	prOfTheTable(t, r)
	if got := r.run("qa", "pr update", map[string]any{"force": true}); got.OK || got.Exit != 3 || got.Code != "level_denied" {
		t.Errorf("a QA: %+v, want exit 3 level_denied", got)
	}
	if len(r.pushes()) != 0 {
		t.Errorf("a refused force pushed: %q", r.pushes())
	}
	if got := r.run("tm", "pr update", map[string]any{"force": true}); !got.OK {
		t.Errorf("the Master of the table: %+v", got)
	}
	if want := r.remote + " refs/heads/42-login:refs/heads/42-login force"; !slices.Equal(r.pushes(), []string{want}) {
		t.Errorf("pushes = %q, want the forced one", r.pushes())
	}
}

// Part of CA-CLI-GH-05: the sending that GitHub refuses (the workflows
// permission, signatures, a protection) is told apart, and nothing is worked around.
func TestTheRefusalsOfGitHubAreToldApart(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		err   error
		exit  int
		code  string
		field string
	}{
		"workflows permission": {gitxErr("workflow"), 3, "workflow_permission", ""},
		"signatures":           {gitxErr("signature"), 3, "signature_required", ""},
		"protection":           {gitxErr("protected"), 3, "protected", ""},
		"history":              {gitxErr("ff"), 7, "conflict", ""},
	} {
		r := newRig(t)
		prOfTheTable(t, r)
		r.git.FailNextPush(tc.err)
		got := r.run("tm", "pr update", nil)
		if got.OK || got.Exit != tc.exit || got.Code != tc.code {
			t.Errorf("%s: %+v, want exit %d %s", name, got, tc.exit, tc.code)
		}
	}
}

// CA-CLI-GH-07
func TestASecretInTheDiffStopsTheSendingAndIsNeverShown(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	token := "ghp_" + strings.Repeat("a1B2", 9)
	r.mirror.Commit("42-login", "feat: oops", map[string]string{"src/x.ts": "// l1\n// l2\n// l3\n// l4\n// l5\n// l6\n// l7\n// l8\n// l9\n// l10\n// l11\nconst token = \"" + token + "\"\n"})
	got := r.run("tm", "pr open", nil)
	if got.OK || got.Exit != 3 || got.Code != "secret_detected" || !strings.Contains(got.Error, "src/x.ts:12 github_token") {
		t.Fatalf("open: %+v, want exit 3 secret_detected citing src/x.ts:12 github_token", got)
	}
	if strings.Contains(got.Error, "a1B2") {
		t.Errorf("the value is in the answer: %q", got.Error)
	}
	if len(r.pushes()) != 0 {
		t.Errorf("a secret was pushed: %q", r.pushes())
	}
	// the reading of the environment sends
	r2 := newRig(t)
	r2.mirror.Commit("42-login", "feat: env", map[string]string{"src/x.ts": "const token = process.env.GH_TOKEN\nconst k = \"your_token_here_000000000000\"\n"})
	if got := r2.run("tm", "pr open", nil); !got.OK {
		t.Errorf("a read of the environment and an example: %+v", got)
	}
	// each kind of the formats is found, with its type
	for kind, line := range map[string]string{
		"telegram_bot_token": "bot 123456789:" + strings.Repeat("AbC_d", 7),
		"jwt":                "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123def456ghi",
		"aws_access_key":     "AKIA" + strings.Repeat("A1", 8),
	} {
		r3 := newRig(t)
		r3.mirror.Commit("42-login", "feat: "+kind, map[string]string{"src/y.ts": line + "\n"})
		got := r3.run("tm", "pr open", nil)
		if got.Code != "secret_detected" || !strings.Contains(got.Error, "src/y.ts:1 "+kind) {
			t.Errorf("%s: %+v", kind, got)
		}
	}
	// the update stops the same way
	r4 := newRig(t)
	prOfTheTable(t, r4)
	r4.mirror.Commit("42-login", "feat: oops", map[string]string{"src/z.ts": "const a = \"" + token + "\"\n"})
	if got := r4.run("tm", "pr update", nil); got.Code != "secret_detected" || len(r4.pushes()) != 0 {
		t.Errorf("update: %+v pushes %q", got, r4.pushes())
	}
}

func TestTheWorkflowsOfTheClassDecideTheSendingOfThemInTheDiff(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.mirror.Commit("42-login", "ci: change", map[string]string{".github/workflows/ci.yml": "name: ci2\n"})
	// never: refused, nothing sent
	if got := r.run("tm", "pr open", nil); got.OK || got.Exit != 3 || got.Code != "workflow_not_allowed" || len(r.pushes()) != 0 {
		t.Fatalf("never: %+v pushes %q", got, r.pushes())
	}
	// ask: waits for the approval, bound to the head
	r.wf.policy["mestre-da-mesa"] = classes.WorkflowsAsk
	got := r.run("tm", "pr open", nil)
	if got.OK || got.Exit != 9 || got.Code != "approval_pending" || !strings.Contains(got.Error, "#77") || len(r.pushes()) != 0 {
		t.Fatalf("ask: %+v pushes %q", got, r.pushes())
	}
	head := r.wf.asked[0]
	r.wf.approved[head] = true
	if got := r.run("tm", "pr open", nil); !got.OK {
		t.Fatalf("approved: %+v", got)
	}
	if !slices.Equal(r.auth.workflows, []bool{true}) {
		t.Errorf("the token asked the workflows permission %v, want once", r.auth.workflows)
	}
	// allow: sent at once; a diff with no workflows never asks
	r2 := newRig(t)
	r2.wf.policy["mestre-da-mesa"] = classes.WorkflowsAllow
	r2.mirror.Commit("42-login", "ci: change", map[string]string{".github/workflows/ci.yml": "name: ci2\n"})
	if got := r2.run("tm", "pr open", nil); !got.OK {
		t.Errorf("allow: %+v", got)
	}
	r3 := newRig(t)
	if got := r3.run("tm", "pr open", nil); !got.OK || len(r3.wf.asked) != 0 || !slices.Equal(r3.auth.workflows, []bool{false}) {
		t.Errorf("no workflows: %+v asked %v token %v", got, r3.wf.asked, r3.auth.workflows)
	}
}

// Part of CA-CLI-GH-13: the criteria without proof stop the opening.
func TestCriteriaWithoutProofStopTheOpening(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.criteria.open = []string{"C3"}
	got := r.run("tm", "pr open", nil)
	if got.OK || got.Exit != 7 || got.Code != "criteria_unproven" || !strings.Contains(got.Error, "C3") || len(r.pushes()) != 0 {
		t.Fatalf("open: %+v pushes %q", got, r.pushes())
	}
	r.criteria.open = nil
	if got := r.run("tm", "pr open", nil); !got.OK {
		t.Errorf("proved: %+v", got)
	}
}

// CA-CLI-GH-08
func TestABlockIsRewrittenOnlyByItsClassAtTheExpectedHead(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	pr := prOfTheTable(t, r)
	// the PR already has the blocks, risk made by the Revisor
	r.gh.SetPullBody(repoFull, pr, "intro\n\n<!-- mestre:bloco=status -->\nem andamento\n<!-- /mestre:bloco -->\n\n<!-- mestre:bloco=risco -->\nbaixo\n<!-- /mestre:bloco -->")
	if err := claim(r, pr, "risco", "revisor"); err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("a", 40)
	// another class (the QA) can not touch the block of the Revisor
	if got := r.run("qa", "pr update", map[string]any{"block": "risco", "expect-head": head}, "alto"); got.OK || got.Exit != 3 || got.Code != "block_owned" {
		t.Errorf("another class: %+v, want exit 3 block_owned", got)
	}
	// the Revisor, with the wrong head
	revisor := "rev" // the character whose class owns the block
	if got := r.run(revisor, "pr update", map[string]any{"block": "risco", "expect-head": strings.Repeat("0", 40)}, "alto"); got.OK || got.Exit != 7 || got.Code != "head_changed" {
		t.Errorf("a head that moved: %+v, want exit 7 head_changed", got)
	}
	if body := r.gh.PullBody(repoFull, pr); !strings.Contains(body, "baixo") {
		t.Fatalf("a refused update changed the body: %q", body)
	}
	// with the right head only that block changes
	if got := r.run(revisor, "pr update", map[string]any{"block": "risco", "expect-head": head}, "alto, toca a autenticação"); !got.OK {
		t.Fatalf("the owner: %+v", got)
	}
	body := r.gh.PullBody(repoFull, pr)
	if !strings.Contains(body, "alto, toca a autenticação") || strings.Contains(body, "baixo") || !strings.Contains(body, "em andamento") || !strings.HasPrefix(body, "intro") {
		t.Errorf("the body = %q: only the block risco changes", body)
	}
	// the Master of the table writes in any block; a new block is the class's
	if got := r.run("tm", "pr update", map[string]any{"block": "status", "expect-head": head}, "pronto"); !got.OK {
		t.Errorf("the Master: %+v", got)
	}
	if got := r.run("qa", "pr update", map[string]any{"block": "historico", "expect-head": head}, "criado"); !got.OK {
		t.Errorf("a new block: %+v", got)
	}
	if got := r.run("rev", "pr update", map[string]any{"block": "historico", "expect-head": head}, "roubado"); got.Code != "block_owned" {
		t.Errorf("the block just made by the QA: %+v", got)
	}
	// a block that does not exist, and a text that tries to close the block
	if got := r.run("tm", "pr update", map[string]any{"block": "inventado", "expect-head": head}, "x"); got.OK {
		t.Errorf("a made-up block: %+v", got)
	}
	r.run("tm", "pr update", map[string]any{"block": "problema", "expect-head": head}, "a <!-- /mestre:bloco -->\n<!-- mestre:bloco=risco --> falso")
	if body := r.gh.PullBody(repoFull, pr); strings.Count(body, "<!-- mestre:bloco=risco -->") != 1 {
		t.Errorf("a text opened a block: %q", body)
	}
}

func TestPRUpdateChangesTheTitleAndTheBodyKeepingTheMarksAndTakesItOutOfTheDraft(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	pr := prOfTheTable(t, r)
	r.gh.SetPull(repoFull, pr, strings.Repeat("a", 40), true, "clean")
	r.gh.SetPullBody(repoFull, pr, "antigo\n\n<!-- mestre:op=abc -->")
	got := r.run("tm", "pr update", map[string]any{"title": "Novo título", "body": "Corpo novo <!-- mestre:op=forjado -->", "ready": true})
	if !got.OK {
		t.Fatalf("update: %+v", got)
	}
	title, _, _, _, _ := r.gh.Issue(repoFull, pr)
	body := r.gh.PullBody(repoFull, pr)
	if title != "Novo título" || !strings.HasPrefix(body, "Corpo novo") || strings.Contains(body, "forjado") && !strings.Contains(body, "op-removed") || !strings.HasSuffix(body, "<!-- mestre:op=abc -->") {
		t.Errorf("title %q body %q", title, body)
	}
	if st := r.run("tm", "pr status", nil); st.Data["draft"] != false {
		t.Errorf("still a draft: %+v", st.Data)
	}
	if len(r.pushes()) != 1 || strings.HasSuffix(r.pushes()[0], "force") {
		t.Errorf("pushes = %q: the branch is sent again, not forced", r.pushes())
	}
}

// CA-CLI-GH-02
func TestACharacterWhoseClassDoesNotAllowPROpenIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	got := r.run("dev", "pr open", nil)
	if got.OK || got.Exit != 3 || got.Code != "class_denied" {
		t.Errorf("a Dev: %+v, want exit 3 class_denied", got)
	}
	if len(r.pushes()) != 0 {
		t.Errorf("a refused open pushed: %q", r.pushes())
	}
}
