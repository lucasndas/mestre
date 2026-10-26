package users

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The numbers of the login of the panel (docs/painel/login.md).
const (
	// PasswordMin is the shortest password; there is no other rule of composition.
	PasswordMin = 12
	// SetupCodeLength is the length of the code of setup of the Owner.
	SetupCodeLength = 12
	// AccessCodeLength is the length of a code of first access or of reset.
	AccessCodeLength = 12
	// AccessCodeTTL is how long a code of first access or reset is valid.
	AccessCodeTTL = 24 * time.Hour
	passwordMax   = 1024
)

// The kinds of code of access.
const (
	// AccessFirst is the code of the first access of a user: it sets the login,
	// the password and the PIN.
	AccessFirst = "first"
	// AccessReset is the code that resets the password of a user.
	AccessReset = "reset"
)

// The errors of the login.
var (
	// ErrSetupDone: the Guild already has a user, so there is no setup.
	ErrSetupDone = errors.New("users: the Guild already has users: there is no setup")
	// ErrBadPassword: the password has fewer than PasswordMin characters.
	ErrBadPassword = errors.New("users: the password is too short")
	// ErrBadLogin: the login name is not a login name.
	ErrBadLogin = errors.New("users: not a login name")
	// ErrLoginTaken: the login name belongs to another user.
	ErrLoginTaken = errors.New("users: the login name belongs to another user")
	// ErrBadPINFormat: the PIN is not 6 to 12 digits.
	ErrBadPINFormat = errors.New("users: the PIN has 6 to 12 digits")
	// ErrWrongPassword: the current password is not the one given.
	ErrWrongPassword = errors.New("users: wrong password")
)

var loginName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// NormalizeLogin is the login name as it is kept: lower case, no spaces around.
func NormalizeLogin(login string) string { return strings.ToLower(strings.TrimSpace(login)) }

// ValidLogin says whether the text (already normalized) is a login name: 3 to
// 32 characters of a-z, 0-9, dot, dash and underscore.
func ValidLogin(login string) bool { return loginName.MatchString(login) }

// ValidPassword says whether the text is a password: at least PasswordMin
// characters (and not an absurd size).
func ValidPassword(p string) bool {
	n := len([]rune(p))
	return n >= PasswordMin && len(p) <= passwordMax
}

// OnSessionsEnded registers f, called after the sessions of a user were ended in
// the database (suspension, removal, change of level, reset or change of
// password): the panel closes the sockets of that user. Only one f is kept.
func (s *Service) OnSessionsEnded(f func(ctx context.Context, userID string)) {
	s.endedMu.Lock()
	s.ended = f
	s.endedMu.Unlock()
}

func (s *Service) sessionsEnded(ctx context.Context, userID string) {
	s.endedMu.Lock()
	f := s.ended
	s.endedMu.Unlock()
	if f != nil {
		f(ctx, userID)
	}
}

