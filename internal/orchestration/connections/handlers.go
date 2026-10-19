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
		return localchan.Fail("not_connected", nc.Error(), "ask the Master of the table for the connection")
	case errors.As(err, &auto):
		return localchan.Fail("automatic_connection", err.Error(), "it goes away by itself when its reason ends").WithExit(localchan.ExitDenied)
	case errors.Is(err, characters.ErrNoCharacter), errors.Is(err, ErrNotFound):
		return localchan.Fail("not_visible", err.Error(), "see `mestre list`")
	case errors.Is(err, ErrGrandMaster), errors.Is(err, ErrLevelDenied):
		return localchan.Fail("level_denied", err.Error(), "ask who has the level (the Master of the table or the Grand Master)")
	case errors.Is(err, ErrTableRequired):
		return localchan.Fail("table_required", err.Error(), "use --table \"<table>\"").WithExit(localchan.ExitUsage)
	case errors.Is(err, ErrDifferentTables):
		return localchan.Fail("different_tables", err.Error(), "a connection links items of the same table").WithExit(localchan.ExitUsage)
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

// AllNames is the arg of Guard for a command whose every argument is a name
// (`wait`).
const AllNames = -1

// Guard wraps the handler of a command that addresses a character by name
// (`ask`, `check`, `wait`): when the name in the argument at index arg (or,
// for AllNames, every argument) is not connected to the caller the command is
// refused before anything happens, with exit 3 and not_connected; one that
// does not exist for the caller is exit 4 and not_visible
// (docs/cli/convencoes.md#regras-e-erros). A call that names no target in its
// arguments (`ask --batch`, `wait --quest`) goes on to its handler, which
// checks the names it finds elsewhere.
func (s *Service) Guard(next localchan.Handler, arg int) localchan.Handler {
	return localchan.HandlerFunc(func(ctx context.Context, c *localchan.Call) localchan.Response {
		x, refused := s.caller(ctx, c)
		if refused != nil {
			return *refused
		}
		args, ok := c.Request.Strings()
		if !ok {
			return localchan.Fail(localchan.CodeUsage, "the arguments are text", "")
		}
		names := args
		if arg != AllNames {
			names = nil
			if arg < len(args) {
				names = args[arg : arg+1]
			}
		}
		for _, name := range names {
			if strings.EqualFold(name, x.Name) {
				continue // the handler says it: asking oneself is a usage error
			}
			if _, err := s.Target(ctx, x.ID, flagString(c, "table"), name); err != nil {
				return Refusal(err)
			}
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
		return localchan.Fail(localchan.CodeUsage, `usage: mestre connect "<A>" "<B>" [--table "<table>"]`, "")
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

// handleList is `mestre list`: the name of the caller, the characters and the
// scrolls it can see, and the connections among them.
func (s *Service) handleList(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := s.caller(ctx, c)
	if refused != nil {
		return *refused
	}
	v, err := s.View(ctx, x.ID)
	if err != nil {
		return Refusal(err)
	}
	type self struct {
		Name    string `json:"name"`
		ID      string `json:"id"`
		Table   string `json:"table"`
		Machine string `json:"machine"`
	}
	type agentRow struct {
		Name       string `json:"name"`
		ID         string `json:"id"`
		Kind       string `json:"kind"`
		Class      string `json:"class"`
		Machine    string `json:"machine"`
		Status     string `json:"status"`
		State      string `json:"state"`
		WaitingFor string `json:"waiting_for"`
		Pinned     bool   `json:"pinned"`
	}
	type noteRow struct {
		Name  string `json:"name"`
		Stack string `json:"stack"`
	}
	type edgeRow struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	me := self{Name: v.Self.Name, ID: v.Self.ID, Table: v.Self.Table, Machine: s.machineName(ctx, v.Self.Machine)}
	var text strings.Builder
	fmt.Fprintf(&text, "you: %s (id %s, table %s, machine %s)\n", me.Name, me.ID, orDash(me.Table), me.Machine)
	agents := make([]agentRow, 0, len(v.Characters))
	text.WriteString("agents:\n")
	for _, ch := range v.Characters {
		r := agentRow{Name: ch.Name, ID: ch.ID, Kind: string(ch.Kind), Class: ch.Class, Machine: s.machineName(ctx, ch.Machine), Status: ch.Status().Label(),
			State: string(ch.State), Pinned: ch.Pinned}
		if ch.State == characters.Waiting || ch.State == characters.Blocked {
			r.WaitingFor = ch.Reason
		}
		agents = append(agents, r)
		fmt.Fprintf(&text, "  %s  class %s  machine %s  %s\n", r.Name, orDash(r.Class), r.Machine, r.Status)
	}
	if len(agents) == 0 {
		text.WriteString("  none\n")
	}
	notes := make([]noteRow, 0, len(v.Scrolls))
	if len(v.Scrolls) > 0 {
		text.WriteString("notes:\n")
	}
	for _, sc := range v.Scrolls {
		notes = append(notes, noteRow{Name: sc.Name, Stack: sc.Stack})
		fmt.Fprintf(&text, "  %s  stack %s\n", sc.Name, orDash(sc.Stack))
	}
	edges := make([]edgeRow, 0, len(v.Edges))
	if len(v.Edges) > 0 {
		text.WriteString("connections:\n")
	}
	for _, e := range v.Edges {
		edges = append(edges, edgeRow(e))
		fmt.Fprintf(&text, "  %s <-> %s\n", e.From, e.To)
	}
	return localchan.OK(map[string]any{"text": strings.TrimRight(text.String(), "\n"), "self": me, "agents": agents, "notes": notes, "connections": edges})
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
