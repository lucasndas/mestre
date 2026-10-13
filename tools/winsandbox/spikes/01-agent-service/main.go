//go:build windows

// Command spikesvc is the test service of the M1 Windows spikes
// (docs/desenvolvimento/ordem-de-implementacao.md#spikes-do-m1, items in
// docs/arquitetura/windows.md#verificar-na-implementaÃ§Ã£o). One binary covers
// the three spikes:
//
//  1. ConPTY with the process started as mestre-agente from a service with a
//     virtual account, in session 0 (LogonUser batch + LoadUserProfile +
//     CreateProcessAsUser + pseudoconsole + job object);
//  2. SeAssignPrimaryTokenPrivilege and SeIncreaseQuotaPrivilege granted to
//     the virtual account by the LSA and declared as required privileges;
//  3. claude, codex and agy logged in on mestre-agente's profile, and a new
//     logon session of the same account finding that login.
//
// Usage (elevated):
//
//	spikesvc install [-service mestre|launcher] [-grant=false] [-declare=false] [-extra SeBackupPrivilege,...]
//	sc.exe start MestreSpike
//	spikesvc info [-svc mestre]                  service user, session, window station, token privileges
//	spikesvc test [-svc mestre] [-logdir dir]    automated ConPTY checks with the fake agent, cmd and PowerShell
//	spikesvc open -i -- <exe> [args]           interactive terminal as mestre-agente (Ctrl+] leaves)
//	spikesvc open [-until re] [-step 're|||keys'] [-deadline 60s] -- <exe> [args]
//	spikesvc rights <account>...               LSA rights of accounts
//	spikesvc uninstall
//
// The service talks to clients over \\.\pipe\mestre-spike-<service> (SYSTEM
// and Administrators only). The plan B service (-service launcher) is the
// same code running as LocalSystem; open/test -launchertoken try the variant
// where the launcher only logs on and loads the profile and hands the token
// to the mestre service.
//
// Spike 3 helpers, copied to C:\ProgramData\MestreSpike\bin\: login.ps1
// (login terminal of each CLI), estado-credenciais.cmd (where logins went)
// and credprobe.ps1 (Credential Manager write/read with a dummy value).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: spikesvc install|uninstall|service|info|open|test|rights ...")
		os.Exit(2)
	}
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "install":
		err = cmdInstall(args)
	case "uninstall":
		err = cmdUninstall()
	case "service":
		name := mainSvc
		if len(args) > 0 {
			name = args[0]
		}
		err = runService(name)
	case "info":
		err = cmdInfo(args)
	case "open":
		var code int
		code, err = cmdOpen(args)
		if err == nil {
			os.Exit(code)
		}
	case "test":
		var failed bool
		failed, err = cmdTest(args)
		if err == nil && failed {
			os.Exit(1)
		}
	case "rights":
		err = cmdRights(args)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "spikesvc:", err)
		os.Exit(1)
	}
}

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	which := fs.String("svc", "mestre", "mestre or launcher")
	_ = fs.Parse(args)
	resp, err := call(serviceName(*which), request{Op: "info"})
	if err != nil {
		return err
	}
	in := resp.Info
	fmt.Printf("service:        %s\nuser:           %s (%s)\nsession:        %d\nlogon session:  %s\nwindow station: %s\ndesktop:        %s\nprivileges:\n",
		in.Service, in.User, in.SID, in.Session, in.LogonSession, in.WindowStation, in.Desktop)
	for _, p := range in.Privileges {
		fmt.Println("  " + p)
	}
	if in.Error != "" {
		fmt.Println("error:", in.Error)
	}
	return nil
}

type stepList []struct {
	re   *regexp.Regexp
	keys string
}

func (s *stepList) String() string { return strconv.Itoa(len(*s)) }

