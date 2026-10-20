//go:build windows

package winsvc

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

// engineQueryAccess is the only right the engine gets over the CLI that
// calls it: PROCESS_QUERY_LIMITED_INFORMATION, which is the creation time of
// the process (the guard against a reused PID). No read of memory, no
// handle to duplicate, no termination.
const engineQueryAccess = windows.PROCESS_QUERY_LIMITED_INFORMATION

// AllowEngineToIdentify is the first thing the `mestre` CLI does on Windows
// (docs/arquitetura/seguranca.md#identidade-de-quem-chama-a-cli). A command
// that Codex runs in the unelevated sandbox has a DACL that names only the
// logon SID of the process that started Codex, so the service of the engine
// cannot open the CLI to read its creation time, and the identification
// would refuse every call. The CLI is our code, and the process has
// WRITE_DAC through that logon SID: it adds to its own DACL one ACE for the
// service SID of the engine of the instance, with PROCESS_QUERY_LIMITED_INFORMATION
// and nothing more. The ACE widens nothing for any other account and dies
// with the process. The caller who fails here is refused by the engine as
// not identified, as before.
func AllowEngineToIdentify(instance string) error {
	name := ServiceName(instance)
	sid, _, _, err := windows.LookupSID("", `NT SERVICE\`+name)
	if err != nil {
		return fmt.Errorf("account of service %s: %w", name, err)
	}
	return allowQueryOfSelf(sid)
}

// allowQueryOfSelf adds the ACE of sid to the DACL of this process.
func allowQueryOfSelf(sid *windows.SID) error {
	h, err := windows.OpenProcess(windows.READ_CONTROL|windows.WRITE_DAC, false, windows.GetCurrentProcessId())
	if err != nil {
		return fmt.Errorf("open this process to change its DACL: %w", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read the DACL of this process: %w", err)
	}
	old, _, err := sd.DACL()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: engineQueryAccess,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, old)
	if err != nil {
		return fmt.Errorf("DACL of this process with the engine: %w", err)
	}
	err = windows.SetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
	runtime.KeepAlive(sd)
	if err != nil {
		return fmt.Errorf("write the DACL of this process: %w", err)
	}
	return nil
}
