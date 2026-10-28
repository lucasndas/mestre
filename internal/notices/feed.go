// Package notices is the feed of the bell of the panel (docs/painel/conversa-e-aprovacoes.md#avisos):
// what the engine tells the users on the Telegram (the notifications of the Grão-Mestre,
// the operational alerts, the "Sistema" messages, the alert of an unknown sender, the
// GitHub that is down) is also kept here, once, for the panel to show with its
// workspace and table, the time and a link, and the read marks of each user.
package notices

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Kind is where a notice comes from.
type Kind string

// The kinds of notice.
const (
	// Notify is `mestre notify` without --blocked: a message of the Grão-Mestre.
	Notify Kind = "notify"
	// Blocked is `mestre notify --blocked`: a table stopped.
	Blocked Kind = "blocked"
	// Alert is an operational alert of the engine (self-healing, a stalled step, a
	// deadlock, a usage limit).
	Alert Kind = "alert"
	// System is a message "Sistema": no workspace.
	System Kind = "system"
	// Stranger is the alert of a sender the bot does not know.
	Stranger Kind = "stranger"
	// Approval is "Nova aprovação": a request waits on the decision of the user.
	Approval Kind = "approval"
	// GitHubDown is "GitHub fora" and its return.
	GitHubDown Kind = "github"
)

// Audience says who sees a notice.
type Audience string

// The audiences.
const (
	// Workspace: the users who act in the workspace and may be notified.
	Workspace Audience = "workspace"
	// Admins: the Owner and the Administrators who act in the workspace (any
	// workspace when it is empty).
	Admins Audience = "admins"
	// Owner: the Owner.
	Owner Audience = "owner"
	// User: the one user of Notice.UserID.
	User Audience = "user"
)

// The kinds of link: where a notice leads in the panel.
const (
	LinkNone     = ""
	LinkRequest  = "request"
	LinkTable    = "table"
	LinkBlocking = "blocking"
	LinkMachine  = "machine"
)

// Notice is an entry of the feed. Read is of the user it was read for.
type Notice struct {
	ID        int64
	Kind      Kind
	Workspace string
	// Mesa is the id of the table ("" for a notice of the system or of the workspace).
	Mesa     string
	Audience Audience
	// UserID is the user of the audience User.
	UserID   string
	Text     string
	LinkKind string
	LinkRef  string
	At       time.Time
	Read     bool
}

// Visible says whether the user is one of those the notice is for.
func Visible(u users.User, n Notice) bool {
	if u.State != users.Active {
		return false
	}
	switch n.Audience {
	case User:
		return u.ID == n.UserID
	case Owner:
		return u.Level == users.Owner
	case Admins:
		return (u.Level == users.Owner || u.Level == users.Administrator) && (n.Workspace == "" || u.Acts(n.Workspace))
	case Workspace:
		return users.Can(u.Level, users.Notifications) && (n.Workspace == "" || u.Acts(n.Workspace))
	}
	return false
}

// Window is how far back the bell looks: older notices are not listed nor counted.
const Window = 30 * 24 * time.Hour

// keep is how many notices the feed keeps; the oldest go first.
const keep = 2000

// Directory lists the users (users.Service).
type Directory interface {
	List(ctx context.Context) ([]users.User, error)
}

// Feed is the store of the notices.
type Feed struct {
	DB    *store.DB
	Clock clock.Clock
	Users Directory
	// Mask takes the secrets out of a text before it is kept: the notice is the text
	// the Telegram sends, and the bot masks that too (nil: none).
	Mask func(string) string

	mu           sync.Mutex
	nextListener int
	listeners    []listener
}

// OnAdd registers what is told when a notice is made (the panel updates the bell of
// who sees it): the notice and the users who see it.
func (f *Feed) OnAdd(fn func(ctx context.Context, n Notice, to []users.User)) (cancel func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextListener++
	id := f.nextListener
	f.listeners = append(f.listeners, listener{id, fn})
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.listeners = slices.DeleteFunc(f.listeners, func(l listener) bool { return l.id == id })
	}
}

type listener struct {
	id int
	fn func(context.Context, Notice, []users.User)
}

func (f *Feed) tell(ctx context.Context, n Notice, to []users.User) {
	f.mu.Lock()
	ls := slices.Clone(f.listeners)
	f.mu.Unlock()
	for _, l := range ls {
		l.fn(ctx, n, to)
	}
}

// Audience is who sees the notice now.
func (f *Feed) audience(ctx context.Context, n Notice) ([]users.User, error) {
	all, err := f.Users.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []users.User
	for _, u := range all {
		if Visible(u, n) {
			out = append(out, u)
		}
	}
	return out, nil
}

// Reach is how many users a notice of this audience and workspace reaches now,
// without making it: the `delivered.panel` of `mestre notify`.
func (f *Feed) Reach(ctx context.Context, n Notice) (int, error) {
	if n.Audience == "" {
		n.Audience = Workspace
	}
	to, err := f.audience(ctx, n)
	return len(to), err
}