// endSessions deletes the sessions of a user, except the one named in keep.
func endSessions(ctx context.Context, tx *sql.Tx, userID, keep string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM panel_sessions WHERE user_id = ? AND id <> ?`, userID, keep)
	return err
}

func codeHash(code string) string {
	h := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(code))))
	return hex.EncodeToString(h[:])
}

func (s *Service) newCode(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := io.ReadFull(s.o.Rand, raw); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, x := range raw {
		b.WriteByte(CodeAlphabet[int(x)%len(CodeAlphabet)])
	}
	return b.String(), nil
}

// NewSetupCode makes the code of setup of the Owner and voids the ones before
// it. There is no setup once the Guild has a user (ErrSetupDone).
func (s *Service) NewSetupCode(ctx context.Context) (string, error) {
	code, err := s.newCode(SetupCodeLength)
	if err != nil {
		return "", err
	}
	now := ms(s.clk.Now())
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if n, err := countUsers(ctx, tx); err != nil {
			return err
		} else if n > 0 {
			return ErrSetupDone
		}
		if _, err := tx.ExecContext(ctx, `UPDATE panel_setup_codes SET voided_at = ? WHERE used_at = 0 AND voided_at = 0`, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO panel_setup_codes (code_hash, created_at) VALUES (?, ?)`, codeHash(code), now)
		return err
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// SetupOpen says whether the Guild has no user yet: the panel then opens the
// wizard of the Owner instead of the login.
func (s *Service) SetupOpen(ctx context.Context) (bool, error) {
	var n int
	if err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

func countUsers(ctx context.Context, q queryer) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// SetupInput is what the wizard of the Owner asks for.
type SetupInput struct {
	Name, Login, Password, PIN string
}

func (s *Service) checkNew(in SetupInput, needPIN bool) (login string, err error) {
	login = NormalizeLogin(in.Login)
	switch {
	case !ValidLogin(login):
		return "", ErrBadLogin
	case !ValidPassword(in.Password):
		return "", ErrBadPassword
	case needPIN && !ValidPIN(in.PIN):
		return "", ErrBadPINFormat
	}
	return login, nil
}

// SetupOwner is the wizard of the first access: it spends the code of setup and
// makes the Owner with his login, password and PIN, in one transaction. A code
// that is wrong, used or voided is ErrBadCode; a Guild that already has a user is
// ErrSetupDone.
func (s *Service) SetupOwner(ctx context.Context, code string, in SetupInput) (User, error) {
	login, err := s.checkNew(in, true)
	if err != nil {
		return User{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return User{}, errors.New("users: a user needs a name")
	}
	ph, err := hashSecret(s.o.Rand, s.o.Password, in.Password)
	if err != nil {
		return User{}, err
	}
	pinHash, err := s.hashPIN(in.PIN)
	if err != nil {
		return User{}, err
	}
	id := s.ids.NewString()
	now := ms(s.clk.Now())
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if n, err := countUsers(ctx, tx); err != nil {
			return err
		} else if n > 0 {
			return ErrSetupDone
		}
		res, err := tx.ExecContext(ctx, `UPDATE panel_setup_codes SET used_at = ? WHERE code_hash = ? AND used_at = 0 AND voided_at = 0`, now, codeHash(code))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrBadCode
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO users (id, name, level, state, login, password_hash, pin_hash, created_at, updated_at) VALUES (?, ?, 'owner', 'active', ?, ?, ?, ?, ?)`,
			id, name, login, ph, pinHash, now, now)
		return err
	})
	if err != nil {
		return User{}, err
	}
	return s.Get(ctx, id)
}

// AccessCode is a code of first access or of reset of a user.
type AccessCode struct {
	Code      string
	UserID    string
	Kind      string
	ExpiresAt time.Time
}

// NewAccessCode makes the code of first access of a user (kind AccessFirst) or the
// code that resets his password (AccessReset). The codes before it are voided.
// A reset takes the old password away at once and ends his sessions. Only the
// Owner; the panel asks for the PIN of the Owner before.
func (s *Service) NewAccessCode(ctx context.Context, by User, userID, kind string) (AccessCode, error) {
	if kind != AccessFirst && kind != AccessReset {
		return AccessCode{}, fmt.Errorf("users: not a kind of code of access: %q", kind)
	}
	code, err := s.newCode(AccessCodeLength)
	if err != nil {
		return AccessCode{}, err
	}
	now := s.clk.Now()
	c := AccessCode{Code: code, UserID: userID, Kind: kind, ExpiresAt: now.Add(AccessCodeTTL)}
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := s.actor(ctx, tx, by, ManageUsers); err != nil {
			return err
		}
		var state, login string
		if err := tx.QueryRowContext(ctx, `SELECT state, login FROM users WHERE id = ?`, userID).Scan(&state, &login); err != nil || state != string(Active) {
			return fmt.Errorf("%w: %s", ErrNoUser, userID)
		}
		if kind == AccessReset && login == "" {
			return errors.New("users: the user has no password to reset: give him a code of first access")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE panel_access_codes SET voided_at = ? WHERE user_id = ? AND used_at = 0 AND voided_at = 0`, ms(now), userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO panel_access_codes (code_hash, user_id, kind, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
			codeHash(code), userID, kind, ms(now), ms(c.ExpiresAt)); err != nil {
			return err
		}
		if kind == AccessReset {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = '', updated_at = ? WHERE id = ?`, ms(now), userID); err != nil {
				return err
			}
			return endSessions(ctx, tx, userID, "")
		}
		return nil
	})
	if err != nil {
		return AccessCode{}, err
	}
	if kind == AccessReset {
		s.sessionsEnded(ctx, c.UserID)
	}
	return c, nil
}

// RedeemAccessCode is the "primeiro acesso" of a user, and the new password after a
// reset: it spends the code and sets the login, the password and (first access)
// the PIN. A reset keeps the login the user has and the PIN he has; the login given
// must be that one. Every failure of the code is ErrBadCode, with no word about
// which part was wrong.
func (s *Service) RedeemAccessCode(ctx context.Context, code string, in SetupInput) (User, error) {
	login, err := s.checkNew(in, false)
	if err != nil {
		return User{}, err
	}
	ph, err := hashSecret(s.o.Rand, s.o.Password, in.Password)
	if err != nil {
		return User{}, err
	}
	pinHash := ""
	if in.PIN != "" {
		if !ValidPIN(in.PIN) {
			return User{}, ErrBadPINFormat
		}
		if pinHash, err = s.hashPIN(in.PIN); err != nil {
			return User{}, err
		}
	}
	now := s.clk.Now()
	var userID string
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var kind string
		var expires, used, voided int64
		err := tx.QueryRowContext(ctx, `SELECT user_id, kind, expires_at, used_at, voided_at FROM panel_access_codes WHERE code_hash = ?`, codeHash(code)).Scan(&userID, &kind, &expires, &used, &voided)
		if err == sql.ErrNoRows {
			return ErrBadCode
		}
		if err != nil {
			return err
		}
		if used != 0 || voided != 0 || !now.Before(fromMs(expires)) {
			return ErrBadCode
		}
		var state, cur, curPIN string
		if err := tx.QueryRowContext(ctx, `SELECT state, login, pin_hash FROM users WHERE id = ?`, userID).Scan(&state, &cur, &curPIN); err != nil || state != string(Active) {
			return ErrBadCode
		}
		switch kind {
		case AccessReset:
			if login != cur {
				return ErrBadCode
			}
			pinHash = curPIN
		default:
			if pinHash == "" {
				return errors.New("users: the first access sets a PIN")
			}
			var other int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE login = ? AND id <> ? AND state <> 'removed'`, login, userID).Scan(&other); err != nil {
				return err
			}
			if other > 0 {
				return ErrLoginTaken
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE panel_access_codes SET used_at = ? WHERE code_hash = ?`, ms(now), codeHash(code)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET login = ?, password_hash = ?, pin_hash = ?, updated_at = ? WHERE id = ?`, login, ph, pinHash, ms(now), userID); err != nil {
			return err
		}
		return endSessions(ctx, tx, userID, "")
	})
	if err != nil {
		return User{}, err
	}
	s.sessionsEnded(ctx, userID)
	return s.Get(ctx, userID)
}

