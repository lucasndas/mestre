//go:build full

package app_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/testkit/clocksock"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The change of Citadel in the full suite (docs/qualidade/testes.md#troca-de-cidadela-e-replicação-na-completa):
// three instances of the same computer as ordinary processes, each with its
// folders, its port, its databases and its certificates, joined by TCP on
// 127.0.0.1, with the clock of the test in every process (MESTRE_TEST_CLOCK),
// the fake lock and the fake Owner (testhooks_full.go). The processes run
// what `mestre run` runs: this test binary, started again with
// MESTRE_TEST_RUN_ENGINE, calls app.Main.

func TestMain(m *testing.M) {
	if os.Getenv("MESTRE_TEST_RUN_ENGINE") == "1" && len(os.Args) > 1 && os.Args[1] == "run" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		code := app.Main(ctx, os.Args[2:], os.Stderr, os.LookupEnv, "test")
		stop()
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func suite() string { return os.Getenv("MESTRE_TEST_SUITE") }

// world is the computer of the test: the clock, the lock, and the instances.
type world struct {
	t     *testing.T
	root  string
	clk   *clocksock.Server
	lock  string
	ext   string
	insts map[string]*proc
}

// proc is one instance and its process.
type proc struct {
	name  string
	dir   string
	cfg   app.Config
	cmd   *exec.Cmd
	log   string
	ctl   string
	runs  int
	alive bool
}

func newWorld(t *testing.T) *world {
	t.Helper()
	// A short folder: the sockets of the clock and of the control must fit
	// the limit of a Unix socket path.
	root, err := os.MkdirTemp("", "msw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	w := &world{t: t, root: root, lock: filepath.Join(root, "lock"), ext: filepath.Join(root, "ext"), insts: map[string]*proc{}}
	for _, d := range []string{w.lock, w.ext} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if w.clk, err = clocksock.Start(filepath.Join(root, "clk.sock"), clock.Real{}.Now().Truncate(time.Second)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.clk.Close)
	t.Cleanup(func() {
		for _, p := range w.insts {
			w.kill(p)
			b, _ := os.ReadFile(p.log)
			if strings.Contains(string(b), "WARNING: DATA RACE") {
				t.Errorf("the process %s found a data race", p.name)
			}
			if t.Failed() {
				t.Logf("---- log of %s ----\n%s", p.name, tail(string(b), 120))
			}
		}
	})
	return w
}

func tail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// freePorts finds n free TCP ports on the loopback.
func freePorts(t *testing.T, n int) []int {
	t.Helper()
	sys := transport.SystemNetwork{Suite: suite()}
	var out []int
	var lns []net.Listener
	for range n {
		ln, err := sys.Listen("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		lns = append(lns, ln)
		out = append(out, ln.Addr().(*net.TCPAddr).Port)
	}
	for _, ln := range lns {
		ln.Close()
	}
	return out
}

func (w *world) add(name string, port int, others []string) *proc {
	dir := filepath.Join(w.root, name)
	p := &proc{
		name: name, dir: dir, log: filepath.Join(w.root, name+".log"), ctl: filepath.Join(w.root, name+".ctl"),
		cfg: app.Config{
			Instance: name, Name: name, Hostname: "localhost", IPs: []string{"127.0.0.1"}, Bind: "127.0.0.1",
			EnginePort: port, NoDiscovery: true, Addresses: others, Synchronous: "OFF",
		},
	}
	w.insts[name] = p
	return p
}

func (p *proc) addr() string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(p.cfg.EnginePort)) }

func (w *world) start(p *proc) {
	w.t.Helper()
	f, err := os.OpenFile(p.log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		w.t.Fatal(err)
	}
	defer f.Close()
	p.runs++
	fmt.Fprintf(f, "==== run %d ====\n", p.runs)
	cmd := exec.Command(os.Args[0], "run", "--dir", p.dir)
	cmd.Env = append(os.Environ(),
		"MESTRE_TEST_RUN_ENGINE=1",
		"MESTRE_TEST_CLOCK="+filepath.Join(w.root, "clk.sock"),
		"MESTRE_TEST_LOCK="+w.lock,
		"MESTRE_TEST_OWNER=accept",
		"MESTRE_TEST_EXTERNAL="+w.ext,
		"MESTRE_TEST_CONTROL="+p.ctl,
	)
	cmd.Stdout, cmd.Stderr = f, f
	if err := cmd.Start(); err != nil {
		w.t.Fatal(err)
	}
	p.cmd, p.alive = cmd, true
}

func (w *world) kill(p *proc) {
	if !p.alive {
		return
	}
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
	p.alive = false
}

// answer is the line of the control of the test.
type answer struct {
	OK     bool       `json:"ok"`
	Error  string     `json:"error"`
	Status app.Status `json:"status"`
	LSN    uint64     `json:"lsn"`
	Rows   [][]any    `json:"rows"`
	// Call is the response of a command of the CLI, op call.
	Call *localchan.Response `json:"call"`
	// Request is the approval request of op approval.
	Request int64 `json:"request"`
}

func (w *world) ask(p *proc, req map[string]string) (answer, error) {
	ctx, cancel := clock.WithTimeout(w.t.Context(), clock.Real{}, 10*time.Second)
	defer cancel()
	c, err := localchan.UnixDialer(p.ctl)(ctx)
	if err != nil {
		return answer{}, err
	}
	defer c.Close()
	if d, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(d)
	}
	b, _ := json.Marshal(req)
	if _, err := c.Write(append(b, '\n')); err != nil {
		return answer{}, err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return answer{}, err
	}
	var a answer
	if err := json.Unmarshal(line, &a); err != nil {
		return answer{}, err
	}
	if !a.OK {
		return a, fmt.Errorf("%s: %s", req["op"], a.Error)
	}
	return a, nil
}

