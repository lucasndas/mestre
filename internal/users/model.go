package users

import (
	"errors"
	"slices"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
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
	ID, Name string
	// Login is the login name of the panel (empty until the first access).
	Login string
	// Language is the language of the panel the user chose ("" until chosen).
	Language string
	// TelegramLanguage is the last language_code the Telegram sent for the user (a
	// hint; it is never written to Language).
	TelegramLanguage string
	// LastWorkspace is the workspace the user opened last in the panel ("" if none).
	LastWorkspace string
	Level         Level
	State         State
	TelegramID    int64
	GitHubLogin   string
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

// MayHoldGitHub says whether the user may have a personal GitHub credential:
// active, at a level that connects, and acting in some workspace (the Owner acts
// in all). A user who lost any of these has no credential in use, and the one
// he had is taken away.
func (u User) MayHoldGitHub() bool {
	return u.State == Active && Can(u.Level, ConnectOwnGitHub) && (u.Level == Owner || len(u.Workspaces) > 0)
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
	ErrBadCode     = errors.New("users: invalid or expired code")
	ErrNotPrivate  = errors.New("users: the pairing is only in a private chat")
	ErrWrongID     = errors.New("users: the Telegram id is not the one of the registration")
	ErrNoPIN       = errors.New("users: the user has no PIN")
	ErrPINLocked   = errors.New("users: the PIN is locked")
	ErrBadPIN      = errors.New("users: wrong PIN")
	ErrPINRequest  = errors.New("users: the request of PIN is over")
	ErrNoTransfer  = errors.New("users: no transfer waits")
	ErrTransferBad = errors.New("users: the transfer is not possible")
)

// Languages are the languages of the panel.
var Languages = []string{"pt-BR", "en"}

// MatchLanguage turns a hint of language into one of Languages: it is i18n.Match,
// the one rule (docs/plataforma/idioma-por-canal.md#a-regra-única).
func MatchLanguage(tag string) string { return i18n.Match(tag) }

// LanguageFor is the language to speak to the user: the one he chose, or else the
// match of the hint.
func (u User) LanguageFor(hint string) string {
	if slices.Contains(Languages, u.Language) {
		return u.Language
	}
	return MatchLanguage(hint)
}

// ErrBadLanguage is the refusal of a language the panel does not have.
var ErrBadLanguage = errors.New("users: not a language of the panel")
