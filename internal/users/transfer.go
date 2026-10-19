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
	if err := Require(by.Level, TransferOwner); err != nil || by.State != Active {
		return &LevelError{Capability: TransferOwner, Have: by.Level, Need: Owner}
	}
	to, err := s.Get(ctx, toID)
	switch {
	case err != nil:
		return err
	case to.State != Active || to.ID == by.ID || !to.HasPIN:
		return fmt.Errorf("%w: the chosen user must be another active user with a PIN", ErrTransferBad)
	}
	if err := s.Consume(ctx, pinRequest, by.ID, ActionStartTransfer); err != nil {
		return err
	}
	now := s.clk.Now()
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE owner_transfers SET state = 'cancelled', ended_at = ? WHERE state = 'pending'`, ms(now)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO owner_transfers (from_user, to_user, state, created_at, expires_at) VALUES (?, ?, 'pending', ?, ?)`,
			by.ID, toID, ms(now), ms(now.Add(TransferTTL)))
		return err
	})
	if err != nil {
		return err
	}
	if s.o.Alerts != nil {
		s.o.Alerts.Tell(ctx, toID, fmt.Sprintf("%s quer passar o posto de Dono a você. Aceite com o seu PIN em até 24 horas.", by.Name))
	}
	return nil
}

// CancelTransfer drops the transfer that waits, before the acceptance. Only the Owner.
func (s *Service) CancelTransfer(ctx context.Context, by User) error {
	if by.Level != Owner || by.State != Active {
		return &LevelError{Capability: TransferOwner, Have: by.Level, Need: Owner}
	}
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE owner_transfers SET state = 'cancelled', ended_at = ? WHERE state = 'pending' AND from_user = ?`, ms(s.clk.Now()), by.ID)
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
// (pinRequest, for ActionAcceptTransfer). The change is one transaction: the new
// Owner takes the post and the old one becomes an Administrator of every
// workspace; at no moment there are zero or two Owners.
func (s *Service) AcceptTransfer(ctx context.Context, by User, pinRequest string) error {
	if by.State != Active {
		return ErrNoTransfer
	}
	now := s.clk.Now()
	var id int64
	var from string
	var expires int64
	err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT id, from_user, expires_at FROM owner_transfers WHERE state = 'pending' AND to_user = ?`, by.ID).Scan(&id, &from, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoTransfer
	}
	if err != nil {
		return err
	}
	if !now.Before(fromMs(expires)) {
		return ErrNoTransfer
	}
	if err := s.Consume(ctx, pinRequest, by.ID, ActionAcceptTransfer); err != nil {
		return err
	}
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE owner_transfers SET state = 'done', ended_at = ? WHERE id = ? AND state = 'pending'`, ms(now), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNoTransfer
		}
		// the old Owner steps down first, so the index of the single Owner holds
		if _, err := tx.ExecContext(ctx, `UPDATE users SET level = 'administrator', updated_at = ? WHERE id = ? AND level = 'owner'`, ms(now), from); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM user_workspaces WHERE user_id = ?`, from); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_workspaces (user_id, workspace) SELECT ?, slug FROM workspaces`, from); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET level = 'owner', updated_at = ? WHERE id = ? AND state = 'active'`, ms(now), by.ID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.o.Alerts != nil {
		s.o.Alerts.Tell(ctx, from, fmt.Sprintf("%s aceitou o posto de Dono. Você agora é Administrador.", by.Name))
		s.o.Alerts.Tell(ctx, by.ID, "Você é o Dono.")
	}
	return nil
}
