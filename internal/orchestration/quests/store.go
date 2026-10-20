package quests

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/store"
)

// Store is the quests, quest_steps and step_runs tables of the Guild database.
// Every change of the state of a quest is one transaction (Do).
type Store struct{ DB *store.DB }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixMilli()
}

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// querier is what a reader and a transaction have in common.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type scanner interface{ Scan(...any) error }

const questColumns = `id, workspace, table_id, name, state, paused_reason, definition, max_returns, input, round, designer, scroll,
	issue, issue_dirty, round_commented, issue_closed, created_at, updated_at`

func scanQuest(sc scanner) (Quest, error) {
	var (
		q                Quest
		state, def       string
		dirty, closed    int
		created, updated int64
	)
	if err := sc.Scan(&q.ID, &q.Workspace, &q.Table, &q.Name, &state, &q.PausedReason, &def, &q.MaxReturns, &q.Input, &q.Round, &q.Designer,
		&q.Scroll, &q.Issue, &dirty, &q.RoundCommented, &closed, &created, &updated); err != nil {
		return Quest{}, err
	}
	q.State, q.Dirty, q.IssueClosed = State(state), dirty, closed != 0
	q.CreatedAt, q.UpdatedAt = fromMs(created), fromMs(updated)
	if err := json.Unmarshal([]byte(def), &q.Def); err != nil {
		return Quest{}, fmt.Errorf("quest %s: definition: %w", q.ID, err)
	}
	return q, nil
}

const stepColumns = `uid, quest, id, pos, state, character, returns, entry, blocker, approved, approval_request, resume_note,
	discard_result, stuck_at, escalated, sub_issue, sub_closed, commented_round, updated_at`

func scanStep(sc scanner) (Step, error) {
	var (
		st                                      Step
		state                                   string
		approved, discard, escalated, subClosed int
		stuck, updated                          int64
	)
	if err := sc.Scan(&st.UID, &st.Quest, &st.ID, &st.Pos, &state, &st.Character, &st.Returns, &st.Entry, &st.Blocker, &approved, &st.Approval,
		&st.ResumeNote, &discard, &stuck, &escalated, &st.SubIssue, &subClosed, &st.CommentedRound, &updated); err != nil {
		return Step{}, err
	}
	st.State, st.Approved, st.DiscardResult, st.Escalated, st.SubClosed = StepState(state), approved != 0, discard != 0, escalated != 0, subClosed != 0
	st.StuckAt, st.UpdatedAt = fromMs(stuck), fromMs(updated)
	return st, nil
}

const runColumns = `id, step, quest, round, attempt, character, character_name, class, start_commit, end_commit, started_at, ended_at,
	outcome, summary, files, uncommitted, warnings`

func scanRun(sc scanner) (Run, error) {
	var (
		r                         Run
		started, ended            int64
		files, uncommitted, warns string
	)
	if err := sc.Scan(&r.ID, &r.Step, &r.Quest, &r.Round, &r.Attempt, &r.Character, &r.CharacterName, &r.Class, &r.StartCommit, &r.EndCommit,
		&started, &ended, &r.Outcome, &r.Summary, &files, &uncommitted, &warns); err != nil {
		return Run{}, err
	}
	r.StartedAt, r.EndedAt = fromMs(started), fromMs(ended)
	for _, f := range []struct {
		raw string
		to  *[]string
	}{{files, &r.Files}, {uncommitted, &r.Uncommitted}, {warns, &r.Warnings}} {
		if f.raw != "" {
			if err := json.Unmarshal([]byte(f.raw), f.to); err != nil {
				return Run{}, fmt.Errorf("run %d: %w", r.ID, err)
			}
		}
	}
	return r, nil
}

func jsonList(l []string) string {
	if len(l) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(l)
	return string(b)
}

