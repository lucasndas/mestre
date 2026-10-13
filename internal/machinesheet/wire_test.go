package machinesheet_test

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
	"github.com/lucasndas/mestre/internal/testkit/sheetfake"
	"github.com/lucasndas/mestre/pkg/clock"
)

const wireAddr = "citadel:7443"

// wire is a Citadel with the registry of sheets and a Tower with its agent,
// connected by the protocol over the in-memory network. It runs in a synctest
// bubble, so the protocol runs on the bubble's clock.
type wire struct {
	t *testing.T
	c *citadelRig

	srv     *protocol.Server
	nw      protorig.Network
	machine *sheetfake.Machine
	agent   *machinesheet.Agent
	cfg     protocol.Config

	mu      sync.Mutex
	changes []change
}

type change struct{ before, after machinesheet.Sheet }

func (w *wire) record(_ string, before, after machinesheet.Sheet) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.changes = append(w.changes, change{before, after})
}

func (w *wire) lastChange() change {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.changes[len(w.changes)-1]
}

// sheetBubble runs f in a bubble with the Citadel serving and the machine
// m1 in its registry.
func sheetBubble(t *testing.T, f func(t *testing.T, w *wire)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		w := &wire{t: t, nw: protorig.Network{Net: fakenet.New(clock.Real{})}, machine: sheetfake.Linux("linux-pc")}
		w.c = &citadelRig{db: newGuildDB(t), names: &names{}}
		addMachine(t, w.c.db, "tower1", "linux-pc", "torre-1")

		cfgC, _ := protorig.Config(clock.Real{}, "citadel", 1, "citadel")
		cfgC.Router = protocol.NewRouter()
		var err error
		w.c.reg, err = machinesheet.NewRegistry(machinesheet.RegistryConfig{
			DB: w.c.db, Presence: lateServer{w}, Self: func() string { return "citadel" }, OnChange: w.record,
		})
		if err != nil {
			t.Fatal(err)
		}
		w.c.reg.Register(cfgC.Router)
		cfgC.Hooks.OnHello = w.c.reg.OnHello
		if w.srv, err = protocol.NewServer(cfgC); err != nil {
			t.Fatal(err)
		}
		lis, err := w.nw.Listen(wireAddr, protocol.Peer{Role: protocol.RoleMachine, ID: "citadel"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		var wg sync.WaitGroup
		wg.Go(func() { _ = w.srv.Serve(ctx, lis) })
		t.Cleanup(func() { cancel(); wg.Wait() })

		w.agent = newAgent(t, clock.Real{}, w.machine.Sources())
		w.cfg, _ = protorig.Config(clock.Real{}, "tower1", 1, "citadel")
		w.cfg.Router = protocol.NewRouter()
		w.cfg.Hello = func(ctx context.Context) (protocol.HelloInfo, error) {
			sheet, err := w.agent.Hello(ctx)
			return protocol.HelloInfo{Version: "0.0.0-test", Sheet: sheet}, err
		}
		f(t, w)
	})
}

type lateServer struct{ w *wire }

func (l lateServer) Connected(id string) bool { return l.w.srv.Link(id).Connected() }

// connect runs a Tower client until the returned function stops it.
func (w *wire) connect(cfg protocol.Config) (stop func()) {
	w.t.Helper()
	client, err := protocol.NewClient(cfg, w.nw.Dialer("tower1", wireAddr, protocol.Peer{Role: protocol.RoleMachine, ID: "tower1"}))
	if err != nil {
		w.t.Fatal(err)
	}
	w.agent.Attach(client.Link())
	ctx, cancel := context.WithCancel(w.t.Context())
	var wg sync.WaitGroup
	wg.Go(func() { _ = client.Run(ctx) })
	synctest.Wait()
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); wg.Wait() }) }
	w.t.Cleanup(stop)
	return stop
}

func (w *wire) sheet() machinesheet.Sheet {
	w.t.Helper()
	s, err := w.c.reg.Sheet(w.t.Context(), "tower1")
	if err != nil {
		w.t.Fatal(err)
	}
	return s
}

