package chain

import (
	"context"
)

// AskRequest is `mestre approval ask`: the Grão-Mestre takes a request that waits
// at its level to the people, with its summary, its details, what it recommends
// and why it does not decide.
type AskRequest struct {
	Request int64
	// By is the Grão-Mestre.
	By   string
	Note AskNote
}

// Ask takes a request of the Grão-Mestre to the people (the answer "Subir agora" of
// the chain, with the words of the Grão-Mestre). The request keeps its number, its
// action and its fingerprint: what the people decide is bound to the state it
// has. A request that is not at the level of the Grão-Mestre is refused, and one
// that already waits on the people is not asked again.
func (s *Service) Ask(ctx context.Context, a AskRequest) (Outcome, error) {
	r, err := s.st.Get(ctx, a.Request)
	switch {
	case err != nil:
		return Outcome{}, err
	case r.State != Open:
		return Outcome{}, refuse(CodeNoRequest, "the request #%d is already %s", r.ID, r.State)
	case r.Level.Kind == LevelPeople:
		return Outcome{}, s.refused(ctx, r, a.By, AnswerClimb, refuse(CodeAlreadyAsked, "the request #%d already waits on the people", r.ID))
	case r.Level.Agent != a.By:
		return Outcome{}, s.refused(ctx, r, a.By, AnswerClimb, refuse(CodeNotAtLevel, "the request #%d is not at your level: it waits on %s", r.ID, r.Level.Label()))
	case r.Deciding != nil:
		return Outcome{}, refuse(CodeBusy, "the request #%d is being decided: try again in a moment", r.ID)
	}
	level := r.Level
	note := a.Note
	note.By = a.By
	s.mu.Lock()
	cur, err := s.st.Get(ctx, a.Request)
	if err != nil || cur.State != Open || cur.Deciding != nil || !cur.Level.same(level) {
		s.mu.Unlock()
		return Outcome{}, refuse(CodeBusy, "the request #%d changed meanwhile: look at it again", a.Request)
	}
	cur.Ask = &note
	err = s.st.Save(ctx, cur)
	s.mu.Unlock()
	if err != nil {
		return Outcome{}, err
	}
	s.climb(ctx, cur.ID, level, AnswerClimb, "o Grão-Mestre levou o pedido às pessoas")
	out := s.outcome(ctx, cur.ID, "asked")
	if out.Level.Kind != LevelPeople {
		// overtaken by a decision or an expiry, or no level above: the words do not
		// stay on a request that did not go up
		s.mu.Lock()
		if back, err := s.st.Get(ctx, cur.ID); err == nil && back.State == Open && back.Level.Kind != LevelPeople && back.Ask != nil {
			back.Ask = nil
			_ = s.st.Save(ctx, back)
		}
		s.mu.Unlock()
		return out, refuse(CodeBusy, "the request #%d did not reach the people: look at it again", cur.ID)
	}
	s.audit(ctx, cur, AuditEntry{Approver: a.By, Level: level.Kind, Answer: AnswerClimb, Outcome: "accepted", Reason: "approval ask: " + oneLine(note.Summary)})
	return out, nil
}
