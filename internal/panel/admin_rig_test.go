package panel_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/vault"
)

// memKeys keeps the key of the vault in memory.
type memKeys struct {
	mu   sync.Mutex
	data []byte
}

func (m *memKeys) Load(context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		return nil, vault.ErrNoKey
	}
	return slices.Clone(m.data), nil
}

func (m *memKeys) Save(_ context.Context, d []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = slices.Clone(d)
	return nil
}

// fakeGitHub is the device flow of GitHub as the panel sees it: what the next
// answer is is set by the test; the discarding of a token of another account is
// proved in internal/github (CA-GH-APROV-06).
type fakeGitHub struct {
	mu     sync.Mutex
	status map[string]github.Status
	begin  error
	poll   []pollAnswer
	begun  []string
}

type pollAnswer struct {
	res github.PollResult
	err error
}

func (f *fakeGitHub) Begin(_ context.Context, id string) (github.Challenge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.begin != nil {
		return github.Challenge{}, f.begin
	}
	f.begun = append(f.begun, id)
	return github.Challenge{UserCode: "WDJB-MJHT", VerificationURI: "https://github.com/login/device", ExpiresAt: time.Date(2026, 10, 26, 9, 15, 0, 0, time.UTC), Interval: 5 * time.Second}, nil
}

func (f *fakeGitHub) Poll(_ context.Context, id string) (github.PollResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.poll) == 0 {
		return github.PollPending, nil
	}
	a := f.poll[0]
	f.poll = f.poll[1:]
	if a.err == nil && a.res == github.PollConnected {
		f.status[id] = github.Status{State: github.CredentialConnected, Login: "lucas-gh", ExpiresAt: time.Date(2026, 10, 26, 17, 0, 0, 0, time.UTC)}
	}
	return a.res, a.err
}

func (f *fakeGitHub) Status(_ context.Context, id string) (github.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status[id], nil
}

// adminRig is the rig with a real vault and the fake device flow behind the panel.
type adminRig struct {
	*rig
	vault *vault.Vault
	rk    *vault.RecoveryKey
	env   string
	gh    *fakeGitHub
	// told counts the times the Towers were told the vault changed.
	told atomic.Int32
}

func newAdmin(t *testing.T) *adminRig {
	t.Helper()
	r := newRig(t)
	dir := t.TempDir()
	rk, err := vault.NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(dir, "cofre-envelope.bin")
	v, err := vault.Create(r.ctx, vault.Config{DB: r.db, Keys: &memKeys{}, EnvelopePath: env, RecoveryPublicKey: rk.PublicKey(), Masker: logging.NewMasker(), Clock: r.clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	a := &adminRig{rig: r, vault: v, rk: rk, env: env, gh: &fakeGitHub{status: map[string]github.Status{}}}
	r.panel, err = panel.New(panel.Options{DB: r.db, Clock: r.clk, Users: r.users, Alerts: r.al, Assets: r.assets, Machine: "m1", Vault: v, GitHub: a.gh,
		VaultChanged: func(context.Context) { a.told.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.panel.Close)
	if _, err := r.db.Writer().ExecContext(r.ctx, `INSERT INTO guild (id, guild_id, ca_cert, epoch, epoch_certificate, timezone, recovery_public_key, created_at) VALUES (1, 'g', x'00', 1, '', 'UTC', ?, 0)`, rk.PublicKey()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Writer().ExecContext(r.ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at) VALUES ('loja', 'o/loja', 'o/loja', 'u', 'main', 'active', 'done', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	return a
}

// auditText is every line of the audit trail as one text, for the checks that
// something is not in it.
func (r *rig) auditText() string {
	r.t.Helper()
	rows, err := r.db.Reader().QueryContext(r.ctx, `SELECT action || ' ' || detail FROM audit_log`)
	if err != nil {
		r.t.Fatal(err)
	}
	defer rows.Close()
	var all string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			r.t.Fatal(err)
		}
		all += s + "\n"
	}
	return all
}

func (r *rig) wantOK(w *httptest.ResponseRecorder) {
	r.t.Helper()
	if w.Code < 200 || w.Code > 299 {
		r.t.Fatalf("answer = %d %s", w.Code, w.Body)
	}
}

func decodeJSON(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("body %q: %v", w.Body, err)
	}
}

// loginAs opens a session of a user who has a password and returns the token.
func loginAs(r *rig, login, password string) string {
	r.t.Helper()
	w := r.login(login, password, opts{})
	if w.Code != 200 {
		r.t.Fatalf("login of %s: %d %s", login, w.Code, w.Body)
	}
	return token(r.t, w)
}
