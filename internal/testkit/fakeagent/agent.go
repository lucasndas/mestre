package fakeagent

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/pkg/clock"
)

// CommandRunner runs the commands of `run` steps, the way an agent's shell
// tool would (in the fast suite, a function call into the CLI code).
type CommandRunner interface {
	RunCommand(ctx context.Context, command, dir string, env []string) (output string, exitCode int, err error)
}

// Config starts one fake agent.
type Config struct {
	Provider Provider
	Args     []string // command-line arguments after the program name
	Env      []string // complete environment, KEY=VALUE
	Dir      string   // working folder
	In       io.Reader
	Out      io.Writer
	Clock    clock.Clock
	Hooks    HookRunner
	Commands CommandRunner
	// Recordings defaults to the testkit recordings; RecordingVersion ""
	// means the newest version. The --fake-recording-version flag and
	// FAKEAGENT_RECORDING_VERSION pin it for the binary of the full suite.
	Recordings       fs.FS
	RecordingVersion string
	// SessionID overrides the generated session id.
	SessionID string
}

// Errors that end the agent.
var (
	ErrDeclinedTrust = errors.New("fakeagent: folder not trusted")
	ErrLoginRequired = errors.New("fakeagent: login is not simulated; give the profile a fake credential")
	ErrCrash         = errors.New("fakeagent: crash")
)

type agent struct {
	cfg  Config
	p    Provider
	clk  clock.Clock
	recs fs.FS
	env  map[string]string
	dir  string

	settingsArg string
	script      *Script
	sessionID   string
	hooks       []handler
	trusted     bool

	outMu   sync.Mutex
	mu      sync.Mutex
	hookLog []string
	turn    int
	toolSeq int
	lastMsg string

	// Claude Code (claude.go).
	layerFile  string     // --append-system-prompt-file
	addDirs    []string   // --add-dir
	statusLine string     // the statusLine command of the settings
	ask        bool       // the next tool asks for permission
	toolFailed bool       // the tool that ran failed
	turnFailed bool       // the turn ended in a failure (the usage limit)
	keys       chan event // what is typed during a turn, for its menus

	// Codex (codex.go).
	transcript string  // the session transcript the fake writes
	queued     []event // typed during a turn and read by no menu: the prompt reads it after the turn
}

// Run runs the agent until the input ends, the user exits or a crash step.
func Run(ctx context.Context, cfg Config) error {
	a := &agent{cfg: cfg, p: cfg.Provider, clk: cfg.Clock, recs: cfg.Recordings, env: envMap(cfg.Env), dir: cfg.Dir}
	if a.recs == nil {
		a.recs = testkit.Recordings()
	}
	if _, ok := profiles[a.p]; !ok {
		return fmt.Errorf("fakeagent: unknown provider %q", a.p)
	}
	if err := a.parseArgs(); err != nil {
		return err
	}
	if a.p == Codex {
		a.transcript = a.codexTranscript()
	}
	events := make(chan event, 64)
	a.keys = make(chan event, 16)
	go readInput(cfg.In, events, a.clk)

	a.print(profiles[a.p].title + "\r\n" + a.dir + "\r\n")
	if s := a.env["FAKEAGENT_START_SCREEN"]; s != "" {
		// A start screen no adapter knows: it waits for Enter (the
		// escalation, answered with `mestre ask --raw`) and goes on.
		a.print(s + "\r\n")
		if _, err := a.menu(ctx, events, []string{"Continue"}); err != nil {
			return err
		}
	}
	if !a.loggedIn() {
		pr := profiles[a.p]
		a.print(pr.loginTitle + "\r\n")
		if _, err := a.menu(ctx, events, pr.loginOpts); err != nil { // escalated by the adapter
			return err
		}
		return ErrLoginRequired
	}
	trusted, err := a.folderTrusted()
	if err != nil {
		return err
	}
	if !trusted {
		pr := profiles[a.p]
		a.print(pr.trustQ + "\r\n")
		choice, err := a.menu(ctx, events, []string{pr.trustYes, pr.trustNo})
		if err != nil {
			return err
		}
		if choice != 0 {
			return ErrDeclinedTrust
		}
		if err := a.trustFolder(); err != nil {
			return err
		}
	}
	a.trusted = true
	if a.hooks, err = a.loadHooks(); err != nil {
		return err
	}
	a.statusLine = a.claudeStatusLine()
	if a.p == Codex {
		if pending := a.untrustedHooks(); len(pending) > 0 {
			a.print(fmt.Sprintf("%s: %d new or modified hook(s) will not run until trusted\r\n", HookReviewTitle, len(pending)))
			choice, err := a.menu(ctx, events, hookReviewOpts)
			if err != nil {
				return err
			}
			if choice == 1 {
				if err := a.trustAllHooks(); err != nil {
					return err
				}
			}
		}
	}
	if ev := profiles[a.p].startEvent; ev != "" {
		if pl, ok := a.payload(ev, a.common(nil)); ok {
			a.fire(ctx, ev, "", pl)
		}
	}
	return a.loop(ctx, events)
}

