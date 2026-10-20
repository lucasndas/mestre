package approvals

import (
	"fmt"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
)

// MergeOrder is a merge the Grão-Mestre wants to make (`mestre gh merge`): the PR,
// the commit it was looked at in, the branch it goes into, and who asks.
type MergeOrder struct {
	Workspace, Table string
	// Repo and Number are the PR; Head is its head commit and Base the branch it
	// goes into, as the engine read them just before asking.
	Repo   string
	Number int
	Head   string
	Base   string
	// Method is the method of the merge (github.MergeMethod…; "" is merge), shown to who
	// approves: it is the choice of who merges, not part of what the approval binds.
	Method string
	// Requester is the character that asked.
	Requester string
}

// MergeRequest is the request of type merge for the order: it names the PR and the
// commit, and its target is the exact one the request is bound to
// (docs/aprovacoes/fluxo.md#vínculo-ao-estado-exato), so that a PR that moves after the
// decision invalidates it. The method of the merge is not part of the target: it
// is the choice of who merges, inside what the repository allows.
func MergeRequest(o MergeOrder) (chain.OpenRequest, error) {
	switch {
	case o.Workspace == "" || o.Repo == "" || o.Number <= 0:
		return chain.OpenRequest{}, fmt.Errorf("approvals: a merge needs the workspace, the repository and the number of the PR")
	case !github.ValidSHA(o.Head) || o.Base == "":
		return chain.OpenRequest{}, fmt.Errorf("approvals: a merge needs the full hash of the head of the PR and the branch it goes into")
	}
	repo, err := github.NormalizeRepo(o.Repo)
	if err != nil {
		return chain.OpenRequest{}, err
	}
	o.Repo = repo
	if o.Method == "" {
		o.Method = github.MergeMethodMerge
	}
	switch o.Method {
	case github.MergeMethodMerge, github.MergeMethodSquash, github.MergeMethodRebase:
	default:
		return chain.OpenRequest{}, fmt.Errorf("approvals: %q is not a method of merge", o.Method)
	}
	return chain.OpenRequest{
		Type: classes.RequestMerge, Workspace: o.Workspace, Table: o.Table, Requester: o.Requester,
		About:  fmt.Sprintf("merge do PR #%d de %s (%s) em %s, método %s", o.Number, o.Repo, shortSHA(o.Head), o.Base, o.Method),
		PR:     &chain.PRRef{Repo: o.Repo, Number: o.Number, SHA: o.Head},
		Target: PRTarget(o.Repo, o.Number, o.Head, o.Base),
	}, nil
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
