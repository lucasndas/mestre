package github

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TablePR is an open PR of a table.
type TablePR struct {
	Number int
	Table  string
}

// Merges is what the merge of the PR of a table needs: the mirror, to know the
// commit is in the base, and the table, to be marked merged.
type Merges interface {
	// Reachable says whether the commit can be reached from the base in the mirror
	// of the workspace (git merge-base --is-ancestor).
	Reachable(ctx context.Context, w Watch, sha string) (bool, error)
	// Merged marks the table merged.
	Merged(ctx context.Context, w Watch, table string, number int) error
}

// MergeWait is how long a merge not yet in the mirror is checked again, at each
// cycle, before the alert.
const MergeWait = time.Hour

// ciRecheck is how long the commit whose checks ended is left alone.
const ciRecheck = 10 * time.Minute

type prRead struct {
	events  []Event
	keys    []string
	running bool
	// done are the commits whose CI ended: they are not asked about again.
	done []string
	p    *Poller
}

func (r *prRead) commit() {
	r.p.mu.Lock()
	defer r.p.mu.Unlock()
	now := r.p.o.Clock.Now()
	if r.p.ciDone == nil {
		r.p.ciDone = map[string]time.Time{}
	}
	for _, sha := range r.done {
		r.p.ciDone[sha] = now
	}
	for sha, at := range r.p.ciDone { // the memory does not grow without end
		if now.Sub(at) > 24*time.Hour {
			delete(r.p.ciDone, sha)
		}
	}
}

type reviewItem struct {
	ID          int64     `json:"id"`
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submitted_at"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	User        struct {
		Login string `json:"login"`
	} `json:"user"`
}

