package terminal

import (
	"context"
	"fmt"
	"regexp"
	"slices"
)

// Access is what the engine does for the local channel while a terminal
// lives (Windows; docs/arquitetura/windows.md#acls-do-socket-da-cli-e-da-pasta-pty).
// A command that Codex runs in its unelevated sandbox carries a token that
// only the logon SID of the terminal vouches for, so the socket of the CLI
// and its folder get an ACE for that SID, and the ACE leaves when the
// terminal ends. The SID is the one the launcher read from the token it
// created, never anything the agent wrote.
type Access interface {
	// Grant adds the ACE of the logon SID (idempotent).
	Grant(logonSID string) error
	// Revoke takes it away; a SID without an ACE is not an error.
	Revoke(logonSID string) error
	// Only takes away the ACE of every other logon SID: what a dead engine
	// left behind.
	Only(keep []string) error
}

// logonSIDPattern is the shape of the logon SID of a session: S-1-5-5-X-Y.
var logonSIDPattern = regexp.MustCompile(`^S-1-5-5-[0-9]{1,10}-[0-9]{1,10}$`)

// ValidLogonSID reports whether s is the text of a logon SID. Anything else
// (another authority, a user SID, a well-known group such as Everyone,
// spaces, SDDL pieces) is refused, so a bad answer of the launcher can never
// become an ACE for a wider group.
func ValidLogonSID(s string) error {
	if !logonSIDPattern.MatchString(s) {
		return fmt.Errorf("%q is not a logon SID (S-1-5-5-<x>-<y>)", s)
	}
	return nil
}

// grantAccess validates the logon SID of a terminal just started, grants it
// and records it in the row, so the engine can take the grant away at the
// end and put it back when it restarts. Any failure is an error: a terminal
// without the grant would have a CLI that cannot reach the engine, and one
// with a doubtful grant is worse. rec gets the SID before the grant is made,
// so a failed start revokes what was done.
func (m *Manager) grantAccess(ctx context.Context, rec *Record, sid string) error {
	if m.cfg.Access == nil {
		return nil
	}
	if err := ValidLogonSID(sid); err != nil {
		return fmt.Errorf("terminal %s: the launcher gave no usable logon SID: %w", rec.ID, err)
	}
	rec.LogonSID = sid
	if err := m.cfg.Access.Grant(sid); err != nil {
		return fmt.Errorf("terminal %s: grant the local channel: %w", rec.ID, err)
	}
	return m.reg.setLogon(ctx, rec.ID, sid)
}

// revokeAccess takes the grant of a terminal away. It never fails the end of
// the terminal: the process is gone, and the next start of the engine sweeps
// what is left (syncAccess).
func (m *Manager) revokeAccess(id, sid string) {
	if m.cfg.Access == nil || sid == "" {
		return
	}
	if err := m.cfg.Access.Revoke(sid); err != nil {
		m.log.Warn("grant of the local channel not taken away", "terminal", id, "error", err.Error())
	}
}

// syncAccess is the engine at start: the live terminals get their grant
// again (the socket was made anew, with the base ACL) and every other grant
// goes. A live terminal without a recorded SID keeps no grant: its CLI is
// refused, which is the safe side.
func (m *Manager) syncAccess(rows []Record) {
	if m.cfg.Access == nil {
		return
	}
	var keep []string
	for _, r := range rows {
		if !r.Live() {
			continue
		}
		if r.LogonSID == "" || ValidLogonSID(r.LogonSID) != nil {
			m.log.Warn("live terminal without a usable logon SID: its CLI will not reach the engine", "terminal", r.ID)
			continue
		}
		if err := m.cfg.Access.Grant(r.LogonSID); err != nil {
			m.log.Warn("grant of the local channel not put back", "terminal", r.ID, "error", err.Error())
			continue
		}
		keep = append(keep, r.LogonSID)
	}
	if err := m.cfg.Access.Only(slices.Clone(keep)); err != nil {
		m.log.Warn("grants of dead terminals not taken away", "error", err.Error())
	}
}
