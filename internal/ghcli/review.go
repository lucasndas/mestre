package ghcli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/localchan"
)

// Heads keeps the commit of a PR that `gh review` read (github.ReviewedHeads):
// `gh approve` is for that commit.
type Heads interface {
	Record(ctx context.Context, repo string, pr int, sha string) error
	Get(ctx context.Context, repo string, pr int) (string, error)
}

// reviewer is the part of the API of the App that sends a review
// (github.REST).
type reviewer interface {
	CreateReview(ctx context.Context, repo string, n int, sha, event, body string) (int64, error)
}

// reviewCmd serves the reading under the lease of the reads and the review under
// the one of the writes.
func (h *Handlers) reviewCmd(ctx context.Context, c *localchan.Call) localchan.Response {
	if flagBool(c, "comment") || flagBool(c, "request-changes") {
		return h.write("gh review", h.review)(ctx, c)
	}
	return h.read(h.review)(ctx, c)
}

// review is `mestre gh review <pr> [--diff]` and `mestre gh review <pr> (--comment
// | --request-changes) --body …` (docs/cli/grao-mestre.md#mestre-gh): the
// Grão-Mestre reads a pull request of the repository, or reviews it with its own
// App. What it reads is recorded: `gh approve` is for that commit.
func (h *Handlers) review(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.grandMaster(ctx, c)
	if bad != nil {
		return *bad
	}
	if len(c.Request.Args) != 1 {
		return usage("usage: mestre gh review <pr> [--diff] | (--comment | --request-changes) (--body \"<text>\" | --body-file <file>)")
	}
	n, ok := number(c.Request.Args[0])
	if !ok {
		return usage("the pull request is a number")
	}
	comment, requestChanges := flagBool(c, "comment"), flagBool(c, "request-changes")
	body, _ := flagString(c, "body")
	switch {
	case comment && requestChanges:
		return usage("--comment and --request-changes are two reviews: give one")
	case (comment || requestChanges) && strings.TrimSpace(body) == "":
		return usage("a review needs --body or --body-file")
	case !comment && !requestChanges && body != "":
		return usage("--body goes with --comment or --request-changes")
	case (comment || requestChanges) && flagBool(c, "diff"):
		return usage("--diff goes with the reading, not with a review")
	}
	api := who.prs.API
	pr, err := api.PullData(ctx, who.repo, n)
	if err != nil {
		return refusal(err)
	}
	if pr.URL == "" && pr.Number == 0 {
		return localchan.Fail("not_found", fmt.Sprintf("#%d is not a pull request", n), "").WithExit(localchan.ExitNotFound)
	}
	if comment || requestChanges {
		op := c.Request.ID
		if op == "" {
			op = github.NewOp()
		}
		return h.sendReview(ctx, who, pr, requestChanges, body, op)
	}
	files, err := api.PullFiles(ctx, who.repo, pr.Number)
	if err != nil {
		return refusal(err)
	}
	checks, err := api.CheckRuns(ctx, who.repo, pr.HeadSHA)
	if err != nil {
		return refusal(err)
	}
	reviews, err := api.Reviews(ctx, who.repo, pr.Number)
	if err != nil {
		return refusal(err)
	}
	comments, err := api.ReviewComments(ctx, who.repo, pr.Number)
	if err != nil {
		return refusal(err)
	}
	// a push while it was read would give the commit of one moment and the files
	// of another: the reading is not kept, and it is done again
	again, err := api.PullData(ctx, who.repo, n)
	if err != nil {
		return refusal(err)
	}
	if again.HeadSHA != pr.HeadSHA {
		return localchan.Fail("head_changed", "the pull request received a commit while it was read", "read it again").WithExit(localchan.ExitConflict)
	}
	if h.Heads != nil {
		if err := h.Heads.Record(ctx, who.repo, pr.Number, pr.HeadSHA); err != nil {
			return localchan.Fail(localchan.CodeInternal, "the commit that was read could not be kept", "")
		}
	}
	return localchan.OK(reviewView(pr, files, checks, reviews, comments, flagBool(c, "diff")))
}

