package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/attachments"
	"github.com/mestre-ia/mestre/internal/glossary"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Gate says whether this Citadel may call the Telegram: only the Citadel of the
// newest epoch, not suspended, does (docs/telegram/bot.md#recepção-por-long-polling).
// Every call goes through it: the polling, each send and each click.
type Gate interface {
	Allowed(ctx context.Context) bool
}

// Holder is the optional part of a Gate that holds the gate of the handover of the Citadel
// while an update is handled: a change the bot makes (a decision, an exclusion, a
// removal) must not land after the last lsn a planned switch reads. Hold returns false
// while the Citadel drains; the update then waits for the Citadel that may act.
type Holder interface {
	Hold(ctx context.Context) (release func(), ok bool)
}

// admit admits one update; a Gate without Holder admits everything.
func (b *Bot) admit(ctx context.Context) (func(), bool) {
	if h, ok := b.o.Gate.(Holder); ok {
		return h.Hold(ctx)
	}
	return func() {}, true
}

// Inbound is a message of an authorized user that goes to the Grão-Mestre.
type Inbound struct {
	User              users.User
	Kind              Kind
	Text              string
	ChatID, MessageID int64
	ReplyToMessage    int64
	ReplyWorkspace    string
	ReplyMesa         string
	HasReplyWorkspace bool
	// File is the file the message carries (a voice message, a photo, a document);
	// the routing receives it once the workspace is known.
	File *attachments.Ref
}

// Stranger is an update of someone who is not a user, as the Owner reads it in
// the alert (docs/telegram/seguranca-do-canal.md#filtro-de-entrada): the
// numeric id, the name and the @user the Telegram gave, and the start of the text.
type Stranger struct {
	ID             int64
	Name, Username string
	Text           string
	Kind           Kind
}

// Strangers is told of every message of a stranger; it groups the alerts (an
// alert per id per hour, with the count). Without it, Options.Alerts is told
// only the id.
type Strangers interface {
	Stranger(ctx context.Context, s Stranger)
}

// Router takes the text of the users to the inbox of the Grão-Mestre.
type Router interface {
	Deliver(ctx context.Context, in Inbound) error
}

// Call is what a quick command receives.
type Call struct {
	User              users.User
	Args              string
	ChatID, MessageID int64
}

// Locker says the Mestre is locked (/travar).
type Locker interface {
	Locked(ctx context.Context) bool
}

// Command is a quick command, answered by the engine at once.
type Command struct {
	Name        string
	Description string
	// Query says the command only reads (or unlocks): it is answered while the
	// Mestre is locked.
	Query bool
	// Capability is what the level needs ("" is any user).
	Capability users.Capability
	Run        func(ctx context.Context, c Call) (string, error)
}

// Click is what the action of a button receives.
type Click struct {
	User              users.User
	Arg               string
	ChatID, MessageID int64
}

// Action is what a button does. The bot checked the recipient, the level and
// the validity; the scope (the workspace the user still acts in) and
// idempotence are the action's: after an error the button can be clicked again.
// It gives the text for the toast and the text that
// replaces the message ("" keeps it).
type Action func(ctx context.Context, c Click) (toast, result string, err error)

// CaptureFunc takes the answer a user typed to a prompt of the bot (a PIN, a
// second factor). The text is never kept: not in the inbox, not in the
// history, not in a log. again keeps the prompt open for another try (a wrong
// PIN of a request that still has tries).
type CaptureFunc func(ctx context.Context, user users.User, ref, text string) (reply string, again bool, err error)

// Options are the pieces of the Bot.
type Options struct {
	DB     *store.DB
	Clock  clock.Clock
	Client *Client
	Users  *users.Service
	// Gate decides whether the Telegram may be called; nil is always.
	Gate   Gate
	Alerts users.Alerts
	Router Router
	// Strangers, when set, takes the messages of who is not a user.
	Strangers Strangers
	// Lock, when set, makes the bot answer only the queries and /destravar while
	// the Mestre is locked, and keep the messages to the Grand Master.
	Lock Locker

	Commands []Command
	Actions  map[string]Action
	Captures map[string]CaptureFunc
	// Terms are the glossary rows explained the first time.
	Terms []glossary.Term
	// Mask takes the secrets out of every message that leaves (the mask of the logs).
	Mask func(string) string
	// Machine is the id of this machine, written in the audit of the attempts to
	// send a secret.
	Machine string
	// OnConflict is told of a 409 of getUpdates (another reader of the same bot).
	OnConflict func(ctx context.Context)
	// PollTimeout is the long polling timeout (50 s by default).
	PollTimeout time.Duration
	Logger      *slog.Logger
}

