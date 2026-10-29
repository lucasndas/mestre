package tables

import (
	"github.com/mestre-ia/mestre/internal/i18n"
)

// coded is an error of the table service that carries its message as a catalog
// code with parameters: the CLI renders it in English for the agent (with the
// parameters on the wire), and whoever shows it to a person can translate it.
// It still is the sentinel it wraps for errors.Is.
type coded struct {
	m   i18n.Msg
	err error
}

// Error is the English text, for logs and for callers that only print.
func (e *coded) Error() string { return i18n.Render(i18n.En, e.m) }

// Msg is the message of the error.
func (e *coded) Msg() i18n.Msg { return e.m }

// Unwrap gives the sentinel, or the cause, the error stands for.
func (e *coded) Unwrap() error { return e.err }

// codedErr makes an error with the message `code` and the pairs `kv`, standing
// for `wrap` (nil for none). A `cause` pair is the text of an underlying error.
func codedErr(wrap error, code string, kv ...any) error {
	return &coded{m: i18n.M(code, kv...), err: wrap}
}

// causeOf is the text of an underlying error, as a parameter.
func causeOf(err error) string { return err.Error() }
