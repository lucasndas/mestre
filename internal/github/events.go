package github

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The numbers of the periodic reading (docs/github/eventos.md#intervalo-adaptativo).
const (
	IntervalActive    = 30 * time.Second
	IntervalAttentive = 2 * time.Minute
	IntervalIdle      = 5 * time.Minute
	// ActiveHold is how long a change seen (or an action of the engine on
	// GitHub) keeps the workspace active.
	ActiveHold = 5 * time.Minute
	// Jitter is the random variation of each interval, so the workspaces do not
	// all ask together: ±10%.
	Jitter = 0.10
	// SeenLife is how long the key of a change is kept.
	SeenLife = 30 * 24 * time.Hour
	// pollMaxPages caps the pages read of a list in one cycle.
	pollMaxPages = 20
	perPage  = 100
)

// Mode is how often a workspace is read.
type Mode int

// The modes.
const (
	// ModeIdle: no table is open.
	ModeIdle Mode = iota
	// ModeAttentive: a table or a PR of a table is open.
	ModeAttentive
	// ModeActive: CI running on a PR of a table, a change seen or an action of
	// the engine in the last 5 minutes.
	ModeActive
)

// EventKind is the kind of an event of GitHub.
type EventKind string

// The kinds of event.
const (
	EventComment      EventKind = "comentario"
	EventReview       EventKind = "revisao"
	EventCI           EventKind = "ci"
	EventIssueChanged EventKind = "issue_alterada"
	EventPRChanged    EventKind = "pr_alterado"
	EventPRMerged     EventKind = "pr_mergeado"
	EventPRClosed     EventKind = "pr_fechado"
)

// Event is something that changed at GitHub, normalized.
type Event struct {
	Kind      EventKind
	Key       string
	Workspace string
	Repo      string
	Number    int
	// Table is the table the change belongs to ("": no table).
	Table   string
	Author  string
	Link    string
	Summary string
	At      time.Time
}

// Watch is a workspace read: its repository and the moment it was made, from
// which the cursor starts (the old history does not become events).
type Watch struct {
	Workspace, Repo string
	Since           time.Time
}

// Sink takes the events of a cycle, one batch for each table (Table "" for the
// ones with no table). It must be idempotent for the keys of the events: the
// cursor moves only after it answered.
type Sink interface {
	Deliver(ctx context.Context, w Watch, table string, events []Event) error
}

// Situation says how busy a workspace is.
type Situation interface {
	// Mode of the workspace from what is open (tables, PRs, CI), not counting the
	// time since the last change, which the poller knows.
	Mode(ctx context.Context, w Watch) Mode
	// TableOf is the table an issue or PR of the repository belongs to.
	TableOf(ctx context.Context, repo string, number int) string
}

// RetryAfterError is a limit of GitHub: nothing is asked before At.
type RetryAfterError struct {
	At     time.Time
	Status int
}

func (e *RetryAfterError) Error() string {
	return fmt.Sprintf("github: the reading was limited (%d); nothing is asked before %s", e.Status, e.At.Format(time.RFC3339))
}

// PollerOptions are the pieces of Poller.
type PollerOptions struct {
	DB    *store.DB
	Clock clock.Clock
	// API is the REST API; Token gives the token of the installation of the App
	// "Grão-Mestre" for the repository.
	API     string
	HTTP    *http.Client
	Token   func(ctx context.Context, repo string) (string, error)
	Objects *Objects
	Sink    Sink
	Situation
	// Watches are the workspaces to read.
	Watches func(ctx context.Context) ([]Watch, error)
	// Rand gives a number in [0, 1) for the jitter; nil: the middle (no jitter).
	Rand   func() float64
	Logger *slog.Logger
}

// Poller reads GitHub for what changed and turns it into events. Only the
// Citadel with the lease valid runs it, and it never changes anything at GitHub.
type Poller struct {
	o PollerOptions

	mu sync.Mutex
	by map[string]*watchState
}

type watchState struct {
	due        time.Time
	lastChange time.Time
	lastAction time.Time
	fails      int
}

// NewPoller returns the poller.
func NewPoller(o PollerOptions) (*Poller, error) {
	if o.DB == nil || o.Clock == nil || o.Token == nil || o.Sink == nil || o.Situation == nil || o.Watches == nil {
		return nil, errors.New("github: the poller needs the database, the clock, the token, the sink, the situation and the workspaces")
	}
	if o.HTTP == nil {
		o.HTTP = http.DefaultClient
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.API == "" {
		o.API = "https://api.github.com"
	}
	return &Poller{o: o, by: map[string]*watchState{}}, nil
}

func (p *Poller) state(workspace string) *watchState {
	st := p.by[workspace]
	if st == nil {
		st = &watchState{}
		p.by[workspace] = st
	}
	return st
}

// Acted tells the poller the engine did something at GitHub for the workspace:
// it is active for the next 5 minutes.
func (p *Poller) Acted(workspace string) {
	p.mu.Lock()
	p.state(workspace).lastAction = p.o.Clock.Now()
	p.mu.Unlock()
}

// Mode is how the workspace is read now.
func (p *Poller) Mode(ctx context.Context, w Watch) Mode {
	p.mu.Lock()
	st := p.state(w.Workspace)
	now := p.o.Clock.Now()
	recent := now.Sub(st.lastChange) < ActiveHold || now.Sub(st.lastAction) < ActiveHold
	p.mu.Unlock()
	if recent {
		return ModeActive
	}
	return p.o.Situation.Mode(ctx, w)
}

// Interval is the time to the next reading of the workspace, with the jitter.
func (p *Poller) Interval(ctx context.Context, w Watch) time.Duration {
	base := map[Mode]time.Duration{ModeActive: IntervalActive, ModeAttentive: IntervalAttentive, ModeIdle: IntervalIdle}[p.Mode(ctx, w)]
	if p.o.Rand == nil {
		return base
	}
	f := 1 + Jitter*(2*p.o.Rand()-1)
	return time.Duration(float64(base) * f)
}

// listSpec is a list the cycle reads: always the same address, for the 304.
type listSpec struct {
	name, path string
	comment    bool
}

var lists = []listSpec{
	{name: "issues", path: "/repos/%s/issues?state=all&sort=updated&direction=desc&per_page=100"},
	{name: "comments", path: "/repos/%s/issues/comments?sort=updated&direction=desc&per_page=100", comment: true},
	{name: "review_comments", path: "/repos/%s/pulls/comments?sort=updated&direction=desc&per_page=100", comment: true},
}

type pollRow struct {
	etag   string
	cursor time.Time
}

func (p *Poller) row(ctx context.Context, repo, list string, since time.Time) (pollRow, error) {
	var etag string
	var cur int64
	err := p.o.DB.Reader().QueryRowContext(ctx, `SELECT etag, cursor FROM github_poll_state WHERE repo = ? AND list = ?`, repo, list).Scan(&etag, &cur)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return pollRow{cursor: since}, nil
	case err != nil:
		return pollRow{}, err
	}
	return pollRow{etag: etag, cursor: time.UnixMilli(cur)}, nil
}

type fetched struct {
	spec   listSpec
	events []Event
	keys   []string
	etag   string
	cursor time.Time
}

type item struct {
	ID        int64     `json:"id"`
	Number    int       `json:"number"`
	Body      string    `json:"body"`
	Title     string    `json:"title"`
	State     string    `json:"state"`
	UpdatedAt time.Time `json:"updated_at"`
	HTMLURL   string    `json:"html_url"`
	IssueURL  string    `json:"issue_url"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	PullRequest *struct {
		MergedAt *time.Time `json:"merged_at"`
	} `json:"pull_request"`
}

// Cycle reads the lists of the workspace once. Nothing is recorded as seen and no
// cursor moves unless every list was read and the events were delivered: a failure
// leaves the same changes to the next cycle. changed says new events came.
func (p *Poller) Cycle(ctx context.Context, w Watch) (changed bool, err error) {
	repo, err := NormalizeRepo(w.Repo)
	if err != nil {
		return false, err
	}
	var got []fetched
	for _, spec := range lists {
		f, err := p.fetch(ctx, w, repo, spec)
		if err != nil {
			return false, err
		}
		got = append(got, f)
	}
	byTable := map[string][]Event{}
	var order []string
	for _, f := range got {
		for _, ev := range f.events {
			if _, ok := byTable[ev.Table]; !ok {
				order = append(order, ev.Table)
			}
			byTable[ev.Table] = append(byTable[ev.Table], ev)
			changed = true
		}
	}
	for _, table := range order {
		if err := p.o.Sink.Deliver(ctx, w, table, byTable[table]); err != nil {
			return false, fmt.Errorf("github: delivering the events of %s: %w", w.Workspace, err)
		}
	}
	now := p.o.Clock.Now()
	err = p.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		for _, f := range got {
			for _, k := range f.keys {
				if _, err := tx.ExecContext(ctx, `INSERT INTO github_seen_events (key, at) VALUES (?, ?) ON CONFLICT (key) DO NOTHING`, k, now.UnixMilli()); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO github_poll_state (repo, list, etag, cursor) VALUES (?, ?, ?, ?)
				ON CONFLICT (repo, list) DO UPDATE SET etag = excluded.etag, cursor = excluded.cursor`,
				repo, f.spec.name, f.etag, f.cursor.UnixMilli()); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM github_seen_events WHERE at < ?`, now.Add(-SeenLife).UnixMilli())
		return err
	})
	if err != nil {
		return false, err
	}
	if changed {
		p.mu.Lock()
		p.state(w.Workspace).lastChange = now
		p.mu.Unlock()
	}
	return changed, nil
}

func (p *Poller) seen(ctx context.Context, key string) (bool, error) {
	var one int
	err := p.o.DB.Reader().QueryRowContext(ctx, `SELECT 1 FROM github_seen_events WHERE key = ?`, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// fetch reads one list: the first page with the ETag of the last time (a 304
// ends it), and the next ones while every item of the page is newer than the
// cursor.
func (p *Poller) fetch(ctx context.Context, w Watch, repo string, spec listSpec) (fetched, error) {
	row, err := p.row(ctx, repo, spec.name, w.Since)
	if err != nil {
		return fetched{}, err
	}
	out := fetched{spec: spec, etag: row.etag, cursor: row.cursor}
	token, err := p.o.Token(ctx, repo)
	if err != nil {
		return fetched{}, fmt.Errorf("github: no token to read %s: %w", repo, err)
	}
	local := map[string]bool{}
	for page := 1; page <= pollMaxPages; page++ {
		path := fmt.Sprintf(spec.path, repo)
		if page > 1 {
			path += "&page=" + strconv.Itoa(page)
		}
		etag := ""
		if page == 1 {
			etag = row.etag
		}
		status, hdr, raw, err := p.get(ctx, path, token, etag)
		if err != nil {
			return fetched{}, err
		}
		switch {
		case status == http.StatusNotModified:
			return out, nil
		case status == http.StatusTooManyRequests || status == http.StatusForbidden:
			return fetched{}, p.limited(status, hdr)
		case status != http.StatusOK:
			return fetched{}, fmt.Errorf("github: reading %s: status %d", spec.name, status)
		}
		if page == 1 {
			out.etag = hdr.Get("ETag")
		}
		var items []item
		if err := json.Unmarshal(raw, &items); err != nil {
			return fetched{}, fmt.Errorf("github: the list %s is not readable", spec.name)
		}
		allNewer := len(items) == perPage
		for _, it := range items {
			if it.UpdatedAt.Before(row.cursor) {
				allNewer = false
				continue
			}
			if it.UpdatedAt.After(out.cursor) {
				out.cursor = it.UpdatedAt
			}
			ev, ok, err := p.eventOf(ctx, w, repo, spec, it)
			if err != nil {
				return fetched{}, err
			}
			if !ok || local[ev.Key] {
				continue
			}
			local[ev.Key] = true
			if seen, err := p.seen(ctx, ev.Key); err != nil {
				return fetched{}, err
			} else if seen {
				continue
			}
			out.keys = append(out.keys, ev.Key)
			out.events = append(out.events, ev)
		}
		if !allNewer {
			return out, nil
		}
	}
	return out, nil
}

// get is a GET with the token, and If-None-Match when there is an ETag.
func (p *Poller) get(ctx context.Context, path, token, etag string) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.o.API, "/")+path, nil)
	if err != nil {
		return 0, nil, nil, errors.New("github: bad request")
	}
	req.Header.Set("Authorization", "Bearer "+token) // also on the 304: only so it is free
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := p.o.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, nil, nil, fmt.Errorf("github: reading GitHub: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, resp.Header, raw, nil
}

// limited is the error of a 403 or 429: retry-after, or x-ratelimit-reset, says
// when to ask again. A 403 that is no limit is a plain refusal.
func (p *Poller) limited(status int, h http.Header) error {
	now := p.o.Clock.Now()
	if v, err := strconv.Atoi(h.Get("Retry-After")); err == nil && v >= 0 {
		return &RetryAfterError{At: now.Add(time.Duration(v) * time.Second), Status: status}
	}
	if h.Get("x-ratelimit-remaining") == "0" {
		if v, err := strconv.ParseInt(h.Get("x-ratelimit-reset"), 10, 64); err == nil {
			return &RetryAfterError{At: time.Unix(v, 0), Status: status}
		}
	}
	if status == http.StatusTooManyRequests {
		return &RetryAfterError{At: now.Add(time.Minute), Status: status}
	}
	return fmt.Errorf("github: reading was refused (%d)", status)
}

// eventOf turns an item into an event; ok is false for what is not an event
// (the echo of the engine).
func (p *Poller) eventOf(ctx context.Context, w Watch, repo string, spec listSpec, it item) (Event, bool, error) {
	stamp := it.UpdatedAt.UTC().Format(time.RFC3339)
	ev := Event{Workspace: w.Workspace, Repo: repo, Author: it.User.Login, Link: it.HTMLURL, At: it.UpdatedAt}
	if spec.comment {
		// what the Guild wrote is no external event: its marker, or its own record of the comment
		if len(Markers(it.Body)) > 0 {
			return Event{}, false, nil
		}
		if p.o.Objects != nil {
			if _, ok, err := p.o.Objects.Get(ctx, repo, KindComment, int(it.ID)); err != nil {
				return Event{}, false, err
			} else if ok {
				return Event{}, false, nil
			}
		}
		ev.Kind, ev.Key = EventComment, fmt.Sprintf("comentario:%d:%s", it.ID, stamp)
		ev.Number = numberOf(it.IssueURL, it.HTMLURL)
		ev.Summary = shorten(it.Body)
		ev.Table = p.o.Situation.TableOf(ctx, repo, ev.Number)
		return ev, true, nil
	}
	if len(Markers(it.Body)) > 0 {
		return Event{}, false, nil
	}
	ev.Number = it.Number
	ev.Key = fmt.Sprintf("issue:%d:%s", it.ID, stamp)
	ev.Summary = shorten(it.Title)
	switch {
	case it.PullRequest == nil:
		ev.Kind = EventIssueChanged
	case it.PullRequest.MergedAt != nil:
		ev.Kind = EventPRMerged
	case it.State == "closed":
		ev.Kind = EventPRClosed
	default:
		ev.Kind = EventPRChanged
	}
	ev.Table = p.o.Situation.TableOf(ctx, repo, ev.Number)
	return ev, true, nil
}

// numberOf finds the number of the issue of a comment from its addresses.
func numberOf(issueURL, htmlURL string) int {
	if i := strings.LastIndexByte(issueURL, '/'); i >= 0 {
		if n, err := strconv.Atoi(issueURL[i+1:]); err == nil {
			return n
		}
	}
	// .../issues/12#issuecomment-5 or .../pull/12#discussion_r5
	h := htmlURL
	if i := strings.IndexByte(h, '#'); i >= 0 {
		h = h[:i]
	}
	if i := strings.LastIndexByte(h, '/'); i >= 0 {
		if n, err := strconv.Atoi(h[i+1:]); err == nil {
			return n
		}
	}
	return 0
}

// shorten is the first line of a text, short enough for a summary.
func shorten(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

// Tick reads the workspaces that are due: after a limit nothing is asked before
// the time GitHub said, after another error the interval grows up to the idle one.
func (p *Poller) Tick(ctx context.Context) error {
	ws, err := p.o.Watches(ctx)
	if err != nil {
		return err
	}
	now := p.o.Clock.Now()
	for _, w := range ws {
		p.mu.Lock()
		st := p.state(w.Workspace)
		due := st.due
		p.mu.Unlock()
		if now.Before(due) {
			continue
		}
		_, err := p.Cycle(ctx, w)
		p.mu.Lock()
		st = p.state(w.Workspace)
		switch {
		case err == nil:
			st.fails = 0
			st.due = now.Add(p.interval(ctx, w, st))
		default:
			var lim *RetryAfterError
			if errors.As(err, &lim) {
				st.due = lim.At
			} else {
				st.fails++
				back := IntervalActive << min(st.fails, 4)
				st.due = now.Add(min(back, IntervalIdle))
				p.o.Logger.Warn("github: reading a workspace failed", "workspace", w.Workspace, "err", err)
			}
		}
		p.mu.Unlock()
	}
	return nil
}

// interval is Interval with the lock held by the caller.
func (p *Poller) interval(ctx context.Context, w Watch, st *watchState) time.Duration {
	now := p.o.Clock.Now()
	mode := p.o.Situation.Mode(ctx, w)
	if now.Sub(st.lastChange) < ActiveHold || now.Sub(st.lastAction) < ActiveHold {
		mode = ModeActive
	}
	base := map[Mode]time.Duration{ModeActive: IntervalActive, ModeAttentive: IntervalAttentive, ModeIdle: IntervalIdle}[mode]
	if p.o.Rand == nil {
		return base
	}
	return time.Duration(float64(base) * (1 + Jitter*(2*p.o.Rand()-1)))
}

// Run does Tick every second until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	t := p.o.Clock.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			if err := p.Tick(ctx); err != nil && ctx.Err() == nil {
				p.o.Logger.Warn("github: reading the workspaces failed", "err", err)
			}
		}
	}
}
