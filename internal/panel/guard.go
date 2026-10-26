package panel

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// The limits of the login (docs/painel/login.md); the window and the block are 15
// minutes by default.
const (
	// AttemptWindow is the time in which wrong attempts count.
	AttemptWindow = 15 * time.Minute
	// LoginErrors wrong passwords in AttemptWindow block the login for LoginBlockFor.
	LoginErrors   = 5
	LoginBlockFor = 15 * time.Minute
	// SetupErrors wrong setup codes in AttemptWindow from one IP close the setup to it.
	SetupErrors = 5
	// attemptKeep is how long the rows of attempts are kept before they are swept.
	attemptKeep = time.Hour
)

const (
	kindLogin      = "login"
	kindLoginBlock = "login_block"
	kindSetupIP    = "setup_ip"
)

// reserveLogin takes one of the attempts of the login before the password is
// looked at, so that requests at the same time cannot all pass the check before
// any failure is written: the attempts past LoginErrors in the window are
// refused, without looking at the password, and block the login. It says whether
// the attempt may go on, and until when the login is blocked when it may not.
func (p *Panel) reserveLogin(ctx context.Context, login string) (ok bool, until time.Time, err error) {
	now := p.clk.Now()
	err = p.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		ok, until = true, time.Time{}
		if _, err := tx.ExecContext(ctx, `DELETE FROM panel_attempts WHERE at < ? AND kind <> ?`, now.Add(-attemptKeep).UnixMilli(), kindLoginBlock); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM panel_attempts WHERE kind = ? AND at < ?`, kindLoginBlock, now.UnixMilli()); err != nil {
			return err
		}
		var blocked sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(at) FROM panel_attempts WHERE kind = ? AND key = ? AND at > ?`, kindLoginBlock, login, now.UnixMilli()).Scan(&blocked); err != nil {
			return err
		}
		if blocked.Valid {
			ok, until = false, time.UnixMilli(blocked.Int64).UTC()
			return nil
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO panel_attempts (kind, key, at) VALUES (?, ?, ?)`, kindLogin, login, now.UnixMilli()); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM panel_attempts WHERE kind = ? AND key = ? AND at > ?`, kindLogin, login, now.Add(-AttemptWindow).UnixMilli()).Scan(&n); err != nil {
			return err
		}
		if n <= LoginErrors {
			return nil
		}
		ok, until = false, now.Add(LoginBlockFor)
		return blockLogin(ctx, tx, login, until)
	})
	return ok, until, err
}

// blockLogin spends the attempts of the login and blocks it until the time.
func blockLogin(ctx context.Context, tx *sql.Tx, login string, until time.Time) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM panel_attempts WHERE kind = ? AND key = ?`, kindLogin, login); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO panel_attempts (kind, key, at) VALUES (?, ?, ?)`, kindLoginBlock, login, until.UnixMilli())
	return err
}

// failLogin is told that a reserved attempt had a wrong password. The fifth in
// the window blocks the login; it says whether this one did, and until when.
func (p *Panel) failLogin(ctx context.Context, login string) (until time.Time, blocked bool, err error) {
	now := p.clk.Now()
	err = p.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		until, blocked = time.Time{}, false
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM panel_attempts WHERE kind = ? AND key = ? AND at > ?`, kindLogin, login, now.Add(-AttemptWindow).UnixMilli()).Scan(&n); err != nil {
			return err
		}
		if n < LoginErrors {
			return nil
		}
		until, blocked = now.Add(LoginBlockFor), true
		return blockLogin(ctx, tx, login, until)
	})
	return until, blocked, err
}

// clearLogin forgets the wrong passwords of a login that got in.
func (p *Panel) clearLogin(ctx context.Context, login string) error {
	return p.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM panel_attempts WHERE kind = ? AND key = ?`, kindLogin, login)
		return err
	})
}

// setupClosedFor is when the setup opens again for the IP; zero when it is open.
func (p *Panel) setupClosedFor(ctx context.Context, ip string) (time.Time, error) {
	now := p.clk.Now()
	var n int
	var first sql.NullInt64
	err := p.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*), MIN(at) FROM panel_attempts WHERE kind = ? AND key = ? AND at > ?`,
		kindSetupIP, ip, now.Add(-AttemptWindow).UnixMilli()).Scan(&n, &first)
	if err != nil || n < SetupErrors || !first.Valid {
		return time.Time{}, err
	}
	return time.UnixMilli(first.Int64).Add(AttemptWindow).UTC(), nil
}

func (p *Panel) failSetup(ctx context.Context, ip string) error {
	now := p.clk.Now()
	return p.db.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM panel_attempts WHERE kind = ? AND at < ?`, kindSetupIP, now.Add(-attemptKeep).UnixMilli()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO panel_attempts (kind, key, at) VALUES (?, ?, ?)`, kindSetupIP, ip, now.UnixMilli())
		return err
	})
}

// audit writes a line of the audit trail (audit_log). The detail never holds a
// password, a PIN or a code.
func (p *Panel) audit(ctx context.Context, action string, detail map[string]any) {
	b, err := json.Marshal(detail)
	if err != nil {
		b = []byte("{}")
	}
	err = p.db.WriteDurable(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, at, machine_id, action, detail) VALUES (?, ?, ?, ?, ?)`,
			p.ids.NewString(), p.clk.Now().UnixMilli(), p.machine, action, string(b))
		return err
	})
	if err != nil {
		p.log.Warn("panel: the audit line was not written", "action", action, "err", err)
	}
}
