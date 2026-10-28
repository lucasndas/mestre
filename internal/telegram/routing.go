package telegram

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mestre-ia/mestre/internal/attachments"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/users"
)

// WorkspaceAction is the button that makes a workspace the active one (the
// answer to the question of a message that waits, and /workspace).
const WorkspaceAction = "workspace"

// Delivery is a message of a user that was routed to the Grand Master of a
// workspace (docs/telegram/roteamento.md#entrega-ao-grão-mestre).
type Delivery struct {
	Workspace string
	// Mesa is the mesa the message cites (a reply to a message about one): a
	// hint, the Grand Master decides where it goes.
	Mesa string
	// Inbound is the message; its Text is the one to deliver (the shortcut
	// `#nome` taken off).
	Inbound
	// ShortcutUsed says the workspace came from `#nome`.
	ShortcutUsed bool
	// File is the file the message carried, received and kept; nil for a text.
	File *attachments.Stored
}

// Files receives the files of the messages once the workspace is known
// (attachments.Service).
type Files interface {
	Receive(ctx context.Context, r attachments.Request) (attachments.Stored, error)
}

// Prefetching is the part of Files that lets the bot download the file of an update before it
// holds the gate of the handover of the Citadel: Fetch only downloads, ReceiveData keeps the
// bytes (attachments.Service has both).
type Prefetching interface {
	Fetch(ctx context.Context, f attachments.Ref) ([]byte, error)
	ReceiveData(ctx context.Context, r attachments.Request, data []byte) (attachments.Stored, error)
}

// Prefetcher is the part of a Router that downloads, outside the gate of the handover, the files an
// update will need; the context it returns carries them to Deliver and to the actions.
type Prefetcher interface {
	Prefetch(ctx context.Context, up Update) context.Context
}

type prefetchKey struct{}

// prefetched is what a download gave, by the id of the file.
type prefetched struct {
	data []byte
	err  error
}

// Prefetch implements Prefetcher: the file of a message of a user who may talk to the Grand Master, and
// the files that wait for the choice of a workspace when the click is that choice. Anything else
// downloads nothing.
func (r *Routing) Prefetch(ctx context.Context, up Update) context.Context {
	pf, ok := r.Files.(Prefetching)
	if !ok || r.bot == nil {
		return ctx
	}
	var refs []attachments.Ref
	switch {
	case up.Message != nil && up.Message.From != nil:
		ref := up.Message.FileOf()
		if ref == nil {
			return ctx
		}
		u, err := r.bot.o.Users.ByTelegram(ctx, up.Message.From.ID)
		if err != nil || u.State != users.Active || users.Require(u.Level, users.TalkToGrandMaster) != nil {
			return ctx
		}
		refs = append(refs, *ref)
	case up.CallbackQuery != nil:
		cq := up.CallbackQuery
		u, err := r.bot.o.Users.ByTelegram(ctx, cq.From.ID)
		if err != nil || u.State != users.Active {
			return ctx
		}
		cb, found, err := r.bot.Store().Callback(ctx, cq.Data)
		if err != nil || !found || cb.Action != WorkspaceAction || cb.UserID != u.ID || cb.State != "open" {
			return ctx
		}
		parked, err := r.bot.Store().Parked(ctx, u.ID)
		if err != nil {
			return ctx
		}
		for _, h := range parked {
			if h.File != nil {
				refs = append(refs, *h.File)
			}
		}
	}
	if len(refs) == 0 {
		return ctx
	}
	set := make(map[string]prefetched, len(refs))
	for _, ref := range refs {
		data, err := pf.Fetch(ctx, ref)
		set[ref.ID] = prefetched{data: data, err: err}
	}
	return context.WithValue(ctx, prefetchKey{}, set)
}

// Deliverer takes a routed message to the inbox of the Grand Master.
type Deliverer interface {
	Deliver(ctx context.Context, d Delivery) error
}

