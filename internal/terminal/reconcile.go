package terminal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Report is what Reconcile did.
type Report struct {
	// Reconnected are the terminals that were alive; the personagem of each
	// goes on in the state it was in, without passing through interrupted.
	Reconnected []*Terminal
	// Interrupted are the ids of the terminals whose mestre-pty is dead (no
	// process, a reused PID, or no handshake); they go to the resumption.
	Interrupted []string
	// Killed are the ids of the live mestre-pty found with no row in the
	// registry, whose tree was ended.
	Killed []string
}

// Reconcile is what the engine does when it starts (restart, update, crash):
// it ends no terminal, it reads the registry and the socket folder and
// reconciles both ways (docs/arquitetura/terminal.md#volta-do-motor):
//
//  1. in the registry and alive (the PID with the same start identity and
//     the handshake accepted, including the row still without a PID, when
//     the engine fell between starting the mestre-pty and recording it):
//     reconnect, read the screen and the output since the last offset the
//     log confirmed;
//  2. in the registry and dead (no process, a reused PID, or a socket that
//     does not complete the handshake in 30 s): interrupted; the cgroup of
//     the terminal is killed and removed;
//  3. alive and not in the registry (a socket in the folder with no row, for
//     instance after a restored backup): without a row there is no secret
//     and the handshake cannot be valid, so the mestre-pty and its tree are
//     ended. The agent only starts after the row is written, so no work is
//     lost.
func (m *Manager) Reconcile(ctx context.Context) (*Report, error) {
	rows, err := m.reg.list(ctx)
	if err != nil {
		return nil, fmt.Errorf("list terminals: %w", err)
	}
	rep := &Report{}
	var errs []error
	for _, rec := range rows {
		if !rec.Live() {
			continue
		}
		t, err := m.reconcileOne(ctx, rec)
		switch {
		case err != nil:
			errs = append(errs, err)
		case t != nil:
			rep.Reconnected = append(rep.Reconnected, t)
		default:
			rep.Interrupted = append(rep.Interrupted, rec.ID)
		}
	}
	socks, err := filepath.Glob(filepath.Join(m.cfg.SocketDir, "*.sock"))
	if err != nil {
		return rep, err
	}
	for _, s := range socks {
		id := strings.TrimSuffix(filepath.Base(s), ".sock")
		if _, ok := m.liveRow(rows, id); ok {
			continue
		}
		killed, err := m.killOrphan(ctx, id, s)
		if err != nil {
			errs = append(errs, err)
		}
		if killed {
			rep.Killed = append(rep.Killed, id)
		}
	}
	return rep, errors.Join(errs...)
}

// liveRow finds the live row of id.
func (m *Manager) liveRow(rows []Record, id string) (Record, bool) {
	for _, r := range rows {
		if r.ID == id && r.Live() {
			return r, true
		}
	}
	return Record{}, false
}

// reconcileOne handles a live row: a reconnected terminal, or nil when it
// was found dead and marked interrupted.
func (m *Manager) reconcileOne(ctx context.Context, rec Record) (*Terminal, error) {
	sys := m.cfg.System
	if rec.PTY.PID != 0 && !Alive(sys, rec.PTY) {
		m.log.Info("terminal's mestre-pty is gone", "terminal", rec.ID, "pid", rec.PTY.PID)
		return nil, m.markDead(ctx, rec)
	}
	c, err := m.connect(ctx, rec)
	if err != nil {
		// Alive (or not yet recorded) but the handshake did not complete, or
		// the socket is held by someone else: it is not a usable terminal.
		if rec.PTY.PID != 0 {
			_ = m.killTree(ctx, rec.ID, rec.PTY, nil)
		}
		return nil, m.markDead(ctx, rec)
	}
	proc := rec.PTY
	if proc.PID == 0 {
		// The row without PID: the mestre-pty proved the secret, so the PID
		// the socket shows is its own; record it now.
		pid, err := sys.PeerPID(c.Net())
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		start, err := sys.Identify(pid)
		if err != nil {
			_ = c.Close()
			return nil, m.markDead(ctx, rec)
		}
		proc = ProcID{PID: pid, Start: start}
		if err := m.reg.setProcess(ctx, rec.ID, proc); err != nil {
			_ = c.Close()
			return nil, err
		}
		rec.PTY = proc
	}
	t, err := m.newTerminal(rec, c, proc, 0)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	if err := t.attach(ctx); err != nil {
		t.Detach()
		return nil, err
	}
	_ = m.reg.touch(ctx, rec.ID, m.clk.Now())
	return t, nil
}

// markDead ends what is left of a dead terminal and marks it interrupted.
func (m *Manager) markDead(ctx context.Context, rec Record) error {
	if err := m.cfg.Spawner.KillCgroup(ctx, rec.ID); err != nil {
		m.log.Warn("cgroup of a dead terminal", "terminal", rec.ID, "error", err)
	}
	m.revokeAccess(rec.ID, rec.LogonSID)
	_ = os.Remove(rec.SocketPath)
	return m.reg.setStatus(ctx, rec.ID, StatusInterrupted, m.clk.Now())
}

// killOrphan ends the mestre-pty behind a socket that has no live row. The
// peer PID comes from the socket itself; a socket nobody listens on is just
// a stale file.
func (m *Manager) killOrphan(ctx context.Context, id, path string) (bool, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		_ = os.Remove(path)
		return false, nil
	}
	pid, err := m.cfg.System.PeerPID(nc)
	_ = nc.Close()
	if err != nil {
		return false, fmt.Errorf("orphan socket %s: %w", path, err)
	}
	m.audit("terminal without a registry row", id, errors.New("ending its mestre-pty "+fmt.Sprint(pid)))
	var errs []error
	if err := m.cfg.Spawner.KillCgroup(ctx, id); err != nil {
		errs = append(errs, err)
	}
	p := ProcID{PID: pid}
	if start, err := m.cfg.System.Identify(pid); err == nil {
		p.Start = start // the PID just came from the socket: this is its process
	}
	if err := m.killTree(ctx, id, p, nil); err != nil {
		errs = append(errs, err)
	}
	_ = os.Remove(path)
	return true, errors.Join(errs...)
}
