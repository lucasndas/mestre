// Package panelchat is the conversation of the users with the Grão-Mestre in the panel
// (docs/painel/conversa-e-aprovacoes.md#conversa-com-o-grão-mestre): one more channel of
// the inbox. A message of the panel enters the inbox of the Grão-Mestre with the origin
// "painel" and the priority of a message of a user; the answer of `mestre reply` comes
// back to the user who wrote it, and is kept when he is away; the question of `mestre
// choose` and the message of progress are shown in the conversation too.
//
// The history is not copied: it is read from the inbox (the messages of the user, on any
// channel) and from the answers kept for the channels (outbound_messages), always filtered
// by the user the session says. What the package keeps is what the panel adds: the read
// mark of each user, the card of progress, and the questions answered here.
package panelchat

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mestre-ia/mestre/internal/attachments"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const (
	// MaxFiles is how many files go in one message.
	MaxFiles = 5
	// MaxText is the longest text of a message, in characters.
	MaxText = 20000
	// history is how many messages of each kind the conversation shows.
	history = 200
	// sendsPerMinute is how many messages one user may send in a minute.
	sendsPerMinute = 30
	excerptLen     = 120
)

// ErrNoGrandMaster is what Options.Enqueue says when the workspace has no Grão-Mestre now.
var ErrNoGrandMaster = errors.New("panelchat: the workspace has no Grand Master now")

// Directory lists the users (users.Service).
type Directory interface {
	List(ctx context.Context) ([]users.User, error)
	Get(ctx context.Context, id string) (users.User, error)
}

// Options are the pieces of the Service. Everything the engine builds late (the inbox, the
// registry of the characters, the attachments) comes as a function, so the Service can be made
// before them.
type Options struct {
	DB    *store.DB
	Clock clock.Clock
	Users Directory
	// Inbox reads the history.
	Inbox *inbox.Store
	// Enqueue puts an item in the inbox of its target (inbox.Service.Enqueue).
	Enqueue func(ctx context.Context, n inbox.NewItem) (inbox.Item, error)
	// GrandMaster and Pending give the state of the Grão-Mestre and what waits in his inbox.
	GrandMaster func(ctx context.Context, workspace string) (characters.Character, bool, error)
	Character   func(ctx context.Context, id string) (characters.Character, error)
	Pending     func(ctx context.Context, id string) (int, error)
	// Receive keeps an attachment of the panel and Limit is the biggest one.
	Receive func(ctx context.Context, r attachments.Request, data []byte) (attachments.Stored, error)
	Limit   func() int64
	// Locked says the Mestre is locked: what is sent is kept until it is not.
	Locked func(ctx context.Context) bool
	// Mask takes the secrets out of every text that leaves, and is the masker of the filter of
	// secrets of what the user writes (internal/secretscreen, the one of the Telegram). Nil: none.
	Mask func(string) string
	// AuditSecret writes the attempt to send a secret to the audit (never the text); how is the kind.
	AuditSecret func(ctx context.Context, user, how string)
	// AlertOwner tells the Owner a user is sending secrets again and again (nil: nobody).
	AlertOwner func(ctx context.Context, text string)
	// Discard takes back an attachment kept for a message that was not delivered (nil: it stays).
	Discard func(ctx context.Context, workspace string, st attachments.Stored) error
	// Open opens a file the Grão-Mestre sent (nil: the files are not there to download yet).
	Open   func(ctx context.Context, workspace string, o inbox.Outbound, index int) (panel.ChatDownload, error)
	Logger *slog.Logger
}

// Service is the conversation. It is a panel.ChatSource and the inbox.Channels of the panel.
type Service struct {
	o   Options
	clk clock.Clock
	log *slog.Logger

	mu        sync.Mutex
	next      int
	listeners map[int]func(panel.ChatEvent)
	sends     map[string][]time.Time
	// hits are the moments secrets of each user were caught, followUp when the next message of a
	// user is held too, alerted when the Owner was last told, and uploads the messages being received.
	hits     map[string][]time.Time
	followUp map[string]time.Time
	alerted  map[string]time.Time
	uploads  map[string]int
}

var _ panel.ChatSource = (*Service)(nil)

