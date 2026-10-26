package chain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
)

// The requests of the Guild are of a fixed rule: they do not enter the chain of
// agents, they go straight to the people, without a workspace and without a
// character that asked (docs/aprovacoes/fluxo.md#pedidos-da-guilda).

// ErrNotSystem is what RequestSystem answers to a type that is not one of the
// requests the engine opens for itself.
var ErrNotSystem = errors.New("chain: this type is not a request of the engine")

// IsSystem says the type is one of the engine's own requests, which only the
// Owner answers with "Aprovar agora" or "Negar agora".
func IsSystem(t classes.RequestType) bool {
	return t == classes.RequestGuildEntry || t == classes.RequestCitadelPromotion
}

// SystemRequest is a request the engine opens for the people.
type SystemRequest struct {
	Type classes.RequestType
	// About is the one line the people read; Target is what the request is bound
	// to (a join id for an entry), kept with the request and read back from it.
	About  string
	Target json.RawMessage
}

// RequestSystem opens a request of the engine at the level of the people: no
// workspace, no requester, no agent in front. Only the Owner may answer it.
func (s *Service) RequestSystem(ctx context.Context, o SystemRequest) (int64, error) {
	if !IsSystem(o.Type) {
		return 0, fmt.Errorf("%w: %q", ErrNotSystem, o.Type)
	}
	r := Request{Type: o.Type, State: Open, About: o.About, Target: o.Target, Level: Level{Kind: LevelPeople}, CreatedAt: s.clk.Now(),
		Risk: classes.ClassifyRisk(classes.Env{}, nil, classes.RiskRequest{Type: o.Type})}
	if r.Risk.Risk != classes.RiskFixed {
		return 0, fmt.Errorf("chain: the request of type %s is not of a fixed rule", o.Type)
	}
	r.Why = []string{"pedido do motor: só o Dono responde"}
	r, err := s.st.Insert(ctx, r)
	if err != nil {
		return 0, err
	}
	s.audit(ctx, r, AuditEntry{Answer: answerBorn, Outcome: "accepted", Level: LevelPeople, Reason: joinWhy(r.Why)})
	st, _ := s.settings(ctx, "")
	s.arrive(ctx, r.ID, st)
	return r.ID, nil
}

// Withdraw ends an open request nobody needs anymore (the machine that asked to
// enter gave up, or its hour ended): it is cancelled, and its owner is told.
func (s *Service) Withdraw(ctx context.Context, id int64, why string) error {
	r, err := s.st.Get(ctx, id)
	if err != nil {
		return err
	}
	if !IsSystem(r.Type) {
		return fmt.Errorf("%w: #%d", ErrNotSystem, id)
	}
	if r.State != Open {
		return nil
	}
	s.cancel(ctx, r, why)
	return nil
}

// GuildEntryValidity is how long a request to enter the Guild waits for the
// Owner: the hour of the entry (guild.JoinValidity).
const GuildEntryValidity = time.Hour
