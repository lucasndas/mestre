package panel

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("github-apps", mountApps) }

const (
	pinAppLink = "github.app_link"
	// maxOthers keeps a typo from becoming the count of the account.
	maxOthers = 10000
)

func mountApps(r *Router) {
	p := r.Panel()
	// the Administrator sees the Apps and their state; the links and the count of the
	// others are the Owner's, because the Apps are born in his personal account
	r.Require("GET /api/github/apps", p.handleApps, users.ApproveWorkspaceCls)
	r.Require("PUT /api/github/apps/others", p.handleOthers, users.GitHubApps)
	r.Sensitive("POST /api/github/apps/{name}/link", pinAppLink, users.GitHubApps, p.handleAppLink)
}

// handleApps lists the fixed and per-class Apps, where they are installed and the
// "N de 100 Apps na conta"; for the Owner, also the requests of creation that wait.
func (p *Panel) handleApps(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	reg := &github.Registry{DB: p.db, Clock: p.clk}
	count, err := p.appCount(ctx)
	if err != nil {
		p.log.Error("panel: counting the Apps", "err", err)
		WriteError(w, errInternal())
		return
	}
	apps, err := reg.List(ctx)
	if err != nil {
		p.log.Error("panel: listing the Apps", "err", err)
		WriteError(w, errInternal())
		return
	}
	owner := users.Can(me.Level, users.GitHubApps)
	page := AppsPage{Count: count, Apps: []AppRow{}, Requests: []AppRequestRow{}, Creation: p.creation != nil}
	open := map[string]string{}
	if owner {
		if page.Requests, err = p.appRequests(r); err != nil {
			p.log.Error("panel: listing the requests of Apps", "err", err)
			WriteError(w, errInternal())
			return
		}
		for _, q := range page.Requests {
			open[q.App] = q.Status
		}
	}
	if owner && p.creation != nil {
		page.NextFixed = nextFixed(apps)
	}
	// the Owner sees every App; the others, the fixed ones and the ones installed on the
	// repositories of the workspaces they act in, with only those repositories
	list, all := actingIn(me)
	mine, err := p.repoSet(ctx, list)
	if err != nil {
		p.log.Error("panel: the repositories of the workspaces", "err", err)
		WriteError(w, errInternal())
		return
	}
	for _, a := range apps {
		repos, err := reg.Repos(ctx, a.Name)
		if err != nil {
			p.log.Error("panel: the repositories of an App", "app", a.Name, "err", err)
			WriteError(w, errInternal())
			return
		}
		if !all {
			repos = slices.DeleteFunc(slices.Clone(repos), func(r string) bool { return !mine[strings.ToLower(r)] })
			if a.Role == github.RoleClass && len(repos) == 0 {
				continue
			}
		}
		linkable := a.State == classes.AppAwaitingCreation || a.State == classes.AppExpired
		page.Apps = append(page.Apps, AppRow{Name: a.Name, Slug: a.Slug, Role: a.Role, Class: a.Class, State: string(a.State), StateLabel: a.State.Label(),
			Repos: orEmpty(repos), Request: open[a.Name], CanLink: owner && p.creation != nil && linkable})
	}
	WriteJSON(w, http.StatusOK, page)
}

// fixedApps are the two Apps every repository has, in the order the screen offers them.
var fixedApps = []struct{ name, role string }{
	{"Grão-Mestre", github.RoleGrandMaster},
	{"Mestre", github.RoleMestre},
}

// nextFixed is the name of the first fixed App that is not in the registry yet.
func nextFixed(apps []github.App) string {
	for _, f := range fixedApps {
		if !slices.ContainsFunc(apps, func(a github.App) bool { return a.Role == f.role }) {
			return f.name
		}
	}
	return ""
}

// repoSet is the repositories (lower case) of the workspaces.
func (p *Panel) repoSet(ctx context.Context, workspaces []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, ws := range workspaces {
		var repo string
		switch err := p.db.Reader().QueryRowContext(ctx, `SELECT repo FROM workspaces WHERE slug = ?`, ws).Scan(&repo); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return nil, err
		default:
			out[strings.ToLower(repo)] = true
		}
	}
	return out, nil
}

