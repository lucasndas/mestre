package conformance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// character is one terminal of the character of a run, with the supervision
// of the adapter (screens, deliveries) fed by the screens of the terminal
// and by the hook calls the host receives.
type character struct {
	ru    *run
	sess  *Session
	sup   *providers.Supervisor
	turns *providers.Turns
	// first is the first call of the log that belongs to this terminal.
	first int

	mu        sync.Mutex
	screen    string
	changed   chan struct{}
	blocked   string // why the supervision blocked it
	delivered int
	ready     bool
	stables   int    // stable screens taken so far
	readyAt   []byte // the raw output up to the first ready screen
	cancel    context.CancelFunc
	done      chan struct{}
}

// start starts a terminal with launch and its supervision.
func (ru *run) start(ctx context.Context, launch providers.Launch, resumeAfter string) (*character, error) {
	c := &character{ru: ru, turns: providers.NewTurns(ru.adapter.Info()), first: ru.r.Host.Events().Len(),
		changed: make(chan struct{}), done: make(chan struct{})}
	if resumeAfter != "" {
		c.turns.ResumeAfter(resumeAfter)
	}
	sess, err := ru.r.Host.Start(ctx, StartSpec{ID: "c" + randomID(), AgentID: ru.ws.AgentID, Launch: launch, Env: ru.env})
	if err != nil {
		return nil, err
	}
	c.sess = sess
	c.sup = providers.NewSupervisor(providers.SupervisorConfig{
		Clock: ru.r.clk(), Adapter: ru.adapter, AgentID: ru.ws.AgentID, TableID: ru.lreq.TableID,
		Keyboard: sess, Characters: c, TableMaster: c, Checklist: c, Inbox: c, Decisions: ru,
	})
	pctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.cancel = cancel
	c.sup.Start(pctx)
	go c.pump(pctx)
	ru.chars = append(ru.chars, c)
	return c, nil
}

// pump feeds the supervision with the stable screens and the hook calls of
// the character, and keeps the events of the run.
func (c *character) pump(ctx context.Context) {
	defer close(c.done)
	log := c.ru.r.Host.Events()
	seen := c.first
	for {
		changes := log.changes()
		for _, call := range log.Since(seen, c.ru.ws.AgentID) {
			c.hook(call)
		}
		seen = log.Len()
		select {
		case <-ctx.Done():
			return
		case <-changes:
		case <-c.sess.Stable():
			text, err := c.sess.Screen(ctx)
			if err != nil {
				continue
			}
			c.sup.Screen(text, true)
			c.mu.Lock()
			c.screen = text
			c.stables++
			if !c.ready && c.ru.adapter.Screens(providers.ScreenInput{Text: text, Stable: true}).State == providers.StateReady {
				c.ready, c.readyAt = true, c.sess.Raw()
			}
			close(c.changed)
			c.changed = make(chan struct{})
			c.mu.Unlock()
		case <-c.sess.Ended():
			c.mu.Lock()
			close(c.changed)
			c.changed = make(chan struct{})
			c.mu.Unlock()
			<-ctx.Done()
			return
		}
	}
}

// hook takes one hook call of the character.
func (c *character) hook(call Call) {
	for _, ev := range c.turns.Apply(call.Event) {
		c.sup.Hook(ev)
		c.ru.mu.Lock()
		c.ru.events = append(c.ru.events, ev)
		c.ru.mu.Unlock()
	}
	switch call.Event.Event {
	case providers.EventPreTool, providers.EventPermissionRequest:
		choice := "deny"
		switch {
		case !call.Answered:
			// A paused call was decided by the degraded mode of the
			// dispatcher, whose answer the run does not see but computes
			// the same way: a screen for it (Antigravity asks natively
			// what its hooks cannot allow) follows that decision, and
			// without one it is denied.
			switch call.Degraded {
			case classes.Allow:
				choice = "allow"
			case "":
				choice = "unknown"
			}
		case call.Answer.Verdict == hook.VerdictAllow:
			choice = "allow"
		}
		c.ru.mu.Lock()
		c.ru.calls = append(c.ru.calls, providers.CallDecision{Tool: call.Event.Tool, Choice: choice, Cwd: call.Event.Cwd})
		c.ru.mu.Unlock()
	case providers.EventPostTool: // the call ended: it asks no more
		c.ru.mu.Lock()
		c.ru.calls = slices.DeleteFunc(c.ru.calls, func(d providers.CallDecision) bool { return sameCall(d.Tool, call.Event.Tool) })
		c.ru.mu.Unlock()
	}
	if c.ru.r.Recorder != nil && len(call.Event.Raw) > 0 {
		tool := ""
		if call.Event.Tool != nil {
			tool = call.Event.Tool.NativeName
		}
		c.ru.r.Recorder.Hook(c.ru.req, call.Event.NativeEvent, tool, call.Event.Raw, c.ru.pathMasks())
	}
}