// PINKind is the kind of capture of the PIN: Captures[PINKind] is built by
// PINCapture.
const PINKind = "pin"

// Bot is the bot of the Citadel.
type Bot struct {
	releasing sync.Mutex // ReleaseHeld
	o         Options
	st        *Store
	clk       clock.Clock
	log       *slog.Logger
	cmds      map[string]Command

	mu     sync.Mutex
	last   map[int64]time.Time // the last send to each chat
	window time.Time           // the second being counted
	count  int                 // sends in that second
	wakeCh chan struct{}
	then   map[string]func(context.Context) // one-shot, after a capture of the user ends
	// followUp is, per user, until when his next message is held: he just sent a
	// password phrase and the value may come in the next message.
	followUp map[string]time.Time
}

// New returns the Bot.
func New(o Options) (*Bot, error) {
	if o.DB == nil || o.Client == nil || o.Users == nil {
		return nil, errors.New("telegram: the bot needs the database, the client and the users")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Mask == nil {
		o.Mask = func(s string) string { return s }
	}
	if o.PollTimeout == 0 {
		o.PollTimeout = 50 * time.Second
	}
	b := &Bot{o: o, st: &Store{DB: o.DB, Clock: o.Clock}, clk: o.Clock, log: o.Logger, cmds: map[string]Command{}, last: map[int64]time.Time{}, wakeCh: make(chan struct{}, 1), then: map[string]func(context.Context){}, followUp: map[string]time.Time{}}
	for _, c := range o.Commands {
		b.cmds[c.Name] = c
	}
	return b, nil
}

// Store gives access to the tables of the bot.
func (b *Bot) Store() *Store { return b.st }

func (b *Bot) allowed(ctx context.Context) bool { return b.o.Gate == nil || b.o.Gate.Allowed(ctx) }

func (b *Bot) wake() {
	select {
	case b.wakeCh <- struct{}{}:
	default:
	}
}

// windowFull counts the sends of the second: about thirty in all.
func (b *Bot) windowFull(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !now.Truncate(time.Second).Equal(b.window) {
		return false
	}
	return b.count >= 30
}

func (b *Bot) counted(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := now.Truncate(time.Second); !s.Equal(b.window) {
		b.window, b.count = s, 0
	}
	b.count++
}

// ErrSuspended is a call while this Citadel may not act.
var ErrSuspended = errors.New("telegram: this Citadel is suspended")

// Start does what the bot does once when it starts: it takes the webhook off
// (getUpdates does not work with one) and registers the commands.
func (b *Bot) Start(ctx context.Context) error {
	if !b.allowed(ctx) {
		return ErrSuspended
	}
	if _, err := b.o.Client.Call(ctx, "deleteWebhook", map[string]any{}); err != nil {
		return err
	}
	names := make([]string, 0, len(b.cmds))
	for n := range b.cmds {
		names = append(names, n)
	}
	sort.Strings(names)
	list := []map[string]string{{"command": "start", "description": "Parear com um código ou ver a ajuda"}}
	for _, n := range names {
		list = append(list, map[string]string{"command": n, "description": b.cmds[n].Description})
	}
	_, err := b.o.Client.Call(ctx, "setMyCommands", map[string]any{"commands": list})
	return err
}

// Run starts the bot and runs the two loops until the context ends: the
// reading of the updates and the sending of the queue.
func (b *Bot) Run(ctx context.Context) error {
	for fails := 0; ; fails++ {
		if err := b.Start(ctx); err == nil {
			break
		} else if !b.sleep(ctx, jitter(backoff(fails))) {
			return ctx.Err()
		}
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.sendLoop(ctx)
	}()
	fails := 0
	for ctx.Err() == nil {
		if !b.allowed(ctx) {
			if !b.sleep(ctx, time.Second) {
				break
			}
			continue
		}
		if err := b.PollOnce(ctx); err != nil && ctx.Err() == nil {
			b.log.Warn("telegram: the polling failed", "err", err)
			fails++
			b.sleep(ctx, jitter(backoff(fails-1)))
			continue
		}
		fails = 0
	}
	wg.Wait()
	return ctx.Err()
}

func (b *Bot) sendLoop(ctx context.Context) {
	for ctx.Err() == nil {
		next, err := b.Flush(ctx)
		wait := time.Minute
		if err != nil {
			b.log.Warn("telegram: the queue was not flushed", "err", err)
		}
		if !next.IsZero() {
			wait = max(next.Sub(b.clk.Now()), time.Millisecond)
		}
		t := b.clk.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-b.wakeCh:
			t.Stop()
		case <-t.C():
		}
	}
}

