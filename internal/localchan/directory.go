package localchan

import (
	"context"
	"errors"
	"sync"

	"github.com/lucasndas/mestre/internal/terminal"
)

// Directory is the Terminals and JobAsker of the engine, built on the
// terminals it holds (internal/terminal): the registry row says whose
// terminal it is, the mestre-pty process says which processes are its own
// (ByPTY) and, on Windows, holds the Job Object (InJob).
//
// A terminal enters the directory before its agent starts (Starting, the
// OnStarting hook of the terminal.Manager): an agent that calls `mestre` in
// its first instant, such as a hook of the start of the session, must
// already be identifiable. Terminals the engine finds again when it starts
// enter with Add.
type Directory struct {
	sys terminal.System

	mu   sync.Mutex
	byID map[string]entry
}

type entry struct {
	id, characterID string
	pty             terminal.ProcID
	client          *terminal.Client
}

var (
	_ Terminals = (*Directory)(nil)
	_ JobAsker  = (*Directory)(nil)
)

// NewDirectory returns an empty directory; sys defaults to terminal.NewSystem().
func NewDirectory(sys terminal.System) *Directory {
	if sys == nil {
		sys = terminal.NewSystem()
	}
	return &Directory{sys: sys, byID: map[string]entry{}}
}

func (d *Directory) put(e entry) {
	d.mu.Lock()
	d.byID[e.id] = e
	d.mu.Unlock()
}

// Starting registers a terminal whose agent is about to start; it is the
// OnStarting of the terminal.Manager.
func (d *Directory) Starting(s terminal.Starting) {
	d.put(entry{id: s.Record.ID, characterID: s.Record.CharacterID, pty: s.Record.PTY, client: s.Client})
}

// Add registers a live terminal the engine has (one it reconnected to when
// it started).
func (d *Directory) Add(t *terminal.Terminal) {
	d.put(entry{id: t.ID(), characterID: t.Record().CharacterID, pty: t.PTY(), client: t.Client()})
}

// Remove forgets the terminal: when it is stopped, or failed to start (the
// OnDiscarded of the terminal.Manager).
func (d *Directory) Remove(id string) {
	d.mu.Lock()
	delete(d.byID, id)
	d.mu.Unlock()
}

func (e entry) info() TerminalInfo { return TerminalInfo{ID: e.id, CharacterID: e.characterID} }

// Terminal implements Terminals.
func (d *Directory) Terminal(id string) (TerminalInfo, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.byID[id]
	return e.info(), ok
}

// OfCharacter implements Terminals.
func (d *Directory) OfCharacter(characterID string) []TerminalInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []TerminalInfo
	for _, e := range d.byID {
		if e.characterID == characterID {
			out = append(out, e.info())
		}
	}
	return out
}

// ByPTY implements Terminals: pid must be the mestre-pty recorded for the
// terminal, with the same start identity.
func (d *Directory) ByPTY(pid int) (TerminalInfo, bool) {
	d.mu.Lock()
	var found entry
	var ok bool
	for _, e := range d.byID {
		if e.pty.PID == pid {
			found, ok = e, true
			break
		}
	}
	d.mu.Unlock()
	if !ok || !terminal.Alive(d.sys, found.pty) {
		return TerminalInfo{}, false
	}
	return found.info(), true
}

// InJob implements JobAsker: it asks the mestre-pty of the terminal.
func (d *Directory) InJob(ctx context.Context, terminalID string, p ProcRef) (bool, error) {
	d.mu.Lock()
	e, ok := d.byID[terminalID]
	d.mu.Unlock()
	if !ok {
		return false, errors.New("terminal " + terminalID + " is not registered")
	}
	return e.client.InJob(ctx, p.PID, p.Start)
}
