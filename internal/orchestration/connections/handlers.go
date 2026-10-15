package connections

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// Handlers are the commands of this document, to register in the local
// forwarder of the Citadel: `list` and `connect` (docs/cli/comunicacao.md and
// docs/cli/coordenacao.md hold their syntax; the CLI answers their syntax and
// the level, the engine does the rest).
func (s *Service) Handlers() map[string]localchan.Handler {
	return map[string]localchan.Handler{
		"list":    localchan.HandlerFunc(s.handleList),
		"connect": localchan.HandlerFunc(s.handleConnect),
	}
}

// Register registers the handlers in the local forwarder of the Citadel
// (app.Engine.Commands).
func (s *Service) Register(fwd *localchan.LocalForwarder) {
	for cmd, h := range s.Handlers() {
		fwd.Handle(cmd, h)
	}
}

// Refusal turns an error of this package into the answer of the CLI. The codes
// are the ones of docs/cli/convencoes.md#regras-e-erros, and the message of a
// target that is not connected is the one of the document.
func Refusal(err error) localchan.Response {
	var nc *NotConnectedError
	var auto *AutomaticError
	switch {
	case errors.As(err, &nc):
		return localchan.Fail("not_connected", nc.Error(), "peça a conexão ao Mestre da mesa")
	case errors.As(err, &auto):
		return localchan.Fail("automatic_connection", err.Error(), "ela some sozinha quando o motivo acabar").WithExit(localchan.ExitDenied)
	case errors.Is(err, characters.ErrNoCharacter), errors.Is(err, ErrNotFound):
		return localchan.Fail("not_visible", err.Error(), "veja `mestre list`")
	case errors.Is(err, ErrGrandMaster), errors.Is(err, ErrLevelDenied):
		return localchan.Fail("level_denied", err.Error(), "peça a quem tem o nível (o Mestre da mesa ou o Grão-Mestre)")
	case errors.Is(err, ErrTableRequired):
		return localchan.Fail("table_required", err.Error(), "use --table \"<mesa>\"").WithExit(localchan.ExitUsage)
	case errors.Is(err, ErrDifferentTables):
		return localchan.Fail("different_tables", err.Error(), "uma conexão liga itens da mesma mesa").WithExit(localchan.ExitUsage)
	case errors.Is(err, ErrSame):
		return localchan.Fail("same_item", err.Error(), "").WithExit(localchan.ExitUsage)
	case errors.Is(err, ErrNoConnection):
		return localchan.Fail("not_found", err.Error(), "")
	}
	return localchan.Fail(localchan.CodeInternal, err.Error(), "")
}

// caller is the character the engine verified.
func (s *Service) caller(ctx context.Context, c *localchan.Call) (characters.Character, *localchan.Response) {
	if !c.Caller.Identified {
		r := localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character", "run mestre debug")
		return characters.Character{}, &r
	}
	x, err := s.o.Registry.Get(ctx, c.Caller.AgentID)
	if err != nil {
		r := localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this character", "run mestre debug")
		return characters.Character{}, &r
	}
	return x, nil
}

func flagString(c *localchan.Call, name string) string {
	v, _ := c.Request.Flags[name].(string)
	return v
}

// Guard wraps the handler of a command that addresses a character by name
// (`ask`, `check`, `wait`): when the name in the argument at index arg is not
// connected to the caller the command is refused before anything happens, with
// exit 3 and not_connected; one that does not exist for the caller is exit 4
// and not_visible (docs/cli/convencoes.md#regras-e-erros).
func (s *Service) Guard(next localchan.Handler, arg int) localchan.Handler {
	return localchan.HandlerFunc(func(ctx context.Context, c *localchan.Call) localchan.Response {
		x, refused := s.caller(ctx, c)
		if refused != nil {
			return *refused
		}
		args, ok := c.Request.Strings()
		if !ok || arg >= len(args) {
			return localchan.Fail(localchan.CodeUsage, "the name of the target is missing", "")
		}
		if _, err := s.Target(ctx, x.ID, flagString(c, "table"), args[arg]); err != nil {
			return Refusal(err)
		}
		return next.Handle(ctx, c)
	})
}