func (b *Bot) sleep(ctx context.Context, d time.Duration) bool {
	t := b.clk.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C():
		return true
	}
}

func jitter(d time.Duration) time.Duration {
	return d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
}

// PollOnce makes one getUpdates, records the updates before the offset moves
// and processes them. Updates recorded and not processed (a stop in between)
// are processed first.
func (b *Bot) PollOnce(ctx context.Context) error {
	if !b.allowed(ctx) {
		return nil
	}
	if err := b.processPending(ctx); err != nil {
		return err
	}
	if err := b.st.Prune(ctx); err != nil {
		return err
	}
	offset, err := b.st.Offset(ctx)
	if err != nil {
		return err
	}
	params := map[string]any{"timeout": int(b.o.PollTimeout / time.Second), "allowed_updates": []string{"message", "callback_query"}}
	if offset > 0 {
		params["offset"] = offset
	}
	raw, err := b.o.Client.Call(ctx, "getUpdates", params)
	if err != nil {
		var api *APIError
		if errors.As(err, &api) && api.Conflict() && b.o.OnConflict != nil {
			b.o.OnConflict(ctx)
		}
		return err
	}
	var ups []json.RawMessage
	if err := json.Unmarshal(raw, &ups); err != nil {
		return err
	}
	// Each update is recorded before the offset moves; the ones that are new are
	// handled from what was read (the stored copy of a PIN has no text).
	for _, u := range ups {
		var up Update
		if err := json.Unmarshal(u, &up); err != nil {
			return err
		}
		fresh, err := b.st.Record(ctx, up.UpdateID, b.redacted(ctx, up, u))
		if err != nil {
			return err
		}
		if !fresh {
			continue
		}
		if !b.allowed(ctx) {
			return nil // recorded, it waits for the Citadel that may act
		}
		release, held := b.admit(ctx)
		if !held {
			return nil // recorded, it waits for the Citadel that may act
		}
		b.handle(ctx, up)
		err = b.st.Done(ctx, up.UpdateID) // marked while the gate is held: the new Citadel does not replay it
		release()
		if err != nil {
			return err
		}
	}
	return nil
}

// redacted is the update as it is kept: the text of a user the bot waits an
// answer from (a PIN) and the text that carries a secret are not kept. A secret
// leaves its kind in the copy, so an update never handled is still blocked.
func (b *Bot) redacted(ctx context.Context, up Update, raw []byte) []byte {
	m := up.Message
	if m == nil || m.From == nil || bodyOf(m) == "" {
		return raw
	}
	secret := secretNone
	u, err := b.o.Users.ByTelegram(ctx, m.From.ID)
	switch {
	case errors.Is(err, users.ErrNoUser):
		// the text of a stranger goes to the Owner: it is kept only if it is clean
		if secret, _ = b.screen(bodyOf(m)); secret == secretNone {
			return raw
		}
	case err != nil:
		return raw
	default:
		if c, found, cerr := b.st.Capturing(ctx, u.ID); cerr == nil && found {
			// the answer of a capture is kept without text; but free text that is
			// not a PIN is a secret of the filter too
			if c.Kind != PINKind {
				secret, _ = b.screen(bodyOf(m))
			}
		} else if secret, _ = b.screenMessage(u, m, false); secret == secretNone {
			// a message with a secret is not kept either: the queue of updates
			// would hold it until it is processed
			return raw
		}
	}
	cp := *m
	cp.Text, cp.Caption = "", ""
	up.Message = &cp // a copy: the update being handled keeps its text
	up.Redacted = true
	up.Secret = string(secret)
	out, err := json.Marshal(up)
	if err != nil {
		return []byte("{}")
	}
	return out
}

