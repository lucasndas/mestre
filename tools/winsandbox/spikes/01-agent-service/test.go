//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// tester runs the automated checks of spike 1 against one service.
type tester struct {
	svc    string
	logdir string
	base   request
	failed bool
}

func cmdTest(args []string) (bool, error) {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	which := fs.String("svc", "node", "node or launcher")
	logdir := fs.String("logdir", `C:\winsandbox\resultados\logs`, "where raw transcripts go")
	only := fs.String("only", "", "comma separated scenarios (identity,fakeagent,resize,tree); empty = all")
	noProfile := fs.Bool("noprofile", false, "experiment: skip LoadUserProfile")
	noWinsta := fs.Bool("nowinsta", false, "experiment: skip the window station/desktop ACEs")
	fromLauncher := fs.Bool("launchertoken", false, "experiment: token and profile from MestreSpikeLauncher")
	_ = fs.Parse(args)
	if err := os.MkdirAll(*logdir, 0o755); err != nil {
		return false, err
	}
	if err := enablePrivileges("SeDebugPrivilege"); err != nil {
		fmt.Println("note: SeDebugPrivilege:", err)
	}
	t := &tester{svc: serviceName(*which), logdir: *logdir,
		base: request{Op: "open", Dir: workDir, Cols: 200, Rows: 50, NoProfile: *noProfile, NoWinsta: *noWinsta, TokenFromLauncher: *fromLauncher}}
	fmt.Printf("== spike 1 checks against %s (%s), %s\n", t.svc, pipeName(t.svc), time.Now().Format(time.RFC3339))
	all := map[string]func(){"identity": t.identity, "fakeagent": t.fakeagent, "resize": t.resizeCheck, "tree": t.tree}
	for _, n := range []string{"identity", "fakeagent", "resize", "tree"} {
		if *only != "" && !strings.Contains(","+*only+",", ","+n+",") {
			continue
		}
		fmt.Printf("\n-- %s\n", n)
		all[n]()
	}
	fmt.Printf("\n== result: failed=%v\n", t.failed)
	return t.failed, nil
}

func (t *tester) check(ok bool, name, format string, a ...any) bool {
	st := "PASS"
	if !ok {
		st, t.failed = "FAIL", true
	}
	fmt.Printf("%s %s: %s\n", st, name, fmt.Sprintf(format, a...))
	return ok
}

func (t *tester) open(name, path string, args []string, mod func(*request)) (*client, func()) {
	req := t.base
	req.Path, req.Args = path, args
	if mod != nil {
		mod(&req)
	}
	c, err := openClient(t.svc, req)
	if !t.check(err == nil, name+"/open", "%v", err) {
		return nil, func() {}
	}
	f, err := os.Create(filepath.Join(t.logdir, fmt.Sprintf("%s-%s.raw", t.svc, name)))
	if err == nil {
		c.rawLog = f
	}
	started := c.waitFor(30*time.Second, func() bool { return c.started != nil || c.exit != nil })
	logs, st, ex, _ := c.snapshot()
	for _, l := range logs {
		fmt.Println("   service: " + l)
	}
	if !t.check(started && st != nil, name+"/start", "started=%+v exit=%+v", st, ex) {
		c.close()
		return nil, func() {}
	}
	return c, func() {
		logs, _, _, text := c.snapshot()
		_ = os.WriteFile(filepath.Join(t.logdir, fmt.Sprintf("%s-%s.txt", t.svc, name)),
			[]byte(text+"\n--- service log\n"+strings.Join(logs, "\n")+"\n"), 0o644)
		c.close()
		if f != nil {
			f.Close()
		}
	}
}

func (t *tester) waitExit(c *client, name string, d time.Duration) *exitInfo {
	start := time.Now()
	ok := c.waitFor(d, func() bool { return c.exit != nil })
	_, _, ex, _ := c.snapshot()
	t.check(ok, name+"/exit", "exit=%+v after %v (deadline %v)", ex, time.Since(start).Round(time.Millisecond), d)
	return ex
}

