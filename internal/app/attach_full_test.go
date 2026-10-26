//go:build full

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/attach"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// usersBox is where panelChain leaves the users service of the Citadel's period.
type usersBox struct{ svc atomic.Pointer[users.Service] }

// panelChain is the chain integration with a panel: the Citadel serves the panel
// over the users it makes, so that a test can log in to it.
type panelChain struct{ box *usersBox }

func (p panelChain) Open(ctx context.Context, env IntegrationEnv) (IntegrationRun, error) {
	us, err := users.New(users.Options{DB: env.DB, Clock: env.Clock, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		return nil, err
	}
	p.box.svc.Store(us)
	return panelChainRun{chainRun{users: us}}, nil
}

type panelChainRun struct{ chainRun }

func (r panelChainRun) PanelPieces() (*users.Service, users.Alerts) { return r.users, refreshAlerts{} }

// attachPass is the password of every user of these tests.
const attachPass = "uma senha bem longa"

// attachWorld is a Citadel with a panel, a Tower with the fake agent of a character, and
// users of every level that `mestre attach` logs in as.
type attachWorld struct {
	*channelRig
	box   *usersBox
	owner users.User
	c     characters.Character
}

func newAttachWorld(t *testing.T) *attachWorld {
	t.Helper()
	box := &usersBox{}
	x := newChannelRigWith(t, channelOpts{panel: box})
	w := &attachWorld{channelRig: x, box: box}
	x.r.waitFor("the users of the Citadel", func() bool { return box.svc.Load() != nil })
	owner, err := box.svc.Load().CreateOwner(t.Context(), "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	w.owner = owner
	w.c = x.character(t, "c1", "Dev-1", characters.Starting)
	if err := x.startOnTower(t.Context(), w.c); err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("the character idle", func() bool { return x.state(t, w.c.ID) == characters.Idle })
	// the agent says something, so that there is a screen to see
	it, err := x.orc.Inbox.Enqueue(t.Context(), inbox.NewItem{Workspace: "meu-app", Target: w.c.ID, Kind: inbox.KindAgent, Sender: "Mestre", Text: "passo MARCA01Z"})
	if err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("the turn to end", func() bool {
		_ = x.orc.Inbox.Pump(t.Context(), w.c.ID)
		cur, err := x.orc.Inbox.Store().Get(t.Context(), it.ID)
		return err == nil && cur.State == inbox.Delivered && x.state(t, w.c.ID) == characters.Idle && strings.Contains(x.towerOutput(t, w.c.ID), "MARCA01Z")
	})
	return w
}

// user makes a user of the level in the workspace and returns the login.
func (w *attachWorld) user(t *testing.T, login string, level users.Level) users.User {
	t.Helper()
	svc := w.box.svc.Load()
	u, err := svc.Create(t.Context(), w.owner, users.NewUser{Name: login, Level: level, Workspaces: []string{"meu-app"}})
	if err != nil {
		t.Fatal(err)
	}
	code, err := svc.NewAccessCode(t.Context(), w.owner, u.ID, users.AccessFirst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RedeemAccessCode(t.Context(), code.Code, users.SetupInput{Login: login, Password: attachPass, PIN: "654321"}); err != nil {
		t.Fatal(err)
	}
	return u
}

// attachConsole is the terminal of a person: it answers the login, records what is
// drawn, and the test types into Keys.
type attachConsole struct {
	login, password string
	answers         []string // answers to ReadLine after the login
	keys            *io.PipeWriter
	in              *io.PipeReader

	mu   sync.Mutex
	out  bytes.Buffer
	asks []string
}

func newAttachConsole(login string, answers ...string) *attachConsole {
	r, w := io.Pipe()
	return &attachConsole{login: login, password: attachPass, answers: answers, in: r, keys: w}
}

func (c *attachConsole) Interactive() bool { return true }
func (c *attachConsole) ReadLine(p string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asks = append(c.asks, p)
	if strings.HasPrefix(p, "login") {
		return c.login, nil
	}
	if len(c.answers) == 0 {
		return "", io.EOF
	}
	a := c.answers[0]
	c.answers = c.answers[1:]
	return a, nil
}
func (c *attachConsole) ReadPassword(string) (string, error) { return c.password, nil }
func (c *attachConsole) Size() (int, int)                    { return 120, 30 }
func (c *attachConsole) EnterRaw() (func(), error)           { return func() { c.keys.Close() }, nil }
func (c *attachConsole) Input() io.Reader                    { return c.in }
func (c *attachConsole) Output() io.Writer {
	return writerFunc(func(p []byte) (int, error) { c.mu.Lock(); defer c.mu.Unlock(); return c.out.Write(p) })
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// type_ types keys, as a person does.
func (c *attachConsole) type_(keys []byte) {
	if _, err := c.keys.Write(keys); err != nil {
		panic(err)
	}
}

func (c *attachConsole) screen() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.String()
}

func (c *attachConsole) asked(prefix string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.asks {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

// attached is a `mestre attach` running.
type attached struct {
	con    *attachConsole
	code   chan int
	stdout *syncBuffer
	stderr *syncBuffer
}

// attach runs `mestre attach` as the person of the console, through the Citadel (or the
// Tower, to follow its 307), over the fake network.
func (w *attachWorld) attach(t *testing.T, con *attachConsole, via string, args ...string) *attached {
	t.Helper()
	w.r.ips++
	w.r.net.SetIP("cli", fmt.Sprintf("10.0.0.%d", w.r.ips))
	dial := w.r.net.DialContext("cli")
	env := attach.Env{
		Stdout: &syncBuffer{}, Stderr: &syncBuffer{}, Console: con, Clock: clock.Real{},
		LookupEnv: func(string) (string, bool) { return "", false },
		Hostname:  "notebook", Retry: 3 * time.Second,
		Folders: func(_, dir string) (string, int, error) { return filepath.Join(dir, "config"), 7443, nil },
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// the names of the machines are <name>.local and the one of the Citadel mestre.local (mDNS
			// is not in the fake network): they reach the machine of the same name, and the Citadel is a
			host, port, _ := net.SplitHostPort(addr)
			if host == "mestre.local" {
				host = "a.local"
			}
			addr = net.JoinHostPort(strings.TrimSuffix(host, ".local"), port)
			return dial(ctx, network, addr)
		},
	}
	a := &attached{con: con, code: make(chan int, 1), stdout: env.Stdout.(*syncBuffer), stderr: env.Stderr.(*syncBuffer)}
	all := append([]string{"--dir", filepath.Dir(w.a.paths.Config), "--server", via + ":7443"}, args...)
	go func() { a.code <- attach.Run(t.Context(), env, all) }()
	return a
}

// exit waits for the end of the command and returns its exit code.
func (a *attached) exit(t *testing.T) int {
	t.Helper()
	select {
	case c := <-a.code:
		return c
	case <-clock.Real{}.NewTimer(30 * time.Second).C():
		t.Fatalf("mestre attach did not end; stderr %q", a.stderr.b.String())
		return -1
	}
}

func (w *attachWorld) audit(t *testing.T, action string) []string {
	t.Helper()
	rows, err := orcDB(t, w.a).Reader().QueryContext(t.Context(), `SELECT detail FROM audit_log WHERE action = ? ORDER BY at`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// CA-ATTACH-01
// An Approver runs `mestre attach` on a character with the fake agent on a Tower: the
// screen it draws is the one of the agent, the keys it types reach nothing, and Ctrl-]
// ends it with 0 and the agent in the same state with the same PID. It also reaches the
// Citadel through the Tower, following the 307.
func TestAnApproverSeesTheScreenOfTheAgentAndDetachesWithoutTouchingIt(t *testing.T) {
	t.Parallel()
	w := newAttachWorld(t)
	w.user(t, "bia", users.Approver)
	before, err := w.orc.Registry.Get(t.Context(), w.c.ID)
	if err != nil {
		t.Fatal(err)
	}

	for _, via := range []string{"mestre.local", "b.local"} { // the Citadel, then a Tower that sends it there
		con := newAttachConsole("bia")
		run := w.attach(t, con, via, "Dev-1")
		w.r.waitFor("the screen of the agent", func() bool { return strings.Contains(con.screen(), "MARCA01Z") })
		if !strings.Contains(con.screen(), "terminal 200x50") {
			t.Fatalf("no line of state with the size:\n%q", con.screen())
		}
		// keys of a read-only attach go nowhere
		con.type_([]byte("NAOTECLAZ\r"))
		con.type_([]byte{0x1d})
		if code := run.exit(t); code != 0 {
			t.Fatalf("via %s: exit %d, stderr %q", via, code, run.stderr.b.String())
		}
		if strings.Contains(w.towerOutput(t, w.c.ID), "NAOTECLAZ") {
			t.Fatalf("via %s: a key reached the agent", via)
		}
	}
	after, err := w.orc.Registry.Get(t.Context(), w.c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != before.State || after.PTY.PID != before.PTY.PID || after.PTY.PID == 0 {
		t.Fatalf("the agent changed: %v pid %d -> %v pid %d", before.State, before.PTY.PID, after.State, after.PTY.PID)
	}
	if got := w.audit(t, "panel.terminal_typing_started"); len(got) != 0 {
		t.Fatalf("a read-only attach has a typing session: %v", got)
	}
	// the session lived only for the command: the user has none left on the Citadel
	if n := countSessions(t, w, "bia"); n != 0 {
		t.Fatalf("%d sessions of the CLI left", n)
	}
}

func countSessions(t *testing.T, w *attachWorld, login string) int {
	t.Helper()
	var n int
	err := orcDB(t, w.a).Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM panel_sessions s JOIN users u ON u.id = s.user_id WHERE u.login = ?`, login).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// CA-ATTACH-02
// The character is busy: an Administrator runs `mestre attach --write`, is warned that
// the agent is working, confirms, types, and what was typed reaches the agent; the audit
// has the typing session with start and end and not a byte of what was typed. An Approver
// with --write exits 3 level_denied and nothing is typed.
func TestAnAdministratorTypesIntoABusyAgentAndItIsAuditedWithoutTheContent(t *testing.T) {
	t.Parallel()
	w := newAttachWorld(t)
	w.user(t, "ana", users.Administrator)
	w.user(t, "bia", users.Approver)

	// an Approver may not type
	bia := newAttachConsole("bia", "s")
	if code := w.attach(t, bia, "mestre.local", "Dev-1", "--write").exit(t); code != 3 {
		t.Fatalf("approver --write: exit %d, want 3", code)
	}
	if bia.asked("o agente") {
		t.Fatal("an Approver was asked to confirm typing")
	}
	if got := w.audit(t, "panel.terminal_typing_started"); len(got) != 0 {
		t.Fatalf("typing sessions: %v", got)
	}

	if _, err := orcDB(t, w.a).Writer().ExecContext(t.Context(), `UPDATE characters SET state = 'busy' WHERE id = ?`, w.c.ID); err != nil {
		t.Fatal(err)
	}
	ana := newAttachConsole("ana", "s")
	run := w.attach(t, ana, "mestre.local", "Dev-1", "--write")
	w.r.waitFor("the screen of the agent", func() bool { return strings.Contains(ana.screen(), "MARCA01Z") })
	if !ana.asked("o agente está trabalhando") {
		t.Fatalf("the warning was not shown; asked %v", ana.asks)
	}
	const marker = "DIGITADOZ"
	ana.type_([]byte(marker + "\r"))
	w.r.waitFor("what was typed to reach the agent", func() bool { return strings.Contains(w.towerOutput(t, w.c.ID), marker) })
	ana.type_([]byte{0x1d})
	if code := run.exit(t); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, run.stderr.b.String())
	}
	w.r.waitFor("the end of the typing session in the audit", func() bool { return len(w.audit(t, "panel.terminal_typing_ended")) == 1 })
	started, ended := w.audit(t, "panel.terminal_typing_started"), w.audit(t, "panel.terminal_typing_ended")
	if len(started) != 1 || !strings.Contains(started[0], `"character":"c1"`) || !strings.Contains(started[0], "started_at") || !strings.Contains(ended[0], "ended_at") {
		t.Fatalf("audit: %v / %v", started, ended)
	}
	for _, line := range append(started, ended...) {
		if strings.Contains(line, marker) {
			t.Fatalf("the content is in the audit: %s", line)
		}
	}
}

// CA-ATTACH-05
// An attach is open; the Owner suspends the user: the Citadel closes the connection and
// the command exits 3.
func TestSuspendingTheUserDropsTheAttachAtOnce(t *testing.T) {
	t.Parallel()
	w := newAttachWorld(t)
	bia := w.user(t, "bia", users.Approver)
	con := newAttachConsole("bia")
	run := w.attach(t, con, "mestre.local", "Dev-1")
	w.r.waitFor("the screen of the agent", func() bool { return strings.Contains(con.screen(), "MARCA01Z") })
	if err := w.box.svc.Load().Suspend(t.Context(), w.owner, bia.ID); err != nil {
		t.Fatal(err)
	}
	if code := run.exit(t); code != 3 {
		t.Fatalf("exit %d, want 3; stderr %q", code, run.stderr.b.String())
	}
	if !strings.Contains(run.stderr.b.String(), "session") {
		t.Fatalf("stderr %q", run.stderr.b.String())
	}
}

// The login of the CLI has the rules of the panel: one answer for every refusal, and a
// character that does not exist (or that the user does not see) is not found.
func TestAttachRefusesAWrongPasswordAndACharacterNobodySees(t *testing.T) {
	t.Parallel()
	w := newAttachWorld(t)
	w.user(t, "bia", users.Approver)

	wrong := newAttachConsole("bia")
	wrong.password = "outra senha bem longa"
	run := w.attach(t, wrong, "mestre.local", "Dev-1")
	if code := run.exit(t); code != 3 || !strings.Contains(run.stderr.b.String(), "invalid user or password") {
		t.Fatalf("wrong password: exit %d, stderr %q", code, run.stderr.b.String())
	}
	if n := len(w.audit(t, "panel.login.failed")); n != 1 {
		t.Fatalf("%d failed logins in the audit, want 1", n)
	}
	unknown := newAttachConsole("ninguem")
	if code := w.attach(t, unknown, "mestre.local", "Dev-1").exit(t); code != 3 {
		t.Fatalf("unknown user: exit %d", code)
	}
	for _, args := range [][]string{{"Fantasma"}, {"Dev-1", "--workspace", "outro"}} {
		con := newAttachConsole("bia")
		if code := w.attach(t, con, "mestre.local", args...).exit(t); code != 4 {
			t.Fatalf("%v: exit %d, want 4", args, code)
		}
	}
	if n := countSessions(t, w, "bia"); n != 0 {
		t.Fatalf("%d sessions left after the failures", n)
	}
}

// A lost connection is retried for the time set and then the command exits 5, with the
// agent untouched.
func TestALostConnectionIsRetriedAndThenExits5(t *testing.T) {
	t.Parallel()
	w := newAttachWorld(t)
	w.user(t, "bia", users.Approver)
	con := newAttachConsole("bia")
	run := w.attach(t, con, "mestre.local", "Dev-1")
	w.r.waitFor("the screen of the agent", func() bool { return strings.Contains(con.screen(), "MARCA01Z") })
	p := w.a.e.panel.Load()
	w.a.e.panel.Store(nil) // the panel is gone: the Citadel answers 503
	p.Close()
	if code := run.exit(t); code != 5 {
		t.Fatalf("exit %d, want 5; stderr %q", code, run.stderr.b.String())
	}
	if !strings.Contains(con.screen(), "connection lost") {
		t.Fatal("the line of state did not say the connection was lost")
	}
}

// A terminal that ends shows "terminal encerrado" and the command exits 0.
func TestATerminalThatEndsExitsZeroAndSaysSo(t *testing.T) {
	t.Parallel()
	w := newAttachWorld(t)
	w.user(t, "bia", users.Approver)
	con := newAttachConsole("bia")
	run := w.attach(t, con, "mestre.local", "Dev-1")
	w.r.waitFor("the screen of the agent", func() bool { return strings.Contains(con.screen(), "MARCA01Z") })
	if err := w.a.e.mach.Load().Stop(t.Context(), w.b.id, characters.StopCommand{Character: w.c.ID, Final: true, Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	if code := run.exit(t); code != 0 || !strings.Contains(run.stdout.b.String(), "terminal encerrado") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, run.stdout.b.String(), run.stderr.b.String())
	}
}