// appRequests are the latest request of each App that has not ended well: the link
// that waits, the one that expired and the ones that failed. The link itself is not
// here: only its hash is kept.
func (p *Panel) appRequests(r *http.Request) ([]AppRequestRow, error) {
	rows, err := p.db.Reader().QueryContext(r.Context(), `SELECT app, status, link_expires_at FROM github_app_requests q
		WHERE id = (SELECT MAX(id) FROM github_app_requests WHERE app = q.app) AND status IN ('link', 'opened', 'exchanging', 'expired', 'failed', 'unconfirmed')
		ORDER BY app`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppRequestRow{}
	now := p.clk.Now()
	for rows.Next() {
		var q AppRequestRow
		var exp int64
		if err := rows.Scan(&q.App, &q.Status, &exp); err != nil {
			return nil, err
		}
		q.ExpiresAt = time.UnixMilli(exp).UTC()
		q.Expired = q.Status == "expired" || q.Status == "link" && !now.Before(q.ExpiresAt)
		out = append(out, q)
	}
	return out, rows.Err()
}

// handleOthers records how many Apps of the account are not the Mestre's. The Mestre
// does not read the Apps page of GitHub: the Owner says it.
func (p *Panel) handleOthers(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	var in OthersRequest
	if !decode(w, r, &in) {
		return
	}
	if in.Count < 0 || in.Count > maxOthers {
		WriteError(w, errBadRequest())
		return
	}
	reg := &github.Registry{DB: p.db, Clock: p.clk}
	before, err := reg.Others(ctx)
	if err == nil {
		err = reg.SetOthers(ctx, in.Count)
	}
	if err != nil {
		p.log.Error("panel: the count of other Apps", "err", err)
		WriteError(w, errInternal())
		return
	}
	p.audit(ctx, "panel.github_apps.others", map[string]any{"user_id": me.ID, "before": before, "after": in.Count, "ip": clientIP(r)})
	count, err := p.appCount(ctx)
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	WriteJSON(w, http.StatusOK, count)
}

// handleAppLink makes a new link of creation of an App that waits for it or whose link
// expired. The link comes in this answer only: the token is not kept. The Owner only,
// with the PIN: the page the link opens sends GitHub the manifest of the App.
// Part of CA-GH-APPS-04 (the page itself refuses anybody who is not the Owner).
func (p *Panel) handleAppLink(w http.ResponseWriter, r *http.Request) {
	ctx, me := r.Context(), UserOf(r)
	if p.creation == nil {
		WriteError(w, Err(CodeUnavailable, "a criação de Apps não está disponível agora", nil))
		return
	}
	name := r.PathValue("name")
	link, err := p.creation.Begin(ctx, name, me.ID)
	if errors.Is(err, github.ErrNoApp) || errors.Is(err, sql.ErrNoRows) {
		// a fixed App is born with its first link: the registry has no row of it before
		if f := slices.IndexFunc(fixedApps, func(f struct{ name, role string }) bool { return f.name == name }); f >= 0 {
			reg := &github.Registry{DB: p.db, Clock: p.clk}
			_, rerr := reg.Request(ctx, github.App{Name: name, Role: fixedApps[f].role})
			if rerr == nil || errors.Is(rerr, github.ErrNameTaken) {
				link, err = p.creation.Begin(ctx, name, me.ID)
			} else {
				err = rerr
			}
		}
	}
	switch {
	case err == nil:
		p.audit(ctx, "panel.github_apps.link", map[string]any{"user_id": me.ID, "app": link.App, "expires_at": link.ExpiresAt, "ip": clientIP(r)})
		WriteJSON(w, http.StatusOK, AppLinkResponse{App: link.App, URL: link.URL, ExpiresAt: link.ExpiresAt})
	case errors.Is(err, github.ErrNoApp), errors.Is(err, sql.ErrNoRows):
		WriteError(w, Err(CodeNotFound, "App não encontrado", nil))
	case errors.Is(err, github.ErrAppState):
		WriteError(w, Err(CodeAppState, "este App não espera um link agora", nil))
	default:
		p.log.Error("panel: the link of an App", "err", err)
		WriteError(w, errInternal())
	}
}
