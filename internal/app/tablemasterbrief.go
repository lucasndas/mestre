package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
)

// tableMasterBrief is the part of the resumption message of a table master that
// comes from the database (docs/orquestracao/mestre-da-mesa.md): the missions
// of the table with their state and current step, the characters and their
// states, its waits and the items in its inbox.
type tableMasterBrief struct {
	reg    characters.Registry
	quests *quests.Store
	inbox  *inbox.Store
	waits  *waits.Store
}

var _ characters.TableBriefer = (*tableMasterBrief)(nil)

// BriefTable implements characters.TableBriefer.
func (b *tableMasterBrief) BriefTable(ctx context.Context, master characters.Character) (string, error) {
	qs, err := b.quests.OfTable(ctx, master.Workspace, master.Table)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "State of your table %s, read from the database:\n", untrusted(master.Table))
	if len(qs) == 0 {
		text.WriteString("No mission yet.\n")
	}
	for i, q := range qs {
		if i == maxBriefItems {
			fmt.Fprintf(&text, "- and %d more (see `mestre quest status`).\n", len(qs)-maxBriefItems)
			break
		}
		fmt.Fprintf(&text, "- mission %q: %s", untrusted(q.Name), q.State)
		if q.State == quests.Paused {
			fmt.Fprintf(&text, " (%s)", untrusted(q.PausedReason))
		}
		steps, err := b.quests.Steps(ctx, q.ID)
		if err != nil {
			return "", err
		}
		for _, st := range steps {
			if st.State == quests.StepRunning || st.State == quests.StepStuck || st.State == quests.StepApproving || st.State == quests.StepPreparing {
				fmt.Fprintf(&text, "; step %s %s", untrusted(st.ID), st.State)
			}
		}
		text.WriteByte('\n')
	}
	alive, err := b.reg.AliveOf(ctx, master.Workspace)
	if err != nil {
		return "", err
	}
	var party []string
	for _, c := range alive {
		if c.Table == master.Table && c.ID != master.ID {
			party = append(party, fmt.Sprintf("%s (%s)", untrusted(c.Name), c.State))
		}
	}
	if len(party) > maxBriefItems {
		party = append(party[:maxBriefItems], fmt.Sprintf("and %d more", len(party)-maxBriefItems))
	}
	if len(party) == 0 {
		text.WriteString("No other character is alive in the table.\n")
	} else {
		fmt.Fprintf(&text, "Characters: %s.\n", strings.Join(party, ", "))
	}
	ws, err := b.waits.ActiveOf(ctx, master.ID)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&text, "Your waits: %d.\n", len(ws))
	n, err := b.inbox.Count(ctx, master.ID)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&text, "Items waiting in your inbox: %d.", n)
	return text.String(), nil
}
