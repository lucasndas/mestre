package users

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"
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
	if err := s.mayManage(by); err != nil {
		return Code{}, err
	}
	u, err := s.Get(ctx, userID)
	if err != nil {
		return Code{}, err
	}
	if u.State != Active {
		return Code{}, fmt.Errorf("%w: %s", ErrNoUser, userID)
	}
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
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
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
// (ErrSilent) without looking at the code.
func (s *Service) Pair(ctx context.Context, r PairRequest) (User, error) {
	now := s.clk.Now()
	var failures int
	if err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM telegram_pairing_failures WHERE telegram_id = ? AND at > ?`, r.TelegramID, ms(now.Add(-PairingWindow))).Scan(&failures); err != nil {
		return User{}, err
	}
	if failures >= PairingFailures {
		return User{}, ErrSilent
	}
	fail := func(reason string, err error) (User, error) {
		werr := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, `INSERT INTO telegram_pairing_failures (telegram_id, at) VALUES (?, ?)`, r.TelegramID, ms(now))
			return e
		})
		if s.o.Alerts != nil {
			s.o.Alerts.Unauthorized(ctx, r.TelegramID, reason)
		}
		if werr != nil {
			return User{}, werr
		}
		return User{}, err
	}
	if !r.Private {
		return fail("pareamento fora de um chat privado", ErrNotPrivate)
	}
	code := strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(r.Text), "/start")))
	if !LooksLikeCode(code) {
		return fail("código com formato inválido", ErrBadCode)
	}
	var userID string
	var expires, used int64
	err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT user_id, expires_at, used_at FROM telegram_pairing_codes WHERE code = ?`, code).Scan(&userID, &expires, &used)
	switch {
	case err == sql.ErrNoRows:
		return fail("código desconhecido", ErrBadCode)
	case err != nil:
		return User{}, err
	case used != 0:
		return fail("código já usado", ErrBadCode)
	case !now.Before(fromMs(expires)):
		return fail("código expirado", ErrBadCode)
	}
	u, err := s.Get(ctx, userID)
	if err != nil || u.State != Active {
		return fail("código de um usuário que não está ativo", ErrBadCode)
	}
	if u.TelegramID != 0 && u.TelegramID != r.TelegramID {
		return fail("o id do Telegram não é o do cadastro", ErrWrongID)
	}
	if other, err := s.ByTelegram(ctx, r.TelegramID); err == nil && other.ID != u.ID {
		return fail("o id do Telegram pertence a outro usuário", ErrTelegramTaken)
	}
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE telegram_pairing_codes SET used_at = ? WHERE code = ? AND used_at = 0`, ms(now), code)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrBadCode // used meanwhile
		}
		_, err = tx.ExecContext(ctx, `UPDATE users SET telegram_id = ?, updated_at = ? WHERE id = ?`, r.TelegramID, ms(now), u.ID)
		return err
	})
	if err != nil {
		return User{}, err
	}
	if s.o.Alerts != nil {
		s.o.Alerts.OwnerAlert(ctx, fmt.Sprintf("%s foi pareado ao Telegram.", u.Name))
	}
	return s.Get(ctx, u.ID)
}

// ClearTelegram clears the Telegram id of a user, so another can be paired. Only
// the Owner.
func (s *Service) ClearTelegram(ctx context.Context, by User, userID string) error {
	if err := s.mayManage(by); err != nil {
		return err
	}
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE users SET telegram_id = 0, updated_at = ? WHERE id = ?`, ms(s.clk.Now()), userID)
		return err
	})
}
