package chain_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
)

func TestARepositoryIsTheSameOnlyByItsAsciiNameInAnyCase(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"lucas/Meu_App", "lucas/meu_app", true},
		{"lucas/meu_app", "lucas/outro", false},
		{"", "", false},
		{"lucas/kit", "lucas/\u212Ait", false}, // the Kelvin sign folds to k
		{"lucas/ssh", "lucas/\u017Fsh", false},
	} {
		if got := chain.SameRepo(tc.a, tc.b); got != tc.want {
			t.Errorf("SameRepo(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestTheRepositoryOfARequestIsTheOneOfItsTarget(t *testing.T) {
	t.Parallel()
	pr := chain.Request{Type: classes.RequestMerge, PR: &chain.PRRef{Repo: "lucas/a"}}
	if got := chain.TargetRepo(pr); got != "lucas/a" {
		t.Errorf("merge = %q", got)
	}
	if got := chain.TargetRepo(chain.Request{Type: classes.RequestPR}); got != "" {
		t.Errorf("a PR request without a PR = %q", got)
	}
	st := chain.Request{Type: classes.RequestRepoSettings, Target: []byte(`{"kind":"ruleset","repo":"lucas/b"}`)}
	if got := chain.TargetRepo(st); got != "lucas/b" {
		t.Errorf("settings = %q", got)
	}
	if got := chain.TargetRepo(chain.Request{Type: classes.RequestRepoSettings, Target: []byte(`nope`)}); got != "" {
		t.Errorf("settings with a target that is not JSON = %q", got)
	}
}
