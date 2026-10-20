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

// errNobodyToTell is a notice about a credential that reached no one: it is made
// again at the next look.
var errNobodyToTell = errors.New("app: nobody could be told about the credential")

// vaultNotices takes the notices of the validity of the site credentials to the
// people by the bot: one message to the owner_user of the credential and one to
// each Owner (one message to a person who is both). The text names the credential
// and the days, never a value.
type vaultNotices struct{ r *stdRun }

func (v vaultNotices) Notify(ctx context.Context, n vault.Notice) error {
	bot := v.r.currentBot()
	if bot == nil {
		return errors.New("app: the bot of the Telegram is not up")
	}
	all, err := v.r.users.List(ctx)
	if err != nil {
		return err
	}
	text := noticeText(n)
	told := 0
	for _, u := range all {
		if u.State != users.Active || u.TelegramID == 0 || (u.ID != n.Owner && u.Level != users.Owner) {
			continue
		}
		if _, err := bot.Send(ctx, telegram.Out{ChatID: u.TelegramID, UserID: u.ID, Text: text, Origin: "motor"}); err != nil {
			v.r.env.Log.Warn("app: a notice about a credential was not queued", "user", u.ID, "err", err)
			continue
		}
		told++
	}
	if told == 0 {
		return errNobodyToTell
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
	return &vault.ExpiryWatcher{Vault: r.env.Vault, Clock: r.env.Clock, Notify: vaultNotices{r}.Notify, Logger: r.env.Log}
}