func (b *Bot) processPending(ctx context.Context) error {
	pending, err := b.st.Pending(ctx)
	if err != nil {
		return err
	}
	for _, p := range pending {
		if !b.allowed(ctx) {
			return nil // what is left waits for the Citadel that may act
		}
		var up Update
		release := func() {}
		if err := json.Unmarshal(p.Body, &up); err == nil {
			switch {
			case !up.Redacted:
				rel, held := b.admit(ctx)
				if !held {
					return nil // what is left waits for the Citadel that may act
				}
				release = rel
				b.handle(ctx, up)
			case up.Secret != "":
				// the text had a secret and was never handled: the message is
				// still in the chat, so it is deleted, audited and the author told
				b.blockedPending(ctx, up)
			}
			// A copy kept without its text and without a secret (a captured
			// answer) is not replayed: the user answers again.
		}
		err := b.st.Done(ctx, p.ID) // marked while the gate is held: the new Citadel does not replay it
		release()
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *Bot) handle(ctx context.Context, up Update) {
	defer func() {
		if r := recover(); r != nil {
			b.log.Error("telegram: handling an update panicked", "panic", fmt.Sprint(r))
		}
	}()
	switch {
	case up.CallbackQuery != nil:
		b.click(ctx, up.CallbackQuery)
	case up.Message != nil:
		b.message(ctx, up.Message)
	}
}

// say sends a text to a user, in his private chat.
func (b *Bot) say(ctx context.Context, chat int64, user, text string) {
	if _, err := b.Send(ctx, Out{ChatID: chat, UserID: user, Text: text, Origin: "motor"}); err != nil {
		b.log.Warn("telegram: a reply was not queued", "err", err)
	}
}

func (b *Bot) message(ctx context.Context, m *Message) {
	// The filter comes before anything else: the type of the chat, then who sent it.
	if m.Chat.Type != "private" {
		b.leave(ctx, m.Chat)
		return
	}
	if m.From == nil || m.From.IsBot {
		return
	}
	u, err := b.o.Users.ByTelegram(ctx, m.From.ID)
	switch {
	case errors.Is(err, users.ErrNoUser):
		b.stranger(ctx, m)
		return
	case err != nil:
		b.log.Warn("telegram: the sender was not looked up", "err", err)
		return
	case u.State != users.Active:
		return // suspended: no answer, no alert
	}
	c, found, cerr := b.st.Capturing(ctx, u.ID)
	armed := cerr == nil && found && c.Armed
	// The filter of secrets comes right after the user is known, before any
	// branch: a command, a capture of free text, a user without the right to talk
	// to the Grand Master. The answer of the PIN is the one thing that is no text.
	var kind secretKind
	var clean string
	if !armed || c.Kind != PINKind {
		kind, clean = b.screenMessage(u, m, true)
	}
	if kind != secretNone {
		b.blocked(ctx, u, m, kind)
		// only an ordinary message of a user who may talk goes on, as the notice or
		// without the piece; what else it would do (run a command, be a capture)
		// would need the text
		text := strings.TrimSpace(bodyOf(m))
		if armed || strings.HasPrefix(text, "/") || m.Kind() == KindOther || users.Require(u.Level, users.TalkToGrandMaster) != nil {
			return
		}
	}
	if armed {
		// The prompt reached the user: what he writes now is the answer (or
		// the end of the wait), never an ordinary message.
		b.capture(ctx, u, c, m)
		return
	}
	if m.Kind() == KindOther {
		b.say(ctx, m.Chat.ID, u.ID, "Não aceito esse tipo de mensagem. Posso receber texto, voz, áudio, foto e documento. Para mandar um vídeo, envie-o como arquivo.")
		return
	}
	text := strings.TrimSpace(m.Text)
	locked := b.o.Lock != nil && b.o.Lock.Locked(ctx)
	if strings.HasPrefix(text, "/") {
		if locked && !b.answeredWhenLocked(text) {
			b.say(ctx, m.Chat.ID, u.ID, "O Mestre está travado: só respondo às consultas e ao /destravar.")
			return
		}
		b.command(ctx, u, m, text)
		return
	}
	if err := users.Require(u.Level, users.TalkToGrandMaster); err != nil {
		b.say(ctx, m.Chat.ID, u.ID, "Com o seu nível você não fala com o Grão-Mestre. "+b.allowedCommands(u))
		return
	}
	in := Inbound{User: u, Kind: m.Kind(), Text: text, ChatID: m.Chat.ID, MessageID: m.MessageID, File: m.FileOf()}
	if in.Text == "" {
		in.Text = strings.TrimSpace(m.Caption)
	}
	// What the Grand Master gets of a secret: the notice, or the text without
	// the piece (the message was already deleted above).
	switch {
	case kind.Held():
		in.Text, in.File, in.Kind = HeldNotice, nil, KindText
	case kind == secretFormat:
		in.Text, in.File = strings.TrimSpace(clean), nil
	}
	if m.ReplyTo != nil {
		in.ReplyToMessage = m.ReplyTo.MessageID
		if ws, mesa, ok, err := b.st.MessageOrigin(ctx, m.Chat.ID, m.ReplyTo.MessageID); err == nil && ok {
			in.ReplyWorkspace, in.ReplyMesa, in.HasReplyWorkspace = ws, mesa, true
		}
	}
	if locked {
		// kept, and delivered in order when the Mestre is unlocked
		if err := b.st.Hold(ctx, in); err != nil {
			b.log.Warn("telegram: a message was not kept while the Mestre is locked", "err", err)
			b.say(ctx, m.Chat.ID, u.ID, "O Mestre está travado e não consegui guardar a mensagem. Mande de novo depois.")
			return
		}
		b.say(ctx, m.Chat.ID, u.ID, "O Mestre está travado: a sua mensagem fica guardada e vai ao Grão-Mestre quando o Senhor da Guilda destravar.")
		return
	}
	if b.o.Router == nil {
		return
	}
	if err := b.o.Router.Deliver(ctx, in); err != nil {
		b.log.Warn("telegram: the message was not delivered", "err", err)
		b.say(ctx, m.Chat.ID, u.ID, "Não consegui entregar a mensagem ao Grão-Mestre. Tente de novo em instantes.")
	}
}

// answeredWhenLocked says whether the command is one the locked Mestre answers.
func (b *Bot) answeredWhenLocked(text string) bool {
	head, _, _ := strings.Cut(text, " ")
	name, _, _ := strings.Cut(strings.TrimPrefix(head, "/"), "@")
	if name == "start" {
		return true
	}
	cmd, ok := b.cmds[name]
	return ok && cmd.Query
}

// ReleaseHeld delivers, in order, the messages kept while the Mestre was locked
// (the unlock calls it). It stops at the first that fails and keeps the rest.
func (b *Bot) ReleaseHeld(ctx context.Context) error {
	if b.o.Router == nil {
		return nil
	}
	b.releasing.Lock() // two unlocks in a row deliver each message once
	defer b.releasing.Unlock()
	held, err := b.st.Held(ctx)
	if err != nil {
		return err
	}
	for _, h := range held {
		u, err := b.o.Users.Get(ctx, h.UserID)
		if err != nil || u.State != users.Active || users.Require(u.Level, users.TalkToGrandMaster) != nil {
			_ = b.st.Unhold(ctx, h.ID) // the user is gone, or lost the right to talk
			continue
		}
		in := h.Inbound
		in.User = u
		if err := b.o.Router.Deliver(ctx, in); err != nil {
			return err
		}
		if err := b.st.Unhold(ctx, h.ID); err != nil {
			return err
		}
	}
	return nil
}

// stranger is a sender who is not a user: only a pairing code is looked at.
func (b *Bot) stranger(ctx context.Context, m *Message) {
	text := strings.TrimSpace(m.Text)
	if !users.LooksLikeCode(text) {
		if b.o.Strangers != nil {
			if text == "" {
				text = strings.TrimSpace(m.Caption)
			}
			// the Owner reads what a stranger wrote: never a secret in it
			switch kind, clean := b.screen(text); {
			case kind.Held():
				b.blockedStranger(ctx, m, kind)
				text = "[mensagem com senha omitida pelo motor]"
			case kind == secretFormat:
				b.blockedStranger(ctx, m, kind)
				text = strings.TrimSpace(clean)
			}
			b.o.Strangers.Stranger(ctx, Stranger{ID: m.From.ID, Name: strings.TrimSpace(m.From.FirstName), Username: m.From.Username, Text: text, Kind: m.Kind()})
			return
		}
		if b.o.Alerts != nil {
			b.o.Alerts.Unauthorized(ctx, m.From.ID, "remetente desconhecido")
		}
		return
	}
	u, err := b.o.Users.Pair(ctx, users.PairRequest{Text: text, TelegramID: m.From.ID, Private: m.Chat.Type == "private"})
	switch {
	case err == nil:
		b.say(ctx, m.Chat.ID, u.ID, fmt.Sprintf("Pareado, %s. O seu nível é %s.", u.Name, u.Level.Name()))
	case errors.Is(err, users.ErrSilent):
	default:
		b.reply(ctx, m.Chat.ID, "Código inválido ou expirado.")
	}
}

// reply sends a text to a chat of someone who is not a user (no explanation, no buttons).
func (b *Bot) reply(ctx context.Context, chat int64, text string) {
	if _, err := b.Send(ctx, Out{ChatID: chat, Text: text, Origin: "motor"}); err != nil {
		b.log.Warn("telegram: a reply was not queued", "err", err)
	}
}

// leave leaves a group, a supergroup or a channel the bot was added to.
func (b *Bot) leave(ctx context.Context, c Chat) {
	if _, err := b.o.Client.Call(ctx, "leaveChat", map[string]any{"chat_id": c.ID}); err != nil {
		b.log.Warn("telegram: the bot did not leave the chat", "err", err)
	}
	if b.o.Alerts != nil {
		b.o.Alerts.OwnerAlert(ctx, "O bot foi adicionado a um chat que não é privado e saiu dele.")
	}
}

func (b *Bot) command(ctx context.Context, u users.User, m *Message, text string) {
	head, args, _ := strings.Cut(text, " ")
	name, _, _ := strings.Cut(strings.TrimPrefix(head, "/"), "@")
	args = strings.TrimSpace(args)
	if name == "start" {
		if args == "" {
			b.say(ctx, m.Chat.ID, u.ID, "Você já está pareado. "+b.allowedCommands(u))
		} else {
			b.say(ctx, m.Chat.ID, u.ID, "Você já está pareado.")
		}
		return
	}
	cmd, ok := b.cmds[name]
	if !ok {
		b.say(ctx, m.Chat.ID, u.ID, "Comando desconhecido. "+b.allowedCommands(u))
		return
	}
	if cmd.Capability != "" {
		if err := users.Require(u.Level, cmd.Capability); err != nil {
			b.say(ctx, m.Chat.ID, u.ID, "/"+name+" "+err.Error())
			return
		}
	}
	out, err := cmd.Run(ctx, Call{User: u, Args: args, ChatID: m.Chat.ID, MessageID: m.MessageID})
	if err != nil {
		b.log.Warn("telegram: a command failed", "command", name)
		out = "Não consegui executar /" + name + ": " + b.o.Mask(err.Error())
	}
	if out != "" {
		b.say(ctx, m.Chat.ID, u.ID, out)
	}
}

// allowedCommands lists the commands the level of the user may use.
func (b *Bot) allowedCommands(u users.User) string {
	var names []string
	for n, c := range b.cmds {
		if c.Capability == "" || users.Can(u.Level, c.Capability) {
			names = append(names, "/"+n)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "Não há comandos para o seu nível."
	}
	return "Comandos permitidos: " + strings.Join(names, ", ") + "."
}

// Ask sends a prompt that opens the reply box and makes the bot wait for the next
// text of the user: it is taken by Captures[kind] and goes nowhere else.
func (b *Bot) Ask(ctx context.Context, userID, kind, ref, prompt string, ttl time.Duration) error {
	u, err := b.o.Users.Get(ctx, userID)
	if err != nil {
		return err
	}
	if u.State != users.Active {
		return fmt.Errorf("%w: %s", users.ErrNoUser, userID)
	}
	if c, found, err := b.st.Capturing(ctx, u.ID); err != nil {
		return err
	} else if found && !c.Expired {
		return ErrCaptureBusy
	}
	if u.TelegramID == 0 {
		return errors.New("telegram: the user has no Telegram")
	}
	if _, ok := b.o.Captures[kind]; !ok {
		return fmt.Errorf("telegram: no capture of kind %q", kind)
	}
	id, err := b.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: prompt, ForceReply: true, Origin: "motor"})
	if err != nil {
		return err
	}
	return b.st.SetCapture(ctx, Capture{UserID: u.ID, Kind: kind, Ref: ref, OutboxID: id, Expires: b.clk.Now().Add(ttl)})
}

