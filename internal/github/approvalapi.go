package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// APIError is a refusal of GitHub (a 4xx other than a missing object or a
// limit) with the reason it gives. The reason is GitHub's own text about the
// request, never a secret.
type APIError struct {
	Op      string
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("github: %s: status %d: %s", e.Op, e.Status, e.Message)
}

// apiFailed turns the answer of a call that did not give what was wanted into an
// error: the ones failed knows, and a typed refusal for the other 4xx.
func apiFailed(what string, st int, raw []byte, hdr http.Header) error {
	if st >= 400 && st < 500 && st != http.StatusNotFound && st != http.StatusTooManyRequests && (st != http.StatusForbidden || hdr.Get("Retry-After") == "") {
		var v struct {
			Message string   `json:"message"`
			Errors  []string `json:"errors"`
		}
		_ = json.Unmarshal(raw, &v)
		msg := strings.Join(append([]string{v.Message}, v.Errors...), ": ")
		return &APIError{Op: what, Status: st, Message: clip(msg, 300)}
	}
	return failed(what, st, raw, hdr)
}

func clip(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// ReviewRules is what the main branch asks of a PR before it merges, from the
// rules of the branch (rulesets) and the classic protection.
type ReviewRules struct {
	// Approvals is the number of approving reviews the branch asks for (0: none).
	Approvals int
	// CodeOwners says a code owner must approve.
	CodeOwners bool
	// DismissStale says a push drops the approvals; LastPush that the approver
	// can not be who pushed last.
	DismissStale, LastPush bool
	// UpToDate says the branch must be up to date with the base before merging
	// (strict required status checks): the CI of the head then says what the
	// merge will be.
	UpToDate bool
	// Summary says which rules were read, to keep with the result.
	Summary string
	// Classic says the review is asked by the classic protection of the branch;
	// Rulesets are the rulesets that ask it.
	Classic  bool
	Rulesets []ReviewRuleset
}

// ReviewRuleset is a ruleset that holds the rule of review of the branch.
type ReviewRuleset struct {
	ID int64
	// Shared says the ruleset has rules besides the review.
	Shared bool
}

type ruleJSON struct {
	Type       string `json:"type"`
	RulesetID  int64  `json:"ruleset_id"`
	Parameters struct {
		Approvals  int  `json:"required_approving_review_count"`
		CodeOwners bool `json:"require_code_owner_review"`
		Dismiss    bool `json:"dismiss_stale_reviews_on_push"`
		LastPush   bool `json:"require_last_push_approval"`
		Strict     bool `json:"strict_required_status_checks_policy"`
	} `json:"parameters"`
}

// ReadRules reads the rules of the branch (GET /rules/branches/{branch}) and the
// classic protection (404 is a branch with none). An answer that can not be
// read is an error: the caller never takes it for "no requirement".
func (c *REST) ReadRules(ctx context.Context, repo, branch string) (ReviewRules, error) {
	var rules []ruleJSON
	if err := c.send(ctx, http.MethodGet, repo, "/rules/branches/"+url.PathEscape(branch), nil, http.StatusOK, &rules, "reading the rules of the branch"); err != nil {
		return ReviewRules{}, err
	}
	var out ReviewRules
	var notes []string
	perSet := map[int64]int{}
	for _, r := range rules {
		perSet[r.RulesetID]++
	}
	for _, r := range rules {
		switch r.Type {
		case "pull_request":
			out.Rulesets = append(out.Rulesets, ReviewRuleset{ID: r.RulesetID, Shared: perSet[r.RulesetID] > 1})
			out.Approvals = max(out.Approvals, r.Parameters.Approvals)
			out.CodeOwners = out.CodeOwners || r.Parameters.CodeOwners
			out.DismissStale = out.DismissStale || r.Parameters.Dismiss
			out.LastPush = out.LastPush || r.Parameters.LastPush
		case "required_status_checks":
			out.UpToDate = out.UpToDate || r.Parameters.Strict
		}
	}
	if len(rules) > 0 {
		notes = append(notes, fmt.Sprintf("%d regra(s) da branch", len(rules)))
	}
	st, hdr, raw, err := c.doHeader(ctx, http.MethodGet, repo, "/branches/"+url.PathEscape(branch)+"/protection", nil)
	if err != nil {
		return ReviewRules{}, err
	}
	switch {
	case st == http.StatusNotFound:
	case st != http.StatusOK:
		return ReviewRules{}, failed("reading the protection of the branch", st, raw, hdr)
	default:
		var p struct {
			Reviews *struct {
				Approvals  int  `json:"required_approving_review_count"`
				CodeOwners bool `json:"require_code_owner_reviews"`
				Dismiss    bool `json:"dismiss_stale_reviews"`
				LastPush   bool `json:"require_last_push_approval"`
			} `json:"required_pull_request_reviews"`
			Checks *struct {
				Strict bool `json:"strict"`
			} `json:"required_status_checks"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return ReviewRules{}, errors.New("github: the protection of the branch is not readable")
		}
		if p.Reviews != nil {
			out.Classic = true
			out.Approvals = max(out.Approvals, p.Reviews.Approvals)
			out.CodeOwners = out.CodeOwners || p.Reviews.CodeOwners
			out.DismissStale = out.DismissStale || p.Reviews.Dismiss
			out.LastPush = out.LastPush || p.Reviews.LastPush
		}
		if p.Checks != nil {
			out.UpToDate = out.UpToDate || p.Checks.Strict
		}
		notes = append(notes, "proteção clássica")
	}
	switch {
	case out.Approvals == 0 && !out.CodeOwners:
		out.Summary = "nenhuma revisão exigida"
	default:
		parts := []string{fmt.Sprintf("%d aprovação(ões) exigida(s)", max(out.Approvals, 1))}
		if out.CodeOwners {
			parts = append(parts, "code owners")
		}
		out.Summary = strings.Join(parts, ", ")
	}
	if len(notes) > 0 {
		out.Summary += " (" + strings.Join(notes, " e ") + ")"
	}
	return out, nil
}

// Requires says the branch asks for an approving review.
func (r ReviewRules) Requires() bool { return r.Approvals > 0 || r.CodeOwners }

// Review events.
const (
	ReviewApprove = "APPROVE"
	ReviewNote    = "COMMENT"
)

// CreateReview sends a review of a PR on the commit sha and returns its id. A
// 422 (an author approving the PR he opened, a PR that is closed) is an
// *APIError.
func (c *REST) CreateReview(ctx context.Context, repo string, n int, sha, event, body string) (int64, error) {
	var v struct {
		ID int64 `json:"id"`
	}
	st, hdr, raw, err := c.doHeader(ctx, http.MethodPost, repo, fmt.Sprintf("/pulls/%d/reviews", n), map[string]any{"commit_id": sha, "event": event, "body": body})
	if err != nil {
		return 0, err
	}
	if st != http.StatusOK {
		return 0, apiFailed("sending a review", st, raw, hdr)
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.ID == 0 {
		return 0, errors.New("github: the answer of the review is not readable")
	}
	return v.ID, nil
}

// ReviewDecision is the reviewDecision of a PR (GraphQL): APPROVED,
// CHANGES_REQUESTED, REVIEW_REQUIRED, or "" when the branch asks for no review.
func (c *REST) ReviewDecision(ctx context.Context, repo string, n int) (string, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return "", err
	}
	owner, name, _ := strings.Cut(repo, "/")
	q := map[string]any{
		"query":     "query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) { pullRequest(number: $number) { reviewDecision } } }",
		"variables": map[string]any{"owner": owner, "name": name, "number": n},
	}
	st, hdr, raw, err := c.doAbs(ctx, http.MethodPost, repo, "/graphql", q)
	if err != nil {
		return "", err
	}
	if st != http.StatusOK {
		return "", failed("reading the review decision", st, raw, hdr)
	}
	var v struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					ReviewDecision *string `json:"reviewDecision"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(raw, &v) != nil || len(v.Errors) > 0 || v.Data.Repository.PullRequest == nil {
		return "", errors.New("github: the review decision of the PR could not be read")
	}
	if d := v.Data.Repository.PullRequest.ReviewDecision; d != nil {
		return *d, nil
	}
	return "", nil
}

// ClosePull closes a PR without merging it.
func (c *REST) ClosePull(ctx context.Context, repo string, n int) error {
	return c.send(ctx, http.MethodPatch, repo, fmt.Sprintf("/pulls/%d", n), map[string]any{"state": "closed"}, http.StatusOK, nil, "closing a PR")
}

// RefSHA is the commit a branch points at (ErrNotFound for a branch that is not there).
func (c *REST) RefSHA(ctx context.Context, repo, branch string) (string, error) {
	var v struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.send(ctx, http.MethodGet, repo, "/git/ref/heads/"+escapeRef(branch), nil, http.StatusOK, &v, "reading a branch"); err != nil {
		return "", err
	}
	return v.Object.SHA, nil
}

// CreateBranch makes a branch at the commit sha.
func (c *REST) CreateBranch(ctx context.Context, repo, branch, sha string) error {
	return c.send(ctx, http.MethodPost, repo, "/git/refs", map[string]any{"ref": "refs/heads/" + branch, "sha": sha}, http.StatusCreated, nil, "creating a branch")
}

// DeleteBranch deletes a branch; one that is already gone is not an error.
func (c *REST) DeleteBranch(ctx context.Context, repo, branch string) error {
	st, hdr, raw, err := c.doHeader(ctx, http.MethodDelete, repo, "/git/refs/heads/"+escapeRef(branch), nil)
	if err != nil {
		return err
	}
	switch st {
	case http.StatusNoContent, http.StatusNotFound, http.StatusUnprocessableEntity: // 422: "Reference does not exist"
		return nil
	}
	return failed("deleting a branch", st, raw, hdr)
}

// PutFile writes a file to a branch in a commit of its own.
func (c *REST) PutFile(ctx context.Context, repo, branch, path, text, message string) error {
	in := map[string]any{"message": message, "content": base64.StdEncoding.EncodeToString([]byte(text)), "branch": branch}
	return c.send(ctx, http.MethodPut, repo, "/contents/"+escapePath(path), in, http.StatusCreated, nil, "writing a file")
}

// FileText reads a text file of a ref; ok is false when it does not exist.
func (c *REST) FileText(ctx context.Context, repo, ref, path string) (text string, ok bool, err error) {
	var v struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	err = c.send(ctx, http.MethodGet, repo, "/contents/"+escapePath(path)+"?ref="+url.QueryEscape(ref), nil, http.StatusOK, &v, "reading a file")
	if errors.Is(err, ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if v.Type != "file" || v.Encoding != "base64" {
		return "", false, errors.New("github: the file is not readable")
	}
	b, derr := base64.StdEncoding.DecodeString(strings.ReplaceAll(v.Content, "\n", ""))
	if derr != nil {
		return "", false, errors.New("github: the file is not readable")
	}
	return string(b), true, nil
}

// CollaboratorPermission is the permission a login has in the repository now
// (admin, maintain, write, triage, read or none).
func (c *REST) CollaboratorPermission(ctx context.Context, repo, login string) (string, error) {
	var v struct {
		Permission string `json:"permission"`
	}
	st, hdr, raw, err := c.doHeader(ctx, http.MethodGet, repo, "/collaborators/"+url.PathEscape(login)+"/permission", nil)
	if err != nil {
		return "", err
	}
	if st != http.StatusOK {
		return "", apiFailed("reading a permission", st, raw, hdr)
	}
	if json.Unmarshal(raw, &v) != nil {
		return "", errors.New("github: the permission is not readable")
	}
	return v.Permission, nil
}

// CreateRuleset creates a ruleset of the repository. The refusal of GitHub (a
// private repository without Pro answers 403) is an *APIError.
func (c *REST) CreateRuleset(ctx context.Context, repo string, body map[string]any) error {
	st, hdr, raw, err := c.doHeader(ctx, http.MethodPost, repo, "/rulesets", body)
	if err != nil {
		return err
	}
	if st != http.StatusCreated {
		return apiFailed("creating a ruleset", st, raw, hdr)
	}
	return nil
}

// RepoInfo is what the engine reads of a repository.
type RepoInfo struct {
	ID            int64
	Private       bool
	DefaultBranch string
}

// Repo reads the repository.
func (c *REST) Repo(ctx context.Context, repo string) (RepoInfo, error) {
	var v struct {
		ID            int64  `json:"id"`
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.send(ctx, http.MethodGet, repo, "", nil, http.StatusOK, &v, "reading the repository"); err != nil {
		return RepoInfo{}, err
	}
	return RepoInfo{ID: v.ID, Private: v.Private, DefaultBranch: v.DefaultBranch}, nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func escapeRef(r string) string { return escapePath(r) }
