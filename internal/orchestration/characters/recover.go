package characters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/sessionzero"
)

// Stop ends a character: the normal exit of the provider, and after 30 s the
// whole tree; then the files it left (.mestre/agents/<id>/, its MESTRE_TMP)
// go away. It is allowed from the safe point (idle), from stuck and from
// interrupted, as the diagram has it; any other state is ErrNotAtSafePoint
// (the Citadel schedules the end for the next safe point, it does not force
// it). Stopping an interrupted character has no process to end.
func (rt *Runtime) Stop(ctx context.Context, cmd StopCommand) error {
	s := rt.session(cmd.Character)
	if s == nil {
		return noProcess(cmd.Character)
	}
	s.mu.Lock()
	var trig Trigger
	abandoned := false
	// A start nobody waits for any more (the Citadel gave up on it) is ended
	// where it is, in whatever state it reached by now (starting, or already busy
	// with its first message): the character is taken as dead and discarded, and
	// its terminal ended.
	if cmd.Abandon && cmd.Terminal != "" && s.terminal != cmd.Terminal {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s runs in %s, not in %s", ErrNotAtSafePoint, cmd.Character, s.terminal, cmd.Terminal)
	}
	if cmd.Abandon && s.st.State != Stopping && s.st.State != Interrupted {
		s.applyLocked(TrigDied, "abandoned: "+cmd.Reason)
		trig, abandoned = TrigDiscard, true
	}
	switch s.st.State {
	case Idle:
		trig = TrigStop
	case Stuck:
		trig = TrigForceStop
	case Interrupted:
		trig = TrigDiscard
	case Stopping:
	default:
		st := s.st.State
		s.mu.Unlock()
		return fmt.Errorf("%w: %s is %s", ErrNotAtSafePoint, cmd.Character, st)
	}
	var rep Report
	var changed bool
	if trig != "" {
		rep, changed = s.applyLocked(trig, "stop: "+cmd.Reason)
	}
	s.stopping = true
	wasInterrupted := trig == TrigDiscard && !abandoned
	proc := s.proc
	provider, l := s.launch.Provider, s.launch
	s.mu.Unlock()
	if changed {
		s.send(ctx, rep)
	}
	s.close()
	var graceful []byte
	if trig == TrigStop {
		graceful = exitInput(provider)
	}
	if proc != nil && !wasInterrupted {
		if err := proc.Stop(ctx, graceful, rt.o.GracefulWait); err != nil {
			return fmt.Errorf("end the terminal of %s: %w", l.Name, err)
		}
	}
	if err := rt.o.Prep.End(ctx, sessionzero.EndRequest{ID: cmd.Character, Dir: l.Dir, Provider: provider, Final: cmd.Final}); err != nil {
		rt.log.Warn("characters: the files of an ended character were not all removed", "id", cmd.Character, "err", err)
	}
	s.mu.Lock()
	rep, _ = s.applyLocked(TrigStopped, nonEmpty(cmd.Reason, "stopped"))
	s.mu.Unlock()
	if err := rt.local.Delete(ctx, cmd.Character); err != nil {
		rt.log.Warn("characters: the row of an ended character was not removed", "id", cmd.Character, "err", err)
	}
	rt.mu.Lock()
	delete(rt.sessions, cmd.Character)
	delete(rt.byTerm, s.terminal)
	rt.mu.Unlock()
	s.send(ctx, rep)
	return nil
}

// RecoverReport is what Recover did.
type RecoverReport struct {
	// Reconnected are the characters whose terminals were alive: they go on in
	// the state they were in, without passing through interrupted.
	Reconnected []string
	// Interrupted are the characters whose process is gone; the Citadel
	// decides the resumption (Service.ResumeMachine).
	Interrupted []string
}

// Recover is what the engine of the machine does when it starts: it reads what
// it kept of its characters, reconnects to the terminals that survived and
// supervises them again in the state they had (the timers continue from the
// last signal), and marks as interrupted every character whose process is gone.
// No character disappears in silence: a row with no terminal is interrupted
// too. Then it tells the Citadel the states as they are.
func (rt *Runtime) Recover(ctx context.Context) (RecoverReport, error) {
	var out RecoverReport
	rows, err := rt.local.List(ctx)
	if err != nil {
		return out, err
	}
	rep, herr := rt.o.Host.Reconcile(ctx)
	byAgent := map[string]RuntimeRow{}
	for _, r := range rows {
		byAgent[r.Agent] = r
	}
	seen := map[string]bool{}
	var errs []error
	if herr != nil {
		errs = append(errs, herr)
	}
	for _, p := range rep.Reconnected {
		row, ok := byAgent[p.CharacterID()]
		if !ok {
			rt.log.Warn("characters: a live terminal with no row of the machine", "terminal", p.ID())
			continue
		}
		seen[row.Agent] = true
		if err := rt.adopt(ctx, row, p); err != nil {
			errs = append(errs, err)
			continue
		}
		out.Reconnected = append(out.Reconnected, row.Agent)
	}
	dead := map[string]bool{}
	for _, id := range rep.Interrupted {
		dead[id] = true
	}
	for _, row := range rows {
		if seen[row.Agent] || row.State == Stopped {
			continue
		}
		reason := "the terminal is gone after the restart of the engine"
		if !dead[row.Terminal] {
			reason = "the machine has no terminal for it after the restart of the engine"
		}
		if err := rt.adoptDead(ctx, row, reason); err != nil {
			errs = append(errs, err)
			continue
		}
		out.Interrupted = append(out.Interrupted, row.Agent)
	}
	return out, errors.Join(errs...)
}

