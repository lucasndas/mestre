package ghcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
)

// Approving applies the approval that stands for a PR at a commit with a review
// on GitHub (*approvals.Approver).
type Approving interface {
	Approve(ctx context.Context, o approvals.ApproveOrder) (approvals.Approval, error)
}

// PullMerger is the part of the API of the App "Grão-Mestre" that reads a PR and
// merges it (github.REST), with a token that has the permissions of a merge and
// only for the repository of the workspace.
type PullMerger interface {
	PullData(ctx context.Context, repo string, n int) (github.PullData, error)
	MergePull(ctx context.Context, repo string, n int, sha, method string) (github.MergeResult, error)
}

// Merging is what `gh merge` needs besides GitHub: the client of the merge, the
// approvals of the chain and the request.
type Merging interface {
	// Merger gives the client for the repository of the workspace.
	Merger(ctx context.Context, workspace string) (PullMerger, error)
	// Standing is the newest approved request of type merge that still stands for the PR
	// at the commit and the base it goes into, and the newest open one (0: none).
	Standing(ctx context.Context, workspace, repo string, n int, sha, base string) (approved, open int64, err error)
	// Redeem spends the point exception of an Approver or above ("I approve the merge of PR
	// #42": one exact action, once, bound to the repository, the PR and the head) that is
	// active for the target, if there is one: used is true when it was spent, and the merge may
	// go on without a request. A target that moved since the exception was made expires it unused
	// (ErrExceptionExpired); none, or none that applies, is (0, false, nil).
	Redeem(ctx context.Context, workspace string, target json.RawMessage, character string) (decree int64, used bool, err error)
	// Ask opens the request of the merge.
	Ask(ctx context.Context, o chain.OpenRequest) (int64, error)
	// Table is the table whose branch the PR comes from ("": none).
	Table(ctx context.Context, workspace, headRef string) string
}

// Rulesets makes the request to the Owner for a ruleset of the repository of the
// workspace (*approvals.Settings).
type Rulesets interface {
	ProposeRuleset(ctx context.Context, workspace, proposer string, body map[string]any) (int64, error)
}

// ErrExceptionExpired: the exception was for another head of the PR: it expired unused.
var ErrExceptionExpired = errors.New("ghcli: the exception was for another commit of the PR and expired unused")

// prNumber reads the number of the PR, the only argument of approve and merge.
func prNumber(c *localchan.Call, usageText string) (int, *localchan.Response) {
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 {
		r := usage("%s", usageText)
		return 0, &r
	}
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(args[0]), "#"))
	if err != nil || n <= 0 {
		r := usage("the pull request is a number")
		return 0, &r
	}
	return n, nil
}

// approve is `mestre gh approve <pr> --reason "<…>" [--on-behalf "<user>"]`: the
// review that applies an approval that stands for the commit `gh review` read. The
// workspace is the one of who asks (the identity the engine knows), the repository
// is the one of that workspace, and there is no flag for either.
func (h *Handlers) approve(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.grandMaster(ctx, c)
	if bad != nil {
		return *bad
	}
	n, bad := prNumber(c, `usage: mestre gh approve <pr> --reason "<reason>" [--on-behalf "<user>"]`)
	if bad != nil {
		return *bad
	}
	reason, _ := flagString(c, "reason")
	if strings.TrimSpace(reason) == "" {
		return usage(`--reason is required: it becomes the text of the review`)
	}
	onBehalf, _ := flagString(c, "on-behalf")
	if h.Approver == nil || h.Heads == nil {
		return unavailable("the approval on GitHub is not available in this run")
	}
	read, err := h.Heads.Get(ctx, who.repo, n)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the commit that was read could not be looked up", "")
	}
	if read == "" {
		return localchan.Fail("head_changed", fmt.Sprintf("you did not read the pull request #%d", n), "read it with `mestre gh review <pr>` before you approve it").WithExit(localchan.ExitConflict)
	}
	if h.isLocked(ctx) {
		return lockedResponse()
	}
	if bad := h.leaseHeld(); bad != nil {
		return *bad
	}
	ap, err := h.Approver.Approve(ctx, approvals.ApproveOrder{Workspace: who.char.Workspace, Repo: who.repo, Number: n, SHA: read, Reason: github.StripMarkers(reason), OnBehalf: onBehalf})
	var ref *chain.Refusal
	switch {
	case err == nil:
	case errors.As(err, &ref) && ref.Code == approvals.CodeNoDelegation:
		return localchan.FailMsg(approvals.CodeNoDelegation, ref.Msg, "the credential of a person is used only with a decree of delegation").WithExit(localchan.ExitDenied)
	case errors.Is(err, approvals.ErrHeadChanged):
		return localchan.Fail("head_changed", err.Error(), "read the pull request again with `mestre gh review <pr>`").WithExit(localchan.ExitConflict)
	case errors.Is(err, approvals.ErrPRClosed):
		return localchan.Fail("not_open", fmt.Sprintf("the pull request #%d is not open", n), "").WithExit(localchan.ExitConflict)
	case errors.Is(err, github.ErrNotFound):
		return localchan.Fail("not_found", fmt.Sprintf("#%d does not exist", n), "").WithExit(localchan.ExitNotFound)
	case errors.As(err, &ref) && ref.Code == chain.CodeHumanRequired:
		return localchan.FailMsg("human_required", ref.Msg, "ask the people with `mestre approval ask`; nothing was sent to GitHub").WithExit(localchan.ExitDenied)
	default:
		return refusal(err)
	}
	data := map[string]any{"as": string(ap.As), "review": ap.Review, "request": ap.Request, "head_sha": read,
		"text": fmt.Sprintf("PR #%d aprovado no commit %.8s (%s)", n, read, ap.As)}
	return localchan.OK(data)
}

