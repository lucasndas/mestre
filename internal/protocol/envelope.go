package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"time"
)

// Limits of the wire (docs/conectividade/protocolo.md#envelope).
const (
	// MaxMessage is the largest JSON message, in bytes; bigger content uses
	// the transfer in parts.
	MaxMessage = 1 << 20
	// MaxDataPayload is the largest payload of a binary frame: with its header
	// the frame is not over MaxMessage either.
	MaxDataPayload = MaxMessage - DataHeaderSize
)

const tsLayout = "2006-01-02T15:04:05.000Z"

// Timestamp is the `ts` of the envelope: RFC 3339 in UTC with milliseconds,
// on the clock of the sender (informative only).
type Timestamp struct{ time.Time }

// MarshalJSON writes the instant in UTC with milliseconds.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	return []byte(`"` + t.UTC().Format(tsLayout) + `"`), nil
}

// UnmarshalJSON reads any RFC 3339 instant and converts it to UTC.
func (t *Timestamp) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("timestamp: %w", err)
	}
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("timestamp: %w", err)
	}
	t.Time = v.UTC()
	return nil
}

// Envelope is the frame of every control message.
type Envelope struct {
	// ID is a ULID, unique per sender; it is also the idempotency key.
	ID string `json:"id"`
	// Type is "<domain>.<action>", e.g. terminal.open.
	Type string `json:"type"`
	// MachineID is the sender. Omitted in the messages of the browser.
	MachineID string `json:"machine_id,omitempty"`
	// Epoch is the epoch of the Guild that the sender follows. Omitted in
	// the messages of the browser.
	Epoch uint64    `json:"epoch,omitempty"`
	TS    Timestamp `json:"ts"`
	// Payload depends on the Type.
	Payload json.RawMessage `json:"payload"`
	// ReplyTo is the id of the message this one answers.
	ReplyTo string `json:"reply_to,omitempty"`
	// Ack is true when the sender demands a confirmation.
	Ack bool `json:"ack,omitempty"`
}

// LogValue keeps the payload out of the logs: it can carry a secret.
func (e Envelope) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("id", e.ID), slog.String("type", e.Type), slog.String("machine_id", e.MachineID),
		slog.Uint64("epoch", e.Epoch), slog.String("reply_to", e.ReplyTo), slog.Bool("ack", e.Ack),
	)
}

var typeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// Encode writes the envelope as a JSON text frame, refusing one over
// MaxMessage.
func Encode(e Envelope) ([]byte, error) {
	if len(e.Payload) == 0 {
		e.Payload = json.RawMessage(`{}`)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", e.Type, err)
	}
	if len(b) > MaxMessage {
		return nil, NewError(CodeTooLarge, "%s is %d bytes, the limit is %d", e.Type, len(b), MaxMessage)
	}
	return b, nil
}

// Decode reads a text frame. Unknown fields are ignored. The errors are the
// ones to send back: `too_large` for a frame over MaxMessage and
// `invalid_payload` for a malformed envelope. It does not look at the epoch
// or the type.
func Decode(data []byte) (Envelope, *Error) {
	if len(data) > MaxMessage {
		return Envelope{}, NewError(CodeTooLarge, "message of %d bytes, the limit is %d", len(data), MaxMessage)
	}
	var e Envelope
	if err := json.Unmarshal(data, &e); err != nil {
		return Envelope{}, NewError(CodeInvalidPayload, "envelope: %v", err)
	}
	if _, err := ParseULID(e.ID); err != nil {
		return e, NewError(CodeInvalidPayload, "envelope: id: %v", err)
	}
	if !typeRe.MatchString(e.Type) || len(e.Type) > 64 {
		return e, NewError(CodeInvalidPayload, "envelope: invalid type %q", e.Type)
	}
	if e.TS.IsZero() {
		return e, NewError(CodeInvalidPayload, "envelope: ts is required")
	}
	if e.ReplyTo != "" {
		if _, err := ParseULID(e.ReplyTo); err != nil {
			return e, NewError(CodeInvalidPayload, "envelope: reply_to: %v", err)
		}
	}
	p := bytes.TrimSpace(e.Payload)
	if len(p) == 0 || p[0] != '{' {
		return e, NewError(CodeInvalidPayload, "envelope: payload must be an object")
	}
	return e, nil
}

// CheckSender says whether the envelope carries what a message from a peer
// of this role must carry: machine_id and epoch between machines (and the
// machine_id must be the one the mTLS certificate proved), nothing of the
// sort from the browser.
func (e Envelope) CheckSender(peer Peer) *Error {
	if peer.Role == RoleBrowser {
		return nil
	}
	if e.MachineID == "" || e.Epoch == 0 {
		return NewError(CodeInvalidPayload, "envelope: machine_id and epoch are required between machines")
	}
	if e.MachineID != peer.ID {
		return NewError(CodeForbidden, "envelope: machine_id %q is not the machine of the connection", e.MachineID)
	}
	return nil
}
