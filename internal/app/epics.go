package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/ghcli"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// stdEpics is GitHub's side of the tables (tables.GitHub): the epics and the
// pull requests, read and closed with the App of the Grão-Mestre of the
// workspace.
type stdEpics struct {
	r   *stdRun
	reg characters.Registry
}

var (
	_ tables.GitHub  = stdEpics{}
	_ tables.Details = stdEpics{}
)

// api gives the GitHub of the Grão-Mestre of the workspace, and its repository.
func (e stdEpics) api(ctx context.Context, workspace string) (github.PullAPI, string, error) {
	var repo string
	err := e.r.env.DB.Reader().QueryRowContext(ctx, `SELECT repo FROM workspaces WHERE slug = ?`, workspace).Scan(&repo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", fmt.Errorf("the workspace %s has no repository", workspace)
	}
	if err != nil {
		return nil, "", err
	}
	gm, ok, err := e.reg.GrandMaster(ctx, workspace)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", errors.New("the workspace has no Grand Master to act on GitHub")
	}
	api, err := stdClients{e.r}.For(ctx, gm, repo)
	return api, repo, err
}

// Epic implements tables.GitHub; an issue that does not exist is
// tables.ErrEpicNotFound.
func (e stdEpics) Epic(ctx context.Context, workspace string, number int) (tables.Epic, error) {
	api, repo, err := e.api(ctx, workspace)
	if err != nil {
		return tables.Epic{}, err
	}
	d, err := api.IssueData(ctx, repo, number)
	if errors.Is(err, github.ErrNotFound) {
		return tables.Epic{}, fmt.Errorf("%w: #%d", tables.ErrEpicNotFound, number)
	}
	if err != nil {
		return tables.Epic{}, err
	}
	if d.IsPull {
		// the issues endpoint answers for a pull request too
		return tables.Epic{}, fmt.Errorf("%w: #%d is a pull request", tables.ErrEpicNotFound, number)
	}
	return tables.Epic{Number: d.Number, Title: d.Title, Body: github.HideMarkers(d.Body), Labels: d.Labels, Open: d.State == "open", NotPlanned: d.State != "open" && d.StateReason == "not_planned"}, nil
}

// errLocked is what a write on GitHub answers while the Mestre is locked
// (/travar): the engine acts on nothing outside.
var errLocked = errors.New("the Mestre is locked: no action on GitHub is made")

// CloseEpic implements tables.GitHub: the epic is closed as "not planned".
func (e stdEpics) CloseEpic(ctx context.Context, workspace string, number int) error {
	if e.r.lock.Locked(ctx) {
		return errLocked
	}
	api, repo, err := e.api(ctx, workspace)
	if err != nil {
		return err
	}
	// an epic closed already keeps the reason it was closed with
	if d, err := api.IssueData(ctx, repo, number); err == nil && d.State != "open" {
		return nil
	}
	closed := "closed"
	return api.UpdateIssue(ctx, repo, number, github.IssueEdit{State: &closed, StateReason: "not_planned"})
}

// ClosePR implements tables.GitHub: the pull request is closed without merge.
func (e stdEpics) ClosePR(ctx context.Context, workspace string, number int) error {
	if e.r.lock.Locked(ctx) {
		return errLocked
	}
	api, repo, err := e.api(ctx, workspace)
	if err != nil {
		return err
	}
	c, ok := api.(interface {
		ClosePull(ctx context.Context, repo string, n int) error
	})
	if !ok {
		return errors.New("the GitHub of this run cannot close a pull request")
	}
	return c.ClosePull(ctx, repo, number)
}

// appCreator is the creation of an App before the panel: the link of creation is a
// page of the panel, which only exists in milestone 6, so the command prepares
// nothing and says why (docs/desenvolvimento/ligacao-de-producao.md).
type appCreator struct{}

func (appCreator) Available(context.Context) string {
	return "the link of the creation of an App is a page of the panel, which comes with milestone 6: nothing was prepared"
}

func (appCreator) Begin(context.Context, string, string) (ghcli.AppLink, error) {
	return ghcli.AppLink{}, errors.New("the creation of Apps needs the panel")
}

// stdSettings is what `gh settings show` reads of the Mestre: the main branch of
// the workspace, the mode of approval of its reviews and the Apps.
type stdSettings struct{ r *stdRun }

func (s stdSettings) Branch(ctx context.Context, workspace string) (string, error) {
	var branch string
	err := s.r.env.DB.Reader().QueryRowContext(ctx, `SELECT default_branch FROM workspaces WHERE slug = ?`, workspace).Scan(&branch)
	return branch, err
}

func (s stdSettings) Mode(ctx context.Context, workspace string) (string, string, error) {
	mode, why, err := (&approvals.Modes{DB: s.r.env.DB, Clock: s.r.env.Clock}).Get(ctx, workspace)
	return string(mode), why, err
}

func (s stdSettings) Apps(ctx context.Context) ([]string, error) {
	apps, err := (&github.Registry{DB: s.r.env.DB}).List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(apps))
	for _, a := range apps {
		out = append(out, fmt.Sprintf("%s (%s)", a.Name, a.State))
	}
	return out, nil
}

// OpenIssues implements tables.Details: the open issues under the epic.
func (e stdEpics) OpenIssues(ctx context.Context, workspace string, epic int) ([]tables.IssueRef, error) {
	api, repo, err := e.api(ctx, workspace)
	if err != nil {
		return nil, err
	}
	subs, err := api.SubIssues(ctx, repo, epic)
	if err != nil {
		return nil, err
	}
	var out []tables.IssueRef
	for _, i := range subs {
		if i.State == "open" && !i.IsPull {
			out = append(out, tables.IssueRef{Number: i.Number, Title: i.Title})
		}
	}
	return out, nil
}

// PullStats implements tables.Details: the files and lines of the pull request and
// what its checks say (failure if any failed, pending if any is not over, success
// if all passed, none if it has none).
func (e stdEpics) PullStats(ctx context.Context, workspace string, number int) (tables.PullStats, error) {
	api, repo, err := e.api(ctx, workspace)
	if err != nil {
		return tables.PullStats{}, err
	}
	pr, err := api.PullData(ctx, repo, number)
	if err != nil {
		return tables.PullStats{}, err
	}
	files, err := api.PullFiles(ctx, repo, number)
	if err != nil {
		return tables.PullStats{}, err
	}
	st := tables.PullStats{Files: len(files), CI: "none"}
	for _, f := range files {
		st.Lines += f.Additions + f.Deletions
	}
	checks, err := api.CheckRuns(ctx, repo, pr.HeadSHA)
	if err != nil {
		return tables.PullStats{}, err
	}
	// The legacy commit statuses count too: a CI that reports by them is not "none".
	if ss, ok := api.(interface {
		CommitStatuses(ctx context.Context, repo, sha string) ([]github.Check, error)
	}); ok {
		statuses, err := ss.CommitStatuses(ctx, repo, pr.HeadSHA)
		if err != nil {
			return tables.PullStats{}, err
		}
		for _, s := range statuses {
			if s.State == "error" {
				s.State = "failure"
			}
			checks = append(checks, s)
		}
	}
	failed, pending := false, false
	for _, c := range checks {
		switch c.State {
		case "success", "neutral", "skipped":
		case "failure", "cancelled", "timed_out", "action_required", "startup_failure":
			failed = true
		default:
			pending = true
		}
	}
	switch {
	case failed:
		st.CI = "failure"
	case pending:
		st.CI = "pending"
	case len(checks) > 0:
		st.CI = "success"
	}
	return st, nil
}
