package chain

import (
	"context"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
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
		return Outcome{}, refuse(CodeNoRequest, "approval.already_closed", "id", r.ID, "state", r.State)
	case r.Level.Kind == LevelPeople:
		return Outcome{}, s.refused(ctx, r, a.By, AnswerClimb, refuse(CodeAlreadyAsked, "approval.already_asked", "id", r.ID))
	case r.Level.Agent != a.By:
		return Outcome{}, s.refused(ctx, r, a.By, AnswerClimb, refuse(CodeNotAtLevel, "approval.not_at_your_level", "id", r.ID, "level", r.Level.Label()))
	case r.Deciding != nil:
		return Outcome{}, refuse(CodeBusy, "approval.being_decided", "id", r.ID)
	}
	level := r.Level
	note := a.Note
	note.By = a.By
	s.mu.Lock()
	cur, err := s.st.Get(ctx, a.Request)
	if err != nil || cur.State != Open || cur.Deciding != nil || !cur.Level.same(level) {
		s.mu.Unlock()
		return Outcome{}, refuse(CodeBusy, "approval.changed_look_again", "id", a.Request)
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
		return out, refuse(CodeBusy, "approval.not_reached_people", "id", cur.ID)
	}
	s.audit(ctx, cur, AuditEntry{Approver: a.By, Level: level.Kind, Answer: AnswerClimb, Outcome: "accepted", Reason: "approval ask: " + oneLine(note.Summary)})
	return out, nil
}

// AskAction opens a question of the Grão-Mestre to the people about an action that is
// not a request yet (`mestre approval ask --action "<text>"`): the request is of the
// type acao_do_grao_mestre, which nobody decides alone and no delegation opens, so
// it goes straight to the people. Approving it only answers the Grão-Mestre, who
// then acts by the normal commands; the engine does nothing else.
func (s *Service) AskAction(ctx context.Context, by characters.Character, tableID string, note AskNote) (int64, error) {
	if by.Role != characters.RoleGrandMaster || by.Workspace == "" {
		return 0, refuse(CodeNotAtLevel, "approval.only_grand_master_asks")
	}
	note.By = by.ID
	return s.Request(ctx, OpenRequest{Type: classes.RequestGMAction, Workspace: by.Workspace, Table: tableID, Requester: by.ID, About: "pergunta do Grão-Mestre sobre uma ação",
		Ask: &note, Task: note.Summary})
}
