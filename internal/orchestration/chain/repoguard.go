package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/classes"
)

// WorkspaceRepos gives the repository (`owner/name`) of a workspace.
type WorkspaceRepos interface {
	Repo(ctx context.Context, workspace string) (string, error)
}

// ErrWrongRepo: the target of a request is a repository other than the one of
// its workspace. A request made in one workspace never applies a review, a merge
// or a ruleset in another repository.
var ErrWrongRepo = errors.New("chain: the target of the request is not the repository of its workspace")

// TargetRepo is the repository a request of PR, merge or settings is about.
func TargetRepo(r Request) string {
	switch r.Type {
	case classes.RequestPR, classes.RequestMerge:
		if r.PR != nil {
			return r.PR.Repo
		}
	case classes.RequestRepoSettings:
		var t struct {
			Repo string `json:"repo"`
		}
		if json.Unmarshal(r.Target, &t) == nil {
			return t.Repo
		}
	}
	return ""
}

func bindsRepo(t classes.RequestType) bool {
	return t == classes.RequestPR || t == classes.RequestMerge || t == classes.RequestRepoSettings
}

// SameRepo says two names are the same repository (GitHub spells it in any case).
func SameRepo(a, b string) bool { return a != "" && strings.EqualFold(a, b) }

// guardRepo refuses, closed, a request whose target is not the repository of its
// workspace, or whose workspace has none that can be read.
func (s *Service) guardRepo(ctx context.Context, r Request) error {
	if s.o.Repos == nil || !bindsRepo(r.Type) {
		return nil
	}
	want, err := s.o.Repos.Repo(ctx, r.Workspace)
	if err != nil {
		return fmt.Errorf("%w: the repository of %s could not be read: %v", ErrWrongRepo, r.Workspace, err)
	}
	if got := TargetRepo(r); !SameRepo(want, got) {
		return fmt.Errorf("%w: %q is not %q", ErrWrongRepo, got, want)
	}
	return nil
}