// merge is `mestre gh merge <pr> [--method merge|squash|rebase]`: the merge with the
// App "Grão-Mestre", for the commit the request of merge approved. With no approved
// request it opens one, and goes on only if the policy approved it at once; the
// rules of the repository apply, and nothing is worked around.
func (h *Handlers) merge(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.grandMaster(ctx, c)
	if bad != nil {
		return *bad
	}
	n, bad := prNumber(c, "usage: mestre gh merge <pr> [--method merge|squash|rebase]")
	if bad != nil {
		return *bad
	}
	method, _ := flagString(c, "method")
	switch method {
	case "":
		method = github.MergeMethodMerge
	case github.MergeMethodMerge, github.MergeMethodSquash, github.MergeMethodRebase:
	default:
		return usage("--method is merge, squash or rebase")
	}
	if h.Merges == nil {
		return unavailable("the merge is not available in this run")
	}
	ws := who.char.Workspace
	mc, err := h.Merges.Merger(ctx, ws)
	if err != nil {
		return localchan.Fail("github_unavailable", "GitHub is not available for the merge now", "try again later").WithExit(localchan.ExitUnavailable)
	}
	pr, err := mc.PullData(ctx, who.repo, n)
	switch {
	case errors.Is(err, github.ErrNotFound):
		return localchan.Fail("not_found", fmt.Sprintf("#%d does not exist", n), "").WithExit(localchan.ExitNotFound)
	case err != nil:
		return refusal(err)
	case pr.State != "open" || pr.Merged:
		return localchan.Fail("not_open", fmt.Sprintf("the pull request #%d is not open", n), "").WithExit(localchan.ExitConflict)
	}
	id, open, err := h.Merges.Standing(ctx, ws, who.repo, n, pr.HeadSHA, pr.BaseRef)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the approvals of the pull request could not be looked up", "")
	}
	// the exception of an Approver is the approval of this exact merge: it is spent first, once, and the
	// merge goes on with no request (GitHub's rules still apply; if it refuses, the exception stays spent)
	var byException int64
	var expired bool
	if id == 0 {
		dec, used, rerr := h.Merges.Redeem(ctx, ws, approvals.PRTarget(who.repo, n, pr.HeadSHA, pr.BaseRef), who.char.ID)
		switch {
		case errors.Is(rerr, ErrExceptionExpired):
			expired = true
		case rerr != nil:
			return localchan.Fail(localchan.CodeInternal, "the exceptions of the Approvers could not be looked up", "")
		case used:
			byException = dec
		}
	}
	if id == 0 && byException == 0 && open != 0 {
		// the request is already with the people: no other is made (a repetition of the command)
		return localchan.Fail("approval_pending", fmt.Sprintf("the merge of #%d waits for the approval of the request #%d", n, open), "run the command again after the approval").WithExit(localchan.ExitPending)
	}
	if id == 0 && byException == 0 {
		// the table is the one whose branch the PR comes from, and only a branch of the repository itself
		table := ""
		if strings.EqualFold(pr.HeadRepo, who.repo) {
			table = h.Merges.Table(ctx, ws, pr.HeadRef)
		}
		req, err := approvals.MergeRequest(approvals.MergeOrder{Workspace: ws, Table: table, Repo: who.repo, Number: n, Head: pr.HeadSHA, Base: pr.BaseRef, Method: method, Proposer: who.char.ID})
		if err != nil {
			return usage("%v", err)
		}
		asked, err := h.Merges.Ask(ctx, req)
		if bad := askRefusal(err); bad != nil {
			return *bad
		}
		if id, _, err = h.Merges.Standing(ctx, ws, who.repo, n, pr.HeadSHA, pr.BaseRef); err != nil {
			return localchan.Fail(localchan.CodeInternal, "the approvals of the pull request could not be looked up", "")
		}
		if id == 0 {
			hint := "run the command again after the approval"
			if expired {
				hint = "the exception of an Approver was for another commit of the PR and expired unused; " + hint
			}
			return localchan.Fail("approval_pending", fmt.Sprintf("the merge of #%d waits for the approval of the request #%d", n, asked), hint).WithExit(localchan.ExitPending)
		}
	}
	if h.isLocked(ctx) {
		return lockedResponse()
	}
	if bad := h.leaseHeld(); bad != nil {
		return *bad
	}
	res, err := mc.MergePull(ctx, who.repo, n, pr.HeadSHA, method)
	switch {
	case err == nil:
	case errors.Is(err, github.ErrHeadChanged):
		return localchan.Fail("head_changed", "the pull request moved while it was merged: the approval is for the commit it had", "read it again; the approval is asked again for the new commit").WithExit(localchan.ExitConflict)
	case errors.Is(err, github.ErrBaseChanged):
		return localchan.Fail("not_mergeable", err.Error(), "the base moved while the PR was merged: run the command again").WithExit(localchan.ExitConflict)
	case errors.Is(err, github.ErrBranchOutdated):
		return localchan.Fail("not_mergeable", err.Error(), "the repository asks the branch to be up to date: the step sync of the table brings it up").WithExit(localchan.ExitConflict)
	case errors.Is(err, github.ErrMergeProtected):
		return localchan.Fail("protected", err.Error(), "a rule of the repository does not let it be merged; nothing is worked around").WithExit(localchan.ExitDenied)
	case errors.Is(err, github.ErrMethodNotAllowed):
		return localchan.Fail("not_mergeable", err.Error(), "use another --method: the repository does not allow this one").WithExit(localchan.ExitConflict)
	case errors.Is(err, github.ErrForbidden):
		return localchan.Fail("forbidden", err.Error(), "the App is not allowed to merge here: tell the Owner").WithExit(localchan.ExitDenied)
	case errors.Is(err, github.ErrNotMergeable):
		return localchan.Fail("not_mergeable", err.Error(), "").WithExit(localchan.ExitConflict)
	default:
		return refusal(err)
	}
	return localchan.OK(map[string]any{"merged": res.Merged, "sha": res.SHA, "recovered": res.Recovered, "method": method, "request": id, "decree": byException, "head_sha": pr.HeadSHA,
		"text": fmt.Sprintf("PR #%d mesclado (%s)", n, method)})
}