func (a *agent) parseArgs() error {
	for i := 0; i < len(a.cfg.Args); i++ {
		arg := a.cfg.Args[i]
		next := func() string {
			if i+1 < len(a.cfg.Args) {
				i++
				return a.cfg.Args[i]
			}
			return ""
		}
		switch arg {
		case "--session-id", "--resume", "--conversation":
			a.sessionID = next()
		case "--settings":
			a.settingsArg = next()
		case "--append-system-prompt-file":
			a.layerFile = next()
		case "--add-dir":
			a.addDirs = append(a.addDirs, next())
		case "--fake-recording-version":
			a.cfg.RecordingVersion = next()
		case "--fake-script":
			s, err := LoadScript(a.resolve(next()))
			if err != nil {
				return err
			}
			a.script = s
		}
	}
	if v := a.env["FAKEAGENT_RECORDING_VERSION"]; v != "" && a.cfg.RecordingVersion == "" {
		a.cfg.RecordingVersion = v
	}
	if a.script == nil {
		if p := a.env["FAKEAGENT_SCRIPT"]; p != "" {
			s, err := LoadScript(a.resolve(p))
			if err != nil {
				return err
			}
			a.script = s
		}
	}
	if v := a.cfg.RecordingVersion; v != "" {
		if _, err := fs.Stat(a.recs, path.Join(profiles[a.p].recordings, v)); err != nil {
			return notImplemented("%s recording version %q: %v", a.p, v, err)
		}
	}
	if a.cfg.SessionID != "" {
		a.sessionID = a.cfg.SessionID
	}
	if a.sessionID == "" {
		a.sessionID = newUUID()
	}
	return nil
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func (a *agent) print(s string) {
	a.outMu.Lock()
	defer a.outMu.Unlock()
	_, _ = io.WriteString(a.cfg.Out, s)
}

// menu draws options and returns the chosen index (arrows move, Enter picks).
func (a *agent) menu(ctx context.Context, events <-chan event, opts []string) (int, error) {
	sel := 0
	draw := func() {
		var b strings.Builder
		for i, o := range opts {
			mark := "  "
			if i == sel {
				mark = "❯ "
			}
			fmt.Fprintf(&b, "%s%d. %s\r\n", mark, i+1, o)
		}
		a.print(b.String())
	}
	draw()
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case ev, ok := <-events:
			if !ok {
				return 0, io.EOF
			}
			switch ev.kind {
			case evUp:
				sel = (sel + len(opts) - 1) % len(opts)
				draw()
			case evDown:
				sel = (sel + 1) % len(opts)
				draw()
			case evEnter:
				return sel, nil
			case evEscape: // the last option: "No, exit", "No"
				return len(opts) - 1, nil
			case evInterrupt:
				return 0, context.Canceled
			}
		}
	}
}

// loop reads prompts and runs turns until the input ends. What was typed
// during a turn, and no menu of the turn read, comes first after it, as in
// the real CLIs.
func (a *agent) loop(ctx context.Context, events <-chan event) error {
	var line strings.Builder
	interrupts := 0
	a.print(PromptMarker)
	for {
		var ev event
		if len(a.queued) > 0 {
			ev, a.queued = a.queued[0], a.queued[1:]
		} else {
			ok := false
			select {
			case <-ctx.Done():
				return nil
			case ev, ok = <-events:
			}
			if !ok {
				return nil
			}
		}
		switch ev.kind {
		case evText:
			interrupts = 0
			line.WriteString(ev.text)
			a.print(ev.text)
		case evBackspace:
			s := []rune(line.String())
			if len(s) > 0 {
				line.Reset()
				line.WriteString(string(s[:len(s)-1]))
			}
		case evInterrupt:
			line.Reset()
			if interrupts++; interrupts == 2 {
				a.endSession(ctx)
				return nil
			}
			a.print("\r\nPress Ctrl-C again to exit" + PromptMarker)
		case evEnter:
			interrupts = 0
			prompt := strings.TrimSpace(line.String())
			line.Reset()
			if prompt == "" {
				continue
			}
			if prompt == "/exit" && (a.p == Claude || a.p == Codex) {
				a.endSession(ctx)
				return nil
			}
			if err := a.runTurn(ctx, events, prompt); err != nil {
				return err
			}
			a.print(PromptMarker)
		}
	}
}

