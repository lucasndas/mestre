package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TransferTTL is how long the chosen user has to accept the post of Owner.
const TransferTTL = 24 * time.Hour

// Transfer actions of PIN (the requests of PIN are for these actions).
const (
	ActionStartTransfer  = "transfer_owner"
	ActionAcceptTransfer = "accept_owner"
)

// StartTransfer asks to pass the post of Owner to an active user that already has a
// PIN. The Owner confirms with his PIN first: pinRequest is a request of PIN
// for ActionStartTransfer that was answered right, and it is spent here.
func (s *Service) StartTransfer(ctx context.Context, by User, toID, pinRequest string) error {
	now := s.clk.Now()
	var told string
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		me, err := s.actor(ctx, tx, by, TransferOwner)
		if err != nil {
			return err
		}
		to, err := s.load(ctx, tx, `id = ?`, toID)
		switch {
		case err != nil:
			return err
		case len(to) == 0:
			return fmt.Errorf("%w: %s", ErrNoUser, toID)
		case to[0].State != Active || to[0].ID == me.ID || !to[0].HasPIN:
			return fmt.Errorf("%w: the chosen user must be another active user with a PIN", ErrTransferBad)
		}
		if err := s.consume(ctx, tx, pinRequest, me.ID, ActionStartTransfer); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE owner_transfers SET state = 'cancelled', ended_at = ? WHERE state = 'pending'`, ms(now)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO owner_transfers (from_user, to_user, state, created_at, expires_at) VALUES (?, ?, 'pending', ?, ?)`,
			me.ID, toID, ms(now), ms(now.Add(TransferTTL))); err != nil {
			return err
		}
		told = fmt.Sprintf("%s quer passar o posto de Dono a você. Aceite com o seu PIN em até 24 horas.", me.Name)
		return nil
	})
	if err != nil {
		return err
	}
	if s.o.Alerts != nil {
		s.o.Alerts.Tell(ctx, toID, told)
	}
	return nil
}

// CancelTransfer drops the transfer that waits, before the acceptance. Only the Owner.
func (s *Service) CancelTransfer(ctx context.Context, by User) error {
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		me, err := s.actor(ctx, tx, by, TransferOwner)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE owner_transfers SET state = 'cancelled', ended_at = ? WHERE state = 'pending' AND from_user = ?`, ms(s.clk.Now()), me.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNoTransfer
		}
		return nil
	})
}

// AcceptTransfer is the acceptance of the chosen user, with his PIN
// (pinRequest, for ActionAcceptTransfer). The change is one transaction, and
// everything is checked inside it with what is stored now: the transfer is
// pending and not over, the chosen user is active, the old Owner still is the
// Owner. The old Owner becomes an Administrator of every workspace and the new
// one takes the post; at no moment there are zero or two Owners, and if one step
// does not hold nothing changes.
func (s *Service) AcceptTransfer(ctx context.Context, by User, pinRequest string) error {
	now := s.clk.Now()
	var from, newName string
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var id, expires int64
		err := tx.QueryRowContext(ctx, `SELECT t.id, t.from_user, t.expires_at FROM owner_transfers t
			JOIN users u ON u.id = t.to_user AND u.state = 'active' AND u.id = ?
			JOIN users o ON o.id = t.from_user AND o.state = 'active' AND o.level = 'owner'
			WHERE t.state = 'pending'`, by.ID).Scan(&id, &from, &expires)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoTransfer
		}
		if err != nil {
			return err
		}
		if !now.Before(fromMs(expires)) {
			return ErrNoTransfer
		}
		if err := s.consume(ctx, tx, pinRequest, by.ID, ActionAcceptTransfer); err != nil {
			return err
		}
		one := func(res sql.Result, err error) error {
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return ErrTransferBad
			}
			return nil
		}
		if err := one(tx.ExecContext(ctx, `UPDATE owner_transfers SET state = 'done', ended_at = ? WHERE id = ? AND state = 'pending'`, ms(now), id)); err != nil {
			return err
		}
		// the old Owner steps down first, so the index of the single Owner holds
		if err := one(tx.ExecContext(ctx, `UPDATE users SET level = 'administrator', updated_at = ? WHERE id = ? AND level = 'owner' AND state = 'active'`, ms(now), from)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_workspaces WHERE user_id = ?`, from); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_workspaces (user_id, workspace) SELECT ?, slug FROM workspaces`, from); err != nil {
			return err
		}
		if err := one(tx.ExecContext(ctx, `UPDATE users SET level = 'owner', updated_at = ? WHERE id = ? AND state = 'active'`, ms(now), by.ID)); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT name FROM users WHERE id = ?`, by.ID).Scan(&newName)
	})
	if err != nil {
		return err
	}
	if s.o.Alerts != nil {
		s.o.Alerts.Tell(ctx, from, fmt.Sprintf("%s aceitou o posto de Dono. Você agora é Administrador.", newName))
		s.o.Alerts.Tell(ctx, by.ID, "Você é o Dono.")
	}
	return nil
}