// New returns the Service.
func New(o Options) (*Service, error) {
	switch {
	case o.DB == nil || o.Inbox == nil || o.Users == nil:
		return nil, errors.New("panelchat: the database, the inbox and the users are required")
	case o.Enqueue == nil || o.GrandMaster == nil || o.Pending == nil || o.Character == nil || o.Receive == nil || o.Limit == nil:
		return nil, errors.New("panelchat: the inbox, the characters and the attachments are required")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Mask == nil {
		o.Mask = func(s string) string { return s }
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{o: o, clk: o.Clock, log: log, listeners: map[int]func(panel.ChatEvent){}, sends: map[string][]time.Time{},
		hits: map[string][]time.Time{}, followUp: map[string]time.Time{}, alerted: map[string]time.Time{}, uploads: map[string]int{}}, nil
}

// enqueue puts an item in the inbox of the Grão-Mestre of its workspace (the target when it
// has one); ErrNoGrandMaster when there is none.
func (s *Service) enqueue(ctx context.Context, n inbox.NewItem) (inbox.Item, error) {
	if n.Target == "" {
		gm, ok, err := s.o.GrandMaster(ctx, n.Workspace)
		if err != nil {
			return inbox.Item{}, err
		}
		if !ok {
			return inbox.Item{}, ErrNoGrandMaster
		}
		n.Target = gm.ID
	}
	return s.o.Enqueue(ctx, n)
}

func (s *Service) mask(t string) string { return s.o.Mask(t) }

func (s *Service) discard(ctx context.Context, workspace string, st attachments.Stored) error {
	if s.o.Discard == nil {
		return nil
	}
	return s.o.Discard(ctx, workspace, st)
}

// Limits implements panel.ChatSource.
func (s *Service) Limits() (int64, int) { return s.o.Limit(), MaxFiles }

// Listen implements panel.ChatSource.
func (s *Service) Listen(fn func(panel.ChatEvent)) func() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	id := s.next
	s.listeners[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.listeners, id)
	}
}

func (s *Service) listening() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.listeners) > 0
}

func (s *Service) emit(e panel.ChatEvent) {
	s.mu.Lock()
	fns := make([]func(panel.ChatEvent), 0, len(s.listeners))
	for _, fn := range s.listeners {
		fns = append(fns, fn)
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn(e)
	}
}

// audience is the users who follow the conversation of a workspace: those who act in it.
func (s *Service) audience(ctx context.Context, workspace string, only func(users.User) bool) []users.User {
	all, err := s.o.Users.List(ctx)
	if err != nil {
		s.log.Warn("panelchat: reading the users", "err", err)
		return nil
	}
	var out []users.User
	for _, u := range all {
		if u.Acts(workspace) && (only == nil || only(u)) {
			out = append(out, u)
		}
	}
	return out
}

func notFound() *panel.APIError { return panel.Err(panel.CodeNotFound, "não encontrado", nil) }

// Overview implements panel.ChatSource.
func (s *Service) Overview(ctx context.Context, u users.User) (panel.ChatOverview, error) {
	per, n := s.Limits()
	out := panel.ChatOverview{Workspaces: []panel.ChatWorkspace{}, Limit: per, MaxFiles: n}
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT slug FROM workspaces WHERE state = 'active' ORDER BY slug`)
	if err != nil {
		return out, err
	}
	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			_ = rows.Close()
			return out, err
		}
		if u.Acts(slug) {
			slugs = append(slugs, slug)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return out, err
	}
	_ = rows.Close()
	unread, err := s.unreadByWorkspace(ctx, u.ID)
	if err != nil {
		return out, err
	}
	can := users.Can(u.Level, users.TalkToGrandMaster)
	for _, slug := range slugs {
		out.Workspaces = append(out.Workspaces, panel.ChatWorkspace{Slug: slug, CanTalk: can, Unread: unread[slug]})
		out.Unread += unread[slug]
	}
	return out, nil
}

// Unread implements panel.ChatSource.
func (s *Service) Unread(ctx context.Context, u users.User) (int, error) {
	m, err := s.unreadByWorkspace(ctx, u.ID)
	n := 0
	for ws, c := range m {
		if u.Acts(ws) {
			n += c
		}
	}
	return n, err
}

// unreadByWorkspace counts the answers kept for the panel after the read mark of the user.
func (s *Service) unreadByWorkspace(ctx context.Context, user string) (map[string]int, error) {
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT o.workspace, COUNT(*) FROM outbound_messages o
		LEFT JOIN panel_chat_reads r ON r.user_id = o.user_id AND r.workspace = o.workspace
		WHERE o.user_id = ? AND o.channel = ? AND o.id > COALESCE(r.outbound_id, 0) GROUP BY o.workspace`, user, inbox.ChannelPanel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var ws string
		var n int
		if err := rows.Scan(&ws, &n); err != nil {
			return nil, err
		}
		out[ws] = n
	}
	return out, rows.Err()
}

