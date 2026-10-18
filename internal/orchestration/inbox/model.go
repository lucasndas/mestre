package inbox

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// Kind is the type of an item of the inbox
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#caixa-de-entrada).
type Kind string

// The types of item.
const (
	KindUser     Kind = "user"
	KindAgent    Kind = "agent"
	KindAnswer   Kind = "answer"
	KindQuest    Kind = "quest"
	KindRitual   Kind = "ritual"
	KindGitHub   Kind = "github"
	KindSystem   Kind = "system"
	KindWake     Kind = "wake"
	KindApproval Kind = "approval"
)

// Valid says whether k is one of the types.
func (k Kind) Valid() bool {
	switch k {
	case KindUser, KindAgent, KindAnswer, KindQuest, KindRitual, KindGitHub, KindSystem, KindWake, KindApproval:
		return true
	}
	return false
}

// State is the state of an item.
type State string

// The states. Delivering is the one the document does not name: the item was
// typed and no turn began yet. It is delivered when the turn begins (or when
// the 10 seconds run out: Unconfirmed), and goes back to queued if the engine
// falls before that.
const (
	Queued     State = "queued"
	Delivering State = "delivering"
	Delivered  State = "delivered"
	Expired    State = "expired"
	Cancelled  State = "cancelled"
)

// Priority orders the delivery: the Owner first, the other users, the rest.
type Priority int

// The priorities.
const (
	PriorityOwner Priority = 1
	PriorityUsers Priority = 2
	PriorityRest  Priority = 3
)

// Channels of the origin of a message of a user.
const (
	ChannelTelegram = "telegram"
	ChannelPanel    = "panel"
)

// Origin is where a message of a user came from, kept so the answer goes back
// the same way (docs/orquestracao/esperas-e-caixa-de-entrada.md#resposta-pelo-mesmo-canal).
type Origin struct {
	Channel string `json:"channel"`
	// User is the id of the user and UserName how the header calls them.
	User     string               `json:"user"`
	UserName string               `json:"user_name"`
	Level    characters.UserLevel `json:"level"`
	// Conversation and Message are the ids in the channel; RepliedTo is the
	// message of the channel this one answers.
	Conversation string `json:"conversation,omitempty"`
	Message      string `json:"message,omitempty"`
	RepliedTo    string `json:"replied_to,omitempty"`
	// Table is the table the message cites (the header says "mesa <mesa>").
	Table string `json:"table,omitempty"`
	// Transcribed is true for an audio the engine turned into text.
	Transcribed bool `json:"transcribed,omitempty"`
}

// Refs are what an item refers to.
type Refs struct {
	Ask   string `json:"ask,omitempty"`
	Wait  string `json:"wait,omitempty"`
	Table string `json:"table,omitempty"`
	Quest string `json:"quest,omitempty"`
	// OpID is the operation a `system` item reports the result of.
	OpID string `json:"op_id,omitempty"`
	// Approval is the request of approval an `approval` item (or the answer to
	// its question) is about.
	Approval int64 `json:"approval,omitempty"`
	// Run is the delivery of a quest step (step_runs.id) an item `quest` is
	// about: the engine finds out whether a run got its item.
	Run int64 `json:"run,omitempty"`
	// Key makes the item idempotent: a second item with the same key is not
	// queued, the first one is returned (a ritual firing that is delivered
	// twice after a crash puts one item in the inbox).
	Key string `json:"key,omitempty"`
}

// Item is one item of the inbox of a character.
type Item struct {
	// ID is "m" and the sequence number ("m81").
	ID        string
	Workspace string
	// Target is the character that receives it.
	Target string
	Kind   Kind
	// Sender is the name the header shows; SenderID the character that sent
	// it, when one did.
	Sender, SenderID string
	Text             string
	Attachments      []string
	Origin           *Origin
	Refs             Refs
	Priority         Priority
	State            State
	// Unconfirmed is true for a delivery whose turn never began in time.
	Unconfirmed bool
	CreatedAt   time.Time
	TypedAt     time.Time
	DeliveredAt time.Time
}

// NewItem is what a part of the engine asks to put in an inbox.
type NewItem struct {
	Workspace string
	Target    string
	Kind      Kind
	Sender    string
	SenderID  string
	Text      string
	// Attachments are paths of files.
	Attachments []string
	// Origin is required for a message of a user.
	Origin *Origin
	Refs   Refs
}

// Errors of the inbox.
var (
	// ErrNoItem: no such item.
	ErrNoItem = errors.New("no such item of the inbox")
	// ErrBadItem: the item cannot be queued.
	ErrBadItem = errors.New("the item is not valid")
	// ErrNothingToReply: no message of a user is being handled.
	ErrNothingToReply = errors.New("there is no message of a user to answer")
	// ErrNotGrandMaster: only the Grand Master replies.
	ErrNotGrandMaster = errors.New("only the Grand Master replies to users")
	// ErrChannelDown: the channel of the origin cannot take the answer now.
	ErrChannelDown = errors.New("the channel is not available")
)

// levelName is the level as the header writes it.
func levelName(l characters.UserLevel) string {
	switch l {
	case characters.LevelOwner:
		return "Dono"
	case characters.LevelAdministrator:
		return "Administrador"
	case characters.LevelApprover:
		return "Aprovador"
	case characters.LevelObserver:
		return "Observador"
	}
	return string(l)
}

