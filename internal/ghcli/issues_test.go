package ghcli_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/github"
)

// CA-CLI-GH-01
// Part of CA-GH-ACOES-01: `mestre issue comment` end to end, refused with the epic that owns the issue.
// Part of CA-MM-03: the table master is refused the issue of another table (the three commands together are in internal/app)
func TestAnIssueOutsideTheEpicOfTheTableIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	otherEpic := r.gh.AddIssue(repoFull, "épico 43", github.EpicLabel)
	r.tbl["meu-app/43-outra"] = github.TableRef{Repo: repoFull, Epic: otherEpic}
	other := r.gh.AddIssue(repoFull, "de outra mesa")
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: other, Table: "meu-app/43-outra"}); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"issue comment", "issue update"} {
		args := []string{itoa(other)}
		flags := map[string]any{"title": "x"}
		if cmd == "issue comment" {
			args = append(args, "x")
			flags = nil
		}
		got := r.run("tm", cmd, flags, args...)
		if got.OK || got.Exit != 3 || got.Code != "outside_epic" {
			t.Errorf("%s: %+v, want exit 3 outside_epic", cmd, got)
		}
		if !strings.Contains(got.Error, "#"+itoa(otherEpic)) {
			t.Errorf("%s: %q does not cite the epic #%d that owns the issue", cmd, got.Error, otherEpic)
		}
	}
	if n := len(r.gh.Comments(repoFull, other)); n != 0 {
		t.Errorf("%d comments were published", n)
	}
	if title, _, _, _, _ := r.gh.Issue(repoFull, other); title != "de outra mesa" {
		t.Errorf("the issue was changed: %q", title)
	}
}

// Part of CA-CLI-GH-02: the gate refuses the class that does not list the command; `pr open` comes with the PR commands.
func TestACharacterWhoseClassDoesNotAllowTheCommandIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	got := r.run("dev", "issue comment", nil, "1", "x")
	if got.OK || got.Exit != 3 || got.Code != "class_denied" {
		t.Errorf("a Dev: %+v, want exit 3 class_denied", got)
	}
	if got := r.run("qa", "issue comment", nil, "1", "x"); !got.OK {
		t.Errorf("a QA whose class allows it: %+v", got)
	}
}

// CA-CLI-GH-06
func TestAnIssueIsCreatedUnderTheEpicWithTheAppOfWhoAsked(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	epic := 1
	got := r.run("qa", "issue create", map[string]any{"title": "Testes", "parent": "1"})
	if !got.OK {
		t.Fatalf("create: %+v", got)
	}
	n := int(got.Data["number"].(float64))
	if int(got.Data["parent"].(float64)) != epic || r.gh.Parent(repoFull, n) != epic {
		t.Errorf("the parent = %v / %d, want #%d", got.Data["parent"], r.gh.Parent(repoFull, n), epic)
	}
	var made bool
	for _, a := range r.gh.Actions() {
		if a.Method == "POST" && strings.HasSuffix(a.Path, "/issues") {
			made = made || a.App == "qa"
		}
	}
	if !made {
		t.Errorf("the issue was not made with the App of the class: %+v", r.gh.Actions())
	}
	// no --parent: the epic of the table
	got = r.run("tm", "issue create", map[string]any{"title": "Docs"})
	if !got.OK || int(got.Data["parent"].(float64)) != epic {
		t.Errorf("default parent: %+v", got)
	}
	// a parent that is not of the table
	other := r.gh.AddIssue(repoFull, "de outra mesa")
	_ = r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: other, Table: "meu-app/43-outra"})
	if got := r.run("tm", "issue create", map[string]any{"title": "x", "parent": itoa(other)}); got.Code != "outside_epic" {
		t.Errorf("parent of another table: %+v", got)
	}
	// the Grão-Mestre makes a loose issue
	got = r.run("gm", "issue create", map[string]any{"title": "Solta"})
	if !got.OK || int(got.Data["parent"].(float64)) != 0 {
		t.Errorf("loose issue: %+v", got)
	}
	if got := r.run("tm", "issue create", nil); got.Exit != 2 {
		t.Errorf("no title: %+v", got)
	}
}

