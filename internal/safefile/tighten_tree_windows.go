//go:build windows

package safefile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

// rulesWriteRights are the rights the agent account is denied on regras/: write data and
// append (create files and folders), write extended and basic attributes, delete a child,
// delete, and change the ACL or the owner. Read, list and execute stay as the ACL gave them.
const rulesWriteRights = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA |
	0x40 /* FILE_DELETE_CHILD */ | windows.FILE_WRITE_ATTRIBUTES |
	windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER

var (
	rulesMu      sync.Mutex
	rulesAccount string
)

// SetRulesAgentAccount names the local account of the agents (mestre-agente, or
// mestre-agente-<inst>) that TightenRules locks out of regras/. The engine sets it at start;
// empty (the default, and the tests) leaves the ACL alone.
func SetRulesAgentAccount(name string) {
	rulesMu.Lock()
	rulesAccount = name
	rulesMu.Unlock()
}

// TightenRules sets, on .mestre\regras\, an ACL that denies the write to the agent account
// (docs/aprovacoes/arquivo-de-politica.md): a DENY entry inherited by everything under it,
// added to the ACL the folder has (the engine keeps its access). It is idempotent. A missing
// regras\ is no error, and with no account set it does nothing.
func TightenRules(mestre string) error {
	rulesMu.Lock()
	name := rulesAccount
	rulesMu.Unlock()
	if name == "" {
		return nil
	}
	dir := filepath.Join(mestre, "regras")
	if _, err := os.Lstat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // nothing to lock, and no account lookup for it
		}
		return err
	}
	sid, err := cachedAccountSID(name)
	if err != nil {
		return err
	}
	return DenyWrite(dir, sid)
}

var sidCache sync.Map // account name -> *windows.SID

// cachedAccountSID resolves the account once: the lookup asks the system, and the rules are
// tightened on every discovery of a campaign and every opening of a rules screen.
func cachedAccountSID(name string) (*windows.SID, error) {
	if v, ok := sidCache.Load(name); ok {
		return v.(*windows.SID), nil
	}
	sid, err := localAccountSID(name)
	if err != nil {
		return nil, err
	}
	sidCache.Store(name, sid)
	return sid, nil
}

// DenyWrite adds to the folder an ACE that denies the write rights to sid, inherited by the
// files and folders under it, and has the system propagate it to what already exists. A
// missing folder is no error.
func DenyWrite(dir string, sid *windows.SID) error {
	st, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read the ACL of %s: %w", dir, err)
	}
	old, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read the ACL of %s: %w", dir, err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: rulesWriteRights,
		AccessMode:        windows.DENY_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, old)
	if err != nil {
		return fmt.Errorf("build the ACL of %s: %w", dir, err)
	}
	err = windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
	runtime.KeepAlive(sd)
	if err != nil {
		return fmt.Errorf("set the ACL of %s: %w", dir, err)
	}
	return nil
}

// localAccountSID resolves a local user as <computer>\<name>, so a domain account of the same
// name cannot stand for it.
func localAccountSID(name string) (*windows.SID, error) {
	if strings.ContainsAny(name, `\@/`) {
		return nil, fmt.Errorf("account %q: only the name of a local account", name)
	}
	host, err := windows.ComputerName()
	if err != nil {
		return nil, fmt.Errorf("account %s: %w", name, err)
	}
	sid, domain, kind, err := windows.LookupSID("", host+`\`+name)
	if err != nil {
		return nil, fmt.Errorf("account %s: %w", name, err)
	}
	if kind != windows.SidTypeUser || !strings.EqualFold(domain, host) {
		return nil, fmt.Errorf("account %s is not a user of this computer", name)
	}
	return sid, nil
}
