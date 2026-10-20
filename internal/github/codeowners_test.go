package github_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/pkg/clock"
)

type ownersRig struct {
	gh *fakegithub.Server
	co github.CodeOwners
	pr int
}

func newOwnersRig(t *testing.T, file string, paths ...string) *ownersRig {
	t.Helper()
	gh, err := fakegithub.New(clock.NewFake(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)), t)
	if err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("lucas", "meu-app")
	gh.IssueToken("tok", "mestre-grao-mestre")
	gh.SetBranchRules("lucas/meu-app", "main", fakegithub.Rule{Type: "pull_request", Parameters: map[string]any{"required_approving_review_count": 1, "require_code_owner_review": true}})
	if file != "" {
		gh.SetFile("lucas/meu-app", "main", ".github/CODEOWNERS", file)
	}
	r := &ownersRig{gh: gh, pr: gh.AddPR("lucas/meu-app", "x", "42-x", "lucas/meu-app")}
	for _, p := range paths {
		gh.AddPullFile("lucas/meu-app", r.pr, p, "@@ -1 +1 @@\n-a\n+b\n")
	}
	rest := &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(gh)},
		Token: func(context.Context, string) (string, error) { return "tok", nil }}
	r.co = github.CodeOwners{Client: rest}
	return r
}

func TestTheOwnersOfAPRAreThoseOfTheLastPatternThatMatchesEachFile(t *testing.T) {
	t.Parallel()
	file := "# owners\n* @lucas\n*.sql @marina # migrations\n/docs/ @rita\ndocs/api/** @pedro\nsrc/*/auth.go @ana\n"
	r := newOwnersRig(t, file, "main.go", "db/0001.sql", "docs/guide.md", "docs/api/v1/x.md", "src/web/auth.go", "src/web/deep/auth.go")
	got, err := r.co.Required(t.Context(), "lucas/meu-app", r.pr)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lucas", "marina", "rita", "pedro", "ana"}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("owners = %v; want %v", got, want)
	}
}

func TestAFileWithNoOwnerRequiresNobodyAndABranchWithoutTheRuleAsksNothing(t *testing.T) {
	t.Parallel()
	r := newOwnersRig(t, "/docs/ @rita\n", "main.go")
	if got, err := r.co.Required(t.Context(), "lucas/meu-app", r.pr); err != nil || len(got) != 0 {
		t.Fatalf("owners = %v, %v; want none", got, err)
	}
	r.gh.SetBranchRules("lucas/meu-app", "main", fakegithub.Rule{Type: "pull_request", Parameters: map[string]any{"required_approving_review_count": 1}})
	if got, err := r.co.Required(t.Context(), "lucas/meu-app", r.pr); err != nil || got != nil {
		t.Fatalf("owners with no code owner rule = %v, %v", got, err)
	}
}

// What the engine does not understand is not "nobody is required".
func TestACodeOwnersFileTheEngineDoesNotUnderstandIsAnError(t *testing.T) {
	t.Parallel()
	for name, file := range map[string]string{
		"a team":      "* @acme/devs\n",
		"an e-mail":   "* dev@example.com\n",
		"a negation":  "!docs/ @rita\n",
		"a class":     "src/[ab].go @rita\n",
		"no file":     "",
		"owner-less":  "src/ x\n",
		"not a login": "* rita\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newOwnersRig(t, file, "src/a.go")
			if got, err := r.co.Required(t.Context(), "lucas/meu-app", r.pr); err == nil {
				t.Fatalf("owners = %v; want an error", got)
			} else if strings.Contains(err.Error(), "ghs_") {
				t.Fatalf("the error carries a token: %v", err)
			}
		})
	}
}