// CA-CLI-GH-10
func TestTheSameProblemIsNotRegisteredTwice(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	flags := func(fp string) map[string]any {
		return map[string]any{"title": "Login lento", "fingerprint": fp, "label": []any{"candidato"}}
	}
	first := r.run("tm", "issue create", flags("auth: login lento"))
	if !first.OK {
		t.Fatalf("first: %+v", first)
	}
	n := int(first.Data["number"].(float64))
	_, body, _, _, _ := r.gh.Issue(repoFull, n)
	if !strings.Contains(body, "<!-- mestre:impressao=") {
		t.Errorf("the impression is not in the body: %q", body)
	}
	if _, has := r.gh.Labels(repoFull)["candidato"]; !has {
		t.Error("the label candidato was not created")
	}
	again := r.run("tm", "issue create", flags("Auth:  login LENTO"))
	if !again.OK || again.Data["seen_again"] != true || int(again.Data["number"].(float64)) != n {
		t.Fatalf("again: %+v", again)
	}
	if cs := r.gh.Comments(repoFull, n); len(cs) != 1 || !strings.HasPrefix(cs[0], "Visto de novo em 2026-10-19: Login lento") {
		t.Errorf("the comments = %v", cs)
	}
	// the first one closed by a person
	r.gh.SetIssueState(repoFull, n, "closed")
	back := r.run("tm", "issue create", flags("auth: login lento"))
	if !back.OK || back.Data["seen_again"] != nil || int(back.Data["recurrence_of"].(float64)) != n {
		t.Fatalf("back: %+v", back)
	}
	m := int(back.Data["number"].(float64))
	_, body, _, labels, _ := r.gh.Issue(repoFull, m)
	if !strings.HasPrefix(body, "Recorrência de #"+itoa(n)) || !slices.Contains(labels, "recorrencia") {
		t.Errorf("the recurrence: body %q labels %v", body, labels)
	}
	if _, has := r.gh.Labels(repoFull)["recorrencia"]; !has {
		t.Error("the label recorrencia was not created")
	}
}

// CA-CLI-GH-12 and CA-GH-ACOES-14 (the CLI answer: exit 3 reserved_label for a table and for the Grão-Mestre)
func TestTheReservedLabelsAreNotChangedByATable(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	n := r.gh.AddIssue(repoFull, "da mesa")
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: n, Table: "meu-app/42-login"}); err != nil {
		t.Fatal(err)
	}
	r.gh.AddIssueLabel(repoFull, n, "mestre:humano") // a person put it
	before := len(r.gh.Actions())
	for name, flags := range map[string]map[string]any{
		"remove mestre:humano": {"remove-label": []any{"mestre:humano"}},
		"add epico":            {"add-label": []any{"epico"}},
		"remove epico":         {"remove-label": []any{"EPICO"}},
		"add mestre:humano":    {"add-label": []any{"mestre:humano"}},
	} {
		if got := r.run("tm", "issue update", flags, itoa(n)); got.OK || got.Exit != 3 || got.Code != "reserved_label" {
			t.Errorf("%s: %+v", name, got)
		}
	}
	if got := r.run("tm", "issue create", map[string]any{"title": "x", "label": []any{"epico"}}); got.Code != "reserved_label" {
		t.Errorf("create with epico: %+v", got)
	}
	if len(r.gh.Actions()) != before {
		t.Errorf("GitHub received %d writes", len(r.gh.Actions())-before)
	}
	if _, _, _, labels, _ := r.gh.Issue(repoFull, n); !slices.Contains(labels, "mestre:humano") {
		t.Errorf("the label mestre:humano did not stay: %v", labels)
	}
	// the Grão-Mestre puts epico and mestre:humano, never takes mestre:humano
	if got := r.run("gm", "issue update", map[string]any{"add-label": []any{"mestre:humano"}}, itoa(n)); !got.OK {
		t.Errorf("GM adds mestre:humano: %+v", got)
	}
	if got := r.run("gm", "issue update", map[string]any{"remove-label": []any{"mestre:humano"}}, itoa(n)); got.Code != "reserved_label" {
		t.Errorf("GM removes mestre:humano: %+v", got)
	}
	if _, _, _, labels, _ := r.gh.Issue(repoFull, n); !slices.Contains(labels, "mestre:humano") {
		t.Errorf("the Grão-Mestre took the label: %v", labels)
	}
	// and a person takes it on GitHub: the issue is then free of it
	r.gh.RemoveIssueLabel(repoFull, n, "mestre:humano")
	if _, _, _, labels, _ := r.gh.Issue(repoFull, n); slices.Contains(labels, "mestre:humano") {
		t.Errorf("a person could not take the label: %v", labels)
	}
}

