package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Alerts is how the Service tells people and the security of the channel what
// happened (the bot, in M5 stage 2, implements it; nil does nothing).
type Alerts interface {
	// Unauthorized records an attempt that was not authorized: a wrong pairing
	// code, a pairing in a group, a code of another user
	// (docs/telegram/seguranca-do-canal.md).
	Unauthorized(ctx context.Context, telegramID int64, reason string)
	// OwnerAlert tells the Owner about something he must know: the PIN of a
	// user was locked, a user was paired, a transfer was asked.
	OwnerAlert(ctx context.Context, text string)
	// Tell sends a notice to one user.
	Tell(ctx context.Context, userID, text string)
}

// Approvals invalidates what a user approved and was not executed yet, when he
// is suspended, removed or loses the level (docs/aprovacoes/fluxo.md).
type Approvals interface {
	InvalidateBy(ctx context.Context, userID string) error
}

// Options are the pieces of the Service.
type Options struct {
	DB    *store.DB
	Clock clock.Clock
	// Alerts and Approvals may be nil.
	Alerts    Alerts
	Approvals Approvals
	// PIN are the cost of the hash of the PIN; zero is the default
	// (argon2id, 64 MiB, 2 passes).
	PIN PINCost
	// Rand is the source of the codes and the salts (crypto/rand by default).
	Rand io.Reader
}

// Service is the registry of the users.
type Service struct {
	o   Options
	clk clock.Clock
	ids *protocol.IDGen
}

