package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// PullData is a PR as the commands read it.
type PullData struct {
	Number         int
	NodeID         string
	Title, Body    string
	Author         string
	State          string
	Draft, Merged  bool
	URL            string
	HeadSHA        string
	HeadRef        string
	HeadRepo       string
	BaseRef        string
	MergeableState string
}

type pullJSON struct {
	NodeID string `json:"node_id"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	Number         int    `json:"number"`
	Title          string `json:"title"`
	Body           string `json:"body"`
	State          string `json:"state"`
	Draft          bool   `json:"draft"`
	Merged         bool   `json:"merged"`
	HTMLURL        string `json:"html_url"`
	MergeableState string `json:"mergeable_state"`
	Head           struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (v pullJSON) data() PullData {
	return PullData{Number: v.Number, NodeID: v.NodeID, Title: v.Title, Body: v.Body, Author: v.User.Login, State: v.State, Draft: v.Draft, Merged: v.Merged, URL: v.HTMLURL,
		HeadSHA: v.Head.SHA, HeadRef: v.Head.Ref, HeadRepo: v.Head.Repo.FullName, BaseRef: v.Base.Ref, MergeableState: v.MergeableState}
}

// PullData reads a PR with its head, state and body.
func (c *REST) PullData(ctx context.Context, repo string, n int) (PullData, error) {
	var v pullJSON
	if err := c.send(ctx, http.MethodGet, repo, fmt.Sprintf("/pulls/%d", n), nil, http.StatusOK, &v, "reading a PR"); err != nil {
		return PullData{}, err
	}
	return v.data(), nil
}

// UpdatePull changes the title and the body of a PR.
func (c *REST) UpdatePull(ctx context.Context, repo string, n int, title, body *string) error {
	in := map[string]any{}
	if title != nil {
		in["title"] = *title
	}
	if body != nil {
		in["body"] = *body
	}
	return c.send(ctx, http.MethodPatch, repo, fmt.Sprintf("/pulls/%d", n), in, http.StatusOK, nil, "updating a PR")
}

// Check is a check run of the head of a PR.
type Check struct{ Name, State string }

// CheckRuns lists the checks of a commit; State is the conclusion, or the status
// of one still running.
func (c *REST) CheckRuns(ctx context.Context, repo, sha string) ([]Check, error) {
	var out []Check
	for page := 1; page <= maxListPages; page++ {
		var v struct {
			Runs []struct {
				Name       string  `json:"name"`
				Status     string  `json:"status"`
				Conclusion *string `json:"conclusion"`
			} `json:"check_runs"`
		}
		if err := c.send(ctx, http.MethodGet, repo, fmt.Sprintf("/commits/%s/check-runs?per_page=100&page=%d", sha, page), nil, http.StatusOK, &v, "reading the checks"); err != nil {
			return nil, err
		}
		for _, r := range v.Runs {
			st := r.Status
			if r.Conclusion != nil {
				st = *r.Conclusion
			}
			out = append(out, Check{Name: r.Name, State: st})
		}
		if len(v.Runs) < 100 {
			return out, nil
		}
	}
	return nil, errTooMany
}

// CommitStatuses lists the legacy commit statuses of a commit (the ones CI
// systems that are not GitHub Actions report), the latest of each context, as
// Checks whose State is success, failure, error or pending.
func (c *REST) CommitStatuses(ctx context.Context, repo, sha string) ([]Check, error) {
	var out []Check
	for page := 1; page <= maxListPages; page++ {
		var v struct {
			Statuses []struct {
				Context string `json:"context"`
				State   string `json:"state"`
			} `json:"statuses"`
		}
		if err := c.send(ctx, http.MethodGet, repo, fmt.Sprintf("/commits/%s/status?per_page=100&page=%d", sha, page), nil, http.StatusOK, &v, "reading the commit statuses"); err != nil {
			return nil, err
		}
		for _, r := range v.Statuses {
			out = append(out, Check{Name: r.Context, State: r.State})
		}
		if len(v.Statuses) < 100 {
			return out, nil
		}
	}
	return nil, errTooMany
}

// Review is a review of a PR.
type Review struct {
	ID        int64
	By, State string
	// Commit is the commit the review was made on.
	Commit string
	// ByType is the type of the account that reviewed (User, Bot, Organization)
	// and Association what the repository says of it (OWNER, COLLABORATOR, NONE…).
	ByType, Association string
	// Body is the text of the review: the engine marks the ones it writes.
	Body string
}

// Reviews lists the reviews of a PR.
func (c *REST) Reviews(ctx context.Context, repo string, n int) ([]Review, error) {
	type item struct {
		ID   int64 `json:"id"`
		User struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"user"`
		State       string `json:"state"`
		CommitID    string `json:"commit_id"`
		Association string `json:"author_association"`
		Body        string `json:"body"`
	}
	raw, err := pages[item](ctx, c, repo, fmt.Sprintf("/pulls/%d/reviews", n), "reading the reviews")
	if err != nil {
		return nil, err
	}
	out := make([]Review, 0, len(raw))
	for _, r := range raw {
		out = append(out, Review{ID: r.ID, By: r.User.Login, State: r.State, Commit: r.CommitID, ByType: r.User.Type, Association: r.Association, Body: r.Body})
	}
	return out, nil
}

