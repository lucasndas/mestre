package scrolls

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/safefile"
)

// copyMode is the mode of the copies in the worktrees: the characters read
// them and the engine rewrites them at every change. Editing a copy by hand
// has no effect, and is overwritten.
const copyMode fs.FileMode = 0o444

// Target is a place that keeps a copy of the scrolls of a table: the worktree
// of the table on a machine where there are characters
// (docs/orquestracao/pergaminhos-e-tomos.md#arquivo-e-índice).
type Target struct {
	Machine, Dir string
}

// Targets says where the copies of the scrolls of a table go.
type Targets interface {
	Of(ctx context.Context, workspace, table string) ([]Target, error)
}

// Copies writes a copy on a machine: the same code for the Citadel's own
// machine and, over the protocol (milestone 5), for a Tower. rel is the path
// with slashes, relative to Target.Dir.
type Copies interface {
	Put(ctx context.Context, t Target, rel string, data []byte) error
	Remove(ctx context.Context, t Target, rel string) error
}

// CharacterTargets gives the worktrees of the tables where characters live: a
// target per machine that runs a living character of the table.
type CharacterTargets struct {
	Registry characters.Registry
	Tables   characters.Tables
}

// Of implements Targets.
func (c CharacterTargets) Of(ctx context.Context, workspace, table string) ([]Target, error) {
	all, err := c.Registry.AliveOf(ctx, workspace)
	if err != nil {
		return nil, err
	}
	var machines []string
	for _, ch := range all {
		if ch.Table == table && ch.Machine != "" && !slices.Contains(machines, ch.Machine) {
			machines = append(machines, ch.Machine)
		}
	}
	slices.Sort(machines)
	out := make([]Target, 0, len(machines))
	for _, m := range machines {
		dir, err := c.Tables.Worktree(ctx, workspace, table, m)
		var down *characters.MachineUnreachableError
		if errors.As(err, &down) {
			continue // a Tower that does not answer must not stop the copies of the others
		}
		if err != nil {
			return nil, fmt.Errorf("worktree of %s on %s: %w", table, m, err)
		}
		out = append(out, Target{Machine: m, Dir: dir})
	}
	return out, nil
}

// LocalCopies writes the copies of the machine it runs on, through
// safefile (the worktree is a folder the agents write); a target on another
// machine is a Tower, reached over the protocol in milestone 5.
type LocalCopies struct{ Machine string }

// Put implements Copies.
func (l LocalCopies) Put(_ context.Context, t Target, rel string, data []byte) error {
	if t.Machine != l.Machine {
		return &characters.MachineUnreachableError{Machine: t.Machine}
	}
	return safefile.Write(filepath.Join(t.Dir, filepath.FromSlash(rel)), data, safefile.Options{Mode: copyMode})
}

// Remove implements Copies.
func (l LocalCopies) Remove(_ context.Context, t Target, rel string) error {
	if t.Machine != l.Machine {
		return &characters.MachineUnreachableError{Machine: t.Machine}
	}
	return safefile.Remove(filepath.Join(t.Dir, filepath.FromSlash(rel)), nil)
}

// copyOut brings the copies of a scroll to what the scroll is now: the engine
// reads the index and the file again, so two changes that come one after the
// other (or a change and a delete) never leave an older copy behind, whatever
// the order the goroutines get here. Copies are serialized among themselves
// by their own lock, never by the one of the service: a Tower is reached over
// the network and a slow one must not stop the reads and writes of the scrolls.
// A copy that cannot be written is logged and made again by Sync; it never
// fails the change.
func (s *Service) copyOut(ctx context.Context, n Note) {
	if s.o.Targets == nil || s.o.Copies == nil {
		return
	}
	s.cmu.Lock()
	defer s.cmu.Unlock()
	s.mu.Lock()
	cur, ok, err := s.reload(ctx, n.Key)
	content := ""
	if err == nil && ok {
		content, err = s.readFile(cur)
	}
	s.mu.Unlock()
	if err != nil {
		s.log.Warn("scrolls: a scroll could not be read to be copied", "scroll", n.Key, "err", err)
		return
	}
	ts, err := s.o.Targets.Of(ctx, n.Workspace, n.Table)
	if err != nil {
		s.log.Warn("scrolls: the places of the copies were not found", "table", n.Table, "err", err)
		return
	}
	for _, t := range ts {
		if !ok { // the scroll is gone: so are its copies
			if err := s.o.Copies.Remove(ctx, t, Rel(n.Table, n.ID)); err != nil {
				s.log.Warn("scrolls: a copy was not removed", "scroll", n.Key, "machine", t.Machine, "err", err)
			}
			continue
		}
		if err := s.o.Copies.Put(ctx, t, Rel(n.Table, n.ID), []byte(content)); err != nil {
			s.log.Warn("scrolls: a copy was not written", "scroll", n.Key, "machine", t.Machine, "err", err)
		}
	}
}

// dropCopies takes the copies of a scroll that was deleted.
func (s *Service) dropCopies(ctx context.Context, n Note) { s.copyOut(ctx, n) }

// Sync writes again the copy of every scroll of a table in every place: when a
// character is born on a machine that has none yet, or a machine is back.
func (s *Service) Sync(ctx context.Context, workspace, table string) error {
	if s.o.Targets == nil || s.o.Copies == nil {
		return nil
	}
	s.mu.Lock()
	ns, err := s.st.OfTable(ctx, workspace, table)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for _, n := range ns {
		s.copyOut(ctx, n)
	}
	return nil
}
