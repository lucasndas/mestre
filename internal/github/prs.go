package github

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
)

// PullAPI is the GitHub the commands of PRs use, over the App of who asks.
type PullAPI interface {
	IssueAPI
	PullData(ctx context.Context, repo string, n int) (PullData, error)
	UpdatePull(ctx context.Context, repo string, n int, title, body *string) error
	CheckRuns(ctx context.Context, repo, sha string) ([]Check, error)
	Reviews(ctx context.Context, repo string, n int) ([]Review, error)
	ReviewComments(ctx context.Context, repo string, n int) ([]ReviewComment, error)
	PullFiles(ctx context.Context, repo string, n int) ([]PullFile, error)
	CreateReviewComment(ctx context.Context, repo string, n int, sha, path string, line int, body string) (int64, error)
	ReplyReviewComment(ctx context.Context, repo string, n int, id int64, body string) (int64, error)
	CreatePull(ctx context.Context, repo, title, body, head, base string, draft bool) (PullData, error)
	MarkReady(ctx context.Context, repo, nodeID string) error
}

var _ PullAPI = (*REST)(nil)

// PRService is what `mestre pr` does on GitHub for the PR of a table. It is
// made per call, over the API of the App of who asks.
type PRService struct {
	API     PullAPI
	Objects *Objects
	Tables  Tables
	// Send is what `pr open` and `pr update` need to send the branch.
	Send Sending
}

func (s *PRService) guard() *Guard { return &Guard{Objects: s.Objects, GH: s.API, Tables: s.Tables} }

// PRStatus is `pr status`.
type PRStatus struct {
	Number     int
	State      string
	Draft      bool
	HeadSHA    string
	Checks     []Check
	Reviews    []Review
	Mergeable  bool
	BlockedBy  string
	Unresolved int
	URL        string
}

// authorize checks that the PR is the actor's.
func (s *PRService) authorize(ctx context.Context, a Actor, repo string, n int) (string, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return "", refuse(CodeNotAllowed, "repositório inválido")
	}
	if err := s.guard().Authorize(ctx, a, Target{Repo: repo, Kind: KindPR, Number: n}); err != nil {
		return "", err
	}
	return repo, nil
}

// Status reads the PR of the table: the state, the checks of the head, the
// reviews, whether it can be merged and why not, and the comments of review
// nobody answered.
func (s *PRService) Status(ctx context.Context, a Actor, repo string, n int) (PRStatus, error) {
	repo, err := s.authorize(ctx, a, repo, n)
	if err != nil {
		return PRStatus{}, err
	}
	p, err := s.API.PullData(ctx, repo, n)
	if err != nil {
		return PRStatus{}, err
	}
	checks, err := s.API.CheckRuns(ctx, repo, p.HeadSHA)
	if err != nil {
		return PRStatus{}, err
	}
	reviews, err := s.API.Reviews(ctx, repo, n)
	if err != nil {
		return PRStatus{}, err
	}
	comments, err := s.API.ReviewComments(ctx, repo, n)
	if err != nil {
		return PRStatus{}, err
	}
	answered := map[int64]bool{}
	for _, c := range comments {
		if c.InReplyTo != 0 {
			answered[c.InReplyTo] = true
		}
	}
	open := 0
	for _, c := range comments {
		if c.InReplyTo == 0 && !answered[c.ID] {
			open++
		}
	}
	st := PRStatus{Number: n, State: p.State, Draft: p.Draft, HeadSHA: p.HeadSHA, Checks: checks, Reviews: reviews, Unresolved: open, URL: p.URL}
	if p.Merged {
		st.State = "merged"
	}
	st.Mergeable, st.BlockedBy = mergeability(p, checks, reviews)
	return st, nil
}