// ReviewComment is a comment of review on a line of the diff.
type ReviewComment struct {
	ID        int64
	InReplyTo int64
	Path      string
	Line      int
	By        string
	Body      string
}

// ReviewComments lists the comments of review of a PR.
func (c *REST) ReviewComments(ctx context.Context, repo string, n int) ([]ReviewComment, error) {
	type item struct {
		ID        int64  `json:"id"`
		InReplyTo int64  `json:"in_reply_to_id"`
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Body      string `json:"body"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	raw, err := pages[item](ctx, c, repo, fmt.Sprintf("/pulls/%d/comments", n), "reading the comments of review")
	if err != nil {
		return nil, err
	}
	out := make([]ReviewComment, 0, len(raw))
	for _, r := range raw {
		out = append(out, ReviewComment{ID: r.ID, InReplyTo: r.InReplyTo, Path: r.Path, Line: r.Line, By: r.User.Login, Body: r.Body})
	}
	return out, nil
}

// PullFile is a file of the diff of a PR with its patch.
type PullFile struct {
	Path, Patch string
	// Status is added, modified, removed or renamed; PreviousPath is where a
	// renamed file was.
	Status, PreviousPath string
	Additions, Deletions int
}

// PullFiles lists the files of the diff of a PR.
func (c *REST) PullFiles(ctx context.Context, repo string, n int) ([]PullFile, error) {
	type item struct {
		Filename         string `json:"filename"`
		Patch            string `json:"patch"`
		Status           string `json:"status"`
		PreviousFilename string `json:"previous_filename"`
		Additions        int    `json:"additions"`
		Deletions        int    `json:"deletions"`
	}
	raw, err := pages[item](ctx, c, repo, fmt.Sprintf("/pulls/%d/files", n), "reading the files of the PR")
	if err != nil {
		return nil, err
	}
	out := make([]PullFile, 0, len(raw))
	for _, r := range raw {
		out = append(out, PullFile{Path: r.Filename, Patch: r.Patch, Status: r.Status, PreviousPath: r.PreviousFilename, Additions: r.Additions, Deletions: r.Deletions})
	}
	return out, nil
}

// CreateReviewComment puts a comment of review on a line of the new side of the
// diff, at the commit.
func (c *REST) CreateReviewComment(ctx context.Context, repo string, n int, sha, path string, line int, body string) (int64, error) {
	var v struct {
		ID int64 `json:"id"`
	}
	in := map[string]any{"body": body, "commit_id": sha, "path": path, "line": line, "side": "RIGHT"}
	if err := c.send(ctx, http.MethodPost, repo, fmt.Sprintf("/pulls/%d/comments", n), in, http.StatusCreated, &v, "commenting a line"); err != nil {
		return 0, err
	}
	return v.ID, nil
}

// ReplyReviewComment answers a comment of review.
func (c *REST) ReplyReviewComment(ctx context.Context, repo string, n int, id int64, body string) (int64, error) {
	var v struct {
		ID int64 `json:"id"`
	}
	if err := c.send(ctx, http.MethodPost, repo, fmt.Sprintf("/pulls/%d/comments/%d/replies", n, id), map[string]string{"body": body}, http.StatusCreated, &v, "answering a comment"); err != nil {
		return 0, err
	}
	return v.ID, nil
}

// maxListPages bounds a listing: 20 pages of 100.
const maxListPages = 20

var errTooMany = errors.New("github: too many items to look through")

// pages reads a listing page by page (100 each), whole: a reading that stops at
// the first page loses the newest reviews and the last checks.
func pages[T any](ctx context.Context, c *REST, repo, path, what string) ([]T, error) {
	var out []T
	for page := 1; page <= maxListPages; page++ {
		var raw []T
		if err := c.send(ctx, http.MethodGet, repo, fmt.Sprintf("%s?per_page=100&page=%d", path, page), nil, http.StatusOK, &raw, what); err != nil {
			return nil, err
		}
		out = append(out, raw...)
		if len(raw) < 100 {
			return out, nil
		}
	}
	return nil, errTooMany
}

// CreatePull opens a PR of head (a branch of the repository) into base.
func (c *REST) CreatePull(ctx context.Context, repo, title, body, head, base string, draft bool) (PullData, error) {
	in := map[string]any{"title": title, "body": body, "head": head, "base": base, "draft": draft}
	var v pullJSON
	if err := c.send(ctx, http.MethodPost, repo, "/pulls", in, http.StatusCreated, &v, "opening a PR"); err != nil {
		return PullData{}, err
	}
	return v.data(), nil
}

// OpenPullOf finds the open PR whose head is the branch of the repository itself
// (not of a fork) and goes into base; ok is false when there is none.
func (c *REST) OpenPullOf(ctx context.Context, repo, branch, base string) (PullData, bool, error) {
	norm, err := NormalizeRepo(repo)
	if err != nil {
		return PullData{}, false, err
	}
	raw, err := pages[pullJSON](ctx, c, repo, "/pulls", "listing the open PRs")
	if err != nil {
		return PullData{}, false, err
	}
	for _, v := range raw {
		d := v.data()
		if d.HeadRef == branch && strings.EqualFold(d.HeadRepo, norm) && d.BaseRef == base && d.State == "open" {
			return d, true, nil
		}
	}
	return PullData{}, false, nil
}

// MarkReady takes a PR out of the draft state (only GraphQL does it).
func (c *REST) MarkReady(ctx context.Context, repo, nodeID string) error {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return err
	}
	q := map[string]any{"query": "mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { number isDraft } } }", "variables": map[string]string{"id": nodeID}}
	st, hdr, raw, err := c.doAbs(ctx, http.MethodPost, repo, "/graphql", q)
	if err != nil {
		return err
	}
	if st != http.StatusOK {
		return failed("marking a PR ready", st, raw, hdr)
	}
	var v struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(raw, &v) != nil || len(v.Errors) > 0 {
		return errors.New("github: marking a PR ready: refused")
	}
	return nil
}

// CommitStatus is the combined state of the legacy statuses of a commit
// (success, pending, failure, error); "" when it has none.
func (c *REST) CommitStatus(ctx context.Context, repo, sha string) (string, error) {
	var v struct {
		State    string `json:"state"`
		Statuses []struct {
			State string `json:"state"`
		} `json:"statuses"`
	}
	if err := c.send(ctx, http.MethodGet, repo, fmt.Sprintf("/commits/%s/status", sha), nil, http.StatusOK, &v, "reading the statuses"); err != nil {
		return "", err
	}
	if len(v.Statuses) == 0 {
		return "", nil
	}
	return v.State, nil
}

// The methods of a merge.
const (
	MergeMethodMerge  = "merge"
	MergeMethodSquash = "squash"
	MergeMethodRebase = "rebase"
)

// The refusals of a merge, told apart by the reason GitHub gives, because what the
// caller does next depends on it. ErrHeadChanged (prs.go) is the PR having moved since
// the commit that was approved; ErrBaseChanged the base having moved while the merge
// was made (repeating with the same commit is safe); ErrBranchOutdated the rule
// "branch up to date"; ErrMergeProtected any other rule of the branch (checks,
// reviews, code owners, rulesets); ErrMethodNotAllowed a method the repository does
// not allow; ErrForbidden the App not being allowed to merge (permission, not a
// rule); ErrNotMergeable the rest (a conflict, a draft, closed or merged already).
var (
	ErrBaseChanged      = errors.New("github: the base branch moved while the PR was merged")
	ErrBranchOutdated   = errors.New("github: the branch is not up to date with the base")
	ErrMergeProtected   = errors.New("github: a rule of the branch does not let the PR be merged")
	ErrMethodNotAllowed = errors.New("github: the repository does not allow this method of merge")
	ErrForbidden        = errors.New("github: the App is not allowed to merge")
	ErrNotMergeable     = errors.New("github: the PR is not mergeable")
)

// MergeResult is what GitHub answers to a merge. Recovered says the merge had
// already happened (the answer to an earlier call was lost) and SHA is then empty.
type MergeResult struct {
	// SHA is the merge commit.
	SHA       string
	Merged    bool
	Recovered bool
}

var shaRe = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// ValidSHA says s is the full hash of a commit, in lower case: the only thing a
// merge is bound to (a branch, a prefix or a mixed-case text is refused).
func ValidSHA(s string) bool { return shaRe.MatchString(s) }

// MergePull merges a PR with the commit that was approved: GitHub refuses with
// ErrHeadChanged if the PR has moved since. The rules of the repository apply to
// the App that merges (the errors above carry GitHub's reason, cleaned). method is
// MergeMethodMerge, MergeMethodSquash or MergeMethodRebase ("" is merge).
//
// The commit is the head of the PR; GitHub has no way to bind the base, so the caller
// who needs the base not to move (the gate of freshness) reads its tip just before.
// The caller writes the operation in github_objects before sending
// (docs/github/acoes-por-nivel.md): if the answer is lost, the call can be made again,
// and a merge that had happened is recognized (Recovered) when the PR is merged at the
// approved commit.
func (c *REST) MergePull(ctx context.Context, repo string, n int, sha, method string) (MergeResult, error) {
	switch method {
	case "":
		method = MergeMethodMerge
	case MergeMethodMerge, MergeMethodSquash, MergeMethodRebase:
	default:
		return MergeResult{}, fmt.Errorf("github: %q is not a method of merge", method)
	}
	if n <= 0 {
		return MergeResult{}, errors.New("github: a merge needs the number of the PR")
	}
	if !ValidSHA(sha) {
		return MergeResult{}, errors.New("github: a merge needs the full hash of the commit that was approved")
	}
	st, hdr, raw, err := c.doHeader(ctx, http.MethodPut, repo, fmt.Sprintf("/pulls/%d/merge", n), map[string]string{"sha": sha, "merge_method": method})
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			return c.recoverMerge(ctx, repo, n, sha, err)
		}
		return MergeResult{}, err
	}
	var v struct {
		SHA     string `json:"sha"`
		Merged  bool   `json:"merged"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &v)
	why := cleanReason(v.Message)
	switch {
	case st == http.StatusOK:
		if !v.Merged || !shaRe.MatchString(v.SHA) {
			return MergeResult{}, fmt.Errorf("github: merging a PR: GitHub answered 200 without a merge (%s)", why)
		}
		return MergeResult{SHA: v.SHA, Merged: true}, nil
	case st >= 500:
		return c.recoverMerge(ctx, repo, n, sha, failed("merging a PR", st, raw, hdr))
	case st == http.StatusConflict:
		if strings.Contains(strings.ToLower(why), "base branch was modified") {
			return MergeResult{}, fmt.Errorf("%w: %s", ErrBaseChanged, why)
		}
		return MergeResult{}, fmt.Errorf("%w: %s", ErrHeadChanged, why)
	case st == http.StatusForbidden:
		low := strings.ToLower(why)
		switch {
		case hdr.Get("Retry-After") != "" || hdr.Get("X-RateLimit-Remaining") == "0" || strings.Contains(low, "rate limit"):
			wait, _ := strconv.Atoi(hdr.Get("Retry-After"))
			if wait < 1 {
				wait = 60
			}
			return MergeResult{}, &LimitError{RetryAfter: time.Duration(wait) * time.Second}
		case mergeRuleMessage(low):
			return MergeResult{}, fmt.Errorf("%w: %s", ErrMergeProtected, why)
		}
		return MergeResult{}, fmt.Errorf("%w: %s", ErrForbidden, why)
	case st == http.StatusMethodNotAllowed || st == http.StatusUnprocessableEntity:
		low := strings.ToLower(why)
		switch {
		case strings.Contains(low, "base branch was modified"):
			return MergeResult{}, fmt.Errorf("%w: %s", ErrBaseChanged, why)
		case strings.Contains(low, "not allowed on this repository") || strings.Contains(low, "invalid merge method"):
			return MergeResult{}, fmt.Errorf("%w: %s", ErrMethodNotAllowed, why)
		case strings.Contains(low, "not up to date"):
			return MergeResult{}, fmt.Errorf("%w: %s", ErrBranchOutdated, why)
		case mergeRuleMessage(low):
			return MergeResult{}, fmt.Errorf("%w: %s", ErrMergeProtected, why)
		}
		if st == http.StatusMethodNotAllowed { // maybe the merge happened and the answer was lost
			return c.recoverMerge(ctx, repo, n, sha, fmt.Errorf("%w: %s", ErrNotMergeable, why))
		}
		return MergeResult{}, fmt.Errorf("%w: %s", ErrNotMergeable, why)
	}
	return MergeResult{}, failed("merging a PR", st, raw, hdr)
}

