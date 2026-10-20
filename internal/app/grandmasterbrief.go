package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
)

// grandMasterBrief is the part of the resumption message of the Grand Master
// that comes from the database (docs/orquestracao/grao-mestre.md#retomada-de-contexto):
// the open tables (state, epic, pull request, pause), its waits, how many items
// are in its inbox, the approvals that wait and the last message of a user.
type grandMasterBrief struct {
	reg    characters.Registry
	tables tables.Store
	inbox  *inbox.Store
	waits  *waits.Store
	chain  *chain.Store
}

var _ characters.Briefer = (*grandMasterBrief)(nil)

// maxBriefText cuts what a user wrote, which is quoted in the message.
const maxBriefText = 300

// Brief implements characters.Briefer.
func (b *grandMasterBrief) Brief(ctx context.Context, workspace string) (string, error) {
	gm, ok, err := b.reg.GrandMaster(ctx, workspace)
	if err != nil || !ok {
		return "", err
	}
	ts, err := b.tables.List(ctx, workspace)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "State of the workspace %s, read from the database:\n", workspace)
	open := 0
	for _, t := range ts {
		if tables.Over(t.State) {
			continue
		}
		if open == 0 {
			text.WriteString("Open tables:\n")
		}
		open++
		fmt.Fprintf(&text, "- %s: %s, epic #%d %q", t.ID, t.State, t.Epic, t.EpicTitle)
		if t.PRNumber != 0 {
			fmt.Fprintf(&text, ", PR #%d", t.PRNumber)
			if t.Merged {
				text.WriteString(" (merged)")
			}
		}
		if t.Paused {
			fmt.Fprintf(&text, ", paused (%s)", oneLine(t.PausedReason))
		}
		text.WriteByte('\n')
	}
	if open == 0 {
		text.WriteString("No table is open.\n")
	}
	if ws, err := b.waits.ActiveOf(ctx, gm.ID); err != nil {
		return "", err
	} else {
		fmt.Fprintf(&text, "Your waits: %d.\n", len(ws))
	}
	n, err := b.inbox.Count(ctx, gm.ID)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&text, "Items waiting in your inbox: %d.\n", n)
	reqs, err := b.chain.Open(ctx)
	if err != nil {
		return "", err
	}
	var pending []string
	for _, r := range reqs {
		if r.Workspace == workspace {
			pending = append(pending, fmt.Sprintf("#%d (%s, with %s: %s)", r.ID, r.Table, r.Level.Label(), oneLine(r.About)))
		}
	}
	if len(pending) == 0 {
		text.WriteString("No approval is waiting.\n")
	} else {
		fmt.Fprintf(&text, "Approvals waiting: %s.\n", strings.Join(pending, "; "))
	}
	items, err := b.inbox.All(ctx, gm.ID)
	if err != nil {
		return "", err
	}
	for i := len(items) - 1; i >= 0; i-- {
		if it := items[i]; it.Kind == inbox.KindUser {
			fmt.Fprintf(&text, "Last message from a user (%s, %s): %q", it.Sender, it.ID, cut(oneLine(it.Text), maxBriefText))
			return text.String(), nil
		}
	}
	text.WriteString("No user message yet.")
	return text.String(), nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
