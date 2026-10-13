package realaccount

import (
	"errors"
	"fmt"
)

// EnvDisposable names the variable that says the machine is disposable. The
// real-account tests create accounts, units and services and change system
// settings, so they refuse to run, and the tool refuses to prepare anything,
// unless it is exactly "1". The CI sets it on the GitHub-hosted runners, which
// are thrown away after each job; nobody sets it in a profile.
const EnvDisposable = "MESTRE_REAL_ACCOUNT"

// EnvRole is set to "engine" by the tool on the process it runs the tests in;
// without it the test binary was started by hand and refuses.
const EnvRole = "MESTRE_RA_ROLE"

// Names shared by the tool that prepares the machine and by the tests. The
// instance is not the main one, so nothing here can be mistaken for a real
// installation (docs/arquitetura/componentes.md#instâncias).
const (
	// Instance is the name of the instance the tests use.
	Instance = "ci"
	// LinuxEngineUser is the account of the engine on Linux (mestre).
	LinuxEngineUser = "mestre-ci"
	// LinuxAgentUser is the account of the agents on Linux
	// (mestre-agente-<inst>).
	LinuxAgentUser = "mestre-agente-ci"
	// WindowsAgentUser is the local account of the agents on Windows.
	WindowsAgentUser = "mestre-agente-ci"
	// LinuxStage is where the tool puts what the tests run (read-only for
	// every account); WindowsStage is the same on Windows.
	LinuxStage   = "/opt/mestre-ci"
	WindowsStage = `C:\ProgramData\MestreCI`
	// PTYVersion is the version of the mestre-pty the tool installs.
	PTYVersion = "1.0.0"
)

// LinuxLaunchers are the instances of the launcher unit the tool starts on
// Linux: the one of the installation (delegated cgroup), one without a
// delegated cgroup (LIN-11 of the checklist failed) and one the tests restart.
var LinuxLaunchers = []string{Instance, "nocg", "rst"}

// LinuxEngineUnit is the transient unit the tests run in, as the engine: the
// launcher accepts the engine only from this cgroup.
const LinuxEngineUnit = "mestre-ci-engine.service"

// CheckDisposable returns an error unless the machine was declared disposable.
func CheckDisposable(getenv func(string) string) error {
	if getenv(EnvDisposable) != "1" {
		return errors.New(refusal)
	}
	return nil
}

var refusal = fmt.Sprintf("refusing to run: %s is not 1. The real-account tests create system users, units and services and change system settings; "+
	"run them only on a disposable machine (a GitHub-hosted runner, a throwaway VM), through `go run ./tools/realaccount run`", EnvDisposable)

// PasswordLabel is the DPAPI entropy of the password file of the agent account
// on Windows.
const PasswordLabel = "mestre agent password v1"

// WindowsLauncherConfig is the file the tool gives the launcher service
// (its command line has the path): where it is installed, the account it
// starts the terminals with and the file with the password, which only SYSTEM
// reads.
type WindowsLauncherConfig struct {
	Instance     string
	AgentUser    string
	PasswordFile string
	BinDir       string
	SocketDir    string
	StateDir     string
	LogDir       string
}
