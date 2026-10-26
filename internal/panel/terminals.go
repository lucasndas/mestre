package panel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The terminal of a character in the browser (docs/painel/canvas.md): the
// panel follows the terminal through the machine of the character, relays what
// the machine sends and types what a person types, and keeps no buffer.

// Errors of a TerminalSource.
var (
	// ErrNoTerminal: the character has no terminal (or there is no such one).
	ErrNoTerminal = errors.New("panel: no such terminal")
	// ErrNoSignal: the machine of the terminal cannot be reached now.
	ErrNoSignal = errors.New("panel: the machine of the terminal has no signal")
	// ErrPermissionPrompt: the agent waits on a permission prompt, which keys do not answer.
	ErrPermissionPrompt = errors.New("panel: the agent waits on a permission prompt")
)

// Terminal size the panel draws when the source does not say: the default of
// the machines (docs/arquitetura/terminal.md#criação).
const (
	defaultCols = 200
	defaultRows = 50
)

// maxTerminalInput is the most a person types in one message.
const maxTerminalInput = 64 << 10

// TerminalSource is the Citadel, as the panel sees the terminals of the Guild.
type TerminalSource interface {
	// ByCharacter finds the terminal of a character (ErrNoTerminal).
	ByCharacter(ctx context.Context, character string) (TerminalInfo, error)
	// Info is the terminal with the id (ErrNoTerminal).
	Info(ctx context.Context, terminalID string) (TerminalInfo, error)
	// Follow asks the machine of the terminal for its output from offset from on
	// (terminal.replay) and then relays the live output to the sink, in order,
	// until stop is called. A machine without signal does not fail it: the sink
	// is told, and the stream goes on when the machine is back. The sink's
	// methods must not block, and Output's data is not kept after it returns.
	Follow(ctx context.Context, terminalID string, from uint64, sink TerminalSink) (stop func(), err error)
	// Screen is the picture of the screen at the byte offset of the output it shows.
	Screen(ctx context.Context, terminalID string) (text string, offset uint64, err error)
	// Type types what a person typed into the PTY, through the engine of the
	// machine (ErrNoSignal, ErrNoTerminal).
	Type(ctx context.Context, terminalID string, data []byte) error
}

// TerminalFinder is a TerminalSource that can also find the terminals by the
// name of the character (the list of GET /api/terminals).
type TerminalFinder interface {
	// FindByName lists the terminals of the living characters with the name
	// (without difference of case), in every workspace.
	FindByName(ctx context.Context, name string) ([]TerminalInfo, error)
}

// TerminalSink receives a followed terminal.
type TerminalSink interface {
	// Output is data at byte offset off of the output, in order. An offset after
	// the end of the last data is a stretch the buffer of the machine lost.
	Output(off uint64, data []byte)
	// Replayed says the replay caught up with the live output.
	Replayed(from, to uint64, lost bool)
	// Signal says the machine lost (up false) or got back its signal.
	Signal(up bool, since time.Time)
}

// terminalHolder lets the source be set after the panel is made.
type terminalHolder struct{ src TerminalSource }

// SetTerminals gives the panel the terminals of the Guild; until then the topics
// of the terminals are refused.
func (p *Panel) SetTerminals(src TerminalSource) { p.terminals.Store(&terminalHolder{src}) }

func (p *Panel) terminalSource() TerminalSource {
	if h := p.terminals.Load(); h != nil {
		return h.src
	}
	return nil
}

func init() { RegisterArea("terminals", mountTerminals) }

type terminalArea struct {
	p       *Panel
	mu      sync.Mutex
	follows map[string]int // terminals followed now, by user
}

func mountTerminals(r *Router) {
	p := r.Panel()
	a := &terminalArea{p: p, follows: map[string]int{}}
	p.hub.Provide("terminal", Topic{Authorize: a.authorize, Snapshot: a.snapshot, Open: a.open})
	p.hub.HandleMessage(TypeTerminalInput, a.input)
	p.hub.HandleMessage(TypeTerminalTypingEnd, a.typingEnd)
	r.Require("GET /api/characters/{id}/terminal", a.handleInfo, users.Status)
	r.Require("GET /api/terminals", a.handleFind, users.Status)
}

// maxTerminalName bounds the name searched for (a name of a character is short).
const maxTerminalName = 200

