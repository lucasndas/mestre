package chain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/store"
)

// Store is the approval_requests, approval_audit, approval_breaker and
// approval_suggestions tables of the Guild database.
type Store struct{ DB *store.DB }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

// AuditEntry is one row of the audit: a decision, accepted or refused, or a
// climb (docs/orquestracao/cadeia-de-aprovacao.md#salvaguardas, item 4).
type AuditEntry struct {
	ID       int64
	Request  int64
	At       time.Time
	Approver string
	Level    LevelKind
	Answer   Answer
	// Outcome is accepted, refused or climbed.
	Outcome string
	// Refusal is the code of a refused decision.
	Refusal string
	Reason  string
	// Risk and the versions that held.
	Risk             string
	RiskTableVersion int
	RulesVersion     int
	PolicyVersion    string
}

const requestColumns = `id, workspace, table_id, type, state, requester, fingerprint, level_kind, level_agent, deadline, body, created_at, closed_at`

type scanner interface{ Scan(...any) error }

func scanRequest(sc scanner) (Request, error) {
	var (
		id                         int64
		ws, table                  string
		requester                  string
		typ, state, kind, agent    string
		fp, body                   string
		deadline, created, closedA int64
	)
	if err := sc.Scan(&id, &ws, &table, &typ, &state, &requester, &fp, &kind, &agent, &deadline, &body, &created, &closedA); err != nil {
		return Request{}, err
	}
	var r Request
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return Request{}, fmt.Errorf("request %d: %w", id, err)
	}
	// The columns are the truth for what they hold; the body has the rest.
	r.ID, r.Workspace, r.Table, r.Type, r.State, r.Requester, r.Fingerprint = id, ws, table, classes.RequestType(typ), State(state), requester, fp
	r.Level = Level{Kind: LevelKind(kind), Agent: agent, Name: r.Level.Name}
	r.Deadline, r.CreatedAt, r.ClosedAt = fromMs(deadline), fromMs(created), fromMs(closedA)
	return r, nil
}

func (r Request) body() (string, error) {
	b, err := json.Marshal(r)
	return string(b), err
}

// Insert registers a request and gives it its number.
func (s *Store) Insert(ctx context.Context, r Request) (Request, error) {
	body, err := r.body()
	if err != nil {
		return Request{}, err
	}
	err = s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO approval_requests (workspace, table_id, type, state, requester, fingerprint, level_kind, level_agent, deadline, body, created_at, closed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`, r.Workspace, r.Table, string(r.Type), string(r.State), r.Requester, r.Fingerprint, string(r.Level.Kind), r.Level.Agent,
			ms(r.Deadline), body, ms(r.CreatedAt))
		if err != nil {
			return err
		}
		r.ID, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return Request{}, fmt.Errorf("register a request of approval: %w", err)
	}
	return r, nil
}

// Save rewrites a request.
func (s *Store) Save(ctx context.Context, r Request) error {
	body, err := r.body()
	if err != nil {
		return err
	}
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE approval_requests SET state = ?, level_kind = ?, level_agent = ?, deadline = ?, body = ?, closed_at = ? WHERE id = ?`,
			string(r.State), string(r.Level.Kind), r.Level.Agent, ms(r.Deadline), body, ms(r.ClosedAt), r.ID)
		return err
	})
}

// Get returns a request (ErrNoRequest).
func (s *Store) Get(ctx context.Context, id int64) (Request, error) {
	r, err := scanRequest(s.DB.Reader().QueryRowContext(ctx, `SELECT `+requestColumns+` FROM approval_requests WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, fmt.Errorf("%w: #%d", ErrNoRequest, id)
	}
	return r, err
}

func (s *Store) list(ctx context.Context, where string, args ...any) ([]Request, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+requestColumns+` FROM approval_requests WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Request
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Open lists the open requests.
func (s *Store) Open(ctx context.Context) ([]Request, error) {
	return s.list(ctx, `state = 'open'`)
}

// OpenOf lists the open requests of a requester.
func (s *Store) OpenOf(ctx context.Context, requester string) ([]Request, error) {
	return s.list(ctx, `state = 'open' AND requester = ?`, requester)
}

// OpenAt lists the open requests at a level held by a character.
func (s *Store) OpenAt(ctx context.Context, agent string) ([]Request, error) {
	return s.list(ctx, `state = 'open' AND level_agent = ?`, agent)
}

// OpenOfTable lists the open requests of a table.
func (s *Store) OpenOfTable(ctx context.Context, workspace, table string) ([]Request, error) {
	return s.list(ctx, `state = 'open' AND workspace = ? AND table_id = ?`, workspace, table)
}

// FindOpen is the open request of a requester for exactly this action.
func (s *Store) FindOpen(ctx context.Context, requester, fingerprint string) (Request, bool, error) {
	rs, err := s.list(ctx, `state = 'open' AND requester = ? AND fingerprint = ? AND fingerprint != ''`, requester, fingerprint)
	if err != nil || len(rs) == 0 {
		return Request{}, false, err
	}
	return rs[0], true, nil
}

// ApprovedFor lists the approved requests of a requester for a fingerprint, the
// newest first.
func (s *Store) ApprovedFor(ctx context.Context, requester, fingerprint string) ([]Request, error) {
	rs, err := s.list(ctx, `state = 'approved' AND requester = ? AND fingerprint = ? AND fingerprint != ''`, requester, fingerprint)
	for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
		rs[i], rs[j] = rs[j], rs[i]
	}
	return rs, err
}

// Audit appends a row of the audit.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	body, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO approval_audit (request, at, approver, level_kind, answer, outcome, body) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			e.Request, ms(e.At), e.Approver, string(e.Level), string(e.Answer), e.Outcome, string(body))
		return err
	})
}

