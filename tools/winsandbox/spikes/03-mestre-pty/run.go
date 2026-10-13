//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// The driver: one scenario per item of the round-3 prompt. Each writes
// <out>\<scenario>.md. It runs as Administrator and talks to the engine over
// the control pipe; the waits have deadlines and report how long they took.

const (
	agentProfile = `C:\Users\mestre-agente`
	accountDir   = `C:\ProgramData\MestreSpike\work\contas\p1`
)

var (
	fakeExe   = filepath.Join(binDir, "fakeagent.exe")
	claudeExe = agentProfile + `\.local\bin\claude.exe`
	codexExe  = agentProfile + `\.codex\packages\standalone\current\bin\codex.exe`
	agyExe    = agentProfile + `\AppData\Local\agy\bin\agy.exe`
	agyEnv    = []string{`HOME=` + accountDir + `\home`, `USERPROFILE=` + accountDir + `\home`}
	cmdExe    = `C:\Windows\System32\cmd.exe`
)

type drv struct {
	b   strings.Builder
	out string
}

func (d *drv) say(format string, a ...any) {
	s := fmt.Sprintf(format, a...)
	fmt.Println(s)
	d.b.WriteString(s + "\n")
}

func (d *drv) block(title, text string) {
	d.say("\n%s:\n\n```\n%s\n```\n", title, text)
}

func (d *drv) save(name string) error {
	p := filepath.Join(d.out, name+".md")
	fmt.Println("results:", p)
	return os.WriteFile(p, []byte(d.b.String()), 0o644)
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	out := fs.String("out", `C:\winsandbox\resultados\rodada-3`, "results folder")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		return errors.New("run clis|survive|isolate|kill|ident|wmi|screen <id>")
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	// The driver checks processes of mestre-agente, whose DACL does not
	// include Administrators.
	_ = enablePrivilege("SeDebugPrivilege")
	d := &drv{out: *out}
	d.say("# %s — %s\n", fs.Arg(0), time.Now().Format(time.RFC3339))
	var err error
	switch fs.Arg(0) {
	case "clis":
		err = d.clis(fs.Args()[1:])
	case "survive":
		err = d.survive()
	case "hkcu":
		err = d.hkcu()
	case "isolate":
		err = d.isolate()
	case "procaccess":
		err = d.procAccess()
	case "kill":
		err = d.kill()
	case "ident":
		err = d.ident()
	case "wmi":
		err = d.wmi()
	case "screen":
		fmt.Println(clean(screen(fs.Arg(1))))
		return nil
	default:
		return fmt.Errorf("unknown scenario %q", fs.Arg(0))
	}
	if err != nil {
		d.say("\n**error:** %v", err)
	}
	if serr := d.save(strings.Join(fs.Args(), "-")); serr != nil {
		return serr
	}
	return err
}

// Engine calls.

func (d *drv) open(id, path string, args, env []string, harden bool) (*termRec, error) {
	_, _ = ctl(control{Op: "forget", ID: id})
	r, err := ctl(control{Op: "open", ID: id, Path: path, Args: args, Env: env, Dir: workDir, Harden: harden})
	d.say("\nopen %s: %s %v env %v", id, path, args, env)
	for _, l := range r.Log {
		d.say("- %s", l)
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", id, err)
	}
	return r.Rec, nil
}

func typ(id, s string) error {
	_, err := ctl(control{Op: "input", ID: id, Data: []byte(s)})
	return err
}

func screen(id string) string {
	r, err := ctl(control{Op: "screen", ID: id})
	if err != nil || r.Host == nil {
		return "(screen: " + fmt.Sprint(err) + ")"
	}
	return r.Host.Text
}

func waitFor(id, re string, d time.Duration) (string, bool, string) {
	r, err := ctl(control{Op: "waitscreen", ID: id, Regex: re, TimeoutS: d.Seconds()})
	if err != nil || r.Host == nil {
		return fmt.Sprint(err), false, r.Waited
	}
	return r.Host.Text, r.Matched, r.Waited
}

func status(id string) (*hostStatus, error) {
	r, err := ctl(control{Op: "status", ID: id})
	if err != nil {
		return nil, err
	}
	return r.Host.Status, nil
}