func getQuest(ctx context.Context, q querier, id string) (Quest, error) {
	quest, err := scanQuest(q.QueryRowContext(ctx, `SELECT `+questColumns+` FROM quests WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Quest{}, fmt.Errorf("%w: %s", ErrNoQuest, id)
	}
	return quest, err
}

func listQuests(ctx context.Context, q querier, where string, args ...any) ([]Quest, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+questColumns+` FROM quests WHERE `+where+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Quest
	for rows.Next() {
		quest, err := scanQuest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, quest)
	}
	return out, rows.Err()
}

func getStep(ctx context.Context, q querier, uid string) (Step, error) {
	st, err := scanStep(q.QueryRowContext(ctx, `SELECT `+stepColumns+` FROM quest_steps WHERE uid = ?`, uid))
	if errors.Is(err, sql.ErrNoRows) {
		return Step{}, fmt.Errorf("%w: step %s", ErrNoQuest, uid)
	}
	return st, err
}

func listSteps(ctx context.Context, q querier, where string, args ...any) ([]Step, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+stepColumns+` FROM quest_steps WHERE `+where+` ORDER BY quest, pos`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		st, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func listRuns(ctx context.Context, q querier, where string, args ...any) ([]Run, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+runColumns+` FROM step_runs WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Quest returns a quest (ErrNoQuest).
func (s *Store) Quest(ctx context.Context, id string) (Quest, error) {
	return getQuest(ctx, s.DB.Reader(), id)
}

// ByName finds a quest of a table by its name.
func (s *Store) ByName(ctx context.Context, workspace, table, name string) (Quest, bool, error) {
	qs, err := listQuests(ctx, s.DB.Reader(), `workspace = ? AND table_id = ? AND name = ? COLLATE NOCASE`, workspace, table, name)
	if err != nil || len(qs) == 0 {
		return Quest{}, false, err
	}
	return qs[0], true, nil
}

// OfTable lists the quests of a table.
func (s *Store) OfTable(ctx context.Context, workspace, table string) ([]Quest, error) {
	return listQuests(ctx, s.DB.Reader(), `workspace = ? AND table_id = ?`, workspace, table)
}

// Live lists the quests the engine still has to look after: the ones that run
// or are paused, and the cancelled ones that wait for a step under way to end.
func (s *Store) Live(ctx context.Context) ([]Quest, error) {
	return listQuests(ctx, s.DB.Reader(), `state IN ('running', 'paused') OR (state = 'cancelled' AND id IN
		(SELECT quest FROM quest_steps WHERE state IN ('running', 'stuck')))`)
}

// WithGitHub lists the quests whose mirror on GitHub has something to do
// (issue_dirty): the issue to make, a checklist to write again, a comment or
// a closing that did not go.
func (s *Store) WithGitHub(ctx context.Context) ([]Quest, error) {
	return listQuests(ctx, s.DB.Reader(), `issue_dirty > 0`)
}

// Steps lists the steps of a quest in the order of the definition.
func (s *Store) Steps(ctx context.Context, quest string) ([]Step, error) {
	return listSteps(ctx, s.DB.Reader(), `quest = ?`, quest)
}

// Step returns a step by its uid.
func (s *Store) Step(ctx context.Context, uid string) (Step, error) {
	return getStep(ctx, s.DB.Reader(), uid)
}

// ActiveOf finds the step a character has under way and has not ended with its
// done yet (running or stuck).
func (s *Store) ActiveOf(ctx context.Context, character string) (Step, bool, error) {
	ss, err := listSteps(ctx, s.DB.Reader(), `character = ? AND state IN ('running', 'stuck')`, character)
	if err != nil || len(ss) == 0 {
		return Step{}, false, err
	}
	return ss[0], true, nil
}

// Holding says whether another step has the character: it is being prepared
// for it or runs with it.
func (s *Store) Holding(ctx context.Context, character, except string) (bool, error) {
	var n int
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM quest_steps WHERE character = ? AND uid <> ? AND state IN ('preparing', 'running', 'stuck', 'approving')`,
		character, except).Scan(&n)
	return n > 0, err
}

// ByRequest finds the step that waits for a request of approval.
func (s *Store) ByRequest(ctx context.Context, request int64) (Step, bool, error) {
	ss, err := listSteps(ctx, s.DB.Reader(), `approval_request = ?`, request)
	if err != nil || len(ss) == 0 {
		return Step{}, false, err
	}
	return ss[0], true, nil
}

// ByCharacter lists the steps a character is assigned, any state.
func (s *Store) ByCharacter(ctx context.Context, character string) ([]Step, error) {
	return listSteps(ctx, s.DB.Reader(), `character = ?`, character)
}

// Runs lists the runs of a step, oldest first.
func (s *Store) Runs(ctx context.Context, step string) ([]Run, error) {
	return listRuns(ctx, s.DB.Reader(), `step = ?`, step)
}

// QuestRuns lists the runs of a quest of a round.
func (s *Store) QuestRuns(ctx context.Context, quest string, round int) ([]Run, error) {
	return listRuns(ctx, s.DB.Reader(), `quest = ? AND round = ?`, quest, round)
}

// Tx is a transaction on the quests.
type Tx struct {
	ctx context.Context
	tx  *sql.Tx
}

// Do runs fn in one transaction: all of it happens or none of it.
func (s *Store) Do(ctx context.Context, fn func(*Tx) error) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error { return fn(&Tx{ctx: ctx, tx: tx}) })
}

// OfTable lists the quests of a table inside the transaction.
func (t *Tx) OfTable(workspace, table string) ([]Quest, error) {
	return listQuests(t.ctx, t.tx, `workspace = ? AND table_id = ?`, workspace, table)
}

// Quest reads a quest inside the transaction.
func (t *Tx) Quest(id string) (Quest, error) { return getQuest(t.ctx, t.tx, id) }

// Steps reads the steps of a quest inside the transaction.
func (t *Tx) Steps(quest string) ([]Step, error) { return listSteps(t.ctx, t.tx, `quest = ?`, quest) }

// Step reads a step inside the transaction.
func (t *Tx) Step(uid string) (Step, error) { return getStep(t.ctx, t.tx, uid) }

// OpenRun is the run of a step that has no outcome yet.
func (t *Tx) OpenRun(step string) (Run, bool, error) {
	rs, err := listRuns(t.ctx, t.tx, `step = ? AND outcome = ''`, step)
	if err != nil || len(rs) == 0 {
		return Run{}, false, err
	}
	return rs[len(rs)-1], true, nil
}

// Runs lists the runs of a step inside the transaction.
func (t *Tx) Runs(step string) ([]Run, error) { return listRuns(t.ctx, t.tx, `step = ?`, step) }

// Insert makes a quest with its steps and gives it its id.
func (t *Tx) Insert(q Quest, steps []Step) (Quest, error) {
	var taken int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(*) FROM quests WHERE workspace = ? AND table_id = ? AND name = ? COLLATE NOCASE`, q.Workspace, q.Table, q.Name).Scan(&taken); err != nil {
		return Quest{}, err
	}
	if taken > 0 {
		return Quest{}, fmt.Errorf("%w: %s", ErrNameTaken, q.Name)
	}
	var n int64
	if err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(MAX(CAST(substr(id, 2) AS INTEGER)), 0) + 1 FROM quests`).Scan(&n); err != nil {
		return Quest{}, err
	}
	q.ID = fmt.Sprintf("q%d", n)
	def, err := json.Marshal(q.Def)
	if err != nil {
		return Quest{}, err
	}
	if _, err := t.tx.ExecContext(t.ctx, `INSERT INTO quests (`+questColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		q.ID, q.Workspace, q.Table, q.Name, string(q.State), q.PausedReason, string(def), q.MaxReturns, q.Input, q.Round, q.Designer, q.Scroll,
		q.Issue, q.Dirty, q.RoundCommented, b2i(q.IssueClosed), ms(q.CreatedAt), ms(q.UpdatedAt)); err != nil {
		return Quest{}, err
	}
	for _, st := range steps {
		st.Quest, st.UID = q.ID, q.ID+"/"+st.ID
		if _, err := t.tx.ExecContext(t.ctx, `INSERT INTO quest_steps (`+stepColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			st.UID, st.Quest, st.ID, st.Pos, string(st.State), st.Character, st.Returns, st.Entry, st.Blocker, b2i(st.Approved), st.Approval, st.ResumeNote,
			b2i(st.DiscardResult), ms(st.StuckAt), b2i(st.Escalated), st.SubIssue, b2i(st.SubClosed), st.CommentedRound, ms(st.UpdatedAt)); err != nil {
			return Quest{}, err
		}
	}
	return q, nil
}

// SaveQuest writes the fields of a quest that change.
func (t *Tx) SaveQuest(q Quest, now time.Time) error {
	q.UpdatedAt = now
	_, err := t.tx.ExecContext(t.ctx, `UPDATE quests SET state = ?, paused_reason = ?, input = ?, round = ?, scroll = ?, issue = ?, issue_dirty = ?,
		round_commented = ?, issue_closed = ?, updated_at = ? WHERE id = ?`,
		string(q.State), q.PausedReason, q.Input, q.Round, q.Scroll, q.Issue, q.Dirty, q.RoundCommented, b2i(q.IssueClosed), ms(q.UpdatedAt), q.ID)
	return err
}

// SaveStep writes the fields of a step that change.
func (t *Tx) SaveStep(st Step, now time.Time) error {
	st.UpdatedAt = now
	_, err := t.tx.ExecContext(t.ctx, `UPDATE quest_steps SET state = ?, character = ?, returns = ?, entry = ?, blocker = ?, approved = ?, approval_request = ?,
		resume_note = ?, discard_result = ?, stuck_at = ?, escalated = ?, sub_issue = ?, sub_closed = ?, commented_round = ?, updated_at = ? WHERE uid = ?`,
		string(st.State), st.Character, st.Returns, st.Entry, st.Blocker, b2i(st.Approved), st.Approval, st.ResumeNote, b2i(st.DiscardResult), ms(st.StuckAt),
		b2i(st.Escalated), st.SubIssue, b2i(st.SubClosed), st.CommentedRound, ms(st.UpdatedAt), st.UID)
	return err
}

// InsertRun records the delivery of a step.
func (t *Tx) InsertRun(r Run) (Run, error) {
	res, err := t.tx.ExecContext(t.ctx, `INSERT INTO step_runs (step, quest, round, attempt, character, character_name, class, start_commit, end_commit,
		started_at, ended_at, outcome, summary, files, uncommitted, warnings) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Step, r.Quest, r.Round, r.Attempt, r.Character, r.CharacterName, r.Class, r.StartCommit, r.EndCommit, ms(r.StartedAt), ms(r.EndedAt),
		r.Outcome, r.Summary, jsonList(r.Files), jsonList(r.Uncommitted), jsonList(r.Warnings))
	if err != nil {
		return Run{}, err
	}
	r.ID, err = res.LastInsertId()
	return r, err
}

// SaveRun writes the end of a run.
func (t *Tx) SaveRun(r Run) error {
	_, err := t.tx.ExecContext(t.ctx, `UPDATE step_runs SET character = ?, character_name = ?, class = ?, end_commit = ?, ended_at = ?, outcome = ?, summary = ?,
		files = ?, uncommitted = ?, warnings = ? WHERE id = ?`,
		r.Character, r.CharacterName, r.Class, r.EndCommit, ms(r.EndedAt), r.Outcome, r.Summary, jsonList(r.Files), jsonList(r.Uncommitted), jsonList(r.Warnings), r.ID)
	return err
}