// Routing is the Router of the bot: it decides the workspace of each message of
// a user by the rules of docs/telegram/roteamento.md and hands it to the
// Deliverer. The bot is made with it as its Router, and then bound.
type Routing struct {
	To Deliverer
	// Files receives the attachments; nil: a message with a file is answered
	// that the engine cannot receive it, and nothing is delivered.
	Files Files

	bot *Bot
}

var _ Router = (*Routing)(nil)

// Bind ties the routing to the bot.
func (r *Routing) Bind(bot *Bot) { r.bot = bot }

// Commands is /workspace, for Options.Commands.
func (r *Routing) Commands() []Command {
	return []Command{{Name: "workspace", Description: "mostra e troca o workspace ativo", Capability: users.TalkToGrandMaster, Run: r.workspaceCommand}}
}

// Actions are the buttons, for Options.Actions.
func (r *Routing) Actions() map[string]Action {
	return map[string]Action{WorkspaceAction: r.choose}
}

var shortcutRe = regexp.MustCompile(`^#([A-Za-z0-9][A-Za-z0-9_-]*)(?:\s+([\s\S]*))?$`)

// shortcut reads `#<workspace> text` at the start of a text.
func shortcut(text string) (workspace, rest string, ok bool) {
	m := shortcutRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", "", false
	}
	return strings.ToLower(m[1]), strings.TrimSpace(m[2]), true
}

