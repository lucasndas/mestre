package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mestre-ia/mestre/internal/notices"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The numbers of the alerts of strangers (docs/telegram/seguranca-do-canal.md#filtro-de-entrada).
const (
	// StrangerWindow is the time of one alert per id.
	StrangerWindow = time.Hour
	// StrangerEdit is the least time between two edits of the count of an alert.
	StrangerEdit = 10 * time.Second
	// StrangerText is how much of the text of the stranger the alert shows.
	StrangerText = 200
)

// OwnerAlerts tells the Owner what the channel saw: the messages of strangers,
// grouped in one alert per id per hour that is edited with the count, and the
// notices of the engine (users.Alerts). It is the Alerts and the Strangers of
// the Bot.
type OwnerAlerts struct {
	// Bind gives the bot (made after the alerts: the bot needs them); until then
	// nothing is told.
	bot   atomic.Pointer[Bot]
	Users *users.Service
	Clock clock.Clock
	// Panel is the feed of the bell of the panel: what is told on the Telegram is
	// also kept there, once, for whoever sees it (nil: only the Telegram).
	Panel *notices.Feed

	mu      sync.Mutex
	group   map[int64]*strangerGroup
	pending []string // notices that wait for the bot
	adminQ  []adminAlert
}

type strangerGroup struct {
	panel         int64 // the notice of the panel, 0 until it is made
	first, edited time.Time
	count         int
	last          Stranger
	sent          map[string]int64 // owner → outbox id
}

// maxPending is how many notices wait for the bot to be up.
const maxPending = 20

// Bind gives the alerts the bot they tell through; the notices that came before
// it was up (the lock going out while the bot starts) are sent now. nil unbinds.
func (a *OwnerAlerts) Bind(b *Bot) {
	a.bot.Store(b)
	if b == nil {
		return
	}
	a.mu.Lock()
	pending := a.pending
	a.pending = nil
	a.mu.Unlock()
	a.mu.Lock()
	queued := a.adminQ
	a.adminQ = nil
	a.mu.Unlock()
	for _, q := range queued {
		if _, err := a.sendAdmin(context.Background(), b, q.workspace, q.text); err != nil {
			b.log.Warn("telegram: an operational alert that waited for the bot was not sent", "workspace", q.workspace, "err", err)
		}
	}
	for _, text := range pending {
		a.ownerAlert(context.Background(), text) // the panel has it since it was told
	}
}

var (
	_ users.Alerts = (*OwnerAlerts)(nil)
	_ Strangers    = (*OwnerAlerts)(nil)
)

func (a *OwnerAlerts) owners(ctx context.Context) []users.User {
	all, err := a.Users.List(ctx)
	if err != nil {
		return nil
	}
	var out []users.User
	for _, u := range all {
		if u.Level == users.Owner && u.State == users.Active && u.TelegramID != 0 {
			out = append(out, u)
		}
	}
	return out
}

// Stranger implements Strangers.
func (a *OwnerAlerts) Stranger(ctx context.Context, s Stranger) {
	bot := a.bot.Load()
	if bot == nil {
		return // the bot is not up: nothing can be told, and nothing is answered
	}
	now := a.Clock.Now()
	a.mu.Lock()
	if a.group == nil {
		a.group = map[int64]*strangerGroup{}
	}
	g := a.group[s.ID]
	if g == nil || now.Sub(g.first) >= StrangerWindow {
		g = &strangerGroup{first: now, edited: now, sent: map[string]int64{}}
		a.group[s.ID] = g
		// the groups of ids that went quiet do not pile up
		for id, o := range a.group {
			if id != s.ID && now.Sub(o.first) >= StrangerWindow {
				delete(a.group, id)
			}
		}
	}
	g.count++
	g.last = s
	first := g.count == 1
	due := !first && now.Sub(g.edited) >= StrangerEdit
	if due {
		g.edited = now
	}
	text := strangerText(g)
	sent := make(map[string]int64, len(g.sent))
	for k, v := range g.sent {
		sent[k] = v
	}
	a.mu.Unlock()
	if first || due {
		a.mirrorStranger(ctx, g, text)
	}
	switch {
	case first:
		for _, o := range a.owners(ctx) {
			id, err := bot.Send(ctx, Out{ChatID: o.TelegramID, UserID: o.ID, Text: text, Origin: "motor"})
			if err != nil {
				bot.log.Warn("telegram: the alert of a stranger was not queued", "err", err)
				continue
			}
			a.mu.Lock()
			g.sent[o.ID] = id
			a.mu.Unlock()
		}
	case due:
		for owner, id := range sent {
			if err := bot.Edit(ctx, Edit{Outbox: id, UserID: owner, Text: text}); err != nil {
				bot.log.Warn("telegram: the alert of a stranger was not updated", "err", err)
			}
		}
	}
}

