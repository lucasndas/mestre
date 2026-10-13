//go:build windows

// Command mestrepty is the 5th M1 Windows spike: the mestre-pty
// (docs/arquitetura/terminal.md#mestre-pty; items (a) to (f) of spike 5 and
// the WMI item in docs/arquitetura/windows.md#verificar-na-implementação).
// One binary with three roles:
//
//   - ptyhost: the mestre-pty prototype, copied to bin\ptyhost.exe and
//     started only by MestreSpikeLauncher's start-pty
//     (tools/winsandbox/spikes/01-agent-service/startpty.go) as mestre-agente.
//     It reads the terminal secret from its stdin (an inherited pipe), creates
//     the ConPTY (github.com/aymanbagabas/go-pty) and a job object with
//     KILL_ON_JOB_CLOSE, keeps the output in a buffer and the screen in
//     github.com/charmbracelet/x/vt, and listens on an AF_UNIX socket in
//     C:\ProgramData\MestreSpike\pty\<id>.sock (ptyhost.go).
//   - service: the engine stand-in. "mestrepty install" points the MestreSpike
//     service (NT SERVICE\MestreSpike, created by spikesvc install) at
//     bin\mestrepty.exe. It asks the launcher for each ptyhost, checks the
//     peer PID of the socket (SIO_AF_UNIX_GETPEERPID) against the PID and
//     creation time returned, does the two-way HMAC handshake, keeps the
//     terminals in no\terminals.json to reconnect after a restart, and serves
//     cli.sock to identify callers by asking each ptyhost (engine.go).
//   - the driver (run, ctl) and the helpers typed into the terminals (helper).
//
// Usage (elevated; 01-agent-service installed, mestre-agente created):
//
//	go build -o C:\winsandbox\bin\spikesvc.exe ./tools/winsandbox/spikes/01-agent-service
//	mestrepty install -spikesvc C:\winsandbox\bin\spikesvc.exe -fakeagent fakeagent.exe
//	mestrepty run basic|clis|survive|isolate|kill|ident|wmi [-out dir]
//	mestrepty ctl '{"op":"list"}'
//	mestrepty restore
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
	controlPipe  = `\\.\pipe\mestre-spike-pty-control`
	cols, rows   = 200, 50
)

var (
	binDir     = filepath.Join(baseDir, "bin")
	logDir     = filepath.Join(baseDir, "logs")
	workDir    = filepath.Join(baseDir, "work")
	ptyDir     = filepath.Join(baseDir, "pty")
	cliDir     = filepath.Join(baseDir, "cli")
	cliSock    = filepath.Join(cliDir, "cli.sock")
	stateFile  = filepath.Join(baseDir, "no", "terminals.json")
	hostLogDir = filepath.Join(workDir, "ptylogs")
	exePath    = filepath.Join(binDir, "mestrepty.exe")
	hostPath   = filepath.Join(binDir, "ptyhost.exe")
)

func sockPath(id string) string { return filepath.Join(ptyDir, id+".sock") }

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mestrepty install|restore|service|ptyhost|run|ctl|helper ...")
		os.Exit(2)
	}
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "install":
		err = cmdInstall(args)
	case "restore":
		err = cmdRestore()
	case "service":
		err = runService()
	case "ptyhost":
		err = runHost(args)
	case "run":
		err = cmdRun(args)
	case "ctl":
		err = cmdCtl(args)
	case "helper":
		err = cmdHelper(args)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "mestrepty:", err)
		os.Exit(1)
	}
}
