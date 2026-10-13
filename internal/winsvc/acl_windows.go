//go:build windows

package winsvc

import (
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// The ACLs of what the engine creates for the agents on Windows
// (docs/arquitetura/windows.md#acls-do-socket-da-cli-e-da-pasta-pty): the
// socket of the local channel and the folder of the sockets of the
// mestre-pty. The installer applies the same ACLs at install time (M7,
// docs/desenvolvimento/ordem-de-implementacao.md); the engine applies them
// again every time it creates the socket or prepares a terminal, so a folder
// that was restored or made by hand is put right. The SIDs are parameters:
// the tests give the user of the test, the engine gives the real accounts.

const (
	fileAddFile = 0x2     // FILE_ADD_FILE: create a file in a folder
	readControl = 0x20000 // READ_CONTROL

	// socketClientAccess is "read and write" on the socket file, which is
	// what connecting to it takes.
	socketClientAccess = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE
)

// cliSocketSDDL is the DACL of cli.sock: the engine (its owner) and SYSTEM
// everything, mestre-agente read and write. Nothing is inherited from the
// folder (the descriptor is protected).
func cliSocketSDDL(owner, agents *windows.SID) string {
	return fmt.Sprintf("D:P(A;;FA;;;SY)(A;;FA;;;%s)(A;;0x%x;;;%s)", owner, socketClientAccess, agents)
}

// ptyDirSDDL is the DACL of the folder of the sockets of the mestre-pty:
// SYSTEM and the engine everything, inherited by the sockets; OWNER RIGHTS
// (the creator of the socket, mestre-agente) only READ_CONTROL, inherited,
// so the account that made a socket can neither change its ACL nor connect
// to it; and mestre-agente only the right to add a file, to this folder and
// not inherited: it can create its socket and nothing else, and not even
// list the folder.
func ptyDirSDDL(engine, agents *windows.SID) string {
	return fmt.Sprintf("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;%s)(A;OICI;0x%x;;;OW)(A;;0x%x;;;%s)", engine, readControl, fileAddFile, agents)
}

// accountSID resolves the SID of an account by name (mestre-agente).
func accountSID(name string) (*windows.SID, error) {
	sid, _, _, err := windows.LookupSID("", name)
	if err != nil {
		return nil, fmt.Errorf("account %s: %w", name, err)
	}
	return sid, nil
}

// AgentAccountName is the account the agents of an instance run with:
// mestre-agente, or mestre-agente-<inst> for the other instances
// (docs/arquitetura/componentes.md#instâncias).
func AgentAccountName(instance string) string {
	if instance == "" || instance == "principal" {
		return "mestre-agente"
	}
	return "mestre-agente-" + instance
}

// agentSID is AgentSID when set, else the SID of AgentAccount.
func agentSID(sid *windows.SID, account string) (*windows.SID, error) {
	if sid != nil {
		return sid, nil
	}
	if account == "" {
		return nil, fmt.Errorf("no agent account configured")
	}
	return accountSID(account)
}

// CLIAccess is the access of the socket of the local channel
// (localchan.Access): the user of this process, which is the engine, owns it
// and has full access, SYSTEM too, and the agent account reads and writes.
type CLIAccess struct {
	// AgentAccount is the name of the account of the agents.
	AgentAccount string
	// AgentSID, when set, is used instead of looking AgentAccount up.
	AgentSID *windows.SID
	// OwnerSID is the engine; the user of this process when nil.
	OwnerSID *windows.SID
}

// Apply sets the owner and the ACL of the socket at path.
func (a CLIAccess) Apply(path string) error {
	agents, err := agentSID(a.AgentSID, a.AgentAccount)
	if err != nil {
		return err
	}
	owner := a.OwnerSID
	if owner == nil {
		if owner, err = currentUserSID(); err != nil {
			return err
		}
	}
	return setFileSecurity(path, cliSocketSDDL(owner, agents), owner)
}

// PTYDirACL is the ACL of the folder of the sockets of the mestre-pty.
type PTYDirACL struct {
	// AgentAccount is the name of the account of the agents.
	AgentAccount string
	// AgentSID, when set, is used instead of looking AgentAccount up.
	AgentSID *windows.SID
	// EngineSID is the engine; the user of this process when nil.
	EngineSID *windows.SID
}

// Ensure creates the folder if it does not exist and sets its ACL. Windows
// has no folder mode: 0700 of os.MkdirAll means nothing here.
func (a PTYDirACL) Ensure(dir string) error {
	agents, err := agentSID(a.AgentSID, a.AgentAccount)
	if err != nil {
		return err
	}
	engine := a.EngineSID
	if engine == nil {
		if engine, err = currentUserSID(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return setFileSecurity(dir, ptyDirSDDL(engine, agents), nil)
}

// setFileSecurity replaces the DACL of path with the one of sddl, protected
// (nothing inherited from the folder above), and the owner when given.
func setFileSecurity(path, sddl string, owner *windows.SID) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("ACL %s: %w", sddl, err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("ACL %s: %w", sddl, err)
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if owner != nil {
		info |= windows.OWNER_SECURITY_INFORMATION
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info, owner, nil, acl, nil)
	runtime.KeepAlive(sd)
	if err != nil {
		return fmt.Errorf("set the ACL of %s: %w", path, err)
	}
	return nil
}