// capture takes the answer: the message of the user and the prompt are deleted
// and the text goes only to the function of the kind. A prompt that expired, or
// an answer that is not text, ends here too: nothing of it goes to an agent.
func (b *Bot) capture(ctx context.Context, u users.User, c Capture, m *Message) {
	b.takeThen(u.ID) // a continuation left by an earlier answer is not this one's
	b.deleteMessage(ctx, m.Chat.ID, m.MessageID)
	switch {
	case c.Expired:
		b.endCapture(ctx, u, c, m.Chat.ID)
		b.say(ctx, m.Chat.ID, u.ID, "O pedido expirou. Peça de novo a ação que o fez.")
		return
	case m.Kind() != KindText:
		b.say(ctx, m.Chat.ID, u.ID, "Responda ao pedido com texto.")
		return
	}
	fn := b.o.Captures[c.Kind]
	if fn == nil {
		b.endCapture(ctx, u, c, m.Chat.ID)
		return
	}
	reply, again, err := fn(ctx, u, c.Ref, m.Text)
	if err != nil {
		b.log.Warn("telegram: a capture failed", "kind", c.Kind)
		reply, again = "Não consegui usar a resposta.", false
	}
	if !again {
		b.endCapture(ctx, u, c, m.Chat.ID)
	}
	if reply != "" {
		b.say(ctx, m.Chat.ID, u.ID, reply)
	}
	if next := b.takeThen(u.ID); next != nil && !again {
		next(ctx)
	}
}

