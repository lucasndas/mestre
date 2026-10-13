package ptywire

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrAuth is wrapped by every handshake failure: the peer did not prove the
// secret, or did not speak the protocol.
var ErrAuth = errors.New("ptywire: handshake refused")

// The labels keep a proof made for one direction from being replayed in the
// other.
const (
	labelPTY    = "mestre-pty"
	labelEngine = "mestre-engine"
)

// Info is what the mestre-pty tells the engine once the handshake succeeded.
type Info struct {
	Proto   int
	Version string
	PID     int
}

func newNonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("nonce: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b[:]), nil
}

// proof is HMAC-SHA256(secret, label || 0 || clientNonce || 0 || serverNonce).
// Both nonces are inside it, so a recorded proof is worthless.
func proof(secret []byte, label, clientNonce, serverNonce string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(label))
	m.Write([]byte{0})
	m.Write([]byte(clientNonce))
	m.Write([]byte{0})
	m.Write([]byte(serverNonce))
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func proofEqual(a, b string) bool { return hmac.Equal([]byte(a), []byte(b)) }

func authErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrAuth, fmt.Sprintf(format, args...))
}

// ServerHandshake is the mestre-pty side: it answers the engine's nonce
// with its own proof, checks the engine's proof, and only then tells who it
// is (version and PID are not given to a peer that failed). The caller sets
// the timeout by closing the connection.
//
//	engine -> pty   hello  {proto, nonce}
//	pty -> engine   hello  {nonce, proof}
//	engine -> pty   auth   {proof}
//	pty -> engine   ready  {proto, version, pid}
func ServerHandshake(c *Conn, secret []byte, info Info) (clientProto int, err error) {
	it, err := c.Read()
	if err != nil {
		return 0, authErr("reading hello: %v", err)
	}
	if it.Msg == nil || it.Msg.T != TypeHello || it.Msg.Nonce == "" {
		return 0, authErr("first message is not a hello")
	}
	cn := it.Msg.Nonce
	sn, err := newNonce()
	if err != nil {
		return 0, err
	}
	if err := c.WriteMsg(&Msg{T: TypeHello, Nonce: sn, Proof: proof(secret, labelPTY, cn, sn)}); err != nil {
		return 0, authErr("writing hello: %v", err)
	}
	it, err = c.Read()
	if err != nil {
		return 0, authErr("reading auth: %v", err)
	}
	if it.Msg == nil || it.Msg.T != TypeAuth {
		return 0, authErr("second message is not an auth")
	}
	if !proofEqual(it.Msg.Proof, proof(secret, labelEngine, cn, sn)) {
		return 0, authErr("wrong secret")
	}
	if err := c.WriteMsg(&Msg{T: TypeReady, Proto: info.Proto, Version: info.Version, PID: info.PID}); err != nil {
		return 0, authErr("writing ready: %v", err)
	}
	return it.Msg.Proto, nil
}

// ClientHandshake is the engine side. It checks the mestre-pty's proof
// before sending its own, so an impostor never sees a valid engine proof.
func ClientHandshake(c *Conn, secret []byte) (Info, error) {
	cn, err := newNonce()
	if err != nil {
		return Info{}, err
	}
	if err := c.WriteMsg(&Msg{T: TypeHello, Proto: ProtoVersion, Nonce: cn}); err != nil {
		return Info{}, authErr("writing hello: %v", err)
	}
	it, err := c.Read()
	if err != nil {
		return Info{}, authErr("reading hello: %v", err)
	}
	if it.Msg == nil || it.Msg.T != TypeHello || it.Msg.Nonce == "" {
		return Info{}, authErr("answer is not a hello")
	}
	sn := it.Msg.Nonce
	if !proofEqual(it.Msg.Proof, proof(secret, labelPTY, cn, sn)) {
		return Info{}, authErr("the mestre-pty does not know the secret")
	}
	if err := c.WriteMsg(&Msg{T: TypeAuth, Proto: ProtoVersion, Proof: proof(secret, labelEngine, cn, sn)}); err != nil {
		return Info{}, authErr("writing auth: %v", err)
	}
	it, err = c.Read()
	if err != nil {
		return Info{}, authErr("reading ready: %v", err)
	}
	if it.Msg == nil || it.Msg.T != TypeReady {
		return Info{}, authErr("answer is not a ready")
	}
	return Info{Proto: it.Msg.Proto, Version: it.Msg.Version, PID: it.Msg.PID}, nil
}