func (s *stepList) Set(v string) error {
	re, keys, ok := strings.Cut(v, "|||")
	if !ok {
		return errors.New("step must be 'regex|||keys'")
	}
	k, err := strconv.Unquote(`"` + keys + `"`)
	if err != nil {
		return err
	}
	r, err := regexp.Compile(re)
	if err != nil {
		return err
	}
	*s = append(*s, struct {
		re   *regexp.Regexp
		keys string
	}{r, k})
	return nil
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func cmdOpen(args []string) (int, error) {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	which := fs.String("svc", "mestre", "mestre or launcher")
	inter := fs.Bool("i", false, "interactive: relay this console (Ctrl+] leaves)")
	cols := fs.Int("cols", 0, "columns (default: this console in -i, else 200)")
	rows := fs.Int("rows", 0, "rows (default: this console in -i, else 50)")
	noProfile := fs.Bool("noprofile", false, "experiment: skip LoadUserProfile")
	noWinsta := fs.Bool("nowinsta", false, "experiment: skip the window station/desktop ACEs")
	home := fs.Bool("home", false, "set HOME to the loaded profile")
	fromLauncher := fs.Bool("launchertoken", false, "experiment: token and profile from MestreSpikeLauncher")
	dir := fs.String("dir", workDir, "working folder")
	deadline := fs.Duration("deadline", 60*time.Second, "non-interactive: give up after this")
	until := fs.String("until", "", "non-interactive: stop when the output matches")
	out := fs.String("out", "", "non-interactive: file for the raw output")
	var env multiFlag
	var steps stepList
	fs.Var(&env, "env", "KEY=VALUE for the process (repeatable)")
	fs.Var(&steps, "step", "non-interactive: 'regex|||keys' (repeatable, in order)")
	_ = fs.Parse(args)
	argv := fs.Args()
	if len(argv) == 0 {
		return 0, errors.New("open: missing command")
	}
	c16, r16 := uint16(*cols), uint16(*rows)
	if c16 == 0 {
		c16, r16 = 200, 50
		if *inter {
			c16, r16 = consoleSize()
		}
	}
	req := request{Op: "open", Path: argv[0], Args: argv[1:], Dir: *dir, Env: env, Cols: c16, Rows: r16,
		NoProfile: *noProfile, NoWinsta: *noWinsta, HomeIsProfile: *home, TokenFromLauncher: *fromLauncher}
	c, err := openClient(serviceName(*which), req)
	if err != nil {
		return 0, err
	}
	defer c.close()
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		c.rawLog = f
	}
	if *inter {
		c.echo = os.Stdout
		err := c.interactive()
		logs, st, ex, _ := c.snapshot()
		fmt.Fprintf(os.Stderr, "\r\n--- spikesvc: started=%+v exit=%+v\r\n", st, ex)
		for _, l := range logs {
			fmt.Fprintf(os.Stderr, "--- %s\r\n", l)
		}
		return 0, err
	}
	start := time.Now()
	off := 0
	for _, s := range steps {
		if _, ok := c.expect(off, s.re, time.Until(start.Add(*deadline))); !ok {
			break
		}
		off = c.mark()
		_ = c.typeKeys(s.keys)
	}
	var re *regexp.Regexp
	if *until != "" {
		re = regexp.MustCompile(*until)
	}
	matched := c.waitFor(time.Until(start.Add(*deadline)), func() bool {
		if re != nil && re.MatchString(c.textFrom(0)) {
			return true
		}
		return c.exit != nil
	})
	if c.exit != nil {
		// Let the last output frames arrive (bounded).
		c.waitFor(2*time.Second, func() bool { return c.gone })
	}
	logs, st, ex, text := c.snapshot()
	fmt.Println(text)
	fmt.Printf("--- waited %v (deadline %v), matched=%v\n", time.Since(start).Round(time.Millisecond), *deadline, matched)
	if st != nil {
		fmt.Printf("--- started: pid %d, session %d, logon session %s, profile %s\n", st.PID, st.Session, st.LogonSession, st.ProfileDir)
	}
	for _, l := range logs {
		fmt.Println("--- " + l)
	}
	if ex == nil {
		return 2, nil
	}
	fmt.Printf("--- exit code %d %s\n", ex.Code, ex.Error)
	return int(ex.Code & 0xFF), nil
}
