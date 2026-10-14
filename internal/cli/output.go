package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/localchan"
)

// out prints results and errors in text or --json.
type out struct {
	env  Env
	json bool
}

// response prints a final response and returns its exit code.
func (o out) response(cmd string, r localchan.Response) int {
	code := r.ExitCode()
	switch {
	case o.json:
		o.printJSON(r)
	case r.OK:
		if text := renderText(cmd, r.Data); text != "" {
			fmt.Fprintln(o.env.Stdout, text)
		}
	default:
		o.textError(r.Error)
	}
	return code
}

// fail prints an error response of the CLI itself.
func (o out) fail(r localchan.Response) int {
	code := r.ExitCode()
	if o.json {
		o.printJSON(r)
	} else {
		o.textError(r.Error)
	}
	return code
}

// textError writes the error on stderr, always in two lines: what happened
// and what to do (docs/cli/convencoes.md#saída-em-texto-padrão).
func (o out) textError(e *localchan.Error) {
	if e == nil {
		e = &localchan.Error{Code: localchan.CodeInternal, Message: "the engine answered with no result"}
	}
	fmt.Fprintf(o.env.Stderr, "error: %s\nhint: %s\n", oneLine(e.Message), oneLine(firstNonEmpty(e.Hint, defaultHint(e.Code))))
}

// oneLine keeps a message on its line: the text of a user may hold a
// newline, and the two-line form is a contract of the output.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// defaultHint is what to do when the command gave no hint of its own, by
// the family of the code.
func defaultHint(code string) string {
	switch localchan.ExitFor(code) {
	case localchan.ExitUsage:
		return "mestre help shows how to use each command"
	case localchan.ExitDenied:
		return "mestre help lists what you may use"
	case localchan.ExitNotFound:
		return "mestre list shows what you can see"
	case localchan.ExitUnavailable:
		return "run mestre debug"
	case localchan.ExitTimeout:
		return "the request is still pending; do not send it again"
	case localchan.ExitConflict, localchan.ExitLimit:
		return "see the message; the state or the limit has to change first"
	case localchan.ExitPending:
		return "the result arrives in your inbox"
	}
	return "this is a bug and went to the log; run mestre debug"
}

// printJSON writes the single object of --json: {"ok", "data"} or
// {"ok": false, "error"}, and nothing on stderr.
func (o out) printJSON(r localchan.Response) {
	v := map[string]any{"ok": r.OK}
	var data any
	if len(r.Data) > 0 {
		_ = json.Unmarshal(r.Data, &data)
	}
	if r.OK {
		if data == nil {
			data = map[string]any{}
		}
		v["data"] = data
	} else {
		e := r.Error
		if e == nil {
			e = &localchan.Error{Code: localchan.CodeInternal, Message: "the engine answered with no result"}
		}
		if e.Details == nil {
			e.Details = map[string]any{}
		}
		e.Hint = firstNonEmpty(e.Hint, defaultHint(e.Code))
		v["error"] = e
		if data != nil {
			v["data"] = data
		}
	}
	b, _ := json.Marshal(v)
	fmt.Fprintln(o.env.Stdout, string(b))
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Queued is the answer of a request that was accepted and is pending, not
// done: the Citadel or GitHub is away and the request waits in the outbox.
// Its exit code is 0; the text mode starts with `PENDING:` and the result
// arrives in the inbox with the same op_id
// (docs/cli/convencoes.md#saída---json).
func Queued(opID string) localchan.Response {
	return localchan.OK(map[string]any{"queued": true, "op_id": opID})
}

// renderText is the text mode of the casca: the answer of `ask`, the text of
// a result that has one, otherwise the data as JSON. A queued request starts
// with `PENDING:` and never says the success sentence of the command. The
// tables of each command arrive with the documents of the CLI.
func renderText(cmd string, data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var obj map[string]any
	if json.Unmarshal(data, &obj) == nil {
		if q, _ := obj["queued"].(bool); q {
			op, _ := obj["op_id"].(string)
			return fmt.Sprintf("PENDING: %s queued (op %s); the result will arrive in your inbox", cmd, op)
		}
		key := "text"
		if cmd == "ask" {
			key = "response"
		}
		if s, ok := obj[key].(string); ok {
			return s
		}
	}
	var s string
	if json.Unmarshal(data, &s) == nil {
		return s
	}
	return string(data)
}