// identity: who the process is, which logon type, integrity, session and
// profile variables.
func (t *tester) identity() {
	const name = "identity"
	line := "whoami /user /groups /fo list & echo USERPROFILE=%USERPROFILE% & echo APPDATA=%APPDATA% & echo LOCALAPPDATA=%LOCALAPPDATA% & echo TERM=%TERM% & echo HOME=%HOME% & reg add HKCU\\Software\\MestreSpike /v probe /d 1 /f >nul 2>&1 && echo HKCU-WRITE=ok || echo HKCU-WRITE=fail"
	c, done := t.open(name, `C:\Windows\System32\cmd.exe`, []string{"/c", line}, nil)
	if c == nil {
		return
	}
	defer done()
	ex := t.waitExit(c, name, 30*time.Second)
	c.waitFor(2*time.Second, func() bool { return c.gone })
	_, st, _, text := c.snapshot()
	self, _ := call(t.svc, request{Op: "info"})
	t.check(ex != nil && ex.Code == 0, name+"/code", "%+v", ex)
	t.check(strings.Contains(strings.ToLower(text), `\mestre-agente`), name+"/user", "whoami shows mestre-agente")
	t.check(regexp.MustCompile(`S-1-5-3\s`).MatchString(text), name+"/batch", "token has NT AUTHORITY\\BATCH (S-1-5-3)")
	t.check(strings.Contains(text, "S-1-16-8192"), name+"/integrity", "medium integrity (S-1-16-8192)")
	t.check(!strings.Contains(text, "S-1-5-32-544"), name+"/not-admin", "no Administrators group")
	m := regexp.MustCompile(`USERPROFILE=(\S+)`).FindStringSubmatch(text)
	t.check(m != nil && strings.EqualFold(m[1], `C:\Users\mestre-agente`), name+"/profile", "%v", m)
	t.check(strings.Contains(text, "TERM=xterm-256color"), name+"/term", "TERM set")
	t.check(strings.Contains(text, "HKCU-WRITE=ok"), name+"/hkcu", "HKCU is the user's own hive (write to HKCU\\Software works)")
	if st != nil && self.Info != nil {
		t.check(st.Session == 0, name+"/session0", "child session %d, service session %d", st.Session, self.Info.Session)
		t.check(st.LogonSession != self.Info.LogonSession, name+"/logon-session", "child %s, service %s", st.LogonSession, self.Info.LogonSession)
	}
}

// fakeagent: output, input (menu, typing, paste-free text), environment and
// graceful exit with the fake agent posing as agy.
func (t *tester) fakeagent() {
	const name = "fakeagent"
	c, done := t.open(name, filepath.Join(binDir, "fake", "agy.exe"), nil, func(r *request) {
		r.HomeIsProfile = true
		r.Env = []string{"MESTRE_SPIKE=1"}
	})
	if c == nil {
		return
	}
	defer done()
	_, ok := c.expect(0, regexp.MustCompile(`Antigravity \(fake\)`), 20*time.Second)
	t.check(ok, name+"/output", "title shown")
	prompt := regexp.MustCompile(`(?m)^> ?\r?$|\n> `)
	trust := regexp.MustCompile(`trust the contents of this project`)
	c.waitFor(10*time.Second, func() bool {
		s := c.textFrom(0)
		return trust.MatchString(s) || prompt.MatchString(s)
	})
	off := 0
	if _, ok := c.expect(0, trust, 0); ok {
		off = c.mark()
		_ = c.typeKeys("\r")
		fmt.Println("   trust menu answered with Enter")
	}
	_, ok = c.expect(off, prompt, 10*time.Second)
	t.check(ok, name+"/prompt", "input prompt reached")
	off = c.mark()
	_ = c.typeKeys("/fake-dump\r")
	_, ok = c.expect(off, regexp.MustCompile(`"provider":"antigravity"`), 10*time.Second)
	t.check(ok, name+"/input", "/fake-dump typed and answered")
	_, ok = c.expect(off, regexp.MustCompile(`"MESTRE_SPIKE":"1"`), 0)
	t.check(ok, name+"/env", "extra environment reached the process")
	off = c.mark()
	_ = c.typeKeys("\x03")
	// Ctrl-C reaches the process as the byte 0x03 when it turned off
	// ENABLE_PROCESSED_INPUT (raw mode, as the real CLIs do), or as
	// CTRL_C_EVENT otherwise; either way it must arrive.
	again := regexp.MustCompile(`Press Ctrl-C again`)
	c.waitFor(5*time.Second, func() bool { return again.MatchString(c.textFrom(off)) || c.exit != nil })
	_, raw := c.expect(off, again, 0)
	_, _, ex0, _ := c.snapshot()
	t.check(raw || ex0 != nil, name+"/ctrl-c", "as input byte: %v; as CTRL_C_EVENT (process ended): %v", raw, ex0 != nil && !raw)
	if raw {
		_ = c.typeKeys("\x03")
	}
	ex := t.waitExit(c, name, 10*time.Second)
	t.check(ex != nil && ex.Code == 0, name+"/code", "%+v", ex)
}