func (w *world) status(p *proc) (app.Status, bool) {
	a, err := w.ask(p, map[string]string{"op": "status"})
	return a.Status, err == nil && a.Status.MachineID != ""
}

func (w *world) must(p *proc, req map[string]string) answer {
	w.t.Helper()
	a, err := w.ask(p, req)
	if err != nil {
		w.t.Fatalf("%s: %v", p.name, err)
	}
	return a
}

// marks lists the rows the test wrote, as an instance has them.
func (w *world) marks(p *proc) []string {
	w.t.Helper()
	a := w.must(p, map[string]string{"op": "query", "sql": `SELECT id FROM audit_log WHERE action = 'test.mark' ORDER BY id`})
	var out []string
	for _, r := range a.Rows {
		out = append(out, fmt.Sprint(r[0]))
	}
	return out
}

// mark writes one row on the Citadel and returns the lsn after it.
func (w *world) mark(p *proc, id string) uint64 {
	w.t.Helper()
	return w.must(p, map[string]string{"op": "exec", "sql": fmt.Sprintf(
		`INSERT INTO audit_log (id, at, machine_id, action, detail) VALUES ('%s', 0, 'test', 'test.mark', '{}')`, id)}).LSN
}

// waitFor polls until ok, advancing the clock of every process by step after
// each poll that fails (zero leaves the clock alone).
func (w *world) waitFor(what string, step time.Duration, ok func() bool) {
	w.t.Helper()
	deadline := clock.Real{}.Now().Add(60 * time.Second)
	for !ok() {
		if (clock.Real{}).Now().After(deadline) {
			w.t.Fatalf("timed out waiting for %s (test clock at %s)", what, w.clk.Now().Format(time.TimeOnly))
		}
		if step > 0 {
			// a process that is down does not answer: logged, the wait goes on
			if err := w.clk.Advance(step); err != nil {
				w.t.Logf("clock: %v", err)
			}
		}
		_ = clock.Sleep(w.t.Context(), clock.Real{}, 15*time.Millisecond)
	}
}

// waitForCitadel waits until p acts as the Citadel and takes entries: the
// routes of the entry are mounted after the Citadel opens its CA, which is a
// moment after the fence says it acts.
func (w *world) waitForCitadel(p *proc) {
	w.t.Helper()
	w.waitFor(p.name+" to act as the Citadel and open the entries", 0, func() bool {
		st, ok := w.status(p)
		if !ok || st.Role != app.RoleCitadel || !st.Acting {
			return false
		}
		_, err := w.ask(p, map[string]string{"op": "joinable"})
		return err == nil
	})
}

// externalRuns splits the lines of the fake external function by process run.
func (w *world) externalRuns(p *proc) [][]string {
	b, _ := os.ReadFile(filepath.Join(w.ext, p.name+".external"))
	var runs [][]string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasPrefix(l, "start ") {
			runs = append(runs, nil)
			continue
		}
		if l != "" && len(runs) > 0 {
			runs[len(runs)-1] = append(runs[len(runs)-1], l)
		}
	}
	return runs
}