func channelName(c string) string {
	switch c {
	case ChannelTelegram:
		return "Telegram"
	case ChannelPanel:
		return "painel"
	}
	return c
}

// Header is the first line of every item, the one format of the inbox:
// `[Mestre · <tipo> · de <remetente> · <id>]`; a message of a user has
// `<nome> (<nível>)` as the sender and the optional fields `via <canal>`,
// `mesa <mesa>` and `áudio transcrito` before the id. It is the engine's whoever
// chose the words in it (the name of a quest or of a ritual, the name a user
// gives, the table a message cites): see headerText.
func (it Item) Header() string {
	sender := headerText(it.Sender)
	parts := []string{"Mestre", string(it.Kind)}
	var extra []string
	if o := it.Origin; it.Kind == KindUser && o != nil {
		name := headerText(o.UserName)
		if name == "" {
			name = sender
		}
		sender = fmt.Sprintf("%s (%s)", name, headerText(levelName(o.Level)))
		if o.Channel != "" {
			extra = append(extra, "via "+headerText(channelName(o.Channel)))
		}
		if o.Table != "" {
			extra = append(extra, "mesa "+headerText(o.Table))
		}
		if o.Transcribed {
			extra = append(extra, "áudio transcrito")
		}
	}
	if sender == "" {
		sender = "sistema"
	}
	parts = append(parts, "de "+sender)
	parts = append(parts, extra...)
	parts = append(parts, headerText(it.ID))
	return "[" + strings.Join(parts, " · ") + "]"
}

// headerText makes words that somebody chose safe inside the header: no line
// break, control character or invisible mark (each run of them becomes a space),
// and none of the three marks of the format, `[`, `]` and `·`, which become `(`,
// `)` and `-`. Nothing written in a sender can end the header, break the line or
// open another header on it (docs/orquestracao/esperas-e-caixa-de-entrada.md#caixa-de-entrada).
func headerText(s string) string {
	var b strings.Builder
	gap := false
	for _, r := range s {
		switch {
		case unicode.In(r, unicode.Cc, unicode.Cf, unicode.Zl, unicode.Zp):
			gap = true
			continue
		case r == '[':
			r = '('
		case r == ']':
			r = ')'
		case r == '·':
			r = '-'
		}
		if gap && b.Len() > 0 {
			b.WriteByte(' ')
		}
		gap = false
		b.WriteRune(r)
	}
	return b.String()
}

// PrintableLine says whether s is one line of printable characters, with no line
// break, control character or invisible mark. A name that becomes the sender of
// an item (of a quest, of a ritual) must be, so whoever names it does not get to
// write a line of the engine; Header cleans what is already named otherwise.
func PrintableLine(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return !unicode.IsPrint(r) })
}

// OneLine keeps words that somebody chose (a path, a command, a URL) on their
// own line in a text of the engine: a text with a line break or another control
// character is shown quoted and escaped, so it cannot open a line that looks
// like a field of the engine, a header or the end of a block of text of an agent.
func OneLine(s string) string {
	for _, r := range s {
		if unicode.IsControl(r) || r == '\U00002028' || r == '\U00002029' {
			return strconv.Quote(s)
		}
	}
	return s
}

// Body is the text delivered after the header: the text and, when there are
// attachments, their paths, one per line.
func (it Item) Body() string {
	text := it.Text
	if it.Kind != KindUser {
		text = escapeHeaders(text)
	}
	if len(it.Attachments) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	if it.Text != "" {
		b.WriteString("\n\n")
	}
	b.WriteString("Anexos:")
	for _, a := range it.Attachments {
		b.WriteString("\n- " + OneLine(a))
	}
	return b.String()
}

// headerStart is how every header begins.
const headerStart = "[Mestre ·"

// escapeHeaders makes a line that imitates a header harmless: the text of an
// item that is not from a user may carry words of an agent, and a line
// `[Mestre · …]` in it must not read as the header of another item. The line
// keeps its words, behind a backslash. A line starts after any break a reader may
// take for one, not only after a newline.
func escapeHeaders(text string) string {
	if !strings.Contains(text, headerStart) {
		return text
	}
	var b strings.Builder
	atLine := true // only spaces and tabs since the start of the line
	for i := 0; i < len(text); {
		r, n := utf8.DecodeRuneInString(text[i:])
		switch {
		case isLineBreak(r):
			atLine = true
		case r == ' ' || r == '\t':
			// still at the start of the line
		default:
			if atLine && strings.HasPrefix(text[i:], headerStart) {
				b.WriteByte('\\')
			}
			atLine = false
		}
		b.WriteString(text[i : i+n])
		i += n
	}
	return b.String()
}

// isLineBreak says whether a reader may take r for the end of a line.
func isLineBreak(r rune) bool {
	switch r {
	case '\n', '\r', '\v', '\f', '\u0085', '\U00002028', '\U00002029':
		return true
	}
	return false
}

func priorityOf(n NewItem) (Priority, int) {
	rank := 0
	if n.Kind == KindApproval {
		rank = 1 // the requests of approval go after the rest of the same priority
	}
	if n.Kind == KindUser && n.Origin != nil {
		if n.Origin.Level == characters.LevelOwner {
			return PriorityOwner, rank
		}
		return PriorityUsers, rank
	}
	return PriorityRest, rank
}
