package panel

import (
	"encoding/json"
	"net/http"
)

// Code is the stable code of an error of the API. The front end translates by
// the code (and the params); Message is the Portuguese fallback.
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
	// CodeRefused: the engine refused an action of the canvas; params.reason says why.
	CodeRefused Code = "refused"
	// CodeConflict: the thing changed under the request (a scroll edited by somebody else).
	CodeConflict Code = "conflict"
)

// APIError is the body of every error of the API: {"error": {...}}.
type APIError struct {
	Code    Code           `json:"code"`
	Params  map[string]any `json:"params,omitempty"`
	Message string         `json:"message"`
}

func (e *APIError) Error() string { return string(e.Code) + ": " + e.Message }

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
	CodeRefused:            http.StatusConflict,
	CodeConflict:           http.StatusConflict,
}

// Err makes an error of the API.
func Err(code Code, message string, params map[string]any) *APIError {
	return &APIError{Code: code, Params: params, Message: message}
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
	st := status[e.Code]
	if st == 0 {
		st = http.StatusInternalServerError
	}
	WriteJSON(w, st, ErrorBody{Error: *e})
}

func errInternal() *APIError {
	return Err(CodeInternal, "erro interno; tente de novo", nil)
}

func errBadRequest() *APIError {
	return Err(CodeBadRequest, "pedido inválido", nil)
}

func errUnauthenticated() *APIError {
	return Err(CodeUnauthenticated, "entre para continuar", nil)
}

func errInvalidCredentials() *APIError {
	return Err(CodeInvalidCredentials, "usuário ou senha inválidos", nil)
}

func errInvalidCode() *APIError {
	return Err(CodeInvalidCode, "código inválido ou expirado", nil)
}
