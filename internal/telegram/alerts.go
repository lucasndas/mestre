package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
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
	// Zone is the time zone of the Guild, where the moments of the alerts are said
	// (nil: UTC).
	Zone func(ctx context.Context) *time.Location

	mu      sync.Mutex
	group   map[int64]*strangerGroup
	pending []i18n.Msg // notices that wait for the bot
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
		if _, err := a.sendAdmin(context.Background(), b, q.workspace, q.msg); err != nil {
			b.log.Warn("telegram: an operational alert that waited for the bot was not sent", "workspace", q.workspace, "err", err)
		}
	}
	for _, m := range pending {
		a.ownerAlert(context.Background(), m) // the panel has it since it was told
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
	text := a.strangerText(ctx, g)
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
			id, err := bot.Send(ctx, Out{ChatID: o.TelegramID, UserID: o.ID, Msg: text, Origin: "motor"})
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
			if err := bot.Edit(ctx, Edit{Outbox: id, UserID: owner, Msg: text}); err != nil {
				bot.log.Warn("telegram: the alert of a stranger was not updated", "err", err)
			}
		}
	}
}

// mirrorStranger keeps the alert of the stranger for the Owner and the Administrators in
// the panel, and counts the tries in it as the Telegram message does.
func (a *OwnerAlerts) mirrorStranger(ctx context.Context, g *strangerGroup, text i18n.Msg) {
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
	id, _, err := a.Panel.Add(ctx, notices.Notice{Kind: notices.Stranger, Audience: notices.Admins, Msg: text})
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

func (a *OwnerAlerts) strangerText(ctx context.Context, g *strangerGroup) i18n.Msg {
	s := g.last
	parts := []i18n.Msg{i18n.M("bot.stranger_head", "id", s.ID)}
	if s.Name != "" {
		parts = append(parts, i18n.M("bot.stranger_name", "name", s.Name))
	}
	if s.Username != "" {
		parts = append(parts, i18n.M("bot.stranger_username", "username", s.Username))
	}
	text := strings.Join(strings.Fields(s.Text), " ")
	if r := []rune(text); len(r) > StrangerText {
		text = string(r[:StrangerText]) + "…"
	}
	shown := i18n.Text(text)
	switch {
	case !s.Reason.IsZero():
		shown = s.Reason
	case s.Omitted:
		shown = i18n.M("bot.stranger_omitted")
	case text == "":
		shown = i18n.Text("[" + string(s.Kind) + "]")
	}
	parts = append(parts, i18n.M("bot.stranger_text", "text", shown))
	if g.count > 1 {
		since := g.first.UTC()
		if a.Zone != nil {
			if loc := a.Zone(ctx); loc != nil {
				since = g.first.In(loc)
			}
		}
		parts = append(parts, i18n.M("bot.stranger_tries", "count", g.count, "since", since))
	} else {
		parts = append(parts, i18n.M("bot.stranger_one_try"))
	}
	return i18n.Concat(parts...)
}

// Unauthorized implements users.Alerts: a wrong code, a pairing in a group, an
// unknown sender seen by another door. Only the id is known.
func (a *OwnerAlerts) Unauthorized(ctx context.Context, telegramID int64, reason i18n.Msg) {
	a.Stranger(ctx, Stranger{ID: telegramID, Reason: reason, Kind: KindText})
}

// OwnerAlert implements users.Alerts: a notice to the Owner.
func (a *OwnerAlerts) OwnerAlert(ctx context.Context, m i18n.Msg) {
	a.OwnerNotice(ctx, notices.System, m)
}

// OwnerNotice is OwnerAlert for a kind of notice the bell tells apart (the GitHub
// that is down).
func (a *OwnerAlerts) OwnerNotice(ctx context.Context, kind notices.Kind, m i18n.Msg) {
	a.keep(ctx, notices.Notice{Kind: kind, Audience: notices.Owner, Msg: m})
	a.ownerAlert(ctx, m)
}

// OwnerPrivate tells the Owner on the Telegram only: it is not kept in the bell of the
// panel. It is for what carries a secret in the text, such as the link that creates an App.
func (a *OwnerAlerts) OwnerPrivate(ctx context.Context, m i18n.Msg) { a.ownerAlert(ctx, m) }

func (a *OwnerAlerts) ownerAlert(ctx context.Context, m i18n.Msg) {
	bot := a.bot.Load()
	if bot == nil {
		a.mu.Lock()
		if a.pending = append(a.pending, m); len(a.pending) > maxPending {
			a.pending = a.pending[1:]
		}
		a.mu.Unlock()
		return
	}
	for _, o := range a.owners(ctx) {
		if _, err := bot.Send(ctx, Out{ChatID: o.TelegramID, UserID: o.ID, Msg: m, Origin: "motor"}); err != nil {
			bot.log.Warn("telegram: a notice to the Owner was not queued", "err", err)
		}
	}
}

// OwnerWorkspaceAlert is a notice to the Owner about a workspace (the warning of
// quota of a limit, a chain alert): the message starts with the header of the
// workspace, not with "Sistema". While the bot is not up it waits as a plain
// notice that names the workspace in its text.
func (a *OwnerAlerts) OwnerWorkspaceAlert(ctx context.Context, workspace string, m i18n.Msg) {
	a.keep(ctx, notices.Notice{Kind: notices.Alert, Workspace: workspace, Audience: notices.Owner, Msg: m})
	bot := a.bot.Load()
	if bot == nil {
		a.ownerAlert(ctx, i18n.M("bot.workspace_prefixed", "workspace", workspace, "text", m))
		return
	}
	for _, o := range a.owners(ctx) {
		if _, err := bot.Send(ctx, Out{ChatID: o.TelegramID, UserID: o.ID, Msg: m, Workspace: workspace, Origin: "motor"}); err != nil {
			bot.log.Warn("telegram: a notice to the Owner was not queued", "workspace", workspace, "err", err)
		}
	}
}

// Tell implements users.Alerts: a notice to one user.
func (a *OwnerAlerts) Tell(ctx context.Context, userID string, m i18n.Msg) {
	a.keep(ctx, notices.Notice{Kind: notices.System, Audience: notices.User, UserID: userID, Msg: m})
	bot := a.bot.Load()
	if bot == nil {
		return
	}
	u, err := a.Users.Get(ctx, userID)
	if err != nil || u.State != users.Active || u.TelegramID == 0 {
		return
	}
	if _, err := bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: m, Origin: "motor"}); err != nil {
		bot.log.Warn("telegram: a notice was not queued", "err", err)
	}
}

