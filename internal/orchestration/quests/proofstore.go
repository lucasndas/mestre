package quests

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Criterion is a criterion of a quest, frozen from its issue.
type Criterion struct {
	ID, Text string
	Pos      int
	// Marked is true when the engine checked the item on the issue.
	Marked bool
}

// Proof is the proof of a criterion in a round.
type Proof struct {
	Quest     string
	Round     int
	Criterion string
	Text      string
	// Origin is ProofDeclared or ProofVerified.
	Origin string
	By     string
	Step   string
	At     time.Time
}

// The origins of a proof.
const (
	ProofDeclared = "declared"
	ProofVerified = "verified"
)

// Criteria lists the criteria of a quest.
func (s *Store) Criteria(ctx context.Context, quest string) ([]Criterion, error) {
	return listCriteria(ctx, s.DB.Reader(), quest)
}

func listCriteria(ctx context.Context, q querier, quest string) ([]Criterion, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, pos, text, marked FROM quest_criteria WHERE quest = ? ORDER BY pos`, quest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Criterion
	for rows.Next() {
		var c Criterion
		var m int
		if err := rows.Scan(&c.ID, &c.Pos, &c.Text, &m); err != nil {
			return nil, err
		}
		c.Marked = m != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// Proofs lists the proofs of a round.
func (s *Store) Proofs(ctx context.Context, quest string, round int) ([]Proof, error) {
	return listProofs(ctx, s.DB.Reader(), quest, round)
}

func listProofs(ctx context.Context, q querier, quest string, round int) ([]Proof, error) {
	rows, err := q.QueryContext(ctx, `SELECT criterion, text, origin, by_name, step, at FROM quest_proofs WHERE quest = ? AND round = ? ORDER BY criterion`, quest, round)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Proof
	for rows.Next() {
		p := Proof{Quest: quest, Round: round}
		var at int64
		if err := rows.Scan(&p.Criterion, &p.Text, &p.Origin, &p.By, &p.Step, &at); err != nil {
			return nil, err
		}
		p.At = fromMs(at)
		out = append(out, p)
	}
	return out, rows.Err()
}

// Criteria reads the criteria inside the transaction.
func (t *Tx) Criteria(quest string) ([]Criterion, error) { return listCriteria(t.ctx, t.tx, quest) }

// Proofs reads the proofs of a round inside the transaction.
func (t *Tx) Proofs(quest string, round int) ([]Proof, error) {
	return listProofs(t.ctx, t.tx, quest, round)
}

// FreezeCriteria replaces the criteria of the quest by the ones read from the
// issue, keeping what is known about the ones that stay.
func (t *Tx) FreezeCriteria(quest string, cs []Criterion) error {
	old, err := listCriteria(t.ctx, t.tx, quest)
	if err != nil {
		return err
	}
	marked := map[string]bool{}
	for _, c := range old {
		marked[c.ID] = c.Marked
	}
	if _, err := t.tx.ExecContext(t.ctx, `DELETE FROM quest_criteria WHERE quest = ?`, quest); err != nil {
		return err
	}
	for i, c := range cs {
		if _, err := t.tx.ExecContext(t.ctx, `INSERT INTO quest_criteria (quest, id, pos, text, marked) VALUES (?, ?, ?, ?, ?)`, quest, c.ID, i, c.Text, b2i(marked[c.ID])); err != nil {
			return err
		}
	}
	return nil
}

// SetMarked records that the engine checked (or took back) the item of a
// criterion on the issue.
func (s *Store) SetMarked(ctx context.Context, quest, criterion string, marked bool) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE quest_criteria SET marked = ? WHERE quest = ? AND id = ?`, b2i(marked), quest, criterion)
		return err
	})
}

// PutProof keeps the proof of a criterion: the last one of the round wins.
func (t *Tx) PutProof(p Proof) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO quest_proofs (quest, round, criterion, text, origin, by_name, step, at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (quest, round, criterion) DO UPDATE SET text = excluded.text, origin = excluded.origin, by_name = excluded.by_name, step = excluded.step, at = excluded.at`,
		p.Quest, p.Round, p.Criterion, p.Text, p.Origin, p.By, p.Step, ms(p.At))
	return err
}

// Failure is the failure a step keeps repeating.
type Failure struct {
	Fingerprint string
	Count       int
	Places      []string
}

// Failure reads it inside the transaction.
func (t *Tx) Failure(step string) (Failure, error) {
	var f Failure
	var places string
	err := t.tx.QueryRowContext(t.ctx, `SELECT fingerprint, count, places FROM step_failures WHERE step = ?`, step).Scan(&f.Fingerprint, &f.Count, &places)
	if errors.Is(err, sql.ErrNoRows) {
		return Failure{}, nil
	}
	if err != nil {
		return Failure{}, err
	}
	if places != "" {
		_ = json.Unmarshal([]byte(places), &f.Places)
	}
	return f, nil
}

// SetFailure keeps the failure of a step; an empty fingerprint forgets it.
func (t *Tx) SetFailure(step string, f Failure) error {
	if f.Fingerprint == "" {
		_, err := t.tx.ExecContext(t.ctx, `DELETE FROM step_failures WHERE step = ?`, step)
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO step_failures (step, fingerprint, count, places) VALUES (?, ?, ?, ?)
		ON CONFLICT (step) DO UPDATE SET fingerprint = excluded.fingerprint, count = excluded.count, places = excluded.places`, step, f.Fingerprint, f.Count, jsonList(f.Places))
	return err
}

// ForgetFailures drops the failures of every step of a quest (a new round).
func (t *Tx) ForgetFailures(quest string) error {
	_, err := t.tx.ExecContext(t.ctx, `DELETE FROM step_failures WHERE step IN (SELECT uid FROM quest_steps WHERE quest = ?)`, quest)
	return err
}
