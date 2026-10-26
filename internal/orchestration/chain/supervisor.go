package chain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The errors of the supervision.
var (
	// ErrLevelDenied: who asks does not have the level for it.
	ErrLevelDenied = errors.New("who asks does not have the level for this")
	// ErrNoTable: no such table.
	ErrNoTable = errors.New("no such table")
	// ErrTableOver: the table ended or is ending.
	ErrTableOver = errors.New("the table is over")
	// ErrCharacterPause: the pause is the Grão-Mestre's and who asks may not resume it
	// (PauseRequest.RefuseCharacterPause).
	ErrCharacterPause = errors.New("the pause of the Grão-Mestre is resumed by the Grão-Mestre")
	// ErrReasonRequired: a pause or a resume says why.
	ErrReasonRequired = errors.New("a reason is required")
)

// TableStates tells whether a table ended or is ending (ErrNoTable when there
// is none).
type TableStates interface {
	Over(ctx context.Context, workspace, table string) (bool, error)
}

// Rerouter takes the requests of a paused table off the Mestre da mesa
// (Service.RerouteTable).
type Rerouter interface {
	RerouteTable(ctx context.Context, workspace, table, why string) error
}

// RuleLister gives the temporary table rules a request made, for the summary.
type RuleLister interface {
	RulesOfRequest(ctx context.Context, workspace, table string, request int64) ([]int64, error)
}

// Rand is the source of randomness of the sample, injected so a test fixes it
// (*math/rand.Rand has Perm).
type Rand interface{ Perm(n int) []int }

// Actor is who pauses or resumes: a character (the Grão-Mestre) or a person
// with the level the Telegram or the panel conferred (milestones 5 and 6).
type Actor struct {
	Character *characters.Character
	User      string
	UserLevel characters.UserLevel
}

func (a Actor) name() string {
	if a.Character != nil {
		return a.Character.ID
	}
	return "user:" + a.User
}

func (a Actor) kind() string {
	if a.Character != nil {
		return "character"
	}
	return "person"
}

// approverOrAbove says a person has the level of an Approver or above.
func (a Actor) approverOrAbove() bool {
	if a.Character != nil {
		return false
	}
	switch a.UserLevel {
	case characters.LevelApprover, characters.LevelAdministrator, characters.LevelOwner:
		return true
	}
	return false
}

// Pause is the pause of the autonomy of a table.
type Pause struct {
	Workspace, Table string
	By               string
	// Kind is character or person.
	Kind   string
	Reason string
	Since  time.Time
}

// PauseEntry is a line of the audit of the pauses.
type PauseEntry struct {
	At     time.Time
	Action string // pause or resume
	By     string
	Kind   string
	Reason string
}

// DecisionRecord is a decision of an agent of the chain as the supervision
// keeps it.
type DecisionRecord struct {
	ID               int64
	Workspace, Table string
	Request          int64
	At               time.Time
	Approver         string
	Level            LevelKind
	Answer           Answer
	Risk             string
	Reason           string
	// About is the request as the engine put it.
	About string
}

// DecisionSource is what the digest of the Owner reads (milestone 5): the
// decisions of the agents of a level in a period, and the random sample of
// them. The Supervisor is the source.
type DecisionSource interface {
	Decisions(ctx context.Context, workspace string, level LevelKind, since, until time.Time) ([]DecisionRecord, error)
	Sample(ctx context.Context, workspace string, level LevelKind, since, until time.Time, n int, rnd Rand) ([]DecisionRecord, error)
}

// QueueMetric is the queue of one approver level: a Mestre da mesa (per
// table), the Grão-Mestre or the people.
type QueueMetric struct {
	Workspace string
	Level     LevelKind
	// Table is set for the levels of a table (agent, mesa).
	Table string
	// Open is how many requests are open at the level now, Oldest how long
	// the oldest has been there, and AvgWait the mean time requests spent at
	// the level over the last 24 h (Waited requests).
	Open    int
	Oldest  time.Duration
	AvgWait time.Duration
	Waited  int
}

// SupervisorOptions are the pieces of the Supervisor. Store, Characters,
// Inbox and Policy are required; the rest may be nil: nobody is told above the
// Grão-Mestre, any table may be paused, nothing is taken off the Mestre da
// mesa (the next requests are born above it), the summary names no rule.
type SupervisorOptions struct {
	Store      *Store
	Clock      clock.Clock
	Characters Characters
	Inbox      Inbox
	Policy     Policy

	Alerts  Alerts
	Tables  TableStates
	Reroute Rerouter
	Rules   RuleLister
	Logger  *slog.Logger

	// StallAfter is how long a table at work may deliver nothing before the
	// warning (DefaultStallAfter); StallPeople gets that warning with the
	// button to pause the table (nil: only the Grão-Mestre is told). Tables
	// must be a TableStalls for the warning to exist.
	StallAfter  time.Duration
	StallPeople StallPeople
	// QueuePeople gets the warning of a queue of a table that accumulates,
	// with the button to pause the autonomy of the table (nil: only the
	// Grão-Mestre is told).
	QueuePeople QueuePeople
}