// settingsApply is `mestre gh settings apply --file <file.json>`: a ruleset for the
// repository of the workspace. It changes nothing: it makes the request of the type
// configuracao_repositorio to the Owner, who approves it with the PIN, and the engine
// applies exactly that body when the answer comes. It always ends pending.
func (h *Handlers) settingsApply(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.grandMaster(ctx, c)
	if bad != nil {
		return *bad
	}
	if h.Rulesets == nil {
		return unavailable("the settings of the repository are not available in this run: nothing was changed")
	}
	raw, _ := flagString(c, "body")
	if strings.TrimSpace(raw) == "" {
		return usage("usage: mestre gh settings apply --file <file.json> (the body of a ruleset of the API of GitHub)")
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil || body == nil {
		return usage("the file is not a JSON object")
	}
	id, err := h.Rulesets.ProposeRuleset(ctx, who.char.Workspace, who.char.ID, body)
	if errors.Is(err, approvals.ErrBadRuleset) {
		return usage("%v", err)
	}
	if bad := askRefusal(err); bad != nil {
		return *bad
	}
	return localchan.Fail("approval_pending", fmt.Sprintf("the ruleset waits for the approval of the Owner (request #%d); it is applied when the Owner approves", id),
		"nothing was changed on GitHub").WithExit(localchan.ExitPending)
}

// askRefusal turns what the chain answers to the opening of a request into the answer of
// the command (nil when it opened): the gate of freshness with its reason, another
// repository than the workspace's, a refusal with its own code, and a PR that comes from no
// table.
func askRefusal(err error) *localchan.Response {
	if err == nil {
		return nil
	}
	var fresh *chain.FreshnessError
	var ref *chain.Refusal
	var r localchan.Response
	switch {
	case errors.As(err, &fresh):
		r = localchan.Fail("not_mergeable", fresh.Error(), "the gate of freshness refused it: fix what it says and ask again").WithExit(localchan.ExitConflict)
	case errors.Is(err, chain.ErrWrongRepo):
		r = localchan.Fail("wrong_repo", err.Error(), "the repository of the request is not the one of the workspace").WithExit(localchan.ExitDenied)
	case errors.As(err, &ref):
		r = localchan.FailMsg(ref.Code, ref.Msg, "the policy sends this to the people; nothing was done").WithExit(localchan.ExitDenied)
	case strings.Contains(err.Error(), "the request has no table"):
		r = localchan.Fail("no_table", "the pull request does not come from a table of the workspace, so the gate of freshness has nothing to check", "its merge is a decision of the people, outside this command").WithExit(localchan.ExitDenied)
	default:
		r = refusal(err)
	}
	return &r
}