// resizeCheck: the console size seen by the process before and after
// ResizePseudoConsole.
func (t *tester) resizeCheck() {
	const name = "resize"
	c, done := t.open(name, `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, []string{"-NoLogo", "-NoProfile"}, nil)
	if c == nil {
		return
	}
	defer done()
	_, ok := c.expect(0, regexp.MustCompile(`PS [^\r\n]*>`), 30*time.Second)
	t.check(ok, name+"/prompt", "PowerShell prompt")
	// ConPTY repaints the screen after a resize, so each question has its own
	// label.
	size := func(label string) *regexp.Regexp { return regexp.MustCompile(label + `=(\d+)x(\d+)`) }
	ask := func(label string) string {
		return `"` + label + `=$([Console]::WindowWidth)x$([Console]::WindowHeight)"` + "\r"
	}
	off := c.mark()
	_ = c.typeKeys(ask("BEFORE"))
	m, ok := c.expect(off, size("BEFORE"), 10*time.Second)
	t.check(ok && m == "BEFORE=200x50", name+"/initial", "%q", m)
	off = c.mark()
	_ = c.resize(120, 40)
	c.waitFor(5*time.Second, func() bool {
		return len(c.logs) > 0 && strings.HasPrefix(c.logs[len(c.logs)-1], "ResizePseudoConsole")
	})
	_ = c.typeKeys(ask("AFTER"))
	m, ok = c.expect(off, size("AFTER"), 10*time.Second)
	t.check(ok && m == "AFTER=120x40", name+"/after", "%q", m)
	_ = c.typeKeys("exit\r")
	ex := t.waitExit(c, name, 10*time.Second)
	t.check(ex != nil && ex.Code == 0, name+"/code", "%+v", ex)
}

// tree: a grandchild attached to the pseudoconsole and one in its own
// hidden console; closing steps 2 (ClosePseudoConsole) and 3
// (TerminateJobObject) of docs/arquitetura/terminal.md#encerramento.
func (t *tester) tree() {
	const name = "tree"
	c, done := t.open(name, `C:\Windows\System32\cmd.exe`, nil, nil)
	if c == nil {
		return
	}
	defer done()
	_, ok := c.expect(0, regexp.MustCompile(`work>`), 20*time.Second)
	t.check(ok, name+"/prompt", "cmd prompt")
	_ = c.typeKeys(`powershell -NoProfile -Command "Start-Process -WindowStyle Hidden ping.exe -ArgumentList '-n','3600','127.0.0.1'"` + "\r")
	_ = c.typeKeys(`start "" /b ping -n 3601 127.0.0.1 >nul` + "\r")

	var pids []uint32
	start := time.Now()
	for time.Since(start) < 20*time.Second {
		pids = c.queryPIDs(2 * time.Second)
		if strings.Count(describePIDs(pids), "PING.EXE")+strings.Count(describePIDs(pids), "ping.exe") >= 2 {
			break
		}
		pause(300 * time.Millisecond)
	}
	desc := describePIDs(pids)
	t.check(strings.Count(strings.ToLower(desc), "ping.exe") >= 2, name+"/grandchildren",
		"job after %v: %s", time.Since(start).Round(time.Millisecond), desc)

	_ = c.send(frClose, nil)
	ex := t.waitExit(c, name+"/step2", 10*time.Second)
	t.check(ex != nil, name+"/step2-main", "main process ended after ClosePseudoConsole: %+v", ex)
	pause(2 * time.Second) // let console-attached processes react to the close
	var alive []uint32
	for _, p := range pids {
		if processAlive(p) {
			alive = append(alive, p)
		}
	}
	fmt.Printf("   alive 2 s after ClosePseudoConsole: %s (job lists %v)\n", describePIDs(alive), c.queryPIDs(2*time.Second))

	_ = c.send(frKill, nil)
	start = time.Now()
	for time.Since(start) < 5*time.Second {
		alive = alive[:0]
		for _, p := range pids {
			if processAlive(p) {
				alive = append(alive, p)
			}
		}
		if len(alive) == 0 {
			break
		}
		pause(200 * time.Millisecond)
	}
	t.check(len(alive) == 0, name+"/step3", "after TerminateJobObject, alive after %v: %v", time.Since(start).Round(time.Millisecond), alive)
}

// pause waits d; used only between polls of process state.
func pause(d time.Duration) {
	<-time.After(d)
}
