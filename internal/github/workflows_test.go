package github_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
)

// Part of CA-GH-ID-05: the decision of the push (the command `mestre pr update`
// that asks for it is of the stage of the CLI of the issues and PRs).
func TestAPushThatChangesTheCIAsksForApprovalWithAskAndIsRefusedWithNever(t *testing.T) {
	t.Parallel()
	ci := []string{"src/login.go", ".github/workflows/ci.yml"}
	if d, err := github.CheckPush(ci, classes.WorkflowsAsk); err != nil || d != github.PushNeedsApproval {
		t.Errorf("ask: %v, %v; want a request of approval (envio_workflows)", d, err)
	}
	if _, err := github.CheckPush(ci, classes.WorkflowsNever); !errors.Is(err, github.ErrWorkflowNotAllowed) {
		t.Errorf("never = %v, want workflow_not_allowed", err)
	}
	if _, err := github.CheckPush(ci, "anything else"); !errors.Is(err, github.ErrWorkflowNotAllowed) {
		t.Errorf("an unknown policy = %v, want refused (fail closed)", err)
	}
	if d, err := github.CheckPush(ci, classes.WorkflowsAllow); err != nil || d != github.PushAllowed {
		t.Errorf("allow: %v, %v", d, err)
	}
	for _, files := range [][]string{{"src/a.go"}, {".github/CODEOWNERS"}, nil} {
		if d, err := github.CheckPush(files, classes.WorkflowsNever); err != nil || d != github.PushAllowed {
			t.Errorf("%v with never: %v, %v; the CI did not change", files, d, err)
		}
	}
	for _, f := range []string{".github/workflows/x.yml", `.github\workflows\x.yml`, ".GitHub/Workflows/x.yml", "./.github/workflows/../workflows/y.yml"} {
		if !github.TouchesWorkflows([]string{f}) {
			t.Errorf("%q is a change of the workflows", f)
		}
	}
	if github.TouchesWorkflows([]string{"docs/.github/workflows-notes.md", "not.github/workflows/x"}) {
		t.Error("a path that only looks like it is taken for the workflows")
	}
}

func TestTheAppOfAClassIsMadeWithTheWorkflowsPermissionOnlyIfTheClassMayChangeTheCI(t *testing.T) {
	t.Parallel()
	for p, want := range map[classes.Workflows]bool{classes.WorkflowsNever: false, classes.WorkflowsAsk: true, classes.WorkflowsAllow: true, "": false} {
		if got := github.NeedsWorkflowsPermission(p); got != want {
			t.Errorf("%q: %v, want %v", p, got, want)
		}
	}
	if _, ok := github.RolePermissions(github.RoleClass, false)["workflows"]; ok {
		t.Error("a class that never changes the CI got the permission")
	}
	if github.RolePermissions(github.RoleClass, true)["workflows"] != "write" {
		t.Error("a class that may change the CI did not get the permission")
	}
	for _, role := range []string{github.RoleGrandMaster, github.RoleMestre} {
		if _, ok := github.RolePermissions(role, true)["workflows"]; ok {
			t.Errorf("%s has the Workflows permission", role)
		}
	}
	if github.RolePermissions(github.RoleGrandMaster, false)["administration"] != "write" || github.RolePermissions(github.RoleMestre, false)["administration"] != "" {
		t.Error("only the Grão-Mestre has Administration")
	}
	// a class moved to ask before the Owner accepted the permission: the push stops
	app := github.App{Name: "lucas-mestre-qa", Workflows: false}
	if err := github.CheckApp(app, []string{".github/workflows/ci.yml"}); !errors.Is(err, github.ErrWorkflowPermission) {
		t.Errorf("a push without the permission = %v", err)
	}
	if err := github.CheckApp(github.App{Workflows: true}, []string{".github/workflows/ci.yml"}); err != nil {
		t.Errorf("with the permission: %v", err)
	}
	if err := github.CheckApp(app, []string{"a.go"}); err != nil {
		t.Errorf("a push that does not touch the CI: %v", err)
	}
}

func TestAPathThatLooksLikeItIsNotTheCIButOneThatGitQuotedOrWithASlashIs(t *testing.T) {
	t.Parallel()
	for _, f := range []string{"/.github/workflows/x.yml", `".github/workflows/\303\251.yml"`, `"src/\303\251.go"`} {
		if !github.TouchesWorkflows([]string{f}) {
			t.Errorf("%q escapes the rule of the CI", f)
		}
	}
}

func TestACommitIsNotAbleToCarryAnotherTrailerThroughTheNameOfTheClass(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.ready(github.App{Name: "lucas-mestre-mestre", Role: github.RoleMestre}, 2, 12, 101, false, "lucas/x")
	id, err := github.Identities{Apps: w.apps}.For(w.ctx, github.Who{Role: "character", Class: "QA\nCo-authored-by: x <x@y>\r\u2028"})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range id.Trailers() {
		if strings.ContainsAny(line, "\r\n\u2028") || strings.Count(line, ":") != 1 {
			t.Errorf("trailer %q", line)
		}
	}
	if m := id.Mark("oi"); strings.ContainsAny(m, "\r\n\u2028") {
		t.Errorf("mark %q", m)
	}
}
