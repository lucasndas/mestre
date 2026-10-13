package protocol

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// Direction says who may send a message type: T is the Tower, C the Citadel.
type Direction uint8

// The directions of the catalogue.
const (
	TowerToCitadel Direction = iota + 1
	CitadelToTower
	Both
)

// Side is which end of the conversation a machine is.
type Side uint8

// The two sides.
const (
	SideTower Side = iota + 1
	SideCitadel
)

// Allows says whether a message of this direction may be sent by a machine
// on side s.
func (d Direction) Allows(s Side) bool {
	return d == Both || (d == TowerToCitadel && s == SideTower) || (d == CitadelToTower && s == SideCitadel)
}

// Spec describes one message type.
type Spec struct {
	Type string
	Dir  Direction
	// Ack is true when the message demands a confirmation: Send sets the
	// ack flag, the outbox keeps it until the ack arrives and the receiver
	// runs it once.
	Ack bool
	// Volatile keeps the message out of the durable outbox because its
	// payload carries a secret: it waits for its ack in memory, is resent
	// on a new connection of the same process and is lost on a restart.
	Volatile bool
	// Payload is a zero value of the struct (or Object type) of the payload.
	Payload any
	// Reply, if not nil, is the payload when the envelope has reply_to (the
	// answer to guild.epoch is a certificate, the request has no fields).
	Reply any
}

type validator interface{ Validate() error }

// Registry is the catalogue of message types of one kind of connection.
type Registry struct {
	mu    sync.RWMutex
	specs map[string]Spec
}

// NewRegistry returns an empty registry: the Guardian connection fills it
// with its own types.
func NewRegistry() *Registry { return &Registry{specs: map[string]Spec{}} }

// Add registers a type.
func (r *Registry) Add(s Spec) error {
	if !typeRe.MatchString(s.Type) || s.Dir < TowerToCitadel || s.Dir > Both || s.Payload == nil {
		return fmt.Errorf("protocol: invalid spec for %q", s.Type)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.specs[s.Type]; dup {
		return fmt.Errorf("protocol: type %q registered twice", s.Type)
	}
	r.specs[s.Type] = s
	return nil
}

// Spec returns the description of a type.
func (r *Registry) Spec(typ string) (Spec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.specs[typ]
	return s, ok
}

// Types lists the registered types, sorted.
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.specs))
	for t := range r.specs {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// Decode checks the payload of env against the schema of its type and
// returns it as a pointer to the struct of the type. A type that is not in
// the catalogue is `unknown_type`; a missing field or one of the wrong type
// is `invalid_payload`. Fields that the struct does not know are ignored.
func (r *Registry) Decode(env Envelope) (any, *Error) {
	s, ok := r.Spec(env.Type)
	if !ok {
		return nil, NewError(CodeUnknownType, "type %q is not known in this version", env.Type)
	}
	return s.decode(env.Payload, env.ReplyTo != "")
}

func (s Spec) decode(raw json.RawMessage, reply bool) (any, *Error) {
	proto := s.Payload
	if reply && s.Reply != nil {
		proto = s.Reply
	}
	t := reflect.TypeOf(proto)
	ptr := reflect.New(t)
	if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
		return nil, NewError(CodeInvalidPayload, "%s: %v", s.Type, err)
	}
	if t.Kind() == reflect.Struct {
		if err := checkRequired(t, raw); err != nil {
			return nil, NewError(CodeInvalidPayload, "%s: %v", s.Type, err)
		}
	}
	if v, ok := ptr.Interface().(validator); ok {
		if err := v.Validate(); err != nil {
			return nil, NewError(CodeInvalidPayload, "%s: %v", s.Type, err)
		}
	} else if v, ok := ptr.Elem().Interface().(validator); ok {
		if err := v.Validate(); err != nil {
			return nil, NewError(CodeInvalidPayload, "%s: %v", s.Type, err)
		}
	}
	return ptr.Interface(), nil
}

var requiredFields sync.Map // reflect.Type -> []string (JSON names)

func requiredOf(t reflect.Type) []string {
	if v, ok := requiredFields.Load(t); ok {
		return v.([]string)
	}
	var names []string
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Tag.Get("proto") != "req" {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" {
			name = f.Name
		}
		names = append(names, name)
	}
	requiredFields.Store(t, names)
	return names
}

// checkRequired verifies that every `proto:"req"` field is a key of the JSON
// object and not null.
func checkRequired(t reflect.Type, raw json.RawMessage) error {
	req := requiredOf(t)
	if len(req) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	for _, name := range req {
		v, ok := obj[name]
		if !ok || string(v) == "null" {
			return fmt.Errorf("field %q is required", name)
		}
	}
	return nil
}

// Marshal encodes a payload for a type and checks it against the same
// schema the receiver applies, so a message that the peer would refuse is
// never sent. replyTo says whether the envelope answers another message.
func (r *Registry) Marshal(typ string, payload any, replyTo bool) (json.RawMessage, error) {
	s, ok := r.Spec(typ)
	if !ok {
		return nil, NewError(CodeUnknownType, "type %q is not known in this version", typ)
	}
	var raw json.RawMessage
	switch p := payload.(type) {
	case nil:
		raw = json.RawMessage(`{}`)
	case json.RawMessage:
		raw = p
	default:
		b, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("encode %s payload: %w", typ, err)
		}
		raw = b
	}
	if _, perr := s.decode(raw, replyTo); perr != nil {
		return nil, perr
	}
	return raw, nil
}
