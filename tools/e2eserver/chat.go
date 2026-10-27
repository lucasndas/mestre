package main

import (
	"context"
	"errors"
	"strings"

	"github.com/mestre-ia/mestre/internal/attachments"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/panelchat"
	"github.com/mestre-ia/mestre/internal/privdir"
	"github.com/mestre-ia/mestre/internal/store"
	guilddb "github.com/mestre-ia/mestre/internal/store/guild"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The conversation of the smoke test: the real service of the panel (the history, the
// attachments, the unread, the routes and the browser are the real ones) over the Guild
// database, with a Grão-Mestre that is a function: a message put in its inbox is answered
// at once, as `mestre reply` would, through the channel of the panel. There is no
// workspace of the engine and no agent in this Guild (seed.go makes only the row of the
// workspace "loja").
const (
	fakeGrandMaster = "e2e-grao-mestre"
	chatWorkspace   = "loja"
	// askForApproval is what a message must hold for the double to leave a request of cost waiting.
	askForApproval = "mais orçamento"
)

type noFiles struct{}

func (noFiles) GetFile(context.Context, string) (string, int64, error) {
	return "", 0, errors.New("no download in the smoke test")
}

func (noFiles) Download(context.Context, string, int64) ([]byte, error) {
	return nil, errors.New("no download in the smoke test")
}

// startFakeChat opens the Guild database for the conversation and returns it and what closes
// the database.
func startFakeChat(ctx context.Context, guildDB, filesDir string, mask func(string) string) (panel.ChatSource, func(), error) {
	clk := clock.Real{}
	db, err := guilddb.Open(ctx, guildDB, store.Options{Clock: clk, Synchronous: "OFF"})
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (panel.ChatSource, func(), error) {
		_ = db.Close()
		return nil, nil, err
	}
	if err := privdir.Ensure(filesDir); err != nil {
		return fail(err)
	}
	us, err := users.New(users.Options{DB: db, Clock: clk})
	if err != nil {
		return fail(err)
	}
	files, err := attachments.New(attachments.Options{DB: db, Clock: clk, Fetcher: noFiles{}, Root: func(context.Context, string) (string, error) { return filesDir, nil }, Mask: mask})
	if err != nil {
		return fail(err)
	}
	ib := &inbox.Store{DB: db}
	gm := characters.Character{ID: fakeGrandMaster, Role: characters.RoleGrandMaster, Workspace: chatWorkspace, State: characters.Idle}
	var svc *panelchat.Service
	svc, err = panelchat.New(panelchat.Options{
		DB: db, Clock: clk, Users: us, Inbox: ib, Mask: mask,
		Enqueue: func(ctx context.Context, n inbox.NewItem) (inbox.Item, error) {
			it, err := ib.Insert(ctx, n, clk.Now())
			if err != nil {
				return it, err
			}
			if n.Origin != nil && n.Origin.Choice == 0 {
				answer(ctx, ib, svc, it)
				// the journey test asks for an approval in words, as a person does; the double raises it
				if strings.Contains(strings.ToLower(it.Text), askForApproval) {
					if err := insertCostRequest(ctx, db, clk.Now()); err != nil {
						return it, err
					}
				}
			}
			return it, nil
		},
		GrandMaster: func(_ context.Context, ws string) (characters.Character, bool, error) {
			return gm, ws == chatWorkspace, nil
		},
		Character: func(context.Context, string) (characters.Character, error) { return gm, nil },
		Pending:   func(context.Context, string) (int, error) { return 0, nil },
		Receive:   files.ReceiveData,
		Limit:     files.Limit,
	})
	if err != nil {
		return fail(err)
	}
	return svc, func() { _ = db.Close() }, nil
}

// answer is `mestre reply` of the fake Grão-Mestre: the answer is kept for the channel of the
// message, and the channel is told.
func answer(ctx context.Context, ib *inbox.Store, svc *panelchat.Service, it inbox.Item) {
	text := strings.TrimSpace(it.Text)
	o, err := ib.AddOutbound(ctx, inbox.Outbound{Workspace: it.Workspace, Message: it.ID, Channel: it.Origin.Channel, User: it.Origin.User,
		Conversation: it.Origin.Conversation, ReplyTo: it.Origin.Message, Text: "Recebi: **" + text + "**", CreatedAt: clock.Real{}.Now()})
	if err != nil {
		return
	}
	if err := svc.Channel().Send(ctx, o); err == nil {
		_ = ib.MarkSent(ctx, o.ID, clock.Real{}.Now())
	}
}
