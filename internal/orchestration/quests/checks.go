package quests

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// checkProofs checks, before anything is registered, what a done says about
// proofs, causes and places, and gives the proofs to keep. A refusal leaves
// the step running.
func (s *Service) checkProofs(ctx context.Context, q Quest, st Step, c characters.Character, r DoneRequest) ([]Proof, error) {
	switch {
	case r.Return != "" && len(r.Proofs) > 0:
		return nil, fmt.Errorf("%w: --proof does not go with --return", ErrUsage)
	case r.Return == "" && (r.Cause != "" || len(r.Where) > 0):
		return nil, fmt.Errorf("%w: --cause and --where need --return", ErrUsage)
	case r.Cause != "" && !slices.Contains(Causes, r.Cause):
		return nil, fmt.Errorf("%w: --cause is one of %s", ErrUsage, strings.Join(Causes, ", "))
	case len(r.Where) > MaxWhere:
		return nil, fmt.Errorf("%w: at most %d --where", ErrUsage, MaxWhere)
	}
	crit, err := s.st.Criteria(ctx, q.ID)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, x := range crit {
		known[x.ID] = true
	}
	var out []Proof
	given := map[string]bool{}
	for _, p := range r.Proofs {
		switch {
		case utf8.RuneCountInString(p.Text) > MaxProof:
			return nil, fmt.Errorf("%w: %d characters at most", ErrProofTooLong, MaxProof)
		case !known[p.Criterion]:
			return nil, fmt.Errorf("%w: %s", ErrNoCriterion, p.Criterion)
		}
		given[p.Criterion] = true
		out = append(out, Proof{Quest: q.ID, Criterion: p.Criterion, Text: p.Text, Origin: ProofDeclared, By: c.Name, Step: st.UID})
	}
	if r.Return != "" || s.o.Done == nil {
		return out, nil
	}
	need, err := s.o.Done.DoneRequires(ctx, q.Workspace, q.Table, c.Class)
	if err != nil {
		return nil, fmt.Errorf("done_requires of the class %s: %w", c.Class, err)
	}
	if need.Proof != classes.ProofAll {
		return out, nil
	}
	have, err := s.st.Proofs(ctx, q.ID, q.Round)
	if err != nil {
		return nil, err
	}
	for _, p := range have {
		given[p.Criterion] = true
	}
	var missing []string
	for _, x := range crit {
		if !given[x.ID] {
			missing = append(missing, x.ID)
		}
	}
	if len(missing) > 0 {
		// the proofs given count for the next done of the round, which is the
		// one that ends the step; the caller keeps them
		return out, fmt.Errorf("%w: %s", ErrProofMissing, strings.Join(missing, ", "))
	}
	return out, nil
}

// relWhere turns the places of a return into paths relative to the worktree
// of the character, when they were given from inside it.
func (s *Service) relWhere(ctx context.Context, q Quest, c characters.Character, where []string) []string {
	if len(where) == 0 {
		return where
	}
	dir := ""
	if t, err := s.o.Tables.Table(ctx, q.Workspace, q.Table); err == nil {
		dir = t.Worktrees[c.Machine]
	}
	out := make([]string, len(where))
	for i, w := range where {
		out[i] = normalizeWhere(w, dir)
	}
	return out
}

// countFailure counts the failure a return names for the step: the same
// print as the last one adds to the count, another print or none starts it
// again. It keeps the count.
func (s *Service) countFailure(tx *Tx, st Step, sd StepDef, r DoneRequest) (Failure, error) {
	return s.countFailureOf(tx, st, sd, r.Cause, r.Where)
}

func (s *Service) countFailureOf(tx *Tx, st Step, sd StepDef, cause string, where []string) (Failure, error) {
	fp, places := Fingerprint(sd.ID, cause, where, "")
	if fp == "" {
		return Failure{}, tx.SetFailure(st.UID, Failure{})
	}
	old, err := tx.Failure(st.UID)
	if err != nil {
		return Failure{}, err
	}
	f := Failure{Fingerprint: fp, Count: 1, Places: places}
	if old.Fingerprint == fp {
		f.Count = old.Count + 1
	}
	return f, tx.SetFailure(st.UID, f)
}

// keepProofs keeps proofs of a done that was refused because others were
// missing: a proof given in an earlier done of the round counts.
func (s *Service) keepProofs(ctx context.Context, q Quest, proofs []Proof) error {
	now := s.clk.Now()
	return s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Quest(q.ID)
		if err != nil {
			return err
		}
		for _, p := range proofs {
			p.Round, p.At = cur.Round, now
			if err := tx.PutProof(p); err != nil {
				return err
			}
		}
		cur.Dirty++
		return tx.SaveQuest(cur, now)
	})
}
