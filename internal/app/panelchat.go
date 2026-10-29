package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/attachments"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/progress"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/panelchat"
	"github.com/mestre-ia/mestre/internal/telegram"
)

// ChatPanelSource is what an IntegrationRun may also be: the conversation with the Grão-Mestre
// that the side panel of the panel reads.
type ChatPanelSource interface {
	PanelChat() panel.ChatSource
}

// PanelChat implements ChatPanelSource.
func (r *stdRun) PanelChat() panel.ChatSource { return r.chat }

var errNoOrchestration = errors.New("app: the orchestration is not up")

// newChat is the conversation of the panel over the pieces of the Citadel. The inbox, the registry
// and the attachments are made after it, so they are read when they are needed.
func (r *stdRun) newChat() (*panelchat.Service, error) {
	return panelchat.New(panelchat.Options{
		DB: r.env.DB, Clock: r.env.Clock, Users: r.users, Inbox: &inbox.Store{DB: r.env.DB}, Logger: r.env.Log,
		Enqueue: func(ctx context.Context, n inbox.NewItem) (inbox.Item, error) {
			orc := r.orc.Load()
			if orc == nil {
				return inbox.Item{}, errNoOrchestration
			}
			return orc.Inbox.Enqueue(ctx, n)
		},
		GrandMaster: func(ctx context.Context, workspace string) (c characters.Character, ok bool, err error) {
			orc := r.orc.Load()
			if orc == nil {
				return c, false, nil
			}
			return orc.Registry.GrandMaster(ctx, workspace)
		},
		Character: func(ctx context.Context, id string) (characters.Character, error) {
			orc := r.orc.Load()
			if orc == nil {
				return characters.Character{}, errNoOrchestration
			}
			return orc.Registry.Get(ctx, id)
		},
		Pending: func(ctx context.Context, id string) (int, error) {
			orc := r.orc.Load()
			if orc == nil {
				return 0, errNoOrchestration
			}
			return orc.Inbox.Pending(ctx, id)
		},
		Receive: func(ctx context.Context, req attachments.Request, data []byte) (attachments.Stored, error) {
			f := r.files.Load()
			if f == nil {
				return attachments.Stored{}, errors.New("app: the attachments are off")
			}
			return f.ReceiveData(ctx, req, data)
		},
		Limit: func() int64 {
			if f := r.files.Load(); f != nil {
				return f.Limit()
			}
			if r.s.AttachmentLimit > 0 {
				return r.s.AttachmentLimit
			}
			return attachments.DefaultLimit
		},
		Locked:     func(ctx context.Context) bool { return r.lock != nil && r.lock.Locked(ctx) },
		Mask:       r.env.Masker.Mask,
		AlertOwner: func(ctx context.Context, text string) { r.alert.OwnerAlert(ctx, i18n.Text(text)) },
		Discard: func(ctx context.Context, workspace string, st attachments.Stored) error {
			f := r.files.Load()
			if f == nil {
				return nil
			}
			return f.Discard(ctx, workspace, st)
		},
		AuditSecret: func(ctx context.Context, user, how string) {
			id := fmt.Sprintf("secret-panel-%s-%d", user, r.env.Clock.Now().UnixNano())
			if err := inbox.AuditSecretAttempt(ctx, r.env.DB, id, r.env.Clock.Now(), r.env.Machine, user, inbox.ChannelPanel, how); err != nil {
				r.env.Log.Warn("app: a secret sent through the panel was not audited", "err", err)
			}
		},
	})
}

// channelMux gives the answer of the Grão-Mestre to the channel of the message it answers: the
// chat of the Telegram or the conversation of the panel. A message of the panel never goes to
// the Telegram, and the other way around.
type channelMux struct {
	telegram *telegram.Replies
	panel    panelchat.Outgoing
}

var (
	_ inbox.Channels      = channelMux{}
	_ inbox.SourceDeleter = channelMux{}
)

// Send implements inbox.Channels.
func (m channelMux) Send(ctx context.Context, o inbox.Outbound) error {
	if o.Channel == inbox.ChannelPanel {
		return m.panel.Send(ctx, o)
	}
	return m.telegram.Send(ctx, o)
}

// DeleteSource implements inbox.SourceDeleter. A message of the panel is already gone from the
// conversation when it leaves the inbox: there is nothing else to delete.
func (m channelMux) DeleteSource(ctx context.Context, o inbox.Origin) error {
	if o.Channel == inbox.ChannelPanel {
		m.panel.Redacted(o) // the open tabs of the user drop it
		return nil
	}
	return m.telegram.DeleteSource(ctx, o)
}

// chooser asks the questions of `mestre choose` in the channel of the message that caused them.
type chooserMux struct {
	telegram chooser
	chat     *panelchat.Service
}

var _ interface {
	AskVia(ctx context.Context, channel, workspace, mesa, asker, userID, question string, options []string, recommend int) (int64, error)
} = chooserMux{}

func (c chooserMux) Ask(ctx context.Context, workspace, mesa, asker, userID, question string, options []string, recommend int) (int64, error) {
	return c.telegram.Ask(ctx, workspace, mesa, asker, userID, question, options, recommend)
}

// AskVia implements blocking.ChannelChooser.
func (c chooserMux) AskVia(ctx context.Context, channel, workspace, mesa, asker, userID, question string, options []string, recommend int) (int64, error) {
	if channel == inbox.ChannelPanel {
		return c.chat.AskChoice(ctx, panelchat.Question{Workspace: workspace, Mesa: mesa, Asker: asker, UserID: userID, Text: question, Options: options}, recommend)
	}
	return c.telegram.Ask(ctx, workspace, mesa, asker, userID, question, options, recommend)
}

// progressFanout gives the message of progress to the bot and keeps it as the card of the
// conversation of the panel, which is updated in place.
type progressFanout struct {
	bot  progressSender
	chat *panelchat.Service
	log  func(msg string, err error)
}

var _ progress.Sender = progressFanout{}

// Send implements progress.Sender.
func (p progressFanout) Send(ctx context.Context, userID, workspace, text string) error {
	if err := p.chat.SetProgress(ctx, workspace, i18n.Text(text)); err != nil && p.log != nil {
		p.log("app: the card of progress of the panel was not kept", err)
	}
	return p.bot.Send(ctx, userID, workspace, text)
}