// ErrEmpty is a notice with no text.
var ErrEmpty = errors.New("notices: the notice has no text")

func (f *Feed) clean(text string) string {
	if f.Mask != nil {
		text = f.Mask(text)
	}
	return strings.TrimSpace(text)
}

// Add keeps a notice, masked, and returns its number and how many users see it.
func (f *Feed) Add(ctx context.Context, n Notice) (id int64, seen int, err error) {
	n.Text = f.clean(n.Text)
	if n.Text == "" {
		return 0, 0, ErrEmpty
	}
	if n.Audience == "" {
		n.Audience = Workspace
	}
	n.At = f.Clock.Now().UTC()
	err = f.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO panel_notices (kind, workspace, mesa, audience, user_id, text, link_kind, link_ref, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			string(n.Kind), n.Workspace, n.Mesa, string(n.Audience), n.UserID, n.Text, n.LinkKind, n.LinkRef, n.At.UnixMilli())
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM panel_notice_reads WHERE notice_id IN (SELECT id FROM panel_notices WHERE id <= ?)`, id-keep); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM panel_notices WHERE id <= ?`, id-keep)
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	n.ID = id
	to, err := f.audience(ctx, n)
	if err != nil {
		return id, 0, err
	}
	f.tell(ctx, n, to)
	return id, len(to), nil
}

// Update replaces the text of a notice (the alert of a stranger counts the tries)
// and tells the listeners again.
func (f *Feed) Update(ctx context.Context, id int64, text string) error {
	text = f.clean(text)
	if text == "" {
		return ErrEmpty
	}
	err := f.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE panel_notices SET text = ? WHERE id = ?`, text, id)
		return err
	})
	if err != nil {
		return err
	}
	n, err := f.get(ctx, id)
	if err != nil {
		return err
	}
	to, err := f.audience(ctx, n)
	if err != nil {
		return err
	}
	f.tell(ctx, n, to)
	return nil
}

const columns = `id, kind, workspace, mesa, audience, user_id, text, link_kind, link_ref, created_at`

func scan(sc interface{ Scan(...any) error }) (Notice, error) {
	var n Notice
	var kind, aud string
	var at int64
	if err := sc.Scan(&n.ID, &kind, &n.Workspace, &n.Mesa, &aud, &n.UserID, &n.Text, &n.LinkKind, &n.LinkRef, &at); err != nil {
		return Notice{}, err
	}
	n.Kind, n.Audience, n.At = Kind(kind), Audience(aud), time.UnixMilli(at).UTC()
	return n, nil
}

func (f *Feed) get(ctx context.Context, id int64) (Notice, error) {
	return scan(f.DB.Reader().QueryRowContext(ctx, `SELECT `+columns+` FROM panel_notices WHERE id = ?`, id))
}

// For lists the notices the user sees, newest first, with what he read; limit is
// at most 200. Notices older than Window are not listed.
func (f *Feed) For(ctx context.Context, u users.User, limit int) ([]Notice, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	since := f.Clock.Now().Add(-Window).UnixMilli()
	rows, err := f.DB.Reader().QueryContext(ctx, `SELECT `+columns+`, EXISTS (SELECT 1 FROM panel_notice_reads r WHERE r.notice_id = n.id AND r.user_id = ?)
		FROM panel_notices n WHERE created_at >= ? ORDER BY id DESC`, u.ID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notice{}
	for rows.Next() {
		var n Notice
		var kind, aud string
		var at int64
		var read bool
		if err := rows.Scan(&n.ID, &kind, &n.Workspace, &n.Mesa, &aud, &n.UserID, &n.Text, &n.LinkKind, &n.LinkRef, &at, &read); err != nil {
			return nil, err
		}
		n.Kind, n.Audience, n.At, n.Read = Kind(kind), Audience(aud), time.UnixMilli(at).UTC(), read
		if !Visible(u, n) {
			continue
		}
		out = append(out, n)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

// Unread is how many notices the user sees and did not read.
func (f *Feed) Unread(ctx context.Context, u users.User) (int, error) {
	all, err := f.For(ctx, u, 200)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range all {
		if !x.Read {
			n++
		}
	}
	return n, nil
}

// MarkRead marks the notices the user sees as read by him (the ones he does not
// see are ignored); with no ids it marks all of them.
func (f *Feed) MarkRead(ctx context.Context, u users.User, ids ...int64) error {
	all, err := f.For(ctx, u, 200)
	if err != nil {
		return err
	}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	now := f.Clock.Now().UnixMilli()
	return f.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		for _, n := range all {
			if n.Read || (len(ids) > 0 && !want[n.ID]) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO panel_notice_reads (user_id, notice_id, read_at) VALUES (?, ?, ?) ON CONFLICT (user_id, notice_id) DO NOTHING`, u.ID, n.ID, now); err != nil {
				return err
			}
		}
		return nil
	})
}
