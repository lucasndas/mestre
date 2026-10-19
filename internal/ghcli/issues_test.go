package ghcli_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/github"
)

// CA-CLI-GH-01
func TestAnIssueOutsideTheEpicOfTheTableIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	other := r.gh.AddIssue(repoFull, "de outra mesa")
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: other, Table: "43-outra"}); err != nil {
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
	_ = r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: other, Table: "43-outra"})
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
	if !strings.HasPrefix(body, "Recorrência de #") || !slices.Contains(labels, "recorrencia") {
		t.Errorf("the recurrence: body %q labels %v", body, labels)
	}
	if _, has := r.gh.Labels(repoFull)["recorrencia"]; !has {
		t.Error("the label recorrencia was not created")
	}
}

// CA-CLI-GH-12
func TestTheReservedLabelsAreNotChangedByATable(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	n := r.gh.AddIssue(repoFull, "da mesa")
	if err := r.objs.Register(r.ctx, github.Object{Repo: repoFull, Kind: github.KindIssue, Number: n, Table: "42-login"}); err != nil {
		t.Fatal(err)
	}
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
	// the Grão-Mestre puts epico and mestre:humano, never takes mestre:humano
	if got := r.run("gm", "issue update", map[string]any{"add-label": []any{"mestre:humano"}}, itoa(n)); !got.OK {
		t.Errorf("GM adds mestre:humano: %+v", got)
	}
	if got := r.run("gm", "issue update", map[string]any{"remove-label": []any{"mestre:humano"}}, itoa(n)); got.Code != "reserved_label" {
		t.Errorf("GM removes mestre:humano: %+v", got)
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
