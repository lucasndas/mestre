//go:build windows

package winsvc

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/lucasndas/mestre/internal/terminal"
)

// logonPrefix starts the text of every logon SID (S-1-5-5-x-y).
const logonPrefix = "S-1-5-5-"

// TerminalAccess is terminal.Access on Windows: the ACE of the logon SID of
// a terminal on the file of the socket of the local channel and on the
// folder that holds it (docs/arquitetura/windows.md#acls-do-socket-da-cli-e-da-pasta-pty).
//
// A command that Codex runs in the unelevated sandbox carries a restricted
// token, and connect() to the socket only passes when the logon SID of the
// terminal that started Codex has read and write access (FILE_GENERIC_READ
// and FILE_GENERIC_WRITE) to the file and to the folder. The ACE is added
// when the terminal starts and taken away when it ends; the SID comes from
// the token the launcher made, and any SID that is not a logon SID is
// refused. The rest of the DACL (SYSTEM, the engine, the agents account) and
// its protection from inheritance are kept as they are.
type TerminalAccess struct {
	// Socket is the path of cli.sock; its folder gets the same ACE.
	Socket string
}

var _ terminal.Access = TerminalAccess{}

func (a TerminalAccess) paths() ([]string, error) {
	if a.Socket == "" || !filepath.IsAbs(a.Socket) {
		return nil, fmt.Errorf("local channel access: the socket path %q is not absolute", a.Socket)
	}
	return []string{a.Socket, filepath.Dir(a.Socket)}, nil
}

// Grant adds the ACE of the logon SID on the socket and on its folder. If
// the second fails, the first is taken back.
func (a TerminalAccess) Grant(logonSID string) error {
	sid, err := parseLogonSID(logonSID)
	if err != nil {
		return err
	}
	ps, err := a.paths()
	if err != nil {
		return err
	}
	for i, p := range ps {
		if err := editDACL(p, func(old *windows.ACL) (*windows.ACL, error) {
			return windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{accessEntry(sid, windows.GRANT_ACCESS)}, old)
		}); err != nil {
			for _, done := range ps[:i] {
				_ = revokeOne(done, sid)
			}
			return fmt.Errorf("grant %s on %s: %w", logonSID, p, err)
		}
	}
	return nil
}

// Revoke takes the ACE of the logon SID away from the socket and its folder;
// it tries both whatever the first answers.
func (a TerminalAccess) Revoke(logonSID string) error {
	sid, err := parseLogonSID(logonSID)
	if err != nil {
		return err
	}
	ps, err := a.paths()
	if err != nil {
		return err
	}
	var first error
	for _, p := range ps {
		if err := revokeOne(p, sid); err != nil && first == nil {
			first = fmt.Errorf("revoke %s on %s: %w", logonSID, p, err)
		}
	}
	return first
}

// Only takes away the ACE of every logon SID not in keep.
func (a TerminalAccess) Only(keep []string) error {
	for _, k := range keep {
		if _, err := parseLogonSID(k); err != nil {
			return err
		}
	}
	ps, err := a.paths()
	if err != nil {
		return err
	}
	var first error
	for _, p := range ps {
		if err := editDACL(p, func(old *windows.ACL) (*windows.ACL, error) {
			var drop []windows.EXPLICIT_ACCESS
			for i := uint32(0); old != nil && i < uint32(old.AceCount); i++ {
				var ace *windows.ACCESS_ALLOWED_ACE
				if err := windows.GetAce(old, i, &ace); err != nil {
					return nil, err
				}
				s := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
				if strings.HasPrefix(s, logonPrefix) && !sliceHas(keep, s) {
					sid, err := windows.StringToSid(s)
					if err != nil {
						return nil, err
					}
					drop = append(drop, accessEntry(sid, windows.REVOKE_ACCESS))
				}
			}
			if len(drop) == 0 {
				return old, nil
			}
			return windows.ACLFromEntries(drop, old)
		}); err != nil && first == nil {
			first = fmt.Errorf("sweep the grants on %s: %w", p, err)
		}
	}
	return first
}

func sliceHas(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func revokeOne(path string, sid *windows.SID) error {
	return editDACL(path, func(old *windows.ACL) (*windows.ACL, error) {
		return windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{accessEntry(sid, windows.REVOKE_ACCESS)}, old)
	})
}

// parseLogonSID validates the text and returns the SID.
func parseLogonSID(s string) (*windows.SID, error) {
	if err := terminal.ValidLogonSID(s); err != nil {
		return nil, err
	}
	sid, err := windows.StringToSid(s)
	if err != nil {
		return nil, fmt.Errorf("logon SID %s: %w", s, err)
	}
	return sid, nil
}

// accessEntry is the ACE of a logon SID: read and write to this object only,
// nothing inherited.
func accessEntry(sid *windows.SID, mode windows.ACCESS_MODE) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: socketClientAccess,
		AccessMode:        mode,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

// editDACL reads the DACL of path, lets edit make the new one from it, and
// writes it back keeping the protection from inheritance it had.
func editDACL(path string, edit func(old *windows.ACL) (*windows.ACL, error)) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	old, _, err := sd.DACL()
	if err != nil {
		return err
	}
	ctl, _, _ := sd.Control()
	acl, err := edit(old)
	if err != nil {
		return err
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if ctl&windows.SE_DACL_PROTECTED != 0 {
		info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	} else {
		info |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info, nil, nil, acl, nil)
	runtime.KeepAlive(sd)
	return err
}
