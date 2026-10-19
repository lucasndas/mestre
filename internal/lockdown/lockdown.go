// Package lockdown is the lock of the channel, /travar and /destravar
// (docs/telegram/seguranca-do-canal.md#travar-e-destravar): while it holds, the
// hook denies every tool, no decree counts, the inbox delivers nothing to the
// agents, no approval is applied, the engine does nothing on GitHub and no work
// is started. The state is in the database, so it survives a restart, and an
// error reading it counts as locked.
package lockdown

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Refusals.
var (
	// ErrLevel: the user's level does not lock (or unlock).
	ErrLevel = errors.New("o seu nível não permite essa ação")
	// ErrPIN: the PIN of the unlock was not confirmed.
	ErrPIN = errors.New("o PIN não foi confirmado")
)

// UnlockAction is the action of the request of PIN of an unlock.
const UnlockAction = "unlock"

// Users is the registry of the users (users.Service).
type Users interface {
	Get(ctx context.Context, id string) (users.User, error)
	List(ctx context.Context) ([]users.User, error)
	// Consume spends the request of PIN that was answered right, once.
	Consume(ctx context.Context, requestID, userID, action string) error
}

// OperatorSession ends the active session of the Operator of the PC, at once
// (the terminal is closed with no wait). Nil: the engine has none.
type OperatorSession interface {
	EndNow(ctx context.Context, by users.User) error
}

// Options are the pieces of the Service.
type Options struct {
	DB    *store.DB
	Clock clock.Clock
	Users Users
	// Tell sends a notice "Sistema" to a user (the bot); nil tells nobody.
	Tell func(ctx context.Context, userID, text string)
	// Operator ends the session of the Operator on /travar.
	Operator OperatorSession
	Location *time.Location
	Logger   *slog.Logger
}

// Service is the lock of the channel.
type Service struct {
	o  Options
	mu sync.Mutex
	on []func(locked bool)
}

// New returns the Service.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Users == nil {
		return nil, errors.New("lockdown: the database and the users are needed")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Location == nil {
		o.Location = time.UTC
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Service{o: o}, nil
}

// State is the lock now.
type State struct {
	Locked bool
	By     string
	At     time.Time
}

// State reads the lock.
func (s *Service) State(ctx context.Context) (State, error) {
	var locked int
	var by string
	var at int64
	err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT locked, by_user, at FROM channel_lock WHERE id = 1`).Scan(&locked, &by, &at)
	if err != nil {
		return State{}, err
	}
	return State{Locked: locked == 1, By: by, At: time.UnixMilli(at)}, nil
}

// Locked says whether the Mestre is locked. An error reading the state says
// locked: the safe side.
func (s *Service) Locked(ctx context.Context) bool {
	st, err := s.State(ctx)
	return err != nil || st.Locked
}

// OnChange registers a function told every change of the lock.
func (s *Service) OnChange(f func(locked bool)) {
	s.mu.Lock()
	s.on = append(s.on, f)
	s.mu.Unlock()
}

func (s *Service) audit(ctx context.Context, user, action, outcome, detail string) {
	err := s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO channel_lock_audit (at, user_id, action, outcome, detail) VALUES (?, ?, ?, ?, ?)`,
			s.o.Clock.Now().UnixMilli(), user, action, outcome, detail)
		return err
	})
	if err != nil {
		s.o.Logger.Error("lockdown: the audit was not written", "user", user, "action", action, "outcome", outcome, "err", err)
	}
}

func (s *Service) changed(locked bool) {
	s.mu.Lock()
	fs := slices.Clone(s.on)
	s.mu.Unlock()
	for _, f := range fs {
		f(locked)
	}
}

// Lock locks the Mestre (Administrator and the Owner): the state is saved, the
// session of the Operator ends, and every user is told who locked and when. A
// level below is refused and the attempt is recorded.
func (s *Service) Lock(ctx context.Context, by users.User) error {
	cur, err := s.o.Users.Get(ctx, by.ID)
	if err != nil || cur.State != users.Active || users.Require(cur.Level, users.Lock) != nil {
		s.audit(ctx, by.ID, "lock", "refused", "level")
		return ErrLevel
	}
	now := s.o.Clock.Now()
	var already bool
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var locked int
		if err := tx.QueryRowContext(ctx, `SELECT locked FROM channel_lock WHERE id = 1`).Scan(&locked); err != nil {
			return err
		}
		if already = locked == 1; already {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE channel_lock SET locked = 1, by_user = ?, at = ? WHERE id = 1`, cur.ID, now.UnixMilli())
		return err
	})
	if err != nil {
		return fmt.Errorf("lockdown: %w", err)
	}
	if already {
		return nil
	}
	s.audit(ctx, cur.ID, "lock", "accepted", "")
	if s.o.Operator != nil {
		if err := s.o.Operator.EndNow(ctx, cur); err != nil {
			s.audit(ctx, cur.ID, "lock", "operator_not_ended", err.Error())
		}
	}
	s.changed(true)
	s.tellAll(ctx, fmt.Sprintf("Sistema: %s travou o Mestre às %s. Nada é executado até o Dono destravar.", cur.Name, now.In(s.o.Location).Format("15:04")))
	return nil
}

func (s *Service) tellAll(ctx context.Context, text string) {
	if s.o.Tell == nil {
		return
	}
	all, err := s.o.Users.List(ctx)
	if err != nil {
		return
	}
	for _, u := range all {
		if u.State == users.Active {
			s.o.Tell(ctx, u.ID, text)
		}
	}
}

// Unlock unlocks the Mestre (only the Owner, with the PIN: pinRequest is the
// request of PIN, answered right, that this spends once).
func (s *Service) Unlock(ctx context.Context, by users.User, pinRequest string) error {
	cur, err := s.o.Users.Get(ctx, by.ID)
	if err != nil || cur.State != users.Active || users.Require(cur.Level, users.Unlock) != nil {
		s.audit(ctx, by.ID, "unlock", "refused", "level")
		return ErrLevel
	}
	// nothing to unlock: the PIN is not spent
	if st, err := s.State(ctx); err == nil && !st.Locked {
		return nil
	}
	if err := s.o.Users.Consume(ctx, pinRequest, cur.ID, UnlockAction); err != nil {
		s.audit(ctx, cur.ID, "unlock", "refused", "pin")
		return ErrPIN
	}
	var was bool
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var locked int
		if err := tx.QueryRowContext(ctx, `SELECT locked FROM channel_lock WHERE id = 1`).Scan(&locked); err != nil {
			return err
		}
		if was = locked == 1; !was {
			return nil
		}
		_, err := tx.ExecContext(ctx, `UPDATE channel_lock SET locked = 0, by_user = ?, at = ? WHERE id = 1`, cur.ID, s.o.Clock.Now().UnixMilli())
		return err
	})
	if err != nil {
		return fmt.Errorf("lockdown: %w", err)
	}
	s.audit(ctx, cur.ID, "unlock", "accepted", "")
	if was {
		s.changed(false)
		s.tellAll(ctx, fmt.Sprintf("Sistema: %s destravou o Mestre às %s.", cur.Name, s.o.Clock.Now().In(s.o.Location).Format("15:04")))
	}
	return nil
}