// New returns the Service.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Clock == nil {
		return nil, errors.New("users: the Guild database and the clock are required")
	}
	if o.Rand == nil {
		o.Rand = rand.Reader
	}
	o.PIN = o.PIN.withDefaults()
	return &Service{o: o, clk: o.Clock, ids: protocol.NewIDGen(o.Clock)}, nil
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func fromMs(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

const userColumns = `id, name, level, state, telegram_id, github_login, pin_hash <> '', pin_locked_until, created_at`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var (
		u              User
		level, state   string
		hasPIN         bool
		locked, create int64
	)
	if err := sc.Scan(&u.ID, &u.Name, &level, &state, &u.TelegramID, &u.GitHubLogin, &hasPIN, &locked, &create); err != nil {
		return User{}, err
	}
	u.Level, u.State, u.HasPIN, u.PINLockedUntil, u.CreatedAt = Level(level), State(state), hasPIN, fromMs(locked), fromMs(create)
	return u, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Service) load(ctx context.Context, q queryer, where string, args ...any) ([]User, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+userColumns+` FROM users WHERE `+where+` ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		ws, err := workspacesOf(ctx, q, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Workspaces = ws
	}
	return out, nil
}

func workspacesOf(ctx context.Context, q queryer, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT workspace FROM user_workspaces WHERE user_id = ? ORDER BY workspace`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Get returns a user.
func (s *Service) Get(ctx context.Context, id string) (User, error) {
	us, err := s.load(ctx, s.o.DB.Reader(), `id = ?`, id)
	if err != nil {
		return User{}, err
	}
	if len(us) == 0 {
		return User{}, fmt.Errorf("%w: %s", ErrNoUser, id)
	}
	return us[0], nil
}

// ByTelegram returns the active or suspended user paired with the Telegram id.
func (s *Service) ByTelegram(ctx context.Context, telegramID int64) (User, error) {
	if telegramID == 0 {
		return User{}, ErrNoUser
	}
	us, err := s.load(ctx, s.o.DB.Reader(), `telegram_id = ? AND state <> 'removed'`, telegramID)
	if err != nil {
		return User{}, err
	}
	if len(us) == 0 {
		return User{}, fmt.Errorf("%w: telegram %d", ErrNoUser, telegramID)
	}
	return us[0], nil
}

// List returns the users that are not removed.
func (s *Service) List(ctx context.Context) ([]User, error) {
	return s.load(ctx, s.o.DB.Reader(), `state <> 'removed'`)
}

// NewUser is the registration of a user.
type NewUser struct {
	Name        string
	Level       Level
	Workspaces  []string
	GitHubLogin string
}

func (s *Service) insert(ctx context.Context, in NewUser) (User, error) {
	if !in.Level.Valid() {
		return User{}, fmt.Errorf("%w: %q", ErrBadLevel, in.Level)
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return User{}, errors.New("users: a user needs a name")
	}
	now := s.clk.Now()
	id := s.ids.NewString()
	ws := slices.Clone(in.Workspaces)
	slices.Sort(ws)
	ws = slices.Compact(ws)
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if in.Level == Owner {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE level = 'owner' AND state <> 'removed'`).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return ErrOwnerExists
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, name, level, state, github_login, created_at, updated_at) VALUES (?, ?, ?, 'active', ?, ?, ?)`,
			id, name, string(in.Level), strings.TrimSpace(in.GitHubLogin), ms(now), ms(now)); err != nil {
			return err
		}
		for _, w := range ws {
			if _, err := tx.ExecContext(ctx, `INSERT INTO user_workspaces (user_id, workspace) VALUES (?, ?)`, id, w); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return s.Get(ctx, id)
}

// CreateOwner makes the first Owner, in the wizard of the first access; there is
// only one Owner in a Guild.
func (s *Service) CreateOwner(ctx context.Context, name string) (User, error) {
	return s.insert(ctx, NewUser{Name: name, Level: Owner})
}

// Create registers a user. Only the Owner registers users, and there is no second
// Owner: the post passes by a transfer.
func (s *Service) Create(ctx context.Context, by User, in NewUser) (User, error) {
	if err := s.mayManage(by); err != nil {
		return User{}, err
	}
	if in.Level == Owner {
		return User{}, ErrOwnerExists
	}
	return s.insert(ctx, in)
}

// mayManage is the check of every action of managing users: the level and a
// user that is active.
func (s *Service) mayManage(by User) error {
	if by.State != Active {
		return &LevelError{Capability: ManageUsers, Have: by.Level, Need: Owner}
	}
	return Require(by.Level, ManageUsers)
}

// Edit is the change of a user; a nil field stays.
type Edit struct {
	Name        *string
	Level       *Level
	Workspaces  *[]string
	GitHubLogin *string
}

// Edit changes a user. What the new level or the shorter list of workspaces no
// longer allows is invalidated: the pending approvals of the user.
func (s *Service) Edit(ctx context.Context, by User, id string, e Edit) (User, error) {
	if err := s.mayManage(by); err != nil {
		return User{}, err
	}
	cur, err := s.Get(ctx, id)
	if err != nil {
		return User{}, err
	}
	if e.Level != nil {
		switch {
		case !e.Level.Valid():
			return User{}, fmt.Errorf("%w: %q", ErrBadLevel, *e.Level)
		case *e.Level == Owner && cur.Level != Owner, cur.Level == Owner && *e.Level != Owner:
			return User{}, ErrOwnerExists // the post only passes by a transfer
		}
	}
	narrowed := false
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		now := ms(s.clk.Now())
		if e.Name != nil {
			if strings.TrimSpace(*e.Name) == "" {
				return errors.New("users: a user needs a name")
			}
			if _, err := tx.ExecContext(ctx, `UPDATE users SET name = ?, updated_at = ? WHERE id = ?`, strings.TrimSpace(*e.Name), now, id); err != nil {
				return err
			}
		}
		if e.GitHubLogin != nil {
			if _, err := tx.ExecContext(ctx, `UPDATE users SET github_login = ?, updated_at = ? WHERE id = ?`, strings.TrimSpace(*e.GitHubLogin), now, id); err != nil {
				return err
			}
		}
		if e.Level != nil && *e.Level != cur.Level {
			narrowed = rank(*e.Level) < rank(cur.Level)
			if _, err := tx.ExecContext(ctx, `UPDATE users SET level = ?, updated_at = ? WHERE id = ?`, string(*e.Level), now, id); err != nil {
				return err
			}
		}
		if e.Workspaces != nil {
			ws := slices.Clone(*e.Workspaces)
			slices.Sort(ws)
			ws = slices.Compact(ws)
			for _, old := range cur.Workspaces {
				if !slices.Contains(ws, old) {
					narrowed = true
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM user_workspaces WHERE user_id = ?`, id); err != nil {
				return err
			}
			for _, w := range ws {
				if _, err := tx.ExecContext(ctx, `INSERT INTO user_workspaces (user_id, workspace) VALUES (?, ?)`, id, w); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	if narrowed {
		if err := s.invalidate(ctx, id); err != nil {
			return User{}, err
		}
	}
	return s.Get(ctx, id)
}

func (s *Service) invalidate(ctx context.Context, id string) error {
	if s.o.Approvals == nil {
		return nil
	}
	if err := s.o.Approvals.InvalidateBy(ctx, id); err != nil {
		return fmt.Errorf("users: the approvals of %s were not invalidated: %w", id, err)
	}
	return nil
}

func (s *Service) setState(ctx context.Context, by User, id string, to State) error {
	if err := s.mayManage(by); err != nil {
		return err
	}
	if by.ID == id && to != Active {
		return ErrSelf
	}
	cur, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if cur.State == Removed {
		return fmt.Errorf("%w: %s", ErrNoUser, id)
	}
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		tg := ``
		if to == Removed { // a removed user frees the Telegram id
			tg = `, telegram_id = 0`
		}
		_, err := tx.ExecContext(ctx, `UPDATE users SET state = ?, updated_at = ?`+tg+` WHERE id = ?`, string(to), ms(s.clk.Now()), id)
		return err
	})
	if err != nil {
		return err
	}
	if to != Active {
		return s.invalidate(ctx, id)
	}
	return nil
}

// Suspend stops a user at once: the bot ignores him, and what he approved and was
// not executed is invalidated. Only the Owner, and never himself.
func (s *Service) Suspend(ctx context.Context, by User, id string) error {
	return s.setState(ctx, by, id, Suspended)
}

// Reactivate brings a suspended user back. Only the Owner.
func (s *Service) Reactivate(ctx context.Context, by User, id string) error {
	return s.setState(ctx, by, id, Active)
}

// Remove has the effects of the suspension and frees the Telegram id; the row
// stays, marked as removed, for the history and the audit.
func (s *Service) Remove(ctx context.Context, by User, id string) error {
	return s.setState(ctx, by, id, Removed)
}