// handleFind lists the terminals of the visible characters with the name; the
// workspace, when given, narrows it. A terminal of a workspace the user does
// not see is not there, as in handleInfo.
func (a *terminalArea) handleFind(w http.ResponseWriter, r *http.Request) {
	finder, _ := a.p.terminalSource().(TerminalFinder)
	if finder == nil {
		WriteError(w, Err(CodeUnavailable, "os terminais não estão disponíveis agora", nil))
		return
	}
	name, ws := r.URL.Query().Get("name"), r.URL.Query().Get("workspace")
	if name == "" || len(name) > maxTerminalName || len(ws) > maxTerminalName {
		WriteError(w, errBadRequest())
		return
	}
	infos, err := finder.FindByName(r.Context(), name)
	if err != nil {
		a.p.log.Warn("panel: finding terminals by name", "err", err)
		WriteError(w, errInternal())
		return
	}
	u := UserOf(r)
	out := TerminalList{Terminals: []TerminalInfo{}}
	for _, info := range infos {
		if visible(u, info.Workspace) && (ws == "" || info.Workspace == ws) {
			out.Terminals = append(out.Terminals, a.fill(info, u))
		}
	}
	WriteJSON(w, http.StatusOK, out)
}

// visible says whether the user sees the terminals of the workspace.
func visible(u users.User, workspace string) bool {
	return users.Can(u.Level, users.Status) && (workspace == "" || u.Acts(workspace))
}

// frameIDOf is the id the binary frames of a terminal carry to the browser: 16
// bytes of the SHA-256 of the terminal id, so the same terminal always has the
// same one and the ids of the terminals need not be ULIDs.
func frameIDOf(terminalID string) protocol.ULID {
	h := sha256.Sum256([]byte(terminalID))
	return protocol.ULID(h[:16])
}

func (a *terminalArea) fill(info TerminalInfo, u users.User) TerminalInfo {
	info.FrameID = frameIDOf(info.TerminalID).String()
	if info.Cols == 0 || info.Rows == 0 {
		info.Cols, info.Rows = defaultCols, defaultRows
	}
	info.CanType = users.Can(u.Level, users.TypeInTerminal) && u.Acts(info.Workspace)
	return info
}

func (a *terminalArea) handleInfo(w http.ResponseWriter, r *http.Request) {
	src := a.p.terminalSource()
	if src == nil {
		WriteError(w, Err(CodeUnavailable, "os terminais não estão disponíveis agora", nil))
		return
	}
	u := UserOf(r)
	info, err := src.ByCharacter(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, ErrNoTerminal) || (err == nil && !visible(u, info.Workspace)):
		WriteError(w, Err(CodeNotFound, "terminal não encontrado", nil))
		return
	case err != nil:
		a.p.log.Warn("panel: reading a terminal", "err", err)
		WriteError(w, errInternal())
		return
	}
	WriteJSON(w, http.StatusOK, a.fill(info, u))
}

func (a *terminalArea) authorize(u users.User, id string) bool {
	src := a.p.terminalSource()
	if src == nil {
		return false
	}
	ctx, cancel := clock.WithTimeout(context.Background(), clock.Real{}, 5*time.Second)
	defer cancel()
	info, err := src.Info(ctx, id)
	return err == nil && visible(u, info.Workspace)
}

func (a *terminalArea) snapshot(ctx context.Context, u users.User, id string) (any, error) {
	info, err := a.p.terminalSource().Info(ctx, id)
	if err != nil {
		return nil, err
	}
	return a.fill(info, u), nil
}

// termSub is one browser following one terminal.
type termSub struct {
	a       *terminalArea
	sub     *Subscription
	user    users.User
	id      string
	info    TerminalInfo
	frameID protocol.ULID
	ctx     context.Context
	cancel  context.CancelFunc

	gen     atomic.Int64 // the follow that may still speak
	lagging atomic.Bool

	mu     sync.Mutex
	stop   func()
	closed bool
	typing *typingSession
}

type typingSession struct {
	started time.Time
	inputs  int
}

func (a *terminalArea) open(ctx context.Context, sub *Subscription, u users.User, id string) func() {
	src := a.p.terminalSource()
	if src == nil {
		sub.Event(sub.Topic, EventSignal, TerminalSignal{Signal: false})
		return nil
	}
	info, err := src.Info(ctx, id)
	if err != nil {
		// no terminal, no stream, and nothing can be typed into it (input needs the open terminal)
		sub.Event(sub.Topic, EventSignal, TerminalSignal{Signal: false})
		return nil
	}
	if !a.reserve(u.ID) {
		sub.Reply(TypeUIError, "", Err(CodeTooManyStreams, "terminais demais abertos nesta conta", map[string]any{"max": maxStreamsPerUser}))
		return nil
	}
	cctx, cancel := context.WithCancel(context.Background())
	ts := &termSub{a: a, sub: sub, user: u, id: id, info: info, frameID: frameIDOf(id), ctx: cctx, cancel: cancel}
	sub.State = ts
	ts.follow(0)
	return func() {
		ts.end()
		a.release(u.ID)
	}
}

