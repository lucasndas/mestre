package panel

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/users"
)

// Code is the stable code of an error of the API. The front end translates by
// the code (and the params); Message is the same text, already translated in the
// language of the one who asked, for a client that has not the code. A refusal
// of the engine has the code of its area ("decree.scope_required") and its
// params (docs/plataforma/mensagens-por-codigo.md).
type Code string

// The codes of the errors of the API.
const (
	CodeInvalidCredentials Code = "invalid_credentials"
	CodeLoginBlocked       Code = "login_blocked"
	CodeRateLimited        Code = "rate_limited"
	CodeUnauthenticated    Code = "unauthenticated"
	CodeForbiddenLevel     Code = "forbidden_level"
	CodeBadOrigin          Code = "bad_origin"
	CodePINRequired        Code = "pin_required"
	CodePINInvalid         Code = "pin_invalid"
	CodePINLocked          Code = "pin_locked"
	CodeNoPIN              Code = "no_pin"
	CodeInvalidCode        Code = "invalid_code"
	CodePasswordTooShort   Code = "password_too_short"
	CodePasswordWrong      Code = "password_wrong"
	CodeInvalidLogin       Code = "invalid_login"
	CodeInvalidPIN         Code = "invalid_pin"
	CodeLoginTaken         Code = "login_taken"
	CodeBadRequest         Code = "bad_request"
	CodeNotFound           Code = "not_found"
	CodeUnknownTopic       Code = "unknown_topic"
	CodeUnavailable        Code = "unavailable"
	CodePermissionPrompt   Code = "permission_prompt"
	CodeTooManyStreams     Code = "too_many_streams"
	CodeInternal           Code = "internal"
	// CodeCitadelInFailover: the Citadel hands over the command; the request changed nothing.
	CodeCitadelInFailover Code = "citadel_in_failover"
	// CodeConflict: the thing changed under the request (a scroll edited by somebody else).
	CodeConflict Code = "conflict"
)

// APIError is the body of every error of the API: {"error": {...}}.
type APIError struct {
	Code    Code           `json:"code"`
	Params  map[string]any `json:"params,omitempty"`
	Message string         `json:"message"`

	// msg is what Message is made from, when the error is written.
	msg i18n.Msg
	// status is the HTTP status of an error that has no entry in the table of codes
	// (the refusal of the engine): 409 when it is not set.
	status int
}

// WithStatus sets the HTTP status of a refusal of the engine.
func (e *APIError) WithStatus(st int) *APIError {
	e.status = st
	return e
}

// Error is the form of the error for a log: the code and the message code, never translated.
func (e *APIError) Error() string { return string(e.Code) + ": " + e.msg.String() }

// Msg is the message of the error: a code of the catalog and its parameters.
func (e *APIError) Msg() i18n.Msg { return e.msg }

// In is the error with Message in the language: a copy, the error itself stays as it is.
func (e *APIError) In(lang string) APIError {
	out := *e
	out.Message = i18n.Render(lang, e.msg)
	return out
}

// ErrorBody is the JSON the API answers with on an error.
type ErrorBody struct {
	Error APIError `json:"error"`
}

// status is the HTTP status of each code.
var status = map[Code]int{
	CodeInvalidCredentials: http.StatusUnauthorized,
	CodeLoginBlocked:       http.StatusTooManyRequests,
	CodeRateLimited:        http.StatusTooManyRequests,
	CodeUnauthenticated:    http.StatusUnauthorized,
	CodeForbiddenLevel:     http.StatusForbidden,
	CodeBadOrigin:          http.StatusForbidden,
	CodePINRequired:        http.StatusForbidden,
	CodePINInvalid:         http.StatusForbidden,
	CodePINLocked:          http.StatusForbidden,
	CodeNoPIN:              http.StatusForbidden,
	CodeInvalidCode:        http.StatusForbidden,
	CodePasswordTooShort:   http.StatusUnprocessableEntity,
	CodePasswordWrong:      http.StatusForbidden,
	CodeInvalidLogin:       http.StatusUnprocessableEntity,
	CodeInvalidPIN:         http.StatusUnprocessableEntity,
	CodeLoginTaken:         http.StatusConflict,
	CodeBadRequest:         http.StatusBadRequest,
	CodeNotFound:           http.StatusNotFound,
	CodeUnknownTopic:       http.StatusNotFound,
	CodeUnavailable:        http.StatusServiceUnavailable,
	CodePermissionPrompt:   http.StatusConflict,
	CodeTooManyStreams:     http.StatusTooManyRequests,
	CodeInternal:           http.StatusInternalServerError,
	CodeCitadelInFailover:  http.StatusServiceUnavailable,
	CodeConflict:           http.StatusConflict,
}

// Err makes an error of the API: the code of the API, the code of the text in the
// catalog (api.<what>) and the params the client and the text use.
func Err(code Code, msg string, params map[string]any) *APIError {
	return &APIError{Code: code, Params: i18n.WireParams(params), msg: i18n.Msg{Code: msg, Params: params}}
}

// ErrMsg makes the error of a refusal of the engine: the code of the message is the
// code of the error, and its params the params.
func ErrMsg(m i18n.Msg) *APIError {
	return &APIError{Code: Code(m.Code), Params: i18n.WireParams(m.Params), msg: m}
}

// errForbidden is the refusal of level: the text says which level is needed when
// the error tells it. params stay as the front end reads them.
func errForbidden(err error, params map[string]any) *APIError {
	e := Err(CodeForbiddenLevel, "api.level_not_allowed", params)
	var le *users.LevelError
	if errors.As(err, &le) {
		e.msg = le.Msg()
	}
	return e
}

// WriteJSON answers with a JSON body.
func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError answers with the error as {"error": {code, params, message}}.
func WriteError(w http.ResponseWriter, e *APIError) {
	WriteJSON(w, statusOf(e), ErrorBody{Error: e.In(langOf(w))})
}

// statusOf is the HTTP status of an error: the one of its code; a refusal of the engine
// (the code of an area, with a dot) has its own or is 409.
func statusOf(e *APIError) int {
	if st := status[e.Code]; st != 0 {
		return st
	}
	if e.status != 0 {
		return e.status
	}
	if strings.Contains(string(e.Code), ".") {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// langer is a ResponseWriter that knows the language of the one who asked.
type langer interface{ lang() string }

// langOf is the language an error is written in: the one of the writer, pt-BR if it has none.
func langOf(w http.ResponseWriter) string {
	if l, ok := w.(langer); ok {
		return l.lang()
	}
	return i18n.PtBR
}

func errInternal() *APIError {
	return Err(CodeInternal, "api.internal", nil)
}

func errBadRequest() *APIError {
	return Err(CodeBadRequest, "api.bad_request", nil)
}

func errUnauthenticated() *APIError {
	return Err(CodeUnauthenticated, "api.unauthenticated", nil)
}

func errInvalidCredentials() *APIError {
	return Err(CodeInvalidCredentials, "api.invalid_credentials", nil)
}

func errInvalidCode() *APIError {
	return Err(CodeInvalidCode, "api.invalid_code", nil)
}
