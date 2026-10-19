package users

import (
	"errors"
	"time"
)

// State is the state of a user.
type State string

// The states.
const (
	Active    State = "active"
	Suspended State = "suspended"
	Removed   State = "removed"
)

// User is a user of the Mestre.
type User struct {
	ID, Name    string
	Level       Level
	State       State
	TelegramID  int64
	GitHubLogin string
	// HasPIN says the user set a PIN; the hash never leaves the package.
	HasPIN bool
	// PINLockedUntil is when the lock of the PIN ends (zero: not locked).
	PINLockedUntil time.Time
	Workspaces     []string
	CreatedAt      time.Time
}

// Acts says whether the user acts in the workspace: the Owner in all, the
// others in their list; a suspended or removed user in none.
func (u User) Acts(workspace string) bool {
	if u.State != Active {
		return false
	}
	if u.Level == Owner {
		return true
	}
	for _, w := range u.Workspaces {
		if w == workspace {
			return true
		}
	}
	return false
}

// The errors.
var (
	ErrNoUser        = errors.New("users: no such user")
	ErrOwnerExists   = errors.New("users: there is already an Owner")
	ErrBadLevel      = errors.New("users: not a level")
	ErrSelf          = errors.New("users: the Owner can not suspend or remove self: transfer the post first")
	ErrTelegramTaken = errors.New("users: the Telegram id belongs to another user")
	// ErrSilent is a refusal the bot does not answer (too many wrong codes).
	ErrSilent = errors.New("users: too many attempts")
	// ErrBadCode is the answer to a wrong, expired or used code.
	ErrBadCode     = errors.New("código inválido ou expirado")
	ErrNotPrivate  = errors.New("users: the pairing is only in a private chat")
	ErrWrongID     = errors.New("users: the Telegram id is not the one of the registration")
	ErrNoPIN       = errors.New("users: the user has no PIN")
	ErrPINLocked   = errors.New("users: the PIN is locked")
	ErrBadPIN      = errors.New("users: wrong PIN")
	ErrPINRequest  = errors.New("users: the request of PIN is over")
	ErrNoTransfer  = errors.New("users: no transfer waits")
	ErrTransferBad = errors.New("users: the transfer is not possible")
)