// readTablePRs reads, for each open PR of a table, its reviews and the CI of its
// head: the PRs of the repository (head and state), the reviews of each, and the
// check runs and the statuses of the head until every check of the commit ended.
func (p *Poller) readTablePRs(ctx context.Context, w Watch, repo string) (*prRead, error) {
	out := &prRead{p: p}
	prs := p.o.TablePRs(ctx, w)
	if len(prs) == 0 {
		return out, nil
	}
	token, err := p.o.Token(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("github: no token to read %s: %w", repo, err)
	}
	heads, err := p.heads(ctx, repo, token)
	if err != nil {
		return nil, err
	}
	local := map[string]bool{}
	add := func(ev Event) error {
		if local[ev.Key] {
			return nil
		}
		local[ev.Key] = true
		if seen, err := p.seen(ctx, ev.Key); err != nil || seen {
			return err
		}
		out.events = append(out.events, ev)
		out.keys = append(out.keys, ev.Key)
		return nil
	}
	for _, pr := range prs {
		var reviews []reviewItem
		if err := p.getPages(ctx, repo, token, fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100", repo, pr.Number), "", &reviews); err != nil {
			if isLimit(err) {
				return nil, err
			}
			// one PR that can not be read (a 404, a permission) does not stop the others
			p.o.Logger.Warn("github: the reviews of a PR were not read", "workspace", w.Workspace, "pr", pr.Number, "err", err)
			continue
		}
		for _, rv := range reviews {
			// the memory of what was seen lasts 30 days: older than that is not news
			if rv.SubmittedAt.Before(w.Since) || rv.SubmittedAt.Before(p.o.Clock.Now().Add(-SeenLife)) || rv.State == "PENDING" {
				continue
			}
			ev := Event{Kind: EventReview, ID: rv.ID, Key: fmt.Sprintf("revisao:%d:%s", rv.ID, rv.State), Workspace: w.Workspace, Repo: repo, Number: pr.Number,
				Table: pr.Table, Author: rv.User.Login, Link: rv.HTMLURL, At: rv.SubmittedAt, Summary: strings.ToLower(rv.State) + shortSuffix(rv.Body)}
			if err := add(ev); err != nil {
				return nil, err
			}
		}
		sha := heads[pr.Number]
		p.mu.Lock()
		doneAt, ok := p.ciDone[sha]
		p.mu.Unlock()
		// a commit whose checks ended is looked at again only now and then: a re-run
		// or a late workflow makes new check runs on the same commit
		if sha == "" || (ok && p.o.Clock.Now().Sub(doneAt) < ciRecheck) {
			continue
		}
		var runs struct {
			CheckRuns []struct {
				ID         int64   `json:"id"`
				Name       string  `json:"name"`
				Status     string  `json:"status"`
				Conclusion *string `json:"conclusion"`
				HTMLURL    string  `json:"html_url"`
			} `json:"check_runs"`
		}
		if err := p.getPages(ctx, repo, token, fmt.Sprintf("/repos/%s/commits/%s/check-runs?per_page=100", repo, sha), "check_runs", &runs.CheckRuns); err != nil {
			if isLimit(err) {
				return nil, err
			}
			p.o.Logger.Warn("github: the checks of a commit were not read", "workspace", w.Workspace, "pr", pr.Number, "err", err)
			continue
		}
		running := false
		for _, c := range runs.CheckRuns {
			if c.Status != "completed" {
				running = true
				continue
			}
			concl := ""
			if c.Conclusion != nil {
				concl = *c.Conclusion
			}
			ev := Event{Kind: EventCI, Key: fmt.Sprintf("check:%d:%s:%s", c.ID, c.Status, concl), Workspace: w.Workspace, Repo: repo, Number: pr.Number,
				Table: pr.Table, Link: c.HTMLURL, At: p.o.Clock.Now(), Summary: shorten(c.Name) + ": " + shorten(concl)}
			if err := add(ev); err != nil {
				return nil, err
			}
		}
		var comb struct {
			State    string `json:"state"`
			Statuses []struct {
				Context   string `json:"context"`
				TargetURL string `json:"target_url"`
			} `json:"statuses"`
		}
		if err := p.getJSON(ctx, repo, token, fmt.Sprintf("/repos/%s/commits/%s/status", repo, sha), &comb); err != nil {
			if isLimit(err) {
				return nil, err
			}
			p.o.Logger.Warn("github: the statuses of a commit were not read", "workspace", w.Workspace, "pr", pr.Number, "err", err)
			continue
		}
		if len(comb.Statuses) > 0 {
			if comb.State == "pending" {
				running = true
			} else {
				ev := Event{Kind: EventCI, Key: fmt.Sprintf("status:%s:%s:%d", sha, comb.State, len(comb.Statuses)), Workspace: w.Workspace, Repo: repo, Number: pr.Number,
					Table: pr.Table, Link: comb.Statuses[0].TargetURL, At: p.o.Clock.Now(), Summary: "status: " + comb.State}
				if err := add(ev); err != nil {
					return nil, err
				}
			}
		}
		if running {
			out.running = true
		} else if len(runs.CheckRuns) > 0 || len(comb.Statuses) > 0 {
			out.done = append(out.done, sha)
		}
	}
	return out, nil
}

func shortSuffix(body string) string {
	if s := shorten(body); s != "" {
		return ": " + s
	}
	return ""
}

