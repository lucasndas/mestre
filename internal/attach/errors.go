package attach

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mestre-ia/mestre/internal/i18n"
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

// failure is an error of the command: it ends the run with the exit code of its
// code. The message and the hint are catalog messages (area person), said in the
// language of the person when the error is reported.
type failure struct {
	code      string
	msg, hint i18n.Msg
}

func (f failure) Error() string { return f.code + ": " + f.msg.String() }

func fail(code string, hint, msg i18n.Msg) failure {
	return failure{code: code, msg: msg, hint: hint}
}

// ui is the language the command speaks to the person: the one of the machine,
// then the one of the user who logged in (docs/plataforma/idioma-por-canal.md#cli).
type ui struct{ lang string }

// say is a message in the language of the person; without a ui, in pt-BR.
func (e Env) say(m i18n.Msg) string {
	if e.ui == nil {
		return i18n.Render(i18n.PtBR, m)
	}
	return i18n.Render(e.ui.lang, m)
}

func (f failure) exit() int {
	if n, ok := exitOf[f.code]; ok {
		return n
	}
	return localchan.ExitInternal
}

// report writes the error as the convention says: two lines on stderr, or a
// single JSON object on stdout with --json.
func (f failure) report(stdout, stderr io.Writer, asJSON bool, lang string) int {
	one := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if asJSON {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": map[string]any{
			"code": f.code, "message": one(i18n.Render(lang, f.msg)), "hint": one(i18n.Render(lang, f.hint)), "details": map[string]any{}}})
		fmt.Fprintln(stdout, string(b))
	} else {
		fmt.Fprintf(stderr, "error: %s\nhint: %s\n", one(i18n.Render(lang, f.msg)), one(i18n.Render(lang, f.hint)))
	}
	return f.exit()
}
