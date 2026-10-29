package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/internal/vault"
)

// vaultNotices takes the notices of the validity of the site credentials to the
// people by the bot: one message to the owner_user of the credential and one to
// each Owner (one message to a person who is both). The text names the credential
// and the days, never a value. Each person is told, and remembered, on his own
// (vault.ExpiryWatcher): one who could not be reached is tried again at the next
// look and the others are not told twice.
type vaultNotices struct{ r *stdRun }

// People are the active users with a Telegram who are owed the notice: its
// owner_user and every Owner.
func (v vaultNotices) People(ctx context.Context, n vault.Notice) ([]string, error) {
	all, err := v.r.users.List(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, u := range all {
		if u.State == users.Active && u.TelegramID != 0 && (u.ID == n.Owner || u.Level == users.Owner) {
			ids = append(ids, u.ID)
		}
	}
	return ids, nil
}

// Notify tells one person.
func (v vaultNotices) Notify(ctx context.Context, n vault.Notice, person string) error {
	bot := v.r.currentBot()
	if bot == nil {
		return errors.New("app: the bot of the Telegram is not up")
	}
	u, err := v.r.users.Get(ctx, person)
	if err != nil {
		return err
	}
	if _, err := bot.Send(ctx, telegram.Out{ChatID: u.TelegramID, UserID: u.ID, Msg: noticeMsg(n), Origin: "motor"}); err != nil {
		return fmt.Errorf("app: a notice about a credential was not queued for %s: %w", u.ID, err)
	}
	return nil
}

// noticeMsg is what the people read: in Portuguese, with the days and what to do.
func noticeMsg(n vault.Notice) i18n.Msg {
	what := i18n.M("vault.what", "name", fmt.Sprintf("%q", n.Name))
	if len(n.Domains) > 0 {
		what = i18n.Concat(what, i18n.M("vault.domains", "domains", strings.Join(n.Domains, ", ")))
	}
	switch {
	case n.Kind == vault.NoticeExpiring && n.Days < 0:
		return i18n.M("vault.expired", "what", what, "on", n.Expires)
	case n.Kind == vault.NoticeExpiring:
		return i18n.M("vault.expiring", "what", what, "days", n.Days, "on", n.Expires)
	}
	return i18n.M("vault.no_expiry", "what", what, "days", n.Days)
}

// expiryWatcher is the watcher of the validity of the site credentials of this
// period (CA-COFRE-17).
func (r *stdRun) expiryWatcher() *vault.ExpiryWatcher {
	return &vault.ExpiryWatcher{Vault: r.env.Vault, Clock: r.env.Clock, People: vaultNotices{r}.People, Notify: vaultNotices{r}.Notify, Logger: r.env.Log}
}
