//go:build full

package inbox_test

import (
	"strings"
	"testing"
	"time"
)

// CA-GH-EV-07
func TestTheFailureOfTheCIOfATablePRReachesTheGrandMasterWithinOneCycleWithTheLinkOfTheCheck(t *testing.T) {
	t.Parallel()
	r := newGHRig(t)
	r.clk.Advance(10 * time.Second)
	n := r.gh.AddPR("lucas/meu-app", "feat: login", "feat/login", "lucas/meu-app")
	sha := strings.Repeat("0", 39) + "1"
	r.gh.SetPull("lucas/meu-app", n, sha, false, "clean")
	r.gh.AddCheck("lucas/meu-app", sha, "ci", "") // running
	if _, err := r.p.Cycle(r.ctx, r.watch); err != nil {
		t.Fatal(err)
	}
	for _, d := range r.deliv.got {
		if strings.Contains(d.Text, "CI") {
			t.Fatalf("the Grand Master was told of a CI that had not ended:\n%s", d.Text)
		}
	}
	before := len(r.deliv.got)
	r.clk.Advance(30 * time.Second)
	r.gh.FinishCheck("lucas/meu-app", sha, "ci", "failure")
	if _, err := r.p.Cycle(r.ctx, r.watch); err != nil {
		t.Fatal(err)
	}
	pending, err := r.svc.Store().Pending(r.ctx, r.gm.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, it := range pending {
		if strings.Contains(it.Text, "ci: failure") {
			found = it.Text
		}
	}
	for _, d := range r.deliv.got[before:] {
		if strings.Contains(d.Text, "ci: failure") {
			found = d.Text
		}
	}
	if found == "" || !strings.Contains(found, "/runs/") || !strings.Contains(found, "mesa-a") {
		t.Fatalf("the Grand Master did not get the failure of the CI with its link:\n%q", found)
	}
}
