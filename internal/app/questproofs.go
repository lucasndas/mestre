package app

import (
	"context"

	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
)

// questProofs gives `mestre table status` the proof of each criterion of the
// missions of a table, from what the engine recorded: it is the only source that
// says a criterion is proved, since the body of the issue is edited by anyone.
type questProofs struct{ st *quests.Store }

var _ tables.Proofs = questProofs{}

// Of implements tables.Proofs.
func (p questProofs) Of(ctx context.Context, workspace, table string) ([]tables.ProofState, error) {
	qs, err := p.st.OfTable(ctx, workspace, table)
	if err != nil {
		return nil, err
	}
	out := []tables.ProofState{}
	for _, q := range qs {
		cs, err := p.st.Criteria(ctx, q.ID)
		if err != nil {
			return nil, err
		}
		proofs, err := p.st.Proofs(ctx, q.ID, q.Round)
		if err != nil {
			return nil, err
		}
		by := map[string]quests.Proof{}
		for _, pr := range proofs {
			by[pr.Criterion] = pr
		}
		for _, c := range cs {
			st := tables.ProofState{Quest: q.Name, Criterion: c.ID, Text: tables.CleanText(c.Text), Marked: c.Marked, Origin: "sem prova"}
			if pr, ok := by[c.ID]; ok {
				st.Origin, st.Proof = "verificada pelo motor", tables.CleanText(pr.Text)
				if pr.Origin != quests.ProofVerified {
					st.Origin = "declarada por " + tables.CleanText(pr.By)
				}
			}
			out = append(out, st)
		}
	}
	return out, nil
}
