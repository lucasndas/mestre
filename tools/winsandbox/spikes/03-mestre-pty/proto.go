//go:build windows

package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// Engine <-> ptyhost: one JSON object per line on the AF_UNIX socket, both
// ways. The handshake (docs/arquitetura/terminal.md#socket-e-aperto-de-mão):
//
//	engine -> host  {"t":"hello","v":1,"nonce":nE}
//	host -> engine  {"t":"hello","v":1,"nonce":nH,"mac":HMAC(secret,"mestre-pty"|nE|nH),"pid":...}
//	engine -> host  {"t":"auth","mac":HMAC(secret,"mestre"|nE|nH)}
//	host -> engine  {"t":"ready"}
//
// then requests (hostReq) and responses (hostResp), one at a time. The bytes
// of the terminal go base64 inside JSON in this spike (the design has binary
// frames).
const protoVersion = 1

type handshakeMsg struct {
	T     string `json:"t"`
	V     int    `json:"v,omitempty"`
	Nonce []byte `json:"nonce,omitempty"`
	MAC   []byte `json:"mac,omitempty"`
	PID   uint32 `json:"pid,omitempty"`
}

type hostReq struct {
	Op      string   `json:"op"` // start, screen, input, replay, status, injob, stop
	Path    string   `json:"path,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
	Dir     string   `json:"dir,omitempty"`
	Data    []byte   `json:"data,omitempty"`
	From    int64    `json:"from,omitempty"`
	PID     uint32   `json:"pid,omitempty"`
	Created int64    `json:"created,omitempty"`
	Mode    string   `json:"mode,omitempty"` // stop: close (ClosePseudoConsole) or kill (TerminateJobObject)
	Sweep   bool     `json:"sweep,omitempty"`
}

type hostStatus struct {
	HostPID      uint32     `json:"host_pid"`
	User         string     `json:"user"`
	Session      uint32     `json:"session"`
	LogonSession string     `json:"logon_session"`
	HostInAJob   bool       `json:"host_in_a_job"`
	Harden       string     `json:"harden,omitempty"`
	AgentPID     uint32     `json:"agent_pid,omitempty"`
	AgentCreated int64      `json:"agent_created,omitempty"`
	AgentAlive   bool       `json:"agent_alive"`
	ExitCode     *int       `json:"exit_code,omitempty"`
	Job          []procInfo `json:"job"`
	OutsideJob   []procInfo `json:"outside_job,omitempty"` // same logon session, not in the job, not the host
	OutTotal     int64      `json:"out_total"`
	SocketSD     string     `json:"socket_sd,omitempty"`
}

type hostResp struct {
	Error   string      `json:"error,omitempty"`
	Text    string      `json:"text,omitempty"`
	Data    []byte      `json:"data,omitempty"`
	From    int64       `json:"from,omitempty"`
	To      int64       `json:"to,omitempty"`
	Lost    bool        `json:"lost,omitempty"`
	In      bool        `json:"in,omitempty"`
	PID     uint32      `json:"pid,omitempty"`
	Created int64       `json:"created,omitempty"`
	Status  *hostStatus `json:"status,omitempty"`
	Killed  []procInfo  `json:"killed,omitempty"`
	Left    []procInfo  `json:"left,omitempty"`
	Log     []string    `json:"log,omitempty"`
}

type lineConn struct {
	c net.Conn
	r *bufio.Reader
}

func newLineConn(c net.Conn) *lineConn { return &lineConn{c: c, r: bufio.NewReaderSize(c, 1<<20)} }

func (l *lineConn) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_ = l.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err = l.c.Write(append(b, '\n'))
	return err
}

func (l *lineConn) recv(v any, d time.Duration) error {
	if d > 0 {
		_ = l.c.SetReadDeadline(time.Now().Add(d))
	} else {
		_ = l.c.SetReadDeadline(time.Time{})
	}
	line, err := l.r.ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, v)
}

func nonce() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func proof(secret []byte, who string, nE, nH []byte) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(who))
	h.Write(nE)
	h.Write(nH)
	return h.Sum(nil)
}

var errBadProof = errors.New("handshake: proof does not match the secret")

// engineHandshake is the engine side. With lie, it sends a wrong proof even
// after the host proved itself (the "wrong secret" test).
func engineHandshake(l *lineConn, secret []byte, lie bool) (hostPID uint32, err error) {
	nE := nonce()
	if err := l.send(handshakeMsg{T: "hello", V: protoVersion, Nonce: nE}); err != nil {
		return 0, err
	}
	var h handshakeMsg
	if err := l.recv(&h, 5*time.Second); err != nil {
		return 0, fmt.Errorf("handshake: host hello: %w", err)
	}
	hostOK := hmac.Equal(h.MAC, proof(secret, "mestre-pty", nE, h.Nonce))
	if !hostOK && !lie {
		return h.PID, errBadProof
	}
	if err := l.send(handshakeMsg{T: "auth", MAC: proof(secret, "mestre", nE, h.Nonce)}); err != nil {
		return h.PID, err
	}
	var r handshakeMsg
	if err := l.recv(&r, 5*time.Second); err != nil {
		return h.PID, fmt.Errorf("handshake: no ready: %w", err)
	}
	if r.T != "ready" {
		return h.PID, fmt.Errorf("handshake: got %q", r.T)
	}
	if !hostOK {
		return h.PID, fmt.Errorf("handshake: host accepted a wrong proof (host proof valid=%v)", hostOK)
	}
	return h.PID, nil
}

// hostHandshake is the ptyhost side.
func hostHandshake(l *lineConn, secret []byte, pid uint32) error {
	var h handshakeMsg
	if err := l.recv(&h, 5*time.Second); err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if h.T != "hello" || h.V != protoVersion || len(h.Nonce) != 32 {
		return fmt.Errorf("bad hello %q v%d", h.T, h.V)
	}
	nH := nonce()
	if err := l.send(handshakeMsg{T: "hello", V: protoVersion, Nonce: nH, MAC: proof(secret, "mestre-pty", h.Nonce, nH), PID: pid}); err != nil {
		return err
	}
	var a handshakeMsg
	if err := l.recv(&a, 5*time.Second); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if a.T != "auth" || !hmac.Equal(a.MAC, proof(secret, "mestre", h.Nonce, nH)) {
		return errBadProof
	}
	return l.send(handshakeMsg{T: "ready"})
}