// mergeability says whether the PR can be merged and, when not, the first reason.
func mergeability(p PullData, checks []Check, reviews []Review) (bool, string) {
	switch {
	case p.Merged:
		return false, "already merged"
	case p.State != "open":
		return false, "the PR is " + p.State
	case p.Draft:
		return false, "the PR is a draft"
	}
	for _, c := range checks {
		switch c.State {
		case "success", "neutral", "skipped":
		case "queued", "in_progress", "pending", "waiting", "requested":
			return false, "check " + c.Name + " has not finished"
		default:
			return false, "check " + c.Name + " " + c.State
		}
	}
	last := map[string]string{}
	for _, r := range reviews {
		if r.State == "APPROVED" || r.State == "CHANGES_REQUESTED" || r.State == "DISMISSED" {
			last[r.By] = r.State
		}
	}
	names := make([]string, 0, len(last))
	for by := range last {
		names = append(names, by)
	}
	slices.Sort(names)
	for _, by := range names {
		if last[by] == "CHANGES_REQUESTED" {
			return false, "changes requested by " + by
		}
	}
	switch p.MergeableState {
	case "clean", "has_hooks":
		return true, ""
	case "dirty":
		return false, "conflict with the base"
	case "behind":
		return false, "the branch is behind the base"
	case "blocked":
		return false, "blocked by the rules of the repository"
	case "unstable":
		return false, "a check that is not required failed"
	}
	return false, "GitHub has not said yet whether it can be merged"
}

// Comment comments on the conversation of the PR, once for the operation.
func (s *PRService) Comment(ctx context.Context, a Actor, repo string, n int, body, op string) (CommentInfo, error) {
	if op == "" {
		op = NewOp()
	}
	// as a PR first: it registers the PR of the table, which the comment (an
	// issue comment) is then authorized through
	repo, err := s.authorize(ctx, a, repo, n)
	if err != nil {
		return CommentInfo{}, err
	}
	return s.guard().PostOnce(ctx, a, repo, n, body, op)
}

// ErrHeadChanged: the head of the PR moved while the command was checked.
var ErrHeadChanged = errors.New("github: the head of the PR changed")

// ErrOutsideDiff: the line is not in the diff of the PR.
var ErrOutsideDiff = errors.New("github: that line is not in the diff of the PR")

// Note puts the note of the author on a line of the diff, at the head of the PR.
func (s *PRService) Note(ctx context.Context, a Actor, repo string, n int, path string, line int, body string) (int64, error) {
	repo, err := s.authorize(ctx, a, repo, n)
	if err != nil {
		return 0, err
	}
	p, err := s.API.PullData(ctx, repo, n)
	if err != nil {
		return 0, err
	}
	files, err := s.API.PullFiles(ctx, repo, n)
	if err != nil {
		return 0, err
	}
	ok := false
	for _, f := range files {
		if f.Path == path && inPatch(f.Patch, line) {
			ok = true
		}
	}
	if !ok {
		return 0, ErrOutsideDiff
	}
	// the lines were checked against the head read first: if the head moved, the
	// diff is another
	now, err := s.API.PullData(ctx, repo, n)
	if err != nil {
		return 0, err
	}
	if now.HeadSHA != p.HeadSHA {
		return 0, ErrHeadChanged
	}
	return s.API.CreateReviewComment(ctx, repo, n, p.HeadSHA, path, line, StripMarkers(body))
}

// inPatch says whether a line of the new side is shown by the patch (an added
// line or a line of context).
func inPatch(patch string, line int) bool {
	cur := 0
	for _, l := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(l, "@@"):
			// @@ -a,b +c,d @@
			f := strings.Fields(l)
			if len(f) < 3 {
				return false
			}
			start := strings.TrimPrefix(f[2], "+")
			if i := strings.IndexByte(start, ','); i >= 0 {
				start = start[:i]
			}
			n, err := strconv.Atoi(start)
			if err != nil {
				return false
			}
			cur = n
		case cur == 0, l == "": // before the first hunk, or the end of the patch
		case strings.HasPrefix(l, "-"):
		case strings.HasPrefix(l, "\\"):
		default: // "+" or " "
			if cur == line {
				return true
			}
			cur++
		}
	}
	return false
}

// Reply answers a comment of review of the PR.
func (s *PRService) Reply(ctx context.Context, a Actor, repo string, n int, id int64, body string) (int64, error) {
	repo, err := s.authorize(ctx, a, repo, n)
	if err != nil {
		return 0, err
	}
	// the comment must be one of this PR, and the start of a thread
	cs, err := s.API.ReviewComments(ctx, repo, n)
	if err != nil {
		return 0, err
	}
	found := false
	for _, c := range cs {
		found = found || c.ID == id && c.InReplyTo == 0
	}
	if !found {
		return 0, ErrNotFound
	}
	return s.API.ReplyReviewComment(ctx, repo, n, id, StripMarkers(body))
}
