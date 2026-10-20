package users

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// The numbers of the PIN (docs/telegram/usuarios-e-niveis.md#pin-pessoal).
const (
	// PINRequestTTL is how long a request of PIN waits for the answer.
	PINRequestTTL = 5 * time.Minute
	// PINRequestErrors are the wrong PINs that cancel a request.
	PINRequestErrors = 3
	// PINLockErrors in PINLockWindow lock the PIN for PINLockFor.
	PINLockErrors  = 5
	PINLockWindow  = 15 * time.Minute
	PINLockFor     = 30 * time.Minute
	pinMin, pinMax = 6, 12
)

// PINCost is the cost of the argon2id hash of a PIN.
type PINCost struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
}

func (c PINCost) withDefaults() PINCost {
	if c.MemoryKiB == 0 {
		c.MemoryKiB = 64 * 1024
	}
	if c.Time == 0 {
		c.Time = 2
	}
	if c.Threads == 0 {
		c.Threads = 1
	}
	return c
}

const (
	pinKeyLen    = 32
	maxMemoryKiB = 256 * 1024
	maxPasses    = 10
	maxThreads   = 16
)

func (s *Service) hashPIN(pin string) (string, error) {
	salt := make([]byte, 16)
	if _, err := io.ReadFull(s.o.Rand, salt); err != nil {
		return "", err
	}
	c := s.o.PIN
	key := argon2.IDKey([]byte(pin), salt, c.Time, c.MemoryKiB, c.Threads, pinKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, c.MemoryKiB, c.Time, c.Threads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

func verifyPIN(hash, pin string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var v int
	var c PINCost
	var threads uint32
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &c.MemoryKiB, &c.Time, &threads); err != nil || threads == 0 || threads > maxThreads ||
		c.Time == 0 || c.Time > maxPasses || c.MemoryKiB < 8 || c.MemoryKiB > maxMemoryKiB {
		return false // a hash with costs out of the limits is not one the Service made
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 || len(want) > 64 {
		return false
	}
	got := argon2.IDKey([]byte(pin), salt, c.Time, c.MemoryKiB, uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ValidPIN says whether the text is a PIN: 6 to 12 digits.
func ValidPIN(pin string) bool {
	if len(pin) < pinMin || len(pin) > pinMax {
		return false
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// SetPIN sets the PIN of a user; the Service keeps only the hash. Changing it asks
// for the current PIN, which the caller confirms with a request of PIN
// before (CheckPIN). The answers given with the old PIN stop counting.
func (s *Service) SetPIN(ctx context.Context, id, pin string) error {
	if !ValidPIN(pin) {
		return errors.New("users: the PIN has 6 to 12 digits")
	}
	h, err := s.hashPIN(pin)
	if err != nil {
		return err
	}
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET pin_hash = ?, updated_at = ? WHERE id = ? AND state <> 'removed'`, h, ms(s.clk.Now()), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s", ErrNoUser, id)
		}
		_, err = tx.ExecContext(ctx, `UPDATE pin_requests SET state = 'cancelled' WHERE user_id = ? AND state IN ('open', 'done')`, id)
		return err
	})
}

// ResetPIN clears the PIN of another user; he sets a new one at the next login.
// Only the Owner. What the user had answered with the old PIN stops counting.
func (s *Service) ResetPIN(ctx context.Context, by User, id string) error {
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := s.actor(ctx, tx, by, ManageUsers); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE users SET pin_hash = '', pin_locked_until = 0, updated_at = ? WHERE id = ? AND state <> 'removed'`, ms(s.clk.Now()), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s", ErrNoUser, id)
		}
		_, err = tx.ExecContext(ctx, `UPDATE pin_requests SET state = 'cancelled' WHERE user_id = ? AND state IN ('open', 'done')`, id)
		return err
	})
}

// PINRequest is a request of PIN that the engine made: it asks, intercepts the
// answer and applies the action only if it was right.
type PINRequest struct {
	ID, UserID, Action string
	ExpiresAt          time.Time
}

// StartPIN opens a request of PIN for an action of the user. A user with no PIN
// can not do the action; one with the PIN locked is refused without a request.
func (s *Service) StartPIN(ctx context.Context, userID, action string) (PINRequest, error) {
	now := s.clk.Now()
	r := PINRequest{ID: s.ids.NewString(), UserID: userID, Action: action, ExpiresAt: now.Add(PINRequestTTL)}
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var state string
		var hasPIN bool
		var locked int64
		err := tx.QueryRowContext(ctx, `SELECT state, pin_hash <> '', pin_locked_until FROM users WHERE id = ?`, userID).Scan(&state, &hasPIN, &locked)
		switch {
		case err == sql.ErrNoRows, err == nil && state != string(Active):
			return fmt.Errorf("%w: %s", ErrNoUser, userID)
		case err != nil:
			return err
		case !hasPIN:
			return ErrNoPIN
		case now.Before(fromMs(locked)):
			return ErrPINLocked
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO pin_requests (id, user_id, action, created_at, expires_at, state) VALUES (?, ?, ?, ?, ?, 'open')`,
			r.ID, userID, action, ms(now), ms(r.ExpiresAt))
		return err
	})
	if err != nil {
		return PINRequest{}, err
	}
	return r, nil
}

// CheckPIN checks the answer to a request. A right PIN makes the request `done`
// (the caller then applies the action, once: Consume). A wrong one counts: 3 in the
// same request cancel it, 5 in 15 minutes lock the PIN of the user for 30
// minutes and the Owner is alerted; a locked PIN is refused without being
// verified. The PIN is never logged and never part of an error. All of it is one
// transaction, and the writes of the database go one at a time, so answers at
// the same time are checked one by one and each wrong one is counted.
func (s *Service) CheckPIN(ctx context.Context, requestID, pin string) error {
	var result error
	var lockedNow bool
	var userID string
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		now := s.clk.Now()
		var state string
		var errs int
		var expires int64
		err := tx.QueryRowContext(ctx, `SELECT user_id, expires_at, errors, state FROM pin_requests WHERE id = ?`, requestID).Scan(&userID, &expires, &errs, &state)
		if err == sql.ErrNoRows {
			result = ErrPINRequest
			return nil
		}
		if err != nil {
			return err
		}
		var hash string
		var locked int64
		uerr := tx.QueryRowContext(ctx, `SELECT pin_hash, pin_locked_until FROM users WHERE id = ? AND state = 'active'`, userID).Scan(&hash, &locked)
		if uerr != nil && uerr != sql.ErrNoRows {
			return uerr
		}
		if uerr == nil && now.Before(fromMs(locked)) {
			// refused without verification, even for a request opened before the
			// lock (the lock cancels it, but the answer is still "locked")
			result = ErrPINLocked
			return nil
		}
		if state != "open" || !now.Before(fromMs(expires)) {
			result = ErrPINRequest
			return nil
		}
		if uerr != nil {
			result = ErrNoUser
			return nil
		}
		if hash == "" {
			result = ErrNoPIN
			return nil
		}
		if verifyPIN(hash, pin) {
			_, err := tx.ExecContext(ctx, `UPDATE pin_requests SET state = 'done' WHERE id = ? AND state = 'open'`, requestID)
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pin_failures (user_id, at) VALUES (?, ?)`, userID, ms(now)); err != nil {
			return err
		}
		errs++
		state = "open"
		if errs >= PINRequestErrors {
			state = "cancelled"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pin_requests SET errors = ?, state = ? WHERE id = ? AND state = 'open'`, errs, state, requestID); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pin_failures WHERE user_id = ? AND at > ?`, userID, ms(now.Add(-PINLockWindow))).Scan(&n); err != nil {
			return err
		}
		if n >= PINLockErrors {
			lockedNow = true
			if _, err := tx.ExecContext(ctx, `UPDATE users SET pin_locked_until = ? WHERE id = ?`, ms(now.Add(PINLockFor)), userID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE pin_requests SET state = 'cancelled' WHERE user_id = ? AND state IN ('open', 'done')`, userID); err != nil {
				return err
			}
		}
		result = ErrBadPIN
		if lockedNow {
			result = fmt.Errorf("%w: %w", ErrBadPIN, ErrPINLocked)
		} else if state == "cancelled" {
			result = fmt.Errorf("%w: o pedido foi cancelado depois de %d erros", ErrBadPIN, PINRequestErrors)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if lockedNow && s.o.Alerts != nil {
		name := userID
		if u, gerr := s.Get(ctx, userID); gerr == nil {
			name = u.Name
		}
		s.o.Alerts.OwnerAlert(ctx, fmt.Sprintf("O PIN de %s foi bloqueado por %d minutos: %d erros em %d minutos.", name, int(PINLockFor/time.Minute), PINLockErrors, int(PINLockWindow/time.Minute)))
	}
	return result
}

// Consume spends a request that was answered right, for the action named, once:
// the second call is refused. This is what an action that asks for PIN calls
// before it acts. A user that is no longer active has no request to spend.
func (s *Service) Consume(ctx context.Context, requestID, userID, action string) error {
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		return s.consume(ctx, tx, requestID, userID, action)
	})
}

func (s *Service) consume(ctx context.Context, tx *sql.Tx, requestID, userID, action string) error {
	res, err := tx.ExecContext(ctx, `UPDATE pin_requests SET state = 'used' WHERE id = ? AND user_id = ? AND action = ? AND state = 'done' AND expires_at > ?
		AND EXISTS (SELECT 1 FROM users WHERE id = ? AND state = 'active')`, requestID, userID, action, ms(s.clk.Now()), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrPINRequest
	}
	return nil
}

// PINAction is the action a request of PIN was opened for (the engine decides
// by it what to do when the PIN is right).
func (s *Service) PINAction(ctx context.Context, requestID string) (string, error) {
	var action string
	err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT action FROM pin_requests WHERE id = ?`, requestID).Scan(&action)
	return action, err
}