// CA-FMAQ-01, over the protocol: the login of a profile falls on a connected
// machine, the machine sends machine.sheet with the provider only, and the
// Citadel stores the new sheet.
func TestWhenALoginFallsTheTowerSendsTheProviderAndTheCitadelStoresIt(t *testing.T) {
	t.Parallel()
	sheetBubble(t, func(t *testing.T, w *wire) {
		w.connect(w.cfg)
		s := w.sheet()
		if !s.Online || s.OS != "linux" || s.Name != "linux-pc" || s.Instance != "torre-1" || !s.Providers[0].Profiles[0].LoggedIn {
			t.Fatalf("after the hello: %+v", s)
		}
		w.machine.SetProviders(machinesheet.Provider{Provider: machinesheet.ProviderClaudeCode, Profiles: []machinesheet.Profile{
			{Name: "claude-trabalho", Version: "2.1.0", LoggedIn: false, Conformant: true, Default: true}}})
		if err := w.agent.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if s = w.sheet(); s.Providers[0].Profiles[0].LoggedIn {
			t.Fatal("the Citadel still has the login")
		}
		ch := w.lastChange()
		if patch, changed := machinesheet.Diff(ch.before, ch.after); !changed || !slices.Equal(keys(patch), []string{"providers"}) {
			t.Errorf("the Citadel saw %v change, want only providers", keys(patch))
		}
	})
}

func TestAMachineShowsOnlineWhileConnectedAndKeepsItsSheetWhenItLeaves(t *testing.T) {
	t.Parallel()
	sheetBubble(t, func(t *testing.T, w *wire) {
		stop := w.connect(w.cfg)
		if !w.sheet().Online {
			t.Fatal("not online while connected")
		}
		stop()
		synctest.Wait()
		s := w.sheet()
		if s.Online || s.OS != "linux" || s.Resources.CPUCores != 8 {
			t.Errorf("after leaving: online %v, sheet %+v", s.Online, s)
		}
	})
}

func TestAHelloWithoutTheProvidersIsRefusedAndTheMachineDoesNotConnect(t *testing.T) {
	t.Parallel()
	sheetBubble(t, func(t *testing.T, w *wire) {
		var asked atomic.Int32
		w.cfg.Hello = func(context.Context) (protocol.HelloInfo, error) {
			asked.Add(1)
			sheet := w.machine
			obj := towerSheet(t, sheet, "tower1", "").Object()
			delete(obj, "providers")
			return protocol.HelloInfo{Version: "0.0.0-test", Sheet: obj}, nil
		}
		w.connect(w.cfg)
		if asked.Load() == 0 {
			t.Fatal("the Tower never tried")
		}
		if w.srv.Link("tower1").Connected() {
			t.Error("a machine with an invalid sheet is connected")
		}
		if s := w.sheet(); s.OS != "" {
			t.Errorf("the Citadel stored %+v", s)
		}
	})
}

func TestAPatchThatWaitedForTheConnectionCannotUndoTheNewerHello(t *testing.T) {
	t.Parallel()
	sheetBubble(t, func(t *testing.T, w *wire) {
		stop := w.connect(w.cfg)
		stop()
		synctest.Wait()

		// offline, the transcription is turned on and the patch waits in the
		// outbox; before the connection returns it is turned off again
		w.machine.SetTranscription(true)
		if err := w.agent.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
		w.machine.SetTranscription(false)

		w.connect(w.cfg)
		if s := w.sheet(); s.Transcricao || !s.Online {
			t.Errorf("transcricao = %v, online = %v: the stale patch won", s.Transcricao, s.Online)
		}
		// and the patches of the new connection are applied again
		w.machine.SetTranscription(true)
		if err := w.agent.Refresh(t.Context()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if !w.sheet().Transcricao {
			t.Error("a patch of the new connection was dropped")
		}
	})
}
