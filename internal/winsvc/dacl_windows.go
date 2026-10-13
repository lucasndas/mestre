//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// terminalSDDL is the default DACL of the token of a terminal, which every
// process of the terminal gets (the mestre-pty sets its own on the Job
// Object, and the launcher gives the mestre-pty a descriptor of its own):
//
//   - SYSTEM, the launcher, everything;
//   - the logon SID of the terminal, everything: the processes of one
//     terminal share one logon session and may open each other;
//   - the engine (NT SERVICE\Mestre), PROCESS_QUERY_LIMITED_INFORMATION and
//     SYNCHRONIZE, to tell who calls it;
//   - OWNER RIGHTS, nothing: the owner of the process is the agent account,
//     which every terminal shares, and without this entry Windows gives the
//     owner READ_CONTROL and WRITE_DAC implicitly, enough to change the
//     DACL and then open the process (the Windows equivalent of
//     kernel.yama.ptrace_scope, docs/arquitetura/seguranca.md).
//
// The user SID of mestre-agente is not in it, so an agent of one terminal
// cannot open a process of another terminal, which has the same user.
const terminalSDDL = "D:(A;;GA;;;SY)(A;;GA;;;%s)(A;;0x%x;;;%s)(A;;;;;OW)"

// terminalDACL builds the default DACL of a terminal. The returned descriptor
// owns the memory of the ACL: keep it alive while the ACL is used.
func terminalDACL(logon, engine *windows.SID) (*windows.ACL, *windows.SECURITY_DESCRIPTOR, error) {
	if logon == nil || engine == nil {
		return nil, nil, errors.New("terminal DACL: the logon SID and the engine SID are both needed")
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf(terminalSDDL, logon, engineProcessMask, engine))
	if err != nil {
		return nil, nil, fmt.Errorf("terminal DACL: %w", err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return nil, nil, fmt.Errorf("terminal DACL: %w", err)
	}
	return acl, sd, nil
}

// tokenDefaultDACL is TOKEN_DEFAULT_DACL.
type tokenDefaultDACL struct{ DefaultDacl *windows.ACL }

// setDefaultDACL replaces the default DACL of the token, the DACL that
// processes created with the token (and their children) get, with the one of
// the terminal.
func setDefaultDACL(t windows.Token, logon, engine *windows.SID) error {
	acl, sd, err := terminalDACL(logon, engine)
	if err != nil {
		return err
	}
	info := tokenDefaultDACL{acl}
	err = windows.SetTokenInformation(t, windows.TokenDefaultDacl, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	runtime.KeepAlive(sd)
	if err != nil {
		return fmt.Errorf("SetTokenInformation(TokenDefaultDacl): %w", err)
	}
	return nil
}