// runTurn runs one turn; Ctrl-C interrupts it.
func (a *agent) runTurn(ctx context.Context, events <-chan event, prompt string) error {
	a.print("\r\n" + BusyMarker + "\r\n")
	if prompt == "/fake-dump" {
		a.print(a.dump() + "\r\n")
		return nil
	}
	if prompt == "/compact" && (a.p == Claude || a.p == Codex) {
		a.fireClaude(ctx, "PreCompact", map[string]any{"trigger": "manual"})
		a.print("Compacted\r\n")
		return nil
	}
	tctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.turnBody(tctx, prompt) }()
	for {
		select {
		case err := <-done:
			a.queueKeys()
			return err
		case ev, ok := <-events:
			if !ok {
				cancel()
				<-done
				return nil
			}
			if ev.kind == evInterrupt {
				cancel()
				<-done
				a.print("Interrupted\r\n")
				if a.p == Codex {
					a.fireClaude(ctx, "Interrupt", nil)
				}
				return nil
			}
			select { // the menus of the turn read the other keys
			case a.keys <- ev:
			default:
			}
		}
	}
}

// queueKeys gives the prompt the keys typed during the turn that no menu
// of the turn read.
func (a *agent) queueKeys() {
	for {
		select {
		case ev := <-a.keys:
			a.queued = append(a.queued, ev)
		default:
			return
		}
	}
}

func (a *agent) turnBody(ctx context.Context, prompt string) error {
	a.mu.Lock()
	a.turn++
	turnID := fmt.Sprintf("t%d", a.turn)
	a.mu.Unlock()
	if a.p == Codex {
		a.codexTurnStart(turnID)
	}
	pr := profiles[a.p]
	if pl, ok := a.payload(pr.promptEvent, a.common(map[string]any{"prompt": prompt})); ok {
		a.fire(ctx, pr.promptEvent, "", pl)
	}
	steps := a.script.stepsFor(prompt)
	if steps == nil {
		say, run := prompt, fmt.Sprintf("mestre done %q", prompt)
		steps = []Step{{Say: &say}, {Run: &run}}
	}
	for _, s := range steps {
		if ctx.Err() != nil || a.turnFailed {
			break
		}
		if err := a.step(ctx, s); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	if a.turnFailed { // StopFailure ended the turn: no Stop
		a.turnFailed = false
		a.runStatusLine(ctx)
		return nil
	}
	if a.p == Codex && a.codexTurnEnd(turnID) {
		return nil // a failed turn runs no Stop hook
	}
	if a.p == Antigravity { // after the tool calls of the invocation finish
		if pl, ok := a.payload("PostInvocation", a.common(nil)); ok {
			a.fire(ctx, "PostInvocation", "", pl)
		}
	}
	if pl, ok := a.payload("Stop", a.common(map[string]any{"last_assistant_message": a.lastMsg})); ok {
		a.fire(ctx, "Stop", "", pl)
	}
	if a.p == Codex {
		a.codexTurnComplete(turnID)
	}
	a.runStatusLine(ctx)
	return nil
}

func (a *agent) step(ctx context.Context, s Step) error {
	switch {
	case s.Say != nil:
		a.lastMsg = *s.Say
		a.print(*s.Say + "\r\n")
	case s.Tool != nil:
		if s.Tool.Ask {
			if a.p != Claude {
				return notImplemented("%s: a tool that asks for permission is emulated for claude only", a.p)
			}
			a.ask = true
		}
		a.useTool(ctx, s.Tool.Name, s.Tool.Input, func() string { return "" })
	case s.Limit != nil:
		return a.claudeLimit(ctx, *s.Limit)
	case s.Edit != nil:
		path := a.resolve(s.Edit.Path)
		in := map[string]any{"file_path": path, "content": s.Edit.Content}
		a.useTool(ctx, profiles[a.p].editTool, in, func() string {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				a.toolFailed = true
				return err.Error()
			}
			if err := os.WriteFile(path, []byte(s.Edit.Content), 0o644); err != nil {
				a.toolFailed = true
				return err.Error()
			}
			return "wrote " + s.Edit.Path
		})
	case s.Run != nil:
		pr := profiles[a.p]
		a.useTool(ctx, pr.shellTool, map[string]any{pr.shellArg: *s.Run}, func() string {
			if a.cfg.Commands == nil {
				return "no command runner"
			}
			out, code, err := a.cfg.Commands.RunCommand(ctx, *s.Run, a.dir, a.cfg.Env)
			if err != nil {
				a.toolFailed = true
				return err.Error()
			}
			a.toolFailed = code != 0
			return fmt.Sprintf("%s(exit %d)", out, code)
		})
	case s.Wait != nil:
		_ = clock.Sleep(ctx, a.clk, s.wait)
	case s.Hang:
		<-ctx.Done()
	case s.Crash != nil:
		a.print("Error: " + *s.Crash + "\r\n")
		return fmt.Errorf("%w: %s", ErrCrash, *s.Crash)
	}
	return nil
}

