//go:build windows

package winsvc

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"

	"github.com/lucasndas/mestre/internal/winsvc/logonsweep"
)

// Session is the logon of the agent account for one terminal: what the
// mestre-pty is created with, and the duty to release it when the mestre-pty
// ends.
type Session struct {
	// Token is the primary token the mestre-pty is created with; zero means
	// the token of the launcher itself (a launcher that is not a service,
	// in the tests).
	Token windows.Token
	// Env is the environment block of the account (profile variables).
	Env []string
	// Logon is the logon session of the token, when the launcher made it for
	// this terminal alone (a batch logon). Zero for a session that shares the
	// logon session of the launcher: logonsweep.Allowed refuses it, and the
	// mestre-pty is not told it owns the session.
	Logon logonsweep.Session
	// Release unloads the profile, takes back what was granted on the window
	// station and desktop, and closes the token, so the logon session ends.
	// It is safe to call more than once.
	Release func()
}

// Account is the account the mestre-pty runs as. The launcher asks for one
// Session per terminal, so each terminal has its own logon session.
type Account interface {
	// Logon makes the session of terminal id.
	Logon(id string) (*Session, error)
	// Adopt rebuilds the session of a mestre-pty that survived a restart of
	// the launcher, from the process itself, to release it when it ends.
	Adopt(id string, pid int) (*Session, error)
}

// ErrSelfAccountInService is the refusal of SelfAccount in a service.
var ErrSelfAccountInService = errors.New("winsvc: the account of the launcher itself is refused in a service: the agents would run as the account of the service (LocalSystem)")

// SelfAccount is the "agent account" of a launcher that is not a service,
// as in the full suite on the CI: the account of the launcher process
// itself, with its own token and environment and no profile to load. The
// whole path of the launcher runs, except the logon; what the logon needs
// (a local account and its password, LoadUserProfile, the window station)
// belongs to the real-account tests (internal/realaccount). In a service it
// refuses to work (the account of the service is LocalSystem, and the agents
// would run as SYSTEM) and NewLauncher refuses it.
type SelfAccount struct {
	// Released is called when a session is released (once per session).
	Released func(id string)
	// service says whether this process is a service; nil asks the system (the
	// tests of the package say it themselves).
	service func() bool
}

var _ Account = SelfAccount{}

// inService says whether this process is a service or runs as an identity that
// only a service has. svc.IsWindowsService looks at the parent of the process
// (the service manager, in session 0), which a LocalSystem process that
// something else started (psexec -s, a task as SYSTEM, a wrapper of services)
// does not have, and the agents would run as SYSTEM all the same: the identity of
// the token counts too. When the system cannot say, it is taken as one: the
// account is refused.
func (a SelfAccount) inService() bool {
	is := false
	if a.service != nil {
		is = a.service()
	} else {
		var err error
		if is, err = svc.IsWindowsService(); err != nil {
			return true
		}
	}
	if is {
		return true
	}
	sid, err := currentUserSID()
	return err != nil || isServiceSID(sid.String())
}

// isServiceSID says whether the SID (in its text form) is of an identity that
// only a service runs as: LocalSystem, LocalService, NetworkService or a service
// SID (NT SERVICE\...).
func isServiceSID(sid string) bool {
	switch sid {
	case "S-1-5-18", "S-1-5-19", "S-1-5-20":
		return true
	}
	return strings.HasPrefix(sid, "S-1-5-80-")
}

// selfInService says whether a is the SelfAccount, by value or by pointer, of a
// process that is a service.
func selfInService(a Account) bool {
	switch s := a.(type) {
	case SelfAccount:
		return s.inService()
	case *SelfAccount:
		return s != nil && s.inService()
	}
	return false
}

func (a SelfAccount) session(id string) *Session {
	var once sync.Once
	return &Session{Env: os.Environ(), Release: func() {
		once.Do(func() {
			if a.Released != nil {
				a.Released(id)
			}
		})
	}}
}

func (a SelfAccount) Logon(id string) (*Session, error) {
	if a.inService() {
		return nil, ErrSelfAccountInService
	}
	return a.session(id), nil
}

func (a SelfAccount) Adopt(id string, _ int) (*Session, error) {
	if a.inService() {
		return nil, ErrSelfAccountInService
	}
	return a.session(id), nil
}

