package waits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/cli"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/connections"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
)

// Handlers are the commands of this document to register in the local
// forwarder of the Citadel: `ask`, `check`, `wait` and `waits`
// (docs/cli/comunicacao.md has the syntax; the CLI answers the syntax and the
// level). Each one resolves the names it gets through the connections of the
// caller, so a character that is not connected is refused with not_connected
// before anything happens.
func (s *Service) Handlers() map[string]localchan.Handler {
	return map[string]localchan.Handler{
		"ask":   localchan.HandlerFunc(s.handleAsk),
		"check": localchan.HandlerFunc(s.handleCheck),
		"wait":  localchan.HandlerFunc(s.handleWait),
		"waits": localchan.HandlerFunc(s.handleWaits),
	}
}

// Register registers the handlers in the local forwarder of the Citadel.
func (s *Service) Register(fwd *localchan.LocalForwarder) {
	for cmd, h := range s.Handlers() {
		fwd.Handle(cmd, h)
	}
}

// Refusal turns an error of this package, or of the connections, into the
// answer of the CLI.
func Refusal(err error) localchan.Response {
	var unreachable *characters.MachineUnreachableError
	switch {
	case errors.Is(err, ErrSelf):
		return localchan.Fail(localchan.CodeUsage, err.Error(), "use another name")
	case errors.Is(err, ErrPast), errors.Is(err, ErrBadWait):
		return localchan.Fail(localchan.CodeUsage, err.Error(), "")
	case errors.Is(err, ErrNoQuest), errors.Is(err, ErrNoWait), errors.Is(err, inbox.ErrNoItem):
		return localchan.Fail("not_found", err.Error(), "`mestre inbox` shows the ids")
	case errors.Is(err, ErrNoAttachments):
		return localchan.Fail(localchan.CodeUnavailable, err.Error(), "send the text and say where the files are")
	case errors.Is(err, characters.ErrBusy):
		return localchan.Fail("busy", err.Error(), "send a normal message, which waits in its inbox")
	case errors.Is(err, characters.ErrPermissionPrompt):
		return localchan.Fail("permission_prompt", err.Error(), "a permission is decided by the approval flow, never by keys")
	case errors.As(err, &unreachable):
		return localchan.Fail("machine_unreachable", err.Error(), "try again when the machine is back")
	}
	return connections.Refusal(err)
}

func flagString(c *localchan.Call, name string) string {
	v, _ := c.Request.Flags[name].(string)
	return v
}

func flagBool(c *localchan.Call, name string) bool {
	v, _ := c.Request.Flags[name].(bool)
	return v
}

func flagDuration(c *localchan.Call, name string) (time.Duration, error) {
	v := flagString(c, name)
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%w: --%s %q is not a duration", ErrBadWait, name, v)
	}
	return d, nil
}

// target finds the character a command names, among the ones connected to the
// caller.
func (s *Service) target(ctx context.Context, x characters.Character, c *localchan.Call, name string) (characters.Character, error) {
	if strings.EqualFold(name, x.Name) && x.Role == characters.RoleCharacter {
		return characters.Character{}, fmt.Errorf("%w: %s", ErrSelf, name)
	}
	if s.o.Connections == nil {
		return characters.Character{}, errors.New("waits: no connections to find the characters")
	}
	t, err := s.o.Connections.Target(ctx, x.ID, flagString(c, "table"), name)
	if err != nil {
		return characters.Character{}, err
	}
	if t.ID == x.ID {
		return characters.Character{}, fmt.Errorf("%w: %s", ErrSelf, name)
	}
	return t, nil
}