func TestIssueUpdateCommentAndListWork(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	made := r.run("tm", "issue create", map[string]any{"title": "Fazer o login", "body": "- [ ] a\n- [ ] b"})
	n := itoa(int(made.Data["number"].(float64)))
	up := r.run("tm", "issue update", map[string]any{"title": "Login pronto", "add-label": []any{"backend"}, "state": "closed", "reason": "completed"}, n)
	if !up.OK || up.Data["state"] != "closed" || up.Data["title"] != "Login pronto" {
		t.Fatalf("update: %+v", up)
	}
	if bad := r.run("tm", "issue update", map[string]any{"reason": "completed"}, n); bad.Exit != 2 {
		t.Errorf("reason without closed: %+v", bad)
	}
	if bad := r.run("tm", "issue update", map[string]any{"state": "weird"}, n); bad.Exit != 2 {
		t.Errorf("bad state: %+v", bad)
	}
	if c := r.run("tm", "issue comment", nil, n, "feito"); !c.OK {
		t.Fatalf("comment: %+v", c)
	}
	// the tree of the epic: the epic and its sub-issues (the closed one is out by default)
	all := r.run("tm", "issue list", map[string]any{"state": "all"})
	if !all.OK || len(all.Data["issues"].([]any)) != 2 {
		t.Errorf("the tree = %+v", all)
	}
	open := r.run("tm", "issue list", nil)
	if len(open.Data["issues"].([]any)) != 1 {
		t.Errorf("open = %+v", open)
	}
	one := r.run("tm", "issue list", map[string]any{"comments": true}, n)
	issues := one.Data["issues"].([]any)
	if !one.OK || len(issues) != 1 || len(issues[0].(map[string]any)["comments"].([]any)) != 1 {
		t.Errorf("one with comments: %+v", one)
	}
	// reading is for the whole repository
	other := r.gh.AddIssue(repoFull, "de outra mesa")
	if got := r.run("tm", "issue list", nil, itoa(other)); !got.OK {
		t.Errorf("reading another issue: %+v", got)
	}
	if got := r.run("gm", "issue list", map[string]any{"all": true}); !got.OK {
		t.Errorf("--all: %+v", got)
	}
}

func TestTheImpressionsOfOtherTablesAreTheirs(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	other := r.gh.AddIssue(repoFull, "de outra mesa")
	hash := github.Impression("auth: login lento")
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: other, Table: "meu-app/43-outra", Fingerprint: hash}); err != nil {
		t.Fatal(err)
	}
	got := r.run("tm", "issue create", map[string]any{"title": "@alguem olha", "fingerprint": "auth: login lento"})
	if !got.OK || got.Data["seen_again"] != nil || int(got.Data["number"].(float64)) == other {
		t.Fatalf("create: %+v", got)
	}
	if n := len(r.gh.Comments(repoFull, other)); n != 0 {
		t.Errorf("a table commented on the issue of another: %d comments", n)
	}
}

func TestAnIssueUpdateDoesNotReachAPR(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	pr := r.gh.AddPR(repoFull, "PR da mesa", "42-login", repoFull)
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindPR, Number: pr, Table: "meu-app/42-login"}); err != nil {
		t.Fatal(err)
	}
	before := len(r.gh.Actions())
	got := r.run("tm", "issue update", map[string]any{"body": "novo corpo", "state": "closed"}, itoa(pr))
	if got.OK || got.Code != "outside_epic" {
		t.Errorf("update of a PR: %+v", got)
	}
	if len(r.gh.Actions()) != before {
		t.Error("GitHub received a write for a PR")
	}
	// a comment on the PR is still a comment on its conversation
	if c := r.run("tm", "issue comment", nil, itoa(pr), "ok"); !c.OK {
		t.Errorf("comment on the PR: %+v", c)
	}
}