// Then leaves a continuation for after the capture the user is answering ends:
// the answer needs another prompt (the PIN after a time), and a prompt can only
// be asked when the first is over.
func (b *Bot) Then(userID string, f func(context.Context)) {
	b.mu.Lock()
	b.then[userID] = f
	b.mu.Unlock()
}

func (b *Bot) takeThen(userID string) func(context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	f := b.then[userID]
	delete(b.then, userID)
	return f
}

// endCapture ends the wait and takes the prompt off the chat.
func (b *Bot) endCapture(ctx context.Context, u users.User, c Capture, chat int64) {
	if _, err := b.st.ClearCapture(ctx, u.ID, c.Ref); err != nil {
		b.log.Warn("telegram: a capture was not cleared", "err", err)
	}
	if prompt := b.promptMessage(ctx, c.OutboxID); prompt != 0 {
		b.deleteMessage(ctx, chat, prompt)
	}
}

func (b *Bot) promptMessage(ctx context.Context, outboxID int64) int64 {
	var id int64
	_ = b.o.DB.Reader().QueryRowContext(ctx, `SELECT message_id FROM telegram_outbox WHERE id = ?`, outboxID).Scan(&id)
	return id
}

func (b *Bot) deleteMessage(ctx context.Context, chat, message int64) {
	if _, err := b.o.Client.Call(ctx, "deleteMessage", map[string]any{"chat_id": chat, "message_id": message}); err != nil {
		b.log.Warn("telegram: a message was not deleted", "err", err)
	}
}