func (s *Service) readMark(ctx context.Context, user, workspace string) (int64, error) {
	var id int64
	err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT COALESCE(MAX(outbound_id), 0) FROM panel_chat_reads WHERE user_id = ? AND workspace = ?`, user, workspace).Scan(&id)
	return id, err
}

// Read implements panel.ChatSource.
func (s *Service) Read(ctx context.Context, u users.User, workspace string) error {
	if !u.Acts(workspace) {
		return notFound()
	}
	var top int64
	if err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM outbound_messages WHERE workspace = ? AND user_id = ? AND channel = ?`,
		workspace, u.ID, inbox.ChannelPanel).Scan(&top); err != nil {
		return err
	}
	prev, err := s.readMark(ctx, u.ID, workspace)
	if err != nil || top <= prev {
		return err
	}
	err = s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO panel_chat_reads (user_id, workspace, outbound_id, read_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(user_id, workspace) DO UPDATE SET outbound_id = MAX(outbound_id, excluded.outbound_id), read_at = excluded.read_at`,
			u.ID, workspace, top, s.clk.Now().UnixMilli())
		return err
	})
	if err == nil {
		s.emit(panel.ChatEvent{User: u.ID, Workspace: workspace, Kind: panel.EventChatChanged})
	}
	return err
}

// Status says the state of the Grão-Mestre of a workspace.
func (s *Service) Status(ctx context.Context, workspace string) panel.ChatStatus {
	st := panel.ChatStatus{State: "none"}
	if s.o.Locked != nil {
		st.Locked = s.o.Locked(ctx)
	}
	gm, ok, err := s.o.GrandMaster(ctx, workspace)
	if err != nil {
		s.log.Warn("panelchat: reading the Grand Master", "workspace", workspace, "err", err)
		return st
	}
	if !ok {
		return st
	}
	if n, err := s.o.Pending(ctx, gm.ID); err == nil {
		st.Ahead = n
	}
	switch gm.State {
	case characters.Idle, characters.Waiting:
		st.State = "idle"
	case characters.Interrupted:
		st.State, st.Reason = "unavailable", s.mask(gm.Reason)
	default:
		st.State = "busy"
	}
	return st
}

// Changed is what the inbox tells when the inbox or the state of a character changes: the
// state of the Grão-Mestre goes to the users who follow his workspace.
func (s *Service) Changed(ctx context.Context, target string) {
	if !s.listening() {
		return
	}
	c, err := s.o.Character(ctx, target)
	if err != nil || c.Role != characters.RoleGrandMaster {
		return
	}
	s.emitStatus(ctx, c.Workspace)
}

func (s *Service) emitStatus(ctx context.Context, workspace string) {
	if !s.listening() {
		return
	}
	st := s.Status(ctx, workspace)
	for _, u := range s.audience(ctx, workspace, nil) {
		s.emit(panel.ChatEvent{User: u.ID, Workspace: workspace, Kind: panel.EventChatStatus, Status: &st})
	}
}

// LockChanged is told when the Mestre is locked or unlocked: every workspace shows it.
func (s *Service) LockChanged(ctx context.Context) {
	if !s.listening() {
		return
	}
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT slug FROM workspaces WHERE state = 'active'`)
	if err != nil {
		return
	}
	var slugs []string
	for rows.Next() {
		var slug string
		if rows.Scan(&slug) == nil {
			slugs = append(slugs, slug)
		}
	}
	_ = rows.Close()
	for _, slug := range slugs {
		s.emitStatus(ctx, slug)
	}
}

func oneLine(v string) string { return strings.Join(strings.Fields(v), " ") }

func excerpt(v string) string {
	v = oneLine(v)
	if utf8.RuneCountInString(v) > excerptLen {
		return string([]rune(v)[:excerptLen]) + "…"
	}
	return v
}

var clientIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