func (s *Service) handleConnect(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := s.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 2 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre connect "<A>" "<B>" [--table "<mesa>"]`, "")
	}
	res, err := s.Connect(ctx, ConnectRequest{Caller: x, Table: flagString(c, "table"), A: args[0], B: args[1]})
	if err != nil {
		return Refusal(err)
	}
	text := fmt.Sprintf("connected %s and %s (%s)", res.A, res.B, strings.Join(res.Connection.ReasonNames(), ", "))
	if res.AlreadyConnected {
		text = fmt.Sprintf("%s and %s were already connected (%s)", res.A, res.B, strings.Join(res.Connection.ReasonNames(), ", "))
	}
	return localchan.OK(map[string]any{
		"text": text, "a": res.A, "b": res.B, "reasons": res.Connection.ReasonNames(), "already_connected": res.AlreadyConnected,
	})
}

func (s *Service) machineName(ctx context.Context, id string) string {
	if s.o.MachineName != nil {
		if n := s.o.MachineName(ctx, id); n != "" {
			return n
		}
	}
	return id
}

// statePT is the state as the people read it.
var statePT = map[characters.State]string{
	characters.Starting: "iniciando", characters.Idle: "ocioso", characters.Busy: "ocupado", characters.Waiting: "aguardando",
	characters.Blocked: "aguardando", characters.Stuck: "travado", characters.Stopping: "encerrando",
	characters.Stopped: "encerrado", characters.Interrupted: "interrompido",
}

func statusOf(c characters.Character) string {
	st := statePT[c.State]
	if (c.State == characters.Waiting || c.State == characters.Blocked) && c.Reason != "" {
		return st + " " + c.Reason
	}
	return st
}

// handleList is `mestre list`: the name of the caller, the characters and the
// scrolls it can see, and the connections of the scrolls.
func (s *Service) handleList(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := s.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	v, err := s.View(ctx, x.ID)
	if err != nil {
		return Refusal(err)
	}
	type charRow struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Machine string `json:"machine"`
		Class   string `json:"class"`
		State   string `json:"state"`
		Reason  string `json:"reason,omitempty"`
		Status  string `json:"status"`
	}
	type scrollRow struct {
		Name        string   `json:"name"`
		Connections []string `json:"connections"`
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Você é %s.\n", v.Self.Name)
	chars := make([]charRow, 0, len(v.Characters))
	text.WriteString("Personagens:\n")
	for _, ch := range v.Characters {
		r := charRow{Name: ch.Name, Kind: string(ch.Kind), Machine: s.machineName(ctx, ch.Machine), Class: ch.Class,
			State: string(ch.State), Reason: ch.Reason, Status: statusOf(ch)}
		chars = append(chars, r)
		fmt.Fprintf(&text, "- %s (máquina %s, classe %s): %s\n", r.Name, r.Machine, orDash(r.Class), r.Status)
	}
	if len(chars) == 0 {
		text.WriteString("- ninguém\n")
	}
	scrolls := make([]scrollRow, 0, len(v.Scrolls))
	if len(v.Scrolls) > 0 {
		text.WriteString("Pergaminhos:\n")
	}
	for _, sc := range v.Scrolls {
		scrolls = append(scrolls, scrollRow{Name: sc.Name, Connections: orEmpty(sc.Connections)})
		fmt.Fprintf(&text, "- %s (conectado a: %s)\n", sc.Name, orDash(strings.Join(sc.Connections, ", ")))
	}
	return localchan.OK(map[string]any{"text": strings.TrimRight(text.String(), "\n"), "name": v.Self.Name, "characters": chars, "scrolls": scrolls})
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