// session rebuilds the session of a row.
func (rt *Runtime) rebuild(row RuntimeRow, p Process) (*session, error) {
	adapter, err := rt.o.Providers.For(row.Launch.Provider)
	if err != nil {
		return nil, err
	}
	s := &session{rt: rt, id: row.Agent, launch: row.Launch, adapter: adapter, info: adapter.Info(), terminal: row.Terminal, sessionID: row.Session,
		lastTurn: row.LastTurn, starts: row.Starts, reduced: row.Reduced, turns: providers.NewTurns(adapter.Info()), proc: p}
	s.turns.ResumeAfter(row.LastTurn)
	s.pendingFirst, s.firstHeld = row.Launch.PendingFirst, row.Launch.PendingFirst != ""
	s.readyRe = readyRegexp(row.Launch)
	s.st, s.since, s.lastSignal = row.Status, row.Since, row.LastSeen
	return s, nil
}

func (rt *Runtime) register(s *session) {
	rt.mu.Lock()
	rt.sessions[s.id], rt.byTerm[s.terminal] = s, s
	rt.mu.Unlock()
}

// adopt takes a character whose terminal survived: the same state, the
// supervision again, the timers from the last signal.
func (rt *Runtime) adopt(ctx context.Context, row RuntimeRow, p Process) error {
	s, err := rt.rebuild(row, p)
	if err != nil {
		return err
	}
	rt.register(s)
	s.mu.Lock()
	switch s.st.State {
	case Starting, Idle, Busy, Waiting, Blocked, Stuck:
		s.sup = rt.supervisor(s)
		rt.rearm(s)
	}
	stopping, starting := s.st.State == Stopping, s.st.State == Starting
	rep := s.reportLocked(rt.clk.Now())
	rep.Resync = true
	sup := s.sup
	s.mu.Unlock()
	switch {
	case sup != nil && starting:
		sup.Start(context.WithoutCancel(ctx))
	case sup != nil:
		// The session began before the restart: the start screens and the
		// 30 seconds of the start do not apply to it.
		sup.Hook(providers.Event{Event: providers.EventSessionStart})
	}
	s.send(ctx, rep)
	go rt.watch(s, p)
	rt.restoreZero(ctx, s)
	s.becameReady(ctx) // the first message, if it was waiting for the terminal to be ready
	if stopping {
		// The engine fell while it was ending the character: finish it.
		return rt.Stop(ctx, StopCommand{Character: s.id, Final: true, Reason: "stop finished after the restart of the engine"})
	}
	return nil
}

// restoreZero gives the session zero back what it keeps of a living character
// (the request it was prepared with), so that the layer can be refreshed and
// the identity updated after the restart of the engine.
func (rt *Runtime) restoreZero(ctx context.Context, s *session) {
	r, ok := rt.o.Prep.(interface {
		Restore(context.Context, sessionzero.CharacterRequest) error
	})
	if !ok || len(s.launch.Request) == 0 {
		return
	}
	var req sessionzero.CharacterRequest
	if err := json.Unmarshal(s.launch.Request, &req); err != nil {
		rt.log.Warn("characters: the request of a character does not decode", "id", s.id, "err", err)
		return
	}
	if err := r.Restore(ctx, req); err != nil {
		rt.log.Warn("characters: the session zero of a character was not brought back", "id", s.id, "err", err)
	}
}

// rearm continues the timers of a state after a restart, from the last signal.
func (rt *Runtime) rearm(s *session) {
	now := rt.clk.Now()
	s.gen++
	gen := s.gen
	switch s.st.State {
	case Starting:
		left := s.since.Add(rt.o.StartTimeout).Sub(now)
		s.startTimer = rt.clk.AfterFunc(max(left, time.Millisecond), func() { s.startExpired(gen) })
	case Busy:
		left := s.lastSignal.Add(rt.o.StuckAfter).Sub(now)
		s.stuckTimer = rt.clk.AfterFunc(max(left, time.Millisecond), func() { s.stuckExpired(gen) })
	}
}

// adoptDead takes a character whose process is gone: interrupted, visible,
// with the reason. A character that was already ending is finished.
func (rt *Runtime) adoptDead(ctx context.Context, row RuntimeRow, reason string) error {
	s, err := rt.rebuild(row, nil)
	if err != nil {
		return err
	}
	rt.register(s)
	s.mu.Lock()
	var rep Report
	switch s.st.State {
	case Interrupted:
		rep = s.reportLocked(rt.clk.Now())
	case Stopping:
		s.mu.Unlock()
		return rt.Stop(ctx, StopCommand{Character: s.id, Final: true, Reason: "stop finished after the restart of the engine"})
	default:
		var ok bool
		if rep, ok = s.applyLocked(TrigDied, reason); !ok {
			s.mu.Unlock()
			return nil
		}
	}
	rep.Resync = true
	s.mu.Unlock()
	s.send(ctx, rep)
	return nil
}
