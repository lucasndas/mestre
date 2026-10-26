//go:build full

package ghcli_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

// CA-CLI-GH-03
func TestPROpenSendsTheMirrorBranchAndOpensThePRWithTheAppMestreAndTheEpic(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	got := r.run("tm", "pr open", nil)
	if !got.OK {
		t.Fatalf("open: %+v", got)
	}
	n := int(got.Data["number"].(float64))
	// the branch sent is the one of the mirror of the Citadel, to the repository on GitHub
	if want := []string{r.remote + " refs/heads/42-login:refs/heads/42-login"}; !slices.Equal(r.git.Pushes(), want) {
		t.Errorf("pushes = %q, want %q", r.git.Pushes(), want)
	}
	if head, err := r.git.Handle(r.remote).Data().Resolve("42-login"); err != nil || string(head) != got.Data["head_sha"] {
		t.Errorf("the remote has %v (%v), want the head of the mirror %v", head, err, got.Data["head_sha"])
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
	if body := r.gh.PullBody(repoFull, n); !strings.Contains(body, "Closes #1") {
		t.Errorf("the description lacks Closes #1 (the epic): %q", body)
	}
}

func TestAPRIsMergeableOnlyWhenGitHubSaysClean(t *testing.T) {
	t.Parallel()
	for state, want := range map[string]bool{"clean": true, "has_hooks": true, "unknown": false, "unstable": false, "blocked": false, "behind": false, "": false} {
		r := newRig(t)
		pr := prOfTheTable(t, r)
		r.gh.SetPull(repoFull, pr, strings.Repeat("a", 40), false, state)
		got := r.run("tm", "pr status", nil)
		if got.Data["mergeable"] != want {
			t.Errorf("%q: mergeable %v, want %v (%v)", state, got.Data["mergeable"], want, got.Data["blocked_by"])
		}
	}
}

// CA-CLI-GH-05: a GitHub that refuses the sending for the lack of the permission
// "Workflows": `mestre pr update` is exit 3 workflow_permission, the sending is
// tried once, and the Grand Master gets the event with the reason.
func TestPRUpdateRefusedForTheWorkflowsPermissionIsExit3AndTheGrandMasterIsTold(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	prOfTheTable(t, r)
	var told []string
	r.h.Refused = func(_ context.Context, c characters.Character, code, reason string) {
		told = append(told, c.Table+" "+code+": "+reason)
	}
	r.git.FailNextPush(gitxErr("workflow"))
	got := r.run("tm", "pr update", nil)
	if got.OK || got.Exit != 3 || got.Code != "workflow_permission" {
		t.Fatalf("update: %+v, want exit 3 workflow_permission", got)
	}
	if len(r.pushes()) != 1 {
		t.Errorf("the sending was tried %d times, want once: %q", len(r.pushes()), r.pushes())
	}
	if len(told) != 1 || !strings.HasPrefix(told[0], "42-login workflow_permission: ") {
		t.Errorf("the Grand Master was told %q", told)
	}
}