// AuditOf lists the audit of a request.
func (s *Store) AuditOf(ctx context.Context, request int64) ([]AuditEntry, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT id, body FROM approval_audit WHERE request = ? ORDER BY id`, request)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var (
			id   int64
			body string
			e    AuditEntry
		)
		if err := rows.Scan(&id, &body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			return nil, err
		}
		e.ID = id
		out = append(out, e)
	}
	return out, rows.Err()
}

// Breaker is the circuit breaker of a character.
type Breaker struct {
	Character string
	Denials   int
	Paused    bool
	PausedAt  time.Time
}

// Breaker returns the breaker of a character (zero when it has none).
func (s *Store) Breaker(ctx context.Context, character string) (Breaker, error) {
	b := Breaker{Character: character}
	var paused int
	var at int64
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT denials, paused, paused_at FROM approval_breaker WHERE character = ?`, character).Scan(&b.Denials, &paused, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	b.Paused, b.PausedAt = paused != 0, fromMs(at)
	return b, err
}

// SaveBreaker writes the breaker of a character.
func (s *Store) SaveBreaker(ctx context.Context, b Breaker) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		p := 0
		if b.Paused {
			p = 1
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO approval_breaker (character, denials, paused, paused_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (character) DO UPDATE SET denials = excluded.denials, paused = excluded.paused, paused_at = excluded.paused_at`,
			b.Character, b.Denials, p, ms(b.PausedAt))
		return err
	})
}

// Suggestion is what an agent could not decide and the summary of the Owner
// will show.
type Suggestion struct {
	ID      int64
	Request int64
	At      time.Time
	// Kind is approve_always or deny_always.
	Kind   Answer
	By     string
	Class  string
	Action string
	Reason string
}

// AddSuggestion records a suggestion.
func (s *Store) AddSuggestion(ctx context.Context, x Suggestion) error {
	body, err := json.Marshal(x)
	if err != nil {
		return err
	}
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO approval_suggestions (request, at, kind, by, body) VALUES (?, ?, ?, ?, ?)`, x.Request, ms(x.At), string(x.Kind), x.By, string(body))
		return err
	})
}

// Window lists the requests that are open or closed since the time, with the
// audit of each: what the queue metrics read the levels of a request from.
func (s *Store) Window(ctx context.Context, since time.Time) ([]Request, map[int64][]AuditEntry, error) {
	rs, err := s.list(ctx, `state = 'open' OR closed_at >= ?`, ms(since))
	if err != nil {
		return nil, nil, err
	}
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT a.id, a.body FROM approval_audit a JOIN approval_requests r ON r.id = a.request
		WHERE r.state = 'open' OR r.closed_at >= ? ORDER BY a.id`, ms(since))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	audit := map[int64][]AuditEntry{}
	for rows.Next() {
		var (
			id   int64
			body string
			e    AuditEntry
		)
		if err := rows.Scan(&id, &body); err != nil {
			return nil, nil, err
		}
		if err := json.Unmarshal([]byte(body), &e); err != nil {
			return nil, nil, err
		}
		e.ID = id
		audit[e.Request] = append(audit[e.Request], e)
	}
	return rs, audit, rows.Err()
}

// Suggestions lists them, oldest first.
func (s *Store) Suggestions(ctx context.Context) ([]Suggestion, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT id, body FROM approval_suggestions ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Suggestion
	for rows.Next() {
		var (
			id   int64
			body string
			x    Suggestion
		)
		if err := rows.Scan(&id, &body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &x); err != nil {
			return nil, err
		}
		x.ID = id
		out = append(out, x)
	}
	return out, rows.Err()
}
