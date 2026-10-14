package providers

import (
	"fmt"
	"slices"
)

// Base is the base of a terminal already resolved for the machine
// (docs/orquestracao/bases-e-fichas.md#base): the command and arguments of
// the machine's system, its extra variables and the start folder.
type Base struct {
	Name    string
	Command string
	Args    []string
	Env     []string // KEY=VALUE
	Dir     string
}

// LaunchRequest is what BuildLaunch receives.
type LaunchRequest struct {
	Base Base
	// OS is the system of the machine ("linux", "windows").
	OS string
	// AgentID is the character (MESTRE_AGENT_ID); TableID its table
	// (MESTRE_MESA).
	AgentID, TableID string
	// ResumeSessionID is the saved session to resume; "" starts a new one.
	ResumeSessionID string
	// NewSessionID is the id the engine generated for a new Claude Code
	// session (--session-id); the other providers report theirs in the
	// hooks.
	NewSessionID string
	// ProviderArgs are the flags the engine adds for the provider: the
	// Mestre layer (DeliverMestreLayer) and the native rules of the class.
	// The same ones go on a resume.
	ProviderArgs []string
	// Env are the variables the engine adds: the account profile's and the
	// rest of MESTRE_*.
	Env []string
	// Profile is the provider folder of the account profile; the
	// adapter puts it in the provider's variable (CODEX_HOME).
	Profile string
	// Model is the class's preferred model, if it has one.
	Model string
}

// Launch is the command of a character's terminal.
type Launch struct {
	Path string
	Args []string
	Env  []string
	Dir  string
	// SessionID is the session the command starts or resumes, when known
	// before the first hook (Claude Code; a resume).
	SessionID string
	// Resumed is true for the resume command of the table of
	// docs/provedores/adaptador.md#retomada-de-sessão.
	Resumed bool
}

// newLaunch fills what every adapter does the same way: base command, the
// variables of the base, of the engine and the two the hooks read.
func newLaunch(req LaunchRequest) (Launch, error) {
	if req.Base.Command == "" {
		return Launch{}, fmt.Errorf("base %q: no command", req.Base.Name)
	}
	env := slices.Concat(req.Base.Env, req.Env)
	if req.AgentID != "" {
		env = append(env, "MESTRE_AGENT_ID="+req.AgentID)
	}
	if req.TableID != "" {
		env = append(env, "MESTRE_MESA="+req.TableID)
	}
	return Launch{Path: req.Base.Command, Env: env, Dir: req.Base.Dir}, nil
}

// refuseLatest refuses the "most recent session" options: several
// characters share a folder, so only a resume by id is safe.
func refuseLatest(p ID, args []string, latest ...string) error {
	for _, a := range args {
		if slices.Contains(latest, a) {
			return fmt.Errorf("%s: option %s resumes the most recent session of the folder; the Mestre resumes by id", p, a)
		}
	}
	return nil
}