// recoverMerge reads the PR after an answer that does not say the merge failed for
// sure: if it is merged at the approved commit the merge happened (the answer was
// lost, or the call is a repetition), and that is success; merged at another commit it
// is not ours; otherwise the original error stands.
func (c *REST) recoverMerge(ctx context.Context, repo string, n int, sha string, orig error) (MergeResult, error) {
	pr, err := c.PullData(ctx, repo, n)
	switch {
	case err != nil || !pr.Merged:
		return MergeResult{}, orig
	case pr.HeadSHA != sha:
		return MergeResult{}, fmt.Errorf("%w: the PR is merged at another commit (%s)", ErrNotMergeable, pr.HeadSHA)
	}
	return MergeResult{Merged: true, Recovered: true}, nil
}

// cleanReason is a reason of GitHub made safe to show: printable characters only (no
// control or direction marks), whitespace folded and cut by characters.
func cleanReason(m string) string {
	var b strings.Builder
	for _, r := range m {
		switch {
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		case unicode.IsPrint(r) && !unicode.Is(unicode.Cf, r):
			b.WriteRune(r)
		}
	}
	out := []rune(strings.Join(strings.Fields(b.String()), " "))
	if len(out) > 300 {
		out = out[:300]
	}
	return string(out)
}

// mergeRuleMessage says GitHub's reason (in lower case) is a rule of the branch: a
// protected branch, a ruleset, a check, a review or the code owners.
func mergeRuleMessage(m string) bool {
	for _, k := range []string{"protected branch", "rule violation", "repository rule", "required status", "required review", "review is required", "required check", "ruleset", "branch protection", "code owner", "approving review", "waiting on"} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}