// Supervisor is the supervision of the chain
// (docs/orquestracao/supervisao-da-cadeia.md): it tells whether the autonomy
// of a table is paused, keeps the decisions of the agents, sends the summary of
// the Mestres de mesa to the Grão-Mestre, measures the queues and warns when
// one accumulates.
type Supervisor struct {
	o   SupervisorOptions
	st  *Store
	clk clock.Clock
	log *slog.Logger

	mu   sync.Mutex // Tick, Pause and Resume one at a time
	mask *logging.Masker
	pol  policyReader
}

var (
	_ Supervision    = (*Supervisor)(nil)
	_ DecisionSource = (*Supervisor)(nil)
)

// NewSupervisor returns the Supervisor.
func NewSupervisor(o SupervisorOptions) (*Supervisor, error) {
	switch {
	case o.Store == nil || o.Store.DB == nil:
		return nil, errors.New("chain: the supervision needs the store")
	case o.Characters == nil, o.Inbox == nil, o.Policy == nil:
		return nil, errors.New("chain: the supervision needs the characters, the inbox and the policy")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Supervisor{o: o, st: o.Store, clk: o.Clock, log: o.Logger, mask: logging.NewMasker()}, nil
}

func (s *Supervisor) settings(ctx context.Context, workspace string) Settings {
	st, _ := s.pol.read(ctx, s.o.Policy, s.log, workspace)
	return st
}

// --- pause ---

// AutonomyPaused implements Supervision.
func (s *Supervisor) AutonomyPaused(ctx context.Context, workspace, table string) (bool, error) {
	var n int
	err := s.st.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM supervision_pauses WHERE workspace = ? AND table_id = ?`, workspace, table).Scan(&n)
	return n > 0, err
}

// PauseOf returns the pause of a table, if there is one.
func (s *Supervisor) PauseOf(ctx context.Context, workspace, table string) (Pause, bool, error) {
	var (
		p     Pause
		since int64
	)
	err := s.st.DB.Reader().QueryRowContext(ctx, `SELECT workspace, table_id, by, by_kind, reason, since FROM supervision_pauses WHERE workspace = ? AND table_id = ?`,
		workspace, table).Scan(&p.Workspace, &p.Table, &p.By, &p.Kind, &p.Reason, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return Pause{}, false, nil
	}
	p.Since = fromMs(since)
	return p, err == nil, err
}

// PauseAudit lists the pauses and resumes of a table, oldest first.
func (s *Supervisor) PauseAudit(ctx context.Context, workspace, table string) ([]PauseEntry, error) {
	rows, err := s.st.DB.Reader().QueryContext(ctx, `SELECT at, action, by, by_kind, reason FROM supervision_audit WHERE workspace = ? AND table_id = ? ORDER BY id`, workspace, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PauseEntry
	for rows.Next() {
		var (
			e  PauseEntry
			at int64
		)
		if err := rows.Scan(&at, &e.Action, &e.By, &e.Kind, &e.Reason); err != nil {
			return nil, err
		}
		e.At = fromMs(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// PauseRequest is a pause or a resume.
type PauseRequest struct {
	Workspace, Table string
	Reason           string
	By               Actor
	// RefuseCharacterPause makes a resume by a person refuse the pause of a character (the
	// Grão-Mestre), checked under the same lock that reads the pause: the panel
	// sets it, since there only the Grão-Mestre resumes its own pause.
	RefuseCharacterPause bool
}

// PauseAutonomy pauses the autonomy of a table: the Mestre da mesa stops
// deciding its medio, also through its temporary rules, the open medio
// requests go up to the Grão-Mestre with the same id, and the next ones are
// born there. Pausing a paused table succeeds without effect. Only the
// Grão-Mestre of the workspace and an Approver or above pause; a table that
// ended refuses.
func (s *Supervisor) PauseAutonomy(ctx context.Context, r PauseRequest) (bool, error) {
	if err := s.allowed(ctx, r); err != nil {
		return false, err
	}
	s.mu.Lock()
	if _, paused, err := s.PauseOf(ctx, r.Workspace, r.Table); err != nil || paused {
		s.mu.Unlock()
		return false, err
	}
	now := s.clk.Now()
	err := s.st.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO supervision_pauses (workspace, table_id, by, by_kind, reason, since) VALUES (?, ?, ?, ?, ?, ?)`,
			r.Workspace, r.Table, r.By.name(), r.By.kind(), r.Reason, ms(now)); err != nil {
			return err
		}
		return s.auditTx(ctx, tx, r, "pause", now)
	})
	s.mu.Unlock()
	if err != nil {
		return false, err
	}
	// The pause holds from here: the requests the Mestre da mesa had that it may
	// not decide anymore go up at once.
	if s.o.Reroute != nil {
		if err := s.o.Reroute.RerouteTable(ctx, r.Workspace, r.Table, "a autonomia da mesa foi pausada"); err != nil {
			s.log.Warn("supervision: the requests of the table were not sent up", "table", r.Table, "err", err)
		}
	}
	s.tellPause(ctx, r, fmt.Sprintf("A autonomia da mesa %s foi pausada por %s: %s. Seus pedidos de risco medio vão ao Grão-Mestre até a retomada.", r.Table, s.who(ctx, r.By), r.Reason))
	return true, nil
}

