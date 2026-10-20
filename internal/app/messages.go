package app

import (
	"context"
	"errors"

	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/telegram"
)

// inboxMessages reads the messages of the users from the inbox of the Grão-Mestre:
// `--from-message <id>` of `table open`, `decree propose` and `choose` names the
// item of the inbox that carries the message, so the text and the author are the
// ones of the channel, never the ones an agent says.
type inboxMessages struct{ store *inbox.Store }

// item is the message of a user of the workspace.
func (m inboxMessages) item(ctx context.Context, workspace, id string) (inbox.Item, error) {
	it, err := m.store.Get(ctx, id)
	if err != nil || it.Workspace != workspace || it.Kind != inbox.KindUser || it.Origin == nil {
		return inbox.Item{}, decrees.ErrNoMessage
	}
	return it, nil
}

// Message implements tables.Messages. The attachments are not copied here: they
// come with the engine of the attachments (docs/desenvolvimento/ligacao-de-producao.md).
func (m inboxMessages) Message(ctx context.Context, workspace, id string) (tables.Message, error) {
	it, err := m.item(ctx, workspace, id)
	if err != nil {
		return tables.Message{}, errors.New("a mensagem não é de um usuário deste workspace")
	}
	return tables.Message{ID: it.ID, Text: it.Text, Origin: *it.Origin}, nil
}

// Source is the message as a decree is made from it.
func (m inboxMessages) Source(ctx context.Context, workspace, id string) (decrees.Source, error) {
	it, err := m.item(ctx, workspace, id)
	if err != nil {
		return decrees.Source{}, err
	}
	return decrees.Source{Text: it.Text, User: it.Origin.User, Channel: it.Origin.Channel, MessageID: it.ID, Audio: it.Origin.Transcribed}, nil
}

// User is the user who wrote a message.
func (m inboxMessages) User(ctx context.Context, workspace, id string) (string, error) {
	it, err := m.item(ctx, workspace, id)
	if err != nil {
		return "", err
	}
	return it.Origin.User, nil
}

// Current is the user of the last message of a user in the inbox of the Grão-Mestre:
// the one it is handling.
func (m inboxMessages) Current(ctx context.Context, workspace, grandMaster string) (string, error) {
	all, err := m.store.All(ctx, grandMaster)
	if err != nil {
		return "", err
	}
	for i := len(all) - 1; i >= 0; i-- {
		if it := all[i]; it.Workspace == workspace && it.Kind == inbox.KindUser && it.Origin != nil && it.Origin.User != "" {
			return it.Origin.User, nil
		}
	}
	return "", decrees.ErrNoMessage
}

// chooser asks the questions of `mestre choose` through the bot.
type chooser struct{ ch *telegram.ChooseChannel }

func (c chooser) Ask(ctx context.Context, workspace, mesa, asker, userID, question string, options []string, recommend int) (int64, error) {
	return c.ch.Ask(ctx, telegram.ChoiceQuestion{Workspace: workspace, Mesa: mesa, Asker: asker, UserID: userID, Question: question, Options: options}, recommend)
}
