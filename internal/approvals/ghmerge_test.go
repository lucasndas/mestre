package approvals_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
)

// The request of a merge is of type merge, names the PR and the
// commit, and is bound to the exact target (repository in lower case, number, head and
// base), the one GitHubSource reads again to see whether the PR moved.
func TestTheMergeRequestIsBoundToTheExactTargetOfThePR(t *testing.T) {
	t.Parallel()
	req, err := approvals.MergeRequest(approvals.MergeOrder{Workspace: "meu-app", Table: "42-login", Repo: "Lucas/Meu-App", Number: 7,
		Head: "0123456789abcdef0123456789abcdef01234567", Base: "main", Method: "squash", Requester: "gm1"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Type != classes.RequestMerge || req.Workspace != "meu-app" || req.Table != "42-login" || req.Requester != "gm1" {
		t.Errorf("request = %+v", req)
	}
	if req.PR == nil || req.PR.Number != 7 || req.PR.SHA != "0123456789abcdef0123456789abcdef01234567" || !strings.EqualFold(req.PR.Repo, "lucas/meu-app") {
		t.Errorf("PR = %+v", req.PR)
	}
	var target map[string]any
	if err := json.Unmarshal(req.Target, &target); err != nil || target["repo"] != "lucas/meu-app" || target["pr"] != float64(7) || target["head"] != req.PR.SHA || target["base"] != "main" {
		t.Errorf("target = %s, %v", req.Target, err)
	}
	if string(req.Target) != string(approvals.PRTarget("Lucas/Meu-App", 7, req.PR.SHA, "main")) {
		t.Errorf("the target is not the one PRTarget makes: %s", req.Target)
	}
	if !strings.Contains(req.About, "#7") || !strings.Contains(req.About, "0123456") || !strings.Contains(req.About, "squash") {
		t.Errorf("about = %q", req.About)
	}
	for _, bad := range []approvals.MergeOrder{
		{Repo: "lucas/meu-app", Number: 7, Head: "abc", Base: "main"},
		{Workspace: "meu-app", Number: 7, Head: "abc", Base: "main"},
		{Workspace: "meu-app", Repo: "lucas/meu-app", Head: "abc", Base: "main"},
		{Workspace: "meu-app", Repo: "lucas/meu-app", Number: 7, Base: "main"},
		{Workspace: "meu-app", Repo: "lucas/meu-app", Number: 7, Head: "abc"},
		{Workspace: "meu-app", Repo: "lucas/meu-app", Number: 7, Head: "0123456789abcdef0123456789abcdef01234567"},
		{Workspace: "meu-app", Repo: "lucas/meu-app", Number: 7, Head: "main", Base: "main"},
		{Workspace: "meu-app", Repo: "lucas/meu-app", Number: 7, Head: "0123456789ABCDEF0123456789ABCDEF01234567", Base: "main"},
		{Workspace: "meu-app", Repo: "nao e repo", Number: 7, Head: "0123456789abcdef0123456789abcdef01234567", Base: "main"},
		{Workspace: "meu-app", Repo: "lucas/meu-app", Number: 7, Head: "0123456789abcdef0123456789abcdef01234567", Base: "main", Method: "ff"},
	} {
		if _, err := approvals.MergeRequest(bad); err == nil {
			t.Errorf("an incomplete order was accepted: %+v", bad)
		}
	}
}
