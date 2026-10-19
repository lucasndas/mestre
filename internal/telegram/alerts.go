package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/pkg/clock"
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
	Bot   *Bot
	Users *users.Service
	Clock clock.Clock

	mu    sync.Mutex
	group map[int64]*strangerGroup
}

type strangerGroup struct {
	first, edited time.Time
	count         int
	last          Stranger
	sent          map[string]int64 // owner → outbox id
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
	switch {
	case first:
		for _, o := range a.owners(ctx) {
			id, err := a.Bot.Send(ctx, Out{ChatID: o.TelegramID, UserID: o.ID, Text: text, Origin: "motor"})
			if err != nil {
				a.Bot.log.Warn("telegram: the alert of a stranger was not queued", "err", err)
				continue
			}
			a.mu.Lock()
			g.sent[o.ID] = id
			a.mu.Unlock()
		}
	case due:
		for owner, id := range sent {
			if err := a.Bot.Edit(ctx, Edit{Outbox: id, UserID: owner, Text: text}); err != nil {
				a.Bot.log.Warn("telegram: the alert of a stranger was not updated", "err", err)
			}
		}
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
	for _, o := range a.owners(ctx) {
		if _, err := a.Bot.Send(ctx, Out{ChatID: o.TelegramID, UserID: o.ID, Text: text, Origin: "motor"}); err != nil {
			a.Bot.log.Warn("telegram: a notice to the Owner was not queued", "err", err)
		}
	}
}

// Tell implements users.Alerts: a notice to one user.
func (a *OwnerAlerts) Tell(ctx context.Context, userID, text string) {
	u, err := a.Users.Get(ctx, userID)
	if err != nil || u.State != users.Active || u.TelegramID == 0 {
		return
	}
	if _, err := a.Bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: text, Origin: "motor"}); err != nil {
		a.Bot.log.Warn("telegram: a notice was not queued", "err", err)
	}
}