// useTool fires PreToolUse, runs the tool when allowed and fires
// PostToolUse.
func (a *agent) useTool(ctx context.Context, name string, input map[string]any, exec func() string) {
	vals := a.toolVals(name, input)
	if pl, ok := a.payload("PreToolUse", vals); ok {
		if blocked, reason := a.fire(ctx, "PreToolUse", name, pl); blocked {
			a.ask = false
			a.print(fmt.Sprintf("Tool %s blocked by hook: %s\r\n", name, reason))
			return
		}
	}
	if !a.codexEscalation(ctx, name, input, vals) {
		return
	}
	if a.ask {
		a.ask = false
		if !a.claudePermission(ctx, name, vals) {
			a.print(fmt.Sprintf("Tool %s denied\r\n", name))
			return
		}
	}
	a.toolFailed = false
	out := exec()
	a.print(fmt.Sprintf("Tool %s ran %s\r\n", name, strings.TrimSpace(out)))
	post := "PostToolUse"
	if a.p == Claude && a.toolFailed {
		post, vals["error"] = "PostToolUseFailure", strings.TrimSpace(out)
	}
	if pl, ok := a.payload(post, vals); ok {
		a.fire(ctx, post, name, pl)
	}
}

// common are the fields every payload of the provider carries.
func (a *agent) common(extra map[string]any) map[string]any {
	a.mu.Lock()
	turn := a.turn
	a.mu.Unlock()
	transcript := filepath.Join(a.accountDir(), "projects", a.sessionID+".jsonl")
	if a.transcript != "" {
		transcript = a.transcript
	}
	v := map[string]any{
		"session_id": a.sessionID, "cwd": a.dir,
		"transcript_path": transcript,
		"prompt_id":       fmt.Sprintf("p%d", turn), "turn_id": fmt.Sprintf("t%d", turn),
		"conversationId": a.sessionID, "workspacePaths": []string{a.dir},
		"invocationNum": turn, "executionNum": turn, "stepIdx": turn,
		"transcriptPath":        filepath.Join(a.accountDir(), "brain", a.sessionID, "transcript_full.jsonl"),
		"artifactDirectoryPath": filepath.Join(a.accountDir(), "brain", a.sessionID),
	}
	for k, x := range extra {
		v[k] = x
	}
	return v
}

func (a *agent) nextToolID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.toolSeq++
	return fmt.Sprintf("toolu_%02d", a.toolSeq)
}

func (a *agent) toolVals(name string, input map[string]any) map[string]any {
	if a.p == Antigravity {
		args := map[string]any{"Cwd": a.dir}
		for k, x := range input {
			args[k] = x
		}
		return a.common(map[string]any{"toolCall": map[string]any{"name": name, "args": args}})
	}
	return a.common(map[string]any{
		"tool_name": name, "tool_input": input, "tool_use_id": a.nextToolID(),
	})
}

