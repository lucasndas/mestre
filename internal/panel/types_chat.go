package panel

import "time"

// The payloads of the conversation with the Grão-Mestre
// (docs/painel/conversa-e-aprovacoes.md#conversa-com-o-grão-mestre). tygo turns them
// into web/src/gen/panel.ts.

// ChatTopic is the topic of /ws/ui a user follows to hear about his conversations: the
// id is his own, and the events carry the workspace.
const ChatTopic = "chat"

// The events of the topic "chat".
const (
	// EventChatChanged says the conversation of a workspace has something new (an
	// answer, a question, the card of progress): the screen reads it again.
	EventChatChanged = "changed"
	// EventChatStatus carries the new state of the Grão-Mestre of a workspace.
	EventChatStatus = "status"
)

// ChatWorkspace is a workspace the user may talk in.
type ChatWorkspace struct {
	Slug string `json:"slug"`
	// CanTalk is false for the level that may not write to the Grão-Mestre (the input is
	// shown off, with the reason).
	CanTalk bool `json:"can_talk"`
	Unread  int  `json:"unread"`
}

// ChatOverview is GET /api/chat.
type ChatOverview struct {
	Workspaces []ChatWorkspace `json:"workspaces"`
	// Unread is the sum of the unread answers of every workspace.
	Unread int `json:"unread"`
	// Limit is the biggest attachment, in bytes; MaxFiles how many go in one message.
	Limit    int64 `json:"limit"`
	MaxFiles int   `json:"max_files"`
}

// ChatSnapshot is the first event of the topic "chat".
type ChatSnapshot struct {
	Unread int `json:"unread"`
}

// ChatStatus is the state of the Grão-Mestre of a workspace.
type ChatStatus struct {
	// State is "idle", "busy", "unavailable" (interrupted, with Reason) or "none" (the
	// workspace has no Grão-Mestre now).
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// Ahead is how many items wait in his inbox.
	Ahead int `json:"ahead"`
	// Locked says the Mestre is locked: what is sent is kept until it is unlocked.
	Locked bool `json:"locked"`
}

// ChatStatusEvent is the payload of the event "status".
type ChatStatusEvent struct {
	Workspace string     `json:"workspace"`
	Status    ChatStatus `json:"status"`
}

// ChatChangedEvent is the payload of the event "changed".
type ChatChangedEvent struct {
	Workspace string `json:"workspace"`
}

// ChatProgress is the message of progress of the workspace: the card at the top.
type ChatProgress struct {
	// Lines are the lines of the message: the count of the tables first, then one line
	// for each thing that waits for somebody.
	Lines     []string  `json:"lines"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ChatAttachment is a file of a message.
type ChatAttachment struct {
	Name string `json:"name"`
	// Download is the path of the file when the user may download it (a file the Grão-Mestre
	// sent); empty for what the user sent himself or a file that is not there to download.
	Download string `json:"download,omitempty"`
}

// ChatQuote is the message a message answers (the quote), with the start of its text.
type ChatQuote struct {
	ID      string `json:"id"`
	Excerpt string `json:"excerpt"`
	// Mine says the quoted message is the user's own.
	Mine bool `json:"mine"`
}

// ChatQuestion is a question of `mestre choose`: one button per option.
type ChatQuestion struct {
	ID      int64    `json:"id"`
	Options []string `json:"options"`
	// State is "open" or "answered"; Answer the option chosen.
	State  string `json:"state"`
	Answer string `json:"answer,omitempty"`
}

// ChatEntry is a message of the conversation.
type ChatEntry struct {
	// ID names the entry: "in-<n>" (a message of the user), "out-<n>" (an answer) or
	// "ask-<n>" (a question). It is what a quote carries.
	ID string `json:"id"`
	// From is "user", "grand_master" or "question".
	From string `json:"from"`
	// Channel is "panel" or "telegram", for the icon.
	Channel string     `json:"channel"`
	Text    string     `json:"text"`
	At      time.Time  `json:"at"`
	Table   string     `json:"table,omitempty"`
	Quote   *ChatQuote `json:"quote,omitempty"`
	// Attachments are the files of the message.
	Attachments []ChatAttachment `json:"attachments,omitempty"`
	Question    *ChatQuestion    `json:"question,omitempty"`
	// Waiting is true for a message of the user that the Grão-Mestre did not take yet
	// (in the queue, or kept because the Mestre is locked).
	Waiting bool `json:"waiting,omitempty"`
	// Unread is true for an answer that came after the user last opened the conversation.
	Unread bool `json:"unread,omitempty"`
}

// ChatConversation is GET /api/chat/{workspace}: the conversation of the user with the
// Grão-Mestre of the workspace, oldest first.
type ChatConversation struct {
	Workspace string `json:"workspace"`
	CanTalk   bool   `json:"can_talk"`
	// Status is the state of the Grão-Mestre.
	Status   ChatStatus    `json:"status"`
	Progress *ChatProgress `json:"progress,omitempty"`
	Entries  []ChatEntry   `json:"entries"`
	Unread   int           `json:"unread"`
	// Limit is the biggest attachment, in bytes; MaxFiles how many go in one message.
	Limit    int64 `json:"limit"`
	MaxFiles int   `json:"max_files"`
}

// ChatSent is the answer of POST /api/chat/{workspace}/messages.
type ChatSent struct {
	ID string `json:"id"`
	// Held says the message was kept because the Mestre is locked: it goes when it is unlocked.
	Held bool `json:"held"`
	// Secret says the filter of secrets acted: "held" (a password: the Grão-Mestre got only a notice) or
	// "removed" (a token: he got the message without the piece). The user is told to change the secret.
	Secret string `json:"secret,omitempty"`
}

// ChatChoice is the body of POST /api/chat/choices/{id}: the option, from 0.
type ChatChoice struct {
	Option int `json:"option"`
	// Language is the language the panel is open in (optional).
	Language string `json:"language,omitempty"`
}
