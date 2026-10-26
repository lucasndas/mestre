package github

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// ReconcileWindow is how far back the new Citadel reads what the Apps of the
// Guild created (docs/guilda/cidadela-e-torres.md#github-depois-da-troca).
const ReconcileWindow = 30 * time.Minute

// RecentComment is a comment of an issue or a PR of the repository, as the list
// of the whole repository gives it.
type RecentComment struct {
	ID      int64
	Issue   int
	Body    string
	Login   string
	Updated time.Time
}

// RecentComments lists the comments of the repository updated since the moment,
// newest first, page by page (100 each, up to 50 pages). GitHub's list is sorted
// by update, so the reading stops at the first comment older than the moment.
func (c *REST) RecentComments(ctx context.Context, repo string, since time.Time) ([]RecentComment, error) {
	var out []RecentComment
	for page := 1; page <= 50; page++ {
		var raw []struct {
			ID        int64     `json:"id"`
			Body      string    `json:"body"`
			IssueURL  string    `json:"issue_url"`
			HTMLURL   string    `json:"html_url"`
			UpdatedAt time.Time `json:"updated_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		path := fmt.Sprintf("/issues/comments?sort=updated&direction=desc&per_page=100&page=%d", page)
		if err := c.get(ctx, repo, path, &raw); err != nil {
			return nil, err
		}
		for _, r := range raw {
			if r.UpdatedAt.Before(since) {
				return out, nil
			}
			out = append(out, RecentComment{ID: r.ID, Issue: numberOf(r.IssueURL, r.HTMLURL), Body: r.Body, Login: r.User.Login, Updated: r.UpdatedAt})
		}
		if len(raw) < 100 {
			return out, nil
		}
	}
	return nil, errors.New("github: too many recent comments to look through")
}

// CommentLister is the reading the reconciliation needs of GitHub.
type CommentLister interface {
	RecentComments(ctx context.Context, repo string, since time.Time) ([]RecentComment, error)
}

var _ CommentLister = (*REST)(nil)

// Reconciler brings into the database of a Citadel that just took the epoch what
// the old one made on GitHub and did not replicate: the comments of the Apps of
// the Guild of the last ReconcileWindow that carry the marker of an operation the
// database does not know are recorded in github_objects. So the same operation
// in the queue of the output, sent again, finds its comment (Guard.PostOnce) and
// makes no second one.
type Reconciler struct {
	Objects *Objects
	API     CommentLister
	// Repos are the repositories of the workspaces to read.
	Repos func(ctx context.Context) ([]string, error)
	// Authors are the logins ("<slug>[bot]", lower case) of the Apps of the Guild:
	// only their comments are the Guild's, not those of any other bot.
	Authors func(ctx context.Context) (map[string]bool, error)
	// Anchor is the moment the window ends at (the start of the epoch the Citadel
	// took): a Citadel that restarts long after the promotion still reads what the
	// old one made. nil, or a zero time: now.
	Anchor func(ctx context.Context) (time.Time, error)
	Clock  clock.Clock
	Window time.Duration

	mu    sync.Mutex
	since time.Time
}

// Reconcile reads the recent comments of each repository. It fails closed: any
// error ends it with that error, and the caller does not act outside.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	repos, err := r.Repos(ctx)
	if err != nil {
		return err
	}
	window := r.Window
	if window <= 0 {
		window = ReconcileWindow
	}
	authors, err := r.Authors(ctx)
	if err != nil {
		return err
	}
	// the window is anchored at the first try: a try that comes after failures
	// reads from the same moment, so the comments of the old Citadel do not leave it
	r.mu.Lock()
	if r.since.IsZero() {
		end := r.Clock.Now()
		if r.Anchor != nil {
			t, err := r.Anchor(ctx)
			if err != nil {
				r.mu.Unlock()
				return err
			}
			if !t.IsZero() {
				end = t
			}
		}
		r.since = end.Add(-window)
	}
	since := r.since
	r.mu.Unlock()
	for _, repo := range repos {
		repo, err := NormalizeRepo(repo)
		if err != nil {
			return err
		}
		list, err := r.API.RecentComments(ctx, repo, since)
		if err != nil {
			return fmt.Errorf("github: reconciling %s: %w", repo, err)
		}
		for _, c := range list {
			if err := r.adopt(ctx, repo, c, authors); err != nil {
				return fmt.Errorf("github: reconciling %s: %w", repo, err)
			}
		}
	}
	return nil
}

// adopt records the comment if an App of the Guild wrote it with a marker of
// operation and the database does not know the operation.
func (r *Reconciler) adopt(ctx context.Context, repo string, c RecentComment, authors map[string]bool) error {
	if !authors[strings.ToLower(c.Login)] || !endsWithMarker(c.Body) {
		return nil // a person's text, or one that quotes a marker, is not the Guild's
	}
	ms := Markers(c.Body)
	last := strings.TrimSpace(ms[len(ms)-1])
	op, ok := strings.CutPrefix(last, "<!-- mestre:op=")
	if !ok {
		return nil
	}
	op = strings.TrimSuffix(op, " -->")
	if _, found, err := r.Objects.ByOp(ctx, op); err != nil || found {
		return err
	}
	// the table is not known from here (the Grão-Mestre and the Apps of the classes
	// write alike): PostOnce takes an object with no table as the operation's own
	table := ""
	err := r.Objects.Register(ctx, Object{Repo: repo, Kind: KindComment, Number: int(c.ID), GitHubID: c.ID, Table: table, ParentNumber: c.Issue, Op: op})
	return err
}
