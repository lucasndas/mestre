// Package localchan is the local channel between the CLI `mestre` (and the
// `mestre hook` dispatcher) and the engine of the machine
// (docs/arquitetura/canal-local.md): the framing, the request and response,
// the partial responses, the idempotency by request id, the identification
// of who calls, and the client library the CLI uses.
//
// The engine side is Server; the CLI side is Client. Both sides speak the
// types of this file. Who is on the other end of the socket is decided by an
// Identifier (identify*.go): the cgroup of the terminal or the process tree
// on Linux, the Job Object of the terminal on Windows.
package localchan

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
)

// Versions of the channel. The engine accepts the current version and the
// previous one; there is no previous one yet, so only Version is accepted
// (docs/conectividade/protocolo.md#abertura-versão-e-capacidades).
const (
	Version    = 1
	MinVersion = 1
)

// Defaults of the channel (docs/arquitetura/canal-local.md).
const (
	// MaxLine is the largest line, request or response, in bytes.
	MaxLine = 1 << 20
	// DefaultCommandTimeout is the time limit of a command that has none
	// of its own.
	DefaultCommandTimeout = 30 * time.Second
	// TimeoutGrace is added to the time limit of the command for the CLI to
	// wait for the engine.
	TimeoutGrace = 5 * time.Second
	// DefaultHeartbeat is the interval of the sign of life the engine sends
	// while a request runs.
	DefaultHeartbeat = 15 * time.Second
	// DefaultConnectWindow is how long the CLI keeps trying to connect to
	// an engine that is restarting.
	DefaultConnectWindow = 60 * time.Second
)

// Request is the line the CLI sends.
type Request struct {
	V       int               `json:"v"`
	ID      string            `json:"id"`
	AgentID string            `json:"agent_id"`
	Cmd     string            `json:"cmd"`
	Args    []json.RawMessage `json:"args"`
	Flags   map[string]any    `json:"flags"`
	Cwd     string            `json:"cwd"`
}

// TextArgs encodes positional arguments given as text.
func TextArgs(args ...string) []json.RawMessage {
	out := make([]json.RawMessage, len(args))
	for i, a := range args {
		b, _ := json.Marshal(a) // a string always encodes
		out[i] = b
	}
	return out
}

// Strings returns the positional arguments that are text, and false if one
// is not (the `hook` command passes a single object).
func (r Request) Strings() ([]string, bool) {
	out := make([]string, len(r.Args))
	for i, a := range r.Args {
		if err := json.Unmarshal(a, &out[i]); err != nil {
			return nil, false
		}
	}
	return out, true
}

// Error is the error of a response.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Params are the parameters of the message the engine said it with: names,
	// numbers and instants (RFC 3339), never a sentence in another language.
	Params  map[string]any `json:"params"`
	Hint    string         `json:"hint,omitempty"`
	Details map[string]any `json:"details"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Response is a final response or, with Partial set, a partial one.
type Response struct {
	V       int             `json:"v"`
	ID      string          `json:"id"`
	OK      bool            `json:"ok"`
	Partial bool            `json:"partial,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	// Exit is the exit code when it is not the obvious one: 0 for ok, the
	// family of the error code otherwise.
	Exit *int `json:"exit,omitempty"`
}

// ExitCode is the exit code of the CLI for a final response.
func (r Response) ExitCode() int {
	switch {
	case r.Exit != nil:
		return *r.Exit
	case r.OK:
		return 0
	case r.Error == nil:
		return ExitInternal
	}
	return ExitFor(r.Error.Code)
}

// OK is a success response with data (nil for none).
func OK(data any) Response {
	b, err := json.Marshal(data)
	if err != nil {
		return Fail(CodeInternal, "encode the response data: "+err.Error(), "")
	}
	if data == nil {
		b = nil
	}
	return Response{OK: true, Data: b}
}

// Fail is an error response.
func Fail(code, message, hint string) Response {
	return Response{Error: &Error{Code: code, Message: message, Params: map[string]any{}, Hint: hint, Details: map[string]any{}}}
}

// FailMsg is an error response whose message comes from the catalog, always in
// English: an agent reads it (docs/plataforma/idioma-por-canal.md#cli). The
// code of the error stays the one the command had; the parameters of the
// message go in params.
func FailMsg(code string, m i18n.Msg, hint string) Response {
	r := Fail(code, i18n.Render(i18n.En, m), hint)
	r.Error.Params = i18n.PlainParams(i18n.En, m)
	return r
}

// WithExit returns r with an explicit exit code.
func (r Response) WithExit(code int) Response {
	r.Exit = &code
	return r
}

// DecodeData decodes the data of a response into v.
func (r Response) DecodeData(v any) error {
	if len(r.Data) == 0 {
		return errors.New("response without data")
	}
	return json.Unmarshal(r.Data, v)
}

// encodeFinal is the line of a final response: it always has "ok".
func encodeFinal(r Response) ([]byte, error) {
	v := struct {
		V     int             `json:"v"`
		ID    string          `json:"id"`
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data,omitempty"`
		Error *Error          `json:"error,omitempty"`
		Exit  *int            `json:"exit,omitempty"`
	}{r.V, r.ID, r.OK, r.Data, r.Error, r.Exit}
	return marshalLine(v)
}

// encodePartial is the line of a partial response: it has no "ok".
func encodePartial(version int, id string, data json.RawMessage) ([]byte, error) {
	v := struct {
		V       int             `json:"v"`
		ID      string          `json:"id"`
		Partial bool            `json:"partial"`
		Data    json.RawMessage `json:"data,omitempty"`
	}{version, id, true, data}
	return marshalLine(v)
}

func marshalLine(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode a message: %w", err)
	}
	return append(b, '\n'), nil
}

// ErrTooLarge means a line is over MaxLine.
var ErrTooLarge = errors.New("localchan: line over the limit")

// readLine reads one line of at most MaxLine bytes, without the newline.
func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := br.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > MaxLine+1 { // the newline does not count
			return nil, ErrTooLarge
		}
		switch {
		case err == nil:
			return bytes.TrimRight(line, "\r\n"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) > 0:
			return nil, io.ErrUnexpectedEOF
		default:
			return nil, err
		}
	}
}
