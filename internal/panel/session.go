package panel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The expiry of a session (docs/painel/login.md#sessões).
const (
	// IdleTimeout is how long a session may go unused.
	IdleTimeout = 12 * time.Hour
	// MaxAge is how long a session lives at most, whatever its use.
	MaxAge = 7 * 24 * time.Hour
	// touchEvery is how often the use of a session is written: every write is
	// replicated, so a request does not write on its own.
	touchEvery = time.Minute
)

// ErrNoSession is a token that names no session, or one that is over.
var ErrNoSession = errors.New("panel: no valid session")

// Session is a session of the panel.
type Session struct {
	ID, UserID, Device, IP string
	CreatedAt, LastUsedAt  time.Time
}

// Sessions keeps the sessions in panel_sessions: only the SHA-256 of the token.
type Sessions struct {
	db  *store.DB
	clk clock.Clock
	ids *protocol.IDGen
	rnd io.Reader
	// ended is told the id of every session this object ended.
	ended func(id string)
	// admit holds the gate of the handover for a write; nil admits every write.
	admit func(ctx context.Context) (release func(), err error)
}

// write is db.WriteDurable through the gate of the handover: ErrInFailover while the
// Citadel drains, with nothing written.
func (s *Sessions) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if s.admit != nil {
		release, err := s.admit(ctx)
		if err != nil {
			return err
		}
		defer release()
	}
	return s.db.WriteDurable(ctx, fn)
}

func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (s *Sessions) end(ids ...string) {
	if s.ended != nil {
		for _, id := range ids {
			s.ended(id)
		}
	}
}

// Create opens a session for the user and returns its token, which is never
// kept: only its hash is.
func (s *Sessions) Create(ctx context.Context, userID, device, ip string) (string, Session, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(s.rnd, raw); err != nil {
		return "", Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.clk.Now()
	ss := Session{ID: s.ids.NewString(), UserID: userID, Device: clip(device, 300), IP: ip, CreatedAt: now, LastUsedAt: now}
	err := s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO panel_sessions (id, token_hash, user_id, device, ip, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			ss.ID, tokenHash(token), userID, ss.Device, ip, now.UnixMilli(), now.UnixMilli())
		return err
	})
	if err != nil {
		return "", Session{}, err
	}
	return token, ss, nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Sessions) expired(ss Session, now time.Time) bool {
	return !now.Before(ss.LastUsedAt.Add(IdleTimeout)) || !now.Before(ss.CreatedAt.Add(MaxAge))
}

// Lookup finds the session of a token. One that is over (12 h idle, 7 days old) is
// ended and ErrNoSession comes back. The use is written at most once a minute.
func (s *Sessions) Lookup(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNoSession
	}
	return s.use(ctx, `token_hash = ?`, tokenHash(token))
}

// Use is Lookup by the id of the session: a message of the socket is a use.
func (s *Sessions) Use(ctx context.Context, id string) (Session, error) {
	return s.use(ctx, `id = ?`, id)
}

func (s *Sessions) use(ctx context.Context, where string, arg string) (Session, error) {
	var ss Session
	var created, used int64
	err := s.db.Reader().QueryRowContext(ctx, `SELECT id, user_id, device, ip, created_at, last_used_at FROM panel_sessions WHERE `+where, arg).
		Scan(&ss.ID, &ss.UserID, &ss.Device, &ss.IP, &created, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNoSession
	}
	if err != nil {
		return Session{}, err
	}
	ss.CreatedAt, ss.LastUsedAt = time.UnixMilli(created).UTC(), time.UnixMilli(used).UTC()
	now := s.clk.Now()
	if s.expired(ss, now) {
		if err := s.End(ctx, ss.ID); err != nil {
			return Session{}, err
		}
		return Session{}, ErrNoSession
	}
	if now.Sub(ss.LastUsedAt) >= touchEvery {
		if err := s.write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE panel_sessions SET last_used_at = ? WHERE id = ?`, now.UnixMilli(), ss.ID)
			return err
		}); err != nil {
			return Session{}, err
		}
		ss.LastUsedAt = now
	}
	return ss, nil
}

// Exists says whether the session is still there (and not over).
func (s *Sessions) Exists(ctx context.Context, id string) bool {
	var created, used int64
	err := s.db.Reader().QueryRowContext(ctx, `SELECT created_at, last_used_at FROM panel_sessions WHERE id = ?`, id).Scan(&created, &used)
	if err != nil {
		return false
	}
	return !s.expired(Session{CreatedAt: time.UnixMilli(created), LastUsedAt: time.UnixMilli(used)}, s.clk.Now())
}

// End ends a session.
func (s *Sessions) End(ctx context.Context, id string) error {
	err := s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM panel_sessions WHERE id = ?`, id)
		return err
	})
	if err == nil {
		s.end(id)
	}
	return err
}

// EndUser ends every session of a user, except the one named in keep.
func (s *Sessions) EndUser(ctx context.Context, userID, keep string) error {
	var gone []string
	err := s.write(ctx, func(tx *sql.Tx) error {
		gone = gone[:0]
		rows, err := tx.QueryContext(ctx, `SELECT id FROM panel_sessions WHERE user_id = ? AND id <> ?`, userID, keep)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			gone = append(gone, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM panel_sessions WHERE user_id = ? AND id <> ?`, userID, keep)
		return err
	})
	if err == nil {
		s.end(gone...)
	}
	return err
}

// Sweep ends the sessions that are over and returns how many.
func (s *Sessions) Sweep(ctx context.Context) (int, error) {
	now := s.clk.Now()
	var gone []string
	err := s.write(ctx, func(tx *sql.Tx) error {
		gone = gone[:0]
		rows, err := tx.QueryContext(ctx, `SELECT id FROM panel_sessions WHERE last_used_at <= ? OR created_at <= ?`,
			now.Add(-IdleTimeout).UnixMilli(), now.Add(-MaxAge).UnixMilli())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			gone = append(gone, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range gone {
			if _, err := tx.ExecContext(ctx, `DELETE FROM panel_sessions WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.end(gone...)
	}
	return len(gone), err
}

// List returns the sessions of a user (userID "": of everyone), the oldest
// first, each with the name of the user.
func (s *Sessions) List(ctx context.Context, userID string) ([]SessionInfo, error) {
	q := `SELECT s.id, s.user_id, u.name, s.device, s.ip, s.created_at, s.last_used_at FROM panel_sessions s JOIN users u ON u.id = s.user_id`
	var args []any
	if userID != "" {
		q += ` WHERE s.user_id = ?`
		args = append(args, userID)
	}
	rows, err := s.db.Reader().QueryContext(ctx, q+` ORDER BY s.created_at, s.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var si SessionInfo
		var created, used int64
		if err := rows.Scan(&si.ID, &si.UserID, &si.UserName, &si.Device, &si.IP, &created, &used); err != nil {
			return nil, err
		}
		si.CreatedAt, si.LastUsedAt = time.UnixMilli(created).UTC(), time.UnixMilli(used).UTC()
		out = append(out, si)
	}
	return out, rows.Err()
}

// Owner returns the user a session belongs to ("" when there is none).
func (s *Sessions) Owner(ctx context.Context, id string) (string, error) {
	var uid string
	err := s.db.Reader().QueryRowContext(ctx, `SELECT user_id FROM panel_sessions WHERE id = ?`, id).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return uid, err
}

func newRand() io.Reader { return rand.Reader }
