package attach

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mestre-ia/mestre/internal/localchan"
)

// The error codes of the command (docs/cli/attach.md#regras-e-erros) and the
// exit code of each family.
const (
	codeNotForAgents = "not_for_agents"
	codeAuthFailed   = "auth_failed"
	codeSessionEnded = "session_ended"
	codeLevelDenied  = "level_denied"
	codeNotVisible   = "not_visible"
	codeUsage        = "usage"
	codeUnavailable  = "citadel_unavailable"
	codeFailover     = "citadel_in_failover"
	codeTooMany      = "too_many_streams"
	codeInternal     = "internal"
)

var exitOf = map[string]int{
	codeNotForAgents: localchan.ExitDenied,
	codeAuthFailed:   localchan.ExitDenied,
	codeSessionEnded: localchan.ExitDenied,
	codeLevelDenied:  localchan.ExitDenied,
	codeNotVisible:   localchan.ExitNotFound,
	codeUsage:        localchan.ExitUsage,
	codeUnavailable:  localchan.ExitUnavailable,
	codeFailover:     localchan.ExitUnavailable,
	codeTooMany:      localchan.ExitLimit,
	codeInternal:     localchan.ExitInternal,
}

// failure is an error of the command: it ends the run with the exit code of its code.
type failure struct{ code, msg, hint string }

func (f failure) Error() string { return f.code + ": " + f.msg }

func fail(code, hint, format string, a ...any) failure {
	return failure{code: code, msg: fmt.Sprintf(format, a...), hint: hint}
}

func (f failure) exit() int {
	if n, ok := exitOf[f.code]; ok {
		return n
	}
	return localchan.ExitInternal
}

// report writes the error as the convention says: two lines on stderr, or a
// single JSON object on stdout with --json.
func (f failure) report(stdout, stderr io.Writer, asJSON bool) int {
	one := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if asJSON {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": map[string]any{
			"code": f.code, "message": one(f.msg), "hint": one(f.hint), "details": map[string]any{}}})
		fmt.Fprintln(stdout, string(b))
	} else {
		fmt.Fprintf(stderr, "error: %s\nhint: %s\n", one(f.msg), one(f.hint))
	}
	return f.exit()
}
