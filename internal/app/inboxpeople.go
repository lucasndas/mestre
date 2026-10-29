package app

import (
	"context"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
)

// inboxUsers is inbox.Users over the bot of the Telegram: a notice "Sistema" that
// found no Grand Master alive goes to the Owner, and a notice for one user goes to
// him. While the bot is not up the Owner's notice waits for it (OwnerAlerts).
type inboxUsers struct{ r *stdRun }

var _ inbox.Users = inboxUsers{}

// NotifyOwner implements inbox.Users.
func (u inboxUsers) NotifyOwner(ctx context.Context, _, text string) error {
	u.r.alert.OwnerAlert(ctx, i18n.Text(text))
	return nil
}

// NotifyUser implements inbox.Users: user is the id of the user.
func (u inboxUsers) NotifyUser(ctx context.Context, _, user, text string) error {
	u.r.alert.Tell(ctx, user, i18n.Text(text))
	return nil
}
