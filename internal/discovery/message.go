package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// The broadcast messages of docs/conectividade/descoberta.md#broadcast-udp.
const (
	// DefaultPort is the discovery port, 7445/udp, shared by all the
	// instances of a machine.
	DefaultPort = 7445
	// DefaultEnginePort is the WSS port of the engine of the default instance.
	DefaultEnginePort = 7443
	// Version is the version of the messages ("v").
	Version = 1
	// MaxMessage is the size every message must stay under.
	MaxMessage = 512

	kindDiscover = "discover"
	kindHere     = "here"

	nonceBytes = 16
)

// Roles a machine announces.
const (
	RoleCitadel = "cidadela"
	RoleTower   = "torre"
)

// Request is `{"mestre":"discover"}`: who is there?
type Request struct {
	Mestre string `json:"mestre"`
	V      int    `json:"v"`
	// Nonce is 32 hex digits chosen by the asker; only answers that carry it
	// are taken.
	Nonce string `json:"nonce"`
	// GuildID is the Guild the asker looks for; empty only to join one.
	GuildID string `json:"guild_id"`
}

// CitadelRef says where the Citadel that a machine follows is.
type CitadelRef struct {
	MachineID string   `json:"machine_id"`
	Addrs     []string `json:"addrs"`
	Port      int      `json:"port"`
}

// Response is `{"mestre":"here"}`: the answer of a machine of the Guild.
type Response struct {
	Mestre    string      `json:"mestre"`
	V         int         `json:"v"`
	Nonce     string      `json:"nonce"`
	GuildID   string      `json:"guild_id"`
	MachineID string      `json:"machine_id"`
	Name      string      `json:"name"`
	Role      string      `json:"role"`
	Epoch     uint64      `json:"epoch"`
	Port      int         `json:"port"`
	Citadel   *CitadelRef `json:"citadel,omitempty"`
	Machines  int         `json:"machines"`
	CAFp      string      `json:"ca_fp"`
}

// Errors of the codec.
var (
	ErrTooBig   = errors.New("discovery: message is not under 512 bytes")
	ErrNotMine  = errors.New("discovery: not a discovery message of this version")
	ErrBadNonce = errors.New("discovery: the nonce is not 32 hex digits")
)

// NewNonce draws a nonce from r (crypto/rand in production).
func NewNonce(r io.Reader) (string, error) {
	var b [nonceBytes]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("discovery: nonce: %w", err)
	}
	return fmt.Sprintf("%x", b), nil
}

func validNonce(s string) bool {
	if len(s) != 2*nonceBytes {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// Encode returns the datagram of r.
func (r Request) Encode() ([]byte, error) {
	r.Mestre, r.V = kindDiscover, Version
	if !validNonce(r.Nonce) {
		return nil, ErrBadNonce
	}
	return encode(r)
}

// Encode returns the datagram of r. An answer that would not fit loses the
// addresses of the Citadel it names, last first, until it does.
func (r Response) Encode() ([]byte, error) {
	r.Mestre, r.V = kindHere, Version
	if !validNonce(r.Nonce) {
		return nil, ErrBadNonce
	}
	if r.Citadel != nil {
		c := *r.Citadel
		c.Addrs = append([]string(nil), c.Addrs...)
		r.Citadel = &c
	}
	for {
		b, err := encode(r)
		if err == nil || r.Citadel == nil || len(r.Citadel.Addrs) <= 1 {
			return b, err
		}
		r.Citadel.Addrs = r.Citadel.Addrs[:len(r.Citadel.Addrs)-1]
	}
}

func encode(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if len(b) >= MaxMessage {
		return nil, ErrTooBig
	}
	return b, nil
}

// DecodeRequest reads a datagram as a request. Unknown fields are ignored; a
// message of another version, kind or size is ErrNotMine / ErrTooBig.
func DecodeRequest(b []byte) (Request, error) {
	var r Request
	if err := decode(b, &r); err != nil {
		return Request{}, err
	}
	if r.Mestre != kindDiscover || r.V != Version {
		return Request{}, ErrNotMine
	}
	if !validNonce(r.Nonce) {
		return Request{}, ErrBadNonce
	}
	return r, nil
}

// DecodeResponse reads a datagram as an answer.
func DecodeResponse(b []byte) (Response, error) {
	var r Response
	if err := decode(b, &r); err != nil {
		return Response{}, err
	}
	if r.Mestre != kindHere || r.V != Version {
		return Response{}, ErrNotMine
	}
	if !validNonce(r.Nonce) {
		return Response{}, ErrBadNonce
	}
	return r, nil
}

func decode(b []byte, v any) error {
	if len(b) >= MaxMessage {
		return ErrTooBig
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%w: %v", ErrNotMine, err)
	}
	return nil
}
