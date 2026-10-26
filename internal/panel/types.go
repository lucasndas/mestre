package panel

import "time"

// The payloads of the API of the login. tygo turns them into
// web/src/gen/panel.ts (go generate in this package); the tags are the contract.

// Status is GET /api/status: what the front end needs before any login.
type Status struct {
	// SetupOpen is true while the Guild has no user: the front end opens the
	// wizard of the Owner instead of the login.
	SetupOpen bool `json:"setup_open"`
}

// SetupRequest is POST /api/setup (the wizard of the Owner).
type SetupRequest struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	Login    string `json:"login"`
	Password string `json:"password"`
	PIN      string `json:"pin"`
}

// LoginRequest is POST /api/login.
type LoginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

// FirstAccessRequest is POST /api/first-access: the first access of a user, or
// the new password after a reset (the code says which). The PIN is only for the
// first access.
type FirstAccessRequest struct {
	Code     string `json:"code"`
	Login    string `json:"login"`
	Password string `json:"password"`
	PIN      string `json:"pin,omitempty"`
}

// PasswordRequest is POST /api/password.
type PasswordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// AccessCodeRequest is POST /api/users/{id}/access-code. Kind is "first" or
// "reset". The PIN of the Owner goes in the header PINHeader.
type AccessCodeRequest struct {
	Kind string `json:"kind"`
}

// AccessCodeResponse carries the code, which is shown once and never kept.
type AccessCodeResponse struct {
	Code      string    `json:"code"`
	Kind      string    `json:"kind"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Me is GET /api/me (and the answer of setup, login and first access).
type Me struct {
	User         UserInfo    `json:"user"`
	Session      SessionInfo `json:"session"`
	Capabilities []string    `json:"capabilities"`
	// Timezone is the IANA name of the Guild time zone: every time the panel
	// shows is in it.
	Timezone string `json:"timezone"`
}

// LanguageRequest is PUT /api/me/language: "pt-BR" or "en".
type LanguageRequest struct {
	Language string `json:"language"`
}

// UserInfo is the user as the panel shows him.
type UserInfo struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Login      string   `json:"login"`
	Level      string   `json:"level"`
	LevelName  string   `json:"level_name"`
	Workspaces []string `json:"workspaces"`
	HasPIN     bool     `json:"has_pin"`
	// Language is the language the user chose ("" until chosen: the browser decides).
	Language string `json:"language"`
}

// SessionInfo is a session of the panel.
type SessionInfo struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	UserName   string    `json:"user_name,omitempty"`
	Device     string    `json:"device"`
	IP         string    `json:"ip"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	Current    bool      `json:"current"`
}

// SessionList is GET /api/sessions and GET /api/admin/sessions.
type SessionList struct {
	Sessions []SessionInfo `json:"sessions"`
}