// maxStreamsPerUser bounds the terminals one user follows at once: each opens a
// replay of up to 1 MiB on a machine.
const maxStreamsPerUser = 16

func (a *terminalArea) reserve(user string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.follows[user] >= maxStreamsPerUser {
		return false
	}
	a.follows[user]++
	return true
}

func (a *terminalArea) release(user string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.follows[user] > 0 {
		a.follows[user]--
	}
}

// follow starts (or restarts) the stream from offset from.
func (ts *termSub) follow(from uint64) {
	g := ts.gen.Add(1)
	stop, err := ts.a.p.terminalSource().Follow(ts.ctx, ts.id, from, &termSink{ts: ts, gen: g})
	if err != nil {
		ts.a.p.log.Warn("panel: following a terminal", "terminal", ts.id, "err", err)
		ts.sub.Event(ts.sub.Topic, EventSignal, TerminalSignal{Signal: false})
		return
	}
	ts.mu.Lock()
	if ts.closed || ts.gen.Load() != g {
		ts.mu.Unlock()
		stop()
		return
	}
	ts.stop = stop
	ts.mu.Unlock()
}

// stopFollow ends the current stream and silences its sink.
func (ts *termSub) stopFollow() {
	ts.gen.Add(1)
	ts.mu.Lock()
	stop := ts.stop
	ts.stop = nil
	ts.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// end is the end of the subscription: the stream stops and an open typing
// session is closed in the audit.
func (ts *termSub) end() {
	ts.mu.Lock()
	ts.closed = true
	ts.mu.Unlock()
	ts.stopFollow()
	ts.cancel()
	ts.endTyping()
}

// resync gives a browser that fell behind the picture of the screen instead of
// the output it lost, and goes on from the byte the picture shows.
func (ts *termSub) resync() {
	ts.stopFollow()
	if !ts.stillVisible() {
		return
	}
	text, off, err := ts.a.p.terminalSource().Screen(ts.ctx, ts.id)
	if err != nil {
		text, off = "", 0 // no picture: the whole buffer comes again
	}
	ts.sub.Event(ts.sub.Topic, EventScreen, TerminalScreenEvent{Text: text, Offset: off})
	ts.lagging.Store(false)
	ts.follow(off)
}

// stillVisible reads the user and the terminal again: a user who lost the
// workspace (or the account) stops getting its output, and the socket closes.
func (ts *termSub) stillVisible() bool {
	u, err := ts.a.p.users.Get(ts.ctx, ts.user.ID)
	if err == nil && u.State == users.Active {
		if info, err := ts.a.p.terminalSource().Info(ts.ctx, ts.id); err == nil && visible(u, info.Workspace) {
			return true
		}
	}
	ts.sub.c.close(CloseSessionEnded, "terminal not visible")
	return false
}

type termSink struct {
	ts  *termSub
	gen int64
}

func (k *termSink) live() bool { return k.ts.gen.Load() == k.gen }

func (k *termSink) Output(off uint64, data []byte) {
	if !k.live() || k.ts.lagging.Load() {
		return
	}
	b, err := protocol.EncodeData(protocol.DataFrame{Type: protocol.DataTerminal, ID: k.ts.frameID, Pos: off, Data: data})
	if err != nil {
		return
	}
	if !k.ts.sub.Frame(b) && k.ts.lagging.CompareAndSwap(false, true) {
		go k.ts.resync()
	}
}

func (k *termSink) Replayed(from, to uint64, lost bool) {
	if k.live() {
		k.ts.sub.Event(k.ts.sub.Topic, EventReplayed, TerminalReplayed{From: from, To: to, Lost: lost})
	}
}

func (k *termSink) Signal(up bool, since time.Time) {
	if !k.live() {
		return
	}
	s := TerminalSignal{Signal: up}
	if !up {
		s.Since = &since
	}
	k.ts.sub.Event(k.ts.sub.Topic, EventSignal, s)
}

func (ts *termSub) detail(extra map[string]any) map[string]any {
	d := map[string]any{"user": ts.user.ID, "character": ts.info.Character, "terminal": ts.id, "machine": ts.info.Machine, "workspace": ts.info.Workspace}
	for k, v := range extra {
		d[k] = v
	}
	return d
}

// beginTyping opens the typing session of the browser if it has none: the first
// input accepted is its start.
func (ts *termSub) beginTyping(ctx context.Context) {
	ts.mu.Lock()
	if ts.typing != nil {
		ts.typing.inputs++
		ts.mu.Unlock()
		return
	}
	now := ts.a.p.clk.Now().UTC()
	ts.typing = &typingSession{started: now, inputs: 1}
	ts.mu.Unlock()
	ts.a.p.audit(ctx, "panel.terminal_typing_started", ts.detail(map[string]any{"started_at": now}))
}

// endTyping closes the typing session: who, which character, when it began and
// when it ended; what was typed is never in the audit (it is in the log of the
// terminal).
func (ts *termSub) endTyping() {
	ts.mu.Lock()
	t := ts.typing
	ts.typing = nil
	ts.mu.Unlock()
	if t == nil {
		return
	}
	ts.a.p.audit(context.Background(), "panel.terminal_typing_ended", ts.detail(map[string]any{
		"started_at": t.started, "ended_at": ts.a.p.clk.Now().UTC(), "inputs": t.inputs}))
}

func (a *terminalArea) termSubOf(s Socket, terminalID string) *termSub {
	sub := s.Subscription("terminal:" + terminalID)
	if sub == nil {
		return nil
	}
	ts, _ := sub.State.(*termSub)
	return ts
}

// input is a message terminal.input. The level is read from the database and
// checked at each one, before anything else: a refused message types nothing.
func (a *terminalArea) input(ctx context.Context, s Socket, env UIEnvelope) {
	var in TerminalInputMessage
	if err := json.Unmarshal(env.Payload, &in); err != nil || in.TerminalID == "" || in.Data == "" || len(in.Data) > maxTerminalInput {
		s.Fail(env.ID, errBadRequest())
		return
	}
	u, ok := s.Fresh(ctx)
	if !ok {
		return
	}
	ts := a.termSubOf(s, in.TerminalID)
	if err := users.Require(u.Level, users.TypeInTerminal); err != nil {
		params := map[string]any{"capability": string(users.TypeInTerminal)}
		var le *users.LevelError
		if errors.As(err, &le) {
			params["need"], params["have"] = string(le.Need), string(le.Have)
		}
		if ts != nil {
			ts.endTyping()
		}
		a.p.audit(ctx, "panel.terminal_input_refused", map[string]any{"user": u.ID, "terminal": in.TerminalID, "have": string(u.Level)})
		s.Fail(env.ID, Err(CodeForbiddenLevel, "ação não permitida: "+err.Error(), params))
		return
	}
	src := a.p.terminalSource()
	switch {
	case ts == nil:
		s.Fail(env.ID, Err(CodeBadRequest, "abra o terminal antes de digitar", nil))
		return
	case src == nil:
		s.Fail(env.ID, Err(CodeUnavailable, "os terminais não estão disponíveis agora", nil))
		return
	case !u.Acts(ts.info.Workspace) && ts.info.Workspace != "":
		ts.endTyping()
		s.Fail(env.ID, Err(CodeForbiddenLevel, "ação não permitida neste workspace", map[string]any{"workspace": ts.info.Workspace}))
		return
	}
	ts.beginTyping(ctx)
	switch err := src.Type(ctx, in.TerminalID, []byte(in.Data)); {
	case err == nil:
	case errors.Is(err, ErrNoSignal):
		s.Fail(env.ID, Err(CodeUnavailable, "a máquina do terminal está sem sinal", map[string]any{"reason": "no_signal"}))
	case errors.Is(err, ErrNoTerminal):
		s.Fail(env.ID, Err(CodeNotFound, "terminal não encontrado", nil))
	case errors.Is(err, ErrPermissionPrompt):
		s.Fail(env.ID, Err(CodePermissionPrompt, "o agente espera uma decisão de permissão; responda pela aprovação", nil))
	default:
		a.p.log.Warn("panel: typing into a terminal", "terminal", in.TerminalID, "err", err)
		s.Fail(env.ID, errInternal())
	}
}

// typingEnd is a message terminal.typing_end.
func (a *terminalArea) typingEnd(_ context.Context, s Socket, env UIEnvelope) {
	var in TerminalTypingEndMessage
	if err := json.Unmarshal(env.Payload, &in); err != nil || in.TerminalID == "" {
		s.Fail(env.ID, errBadRequest())
		return
	}
	if ts := a.termSubOf(s, in.TerminalID); ts != nil {
		ts.endTyping()
	}
}
