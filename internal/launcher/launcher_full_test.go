//go:build full

package launcher_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/launcher"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The full suite of the launcher starts the real thing as ordinary
// processes: the launcher runs inside the test (as the account of the test,
// which plays the engine and the agent), and starts the real mestre-pty,
// built here, from a folder of installed binaries. What can only be
// checked as a service of the system (the accounts of the installation,
// the logon of mestre-agente) is the real-account tests'.

const ptyVersion = "1.0.0"

// builds holds what TestMain built.
var builds struct {
	dir string
	bin string // folder of installed binaries: <bin>/<version>/mestre-pty
	pty string
}

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		if handled, code := osHelper(os.Args[1:]); handled {
			os.Exit(code)
		}
	}
	if dir := os.Getenv(envBuilds); dir != "" {
		// A test run again in a delegated scope (delegated.Rerun): the
		// binaries of the run that started it, which also removes them.
		useBuilds(dir)
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "mlbin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	useBuilds(dir)
	out, err := exec.Command("go", "build", "-o", builds.pty, "-ldflags", "-X main.version="+ptyVersion,
		"github.com/lucasndas/mestre/cmd/mestre-pty").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go build mestre-pty: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// envBuilds names the folder of the binaries of the run that started this one.
const envBuilds = "MESTRE_TEST_BUILDS"

func useBuilds(dir string) {
	name := "mestre-pty"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	builds.dir = dir
	builds.bin = filepath.Join(dir, "bin")
	builds.pty = filepath.Join(builds.bin, ptyVersion, name)
}

// auditLog keeps the records the launcher makes of each request.
type auditLog struct {
	mu sync.Mutex
	ev []launcher.Event
}

func (a *auditLog) record(e launcher.Event) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ev = append(a.ev, e)
}

func (a *auditLog) count(outcome string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, e := range a.ev {
		if e.Outcome == outcome {
			n++
		}
	}
	return n
}

func (a *auditLog) all() []launcher.Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]launcher.Event(nil), a.ev...)
}

// countingBackend counts the processes the launcher is asked to create: the
// refusals must leave this at zero.
type countingBackend struct {
	launcher.Backend
	starts atomic.Int32
}

func (c *countingBackend) Start(ctx context.Context, r launcher.StartRequest) (launcher.Started, error) {
	c.starts.Add(1)
	return c.Backend.Start(ctx, r)
}

// KillProcs passes on what the backend underneath can do, so the tests of
// the kill by identity can go through the counting wrapper.
func (c *countingBackend) KillProcs(ctx context.Context, id string, p terminal.ProcID, sessions []int) error {
	pk, ok := c.Backend.(launcher.ProcKiller)
	if !ok {
		return launcher.Refused("this launcher does not end processes by identity")
	}
	return pk.KillProcs(ctx, id, p, sessions)
}

// env is one launcher under test and the engine side that talks to it.
type env struct {
	root    string // short folder: socket paths are limited
	sockets string
	audit   *auditLog
	// started counts the processes the launcher was asked to create.
	started func() int
	// client is the engine side, the one the launcher must accept.
	client *launcher.Client
	// dial connects as that same legitimate peer, to send raw requests.
	dial func(ctx context.Context) (net.Conn, error)
	// strangers are clients the launcher must refuse, each with the record
	// of the launcher that refused it.
	strangers []stranger
	sys       terminal.System
	os        osEnv
}

type stranger struct {
	name string
	// try asks the launcher to start a terminal as that stranger.
	try     func(ctx context.Context, spec terminal.SpawnSpec) error
	audit   *auditLog
	started func() int
}

