//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/mestre-ia/mestre/internal/terminal"
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

// aclMu serializes every change of these DACLs: each one reads the DACL,
// edits it and writes it back, and the terminals end and start on their own
// goroutines, so two changes at once would lose one.
var aclMu sync.Mutex

// folderAccess is what the folder of the socket gets: read, list and
// traverse, and nothing that creates or deletes there. The measurement that
// asked for FILE_GENERIC_WRITE on the folder too would let a terminal create
// the name of cli.sock in run\ while the engine is stopped (terminals outlive
// the engine) and answer the CLI and the hooks of every other character, so
// the folder gets the read side only; whether connect() still passes is on
// the list of the real Windows round (docs/provedores/codex-no-windows.md).
const folderAccess = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_EXECUTE

func (a TerminalAccess) paths() ([]string, error) {
	if a.Socket == "" || !filepath.IsAbs(a.Socket) {
		return nil, fmt.Errorf("local channel access: the socket path %q is not absolute", a.Socket)
	}
	return []string{a.Socket, filepath.Dir(a.Socket)}, nil
}

// maskOf is the right the ACE has on the i-th path of paths(): read and
// write on the socket file, the read side on the folder.
func maskOf(i int) uint32 {
	if i == 0 {
		return socketClientAccess
	}
	return folderAccess
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
	aclMu.Lock()
	defer aclMu.Unlock()
	for i, p := range ps {
		if err := editDACL(p, func(old *windows.ACL) (*windows.ACL, error) {
			return windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{accessEntry(sid, windows.GRANT_ACCESS, maskOf(i))}, old)
		}); err != nil {
			for _, done := range ps[:i] {
				_ = revokeOne(done, sid, maskOf(0))
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
	aclMu.Lock()
	defer aclMu.Unlock()
	var first error
	for i, p := range ps {
		if err := revokeOne(p, sid, maskOf(i)); err != nil && first == nil {
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
	aclMu.Lock()
	defer aclMu.Unlock()
	var first error
	for i, p := range ps {
		if err := editDACL(p, func(old *windows.ACL) (*windows.ACL, error) {
			var drop []windows.EXPLICIT_ACCESS
			for k := uint32(0); old != nil && k < uint32(old.AceCount); k++ {
				var ace *windows.ACCESS_ALLOWED_ACE
				if err := windows.GetAce(old, k, &ace); err != nil {
					return nil, err
				}
				if t := ace.Header.AceType; t != windows.ACCESS_ALLOWED_ACE_TYPE && t != windows.ACCESS_DENIED_ACE_TYPE {
					continue // object and callback ACEs lay the SID elsewhere
				}
				s := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
				if strings.HasPrefix(s, logonPrefix) && !sliceHas(keep, s) {
					sid, err := windows.StringToSid(s)
					if err != nil {
						return nil, err
					}
					drop = append(drop, accessEntry(sid, windows.REVOKE_ACCESS, maskOf(i)))
				}
			}
			if len(drop) == 0 {
				return old, nil
			}
			return windows.ACLFromEntries(drop, old)
		}); err != nil && first == nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			// A socket or folder that is not there has no grant to sweep
			// (the socket after a clean stop of the engine).
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

func revokeOne(path string, sid *windows.SID, mask uint32) error {
	return editDACL(path, func(old *windows.ACL) (*windows.ACL, error) {
		return windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{accessEntry(sid, windows.REVOKE_ACCESS, mask)}, old)
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
func accessEntry(sid *windows.SID, mode windows.ACCESS_MODE, mask uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.ACCESS_MASK(mask),
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