// handleAsk is `mestre ask`, in its three forms: the normal one, `--batch` and
// `--raw`.
func (s *Service) handleAsk(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := inbox.Caller(ctx, s.o.Registry, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok {
		return localchan.Fail(localchan.CodeUsage, "the arguments of ask are text", "")
	}
	timeout, err := flagDuration(c, "timeout")
	if err != nil {
		return Refusal(err)
	}
	if batch := flagString(c, "batch"); batch != "" {
		return s.askBatch(ctx, x, c, batch, timeout)
	}
	if raw, has := c.Request.Flags["raw"]; has {
		text, _ := raw.(string)
		if len(args) != 1 {
			return localchan.Fail(localchan.CodeUsage, `usage: mestre ask "<name>" --raw "<text>"`, "")
		}
		return s.askRaw(ctx, x, c, args[0], text)
	}
	from := flagString(c, "from-message")
	if len(args) != 2 && (from == "" || len(args) < 1 || len(args) > 2) {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre ask "<name>" "<prompt>" [--timeout <dur>] [--from-message <id>]`, "")
	}
	t, err := s.target(ctx, x, c, args[0])
	if err != nil {
		return Refusal(err)
	}
	prompt := ""
	if len(args) == 2 {
		prompt = args[1]
	}
	if from != "" {
		if prompt, err = s.passOn(ctx, x, t, from, prompt); err != nil {
			return Refusal(err)
		}
	}
	out, err := s.Ask(ctx, AskRequest{From: x, To: t, Prompt: prompt, Timeout: timeout, NoWait: flagBool(c, "no-wait"), Gone: c.Gone,
		Progress: func(status string) { c.Partial(map[string]any{"status": status, "to": t.Name}) }})
	if err != nil {
		return Refusal(err)
	}
	return askResponse(out)
}

func askResponse(out AskOutcome) localchan.Response {
	data := map[string]any{"to": out.To, "request_id": out.ID, "status": string(out.Status)}
	switch out.Status {
	case StatusAnswered:
		data["response"], data["text"] = out.Response, out.Response
		return localchan.OK(data)
	case StatusPending:
		r := localchan.Fail("timeout", "no answer in time: the request is still open and the answer arrives in your inbox",
			"do not send it again; use `mestre check` and wait")
		r.Error.Details = data
		return r
	case StatusEnded:
		data["text"] = out.Response
		return localchan.OK(data).WithExit(localchan.ExitNotFound)
	case StatusSent:
		data["text"] = "entregue, sem esperar: a resposta chega na sua caixa de entrada"
	default:
		data["text"] = "entregue"
	}
	return localchan.OK(data)
}

// askBatch is `ask --batch`: one ask per target, in parallel.
func (s *Service) askBatch(ctx context.Context, x characters.Character, c *localchan.Call, batch string, timeout time.Duration) localchan.Response {
	var prompts map[string]string
	if err := json.Unmarshal([]byte(batch), &prompts); err != nil || len(prompts) == 0 {
		return localchan.Fail(localchan.CodeUsage, `--batch is a JSON object: {"Name": "prompt", …}`, "")
	}
	names := make([]string, 0, len(prompts))
	for n := range prompts {
		names = append(names, n)
	}
	slices.Sort(names)
	targets := make([]characters.Character, len(names))
	for i, n := range names {
		t, err := s.target(ctx, x, c, n)
		if err != nil {
			return Refusal(err) // nothing is asked if one of the names is not valid
		}
		targets[i] = t
	}
	outs := make([]AskOutcome, len(names))
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	for i := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i], errs[i] = s.Ask(ctx, AskRequest{From: x, To: targets[i], Prompt: prompts[names[i]], Timeout: timeout, NoWait: flagBool(c, "no-wait"), Gone: c.Gone,
				Progress: func(status string) { c.Partial(map[string]any{"status": status, "to": targets[i].Name}) }})
		}()
	}
	wg.Wait()
	type result struct {
		Status   string `json:"status"`
		Response string `json:"response,omitempty"`
		Error    string `json:"error,omitempty"`
	}
	results := map[string]result{}
	pending := false
	for i, n := range names {
		switch {
		case errs[i] != nil:
			results[n] = result{Status: "error", Error: errs[i].Error()}
		default:
			results[n] = result{Status: string(outs[i].Status), Response: outs[i].Response}
			pending = pending || outs[i].Status == StatusPending
		}
	}
	b, _ := json.Marshal(map[string]any{"results": results})
	r := localchan.OK(map[string]any{"results": results, "text": string(b)})
	if pending {
		r = r.WithExit(localchan.ExitTimeout)
	}
	return r
}

// askRaw is `ask --raw`: input typed at once into the terminal, past the queue,
// only into a free target (idle, stuck, blocked for input) or the terminal of
// a spawn. A busy one, or one that waits for a permission, is refused and
// nothing is typed.
func (s *Service) askRaw(ctx context.Context, x characters.Character, c *localchan.Call, name, raw string) localchan.Response {
	t, err := s.target(ctx, x, c, name)
	if err != nil {
		return Refusal(err)
	}
	data, err := UnescapeRaw(raw)
	if err != nil {
		return localchan.Fail(localchan.CodeUsage, err.Error(), `escapes are \n, \t, \xNN and \e`)
	}
	if s.o.Terminals == nil {
		return localchan.Fail("machine_unreachable", "the terminals are not reachable from this engine", "")
	}
	if t.Kind != characters.KindTerminal {
		if refusal, ok := cli.CheckRaw(string(t.State), t.Reason); !ok {
			return refusal
		}
	}
	if err := s.o.Terminals.Input(ctx, t.Machine, t.ID, data); err != nil {
		return Refusal(err)
	}
	return localchan.OK(map[string]any{"text": i18n.Render(i18n.En, i18n.M("cli.typed", "count", len(data), "name", t.Name)), "to": t.Name, "status": "typed", "bytes": len(data)})
}

// UnescapeRaw reads the text of `ask --raw`: `\n`, `\r`, `\t`, `\\`, `\xNN`
// (two hexadecimal digits, ex.: `\x03` is Ctrl+C) and `\e` (ESC, so the arrows
// are `\e[A`, `\e[B`, `\e[C` and `\e[D`).
func UnescapeRaw(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			out = append(out, s[i])
			continue
		}
		i++
		if i >= len(s) {
			return nil, errors.New(`the text ends in a lone \`)
		}
		switch s[i] {
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'e':
			out = append(out, 0x1b)
		case '\\':
			out = append(out, '\\')
		case 'x':
			hi, ok1 := hexVal(s, i+1)
			lo, ok2 := hexVal(s, i+2)
			if !ok1 || !ok2 {
				return nil, errors.New(`\x needs two hexadecimal digits`)
			}
			out = append(out, hi<<4|lo)
			i += 2
		default:
			return nil, fmt.Errorf(`unknown escape \%c`, s[i])
		}
	}
	return out, nil
}

func hexVal(s string, i int) (byte, bool) {
	if i >= len(s) {
		return 0, false
	}
	switch c := s[i]; {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// handleCheck is `mestre check`: the current screen of a target, in any state,
// sending nothing.
func (s *Service) handleCheck(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := inbox.Caller(ctx, s.o.Registry, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre check "<name>" [--lines <n>]`, "")
	}
	t, err := s.target(ctx, x, c, args[0])
	if err != nil {
		return Refusal(err)
	}
	if s.o.Terminals == nil {
		return localchan.Fail("machine_unreachable", "the terminals are not reachable from this engine", "")
	}
	lines := 0
	if v := flagString(c, "lines"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 {
			return localchan.Fail(localchan.CodeUsage, fmt.Sprintf("--lines %q is not a number of lines", v), "")
		}
		lines = n
	}
	var sc characters.Screen
	if ht, ok := s.o.Terminals.(HistoryTerminals); ok && lines > 0 {
		sc, err = ht.ScreenLines(ctx, t.Machine, t.ID, lines)
	} else {
		sc, err = s.o.Terminals.Screen(ctx, t.Machine, t.ID)
	}
	if err != nil {
		return Refusal(err)
	}
	waiting, _, _ := s.Active(ctx, t.ID)
	status := t.Status().Label()
	screen := sc.Text
	if sc.History != "" {
		screen = strings.TrimRight(sc.History, "\n") + "\n" + sc.Text
	}
	return localchan.OK(map[string]any{"text": screen, "name": t.Name, "status": status, "waiting_for": waiting, "screen": screen, "captured_at": s.clk.Now().Format(time.RFC3339)})
}

// handleWait is `mestre wait`: it registers the wait and returns at once.
func (s *Service) handleWait(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := inbox.Caller(ctx, s.o.Registry, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok {
		return localchan.Fail(localchan.CodeUsage, "the arguments of wait are text", "")
	}
	timeout, err := flagDuration(c, "timeout")
	if err != nil {
		return Refusal(err)
	}
	var w Wait
	switch quest, until := flagString(c, "quest"), flagString(c, "until"); {
	case until != "":
		at, perr := s.ParseUntil(until)
		if perr != nil {
			return Refusal(perr)
		}
		w, err = s.WaitUntil(ctx, x, at)
	case quest != "":
		w, err = s.WaitQuest(ctx, QuestRequest{Waiter: x, Table: flagString(c, "table"), Quest: quest, Timeout: timeout})
	case len(args) > 0:
		w, err = s.WaitFor(ctx, WaitRequest{Waiter: x, Table: flagString(c, "table"), Names: args, First: flagBool(c, "first"), Timeout: timeout})
	default:
		return localchan.Fail(localchan.CodeUsage, `usage: mestre wait "<name>" … | --quest "<quest>" | --until "<HH:MM>"`, "")
	}
	if err != nil {
		return Refusal(err)
	}
	targets := make([]string, len(w.Targets))
	for i, t := range w.Targets {
		targets[i] = t.Name
	}
	data := map[string]any{"wait_id": w.ID, "kind": string(w.Kind), "targets": targets, "text": fmt.Sprintf("wait %s registered (waiting for %s); end your turn: the engine wakes you up", w.ID, w.Reason)}
	if !w.TimeoutAt.IsZero() {
		data["timeout_at"] = w.TimeoutAt.Format(time.RFC3339)
	}
	return localchan.OK(data)
}

// handleWaits is `mestre waits`: the map of waits and the deadlocks.
func (s *Service) handleWaits(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := inbox.Caller(ctx, s.o.Registry, c)
	if refused != nil {
		return *refused
	}
	all, table := flagBool(c, "all"), flagString(c, "table")
	switch {
	case x.Role == characters.RoleGrandMaster && table == "":
		all = true
	case x.Role != characters.RoleGrandMaster && (all || (table != "" && table != x.Table)):
		return localchan.Fail("level_denied", "only the Grand Master sees the waits of other tables", "")
	case x.Role != characters.RoleGrandMaster:
		table = x.Table
	}
	m, err := s.Map(ctx, x.Workspace, table, all)
	if err != nil {
		return Refusal(err)
	}
	type row struct {
		ID        string `json:"id"`
		Waiter    string `json:"waiter"`
		Target    string `json:"target"`
		Kind      string `json:"kind"`
		Reason    string `json:"reason"`
		Since     string `json:"since"`
		TimeoutAt string `json:"timeout_at,omitempty"`
	}
	rows := make([]row, 0, len(m.Rows))
	var text strings.Builder
	for _, r := range m.Rows {
		jr := row{ID: r.ID, Waiter: r.Waiter, Target: r.Target, Kind: r.Kind, Reason: r.Reason, Since: r.Since.Format(time.RFC3339)}
		if !r.TimeoutAt.IsZero() {
			jr.TimeoutAt = r.TimeoutAt.Format(time.RFC3339)
		}
		rows = append(rows, jr)
		fmt.Fprintf(&text, "%s -> %s  %s  %s  %s\n", r.Waiter, r.Target, r.Reason, jr.Since, jr.TimeoutAt)
	}
	cycles := make([][]string, 0, len(m.Cycles))
	for _, cy := range m.Cycles {
		cycles = append(cycles, strings.Split(cy.Text, " → "))
		fmt.Fprintf(&text, "CYCLE: %s\n", strings.ReplaceAll(cy.Text, " → ", " -> "))
	}
	if len(rows) == 0 && len(cycles) == 0 {
		text.WriteString("nenhuma espera ativa\n")
	}
	return localchan.OK(map[string]any{"text": strings.TrimRight(text.String(), "\n"), "waits": rows, "cycles": cycles})
}

// passOn is the prompt of `ask --from-message`: a message of the inbox of the
// caller, as it came (text and attachments, untouched), with the prompt, if
// there is one, before it as a note. The attachments are copied into the table
// of the target and the text says where.
func (s *Service) passOn(ctx context.Context, x, target characters.Character, id, note string) (string, error) {
	it, err := s.o.Inbox.Store().Get(ctx, id)
	if err != nil || it.Target != x.ID {
		return "", fmt.Errorf("%w: the message %s is not in your inbox", inbox.ErrNoItem, id)
	}
	var b strings.Builder
	if strings.TrimSpace(note) != "" {
		b.WriteString(note)
		b.WriteString("\n\n--- Mensagem repassada ---\n")
	}
	b.WriteString(it.Text)
	if len(it.Attachments) > 0 {
		if s.o.Attachments == nil {
			return "", fmt.Errorf("%w: the attachments of the message %s cannot be forwarded", ErrNoAttachments, id)
		}
		copies, err := s.o.Attachments.Forward(ctx, target.Workspace, target.Table, it.Attachments)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrNoAttachments, err)
		}
		for _, p := range copies {
			fmt.Fprintf(&b, "\n[anexo] %s", p)
		}
	}
	return b.String(), nil
}
