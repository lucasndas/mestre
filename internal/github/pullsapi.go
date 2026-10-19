package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// PullData is a PR as the commands read it.
type PullData struct {
	Number         int
	Title, Body    string
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
	return PullData{Number: v.Number, Title: v.Title, Body: v.Body, State: v.State, Draft: v.Draft, Merged: v.Merged, URL: v.HTMLURL,
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

// Review is a review of a PR.
type Review struct{ By, State string }

// Reviews lists the reviews of a PR.
func (c *REST) Reviews(ctx context.Context, repo string, n int) ([]Review, error) {
	type item struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		State string `json:"state"`
	}
	raw, err := pages[item](ctx, c, repo, fmt.Sprintf("/pulls/%d/reviews", n), "reading the reviews")
	if err != nil {
		return nil, err
	}
	out := make([]Review, 0, len(raw))
	for _, r := range raw {
		out = append(out, Review{By: r.User.Login, State: r.State})
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
type PullFile struct{ Path, Patch string }

// PullFiles lists the files of the diff of a PR.
func (c *REST) PullFiles(ctx context.Context, repo string, n int) ([]PullFile, error) {
	type item struct {
		Filename string `json:"filename"`
		Patch    string `json:"patch"`
	}
	raw, err := pages[item](ctx, c, repo, fmt.Sprintf("/pulls/%d/files", n), "reading the files of the PR")
	if err != nil {
		return nil, err
	}
	out := make([]PullFile, 0, len(raw))
	for _, r := range raw {
		out = append(out, PullFile{Path: r.Filename, Patch: r.Patch})
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