func reviewView(pr github.PullData, files []github.PullFile, checks []github.Check, reviews []github.Review, comments []github.ReviewComment, diff bool) map[string]any {
	fv := make([]map[string]any, 0, len(files))
	var d strings.Builder
	var add, del int
	truncated := false
	for _, f := range files {
		if f.Patch == "" && f.Additions+f.Deletions > 0 {
			truncated = true // GitHub leaves out the patch of a binary or a large file
		}
		fv = append(fv, map[string]any{"path": f.Path, "status": f.Status, "additions": f.Additions, "deletions": f.Deletions})
		add, del = add+f.Additions, del+f.Deletions
		if diff {
			fmt.Fprintf(&d, "--- %s (%s)\n%s\n", f.Path, f.Status, f.Patch)
		}
	}
	cv := make([]map[string]any, 0, len(checks))
	for _, ch := range checks {
		cv = append(cv, map[string]any{"name": ch.Name, "state": ch.State})
	}
	rv := make([]map[string]any, 0, len(reviews))
	for _, r := range reviews {
		rv = append(rv, map[string]any{"by": r.By, "state": r.State, "commit": r.Commit})
	}
	mv := make([]map[string]any, 0, len(comments))
	for _, m := range comments {
		mv = append(mv, map[string]any{"id": m.ID, "path": m.Path, "line": m.Line, "by": m.By, "body": github.HideMarkers(m.Body)})
	}
	out := map[string]any{
		"number": pr.Number, "title": github.HideMarkers(pr.Title), "state": pr.State, "draft": pr.Draft, "merged": pr.Merged, "author": pr.Author, "head_sha": pr.HeadSHA,
		"mergeable_state": pr.MergeableState, "files": fv, "checks": cv, "reviews": rv, "comments": mv, "additions": add, "deletions": del,
		"body": github.HideMarkers(pr.Body),
	}
	out["truncated"] = truncated
	if diff {
		out["diff"] = github.HideMarkers(d.String())
	}
	var text strings.Builder
	fmt.Fprintf(&text, "PR #%d %q (%s, cabeça %.8s): %d arquivos, +%d −%d", pr.Number, oneLine(github.HideMarkers(pr.Title)), pr.State, pr.HeadSHA, len(files), add, del)
	for _, ch := range checks {
		fmt.Fprintf(&text, "\n- CI %s: %s", oneLine(ch.Name), ch.State)
	}
	out["text"] = text.String()
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func (h *Handlers) sendReview(ctx context.Context, who caller, pr github.PullData, requestChanges bool, body, op string) localchan.Response {
	if pr.State != "open" || pr.Merged {
		return localchan.Fail("not_open", fmt.Sprintf("the pull request #%d is not open", pr.Number), "").WithExit(localchan.ExitConflict)
	}
	if h.isLocked(ctx) {
		return lockedResponse()
	}
	// the review is for the commit the Grão-Mestre read, and for no other
	if h.Heads == nil {
		return unavailable("the commits that were read are not kept in this run")
	}
	read, err := h.Heads.Get(ctx, who.repo, pr.Number)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the commit that was read could not be looked up", "")
	}
	if read == "" || read != pr.HeadSHA {
		return localchan.Fail("head_changed", fmt.Sprintf("the pull request is at %.8s now and you read %.8s", pr.HeadSHA, orDash(read)), "read it with `mestre gh review <pr>` before you review it").WithExit(localchan.ExitConflict)
	}
	rv, ok := who.prs.API.(reviewer)
	if !ok {
		return unavailable("the GitHub of this run cannot send a review")
	}
	event := "COMMENT"
	if requestChanges {
		event = "REQUEST_CHANGES"
	}
	id, err := rv.CreateReview(ctx, who.repo, pr.Number, read, event, github.WithMarker(github.StripMarkers(body), op))
	if err != nil {
		if errors.Is(err, github.ErrNotFound) {
			return localchan.Fail("not_found", "GitHub does not have it", "").WithExit(localchan.ExitNotFound)
		}
		return refusal(err)
	}
	return localchan.OK(map[string]any{"review": id, "head_sha": pr.HeadSHA, "text": fmt.Sprintf("revisão enviada ao PR #%d", pr.Number)})
}

// Settings is what `gh settings show` reads besides the rules on GitHub: the
// main branch of the workspace, the mode of approval of its reviews and the Apps
// of the Mestre.
type Settings interface {
	Branch(ctx context.Context, workspace string) (string, error)
	// Mode is app or human, and why it is human.
	Mode(ctx context.Context, workspace string) (mode, why string, err error)
	// Apps are the names of the Apps of the Mestre with their state.
	Apps(ctx context.Context) ([]string, error)
}

// rulesReader is the part of the API that reads the rules of a branch
// (github.REST).
type rulesReader interface {
	ReadRules(ctx context.Context, repo, branch string) (github.ReviewRules, error)
}

// settings is `mestre gh settings show|apply` (docs/cli/grao-mestre.md#mestre-gh).
// `apply` changes the repository only through the approval of the type
// configuracao_repositorio (settingsApply).
func (h *Handlers) settings(ctx context.Context, c *localchan.Call) localchan.Response {
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 || (args[0] != "show" && args[0] != "apply") {
		return usage("usage: mestre gh settings show | apply --file <file.json>")
	}
	if args[0] == "apply" {
		return h.write("gh settings apply", h.settingsApply)(ctx, c)
	}
	return h.read(h.settingsShow)(ctx, c)
}

func (h *Handlers) settingsShow(ctx context.Context, c *localchan.Call) localchan.Response {
	who, bad := h.grandMaster(ctx, c)
	if bad != nil {
		return *bad
	}
	if h.Settings == nil {
		return unavailable("the settings of the repository are not available in this run")
	}
	rr, ok := who.prs.API.(rulesReader)
	if !ok {
		return unavailable("the GitHub of this run cannot read the rules of the repository")
	}
	branch, err := h.Settings.Branch(ctx, who.char.Workspace)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the main branch of the workspace could not be read", "")
	}
	rules, err := rr.ReadRules(ctx, who.repo, branch)
	if err != nil {
		return refusal(err)
	}
	mode, why, err := h.Settings.Mode(ctx, who.char.Workspace)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the mode of approval could not be read", "")
	}
	apps, err := h.Settings.Apps(ctx)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the Apps could not be read", "")
	}
	rulesets := make([]map[string]any, 0, len(rules.Rulesets))
	for _, rs := range rules.Rulesets {
		rulesets = append(rulesets, map[string]any{"id": rs.ID, "shared": rs.Shared})
	}
	if apps == nil {
		apps = []string{}
	}
	return localchan.OK(map[string]any{
		"branch": branch, "approvals": rules.Approvals, "code_owners": rules.CodeOwners, "dismiss_stale": rules.DismissStale, "last_push": rules.LastPush,
		"up_to_date": rules.UpToDate, "classic_protection": rules.Classic, "rulesets": rulesets, "rules": rules.Summary,
		"approval_mode": mode, "approval_mode_why": why, "apps": apps,
		"text": fmt.Sprintf("branch %s: %d aprovações exigidas; modo de aprovação %s", branch, rules.Approvals, mode),
	})
}

func orDash(s string) string {
	if s == "" {
		return "nothing"
	}
	return s
}