func (d *drv) stop(id, mode string, sweep bool) *hostResp {
	r, err := ctl(control{Op: "stop", ID: id, Mode: mode, Sweep: sweep})
	if err != nil {
		d.say("stop %s %s: %v", id, mode, err)
		return nil
	}
	for _, l := range r.Host.Log {
		d.say("- %s", l)
	}
	return r.Host
}

var braille = regexp.MustCompile(`[\x{2800}-\x{28FF}]`)

// clean drops the logo lines (braille art) and the blank tail of a screen.
func clean(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if braille.MatchString(l) {
			continue
		}
		out = append(out, strings.TrimRight(l, " "))
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

func psList(ps []procInfo) string {
	var s []string
	for _, p := range ps {
		s = append(s, fmt.Sprintf("%d %s", p.PID, p.Image))
	}
	return strings.Join(s, ", ")
}

// survivors returns the processes of the list still running (same PID and
// creation time).
func survivors(ps []procInfo) []procInfo {
	var out []procInfo
	for _, p := range ps {
		if alive(p.PID, p.Created) {
			out = append(out, p)
		}
	}
	return out
}

// waitGone waits (bounded) until none of ps runs.
func waitGone(ps []procInfo, d time.Duration) ([]procInfo, time.Duration) {
	start := time.Now()
	for {
		left := survivors(ps)
		if len(left) == 0 || time.Since(start) > d {
			return left, time.Since(start).Round(time.Millisecond)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitStatus polls the host status (bounded) until pred holds.
func waitStatus(id string, d time.Duration, pred func(*hostStatus) bool) (*hostStatus, bool, time.Duration) {
	start := time.Now()
	for {
		st, err := status(id)
		if err == nil && pred(st) {
			return st, true, time.Since(start).Round(time.Millisecond)
		}
		if time.Since(start) > d {
			return st, false, time.Since(start).Round(time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func hasImage(ps []procInfo, image string, n int) bool {
	c := 0
	for _, p := range ps {
		if strings.EqualFold(p.Image, image) {
			c++
		}
	}
	return c >= n
}

func jsonStr(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// Item 4: the fake agent and the three CLIs through the ptyhost.

type cliCase struct {
	id, name, path string
	args, env      []string
	ready          string            // screen when it waits for input
	answers        map[string]string // startup screen regex -> keys
	again          string            // the "press Ctrl-C again" hint
}

func cliCases() []cliCase {
	trust := `(?i)do you trust|trust this folder|trust the files`
	return []cliCase{
		{id: "c-fake", name: "agente falso", path: fakeExe, args: []string{"--provider", "claude"},
			env: []string{`CLAUDE_CONFIG_DIR=` + accountDir + `\fake-claude`}, ready: `(?m)^>\s*$`,
			answers: map[string]string{trust: "\r", `(?i)log ?in|sign ?in`: "\r"}, again: `(?i)again to exit`},
		{id: "c-claude", name: "claude", path: claudeExe, env: []string{`CLAUDE_CONFIG_DIR=` + accountDir + `\claude`},
			// Claude's trust menu starts on "No, exit": arrow down, then Enter.
			ready: `(?i)\? for shortcuts|bypass|shift\+tab`, answers: map[string]string{`(?i)yes, i trust this folder`: "\x1b[B\r",
				`(?i)choose the text style|enter to confirm|press enter to continue|security notes`: "\r"},
			again: `(?i)again to exit`},
		{id: "c-codex", name: "codex", path: codexExe, ready: `(?i)\? for shortcuts`,
			answers: map[string]string{trust: "\r"}, again: `(?i)again to (quit|exit)`},
		{id: "c-agy", name: "agy", path: agyExe, env: agyEnv, ready: `(?i)type your message|\? for shortcuts|shortcuts`,
			answers: map[string]string{trust: "\r"}, again: `(?i)again to (quit|exit)`},
	}
}

func (d *drv) clis(only []string) error {
	_ = os.MkdirAll(filepath.Join(accountDir, "fake-claude"), 0o755)
	// The fake agent checks for a credential file only (a dummy one).
	_ = os.WriteFile(filepath.Join(accountDir, "fake-claude", ".credentials.json"), []byte(`{"fake":true}`), 0o644)
	for _, c := range cliCases() {
		if len(only) > 0 && !slices.Contains(only, c.name) {
			continue
		}
		d.say("\n## %s\n", c.name)
		if err := d.cliCheck(c); err != nil {
			d.say("\n**%s: error:** %v", c.name, err)
		}
	}
	return nil
}

func (d *drv) cliCheck(c cliCase) error {
	rec, err := d.open(c.id, c.path, c.args, c.env, false)
	if err != nil {
		return err
	}
	st, _ := status(c.id)
	d.say("- ptyhost pid %d as %s, session %d, logon session %s, ptyhost in a job: %v; agent pid %d in the job: %s",
		rec.HostPID, st.User, st.Session, st.LogonSession, st.HostInAJob, st.AgentPID, psList(st.Job))
	// Startup screens, then the input prompt (deadline 90 s).
	start := time.Now()
	answered := map[string]int{}
	ready := false
	text := ""
	for time.Since(start) < 90*time.Second && !ready {
		var ok bool
		text, ok, _ = waitFor(c.id, c.ready+`|`+strings.Join(keys(c.answers), "|"), 90*time.Second-time.Since(start))
		if !ok {
			break
		}
		if regexp.MustCompile(c.ready).MatchString(text) {
			ready = true
			break
		}
		for re, k := range c.answers {
			if regexp.MustCompile(re).MatchString(text) && answered[re] < 3 {
				answered[re]++
				d.say("- startup screen matched %q: sent %q", re, k)
				_ = typ(c.id, k)
				// Let the screen change before matching again (bounded).
				waitChange(c.id, text, 10*time.Second)
			}
		}
	}
	d.say("- ready=%v after %v (deadline 90s)", ready, time.Since(start).Round(time.Millisecond))
	d.block("tela de início", clean(text))
	if !ready {
		d.stop(c.id, "kill", true)
		return errors.New("did not reach the input prompt")
	}
	// Input and output.
	_ = typ(c.id, "hello ptyhost")
	text, ok, waited := waitFor(c.id, `hello ptyhost`, 15*time.Second)
	d.say("- **entrada/saída:** typed `hello ptyhost`, on screen=%v after %s (deadline 15s)", ok, waited)
	// Raw mode: Ctrl-C as a byte. A process that got CTRL_C_EVENT without
	// a handler would die; the CLIs clear the line or show a hint instead.
	_ = typ(c.id, "\x03")
	text2, _ := waitChange(c.id, text, 10*time.Second)
	st, _ = status(c.id)
	cleared := !strings.Contains(text2, "hello ptyhost")
	d.say("- **modo cru:** 1st `\\x03`: agent alive=%v, line cleared=%v, hint=%v", st.AgentAlive, cleared, regexp.MustCompile(c.again).MatchString(text2))
	d.block("tela depois do 1º \\x03", clean(text2))
	_ = typ(c.id, "\x03")
	st, exited, ew := waitStatus(c.id, 10*time.Second, func(s *hostStatus) bool { return !s.AgentAlive })
	d.say("- 2nd `\\x03`: agent exited=%v code=%s (%v, deadline 10s)", exited, deref(st.ExitCode), ew)
	if !exited {
		d.block("tela depois do 2º \\x03", clean(screen(c.id)))
	}
	r := d.stop(c.id, "kill", true)
	if r != nil {
		left, w := waitGone(r.Killed, 5*time.Second)
		d.say("- stop kill: %d processes ended (%s), still running after %v: [%s]", len(r.Killed), psList(r.Killed), w, psList(left))
	}
	return nil
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func deref(p *int) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprint(*p)
}

// waitChange waits (bounded) for the screen to differ from old.
func waitChange(id, old string, d time.Duration) (string, bool) {
	start := time.Now()
	for {
		s := screen(id)
		if s != old {
			return s, true
		}
		if time.Since(start) > d {
			return s, false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Item 5: the engine and the launcher restarted with the agents alive.

func (d *drv) survive() error {
	fa, err := d.open("sv-fake", fakeExe, []string{"--provider", "claude"}, []string{`CLAUDE_CONFIG_DIR=` + accountDir + `\fake-claude`}, false)
	if err != nil {
		return err
	}
	if _, ok, w := waitFor("sv-fake", `(?m)^>\s*$`, 20*time.Second); !ok {
		return fmt.Errorf("fake agent not ready in %s", w)
	}
	_ = typ("sv-fake", "antes do reinicio")
	waitFor("sv-fake", "antes do reinicio", 10*time.Second)
	agy, err := d.open("sv-agy", agyExe, nil, agyEnv, false)
	if err != nil {
		return err
	}
	_, agyReady, w := waitFor("sv-agy", `(?i)type your message|shortcuts`, 60*time.Second)
	d.say("- agy ready=%v (%s, deadline 60s)", agyReady, w)
	cmd, err := d.open("sv-cmd", cmdExe, []string{"/q", "/k"}, []string{"PROMPT=$G$S"}, false)
	if err != nil {
		return err
	}
	waitFor("sv-cmd", `>`, 10*time.Second)
	agents := map[string]*termRec{"sv-fake": fa, "sv-agy": agy, "sv-cmd": cmd}
	agentProcs := func() map[string]procInfo {
		m := map[string]procInfo{}
		for id := range agents {
			if st, err := status(id); err == nil {
				m[id] = procInfo{PID: st.AgentPID, Created: st.AgentCreated}
			}
		}
		return m
	}
	before := agentProcs()
	s1fake, s1agy := screen("sv-fake"), screen("sv-agy")
	d.block("sv-fake antes", clean(s1fake))
	d.block("sv-agy antes", clean(s1agy))

	d.say("\n## Reinício do serviço MestreSpike (o motor)\n")
	if err := stopService(serviceName, 15*time.Second); err != nil {
		return err
	}
	for id, p := range before {
		d.say("- engine stopped: %s agent pid %d alive=%v; ptyhost %d alive=%v", id, p.PID, alive(p.PID, p.Created), agents[id].HostPID, alive(agents[id].HostPID, agents[id].HostCreated))
	}
	if err := startService(serviceName); err != nil {
		return err
	}
	l, err := ctl(control{Op: "list"})
	if err != nil {
		return err
	}
	d.block("volta do motor (boot log)", strings.Join(l.Boot, "\n"))
	after := agentProcs()
	for id, p := range before {
		d.say("- %s: agent pid before %d, after %d, same=%v", id, p.PID, after[id].PID, p == after[id])
	}
	s2fake, s2agy := screen("sv-fake"), screen("sv-agy")
	d.say("- **sv-fake: tela igual depois da volta: %v**", s1fake == s2fake)
	d.say("- sv-agy: tela igual depois da volta: %v", s1agy == s2agy)
	if s1agy != s2agy {
		d.block("sv-agy depois", clean(s2agy))
	}
	_ = typ("sv-fake", " depois")
	_, ok, w := waitFor("sv-fake", "antes do reinicio depois", 10*time.Second)
	d.say("- typing after the return reaches the agent: %v (%s)", ok, w)

	d.say("\n## Reinício do MestreSpikeLauncher\n")
	if err := stopService(launcherSvc, 15*time.Second); err != nil {
		return err
	}
	if err := startService(launcherSvc); err != nil {
		return err
	}
	after2 := agentProcs()
	for id, p := range before {
		d.say("- %s: agent pid %d alive=%v (status says %d)", id, p.PID, alive(p.PID, p.Created), after2[id].PID)
	}
	// The old terminal still reads its HKCU and the Credential Manager.
	_ = typ("sv-cmd", `reg query "HKCU\Software\Microsoft\Windows\CurrentVersion\Explorer\Shell Folders" /v "Local AppData" & cmdkey /list & echo FIM-CMD`+"\r")
	text, ok, w := waitFor("sv-cmd", `FIM-CMD\s*\n`, 20*time.Second)
	d.say("- old terminal sv-cmd after the launcher restart: done=%v (%s)", ok, w)
	d.block("sv-cmd", clean(text))
	// A new ptyhost after the restart, with agy -p "/usage".
	if _, err := d.open("sv-usage", agyExe, []string{"-p", "/usage", "--output-format", "json"}, agyEnv, false); err != nil {
		return err
	}
	st, done, uw := waitStatus("sv-usage", 90*time.Second, func(s *hostStatus) bool { return !s.AgentAlive })
	rp, _ := ctl(control{Op: "replay", ID: "sv-usage"})
	out := ""
	if rp.Host != nil {
		out = strip(rp.Host.Data)
	}
	d.say("- **new ptyhost after the launcher restart: agy -p /usage exited=%v code=%s (%v, deadline 90s); SUCCESS in the output: %v**",
		done, deref(st.ExitCode), uw, strings.Contains(out, "SUCCESS"))
	d.block("saída do agy -p /usage (replay)", strings.TrimSpace(out))
	if _, err := d.open("sv-cmdkey", cmdExe, []string{"/c", "cmdkey /list & echo FIM-CMD"}, nil, false); err == nil {
		waitStatus("sv-cmdkey", 20*time.Second, func(s *hostStatus) bool { return !s.AgentAlive })
		rp, _ := ctl(control{Op: "replay", ID: "sv-cmdkey"})
		if rp.Host != nil {
			d.block("cmdkey /list num ptyhost novo", strings.TrimSpace(strip(rp.Host.Data)))
		}
		d.stop("sv-cmdkey", "kill", true)
	}
	d.say("\n## Encerramento\n")
	for _, id := range []string{"sv-fake", "sv-agy", "sv-cmd", "sv-usage"} {
		d.stop(id, "kill", true)
	}
	return nil
}

// hkcu: an old terminal reads its HKCU and the Credential Manager before
// and after a launcher restart (the launcher held the loaded profile).
func (d *drv) hkcu() error {
	if _, err := d.open("hk", cmdExe, []string{"/q", "/k"}, []string{"PROMPT=$G$S"}, false); err != nil {
		return err
	}
	waitFor("hk", `>`, 10*time.Second)
	check := func(tag string) {
		_ = typ("hk", `reg query HKCU\Environment & cmdkey /list | findstr /i antigravity & echo FIM-`+tag+"\r")
		_, ok, w := waitFor("hk", `(?m)^FIM-`+tag+`\s*$`, 15*time.Second)
		d.say("- %s: done=%v (%s, deadline 15s)", tag, ok, w)
	}
	check("ANTES")
	if err := stopService(launcherSvc, 15*time.Second); err != nil {
		return err
	}
	if err := startService(launcherSvc); err != nil {
		return err
	}
	d.say("- MestreSpikeLauncher stopped and started")
	check("DEPOIS")
	d.block("tela de hk", clean(screen("hk")))
	d.stop("hk", "kill", true)
	return nil
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[@-Z\\-_]|\x1b[()][0-9A-Za-z]`)

func strip(b []byte) string { return ansiRE.ReplaceAllString(string(b), "") }

// Item 6 (socket part): a mestre-agente process outside the terminal and
// one inside another terminal try the socket; an engine with the wrong
// secret tries the handshake.

func (d *drv) isolate() error {
	a, err := d.open("is-a", cmdExe, []string{"/q", "/k"}, []string{"PROMPT=$G$S"}, false)
	if err != nil {
		return err
	}
	if _, err := d.open("is-b", cmdExe, []string{"/q", "/k"}, []string{"PROMPT=$G$S"}, false); err != nil {
		return err
	}
	waitFor("is-b", `>`, 10*time.Second)
	st, _ := status("is-a")
	d.say("- socket of is-a: %s, SD %s", a.Sock, st.SocketSD)

	outside := filepath.Join(workDir, "sockcheck-outside.json")
	inside := filepath.Join(workDir, "sockcheck-inside.json")
	_ = os.Remove(outside)
	_ = os.Remove(inside)
	out, _ := exec.Command(filepath.Join(binDir, "spikesvc.exe"), "open", "-svc", "launcher", "-deadline", "60s", "--",
		exePath, "helper", "sockcheck", "-sock", a.Sock, "-out", outside).CombinedOutput()
	d.say("\n### mestre-agente fora do terminal (iniciado pelo launcher)\n")
	d.say("```\n%s\n```", readOr(outside, string(out)))

	_ = typ("is-b", exePath+" helper sockcheck -sock "+a.Sock+" -out "+inside+"\r")
	start := time.Now()
	for time.Since(start) < 30*time.Second {
		if _, err := os.Stat(inside); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	d.say("\n### mestre-agente dentro de outro terminal (is-b), esperou %v (prazo 30s)\n", time.Since(start).Round(time.Millisecond))
	d.say("```\n%s\n```", readOr(inside, clean(screen("is-b"))))

	d.say("\n### Motor falso com o segredo errado\n")
	r, err := ctl(control{Op: "badsecret", ID: "is-a"})
	for _, l := range r.Log {
		d.say("- %s", l)
	}
	if err != nil {
		d.say("- error: %v", err)
	}
	_ = typ("is-a", "echo ainda-funciona\r")
	_, ok, w := waitFor("is-a", "\nainda-funciona", 10*time.Second)
	d.say("- the real engine still uses is-a afterwards: %v (%s)", ok, w)
	st, _ = status("is-a")
	d.say("- is-a socket SD afterwards: %s", st.SocketSD)
	d.say("\n### Acesso ao processo do ptyhost\n\n- não executado nesta rodada (aguarda confirmação do Dono)")
	for _, id := range []string{"is-a", "is-b"} {
		d.stop(id, "kill", true)
	}
	return nil
}

// Item 6 (d): which rights a mestre-agente process gets on the ptyhost
// process, from outside any terminal and from inside another terminal. If
// PROCESS_VM_READ or PROCESS_DUP_HANDLE is granted, plan B: the same checks
// against a ptyhost started with -harden.
func (d *drv) procAccess() error {
	if _, err := d.open("pa-b", cmdExe, []string{"/q", "/k"}, []string{"PROMPT=$G$S"}, false); err != nil {
		return err
	}
	waitFor("pa-b", `>`, 10*time.Second)
	targets := []struct {
		id     string
		harden bool
	}{{"pa-a", false}, {"pa-h", true}}
	for _, tg := range targets {
		d.say("\n## Alvo %s (harden=%v)\n", tg.id, tg.harden)
		rec, err := d.open(tg.id, cmdExe, []string{"/q", "/k"}, []string{"PROMPT=$G$S"}, tg.harden)
		if err != nil {
			return err
		}
		st, _ := status(tg.id)
		if st.Harden != "" {
			d.say("- harden: %s", st.Harden)
		}
		granted := false
		for _, where := range []string{"fora", "dentro"} {
			out := filepath.Join(workDir, fmt.Sprintf("procaccess-%s-%s.json", tg.id, where))
			_ = os.Remove(out)
			args := []string{exePath, "helper", "procaccess", "-pid", fmt.Sprint(rec.HostPID), "-out", out}
			start := time.Now()
			if where == "fora" {
				_, _ = exec.Command(filepath.Join(binDir, "spikesvc.exe"), append([]string{"open", "-svc", "launcher", "-deadline", "60s", "--"}, args...)...).CombinedOutput()
			} else {
				_ = typ("pa-b", windows.ComposeCommandLine(args)+"\r")
				for time.Since(start) < 30*time.Second {
					if _, err := os.Stat(out); err == nil {
						break
					}
					time.Sleep(200 * time.Millisecond)
				}
			}
			res := readOr(out, clean(screen("pa-b")))
			d.say("\n### mestre-agente %s do terminal (ptyhost pid %d, esperou %v, prazo 30-60s)\n\n```\n%s\n```", where, rec.HostPID, time.Since(start).Round(time.Millisecond), res)
			var m map[string]string
			if json.Unmarshal([]byte(res), &m) == nil {
				granted = granted || m["PROCESS_VM_READ"] == "GRANTED" || m["PROCESS_DUP_HANDLE"] == "GRANTED"
			}
		}
		d.say("\n- **%s: PROCESS_VM_READ ou PROCESS_DUP_HANDLE concedido a mestre-agente: %v**", tg.id, granted)
		d.stop(tg.id, "kill", true)
		if !granted {
			d.say("- padrão bastou; plano B (-harden) não é necessário para este item")
			break
		}
	}
	d.stop("pa-b", "kill", true)
	return nil
}

func readOr(path, fallback string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(no results file: " + err.Error() + ")\n" + fallback
	}
	return string(b)
}

// Item 7: TerminateJobObject by the ptyhost, and TerminateProcess of the
// ptyhost by the engine, with a grandchild that ignores Ctrl-C and another
// with its own console.

func (d *drv) kill() error {
	for _, v := range []struct{ id, how string }{{"k-job", "TerminateJobObject pelo ptyhost"}, {"k-host", "TerminateProcess do ptyhost pelo motor"}} {
		d.say("\n## %s (%s)\n", v.id, v.how)
		rec, err := d.open(v.id, cmdExe, []string{"/q", "/k"}, []string{"PROMPT=$G$S"}, false)
		if err != nil {
			return err
		}
		waitFor(v.id, `>`, 10*time.Second)
		_ = typ(v.id, `start "" /b `+exePath+` helper stay -label sem-ctrlc -ignore-ctrlc`+"\r")
		_ = typ(v.id, `start "" `+exePath+` helper stay -label console-proprio`+"\r")
		st, ok, w := waitStatus(v.id, 20*time.Second, func(s *hostStatus) bool { return hasImage(s.Job, "mestrepty.exe", 2) })
		d.say("- both grandchildren in the job: %v (%s, deadline 20s)", ok, w)
		_ = typ(v.id, "\x03")
		st, _, _ = waitStatus(v.id, 3*time.Second, func(s *hostStatus) bool { return false })
		d.say("- after \\x03 (waited 3s): job = [%s]; same logon session outside the job = [%s]", psList(st.Job), psList(st.OutsideJob))
		tree := append(append([]procInfo(nil), st.Job...), st.OutsideJob...)
		if v.id == "k-job" {
			r := d.stop(v.id, "kill", false)
			if r == nil {
				continue
			}
		} else {
			r, err := ctl(control{Op: "killhost", ID: v.id})
			for _, l := range r.Log {
				d.say("- %s", l)
			}
			if err != nil {
				d.say("- killhost: %v", err)
			}
			tree = append(tree, procInfo{PID: rec.HostPID, Created: rec.HostCreated, Image: "ptyhost.exe"})
		}
		left, w := waitGone(tree, 10*time.Second)
		d.say("- **processes of the tree still running after %v (deadline 10s): %d [%s]**", w, len(left), psList(left))
		procs, denied := sessionProcesses(st.LogonSession)
		d.say("- processes left in logon session %s: [%s] (%d tokens not readable by the Administrator)", st.LogonSession, psList(procs), denied)
		_, _ = ctl(control{Op: "forget", ID: v.id})
	}
	return nil
}

// Item 8: identification of the caller through the ptyhost's job.

type identCase struct {
	name, term, line string
	outer            []string
	label, expect    string // expect: terminal id, "" = refuse
}

func identCases() []identCase {
	p := exePath
	return []identCase{
		{"direto", "id-t1", p + " helper probe -label direct", nil, "direct", "id-t1"},
		{"cmd /c", "id-t1", "cmd /c " + p + " helper probe -label cmd-c", nil, "cmd-c", "id-t1"},
		{"powershell -c", "id-t1", `powershell -NoProfile -c "& '` + p + `' helper probe -label ps-c"`, nil, "ps-c", "id-t1"},
		{"Job aninhado", "id-t1", p + " helper probe -label nestjob -nestjob", nil, "nestjob", "id-t1"},
		{"órfão: start /b de um cmd que sai", "id-t1", `cmd /c start "" /b ` + p + " helper probe -label orphan-start-b -wait-parent", nil, "orphan-start-b", "id-t1"},
		{"órfão: Start-Process de um powershell que sai", "id-t1", `powershell -NoProfile -c "Start-Process -NoNewWindow -FilePath '` + p + `' -ArgumentList 'helper','probe','-label','orphan-ps','-wait-parent'"`, nil, "orphan-ps", "id-t1"},
		{"CREATE_BREAKAWAY_FROM_JOB (filho)", "id-t1", p + " helper probe -label breakaway -breakaway", nil, "breakaway", "id-t1"},
		{"WMI (Win32_Process.Create)", "id-t1", `powershell -NoProfile -c "Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{CommandLine='` + p + ` helper probe -label wmi'} | Out-Null"`, nil, "wmi", ""},
		{"direto no T2", "id-t2", p + " helper probe -label direct-t2", nil, "direct-t2", "id-t2"},
		{"fora: console de Administrador", "", "", []string{p, "helper", "probe", "-label", "outside-admin"}, "outside-admin", ""},
		{"fora: mestre-agente pelo launcher", "", "", []string{filepath.Join(binDir, "spikesvc.exe"), "open", "-svc", "launcher", "-deadline", "60s", "--", p, "helper", "probe", "-label", "outside-agent"}, "outside-agent", ""},
	}
}

func (d *drv) ident() error {
	for _, id := range []string{"id-t1", "id-t2"} {
		if _, err := d.open(id, cmdExe, []string{"/q", "/k"}, []string{"PROMPT=$G$S"}, false); err != nil {
			return err
		}
		waitFor(id, `>`, 10*time.Second)
	}
	_, _ = ctl(control{Op: "clearprobes"})
	d.say("\n| caso | esperado | resultado | PID pelo socket = PID do processo | respostas dos ptyhost | custo (µs) | nota |\n|---|---|---|---|---|---|---|")
	for _, c := range identCases() {
		if c.term != "" {
			_ = typ(c.term, c.line+"\r")
		} else {
			_, _ = exec.Command(c.outer[0], c.outer[1:]...).CombinedOutput()
		}
		r, _ := ctl(control{Op: "probes", Labels: []string{c.label}, TimeoutS: 40})
		res, ok := r.Probes[c.label]
		exp := c.expect
		if exp == "" {
			exp = "recusa"
		}
		if !ok {
			d.say("| %s | %s | não chamou (%s, prazo 40s) | - | - | - | |", c.name, exp, r.Waited)
			continue
		}
		got := res.Terminal
		verdict := "ok"
		switch {
		case got == c.expect && got == "":
			got, verdict = "recusa", "ok (recusou)"
		case got == "":
			got, verdict = "recusa", "recusou"
		case got != c.expect:
			verdict = "**ERRADO**"
		}
		d.say("| %s | %s | %s: %s | %v (%d) | %s | %.0f | %s %s |", c.name, exp, verdict, got, res.PeerPID == res.SelfPID, res.PeerPID,
			strings.ReplaceAll(fmt.Sprint(res.Answers), "|", "/"), res.CostMicro, strings.ReplaceAll(res.Err, "|", "/"), strings.ReplaceAll(res.Note, "|", "/"))
	}
	for _, id := range []string{"id-t1", "id-t2"} {
		d.block("tela de "+id, clean(screen(id)))
		d.stop(id, "kill", true)
	}
	return nil
}

// Item 9: a process created through WMI from inside the terminal.

func (d *drv) wmi() error {
	if _, err := d.open("w1", cmdExe, []string{"/q", "/k"}, []string{"PROMPT=$G$S"}, false); err != nil {
		return err
	}
	waitFor("w1", `>`, 10*time.Second)
	st0, _ := status("w1")
	d.say("- terminal logon session %s", st0.LogonSession)
	before := snapshot()
	_ = typ("w1", `powershell -NoProfile -c "Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{CommandLine='`+exePath+` helper stay -label wmi -seconds 300'}"`+"\r")
	// Find the WMI-created process: a new mestrepty.exe that is not in the job.
	var wmiProc *procInfo
	start := time.Now()
	for time.Since(start) < 30*time.Second && wmiProc == nil {
		for pid, p := range snapshot() {
			if _, old := before[pid]; !old && strings.EqualFold(p.Image, "mestrepty.exe") {
				p := p
				wmiProc = &p
			}
		}
		if wmiProc == nil {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if wmiProc == nil {
		d.block("tela de w1", clean(screen("w1")))
		return fmt.Errorf("no WMI-created process in %v", time.Since(start).Round(time.Millisecond))
	}
	parent := snapshot()[wmiProc.PPID]
	ls, lerr := processLogonSession(wmiProc.PID)
	d.say("- WMI process: pid %d, parent %d %s (found after %v, deadline 30s)", wmiProc.PID, wmiProc.PPID, parent.Image, time.Since(start).Round(time.Millisecond))
	d.say("- **logon session of the WMI process (read by the Administrator): %s (err %v); terminal: %s; same: %v**", ls, lerr, st0.LogonSession, ls == st0.LogonSession)
	st, _ := status("w1")
	inJob := false
	for _, p := range st.Job {
		inJob = inJob || p.PID == wmiProc.PID
	}
	d.say("- in the terminal's job: %v; the ptyhost lists it in its logon session outside the job: %v ([%s])", inJob, slices.ContainsFunc(st.OutsideJob, func(p procInfo) bool { return p.PID == wmiProc.PID }), psList(st.OutsideJob))
	d.block("tela de w1", clean(screen("w1")))
	r := d.stop("w1", "kill", true)
	if r != nil {
		d.say("- ended by the stop: [%s]", psList(r.Killed))
	}
	left, w := waitGone([]procInfo{*wmiProc}, 10*time.Second)
	d.say("- **WMI process still running after the end of the terminal (%v, deadline 10s): %v**", w, len(left) > 0)
	procs, denied := sessionProcesses(st0.LogonSession)
	d.say("- processes left in logon session %s: [%s] (%d tokens not readable)", st0.LogonSession, psList(procs), denied)
	if len(left) > 0 {
		for _, p := range left {
			if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, p.PID); err == nil {
				_ = windows.TerminateProcess(h, 1)
				windows.CloseHandle(h)
			}
		}
		d.say("- (driver ended the leftover WMI process)")
	}
	return nil
}
