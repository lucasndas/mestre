package inbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
)

// Handlers are the commands of this document to register in the local
// forwarder of the Citadel: `inbox` and `reply` (docs/cli/comunicacao.md has
// the syntax; the CLI answers the syntax, the level and the secrets).
func (s *Service) Handlers() map[string]localchan.Handler {
	return map[string]localchan.Handler{
		"inbox": localchan.HandlerFunc(s.handleInbox),
		"reply": localchan.HandlerFunc(s.handleReply),
	}
}

// Register registers the handlers in the local forwarder of the Citadel.
func (s *Service) Register(fwd *localchan.LocalForwarder) {
	for cmd, h := range s.Handlers() {
		fwd.Handle(cmd, h)
	}
}

// Caller finds the character the engine verified as the caller of a call.
func Caller(ctx context.Context, reg characters.Registry, c *localchan.Call) (characters.Character, *localchan.Response) {
	if !c.Caller.Identified {
		r := localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character", "run mestre debug")
		return characters.Character{}, &r
	}
	x, err := reg.Get(ctx, c.Caller.AgentID)
	if err != nil {
		r := localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this character", "run mestre debug")
		return characters.Character{}, &r
	}
	return x, nil
}

// Refusal turns an error of this package into the answer of the CLI.
func Refusal(err error) localchan.Response {
	switch {
	case errors.Is(err, ErrNothingToReply):
		return localchan.Fail("nothing_to_reply", err.Error(), "see `mestre inbox` for the id of the message").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrNotGrandMaster):
		return localchan.Fail("level_denied", err.Error(), "only the Grand Master answers the users")
	case errors.Is(err, ErrNoItem):
		return localchan.Fail("not_found", err.Error(), "")
	}
	return localchan.Fail(localchan.CodeInternal, err.Error(), "")
}

func flagString(c *localchan.Call, name string) string {
	v, _ := c.Request.Flags[name].(string)
	return v
}

// flagList reads a flag that may be repeated: a list of text once it crossed
// the channel as JSON.
func flagList(c *localchan.Call, name string) []string {
	var out []string
	switch v := c.Request.Flags[name].(type) {
	case []string:
		out = append(out, v...)
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func flagBool(c *localchan.Call, name string) bool {
	v, _ := c.Request.Flags[name].(bool)
	return v
}

// excerpt is the start of a text, in one line.
func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// handleInbox is `mestre inbox`: the queue of the caller, in the order of
// delivery.
func (s *Service) handleInbox(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := Caller(ctx, s.o.Registry, c)
	if refused != nil {
		return *refused
	}
	items, err := s.st.Pending(ctx, x.ID)
	if err != nil {
		return Refusal(err)
	}
	full := flagBool(c, "full")
	type row struct {
		ID         string `json:"id"`
		From       string `json:"from"`
		Kind       string `json:"kind"`
		Priority   int    `json:"priority"`
		ReceivedAt string `json:"received_at"`
		Text       string `json:"text"`
	}
	rows := make([]row, 0, len(items))
	var text strings.Builder
	for _, it := range items {
		body := excerpt(it.Text, 80)
		if full {
			body = it.Text
		}
		r := row{ID: it.ID, From: it.Sender, Kind: string(it.Kind), Priority: int(it.Priority), ReceivedAt: it.CreatedAt.Format(time.RFC3339), Text: body}
		rows = append(rows, r)
		fmt.Fprintf(&text, "%s  %-8s  de %s  prioridade %d  %s  %s\n", r.ID, r.Kind, orSystem(r.From), r.Priority, r.ReceivedAt, r.Text)
	}
	if len(rows) == 0 {
		text.WriteString("a caixa de entrada está vazia\n")
	}
	return localchan.OK(map[string]any{"text": strings.TrimRight(text.String(), "\n"), "items": rows})
}

func orSystem(s string) string {
	if s == "" {
		return SystemSender
	}
	return s
}

// handleReply is `mestre reply`: the answer of the Grand Master to the message
// of a user. The detector of secrets runs before, in the gate of the CLI.
func (s *Service) handleReply(ctx context.Context, c *localchan.Call) localchan.Response {
	x, refused := Caller(ctx, s.o.Registry, c)
	if refused != nil {
		return *refused
	}
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre reply "<text>" [--to-message <id>]`, "")
	}
	attach := flagList(c, "attach")
	res, err := s.Reply(ctx, ReplyRequest{From: x, Text: args[0], Attachments: attach, ToMessage: flagString(c, "to-message"), RedactSource: flagBool(c, "redact-source")})
	if err != nil {
		return Refusal(err)
	}
	text := "resposta enviada pelo canal de origem"
	if flagBool(c, "redact-source") {
		text = "a mensagem com o segredo foi apagada; " + text
	}
	if res.Delivery == DeliveryQueued {
		text = "o canal de origem está fora do ar: a resposta ficou guardada e será entregue quando ele voltar"
	}
	return localchan.OK(map[string]any{"text": text, "message": res.Message, "channel": res.Channel, "delivery": string(res.Delivery)})
}
