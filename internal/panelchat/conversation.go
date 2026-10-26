package panelchat

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// The ids of the entries of the conversation.
const (
	prefixIn  = "in-"
	prefixOut = "out-"
	prefixAsk = "ask-"
)

// question is a row of choose_questions.
type question struct {
	ID        int64
	Workspace string
	Mesa      string
	UserID    string
	Channel   string
	Text      string
	Options   []string
	State     string
	Answer    string
	At        time.Time
}

func (s *Service) questions(ctx context.Context, workspace, user string) ([]question, error) {
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT id, workspace, mesa, user_id, channel, question, options, state, answer, created_at FROM choose_questions
		WHERE workspace = ? AND user_id = ? ORDER BY id DESC LIMIT ?`, workspace, user, history)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []question
	for rows.Next() {
		q, err := scanQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanQuestion(sc rowScanner) (question, error) {
	var q question
	var opts string
	var at int64
	if err := sc.Scan(&q.ID, &q.Workspace, &q.Mesa, &q.UserID, &q.Channel, &q.Text, &opts, &q.State, &q.Answer, &at); err != nil {
		return q, err
	}
	if err := json.Unmarshal([]byte(opts), &q.Options); err != nil {
		return q, fmt.Errorf("question %d: %w", q.ID, err)
	}
	q.At = time.UnixMilli(at).UTC()
	return q, nil
}

// canSeeProgress says the user is one of those the message of progress is for: the Owner
// and the Administrators who act in the workspace.
func canSeeProgress(u users.User, workspace string) bool {
	return (u.Level == users.Owner || u.Level == users.Administrator) && u.Acts(workspace)
}

func (s *Service) progress(ctx context.Context, workspace string) (*panel.ChatProgress, error) {
	var text string
	var at int64
	err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT text, updated_at FROM panel_chat_progress WHERE workspace = ?`, workspace).Scan(&text, &at)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := []string{}
	for _, l := range strings.Split(s.mask(text), "\n") {
		if l = strings.TrimRight(l, " \t\r"); l != "" {
			lines = append(lines, l)
		}
	}
	return &panel.ChatProgress{Lines: lines, UpdatedAt: time.UnixMilli(at).UTC()}, nil
}

// Conversation implements panel.ChatSource: the conversation of the user with the Grão-Mestre
// of the workspace, oldest first. It holds what the user wrote on any channel, what the
// Grão-Mestre answered him, and the questions asked of him, never anything of another user.
func (s *Service) Conversation(ctx context.Context, u users.User, workspace string) (panel.ChatConversation, error) {
	if !u.Acts(workspace) {
		return panel.ChatConversation{}, notFound()
	}
	per, n := s.Limits()
	out := panel.ChatConversation{Workspace: workspace, CanTalk: users.Can(u.Level, users.TalkToGrandMaster), Entries: []panel.ChatEntry{}, Limit: per, MaxFiles: n}
	out.Status = s.Status(ctx, workspace)
	items, err := s.o.Inbox.UserItems(ctx, workspace, u.ID, history)
	if err != nil {
		return out, err
	}
	outs, err := s.o.Inbox.OutboundOf(ctx, workspace, u.ID, history)
	if err != nil {
		return out, err
	}
	asks, err := s.questions(ctx, workspace, u.ID)
	if err != nil {
		return out, err
	}
	mark, err := s.readMark(ctx, u.ID, workspace)
	if err != nil {
		return out, err
	}
	if canSeeProgress(u, workspace) {
		if out.Progress, err = s.progress(ctx, workspace); err != nil {
			return out, err
		}
	}

	byID := map[string]*panel.ChatEntry{}
	var entries []panel.ChatEntry
	add := func(e panel.ChatEntry) { entries = append(entries, e) }
	for _, it := range items {
		o := it.Origin
		e := panel.ChatEntry{ID: prefixIn + strconv.FormatInt(seqOf(it.ID), 10), From: "user", Channel: o.Channel, At: it.CreatedAt, Table: o.Table,
			Text: s.mask(withoutFileLines(it.Text, it.Attachments)), Waiting: it.State == inbox.Queued || it.State == inbox.Delivering}
		for _, a := range it.Attachments {
			e.Attachments = append(e.Attachments, panel.ChatAttachment{Name: path.Base(strings.ReplaceAll(a, "\\", "/"))})
		}
		if o.Channel == inbox.ChannelPanel {
			e.Quote = quoteRef(o.RepliedTo) // the id of an entry; its text is filled below
		}
		add(e)
	}
	for _, o := range outs {
		e := panel.ChatEntry{ID: prefixOut + strconv.FormatInt(o.ID, 10), From: "grand_master", Channel: o.Channel, At: o.CreatedAt, Text: s.mask(o.Text),
			Unread: o.Channel == inbox.ChannelPanel && o.ID > mark}
		if seq := seqOf(o.Message); seq > 0 {
			e.Quote = quoteRef(prefixIn + strconv.FormatInt(seq, 10))
		}
		for i, a := range o.Attachments {
			at := panel.ChatAttachment{Name: path.Base(strings.ReplaceAll(a, "\\", "/"))}
			if s.o.Open != nil && o.Channel == inbox.ChannelPanel {
				at.Download = fmt.Sprintf("/api/chat/%s/files/%d/%d", workspace, o.ID, i)
			}
			e.Attachments = append(e.Attachments, at)
		}
		if e.Unread {
			out.Unread++
		}
		add(e)
	}
	for _, q := range asks {
		add(panel.ChatEntry{ID: prefixAsk + strconv.FormatInt(q.ID, 10), From: "question", Channel: q.Channel, At: q.At, Table: q.Mesa, Text: s.mask(q.Text),
			Question: &panel.ChatQuestion{ID: q.ID, Options: maskAll(s.mask, q.Options), State: q.State, Answer: s.mask(q.Answer)}})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].At.Equal(entries[j].At) {
			return entries[i].At.Before(entries[j].At)
		}
		return entries[i].ID < entries[j].ID
	})
	for i := range entries {
		byID[entries[i].ID] = &entries[i]
	}
	// the quote shows the start of what it quotes, when that is in the window of the history
	for i := range entries {
		q := entries[i].Quote
		if q == nil {
			continue
		}
		if t, ok := byID[q.ID]; ok {
			q.Excerpt, q.Mine = excerpt(t.Text), t.From == "user"
		} else {
			entries[i].Quote = nil
		}
	}
	if entries == nil {
		entries = []panel.ChatEntry{} // JSON: [] and never null
	}
	out.Entries = entries
	return out, nil
}

func quoteRef(id string) *panel.ChatQuote {
	if id == "" {
		return nil
	}
	return &panel.ChatQuote{ID: id}
}

func maskAll(mask func(string) string, in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = mask(v)
	}
	return out
}

func seqOf(id string) int64 {
	if len(id) < 2 || id[0] != 'm' {
		return 0
	}
	n, err := strconv.ParseInt(id[1:], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// withoutFileLines takes the lines `[anexo] <path> …` the engine writes for the files out of the
// text of a message: the files are shown as such.
func withoutFileLines(text string, files []string) string {
	if len(files) == 0 {
		return text
	}
	var keep []string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "[anexo] ") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}