// Decision implements providers.Decisions: the trust of the worktree the
// run created, and the native permission screen answered with the engine's
// decision for the call the screen shows (providers.ScreenCall), used once;
// without one, denied.
func (ru *run) Decision(_ context.Context, _, screenID, screen string) (string, bool) {
	switch screenID {
	case "trust":
		ru.res.Notes = append(ru.res.Notes, "a tela de confiança da pasta apareceu mesmo com a pré-confiança")
		return "trust", true
	case "permission", "approval":
		ru.mu.Lock()
		defer ru.mu.Unlock()
		i := providers.ScreenCall(screen, ru.calls)
		if ru.r.Recorder != nil {
			var calls []string
			for _, d := range ru.calls {
				if d.Tool != nil {
					calls = append(calls, fmt.Sprintf("%s %s: %s%s%s (cwd %s)", d.Choice, d.Tool.NativeName, d.Tool.Command, strings.Join(d.Tool.Paths, " "), d.Tool.URL, d.Cwd))
				}
			}
			answer := "deny"
			if i >= 0 && ru.calls[i].Choice == "allow" {
				answer = "allow"
			}
			ru.r.Recorder.Dialog(ru.req, screen, calls, answer, ru.pathMasks())
		}
		if i < 0 {
			ru.res.Notes = append(ru.res.Notes, "a tela de permissão nativa apareceu sem decisão do motor para a chamada que ela mostra: negada")
			return "deny", true
		}
		ch := ru.calls[i].Choice
		if ch != "allow" {
			ch = "deny" // a denial, or a call whose decision the run does not know
		}
		ru.calls = slices.Delete(ru.calls, i, i+1)
		ru.res.Notes = append(ru.res.Notes, "a tela de permissão nativa apareceu e foi respondida com a decisão da chamada: "+ch)
		return ch, true
	}
	return "", false
}

// sameCall reports whether two tool events are the same call: the same tool
// with the same input.
func sameCall(a, b *providers.Tool) bool {
	return a != nil && b != nil && a.NativeName == b.NativeName && a.Command == b.Command &&
		slices.Equal(a.Paths, b.Paths) && a.URL == b.URL && bytes.Equal(a.RawInput, b.RawInput)
}

// The parts of the supervision: the character state, the Mestre da mesa,
// the checklist and the inbox of a run only record.

func (c *character) Block(_ context.Context, _ string, reason providers.BlockReason, detail string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocked = string(reason) + ": " + detail
	close(c.changed)
	c.changed = make(chan struct{})
	return nil
}

func (c *character) ReduceMonitoring(context.Context, string, providers.ReducedReason, string) error {
	return nil
}

func (c *character) Notify(context.Context, providers.Notice) error { return nil }

func (c *character) Alert(_ context.Context, a providers.Alert) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blocked == "" {
		c.blocked = a.Item + ": " + a.Detail
	}
	return nil
}

func (c *character) Confirmed(context.Context, string) error   { return nil }
func (c *character) Unconfirmed(context.Context, string) error { return nil }

// state is the current screen, why it is blocked and the signal of the
// next change.
func (c *character) state() (string, string, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.screen, c.blocked, c.changed
}

func (c *character) ended() bool {
	select {
	case <-c.sess.Ended():
		return true
	default:
		return false
	}
}

// waitReady waits for the input prompt of the adapter on a stable screen.
func (c *character) waitReady(ctx context.Context, limit time.Duration) error {
	ctx, cancel := clock.WithTimeout(ctx, c.ru.r.clk(), limit)
	defer cancel()
	for {
		screen, blocked, changed := c.state()
		c.mu.Lock()
		ready := c.ready && c.ru.adapter.Screens(providers.ScreenInput{Text: screen, Stable: true}).State == providers.StateReady
		c.mu.Unlock()
		switch {
		case blocked != "":
			return fmt.Errorf("tela não reconhecida ou que pede resposta (%s)", blocked)
		case ready:
			return nil
		case c.ended():
			return errors.New("o processo do provedor terminou")
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return fmt.Errorf("não ficou pronto em %s", limit)
		}
	}
}

