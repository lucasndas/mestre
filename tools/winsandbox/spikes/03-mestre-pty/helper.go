//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Helpers typed into the terminals (or run as mestre-agente outside them).
func cmdHelper(args []string) error {
	if len(args) == 0 {
		return errors.New("helper probe|stay|sockcheck ...")
	}
	switch args[0] {
	case "probe":
		return helperProbe(args[1:])
	case "stay":
		return helperStay(args[1:])
	case "sockcheck":
		return helperSockCheck(args[1:])
	}
	return fmt.Errorf("unknown helper %q", args[0])
}

// helperProbe calls cli.sock, as the CLI would; options shape the process
// tree first (same cases as 02-caller-identity).
func helperProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	label := fs.String("label", "probe", "case name")
	waitParent := fs.Bool("wait-parent", false, "wait (up to 15 s) for the parent to exit first: an orphan")
	nestJob := fs.Bool("nestjob", false, "put this process in a new job of its own first")
	breakaway := fs.Bool("breakaway", false, "try to start a child with CREATE_BREAKAWAY_FROM_JOB first")
	_ = fs.Parse(args)
	var notes []string
	if *nestJob {
		j, err := windows.CreateJobObject(nil, nil)
		if err == nil {
			err = windows.AssignProcessToJobObject(j, windows.CurrentProcess())
		}
		notes = append(notes, fmt.Sprintf("own nested job: err=%v", err))
	}
	if *waitParent {
		notes = append(notes, waitForParent(15*time.Second))
	}
	if *breakaway {
		exe, _ := os.Executable()
		si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
		var pi windows.ProcessInformation
		err := windows.CreateProcess(nil, wstr(windows.ComposeCommandLine([]string{exe, "helper", "stay", "-seconds", "1"})), nil, nil, false,
			windows.CREATE_BREAKAWAY_FROM_JOB, nil, nil, &si, &pi)
		if err == nil {
			windows.CloseHandle(pi.Process)
			windows.CloseHandle(pi.Thread)
		}
		notes = append(notes, fmt.Sprintf("CreateProcess(CREATE_BREAKAWAY_FROM_JOB): err=%v", err))
	}
	c, err := net.Dial("unix", cliSock)
	if err != nil {
		return err
	}
	defer c.Close()
	l := newLineConn(c)
	if err := l.send(probeMsg{Label: *label, Note: strings.Join(notes, "; "), SelfPID: windows.GetCurrentProcessId()}); err != nil {
		return err
	}
	var r identResult
	if err := l.recv(&r, 30*time.Second); err != nil {
		return err
	}
	fmt.Printf("probe %s pid %d: peer pid %d, terminal %q, err %q\n", *label, windows.GetCurrentProcessId(), r.PeerPID, r.Terminal, r.Err)
	return nil
}

func waitForParent(d time.Duration) string {
	ppid := parentPID()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, ppid)
	if err != nil {
		return fmt.Sprintf("parent %d not open: %v", ppid, err)
	}
	defer windows.CloseHandle(h)
	start := time.Now()
	ev, _ := windows.WaitForSingleObject(h, uint32(d.Milliseconds()))
	return fmt.Sprintf("parent %d exited=%v after %v (deadline %v)", ppid, ev == windows.WAIT_OBJECT_0, time.Since(start).Round(time.Millisecond), d)
}

// helperStay is a long-lived process for the closing tests: it stays for at
// most -seconds and, with -ignore-ctrlc, ignores Ctrl-C.
func helperStay(args []string) error {
	fs := flag.NewFlagSet("stay", flag.ExitOnError)
	label := fs.String("label", "stay", "name")
	secs := fs.Int("seconds", 300, "stay at most this long")
	ignore := fs.Bool("ignore-ctrlc", false, "ignore Ctrl-C (SetConsoleCtrlHandler(NULL, TRUE))")
	_ = fs.Parse(args)
	note := ""
	if *ignore {
		note = fmt.Sprintf(", ignoring Ctrl-C (err=%v)", setConsoleCtrlIgnore())
	}
	fmt.Printf("stay %s: pid %d, up to %ds%s\n", *label, windows.GetCurrentProcessId(), *secs, note)
	<-time.After(time.Duration(*secs) * time.Second)
	return nil
}

// helperSockCheck runs, as mestre-agente, the socket checks of item 6:
// connecting to a ptyhost's socket and changing the socket file's ACL. It
// writes the results as JSON to -out (and prints them).
func helperSockCheck(args []string) error {
	fs := flag.NewFlagSet("sockcheck", flag.ExitOnError)
	sock := fs.String("sock", "", "socket path")
	out := fs.String("out", "", "JSON results file")
	_ = fs.Parse(args)
	res := map[string]string{}
	if u, err := windows.GetCurrentProcessToken().GetTokenUser(); err == nil {
		res["user"] = accountName(u.User.Sid)
	}
	if c, err := net.Dial("unix", *sock); err != nil {
		res["connect"] = "refused: " + err.Error()
	} else {
		c.Close()
		res["connect"] = "CONNECTED"
	}
	res["read_sd"] = pathSD(*sock)
	acl, _ := daclFromSDDL("D:(A;;FA;;;WD)")
	if err := windows.SetNamedSecurityInfo(*sock, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		res["set_dacl"] = "refused: " + err.Error()
	} else {
		res["set_dacl"] = "CHANGED"
	}
	if err := os.Remove(*sock); err != nil {
		res["delete"] = "refused: " + err.Error()
	} else {
		res["delete"] = "DELETED"
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(b))
	if *out != "" {
		return os.WriteFile(*out, b, 0o644)
	}
	return nil
}

// cmdCtl sends one control line to the engine and prints the answer.
func cmdCtl(args []string) error {
	if len(args) != 1 {
		return errors.New(`ctl '{"op":"list"}'`)
	}
	var c control
	if err := json.Unmarshal([]byte(args[0]), &c); err != nil {
		return err
	}
	r, err := ctl(c)
	b, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(b))
	return err
}

func ctl(c control) (controlResp, error) {
	var r controlResp
	if err := callJSON(controlPipe, c, &r); err != nil {
		return r, err
	}
	if r.Error != "" {
		return r, errors.New(r.Error)
	}
	return r, nil
}