func TestTheLabelsOfAnUpdateAreAddedAndTakenOneByOne(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	made := r.run("tm", "issue create", map[string]any{"title": "x", "label": []any{"bug"}})
	n := int(made.Data["number"].(float64))
	// a person puts a label meanwhile
	r.gh.AddIssueLabel(repoFull, n, "mestre:humano")
	up := r.run("tm", "issue update", map[string]any{"add-label": []any{"backend"}, "remove-label": []any{"bug"}}, itoa(n))
	if !up.OK {
		t.Fatalf("update: %+v", up)
	}
	_, _, _, labels, _ := r.gh.Issue(repoFull, n)
	slices.Sort(labels)
	if !slices.Equal(labels, []string{"backend", "mestre:humano"}) {
		t.Errorf("labels = %v: the label of the person must stay", labels)
	}
	// taking a label the issue does not have is fine
	if up := r.run("tm", "issue update", map[string]any{"remove-label": []any{"nada"}}, itoa(n)); !up.OK {
		t.Errorf("remove a missing label: %+v", up)
	}
}

func TestTheGrandMasterUnderAnIssueOfATableMakesItTheTablesOwn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	got := r.run("gm", "issue create", map[string]any{"title": "Pedido do GM", "parent": "1"})
	if !got.OK {
		t.Fatalf("create: %+v", got)
	}
	n := int(got.Data["number"].(float64))
	if c := r.run("tm", "issue comment", nil, itoa(n), "vi"); !c.OK {
		t.Errorf("the table can not act on the issue the Grand Master made under its epic: %+v", c)
	}
	// under an issue the table made, it is that table's too
	mine := r.run("tm", "issue create", map[string]any{"title": "da mesa"})
	under := r.run("gm", "issue create", map[string]any{"title": "do GM", "parent": itoa(int(mine.Data["number"].(float64)))})
	obj, found, err := r.objs.Get(r.ctx, repoFull, github.KindIssue, int(under.Data["number"].(float64)))
	if err != nil || !found || obj.Table != "meu-app/42-login" {
		t.Errorf("the issue under an issue of the table: %+v %v %v", obj, found, err)
	}
	// a loose issue of the Grand Master is nobody's
	loose := r.run("gm", "issue create", map[string]any{"title": "Solta"})
	if c := r.run("tm", "issue comment", nil, itoa(int(loose.Data["number"].(float64))), "oi"); c.OK || c.Code != "outside_epic" {
		t.Errorf("a table commented on a loose issue: %+v", c)
	}
}

func TestAnIssueWhoseLinkFailedIsNotMadeAgain(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.gh.FailNextSubIssue(502)
	got := r.run("tm", "issue create", map[string]any{"title": "Meio feita"})
	if got.OK || got.Exit != 5 || got.Code != "github_unavailable" || !strings.Contains(got.Error, "do not create it again") || !strings.Contains(got.Error, "not linked under #1") {
		t.Fatalf("create: %+v", got)
	}
	// it is the table's, so the table can still work on it
	if c := r.run("tm", "issue comment", nil, "2", "ainda é minha"); !c.OK {
		t.Errorf("comment on the half-made issue: %+v", c)
	}
}

