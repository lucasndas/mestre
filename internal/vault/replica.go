package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/mestre-ia/mestre/internal/logging"
)

// GuardianReplica is the restricted copy sent to the Guardians
// (docs/plataforma/cofre.md#réplica-para-os-guardiões): the bot token, the
// user list and, not secret, the locked state and the bot's last update_id.
// Nothing else from the vault ever goes in it.
type GuardianReplica struct {
	Version      uint64        `json:"version"`
	BotToken     *Sensitive    `json:"bot_token,omitempty"` // absent for an excluded machine
	Users        []ReplicaUser `json:"users"`
	Locked       bool          `json:"locked"`
	LastUpdateID int64         `json:"last_update_id"`
}

// ReplicaUser is one user as the Guardian needs it for the fallback page
// and the reduced Telegram mode.
type ReplicaUser struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Login        string   `json:"login"`
	Level        string   `json:"level"`
	Workspaces   []string `json:"workspaces"`
	TelegramID   int64    `json:"telegram_id"`
	PasswordHash string   `json:"password_hash"`
	PINHash      string   `json:"pin_hash"`
	Active       bool     `json:"active"` // false when suspended
}

// ReplicaInput is what the engine knows outside the vault.
type ReplicaInput struct {
	Version      uint64
	Users        []ReplicaUser
	Locked       bool
	LastUpdateID int64
	// Excluded is true for the Guardian of an excluded machine: it gets the
	// users only, never the token.
	Excluded bool
}

// BotTokenSecret is the vault name of the Telegram bot token.
const BotTokenSecret = "telegram_bot_token"

// BuildGuardianReplica assembles the replica. The only secret it reads is
// the bot token (name BotTokenSecret, type telegram_bot_token), and only
// for a machine that is not excluded.
func (v *Vault) BuildGuardianReplica(ctx context.Context, in ReplicaInput) (GuardianReplica, error) {
	r := GuardianReplica{
		Version:      in.Version,
		Users:        make([]ReplicaUser, len(in.Users)),
		Locked:       in.Locked,
		LastUpdateID: in.LastUpdateID,
	}
	for i, u := range in.Users {
		u.Workspaces = slices.Clone(u.Workspaces)
		r.Users[i] = u
	}
	if in.Excluded {
		return r, nil
	}
	info, err := v.Info(ctx, BotTokenSecret)
	if err != nil {
		return GuardianReplica{}, fmt.Errorf("guardian replica: %w", err)
	}
	if info.Type != TypeTelegramBotToken {
		return GuardianReplica{}, fmt.Errorf("guardian replica: secret %q has type %s, want %s", BotTokenSecret, info.Type, TypeTelegramBotToken)
	}
	err = v.Use(ctx, BotTokenSecret, "guardian replica", func(value []byte) error {
		s := Sensitive(value)
		r.BotToken = &s
		return nil
	})
	if err != nil {
		return GuardianReplica{}, fmt.Errorf("guardian replica: %w", err)
	}
	return r, nil
}

// Sensitive is a secret that must travel (the replica) but never be
// printed: fmt and slog show the redaction marker; JSON carries the value.
type Sensitive string

func (s Sensitive) String() string             { return logging.Redacted }
func (s Sensitive) GoString() string           { return logging.Redacted }
func (s Sensitive) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(logging.Redacted)) }
func (s Sensitive) LogValue() slog.Value       { return slog.StringValue(logging.Redacted) }

// MarshalJSON writes the value: only for the encrypted transport to the
// Guardian.
func (s Sensitive) MarshalJSON() ([]byte, error) { return json.Marshal(string(s)) }

// Reveal returns the value.
func (s Sensitive) Reveal() string { return string(s) }