// maxAdminQueue is how many operational alerts wait for the bot.
const maxAdminQueue = 20

type adminAlert struct {
	workspace string
	msg       i18n.Msg
}

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
func (a *OwnerAlerts) AdminAlert(ctx context.Context, workspace string, m i18n.Msg) (told int, err error) {
	a.keep(ctx, notices.Notice{Kind: notices.Alert, Workspace: workspace, Audience: notices.Admins, Msg: m})
	if bot := a.bot.Load(); bot != nil {
		return a.sendAdmin(ctx, bot, workspace, m)
	}
	a.mu.Lock()
	if bot := a.bot.Load(); bot != nil { // it came up while the lock was taken
		a.mu.Unlock()
		return a.sendAdmin(ctx, bot, workspace, m)
	}
	a.adminQ = append(a.adminQ, adminAlert{workspace, m})
	if len(a.adminQ) > maxAdminQueue {
		d := a.adminQ[0]
		a.adminQ = a.adminQ[1:]
		slog.Warn("telegram: an operational alert was dropped, the bot is still not up", "workspace", d.workspace)
	}
	a.mu.Unlock()
	return 0, ErrQueued
}

func (a *OwnerAlerts) sendAdmin(ctx context.Context, bot *Bot, workspace string, m i18n.Msg) (told int, err error) {
	all, err := a.Users.List(ctx)
	if err != nil {
		return 0, err
	}
	for _, u := range all {
		if u.State != users.Active || u.TelegramID == 0 || !u.Acts(workspace) || u.Level != users.Owner && u.Level != users.Administrator {
			continue
		}
		if _, serr := bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: m, Workspace: workspace, Origin: "motor"}); serr != nil {
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
