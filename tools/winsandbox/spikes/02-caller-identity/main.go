//go:build windows

// Command callerid is the 4th M1 Windows spike: identifying who calls the
// local channel (docs/arquitetura/seguranca.md#verificar-na-implementação).
//
// It reuses the setup of tools/winsandbox/spikes/01-agent-service: the
// mestre-agente account, the MestreSpikeLauncher service (LocalSystem, hands
// out mestre-agente's token) and the MestreSpike service with the virtual
// account NT SERVICE\MestreSpike. "callerid install" points MestreSpike at
// this binary, so the engine stand-in keeps the virtual account, its
// privileges and the launcher's trust; "callerid restore" points it back to
// spikesvc.
//
// The service opens two terminals as mestre-agente with the token from the
// launcher (ConPTY, root cmd.exe, each in its own job object; T1's job without
// JOB_OBJECT_LIMIT_BREAKAWAY_OK, T2's with it) and serves the named pipe
// \\.\pipe\mestre-spike-callerid. For each client it takes the PID from
// GetNamedPipeClientProcessId and runs the four methods of ident.go. "callerid
// run" types each case into a terminal, collects the results and prints them.
//
// Usage (elevated; 01-agent-service installed and both services started):
//
//	callerid install
//	sc.exe start MestreSpike
//	callerid run [-dacl] [-out dir]   -dacl: add the service to the token's default DACL
//	callerid probe -label x [...]     the caller (typed into the terminals by run)
//	callerid restore
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	agentUser    = "mestre-agente"
	baseDir      = `C:\ProgramData\MestreSpike`
	serviceName  = "MestreSpike" // NT SERVICE\MestreSpike
	launcherSvc  = "MestreSpikeLauncher"
	launcherPipe = `\\.\pipe\mestre-spike-MestreSpikeLauncher`
	controlPipe  = `\\.\pipe\mestre-spike-callerid-control`
	identityPipe = `\\.\pipe\mestre-spike-callerid`
)

var (
	binDir  = filepath.Join(baseDir, "bin")
	logDir  = filepath.Join(baseDir, "logs")
	workDir = filepath.Join(baseDir, "work")
	exePath = filepath.Join(binDir, "callerid.exe")
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: callerid install|restore|service|run|probe ...")
		os.Exit(2)
	}
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "install":
		err = cmdInstall()
	case "restore":
		err = cmdRestore()
	case "service":
		err = runService()
	case "run":
		err = cmdRun(args)
	case "probe":
		err = cmdProbe(args)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "callerid:", err)
		os.Exit(1)
	}
}
