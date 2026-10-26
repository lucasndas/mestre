package chain

import (
	"context"

	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
)

// InboxTasks gives, as the original task of a character, the text of the last
// step, request or message it was handed: the newest item of the inbox that
// is not the engine's own (approval, answer, system, wake).
type InboxTasks struct{ Inbox *inbox.Service }

// Task implements Tasks.
func (t InboxTasks) Task(ctx context.Context, characterID string) (string, error) {
	items, err := t.Inbox.Store().All(ctx, characterID)
	if err != nil {
		return "", err
	}
	for i := len(items) - 1; i >= 0; i-- {
		switch it := items[i]; it.Kind {
		case inbox.KindQuest, inbox.KindAgent, inbox.KindUser, inbox.KindRitual, inbox.KindGitHub:
			if it.State == inbox.Delivered || it.State == inbox.Delivering {
				return it.Text, nil
			}
		}
	}
	return "", nil
}

// QuestsSink tells the quests what became of a point of approval. The quests
// refuse the decision of the designer of the quest, which the chain never
// asks for.
type QuestsSink struct {
	Quests interface {
		Decided(ctx context.Context, d quests.Decision) error
	}
}

// Decided implements Sink.
func (q QuestsSink) Decided(ctx context.Context, r Result) error {
	if q.Quests == nil {
		return nil
	}
	return q.Quests.Decided(ctx, quests.Decision{Request: r.Request.ID, Approved: r.Approved, By: r.By, Reason: r.Reason})
}
