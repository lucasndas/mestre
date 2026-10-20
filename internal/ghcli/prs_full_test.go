//go:build full

package ghcli_test

import (
	"slices"
	"strings"
	"testing"
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
