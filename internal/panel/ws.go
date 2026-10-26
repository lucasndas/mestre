package panel

import "encoding/json"

// The messages of /ws/ui (docs/conectividade/protocolo.md#canal-do-navegador): the
// envelope of the protocol without machine_id and epoch, in text frames.

// The types of the messages of the browser channel.
const (
	// TypeUISubscribe (browser → Cidadela) subscribes to topics.
	TypeUISubscribe = "ui.subscribe"
	// TypeUIUnsubscribe (browser → Cidadela) leaves topics.
	TypeUIUnsubscribe = "ui.unsubscribe"
	// TypeUISubscribed (Cidadela → browser) answers a subscribe, with reply_to.
	TypeUISubscribed = "ui.subscribed"
	// TypeUIEvent (Cidadela → browser) is the snapshot of a topic (event
	// "snapshot") and then each event of it.
	TypeUIEvent = "ui.event"
	// TypeUIError (Cidadela → browser) answers a message that was refused.
	TypeUIError = "error"
)

// EventSnapshot is the event of the first ui.event of a topic.
const EventSnapshot = "snapshot"

// CloseSessionEnded is the code the Citadel closes the socket with when the session
// ends (expiry, logout, suspension, change of password…): the browser goes to the login.
const CloseSessionEnded = 4001

// UIEnvelope is the envelope of a message of the browser channel.
type UIEnvelope struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	TS      string          `json:"ts"`
	Payload json.RawMessage `json:"payload"`
	ReplyTo string          `json:"reply_to,omitempty"`
}

// UISubscribe is the payload of ui.subscribe and ui.unsubscribe. A topic is
// "workspace:<id>", "table:<id>" or "terminal:<id>".
type UISubscribe struct {
	Topics []string `json:"topics"`
}

// UIRejected is a topic a subscribe did not get, and why (an error code).
type UIRejected struct {
	Topic  string         `json:"topic"`
	Code   Code           `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

// UISubscribed is the payload of ui.subscribed.
type UISubscribed struct {
	Topics   []string     `json:"topics"`
	Rejected []UIRejected `json:"rejected"`
}

// UIEvent is the payload of ui.event.
type UIEvent struct {
	Topic string          `json:"topic"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}
