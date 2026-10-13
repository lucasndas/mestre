package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Errors of the opening.
var (
	// ErrRevoked ends a Client: the credential was revoked (4002).
	ErrRevoked = errors.New("protocol: credential revoked")
	// ErrOpening is wrapped by the errors of an opening that failed.
	ErrOpening = errors.New("protocol: opening failed")
)

// IncompatibleError is a connection closed with 4003: the other side accepts
// another range of versions.
type IncompatibleError struct {
	Local, Remote Range
}

func (e *IncompatibleError) Error() string {
	if e.Remote.Valid() {
		return fmt.Sprintf("protocol: no version in common (this binary speaks %s, the peer %s)", e.Local, e.Remote)
	}
	return fmt.Sprintf("protocol: no version in common (this binary speaks %s)", e.Local)
}

// helloHeader is what the opening itself reads of the first message, for
// every kind of connection; the rest is checked against the schema of the
// type.
type helloHeader struct {
	Protocol     Range        `json:"protocol"`
	Capabilities Capabilities `json:"capabilities"`
	Certificate  *Certificate `json:"certificate"`
}

// follow is the common step of the epoch: cert must be signed by the CA of
// the Guild, prove at least the epoch that the other side claimed, and, if it
// is higher than ours, is followed.
func (c *Config) follow(ctx context.Context, cert Certificate, claimed uint64) *Error {
	if err := c.Certs.Verify(cert); err != nil {
		return NewError(CodeForbidden, "the certificate of epoch %d was refused: %v", cert.Epoch, err)
	}
	if cert.Epoch < claimed {
		return NewError(CodeForbidden, "the certificate proves epoch %d, not %d", cert.Epoch, claimed)
	}
	if cert.Epoch > c.Epoch.Epoch() {
		if err := c.Epoch.Follow(ctx, cert); err != nil {
			return NewError(CodeForbidden, "cannot follow epoch %d: %v", cert.Epoch, err)
		}
	}
	return nil
}

// sendNow writes an envelope on a connection whose writer is not running yet.
func (c *Config) sendNow(ctx context.Context, conn Conn, gen *IDGen, typ string, raw json.RawMessage, replyTo string) (Envelope, error) {
	env := Envelope{
		ID: gen.NewString(), Type: typ, MachineID: c.MachineID, Epoch: c.Epoch.Epoch(),
		TS: Timestamp{c.Clock.Now().UTC()}, Payload: raw, ReplyTo: replyTo,
	}
	b, err := Encode(env)
	if err != nil {
		return env, err
	}
	return env, conn.Write(ctx, FrameText, b)
}

// refuse answers an opening with an error and closes the connection.
func (c *Config) refuse(ctx context.Context, conn Conn, gen *IDGen, replyTo string, e *Error, code int, reason string) {
	_, _ = c.sendNow(ctx, conn, gen, TypeError, jsonOf(e), replyTo)
	_ = conn.Close(code, reason)
}
