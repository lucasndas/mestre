package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Close codes of the connection (docs/conectividade/protocolo.md#heartbeat-e-reconexão).
const (
	CloseBadCredential = 4001 // invalid credential
	CloseRevoked       = 4002 // revoked credential: the machine stops trying
	CloseIncompatible  = 4003 // no protocol version in common
	CloseReplaced      = 4004 // replaced by a newer connection of the machine
	CloseShuttingDown  = 4005 // the Citadel is shutting down: reconnect soon
	CloseStaleEpoch    = 4006 // the epoch of the sender is old
	CloseNotCitadel    = 4007 // I am not the Citadel
	CloseOverloaded    = 4008 // the receiver is behind: what was sent waits too long to run
)

// Codes of the `error` message (docs/conectividade/mensagens.md#códigos-de-erro).
const (
	CodeUnknownType         = "unknown_type"
	CodeInvalidPayload      = "invalid_payload"
	CodeTooLarge            = "too_large"
	CodeNotFound            = "not_found"
	CodeStaleEpoch          = "stale_epoch"
	CodeMissingPrerequisite = "missing_prerequisite"
	CodeHashMismatch        = "hash_mismatch"
	CodeBusyTerminal        = "busy_terminal"
	CodeUnavailable         = "unavailable"
	CodeAffectsGuardian     = "affects_guardian"
	CodeForbidden           = "forbidden"
	CodeInternal            = "internal"
)

// retryableCodes are the codes whose `retryable` is true in the table.
var retryableCodes = map[string]bool{
	CodeHashMismatch: true, CodeBusyTerminal: true, CodeUnavailable: true, CodeInternal: true,
}

// KnownCode says whether code is in the table of error codes.
func KnownCode(code string) bool {
	switch code {
	case CodeUnknownType, CodeInvalidPayload, CodeTooLarge, CodeNotFound, CodeStaleEpoch,
		CodeMissingPrerequisite, CodeHashMismatch, CodeBusyTerminal, CodeUnavailable,
		CodeAffectsGuardian, CodeForbidden, CodeInternal:
		return true
	}
	return false
}

// Error is the payload of an `error` message and the error a handler
// returns to refuse a message.
type Error struct {
	Code      string `json:"code" proto:"req"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// NewError builds an Error whose retryable flag is the one of the table.
func NewError(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Retryable: retryableCodes[code]}
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// asError turns any error into an *Error: its own code if it carries one,
// otherwise `internal`.
func asError(err error) *Error {
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	return NewError(CodeInternal, "%v", err)
}

// CloseError is what Recv returns when the peer closed the connection with
// a code, and what the session reports as its cause.
type CloseError struct {
	Code   int
	Reason string
}

func (e *CloseError) Error() string {
	return fmt.Sprintf("protocol: connection closed by the peer with code %d: %s", e.Code, e.Reason)
}

// VersionReason is the reason that accompanies close code 4003: the range
// the closing side accepts.
func VersionReason(r Range) string {
	b, _ := json.Marshal(r) // a Range always encodes
	return string(b)
}

// ParseVersionReason reads the range out of the reason of a 4003 close.
func ParseVersionReason(reason string) (Range, bool) {
	var r Range
	if err := json.Unmarshal([]byte(reason), &r); err != nil || !r.Valid() {
		return Range{}, false
	}
	return r, true
}