// turn delivers text and waits for the end of the turn: turn_end (or
// turn_failure) of the character after the delivery, then the prompt again.
// It returns the answer (the last message of the turn, or the screen) and
// the hook calls of the turn.
func (c *character) turn(ctx context.Context, text string) (string, []Call, error) {
	log := c.ru.r.Host.Events()
	from := log.Len()
	c.mu.Lock()
	c.delivered++
	id := fmt.Sprintf("d%d", c.delivered)
	c.ready = false
	c.mu.Unlock()
	// A native permission screen is answered with the decision of a call of
	// this turn only; without one, it is denied.
	c.ru.mu.Lock()
	c.ru.calls = nil
	c.ru.mu.Unlock()
	tctx, cancel := clock.WithTimeout(ctx, c.ru.r.clk(), or(c.ru.r.TurnLimit, DefaultTurnLimit))
	defer cancel()
	if err := c.sup.Deliver(tctx, id, text); err != nil {
		return "", nil, err
	}
	// A blocked character or an ended process ends the wait too.
	wctx, stopWait := context.WithCancel(tctx)
	go func() {
		defer stopWait()
		for {
			_, blocked, changed := c.state()
			if blocked != "" || c.ended() {
				return
			}
			select {
			case <-changed:
			case <-c.sess.Ended():
			case <-wctx.Done():
				return
			}
		}
	}()
	end, ok := log.Wait(wctx, from, c.ru.ws.AgentID, func(call Call) bool {
		return call.Event.Event == providers.EventTurnEnd || call.Event.Event == providers.EventTurnFailure
	})
	stopWait()
	calls := log.Since(from, c.ru.ws.AgentID)
	if !ok {
		screen, blocked, _ := c.state()
		switch {
		case blocked != "":
			return screen, calls, fmt.Errorf("o personagem ficou bloqueado: %s", blocked)
		case c.ended():
			return screen, calls, errors.New("o processo do provedor terminou no meio do turno")
		}
		// The next message must not land on a screen of this turn: Esc
		// cancels it, and a character that does not come back stays blocked.
		_ = c.sess.Input(ctx, []byte("\x1b"))
		if err := c.waitReady(ctx, 30*time.Second); err != nil {
			c.mu.Lock()
			if c.blocked == "" {
				c.blocked = "o turno não terminou e o personagem não voltou ao prompt"
			}
			c.mu.Unlock()
		}
		return screen, calls, fmt.Errorf("o turno não terminou em %s", or(c.ru.r.TurnLimit, DefaultTurnLimit))
	}
	// The screen of the answer is the one after the end of the turn: a
	// ready screen from before it (the message echoed while the hooks are
	// not there yet) has no answer.
	c.settle(ctx, c.sess)
	// The prompt again before the next delivery: typed during a turn, it
	// would wait for the turn's end.
	_ = c.waitReady(ctx, 30*time.Second)
	answer := end.Event.LastMessage
	if strings.TrimSpace(answer) == "" {
		screen, _, _ := c.state()
		answer = after(screen, lastLine(text))
	}
	return answer, log.Since(from, c.ru.ws.AgentID), nil
}

// liveScreen is the terminal as settle sees it.
type liveScreen interface {
	Screen(context.Context) (string, error)
	Ended() <-chan struct{}
}

// settleLimit is how long the screen of the answer is waited for after the
// end of a turn.
const settleLimit = 2 * time.Second

// settle makes c.screen a screen taken after the end of the turn: the next
// stable screen, or, when the screen did not change in settleLimit (it was
// already final), the live one. Antigravity draws the answer at the Stop
// hook, and the stable screen before it is the echoed message with an empty
// input box, which is ready too (conformance round 3).
func (c *character) settle(ctx context.Context, live liveScreen) {
	c.mu.Lock()
	n, changed := c.stables, c.changed
	c.mu.Unlock()
	t := c.ru.r.clk().NewTimer(settleLimit)
	defer t.Stop()
	for {
		select {
		case <-changed:
			c.mu.Lock()
			done := c.stables > n
			changed = c.changed
			c.mu.Unlock()
			if done {
				return
			}
		case <-t.C():
			if text, err := live.Screen(ctx); err == nil {
				c.mu.Lock()
				c.screen = text
				c.mu.Unlock()
			}
			return
		case <-live.Ended():
			return
		case <-ctx.Done():
			return
		}
	}
}

// command types a command of the provider (/compact, /exit) the way a
// message goes.
func (c *character) command(ctx context.Context, text string) error {
	b, err := c.ru.adapter.TypeMessage(text)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.ready = false // the next ready screen is after the command
	c.mu.Unlock()
	if err := c.sess.Input(ctx, b); err != nil {
		return err
	}
	if r, ok := c.ru.adapter.(providers.SendRetrier); ok {
		go c.resendCommand(ctx, text, r)
	}
	return nil
}

// resendCommand types the send key once more while the input line still
// holds the command, as the supervision does for a delivery: Codex on
// Windows can keep what it was given without sending it
// (providers.SendRetrier). It stops at the first look that finds the line
// without the command, or after providers.SendRetries looks.
func (c *character) resendCommand(ctx context.Context, text string, r providers.SendRetrier) {
	for range providers.SendRetries {
		t := c.ru.r.clk().NewTimer(providers.DefaultSendRetry)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-c.sess.Ended():
			t.Stop()
			return
		case <-t.C():
		}
		screen, err := c.sess.Screen(ctx)
		if err != nil || !r.InputHolds(screen, text) {
			return
		}
		if err := c.sess.Input(ctx, r.SendKey()); err != nil {
			return
		}
	}
}

// stop ends the terminal and its supervision.
func (c *character) stop(ctx context.Context) error {
	err := c.sess.Stop(ctx)
	c.sup.Close()
	c.cancel()
	<-c.done
	return err
}

// after is the part of screen after the last line of the question: the
// answer, without the question.
func after(screen, last string) string {
	last = strings.TrimSpace(last)
	if i := strings.LastIndex(screen, last); last != "" && i >= 0 {
		return screen[i+len(last):]
	}
	return screen
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return lines[len(lines)-1]
}
