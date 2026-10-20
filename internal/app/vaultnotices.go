package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/internal/vault"
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
	if _, err := bot.Send(ctx, telegram.Out{ChatID: u.TelegramID, UserID: u.ID, Text: noticeText(n), Origin: "motor"}); err != nil {
		return fmt.Errorf("app: a notice about a credential was not queued for %s: %w", u.ID, err)
	}
	return nil
}

// noticeText is what the people read: in Portuguese, with the days and what to do.
func noticeText(n vault.Notice) string {
	what := fmt.Sprintf("a credencial de site %q", n.Name)
	if len(n.Domains) > 0 {
		what += " (" + strings.Join(n.Domains, ", ") + ")"
	}
	switch {
	case n.Kind == vault.NoticeExpiring && n.Days < 0:
		return fmt.Sprintf("Atenção: %s venceu em %s. Troque-a pelo painel.", what, n.Expires.Format("02/01/2006"))
	case n.Kind == vault.NoticeExpiring:
		return fmt.Sprintf("Atenção: %s vence em %d dias (%s). Troque-a pelo painel antes disso.", what, n.Days, n.Expires.Format("02/01/2006"))
	}
	return fmt.Sprintf("Lembrete: %s não tem validade e não é trocada há %d dias. Considere trocá-la pelo painel.", what, n.Days)
}

// expiryWatcher is the watcher of the validity of the site credentials of this
// period (CA-COFRE-17).
func (r *stdRun) expiryWatcher() *vault.ExpiryWatcher {
	return &vault.ExpiryWatcher{Vault: r.env.Vault, Clock: r.env.Clock, People: vaultNotices{r}.People, Notify: vaultNotices{r}.Notify, Logger: r.env.Log}
}