// mirrorStranger keeps the alert of the stranger for the Owner and the Administrators in
// the panel, and counts the tries in it as the Telegram message does.
func (a *OwnerAlerts) mirrorStranger(ctx context.Context, g *strangerGroup, text string) {
	if a.Panel == nil {
		return
	}
	a.mu.Lock()
	id := g.panel
	a.mu.Unlock()
	if id != 0 {
		if err := a.Panel.Update(ctx, id, text); err != nil {
			slog.Warn("telegram: the alert of a stranger was not updated in the panel", "err", err)
		}
		return
	}
	id, _, err := a.Panel.Add(ctx, notices.Notice{Kind: notices.Stranger, Audience: notices.Admins, Text: text})
	if err != nil {
		slog.Warn("telegram: the alert of a stranger was not kept for the panel", "err", err)
		return
	}
	a.mu.Lock()
	g.panel = id
	a.mu.Unlock()
}

// keep puts a notice in the panel; a failure costs the bell, never the Telegram.
func (a *OwnerAlerts) keep(ctx context.Context, n notices.Notice) {
	if a.Panel == nil {
		return
	}
	if _, _, err := a.Panel.Add(ctx, n); err != nil {
		slog.Warn("telegram: a notice was not kept for the panel", "kind", n.Kind, "err", err)
	}
}

func strangerText(g *strangerGroup) string {
	s := g.last
	var b strings.Builder
	b.WriteString("Remetente não autorizado\n")
	fmt.Fprintf(&b, "ID: %d\n", s.ID)
	if s.Name != "" {
		fmt.Fprintf(&b, "Nome: %s\n", s.Name)
	}
	if s.Username != "" {
		fmt.Fprintf(&b, "Usuário: @%s\n", s.Username)
	}
	text := strings.Join(strings.Fields(s.Text), " ")
	if r := []rune(text); len(r) > StrangerText {
		text = string(r[:StrangerText]) + "…"
	}
	if text == "" {
		text = "[" + string(s.Kind) + "]"
	}
	fmt.Fprintf(&b, "Texto: %s\n", text)
	if g.count > 1 {
		fmt.Fprintf(&b, "%d tentativas desde %s (um alerta por hora)", g.count, g.first.UTC().Format("15:04"))
	} else {
		b.WriteString("1 tentativa (um alerta por hora)")
	}
	return b.String()
}

// Unauthorized implements users.Alerts: a wrong code, a pairing in a group, an
// unknown sender seen by another door. Only the id is known.
func (a *OwnerAlerts) Unauthorized(ctx context.Context, telegramID int64, reason string) {
	a.Stranger(ctx, Stranger{ID: telegramID, Text: reason, Kind: KindText})
}

// OwnerAlert implements users.Alerts: a notice to the Owner.
func (a *OwnerAlerts) OwnerAlert(ctx context.Context, text string) {
	a.OwnerNotice(ctx, notices.System, text)
}

// OwnerNotice is OwnerAlert for a kind of notice the bell tells apart (the GitHub
// that is down).
func (a *OwnerAlerts) OwnerNotice(ctx context.Context, kind notices.Kind, text string) {
	a.keep(ctx, notices.Notice{Kind: kind, Audience: notices.Owner, Text: text})
	a.ownerAlert(ctx, text)
}

// OwnerPrivate tells the Owner on the Telegram only: it is not kept in the bell of the
// panel. It is for what carries a secret in the text, such as the link that creates an App.
func (a *OwnerAlerts) OwnerPrivate(ctx context.Context, text string) { a.ownerAlert(ctx, text) }

func (a *OwnerAlerts) ownerAlert(ctx context.Context, text string) {
	bot := a.bot.Load()
	if bot == nil {
		a.mu.Lock()
		if a.pending = append(a.pending, text); len(a.pending) > maxPending {
			a.pending = a.pending[1:]
		}
		a.mu.Unlock()
		return
	}
	for _, o := range a.owners(ctx) {
		if _, err := bot.Send(ctx, Out{ChatID: o.TelegramID, UserID: o.ID, Text: text, Origin: "motor"}); err != nil {
			bot.log.Warn("telegram: a notice to the Owner was not queued", "err", err)
		}
	}
}