// ResumeAutonomy ends the pause of a table. Resuming what is not paused
// succeeds without effect. The pause of the Grão-Mestre is resumed by him or by
// an Approver or above; the pause of a person only by an Approver or above.
func (s *Supervisor) ResumeAutonomy(ctx context.Context, r PauseRequest) (bool, error) {
	if err := s.allowed(ctx, r); err != nil {
		return false, err
	}
	s.mu.Lock()
	p, paused, err := s.PauseOf(ctx, r.Workspace, r.Table)
	if err != nil || !paused {
		s.mu.Unlock()
		return false, err
	}
	if r.RefuseCharacterPause && r.By.Character == nil && p.Kind == "character" {
		s.mu.Unlock()
		return false, ErrCharacterPause
	}
	if p.Kind == "person" && !r.By.approverOrAbove() {
		s.mu.Unlock()
		return false, fmt.Errorf("%w: the pause of a person is resumed by an Approver or above", ErrLevelDenied)
	}
	now := s.clk.Now()
	err = s.st.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM supervision_pauses WHERE workspace = ? AND table_id = ?`, r.Workspace, r.Table); err != nil {
			return err
		}
		return s.auditTx(ctx, tx, r, "resume", now)
	})
	s.mu.Unlock()
	if err != nil {
		return false, err
	}
	s.tellPause(ctx, r, fmt.Sprintf("A autonomia da mesa %s foi retomada por %s: %s.", r.Table, s.who(ctx, r.By), r.Reason))
	return true, nil
}

// allowed checks the level, the reason and the table.
func (s *Supervisor) allowed(ctx context.Context, r PauseRequest) error {
	switch c := r.By.Character; {
	case c != nil:
		if c.Role != characters.RoleGrandMaster || c.Workspace != r.Workspace {
			return fmt.Errorf("%w: only the Grão-Mestre of the workspace", ErrLevelDenied)
		}
	case !r.By.approverOrAbove():
		return fmt.Errorf("%w: an Approver or above", ErrLevelDenied)
	}
	if strings.TrimSpace(r.Reason) == "" {
		return ErrReasonRequired
	}
	if s.o.Tables != nil {
		over, err := s.o.Tables.Over(ctx, r.Workspace, r.Table)
		switch {
		case err != nil:
			return err
		case over:
			return fmt.Errorf("%w: %s", ErrTableOver, r.Table)
		}
	}
	return nil
}

func (s *Supervisor) auditTx(ctx context.Context, tx *sql.Tx, r PauseRequest, action string, at time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO supervision_audit (workspace, table_id, at, action, by, by_kind, reason) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.Workspace, r.Table, ms(at), action, r.By.name(), r.By.kind(), r.Reason)
	return err
}

func (s *Supervisor) who(ctx context.Context, a Actor) string {
	if a.Character != nil {
		return a.Character.Name
	}
	return a.User
}

// tellPause tells the Mestre da mesa, and the Grão-Mestre when a person did it.
func (s *Supervisor) tellPause(ctx context.Context, r PauseRequest, text string) {
	var to []characters.Character
	if m, ok, err := s.o.Characters.TableMaster(ctx, r.Workspace, r.Table); err == nil && ok {
		to = append(to, m)
	}
	if r.By.Character == nil {
		if gm, ok, err := s.o.Characters.GrandMaster(ctx, r.Workspace); err == nil && ok {
			to = append(to, gm)
		}
	}
	for _, c := range to {
		if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: r.Workspace, Target: c.ID, Kind: inbox.KindSystem, Sender: "Motor", Text: text,
			Refs: inbox.Refs{Table: r.Table}}); err != nil {
			s.log.Warn("supervision: the pause was not told", "to", c.ID, "err", err)
		}
	}
}

// Status is what `mestre table status` and the canvas show of a table: whether
// its autonomy is paused and the line of its queue.
func (s *Supervisor) Status(ctx context.Context, workspace, table string) (paused bool, reason, queue string, err error) {
	p, paused, err := s.PauseOf(ctx, workspace, table)
	if err != nil {
		return false, "", "", err
	}
	mets, err := s.Metrics(ctx, workspace)
	if err != nil {
		return paused, p.Reason, "", err
	}
	for _, m := range mets {
		if m.Table == table && m.Level == LevelMesa {
			queue = fmt.Sprintf("fila do Mestre: %d abertos, o mais antigo há %s, espera média %s", m.Open, short(m.Oldest), short(m.AvgWait))
		}
	}
	return paused, p.Reason, queue, nil
}

// --- decisions ---

// Decided implements Supervision: it keeps the decision of an agent of the
// chain for the summary and the sample.
func (s *Supervisor) Decided(ctx context.Context, d AuditEntry) error {
	if d.Level != LevelMesa && d.Level != LevelGrandMaster {
		return nil
	}
	r, err := s.st.Get(ctx, d.Request)
	if err != nil {
		return err
	}
	delivered := 0
	if d.Level != LevelMesa {
		delivered = 1 // the summary is about the Mestres de mesa
	}
	about := strings.Join(strings.Fields(strings.ReplaceAll(describe(r), "\n", " · ")), " ")
	return s.st.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO supervision_decisions (workspace, table_id, request, at, approver, level, answer, risk, reason, about, delivered)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.Workspace, r.Table, d.Request, ms(d.At), d.Approver, string(d.Level), string(d.Answer), d.Risk, d.Reason, about, delivered)
		return err
	})
}

const decisionColumns = `id, workspace, table_id, request, at, approver, level, answer, risk, reason, about`

func scanDecision(sc scanner) (DecisionRecord, error) {
	var (
		d          DecisionRecord
		at         int64
		level, ans string
	)
	if err := sc.Scan(&d.ID, &d.Workspace, &d.Table, &d.Request, &at, &d.Approver, &level, &ans, &d.Risk, &d.Reason, &d.About); err != nil {
		return DecisionRecord{}, err
	}
	d.At, d.Level, d.Answer = fromMs(at), LevelKind(level), Answer(ans)
	return d, nil
}

func (s *Supervisor) decisions(ctx context.Context, where string, args ...any) ([]DecisionRecord, error) {
	rows, err := s.st.DB.Reader().QueryContext(ctx, `SELECT `+decisionColumns+` FROM supervision_decisions WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DecisionRecord
	for rows.Next() {
		d, err := scanDecision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Decisions implements DecisionSource: the decisions of the agents of a level
// in a workspace, from since (included) to until (excluded).
func (s *Supervisor) Decisions(ctx context.Context, workspace string, level LevelKind, since, until time.Time) ([]DecisionRecord, error) {
	return s.decisions(ctx, `workspace = ? AND level = ? AND at >= ? AND at < ?`, workspace, string(level), ms(since), ms(until))
}

// Sample implements DecisionSource: n of the decisions, drawn with rnd (all of
// them when there are no more than n), in the order they were made.
func (s *Supervisor) Sample(ctx context.Context, workspace string, level LevelKind, since, until time.Time, n int, rnd Rand) ([]DecisionRecord, error) {
	all, err := s.Decisions(ctx, workspace, level, since, until)
	if err != nil || len(all) <= n || n <= 0 {
		return all, err
	}
	picked := rnd.Perm(len(all))[:n]
	sort.Ints(picked)
	out := make([]DecisionRecord, 0, n)
	for _, i := range picked {
		out = append(out, all[i])
	}
	return out, nil
}

// --- the loop ---

// Tick does what is due: the summary to the Grão-Mestre of each workspace and
// the alerts of the queues.
func (s *Supervisor) Tick(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.summaries(ctx), s.alerts(ctx), s.stalls(ctx))
}

// Loop runs Tick every so often until the context ends.
func (s *Supervisor) Loop(ctx context.Context, every time.Duration) error {
	t := s.clk.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C():
			if err := s.Tick(ctx); err != nil {
				s.log.Warn("supervision: tick", "err", err)
			}
		}
	}
}