// heads are the head commits of the open PRs of the repository.
func (p *Poller) heads(ctx context.Context, repo, token string) (map[int]string, error) {
	var list []struct {
		Number int `json:"number"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := p.getPages(ctx, repo, token, fmt.Sprintf("/repos/%s/pulls?state=open&per_page=100", repo), "", &list); err != nil {
		return nil, err
	}
	out := make(map[int]string, len(list))
	for _, x := range list {
		out[x.Number] = x.Head.SHA
	}
	return out, nil
}

// getJSON is a GET that must answer 200 with JSON; a limit is a RetryAfterError.
func (p *Poller) getJSON(ctx context.Context, repo, token, path string, out any) error {
	status, hdr, raw, err := p.get(ctx, path, token, "")
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusTooManyRequests || status == http.StatusForbidden:
		return p.limited(status, hdr)
	case status != http.StatusOK:
		return fmt.Errorf("github: reading %s: status %d", repo, status)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return errors.New("github: an answer of GitHub is not readable")
	}
	return nil
}

// isLimit says the error is a limit of GitHub, which stops the cycle for the workspace.
func isLimit(err error) bool {
	var lim *RetryAfterError
	return errors.As(err, &lim)
}

// getPages reads a list in pages of 100 into out (a slice); key is the field of
// the object that holds the list ("" when the answer is the list itself).
func (p *Poller) getPages(ctx context.Context, repo, token, path, key string, out any) error {
	var all []json.RawMessage
	for page := 1; page <= 10; page++ {
		var doc json.RawMessage
		if err := p.getJSON(ctx, repo, token, path+"&page="+strconv.Itoa(page), &doc); err != nil {
			return err
		}
		raw := doc
		if key != "" {
			var obj map[string]json.RawMessage
			if json.Unmarshal(doc, &obj) != nil {
				return errors.New("github: an answer of GitHub is not readable")
			}
			raw = obj[key]
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return errors.New("github: an answer of GitHub is not readable")
		}
		all = append(all, items...)
		if len(items) < perPage {
			break
		}
	}
	joined, _ := json.Marshal(all)
	return json.Unmarshal(joined, out)
}

// merges is the outcome of the confirmation of the merges of a cycle.
type merges struct {
	events []Event
	// merged are the tables to mark merged once the events were delivered.
	merged []Event
	// pending are the merges not in the mirror yet, new ones; resolved are the
	// ones that were pending and are done (or gave up).
	pending  []pendingMerge
	resolved []int
}

type pendingMerge struct {
	Event
	SHA string
	// update says the row exists and only its commit is new; since is when it began.
	update bool
	since  time.Time
}

func (m *merges) persist(ctx context.Context, tx *sql.Tx, repo string, now time.Time) error {
	for _, pm := range m.pending {
		if pm.update {
			if _, err := tx.ExecContext(ctx, `UPDATE github_pending_merges SET sha = ? WHERE repo = ? AND number = ?`, pm.SHA, repo, pm.Number); err != nil {
				return err
			}
			continue
		}
		// a stale row of an earlier merge of the same PR is replaced
		if _, err := tx.ExecContext(ctx, `INSERT INTO github_pending_merges (repo, number, table_id, sha, author, link, summary, since)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (repo, number) DO UPDATE SET table_id = excluded.table_id, sha = excluded.sha,
			author = excluded.author, link = excluded.link, summary = excluded.summary, since = excluded.since`,
			pm.Repo, pm.Number, pm.Table, pm.SHA, pm.Author, pm.Link, pm.Summary, now.UnixMilli()); err != nil {
			return err
		}
	}
	for _, n := range m.resolved {
		if _, err := tx.ExecContext(ctx, `DELETE FROM github_pending_merges WHERE number = ? AND repo = ?`, n, repo); err != nil {
			return err
		}
	}
	return nil
}

// reachable asks the mirror; an empty commit, or a mirror that fails, is "not
// yet": the merge waits and is asked again, it does not stop the workspace.
func (p *Poller) reachable(ctx context.Context, w Watch, sha string) bool {
	if sha == "" {
		return false
	}
	ok, err := p.o.Merges.Reachable(ctx, w, sha)
	if err != nil {
		p.o.Logger.Warn("github: the mirror could not say whether a merge commit is there", "workspace", w.Workspace, "err", err)
		return false
	}
	return ok
}

// confirmMerges lets a merge of a table through only when the commit is in the
// mirror (no "ready" goes to the user before that). One that is not there yet
// becomes a warning to the Grand Master and is checked again at each cycle for an
// hour, then it is an alert and is not checked anymore.
func (p *Poller) confirmMerges(ctx context.Context, w Watch, repo string, events []Event) (*merges, error) {
	out := &merges{}
	if p.o.Merges == nil {
		out.events = events
		return out, nil
	}
	now := p.o.Clock.Now()
	var token string
	for _, ev := range events {
		if ev.Kind != EventPRMerged || ev.Table == "" {
			out.events = append(out.events, ev)
			continue
		}
		if token == "" {
			t, err := p.o.Token(ctx, repo)
			if err != nil {
				return nil, fmt.Errorf("github: no token to read %s: %w", repo, err)
			}
			token = t
		}
		var pull struct {
			MergeCommitSHA string `json:"merge_commit_sha"`
		}
		if err := p.getJSON(ctx, repo, token, fmt.Sprintf("/repos/%s/pulls/%d", repo, ev.Number), &pull); err != nil {
			return nil, err
		}
		ok := p.reachable(ctx, w, pull.MergeCommitSHA)
		if ok {
			out.events = append(out.events, ev)
			out.merged = append(out.merged, ev)
			continue
		}
		out.pending = append(out.pending, pendingMerge{Event: ev, SHA: pull.MergeCommitSHA})
		out.events = append(out.events, Event{Kind: EventWarning, Key: "aviso:" + ev.Key, Workspace: ev.Workspace, Repo: repo, Number: ev.Number, Table: ev.Table,
			Link: ev.Link, At: now, Summary: fmt.Sprintf("o PR #%d foi mesclado no GitHub, mas o commit ainda não está no espelho; a mesa não foi encerrada", ev.Number)})
	}
	// the ones that waited from before
	rows, err := p.o.DB.Reader().QueryContext(ctx, `SELECT number, table_id, sha, author, link, summary, since FROM github_pending_merges WHERE repo = ?`, repo)
	if err != nil {
		return nil, err
	}
	var waiting []pendingMerge
	var since []int64
	for rows.Next() {
		var pm pendingMerge
		var at int64
		pm.Repo, pm.Workspace, pm.Kind = repo, w.Workspace, EventPRMerged
		if err := rows.Scan(&pm.Number, &pm.Table, &pm.SHA, &pm.Author, &pm.Link, &pm.Summary, &at); err != nil {
			_ = rows.Close()
			return nil, err
		}
		waiting = append(waiting, pm)
		since = append(since, at)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i, pm := range waiting {
		if pm.SHA == "" { // GitHub had not said the merge commit yet
			if token == "" {
				t, err := p.o.Token(ctx, repo)
				if err != nil {
					return nil, fmt.Errorf("github: no token to read %s: %w", repo, err)
				}
				token = t
			}
			var pull struct {
				MergeCommitSHA string `json:"merge_commit_sha"`
			}
			if err := p.getJSON(ctx, repo, token, fmt.Sprintf("/repos/%s/pulls/%d", repo, pm.Number), &pull); err != nil {
				if isLimit(err) {
					return nil, err
				}
				p.o.Logger.Warn("github: the merge commit of a PR was not read", "workspace", w.Workspace, "pr", pm.Number, "err", err)
			} else if pull.MergeCommitSHA != "" {
				pm.SHA = pull.MergeCommitSHA
				out.pending = append(out.pending, pendingMerge{Event: pm.Event, SHA: pm.SHA, update: true, since: time.UnixMilli(since[i])})
			}
		}
		ok := p.reachable(ctx, w, pm.SHA)
		switch {
		case ok:
			pm.Key, pm.At = fmt.Sprintf("merge-confirmado:%s:%d:%d", repo, pm.Number, since[i]), now
			out.events = append(out.events, pm.Event)
			out.merged = append(out.merged, pm.Event)
			out.resolved = append(out.resolved, pm.Number)
		case now.Sub(time.UnixMilli(since[i])) >= MergeWait:
			out.events = append(out.events, Event{Kind: EventAlert, Key: fmt.Sprintf("alerta-merge:%s:%d:%d", repo, pm.Number, since[i]), Workspace: w.Workspace, Repo: repo,
				Number: pm.Number, Table: pm.Table, Link: pm.Link, At: now,
				Summary: fmt.Sprintf("o commit do merge do PR #%d não chegou ao espelho em 1 hora; a mesa não foi encerrada e não será mais conferida", pm.Number)})
			out.resolved = append(out.resolved, pm.Number)
		}
	}
	return out, nil
}