// AgentAccount is mestre-agente, the local account without interactive
// logon of the agents (docs/arquitetura/windows.md#conta-dos-agentes). The
// launcher runs as LocalSystem, the only one that may load the profile of
// another account.
type AgentAccount struct {
	// User is the account name (mestre-agente, or mestre-agente-<inst>).
	User string
	// Password returns the password the installation kept (DPAPI, machine
	// scope, readable by SYSTEM only). It is read for each logon and never
	// kept.
	Password func() (string, error)
	// Engine is the SID of the engine's account (NT SERVICE\Mestre): it
	// gets query and wait rights over every process of the terminal, through
	// the default DACL of the token, so the engine can tell who calls it. The
	// account itself gets nothing in that DACL, only the logon session of the
	// terminal does (see terminalDACL).
	Engine *windows.SID
}

var _ Account = (*AgentAccount)(nil)

// engineProcessMask is what the engine gets over the processes of a
// terminal: PROCESS_QUERY_LIMITED_INFORMATION and SYNCHRONIZE.
const engineProcessMask = windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.SYNCHRONIZE

// Logon does the batch logon (one logon session per terminal), loads the
// profile, builds the environment block, sets the default DACL of the token
// (terminalDACL) and gives the logon SID access to the window station and the
// desktop of the service, which the process needs to start.
func (a *AgentAccount) Logon(id string) (*Session, error) {
	pw, err := a.Password()
	if err != nil {
		return nil, fmt.Errorf("password of %s: %w", a.User, err)
	}
	t, err := logonUser(a.User, ".", pw, logon32LogonBatch)
	if err != nil {
		return nil, err
	}
	s, err := a.prepare(t, id)
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	return s, nil
}

// Adopt takes the token from the running mestre-pty and loads the profile
// again, so that releasing it balances; the window station and desktop keep
// the entries made at its creation, and are revoked at its end. Not
// exercised by the CI (it needs the real account).
func (a *AgentAccount) Adopt(id string, pid int) (*Session, error) {
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return nil, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(p) }()
	var pt windows.Token
	if err := windows.OpenProcessToken(p, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &pt); err != nil {
		return nil, fmt.Errorf("token of process %d: %w", pid, err)
	}
	defer pt.Close()
	var t windows.Token
	if err := windows.DuplicateTokenEx(pt, windows.MAXIMUM_ALLOWED, nil, windows.SecurityImpersonation, windows.TokenPrimary, &t); err != nil {
		return nil, fmt.Errorf("duplicate the token of process %d: %w", pid, err)
	}
	s, err := a.prepare(t, id)
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	return s, nil
}

// prepare finishes the session of the token t, which it now owns (on error
// the caller closes it).
func (a *AgentAccount) prepare(t windows.Token, id string) (*Session, error) {
	var (
		prof    windows.Handle
		logon   *windows.SID
		winsta  = processWindowStation()
		desktop = threadDesktop()
		once    sync.Once
	)
	release := func() {
		once.Do(func() {
			if logon != nil {
				_ = setObjectACE(winsta, logon, winstaAllAccess, false)
				_ = setObjectACE(desktop, logon, desktopAllAccess, false)
			}
			if prof != 0 {
				_ = unloadUserProfile(t, prof)
			}
			_ = t.Close()
		})
	}
	fail := func(err error) (*Session, error) {
		// The caller closes t when this returns an error; release must not.
		once.Do(func() {
			if logon != nil {
				_ = setObjectACE(winsta, logon, winstaAllAccess, false)
				_ = setObjectACE(desktop, logon, desktopAllAccess, false)
			}
			if prof != 0 {
				_ = unloadUserProfile(t, prof)
			}
		})
		return nil, fmt.Errorf("terminal %s: %w", id, err)
	}
	var err error
	if prof, err = loadUserProfile(t, a.User); err != nil {
		return fail(err)
	}
	env, err := tokenEnvironment(t)
	if err != nil {
		return fail(fmt.Errorf("CreateEnvironmentBlock: %w", err))
	}
	if a.Engine == nil {
		return fail(errors.New("no engine account configured"))
	}
	if logon, err = logonSID(t); err != nil {
		return fail(err)
	}
	if err = setDefaultDACL(t, logon, a.Engine); err != nil {
		return fail(err)
	}
	if err = setObjectACE(winsta, logon, winstaAllAccess, true); err != nil {
		return fail(err)
	}
	if err = setObjectACE(desktop, logon, desktopAllAccess, true); err != nil {
		return fail(err)
	}
	logonSession, err := logonsweep.SessionOfToken(t)
	if err != nil {
		return fail(err)
	}
	return &Session{Token: t, Env: env, Logon: logonSession, Release: release}, nil
}