func shortDir(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "ml")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func ctxFor(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := clock.WithTimeout(t.Context(), clock.Real{}, 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// raw sends one frame as the legitimate peer and returns the answer.
func (e *env) raw(ctx context.Context, body []byte) (launcher.Response, error) {
	nc, err := e.dial(ctx)
	if err != nil {
		return launcher.Response{}, err
	}
	conn := launcher.NewConn(nc)
	defer conn.Close()
	if err := conn.WriteFrame(body, nil); err != nil {
		return launcher.Response{}, err
	}
	frame, _, err := conn.ReadFrame()
	if err != nil {
		return launcher.Response{}, err
	}
	return decodeResponse(frame)
}

func (e *env) spec(id string) terminal.SpawnSpec {
	sock, err := terminal.SocketPath(e.sockets, id)
	if err != nil {
		panic(err)
	}
	secret := bytes.Repeat([]byte{0x5A}, ptywire.SecretSize)
	return terminal.SpawnSpec{ID: id, Version: ptyVersion, Binary: "/not/used/by/the/launcher", SocketPath: sock, Secret: secret}
}

// CA-TERM-07
func TestLauncher_CA_TERM_07(t *testing.T) {
	t.Parallel()

	// The launcher refuses what is not start-pty of the installed
	// mestre-pty as the account of the agents, and records it.
	t.Run("refuses other accounts, other executables and other requests", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		ctx := ctxFor(t)
		spec := e.spec("t07-refused")
		secret := spec.Secret
		start := func(extra map[string]any) []byte {
			m := map[string]any{"op": "start-pty", "id": "t07-refused", "version": ptyVersion, "socket": spec.SocketPath, "secret": secret}
			for k, v := range extra {
				m[k] = v
			}
			return mustJSON(t, m)
		}
		cases := []struct {
			name string
			body []byte
		}{
			{"another account", start(map[string]any{"user": "root"})},
			{"another account, by name of field", start(map[string]any{"account": "Administrator"})},
			{"another executable", start(map[string]any{"path": os.Args[0]})},
			{"another executable, by version", start(map[string]any{"version": "../../" + filepath.Base(os.Args[0])})},
			{"a version that is not installed", start(map[string]any{"version": "9.9.9"})},
			{"arguments", start(map[string]any{"args": []string{"--listen-fd", "0"}})},
			{"a socket outside the folder of the terminals", start(map[string]any{"socket": filepath.Join(e.root, "elsewhere.sock")})},
			{"not start-pty: exec", mustJSON(t, map[string]any{"op": "exec", "id": "t07-refused", "path": os.Args[0]})},
			{"not start-pty: shell", mustJSON(t, map[string]any{"op": "shell", "id": "t07-refused"})},
			{"not start-pty: no op", mustJSON(t, map[string]any{"id": "t07-refused"})},
			{"no secret", mustJSON(t, map[string]any{"op": "start-pty", "id": "t07-refused", "version": ptyVersion, "socket": spec.SocketPath})},
			{"not json", []byte("start-pty")},
		}
		for _, tc := range cases {
			before := e.audit.count(launcher.OutcomeRefused)
			resp, err := e.raw(ctx, tc.body)
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
				continue
			}
			if resp.OK || resp.Code != launcher.CodeRefused {
				t.Errorf("%s: answer = %+v, want a refusal", tc.name, resp)
			}
			if got := e.audit.count(launcher.OutcomeRefused); got != before+1 {
				t.Errorf("%s: %d refusals recorded, want %d", tc.name, got, before+1)
			}
		}
		if n := e.started(); n != 0 {
			t.Errorf("the launcher created %d processes for requests it had to refuse", n)
		}
		if n := e.audit.count(launcher.OutcomeOK); n != 0 {
			t.Errorf("%d requests recorded as done", n)
		}
		if _, err := os.Stat(spec.SocketPath); err == nil {
			t.Error("a terminal socket exists: a mestre-pty ran")
		}
	})

	// Whoever is not the engine of the instance is refused, even with a
	// perfect request.
	t.Run("refuses who is not the engine", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		if len(e.strangers) == 0 {
			t.Skip("no stranger to try on this system")
		}
		for i, s := range e.strangers {
			t.Run(s.name, func(t *testing.T) {
				t.Parallel()
				ctx := ctxFor(t)
				spec := e.spec(fmt.Sprintf("t07-s%d", i))
				err := s.try(ctx, spec)
				if err == nil {
					t.Fatal("a mestre-pty was started for a peer that is not the engine")
				}
				if s.audit.count(launcher.OutcomeRefused) == 0 {
					t.Errorf("no refusal recorded (the error was: %v)", err)
				}
				if n := s.started(); n != 0 {
					t.Errorf("%d processes created for a stranger", n)
				}
				if s.audit.count(launcher.OutcomeOK) != 0 {
					t.Error("a request of a stranger was recorded as done")
				}
			})
		}
	})

	t.Run("starts the mestre-pty of the installed version", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.needStart(t)
		ctx := ctxFor(t)
		spec := e.spec("t07-start")
		sp, err := e.client.Spawn(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = e.client.KillCgroup(context.Background(), spec.ID)
			_ = e.sys.KillTree(context.Background(), sp.ProcID, nil)
		})
		if !terminal.Alive(e.sys, sp.ProcID) {
			t.Fatalf("mestre-pty %+v is not alive", sp.ProcID)
		}
		// The secret reached it (handshake) and the process is the one the
		// launcher reported.
		c, err := terminal.Dial(ctx, clock.Real{}, spec.SocketPath, spec.Secret, 0)
		if err != nil {
			t.Fatalf("handshake with the mestre-pty the launcher started: %v", err)
		}
		defer c.Close()
		if c.Info().PID != sp.PID {
			t.Errorf("the mestre-pty says PID %d, the launcher said %d", c.Info().PID, sp.PID)
		}
		if peer, err := e.sys.PeerPID(c.Net()); err != nil || peer != sp.PID {
			t.Errorf("the socket is held by PID %d (%v), the launcher said %d", peer, err, sp.PID)
		}
		if n := e.audit.count(launcher.OutcomeOK); n != 1 {
			t.Errorf("%d requests recorded as done, want 1", n)
		}
		for _, ev := range e.audit.all() {
			if ev.Outcome == launcher.OutcomeOK && (ev.Op != launcher.OpStartPTY || ev.ID != spec.ID || ev.Version != ptyVersion) {
				t.Errorf("record = %+v", ev)
			}
		}
		e.checkStarted(t, ctx, spec, sp, c)
	})

	e2 := func(name string, f func(t *testing.T, e *env)) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f(t, newEnv(t))
		})
	}
	e2("outlives and survives the launcher", func(t *testing.T, e *env) { e.checkRestart(t) })
	osCases(t)
}

func decodeResponse(b []byte) (launcher.Response, error) {
	var r launcher.Response
	err := json.Unmarshal(b, &r)
	return r, err
}