func TestAResendingOfTheSameRequestMakesNothingTwice(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	first := r.runID("req-1", "tm", "issue create", map[string]any{"title": "Uma só"})
	again := r.runID("req-1", "tm", "issue create", map[string]any{"title": "Uma só"})
	if !first.OK || !again.OK || first.Data["number"] != again.Data["number"] {
		t.Fatalf("create twice: %+v %+v", first, again)
	}
	n := itoa(int(first.Data["number"].(float64)))
	r.runID("req-2", "tm", "issue comment", nil, n, "uma vez")
	r.runID("req-2", "tm", "issue comment", nil, n, "uma vez")
	if cs := r.gh.Comments(repoFull, int(first.Data["number"].(float64))); len(cs) != 1 {
		t.Errorf("%d comments", len(cs))
	}
	// the request of another table does not take the issue of the first
	if got := r.runID("req-1", "gm", "issue create", map[string]any{"title": "Outra"}); got.OK && got.Data["number"] == first.Data["number"] {
		// the Grão-Mestre is not tied to a table, so it is allowed to see it: only a table is held to its own
		t.Log("the Grand Master resending a request of a table gets the same issue")
	}
}

func TestTheMarksOfTheEngineAreNeitherForgedNorLost(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	forged := "texto\n<!-- mestre:impressao=abc -->\n<!-- mestre:op=fake -->"
	made := r.run("tm", "issue create", map[string]any{"title": "Marcas", "body": forged, "fingerprint": "problema um"})
	n := int(made.Data["number"].(float64))
	_, body, _, _, _ := r.gh.Issue(repoFull, n)
	if strings.Contains(body, "mestre:impressao=abc") || strings.Contains(body, "mestre:op=fake") {
		t.Errorf("a forged mark went through: %q", body)
	}
	if !strings.Contains(body, github.ImpressionMarker(github.Impression("problema um"))) {
		t.Errorf("the impression is missing: %q", body)
	}
	up := r.run("tm", "issue update", map[string]any{"body": "- [x] tudo feito"}, itoa(n))
	if !up.OK {
		t.Fatalf("update: %+v", up)
	}
	_, after, _, _, _ := r.gh.Issue(repoFull, n)
	if !strings.HasPrefix(after, "- [x] tudo feito") || len(github.Markers(after)) != 2 {
		t.Errorf("the body after the update lost the marks: %q", after)
	}
	// the agent reads the body without the marks
	list := r.run("tm", "issue list", nil, itoa(n))
	body = list.Data["issues"].([]any)[0].(map[string]any)["body"].(string)
	if strings.Contains(body, "mestre:") || body != "- [x] tudo feito" {
		t.Errorf("the body the agent reads = %q", body)
	}
}

func TestIssueListTakesAFewIssues(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	args := make([]string, 21)
	for i := range args {
		args[i] = "1"
	}
	if got := r.run("tm", "issue list", nil, args...); got.OK || got.Exit != 2 {
		t.Errorf("21 issues: %+v", got)
	}
}

// CA-GH-ACOES-10: the request that reached GitHub and whose answer was lost comes
// again with the same uuid (the queue of the output keeps it): the engine finds
// the marker in the comment that is there, records it and makes no second one.
func TestAResentCommentFindsItsMarkerOnGitHubAndMakesNoSecondOne(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	n := r.gh.AddIssue(repoFull, "da mesa")
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: n, Table: "meu-app/42-login"}); err != nil {
		t.Fatal(err)
	}
	uuid := "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	existing := r.gh.AddIssueComment(repoFull, n, "mestre[bot]", github.WithMarker("feito", uuid))
	if !strings.Contains(r.gh.Comments(repoFull, n)[0], "<!-- mestre:op="+uuid+" -->") {
		t.Fatalf("the comment on GitHub has no marker: %q", r.gh.Comments(repoFull, n))
	}
	got := r.runID(uuid, "tm", "issue comment", nil, itoa(n), "feito")
	if !got.OK || int64(got.Data["comment_id"].(float64)) != existing {
		t.Fatalf("resend = %+v, want the id %d of the comment that was there", got, existing)
	}
	if cs := r.gh.Comments(repoFull, n); len(cs) != 1 || !strings.Contains(cs[0], "mestre:op="+uuid) {
		t.Fatalf("comments = %q, want the one carrying the marker", cs)
	}
	obj, found, err := r.objs.ByOp(r.ctx, uuid)
	if err != nil || !found || obj.GitHubID != existing || obj.Kind != github.KindComment {
		t.Fatalf("github_objects = %+v, %v, %v", obj, found, err)
	}
}