// OwnerWorkspaceAlert is a notice to the Owner about a workspace (the warning of
// quota of a limit, a chain alert): the message starts with the header of the
// workspace, not with "Sistema". While the bot is not up it waits as a plain
// notice that names the workspace in its text.
func (a *OwnerAlerts) OwnerWorkspaceAlert(ctx context.Context, workspace, text string) {
	a.keep(ctx, notices.Notice{Kind: notices.Alert, Workspace: workspace, Audience: notices.Owner, Text: text})
	bot := a.bot.Load()
	if bot == nil {
		a.ownerAlert(ctx, fmt.Sprintf("[%s] %s", workspace, text))
		return
	}
	for _, o := range a.owners(ctx) {
		if _, err := bot.Send(ctx, Out{ChatID: o.TelegramID, UserID: o.ID, Text: text, Workspace: workspace, Origin: "motor"}); err != nil {
			bot.log.Warn("telegram: a notice to the Owner was not queued", "workspace", workspace, "err", err)
		}
	}
}

// Tell implements users.Alerts: a notice to one user.
func (a *OwnerAlerts) Tell(ctx context.Context, userID, text string) {
	a.keep(ctx, notices.Notice{Kind: notices.System, Audience: notices.User, UserID: userID, Text: text})
	bot := a.bot.Load()
	if bot == nil {
		return
	}
	u, err := a.Users.Get(ctx, userID)
	if err != nil || u.State != users.Active || u.TelegramID == 0 {
		return
	}
	if _, err := bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: text, Origin: "motor"}); err != nil {
		bot.log.Warn("telegram: a notice was not queued", "err", err)
	}
}

// maxAdminQueue is how many operational alerts wait for the bot.
const maxAdminQueue = 20

type adminAlert struct{ workspace, text string }

// ErrQueued says the alert waits for the bot: it goes out, once, when the bot is up
// (in this run; a restart before that loses it, and the caller that marked it as told
// does not send it again).
var ErrQueued = errors.New("telegram: the bot is not up, the alert waits for it")

// AdminAlert tells the Owner and the Administrators who act in the workspace, one
// message each, in the order of the registry. While the bot is not up the alert waits
// in a bounded queue (the oldest is dropped, and said in the log) and ErrQueued comes
// back; the queue and the bind of the bot share one lock, so an alert is never left
// behind by the flush. Nobody to tell is not an error (the caller logs the notice);
// of the ones told, none is told again, and the ones that failed are logged.
func (a *OwnerAlerts) AdminAlert(ctx context.Context, workspace, text string) (told int, err error) {
	a.keep(ctx, notices.Notice{Kind: notices.Alert, Workspace: workspace, Audience: notices.Admins, Text: text})
	if bot := a.bot.Load(); bot != nil {
		return a.sendAdmin(ctx, bot, workspace, text)
	}
	a.mu.Lock()
	if bot := a.bot.Load(); bot != nil { // it came up while the lock was taken
		a.mu.Unlock()
		return a.sendAdmin(ctx, bot, workspace, text)
	}
	a.adminQ = append(a.adminQ, adminAlert{workspace, text})
	if len(a.adminQ) > maxAdminQueue {
		d := a.adminQ[0]
		a.adminQ = a.adminQ[1:]
		slog.Warn("telegram: an operational alert was dropped, the bot is still not up", "workspace", d.workspace)
	}
	a.mu.Unlock()
	return 0, ErrQueued
}

func (a *OwnerAlerts) sendAdmin(ctx context.Context, bot *Bot, workspace, text string) (told int, err error) {
	all, err := a.Users.List(ctx)
	if err != nil {
		return 0, err
	}
	for _, u := range all {
		if u.State != users.Active || u.TelegramID == 0 || !u.Acts(workspace) || u.Level != users.Owner && u.Level != users.Administrator {
			continue
		}
		if _, serr := bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: text, Workspace: workspace, Origin: "motor"}); serr != nil {
			bot.log.Warn("telegram: an operational alert was not queued for a user", "workspace", workspace, "user", u.ID, "err", bot.maskText(serr.Error()))
			if err == nil {
				err = serr
			}
			continue
		}
		told++
	}
	if told > 0 {
		err = nil // the ones told are not told again: a retry would repeat it to them
	}
	return told, err
}