// Credential is the result of CheckPassword.
type Credential int

// The results of CheckPassword.
const (
	// CredOK: the login and the password are right and the user is active.
	CredOK Credential = iota
	// CredWrong: no such login, or a wrong password.
	CredWrong
	// CredSuspended: the password is right but the user is suspended (or removed).
	CredSuspended
)

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { return len(p), nil }

// dummy is verified when the login does not exist, so that a wrong login takes
// as long as a wrong password.
func (s *Service) dummy() string {
	s.dummyOnce.Do(func() { s.dummyHash, _ = hashSecret(zeroReader{}, s.o.Password, "dummy") })
	return s.dummyHash
}

// CheckPassword checks the login and the password. The user it returns is
// filled for CredOK and CredSuspended only. Wrong login and wrong password are
// the same, and take about the same time.
func (s *Service) CheckPassword(ctx context.Context, login, password string) (User, Credential, error) {
	login = NormalizeLogin(login)
	var id, hash string
	err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT id, password_hash FROM users WHERE login = ? AND login <> '' AND state <> 'removed'`, login).Scan(&id, &hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return User{}, CredWrong, err
	}
	if err != nil || hash == "" {
		verifyPIN(s.dummy(), password) // same cost as a real check
		return User{}, CredWrong, nil
	}
	if !verifyPIN(hash, password) {
		return User{}, CredWrong, nil
	}
	u, err := s.Get(ctx, id)
	if err != nil {
		return User{}, CredWrong, err
	}
	if u.State != Active {
		return u, CredSuspended, nil
	}
	return u, CredOK, nil
}

// ByLogin returns the user of a login name that is not removed.
func (s *Service) ByLogin(ctx context.Context, login string) (User, error) {
	us, err := s.load(ctx, s.o.DB.Reader(), `login = ? AND login <> '' AND state <> 'removed'`, NormalizeLogin(login))
	if err != nil {
		return User{}, err
	}
	if len(us) == 0 {
		return User{}, ErrNoUser
	}
	return us[0], nil
}

// ChangePassword changes the password of a user who gives the current one, and
// ends his other sessions (keep is the session that stays; "" ends all).
func (s *Service) ChangePassword(ctx context.Context, id, current, next, keep string) error {
	if !ValidPassword(next) {
		return ErrBadPassword
	}
	ph, err := hashSecret(s.o.Rand, s.o.Password, next)
	if err != nil {
		return err
	}
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var hash, state string
		if err := tx.QueryRowContext(ctx, `SELECT password_hash, state FROM users WHERE id = ?`, id).Scan(&hash, &state); err != nil || state != string(Active) {
			return fmt.Errorf("%w: %s", ErrNoUser, id)
		}
		if hash == "" || !verifyPIN(hash, current) {
			return ErrWrongPassword
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, ph, ms(s.clk.Now()), id); err != nil {
			return err
		}
		return endSessions(ctx, tx, id, keep)
	})
	if err != nil {
		return err
	}
	s.sessionsEnded(ctx, id) // the panel closes the sockets of the sessions that no longer exist
	return nil
}

// SetLanguage records the language of the panel a user chose.
func (s *Service) SetLanguage(ctx context.Context, id, language string) error {
	if !slices.Contains(Languages, language) {
		return fmt.Errorf("%w: %q", ErrBadLanguage, language)
	}
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET language = ?, updated_at = ? WHERE id = ? AND state = ?`, language, ms(s.clk.Now()), id, string(Active))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s", ErrNoUser, id)
		}
		return nil
	})
}
