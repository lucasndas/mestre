package characters

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/mestre-ia/mestre/internal/protocol"
)

// resumeRank is the order of the resumption: the Grand Master, the table
// masters, the characters with a task or step under way, the persistent ones,
// the rest (docs/orquestracao/personagens.md#retomada-após-reinício).
func resumeRank(c Character) int {
	switch {
	case c.Role == RoleGrandMaster:
		return 0
	case c.Role == RoleTableMaster:
		return 1
	case c.TaskOpen:
		return 2
	case c.Lifecycle == Persistent:
		return 3
	}
	return 4
}

// ResumeMachine decides what happens to the characters of a machine that are
// interrupted (it restarted, or a mestre-pty died, or it was away and is
// back), in the order of the resumption:
//
//   - a simple terminal stays interrupted: its creator was told, and the
//     engine never runs commands again by itself;
//   - an ephemeral character with no task under way is not resumed: it goes
//     to stopped, with the reason recorded;
//   - the others start again with the session of the provider they saved, or
//     from scratch with a message that says where they were. Their inbox and
//     waits are kept (they are the Citadel's).
//
// A machine that cannot be reached leaves its characters interrupted, visible,
// with the table master told; the next call tries again. No character
// disappears in silence.
func (s *Service) ResumeMachine(ctx context.Context, machine string) error {
	all, err := s.o.Registry.OnMachine(ctx, machine)
	if err != nil {
		return err
	}
	var todo []Character
	for _, c := range all {
		if c.State == Interrupted {
			todo = append(todo, c)
		}
	}
	slices.SortStableFunc(todo, func(a, b Character) int { return resumeRank(a) - resumeRank(b) })
	var errs []error
	for _, c := range todo {
		switch {
		case c.Kind == KindTerminal:
			continue
		case c.Role == RoleCharacter && c.Lifecycle == Ephemeral && !c.TaskOpen:
			err = s.discard(ctx, c, "an ephemeral character with no task under way is not resumed")
		default:
			err = s.resume(ctx, c)
		}
		if err != nil {
			errs = append(errs, err)
			var unreachable *MachineUnreachableError
			if errors.As(err, &unreachable) {
				_ = s.tellMaster(ctx, c, NoticeMachineDown, fmt.Sprintf("A máquina de %s está fora; ele continua interrompido e será retomado quando ela voltar.", c.Name))
			}
		}
	}
	return errors.Join(errs...)
}

// discard ends an interrupted character for good, recording why.
func (s *Service) discard(ctx context.Context, c Character, reason string) error {
	if err := s.executeStop(ctx, c, reason); err != nil {
		return err
	}
	return nil
}

// resume starts an interrupted character again.
func (s *Service) resume(ctx context.Context, c Character) error {
	p := placement{kind: c.Kind}
	if snap, err := s.o.Presets.Snapshot(ctx, c.Sheet); err == nil {
		p.snap = snap
	}
	if c.Table != "" {
		t, err := s.o.Tables.Table(ctx, c.Workspace, c.Table)
		if err != nil {
			return err
		}
		p.table = t
	}
	from, changed, err := s.o.Registry.Transition(ctx, c.ID, Starting, Change{Reason: "resume", At: s.clk.Now()})
	if err != nil {
		return err
	}
	if !changed {
		return nil // another resume got there first
	}
	if from != Interrupted {
		// The character moved on since the list was read (stopped, or
		// already running): put it back, it is not ours to start.
		_, _, err := s.o.Registry.Transition(ctx, c.ID, from, Change{Reason: "resume skipped: it was " + string(from), At: s.clk.Now()})
		return err
	}
	c.State = Starting
	if err := s.launch(ctx, c, p, true); err != nil {
		reason := "resume failed: " + err.Error()
		if _, _, terr := s.o.Registry.Transition(ctx, c.ID, Interrupted, Change{Reason: reason, At: s.clk.Now()}); terr != nil {
			return errors.Join(err, terr)
		}
		return err
	}
	return nil
}

// Reconcile takes the terminals a machine says are alive when it connects
// and compares them with the registry: the machine is the truth about the
// process, the Citadel about the rest. A character the registry has as alive
// that the machine does not have is interrupted; a state the machine knows
// better is taken. A terminal the registry does not know is not touched here
// (the machine ends it, terminal.Manager.Reconcile).
func (s *Service) Reconcile(ctx context.Context, machine string, live []protocol.TerminalState) error {
	all, err := s.o.Registry.OnMachine(ctx, machine)
	if err != nil {
		return err
	}
	byAgent := map[string]protocol.TerminalState{}
	for _, t := range live {
		byAgent[t.AgentID] = t
	}
	now := s.clk.Now()
	for _, c := range all {
		t, ok := byAgent[c.ID]
		switch {
		case !ok && c.State != Interrupted && c.State != Stopping:
			if err := s.Report(ctx, Report{Character: c.ID, State: Interrupted, Reason: "the machine does not have its terminal", At: now}); err != nil {
				return err
			}
		case ok && State(t.State) != c.State && State(t.State).Valid():
			if err := s.Report(ctx, Report{Character: c.ID, State: State(t.State), Reason: "reconciled with the machine", At: now, Session: t.SessionID, Resync: true}); err != nil {
				return err
			}
		}
	}
	return nil
}