// PINCapture is the capture of kind PINKind: the ref is the id of a request of
// PIN of the users, the text is checked and never kept; done is told whether the
// PIN was right (the action that asked for it is applied there, once).
func PINCapture(svc *users.Service, done func(ctx context.Context, user users.User, requestID string, err error)) CaptureFunc {
	return func(ctx context.Context, user users.User, ref, text string) (string, bool, error) {
		err := svc.CheckPIN(ctx, ref, strings.TrimSpace(text))
		if done != nil {
			done(ctx, user, ref, err)
		}
		switch {
		case err == nil:
			return "PIN confirmado.", false, nil
		case errors.Is(err, users.ErrPINLocked):
			return "O PIN está bloqueado por um tempo.", false, nil
		case err == users.ErrBadPIN:
			return "PIN incorreto. Tente de novo.", true, nil // the request still has tries
		default:
			return "PIN incorreto ou pedido encerrado.", false, nil
		}
	}
}

// click runs a button: only the user it was given to, at the level it needs, once.
func (b *Bot) click(ctx context.Context, cq *CallbackQuery) {
	answer := func(text string) {
		p := map[string]any{"callback_query_id": cq.ID}
		if text != "" {
			p["text"] = text
		}
		if _, err := b.o.Client.Call(ctx, "answerCallbackQuery", p); err != nil {
			b.log.Warn("telegram: a click was not answered", "err", err)
		}
	}
	u, err := b.o.Users.ByTelegram(ctx, cq.From.ID)
	if err != nil || u.State != users.Active {
		if b.o.Alerts != nil && errors.Is(err, users.ErrNoUser) {
			b.o.Alerts.Unauthorized(ctx, cq.From.ID, "clique de remetente desconhecido")
		}
		answer("")
		return
	}
	cb, ok, err := b.st.Callback(ctx, cq.Data)
	if err != nil || !ok {
		answer("Esse botão expirou.")
		return
	}
	switch {
	case cb.UserID != u.ID:
		answer("Esse botão não é seu.")
		return
	case !b.clk.Now().Before(cb.Expires):
		answer("Esse botão expirou.")
		return
	case cb.State == "void":
		answer("Esse botão foi substituído por um novo.")
		return
	case cb.State != "open":
		name := cb.DecidedBy
		if d, err := b.o.Users.Get(ctx, cb.DecidedBy); err == nil {
			name = d.Name
		}
		answer("Já respondido por " + name + ".")
		return
	}
	if cb.Capability != "" {
		if err := users.Require(u.Level, users.Capability(cb.Capability)); err != nil {
			answer("Seu nível não permite: " + b.o.Mask(err.Error()))
			return
		}
	}
	act := b.o.Actions[cb.Action]
	if act == nil {
		answer("Ação desconhecida.")
		return
	}
	if took, err := b.st.Take(ctx, cb.Key, u.ID); err != nil || !took {
		answer("Já respondido.")
		return
	}
	var chat, msg int64
	if cq.Message != nil {
		chat, msg = cq.Message.Chat.ID, cq.Message.MessageID
	}
	toast, result, err := act(ctx, Click{User: u, Arg: cb.Arg, ChatID: chat, MessageID: msg})
	if err != nil {
		_ = b.st.Release(ctx, cb.Key)
		answer(b.o.Mask(err.Error()))
		return
	}
	answer(b.o.Mask(toast))
	if result != "" && msg != 0 {
		result = b.maskText(result)
		// the message keeps its header: where it came from is still true
		ws, mesa, _, _ := b.st.MessageOrigin(ctx, chat, msg)
		h, plain, ok := render(headOf(u.ID, ws, mesa), result)
		p := map[string]any{"chat_id": chat, "message_id": msg}
		if ok {
			p["text"], p["parse_mode"] = b.o.Mask(h), "HTML"
		} else {
			p["text"] = plain
		}
		if _, err := b.o.Client.Call(ctx, "editMessageText", p); err != nil {
			b.log.Warn("telegram: the message was not edited", "err", err)
		}
	}
}
