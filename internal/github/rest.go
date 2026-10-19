package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotFound is a 404 of GitHub.
var ErrNotFound = errors.New("github: not found")

// IssueInfo is what the guard reads of an issue.
type IssueInfo struct {
	Number int
	ID     int64
	Labels []string
}

// PullInfo is what the guard reads of a PR: the branch it comes from and the
// repository of that branch.
type PullInfo struct {
	HeadRef  string
	HeadRepo string
}

// CommentInfo is a comment of an issue or a PR.
type CommentInfo struct {
	ID   int64
	Body string
}

// GitHub is the part of the REST API the actions of the engine use: reads of the
// tree of an issue, the comments and the reserved labels.
type GitHub interface {
	Issue(ctx context.Context, repo string, n int) (IssueInfo, error)
	// Parent is the issue above a sub-issue; ok is false for one with none.
	Parent(ctx context.Context, repo string, n int) (parent int, ok bool, err error)
	// Pull reads a PR; ErrNotFound for a number that is not one.
	Pull(ctx context.Context, repo string, n int) (PullInfo, error)
	Comments(ctx context.Context, repo string, n int) ([]CommentInfo, error)
	PostComment(ctx context.Context, repo string, n int, body string) (CommentInfo, error)
	HasLabel(ctx context.Context, repo, name string) (bool, error)
	CreateLabel(ctx context.Context, repo, name, description string) error
}

// REST is the GitHub over HTTP. Token gives the installation token of the App
// of who acts, for the repository; it never goes into an error.
type REST struct {
	BaseURL string
	HTTP    *http.Client
	Token   func(ctx context.Context, repo string) (string, error)
}

var _ GitHub = (*REST)(nil)

func (c *REST) do(ctx context.Context, method, repo, path string, body any) (int, []byte, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return 0, nil, err
	}
	tok, err := c.Token(ctx, repo)
	if err != nil {
		return 0, nil, fmt.Errorf("github: no token for %s", repo)
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+"/repos/"+repo+path, rd)
	if err != nil {
		return 0, nil, errors.New("github: bad request")
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, errors.New("github: " + strings.ReplaceAll(err.Error(), tok, "***"))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, raw, err
}

func (c *REST) get(ctx context.Context, repo, path string, out any) error {
	st, raw, err := c.do(ctx, http.MethodGet, repo, path, nil)
	switch {
	case err != nil:
		return err
	case st == http.StatusNotFound:
		return ErrNotFound
	case st != http.StatusOK:
		return fmt.Errorf("github: GET %s: status %d", path, st)
	}
	return json.Unmarshal(raw, out)
}

// Issue reads an issue.
func (c *REST) Issue(ctx context.Context, repo string, n int) (IssueInfo, error) {
	var v struct {
		Number int   `json:"number"`
		ID     int64 `json:"id"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := c.get(ctx, repo, fmt.Sprintf("/issues/%d", n), &v); err != nil {
		return IssueInfo{}, err
	}
	out := IssueInfo{Number: v.Number, ID: v.ID}
	for _, l := range v.Labels {
		out.Labels = append(out.Labels, l.Name)
	}
	return out, nil
}

// Parent reads the parent of a sub-issue.
func (c *REST) Parent(ctx context.Context, repo string, n int) (int, bool, error) {
	var v struct {
		Number int `json:"number"`
	}
	err := c.get(ctx, repo, fmt.Sprintf("/issues/%d/parent", n), &v)
	if errors.Is(err, ErrNotFound) {
		return 0, false, nil
	}
	return v.Number, err == nil, err
}

// Pull reads a PR.
func (c *REST) Pull(ctx context.Context, repo string, n int) (PullInfo, error) {
	var v struct {
		Head struct {
			Ref  string `json:"ref"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
	}
	if err := c.get(ctx, repo, fmt.Sprintf("/pulls/%d", n), &v); err != nil {
		return PullInfo{}, err
	}
	return PullInfo{HeadRef: v.Head.Ref, HeadRepo: v.Head.Repo.FullName}, nil
}

// Comments lists the comments of an issue or a PR, page by page (100 each, up to
// 50 pages).
func (c *REST) Comments(ctx context.Context, repo string, n int) ([]CommentInfo, error) {
	var out []CommentInfo
	for page := 1; page <= 50; page++ {
		var raw []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
		}
		if err := c.get(ctx, repo, fmt.Sprintf("/issues/%d/comments?per_page=100&page=%d", n, page), &raw); err != nil {
			return nil, err
		}
		for _, r := range raw {
			out = append(out, CommentInfo{ID: r.ID, Body: r.Body})
		}
		if len(raw) < 100 {
			return out, nil
		}
	}
	return nil, errors.New("github: too many comments to look through")
}

// PostComment creates a comment.
func (c *REST) PostComment(ctx context.Context, repo string, n int, body string) (CommentInfo, error) {
	st, raw, err := c.do(ctx, http.MethodPost, repo, fmt.Sprintf("/issues/%d/comments", n), map[string]string{"body": body})
	if err != nil {
		return CommentInfo{}, err
	}
	if st != http.StatusCreated {
		return CommentInfo{}, fmt.Errorf("github: creating a comment: status %d", st)
	}
	var v CommentInfo
	var r struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return CommentInfo{}, err
	}
	v = CommentInfo{ID: r.ID, Body: r.Body}
	return v, nil
}

// HasLabel says whether the repository has the label.
func (c *REST) HasLabel(ctx context.Context, repo, name string) (bool, error) {
	var v struct {
		Name string `json:"name"`
	}
	err := c.get(ctx, repo, "/labels/"+url.PathEscape(name), &v)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// CreateLabel creates a label; one that exists is fine.
func (c *REST) CreateLabel(ctx context.Context, repo, name, description string) error {
	st, _, err := c.do(ctx, http.MethodPost, repo, "/labels", map[string]string{"name": name, "description": description})
	if err != nil {
		return err
	}
	if st != http.StatusCreated && st != http.StatusUnprocessableEntity { // 422: it exists
		return fmt.Errorf("github: creating the label %q: status %d", name, st)
	}
	return nil
}
