package users

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
)

// The numbers of the pairing (docs/telegram/usuarios-e-niveis.md#pareamento-por-código).
const (
	CodeLength = 8
	// CodeAlphabet is A-Z2-9 without 0, O, 1 and I.
	CodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	CodeTTL      = 30 * time.Minute
	// PairingFailures in PairingWindow silence the bot for that Telegram id.
	PairingFailures = 5
	PairingWindow   = time.Hour
)

// Code is a code of pairing.
type Code struct {
	Code      string
	UserID    string
	ExpiresAt time.Time
}

// NewCode makes the code of a user already registered. Only the Owner.
func (s *Service) NewCode(ctx context.Context, by User, userID string) (Code, error) {
	raw := make([]byte, CodeLength)
	if _, err := io.ReadFull(s.o.Rand, raw); err != nil {
		return Code{}, err
	}
	var b strings.Builder
	for _, x := range raw {
		b.WriteByte(CodeAlphabet[int(x)%len(CodeAlphabet)])
	}
	now := s.clk.Now()
	c := Code{Code: b.String(), UserID: userID, ExpiresAt: now.Add(CodeTTL)}
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := s.actor(ctx, tx, by, ManageUsers); err != nil {
			return err
		}
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM users WHERE id = ?`, userID).Scan(&state); err != nil || state != string(Active) {
			return fmt.Errorf("%w: %s", ErrNoUser, userID)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO telegram_pairing_codes (code, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`, c.Code, userID, ms(now), ms(c.ExpiresAt))
		return err
	})
	return c, err
}

// LooksLikeCode says whether the text has the format of a pairing: the bot
// answers "código inválido ou expirado" only to messages that have it.
func LooksLikeCode(text string) bool {
	t := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "/start")))
	if len(t) != CodeLength {
		return false
	}
	for _, r := range t {
		if !strings.ContainsRune(CodeAlphabet, r) {
			return false
		}
	}
	return true
}

// PairRequest is a message with a code, as the bot received it.
type PairRequest struct {
	// Text is the message: `/start <code>` or the code alone.
	Text       string
	TelegramID int64
	// Private says the chat is a private chat.
	Private bool
}

// Pair confirms a code and writes the Telegram id of the user. It checks that the
// code exists, did not expire and was not used; that the chat is private; that
// the id of the registration, if there is one, is this one; and that the id
// belongs to no other user. Every failure is an attempt that was not
// authorized, and 5 wrong ones in an hour from the same id silence the bot
// (ErrSilent) without looking at the code. All of it is one transaction: two
// messages at the same time spend a code once.
func (s *Service) Pair(ctx context.Context, r PairRequest) (User, error) {
	now := s.clk.Now()
	var reason i18n.Msg
	var outcome error
	var paired string
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var failures int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM telegram_pairing_failures WHERE telegram_id = ? AND at > ?`, r.TelegramID, ms(now.Add(-PairingWindow))).Scan(&failures); err != nil {
			return err
		}
		if failures >= PairingFailures {
			outcome = ErrSilent
			return nil
		}
		fail := func(why i18n.Msg, err error) error { // the failure is recorded and the transaction ends well
			reason, outcome = why, err
			_, e := tx.ExecContext(ctx, `INSERT INTO telegram_pairing_failures (telegram_id, at) VALUES (?, ?)`, r.TelegramID, ms(now))
			return e
		}
		if !r.Private {
			return fail(i18n.M("pairing.not_private"), ErrNotPrivate)
		}
		code := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(r.Text), "/start")))
		if !LooksLikeCode(code) {
			return fail(i18n.M("pairing.bad_format"), ErrBadCode)
		}
		var userID string
		var expires, used int64
		err := tx.QueryRowContext(ctx, `SELECT user_id, expires_at, used_at FROM telegram_pairing_codes WHERE code = ?`, code).Scan(&userID, &expires, &used)
		switch {
		case err == sql.ErrNoRows:
			return fail(i18n.M("pairing.unknown_code"), ErrBadCode)
		case err != nil:
			return err
		case used != 0:
			return fail(i18n.M("pairing.used_code"), ErrBadCode)
		case !now.Before(fromMs(expires)):
			return fail(i18n.M("pairing.expired_code"), ErrBadCode)
		}
		us, err := s.load(ctx, tx, `id = ?`, userID)
		if err != nil {
			return err
		}
		if len(us) == 0 || us[0].State != Active {
			return fail(i18n.M("pairing.inactive_user"), ErrBadCode)
		}
		u := us[0]
		if u.TelegramID != 0 && u.TelegramID != r.TelegramID {
			return fail(i18n.M("pairing.wrong_id"), ErrWrongID)
		}
		var other string
		switch err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE telegram_id = ? AND state <> 'removed' AND id <> ?`, r.TelegramID, u.ID).Scan(&other); {
		case err == nil:
			return fail(i18n.M("pairing.id_taken"), ErrTelegramTaken)
		case err != sql.ErrNoRows:
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE telegram_pairing_codes SET used_at = ? WHERE code = ? AND used_at = 0`, ms(now), code); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET telegram_id = ?, updated_at = ? WHERE id = ? AND state = 'active'`, r.TelegramID, ms(now), u.ID); err != nil {
			return err
		}
		paired = u.ID
		return nil
	})
	if err != nil {
		return User{}, err
	}
	if outcome != nil {
		if !reason.IsZero() && s.o.Alerts != nil {
			s.o.Alerts.Unauthorized(ctx, r.TelegramID, reason)
		}
		return User{}, outcome
	}
	u, err := s.Get(ctx, paired)
	if err != nil {
		return User{}, err
	}
	if s.o.Alerts != nil {
		s.o.Alerts.OwnerAlert(ctx, i18n.M("user.paired", "name", u.Name))
	}
	return u, nil
}

// ClearTelegram clears the Telegram id of a user, so another can be paired. Only
// the Owner.
func (s *Service) ClearTelegram(ctx context.Context, by User, userID string) error {
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := s.actor(ctx, tx, by, ManageUsers); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE users SET telegram_id = 0, telegram_language = '', updated_at = ? WHERE id = ? AND state <> 'removed'`, ms(s.clk.Now()), userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s", ErrNoUser, userID)
		}
		return nil
	})
}
