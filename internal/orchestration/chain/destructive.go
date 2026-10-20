package chain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/classes"
)

// A destructive action (`note delete`, `ritual delete`, `class delete`,
// `table close --cancel`) runs only after a user asked for it explicitly: the
// engine opens a request of type acao_destrutiva bound to the exact command and
// object and to the state of the object, answers "pending" and, when the same
// character tries again after the approval, spends the approval once if the
// object is still the one that was approved (docs/aprovacoes/fluxo.md#vínculo-ao-estado-exato).

// DestructiveAsk is a destructive action a character wants to run.
type DestructiveAsk struct {
	// Requester is the character that runs the command.
	Requester string
	// Command is the exact command ("note delete"); Object the stable key of
	// what it destroys; Version the state of the object now (a revision, a
	// hash, the head of a branch): the approval is for that state only.
	Command, Object, Version string
	// About is the line the people read; Reason what the character wrote.
	About, Reason string
}

// DestructiveTarget is the target a destructive request is bound to.
type DestructiveTarget struct {
	Command string `json:"command"`
	Object  string `json:"object"`
	Version string `json:"version"`
}

// JSON is the target in the form the impression is made from.
func (t DestructiveTarget) JSON() json.RawMessage {
	b, _ := json.Marshal(t)
	return b
}

// DestructiveFingerprint finds the requests for the same thing: the workspace,
// the command and the object, whatever the state of the object.
func DestructiveFingerprint(workspace, command, object string) string {
	h := sha256.Sum256([]byte(workspace + "\x00" + command + "\x00" + object))
	return "destrutiva:" + hex.EncodeToString(h[:])
}

// ErrNoDestructive: the ask is incomplete.
var ErrNoDestructive = errors.New("chain: a destructive action needs the character, the command and the object")

// Destructive spends the approval of the action if there is one that holds, or
// leaves a request for it open and says which. granted true means the caller
// may run the action now; it is true once per approval and only for the
// character that asked, for the object in the state that was approved, within
// the time of the people after the decision.
func (s *Service) Destructive(ctx context.Context, a DestructiveAsk) (granted bool, request int64, err error) {
	if a.Requester == "" || a.Command == "" || a.Object == "" {
		return false, 0, ErrNoDestructive
	}
	c, err := s.o.Characters.Get(ctx, a.Requester)
	if err != nil {
		return false, 0, err
	}
	fp := DestructiveFingerprint(c.Workspace, a.Command, a.Object)
	target := DestructiveTarget{Command: a.Command, Object: a.Object, Version: a.Version}.JSON()
	imp, err := Impress(target)
	if err != nil {
		return false, 0, err
	}
	st, _ := s.settings(ctx, c.Workspace)
	s.dmu.Lock()
	approved, err := s.st.ApprovedFor(ctx, c.ID, fp)
	if err != nil {
		s.dmu.Unlock()
		return false, 0, err
	}
	for _, r := range approved {
		if r.Spent || r.Type != classes.RequestDestructive {
			continue
		}
		if r.ClosedAt.IsZero() || !s.clk.Now().Before(r.ClosedAt.Add(st.peopleDeadline(r.Type))) {
			continue // the approval was not used in time
		}
		if r.Impression != imp {
			// the object changed after it was approved: the approval is over
			r.State, r.Spent = Invalidated, true
			if err := s.st.Save(ctx, r); err != nil {
				s.dmu.Unlock()
				return false, 0, err
			}
			s.audit(ctx, r, AuditEntry{Level: LevelPeople, Answer: "invalidate", Outcome: "accepted", Reason: "o alvo mudou antes de a aprovação ser aplicada"})
			continue
		}
		r.Spent = true
		if err := s.st.Save(ctx, r); err != nil {
			s.dmu.Unlock()
			return false, 0, err
		}
		s.audit(ctx, r, AuditEntry{Level: LevelPeople, Answer: "apply", Outcome: "accepted", Reason: "a aprovação foi usada: " + a.Command})
		s.dmu.Unlock()
		return true, r.ID, nil
	}
	s.dmu.Unlock()
	if open, ok, err := s.st.FindOpen(ctx, c.ID, fp); err != nil {
		return false, 0, err
	} else if ok {
		if open.Impression == imp {
			return false, open.ID, nil
		}
		// the object changed while the question was open: it is asked again
		// a second try may have closed it already: then it is simply asked again
		if err := s.Invalidate(ctx, open.ID, "o alvo mudou antes de a aprovação ser aplicada"); err != nil && !IsNoRequest(err) {
			return false, 0, err
		}
	}
	id, err := s.Request(ctx, OpenRequest{Type: classes.RequestDestructive, Requester: a.Requester, About: fmt.Sprintf("%s: %s", a.Command, a.About),
		Justification: a.Reason, Target: target, Fingerprint: fp})
	return false, id, err
}

// IsNoRequest says the error is "no open request with that number".
func IsNoRequest(err error) bool {
	var r *Refusal
	return errors.As(err, &r) && r.Code == CodeNoRequest
}