// Part of CA-CIDADELA-06 and CA-CIDADELA-07 (fecham no M7; falta: a aprovação
// pelo Telegram, o Grão-Mestre retomado, o commit em
// refs/mestre/recuperados/epoca-1/<mesa> e o GitHub falso sem chamadas da
// antiga).
// The change of Citadel with three instances: the Citadel stops for good, a
// Tower is promoted with the approval of the fake Owner and the fake lock, it
// has the data up to the last lsn it applied, the third one follows it, the
// old one comes back as a Tower and lists the transactions the change lost,
// and no two Citadels act outside at once (the fencing).
func TestTheCitadelFallsATowerIsPromotedTheOtherFollowsAndTheOldOneReturnsAsATower(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ports := freePorts(t, 3)
	a := w.add("a", ports[0], nil)
	b := w.add("b", ports[1], nil)
	c := w.add("c", ports[2], nil)
	a.cfg.Addresses = []string{b.addr(), c.addr()}
	b.cfg.Addresses = []string{a.addr(), c.addr()}
	c.cfg.Addresses = []string{a.addr(), b.addr()}
	ctx := t.Context()

	// a creates the Guild; the lock shows its epoch 1
	founded, rk, err := app.Found(ctx, a.cfg, app.DirPaths(a.dir), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	rk.Destroy()
	if err := os.WriteFile(filepath.Join(w.lock, "epoca-1"), []byte(founded.MachineID), 0o600); err != nil {
		t.Fatal(err)
	}
	w.start(a)
	w.waitForCitadel(a)

	// b and c enter, approved by the fake Owner, and connect as Towers
	ids := map[string]string{"a": founded.MachineID}
	for _, p := range []*proc{b, c} {
		j, err := app.Enroll(ctx, p.cfg, app.DirPaths(p.dir), a.addr(), transport.SystemNetwork{Suite: suite()}, clock.Real{}, nil)
		if err != nil {
			t.Fatalf("%s enters: %v", p.name, err)
		}
		ids[p.name] = j.MachineID
		w.start(p)
	}
	connected := func(p *proc, epoch uint64, lsn uint64) func() bool {
		return func() bool {
			st, ok := w.status(p)
			return ok && st.Role == app.RoleTower && st.Connected && st.Epoch == epoch && st.LSN >= lsn
		}
	}

	// three transactions reach everybody
	var lsn3 uint64
	for _, id := range []string{"t1", "t2", "t3"} {
		lsn3 = w.mark(a, id)
	}
	w.waitFor("b and c to apply t1-t3", 0, func() bool { return connected(b, 1, lsn3)() && connected(c, 1, lsn3)() })

	// b stops; two more reach only c; then the Citadel stops for good
	w.kill(b)
	lsn4 := w.mark(a, "t4") // at least the lsn of t4: other writes may come between
	lsn5 := w.mark(a, "t5")
	w.waitFor("c to apply t4-t5", 0, connected(c, 1, lsn5))
	w.kill(a)

	// b comes back, without a Citadel, and the Owner promotes it
	w.start(b)
	w.waitFor("b to run as a Tower", 0, func() bool {
		st, ok := w.status(b)
		return ok && st.Role == app.RoleTower
	})
	w.must(b, map[string]string{"op": "promote"})
	w.waitFor("b to take the command", 500*time.Millisecond, func() bool {
		st, ok := w.status(b)
		return ok && st.Role == app.RoleCitadel && st.Epoch == 2
	})
	if st, _ := w.status(b); st.Acting {
		t.Fatal("the promoted machine acts outside before the wait of 60 s")
	}
	w.waitFor("b to act after the wait and the check of the lock", 500*time.Millisecond, func() bool {
		st, ok := w.status(b)
		return ok && st.Acting
	})
	if got := w.marks(b); !slices.Equal(got, []string{"t1", "t2", "t3"}) {
		t.Errorf("the new Citadel has %v, want the data up to the last lsn it applied (t1-t3)", got)
	}
	from := w.must(b, map[string]string{"op": "query", "sql": `SELECT from_lsn FROM epochs WHERE epoch = 2`})
	if len(from.Rows) != 1 {
		t.Fatalf("epochs of the new Citadel: %v", from.Rows)
	}
	fromLSN := uint64(from.Rows[0][0].(float64))
	if fromLSN < lsn3 || fromLSN >= lsn4 {
		t.Errorf("the epoch 2 starts from the lsn %d; b had applied at least %d and never %d", fromLSN, lsn3, lsn4)
	}

	// c follows the new Citadel and drops what only the old one had
	w.waitFor("c to follow the epoch 2", 500*time.Millisecond, func() bool {
		st, ok := w.status(c)
		bs, _ := w.status(b)
		return ok && st.Connected && st.Epoch == 2 && st.Citadel == ids["b"] && st.LSN >= bs.LSN
	})
	if got := w.marks(c); !slices.Equal(got, []string{"t1", "t2", "t3"}) {
		t.Errorf("c has %v after following the new Citadel, want t1-t3", got)
	}

	// the old Citadel comes back: it steps down without acting and lists
	// what it lost
	w.start(a)
	w.waitFor("a to come back as a Tower of the epoch 2", 500*time.Millisecond, func() bool {
		st, ok := w.status(a)
		return ok && st.Role == app.RoleTower && st.Connected && st.Epoch == 2 && st.Discarded > 0
	})
	if _, err := os.Stat(filepath.Join(a.dir, "banco", "descartado-1.db")); err != nil {
		t.Errorf("the old Citadel did not keep its database: %v", err)
	}
	// The audit names each lost row by table and key: t4 and t5, which only
	// the old Citadel (and c, before the full copy) had.
	var (
		discarded []uint64
		lostKeys  []string
	)
	w.waitFor("the audit of the new Citadel to list the discarded transactions", 500*time.Millisecond, func() bool {
		r := w.must(b, map[string]string{"op": "query", "sql": `SELECT json_extract(detail, '$.lsn'), json_extract(detail, '$.changes') FROM audit_log WHERE action = 'citadel.discarded' AND json_extract(detail, '$.lsn') IS NOT NULL ORDER BY 1`})
		discarded, lostKeys = discarded[:0], lostKeys[:0]
		for _, row := range r.Rows {
			discarded = append(discarded, uint64(row[0].(float64)))
			var changes []struct {
				Table string `json:"table"`
				Key   []any  `json:"key"`
			}
			_ = json.Unmarshal([]byte(fmt.Sprint(row[1])), &changes)
			for _, c := range changes {
				if c.Table == "audit_log" && len(c.Key) == 1 {
					lostKeys = append(lostKeys, fmt.Sprint(c.Key[0]))
				}
			}
		}
		return len(discarded) > 0
	})
	for _, want := range []string{"t4", "t5"} {
		if !slices.Contains(lostKeys, want) {
			t.Errorf("the discarded transactions %v (rows %v) do not list %s", discarded, lostKeys, want)
		}
	}
	for _, k := range lostKeys {
		if k == "t1" || k == "t2" || k == "t3" {
			t.Errorf("%s is listed as discarded, but the new Citadel has it", k)
		}
	}
	for _, l := range discarded {
		if l <= fromLSN {
			t.Errorf("the lsn %d is listed as discarded, but the new Citadel has it (from %d)", l, fromLSN)
		}
	}
	if got := w.marks(a); !slices.Equal(got, []string{"t1", "t2", "t3"}) {
		t.Errorf("the old Citadel has %v as a Tower, want the data of the new one", got)
	}

	// The fencing: the old Citadel acted only in its first run, with the
	// epoch 1; when it came back it never acted. b acted only with the
	// epoch 2, after the old one stopped, and c never.
	ra := w.externalRuns(a)
	if len(ra) != 2 || len(ra[0]) == 0 || !strings.HasPrefix(ra[0][0], "on 1 ") || len(ra[1]) != 0 {
		t.Errorf("the external function of a: %q; want it on with the epoch 1 in the first run and never in the second", ra)
	}
	rb := w.externalRuns(b)
	for i, run := range rb {
		for _, l := range run {
			if !strings.HasPrefix(l, "on 2 ") && !strings.HasPrefix(l, "off 2 ") || i == 0 {
				t.Errorf("the external function of b, run %d: %q; want it only with the epoch 2", i+1, l)
			}
		}
	}
	if len(rb) != 2 || len(rb[1]) == 0 {
		t.Errorf("b never acted as the new Citadel: %q", rb)
	}
	for _, run := range w.externalRuns(c) {
		if len(run) > 0 {
			t.Errorf("c, a Tower all along, acted outside: %q", run)
		}
	}
	for _, p := range []*proc{a, c} {
		if st, _ := w.status(p); st.Role != app.RoleTower || st.Acting {
			t.Errorf("%s is %s (acting %v); only b is the Citadel", p.name, st.Role, st.Acting)
		}
	}
}

// Part of CA-CIDADELA-08 (fecha no M6; falta: o 307 da Torre para a Cidadela
// no painel).
// The planned handover (docs/guilda/cidadela-e-torres.md#troca-planejada):
// the Citadel stops acting outside, waits for the chosen machine to confirm
// its last lsn and tells it to promote itself through the lock; the chosen
// machine takes the command with everything, and the old one becomes a Tower.
func TestAPlannedHandoverPassesTheCommandWithTheData(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ports := freePorts(t, 2)
	a := w.add("a", ports[0], nil)
	b := w.add("b", ports[1], nil)
	a.cfg.Addresses = []string{b.addr()}
	b.cfg.Addresses = []string{a.addr()}
	ctx := t.Context()
	founded, rk, err := app.Found(ctx, a.cfg, app.DirPaths(a.dir), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	rk.Destroy()
	if err := os.WriteFile(filepath.Join(w.lock, "epoca-1"), []byte(founded.MachineID), 0o600); err != nil {
		t.Fatal(err)
	}
	w.start(a)
	w.waitForCitadel(a)
	j, err := app.Enroll(ctx, b.cfg, app.DirPaths(b.dir), a.addr(), transport.SystemNetwork{Suite: suite()}, clock.Real{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	w.start(b)
	lsn := w.mark(a, "t1")
	w.waitFor("b to apply t1", 0, func() bool {
		st, ok := w.status(b)
		return ok && st.Connected && st.LSN >= lsn
	})

	w.must(a, map[string]string{"op": "handover", "target": j.MachineID})
	w.waitFor("b to take the command and act", 500*time.Millisecond, func() bool {
		st, ok := w.status(b)
		return ok && st.Role == app.RoleCitadel && st.Epoch == 2 && st.Acting
	})
	w.waitFor("a to follow b as a Tower", 500*time.Millisecond, func() bool {
		st, ok := w.status(a)
		return ok && st.Role == app.RoleTower && st.Connected && st.Epoch == 2 && st.Citadel == j.MachineID
	})
	if got := w.marks(b); !slices.Equal(got, []string{"t1"}) {
		t.Errorf("the new Citadel has %v, want t1", got)
	}
	if st, _ := w.status(a); st.Acting {
		t.Error("the old Citadel acts outside after the handover")
	}
	// Nothing of the Guild was lost: the old Citadel reports no discarded
	// transaction (the bookkeeping of the protocol does not count).
	report := filepath.Join(a.dir, "banco", "descartado-1.json")
	var r struct {
		Total int      `json:"total"`
		LSNs  []uint64 `json:"lsns"`
	}
	w.waitFor("a to report what it discarded", 500*time.Millisecond, func() bool {
		b, err := os.ReadFile(report)
		return err == nil && json.Unmarshal(b, &r) == nil
	})
	if r.Total != 0 || len(r.LSNs) != 0 {
		t.Errorf("the planned handover discarded %d transactions %v, want none", r.Total, r.LSNs)
	}
	if got := w.must(b, map[string]string{"op": "query", "sql": `SELECT id FROM audit_log WHERE action = 'citadel.discarded'`}); len(got.Rows) != 0 {
		t.Errorf("the audit of the new Citadel lists discarded transactions after a planned handover: %v", got.Rows)
	}
	ra := w.externalRuns(a)
	if len(ra) != 1 || len(ra[0]) == 0 || !strings.HasPrefix(ra[0][0], "on 1 ") {
		t.Fatalf("the external function of a: %q", ra)
	}
	for _, l := range ra[0] {
		if strings.HasPrefix(l, "on 2") {
			t.Errorf("the old Citadel acted with the epoch 2: %q", l)
		}
	}
}

// A promoted machine whose process stops during the wait of 60 s waits, when
// it starts again, the whole wait (a wall clock that stepped forward while it
// was down must not shorten it): never less than 60 s after it took the
// command, nor than 60 s after the restart.
func TestACitadelRestartedDuringTheWaitOfItsPromotionStillWaits(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ports := freePorts(t, 2)
	a := w.add("a", ports[0], nil)
	b := w.add("b", ports[1], nil)
	a.cfg.Addresses = []string{b.addr()}
	b.cfg.Addresses = []string{a.addr()}
	ctx := t.Context()
	founded, rk, err := app.Found(ctx, a.cfg, app.DirPaths(a.dir), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	rk.Destroy()
	if err := os.WriteFile(filepath.Join(w.lock, "epoca-1"), []byte(founded.MachineID), 0o600); err != nil {
		t.Fatal(err)
	}
	w.start(a)
	w.waitForCitadel(a)
	if _, err := app.Enroll(ctx, b.cfg, app.DirPaths(b.dir), a.addr(), transport.SystemNetwork{Suite: suite()}, clock.Real{}, nil); err != nil {
		t.Fatal(err)
	}
	w.start(b)
	lsn := w.mark(a, "t1")
	w.waitFor("b to apply t1", 0, func() bool {
		st, ok := w.status(b)
		return ok && st.Connected && st.LSN >= lsn
	})

	// the Citadel stops for good and b takes the command
	w.kill(a)
	w.must(b, map[string]string{"op": "promote"})
	w.waitFor("b to take the command", 0, func() bool {
		st, ok := w.status(b)
		return ok && st.Role == app.RoleCitadel && st.Epoch == 2
	})
	r := w.must(b, map[string]string{"op": "query", "sql": `SELECT started_at FROM epochs WHERE epoch = 2`})
	if len(r.Rows) != 1 {
		t.Fatalf("epochs: %v", r.Rows)
	}
	took := time.UnixMilli(int64(r.Rows[0][0].(float64)))

	// 30 s into the wait the process stops; the clock steps forward 2 minutes
	// while it is down, and it starts again
	_ = w.clk.Advance(30 * time.Second) // a is down: it cannot apply the time, and the error says so
	w.kill(b)
	_ = w.clk.Advance(2 * time.Minute) // b is down: it cannot apply the time, and the error says so
	w.start(b)
	w.waitFor("b to come back as the Citadel of the epoch 2", 0, func() bool {
		st, ok := w.status(b)
		return ok && st.Role == app.RoleCitadel && st.Epoch == 2
	})
	// The role is told before the fence arms its timer of 60 s (Run starts
	// after it), so one jump of 59 s could land before the timer exists and
	// leave it 60 s ahead of the clock. The clock goes by steps of 1 s: the
	// wait ends 60 s after the timer was armed, whenever that was, and never
	// earlier.
	for range 59 {
		if err := w.clk.Advance(time.Second); err != nil {
			t.Fatal(err)
		}
	}
	if st, _ := w.status(b); st.Acting || st.Fence != "waiting" {
		t.Fatalf("59 s after the restart the Citadel is %s (acting %v); want it waiting the whole wait again", st.Fence, st.Acting)
	}
	// The wait is 60 s from the timer, so it ends within a few seconds of the
	// clock past the 59: a wait of 30 s would have acted before, one of 10
	// minutes never does here.
	acting := func() bool {
		st, ok := w.status(b)
		return ok && st.Acting
	}
	for i := range 3 {
		if err := w.clk.Advance(time.Second); err != nil {
			t.Fatal(err)
		}
		// no advance while the check of the lock runs on the real time
		deadline := clock.Real{}.Now().Add(5 * time.Second)
		for !acting() && (clock.Real{}).Now().Before(deadline) {
			_ = clock.Sleep(t.Context(), clock.Real{}, 15*time.Millisecond)
		}
		if acting() {
			break
		}
		if i == 2 {
			t.Fatalf("b did not act %d s after the restart (test clock at %s)", 59+3, w.clk.Now().Format(time.TimeOnly))
		}
	}
	for i, run := range w.externalRuns(b) {
		for _, l := range run {
			f := strings.Fields(l)
			ms, _ := strconv.ParseInt(f[len(f)-1], 10, 64)
			if f[0] == "on" && time.UnixMilli(ms).Before(took.Add(60*time.Second)) {
				t.Errorf("run %d of b acted outside %s after taking the command: %q", i+1, time.UnixMilli(ms).Sub(took), l)
			}
		}
	}
}
