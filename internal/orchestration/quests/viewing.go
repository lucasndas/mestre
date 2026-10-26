package quests

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Progress is what the workspace view of the panel shows of the current round of
// a quest (docs/painel/visao-do-workspace.md): how many criteria it has, how many
// have a proof and how many of those the engine verified itself, and the failure that
// paused the quest.
type Progress struct {
	Criteria, Proven, Verified int
	// Failure is the failure that paused the quest for the same failure again (zero otherwise).
	Failure Failure
}

// Progress reads the progress of the quest in its current round.
func (s *Service) Progress(ctx context.Context, q Quest) (Progress, error) {
	var p Progress
	cs, err := s.st.Criteria(ctx, q.ID)
	if err != nil {
		return p, err
	}
	p.Criteria = len(cs)
	if p.Criteria > 0 {
		proofs, err := s.st.Proofs(ctx, q.ID, q.Round)
		if err != nil {
			return p, err
		}
		known := map[string]bool{}
		for _, c := range cs {
			known[c.ID] = true
		}
		for _, pr := range proofs {
			if !known[pr.Criterion] {
				continue
			}
			p.Proven++
			if pr.Origin == ProofVerified {
				p.Verified++
			}
		}
	}
	if q.State == Paused && q.PausedReason == ReasonSameFailure {
		p.Failure, err = s.st.failureOfQuest(ctx, q.ID)
	}
	return p, err
}

// failureOfQuest is the failure that paused the quest the last time it paused for the
// same failure again.
func (s *Store) failureOfQuest(ctx context.Context, quest string) (Failure, error) {
	var f Failure
	var places string
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT fingerprint, count, places FROM quest_paused_failure WHERE quest = ?`, quest).Scan(&f.Fingerprint, &f.Count, &places)
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