// Deliver implements Router.
func (r *Routing) Deliver(ctx context.Context, in Inbound) error {
	st := r.bot.Store()
	all, err := st.Workspaces(ctx)
	if err != nil {
		return err
	}
	// The user as stored now: the level and the workspaces are the engine's.
	u, err := r.bot.o.Users.Get(ctx, in.User.ID)
	if err != nil {
		return err
	}
	in.User = u
	allowed := permitted(all, u)
	switch {
	case in.HasReplyWorkspace && in.ReplyWorkspace == "":
		return r.systemReply(ctx, in) // a reply to a "Sistema" message: the engine answers
	case in.HasReplyWorkspace:
		return r.to(ctx, in, allowed, in.ReplyWorkspace, in.ReplyMesa, in.Text, false)
	}
	if ws, rest, ok := shortcut(in.Text); ok {
		if rest == "" && in.Kind == KindText {
			r.say(ctx, in, fmt.Sprintf("Escreva a mensagem depois de #%s.", ws))
			return nil
		}
		return r.to(ctx, in, allowed, ws, "", rest, true)
	}
	switch len(allowed) {
	case 0:
		r.say(ctx, in, "Você não atua em nenhuma campanha ainda. Peça ao Senhor da Guilda para incluí-lo em um.")
		return nil
	case 1:
		return r.to(ctx, in, allowed, allowed[0], "", in.Text, false)
	}
	active, err := st.Active(ctx, u.ID)
	if err != nil {
		return err
	}
	if contains(allowed, active) {
		return r.to(ctx, in, allowed, active, "", in.Text, false)
	}
	// acts in several and did not choose: the message is kept, the user is asked
	if err := st.Park(ctx, in); err != nil {
		return err
	}
	r.ask(ctx, in, allowed, "Em qual campanha entrego a sua mensagem? A escolha passa a ser a sua campanha ativa.")
	return nil
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// to delivers the message to the workspace, if the user acts in it; a workspace
// that is not his, or does not exist, gets the same answer.
func (r *Routing) to(ctx context.Context, in Inbound, allowed []string, ws, mesa, text string, short bool) error {
	if !contains(allowed, ws) {
		r.say(ctx, in, "Você não atua em #"+ws+". "+listOf(allowed))
		return nil
	}
	in.Text = text
	d := Delivery{Workspace: ws, Mesa: mesa, Inbound: in, ShortcutUsed: short}
	if in.File != nil {
		stored, ok := r.receive(ctx, in, ws)
		if !ok {
			return nil
		}
		d.File = &stored
	}
	return r.To.Deliver(ctx, d)
}

// receive keeps the file of the message; when it can not, the user is told why
// and nothing goes to the Grão-Mestre (a file that failed is not delivered).
func (r *Routing) receive(ctx context.Context, in Inbound, ws string) (attachments.Stored, bool) {
	if r.Files == nil {
		r.say(ctx, in, "Não consigo receber arquivos agora.")
		return attachments.Stored{}, false
	}
	req := attachments.Request{Workspace: ws, UserID: in.User.ID, File: *in.File, Caption: in.Text,
		Origin: attachments.Origin{Channel: "telegram", ChatID: in.ChatID, MessageID: in.MessageID}}
	var stored attachments.Stored
	var err error
	set, _ := ctx.Value(prefetchKey{}).(map[string]prefetched)
	if got, ok := set[in.File.ID]; ok {
		// downloaded before the gate of the handover was held: here it is only kept
		if pf, isPf := r.Files.(Prefetching); isPf {
			if got.err != nil {
				err = got.err
			} else {
				stored, err = pf.ReceiveData(ctx, req, got.data)
			}
		} else {
			stored, err = r.Files.Receive(ctx, req)
		}
	} else {
		stored, err = r.Files.Receive(ctx, req)
	}
	var big *attachments.TooLargeError
	switch {
	case errors.As(err, &big):
		r.say(ctx, in, fmt.Sprintf("O arquivo passa do limite de %s. Não baixei nada: mande um menor.", attachments.HumanSize(big.Limit)))
	case errors.Is(err, attachments.ErrDownload):
		r.say(ctx, in, fmt.Sprintf("Não consegui baixar o arquivo depois de %d tentativas. Tente de novo em instantes.", attachments.Attempts))
	case err != nil:
		r.bot.log.Warn("telegram: an attachment was not received", "err", err)
		r.say(ctx, in, "Não consegui guardar o arquivo. Tente de novo em instantes.")
	default:
		return stored, true
	}
	return attachments.Stored{}, false
}

func listOf(allowed []string) string {
	if len(allowed) == 0 {
		return "Você não atua em nenhuma campanha."
	}
	names := make([]string, len(allowed))
	for i, w := range allowed {
		names[i] = "#" + w
	}
	return "Campanhas que você pode usar: " + strings.Join(names, ", ") + "."
}

// say answers the user from the engine.
func (r *Routing) say(ctx context.Context, in Inbound, text string) {
	r.bot.say(ctx, in.ChatID, in.User.ID, text)
}

// ask puts the workspaces in buttons.
func (r *Routing) ask(ctx context.Context, in Inbound, allowed []string, text string) {
	active, _ := r.bot.Store().Active(ctx, in.User.ID)
	var rows [][]Button
	for _, w := range allowed {
		label := w
		if w == active {
			label = "● " + w
		}
		rows = append(rows, []Button{{Text: label, Action: WorkspaceAction, Arg: w, Capability: users.TalkToGrandMaster}})
	}
	if _, err := r.bot.Send(ctx, Out{ChatID: in.ChatID, UserID: in.User.ID, Text: text, Buttons: rows, Origin: "motor"}); err != nil {
		r.bot.log.Warn("telegram: the question of the workspace was not queued", "err", err)
	}
}

// systemReply answers a text that replies to a "Sistema" message: the engine
// treats it, and nothing goes to an agent. It says what that message allows.
func (r *Routing) systemReply(ctx context.Context, in Inbound) error {
	actions, err := r.bot.Store().MessageActions(ctx, in.ChatID, in.ReplyToMessage)
	if err != nil {
		return err
	}
	if actions == "" {
		r.say(ctx, in, "Essa mensagem do Sistema é um aviso: não há nada a responder.")
		return nil
	}
	r.say(ctx, in, "Essa mensagem do Sistema se responde pelos botões: "+strings.ReplaceAll(actions, "\n", ", ")+".")
	return nil
}

// workspaceCommand is /workspace.
func (r *Routing) workspaceCommand(ctx context.Context, call Call) (string, error) {
	all, err := r.bot.Store().Workspaces(ctx)
	if err != nil {
		return "", err
	}
	allowed := permitted(all, call.User)
	switch len(allowed) {
	case 0:
		return "Você não atua em nenhuma campanha.", nil
	case 1:
		return "Você atua só em #" + allowed[0] + ": ela é sempre a sua campanha ativa.", nil
	}
	r.ask(ctx, Inbound{User: call.User, ChatID: call.ChatID}, allowed, "Escolha a campanha ativa. O `#nome` no começo de uma mensagem a leva a outro sem trocar a ativa.")
	return "", nil
}

// choose makes the workspace the active one and delivers what waited for the choice.
func (r *Routing) choose(ctx context.Context, k Click) (string, string, error) {
	st := r.bot.Store()
	all, err := st.Workspaces(ctx)
	if err != nil {
		return "", "", err
	}
	// the level and the workspaces as stored now, not as the button remembers
	u, err := r.bot.o.Users.Get(ctx, k.User.ID)
	if err != nil {
		return "", "", err
	}
	allowed := permitted(all, u)
	if !contains(allowed, k.Arg) {
		return "", "", errors.New("você não atua nessa campanha")
	}
	if users.Require(u.Level, users.TalkToGrandMaster) != nil {
		return "", "", errors.New("com o seu nível você não fala com o Grão-Mestre")
	}
	if err := st.SetActive(ctx, u.ID, k.Arg); err != nil {
		return "", "", err
	}
	parked, err := st.Parked(ctx, u.ID)
	if err != nil {
		return "", "", err
	}
	for _, h := range parked {
		in := h.Inbound
		in.User = u
		d := Delivery{Workspace: k.Arg, Inbound: in}
		if in.File != nil {
			stored, ok := r.receive(ctx, in, k.Arg)
			if !ok {
				_ = st.Unpark(ctx, h.ID) // the user was told; the file is not kept waiting
				continue
			}
			d.File = &stored
		}
		if err := r.To.Deliver(ctx, d); err != nil {
			return "", "", fmt.Errorf("a mensagem não foi entregue: %w", err)
		}
		if err := st.Unpark(ctx, h.ID); err != nil {
			return "", "", err
		}
	}
	text := "Campanha ativa: #" + k.Arg + "."
	if len(parked) > 0 {
		text += " Entreguei a sua mensagem."
	}
	return "Ativo: " + k.Arg, text, nil
}

// OriginOf is the origin a message of the Telegram carries in the inbox of the
// Grand Master (docs/telegram/roteamento.md#entrega-ao-grão-mestre): the user, the
// channel, the ids of the chat and of the message and the mesa it cites. The
// answer of the Grand Master goes back through it.
func OriginOf(d Delivery) *inbox.Origin {
	o := &inbox.Origin{Channel: inbox.ChannelTelegram, User: d.User.ID, UserName: d.User.Name, Level: characters.UserLevel(d.User.Level),
		Conversation: fmt.Sprint(d.ChatID), Message: fmt.Sprint(d.MessageID), Table: d.Mesa}
	if d.ReplyToMessage != 0 {
		o.RepliedTo = fmt.Sprint(d.ReplyToMessage)
	}
	return o
}

// NewItem is the item of the inbox of the Grand Master for a routed message.
func NewItem(d Delivery, grandMaster string) inbox.NewItem {
	n := inbox.NewItem{Workspace: d.Workspace, Target: grandMaster, Kind: inbox.KindUser, Sender: d.User.Name, Text: d.Text, Origin: OriginOf(d),
		Refs: inbox.Refs{Key: fmt.Sprintf("telegram:%d:%d", d.ChatID, d.MessageID)}}
	if d.File != nil {
		// the caption (or the transcript) and the line `[anexo]`, in the words
		// of docs/telegram/anexos-e-transcricao.md#recebimento-de-anexos
		n.Text = strings.Join(d.File.Lines(), "\n")
		n.Attachments = []string{d.File.Path}
	}
	return n
}