// dump answers /fake-dump with what the agent received.
func (a *agent) dump() string {
	type hookInfo struct {
		Event   string `json:"event"`
		Command string `json:"command"`
		Trusted bool   `json:"trusted"`
	}
	hs := make([]hookInfo, 0, len(a.hooks))
	for _, h := range a.hooks {
		hs = append(hs, hookInfo{h.event, h.command, h.trusted})
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].Event+hs[i].Command < hs[j].Event+hs[j].Command })
	mestreEnv := map[string]string{}
	for k, v := range a.env {
		if strings.HasPrefix(k, "MESTRE_") {
			mestreEnv[k] = v
		}
	}
	a.mu.Lock()
	fired := append([]string(nil), a.hookLog...)
	a.mu.Unlock()
	b, _ := json.Marshal(map[string]any{
		"provider": a.p, "cwd": a.dir, "args": a.cfg.Args, "session_id": a.sessionID,
		"trusted": a.trusted, "hooks": hs, "hooks_fired": fired, "env": mestreEnv,
		"append_system_prompt": a.layerText(), "add_dirs": a.addDirs, "status_line": a.statusLine,
	})
	return string(b)
}

// Input events.
type eventKind int

const (
	evText eventKind = iota
	evEnter
	evUp
	evDown
	evInterrupt
	evBackspace
	evEscape
)

type event struct {
	kind eventKind
	text string
}

// readInput turns raw terminal bytes into events: bracketed paste, arrows,
// Ctrl-C, backspace and Enter (\r). The channel closes when in ends.
func readInput(in io.Reader, out chan<- event, clk clock.Clock) {
	defer close(out)
	src := newSource(in, clk)
	r := bufio.NewReader(src)
	for {
		b, err := r.ReadByte()
		if err != nil {
			return
		}
		switch b {
		case '\r':
			out <- event{kind: evEnter}
		case 0x03:
			out <- event{kind: evInterrupt}
		case 0x7f, 0x08:
			out <- event{kind: evBackspace}
		case 0x1b:
			if r.Buffered() == 0 && !src.more(escapeWait) { // Esc alone, not the start of a sequence
				out <- event{kind: evEscape}
				continue
			}
			seq := readEscape(r)
			switch seq {
			case "[A":
				out <- event{kind: evUp}
			case "[B":
				out <- event{kind: evDown}
			case "[200~":
				text, ok := readPaste(r)
				out <- event{kind: evText, text: text}
				if !ok {
					return
				}
			}
		default:
			out <- event{kind: evText, text: string([]byte{b})}
		}
	}
}

// escapeWait is how long after an Esc the rest of an escape sequence may
// take to arrive; none by then, and it was the Esc key.
const escapeWait = 100 * time.Millisecond

// source is the input of the agent read by a goroutine of its own, so that
// readInput can tell whether more input comes soon.
type source struct {
	clk  clock.Clock
	ch   chan []byte
	rest []byte
}

func newSource(in io.Reader, clk clock.Clock) *source {
	s := &source{clk: clk, ch: make(chan []byte, 8)}
	go func() {
		defer close(s.ch)
		buf := make([]byte, 4096)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				s.ch <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	return s
}

func (s *source) Read(p []byte) (int, error) {
	if len(s.rest) == 0 {
		chunk, ok := <-s.ch
		if !ok {
			return 0, io.EOF
		}
		s.rest = chunk
	}
	n := copy(p, s.rest)
	s.rest = s.rest[n:]
	return n, nil
}

// more reports whether input arrives within d of the clock of the agent: a
// test with a fake clock advances it to tell that the Esc was alone.
func (s *source) more(d time.Duration) bool {
	if len(s.rest) > 0 {
		return true
	}
	t := s.clk.NewTimer(d)
	defer t.Stop()
	select {
	case chunk, ok := <-s.ch:
		s.rest = chunk
		return ok
	case <-t.C():
		return false
	}
}

func readEscape(r *bufio.Reader) string {
	var b strings.Builder
	for {
		c, err := r.ReadByte()
		if err != nil {
			return b.String()
		}
		b.WriteByte(c)
		if b.Len() > 1 && c >= 0x40 && c <= 0x7e {
			return b.String()
		}
	}
}

func readPaste(r *bufio.Reader) (string, bool) {
	const end = "\x1b[201~"
	var b strings.Builder
	for {
		c, err := r.ReadByte()
		if err != nil {
			return b.String(), false
		}
		b.WriteByte(c)
		if strings.HasSuffix(b.String(), end) {
			return strings.TrimSuffix(b.String(), end), true
		}
	}
}
