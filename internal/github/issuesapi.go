package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ErrUnavailable is GitHub out (a 5xx or no network): the command that changes
// data is not repeated on its own (docs/cli/coordenacao-github.md#regras-e-erros).
var ErrUnavailable = errors.New("github: unavailable")

// LimitError is a limit of GitHub (the secondary rate limit, a 403 or 429 with
// a time to wait). It names when to try again; the action is not repeated.
type LimitError struct{ RetryAfter time.Duration }

func (e *LimitError) Error() string {
	return fmt.Sprintf("github: limit reached, try again in %s", e.RetryAfter.Round(time.Second))
}

// IssueData is an issue as the commands read it.
type IssueData struct {
	Number int
	ID     int64
	Title  string
	Body   string
	State  string
	URL    string
	Labels []string
}

// IssueEdit is what `issue update` changes; nil is left as it is. Labels is the
// whole list.
type IssueEdit struct {
	Title, Body *string
	Labels      *[]string
	State       *string
	StateReason string
}

type issueJSON struct {
	Number  int    `json:"number"`
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	Labels  []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (v issueJSON) data() IssueData {
	out := IssueData{Number: v.Number, ID: v.ID, Title: v.Title, Body: v.Body, State: v.State, URL: v.HTMLURL}
	for _, l := range v.Labels {
		out.Labels = append(out.Labels, l.Name)
	}
	return out
}

// failed turns a status that is not the expected one into the error the
// commands tell apart.
func failed(what string, st int, raw []byte, hdr http.Header) error {
	switch {
	case st >= 500:
		return fmt.Errorf("%w: %s: status %d", ErrUnavailable, what, st)
	case st == http.StatusNotFound:
		return ErrNotFound
	case st == http.StatusTooManyRequests || st == http.StatusForbidden && hdr.Get("Retry-After") != "":
		wait, _ := strconv.Atoi(hdr.Get("Retry-After"))
		return &LimitError{RetryAfter: time.Duration(max(wait, 1)) * time.Second}
	}
	return fmt.Errorf("github: %s: status %d", what, st)
}

func (c *REST) send(ctx context.Context, method, repo, path string, body any, want int, out any, what string) error {
	st, hdr, raw, err := c.doHeader(ctx, method, repo, path, body)
	if err != nil {
		return err
	}
	if st != want {
		return failed(what, st, raw, hdr)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// CreateIssue creates an issue.
func (c *REST) CreateIssue(ctx context.Context, repo, title, body string, labels []string) (IssueData, error) {
	in := map[string]any{"title": title, "body": body}
	if len(labels) > 0 {
		in["labels"] = labels
	}
	var v issueJSON
	if err := c.send(ctx, http.MethodPost, repo, "/issues", in, http.StatusCreated, &v, "creating an issue"); err != nil {
		return IssueData{}, err
	}
	return v.data(), nil
}

// IssueData reads an issue with its title, body and state.
func (c *REST) IssueData(ctx context.Context, repo string, n int) (IssueData, error) {
	var v issueJSON
	if err := c.send(ctx, http.MethodGet, repo, fmt.Sprintf("/issues/%d", n), nil, http.StatusOK, &v, "reading an issue"); err != nil {
		return IssueData{}, err
	}
	return v.data(), nil
}

// UpdateIssue changes an issue.
func (c *REST) UpdateIssue(ctx context.Context, repo string, n int, e IssueEdit) error {
	in := map[string]any{}
	if e.Title != nil {
		in["title"] = *e.Title
	}
	if e.Body != nil {
		in["body"] = *e.Body
	}
	if e.Labels != nil {
		in["labels"] = *e.Labels
	}
	if e.State != nil {
		in["state"] = *e.State
		if e.StateReason != "" {
			in["state_reason"] = e.StateReason
		}
	}
	return c.send(ctx, http.MethodPatch, repo, fmt.Sprintf("/issues/%d", n), in, http.StatusOK, nil, "updating an issue")
}

// AddSubIssue makes the issue with the id a sub-issue of parent.
func (c *REST) AddSubIssue(ctx context.Context, repo string, parent int, childID int64) error {
	return c.send(ctx, http.MethodPost, repo, fmt.Sprintf("/issues/%d/sub_issues", parent), map[string]int64{"sub_issue_id": childID}, http.StatusCreated, nil, "linking a sub-issue")
}

// SubIssues lists the sub-issues of an issue.
func (c *REST) SubIssues(ctx context.Context, repo string, n int) ([]IssueData, error) {
	var raw []issueJSON
	if err := c.send(ctx, http.MethodGet, repo, fmt.Sprintf("/issues/%d/sub_issues?per_page=100", n), nil, http.StatusOK, &raw, "listing sub-issues"); err != nil {
		return nil, err
	}
	out := make([]IssueData, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.data())
	}
	return out, nil
}

// Issues lists the issues of the repository in a state (open, closed, all), the
// first page of 100.
func (c *REST) Issues(ctx context.Context, repo, state string) ([]IssueData, error) {
	var raw []issueJSON
	q := url.Values{"state": {state}, "per_page": {"100"}}
	if err := c.send(ctx, http.MethodGet, repo, "/issues?"+q.Encode(), nil, http.StatusOK, &raw, "listing issues"); err != nil {
		return nil, err
	}
	out := make([]IssueData, 0, len(raw))
	for _, v := range raw {
		out = append(out, v.data())
	}
	return out, nil
}
