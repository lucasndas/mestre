package app_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/testkit/fakelock"
	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
	"github.com/mestre-ia/mestre/internal/testkit/protorig"
	"github.com/mestre-ia/mestre/internal/testkit/sheetfake"
	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// fixedEpoch is the epoch of a Tower that already follows a newer
// certificate.
type fixedEpoch struct {
	cert protocol.Certificate
	v    *guild.Verifier
}

func (f fixedEpoch) Epoch() uint64                                      { return f.cert.Epoch }
func (f fixedEpoch) Certificate() *protocol.Certificate                 { c := f.cert; return &c }
func (f fixedEpoch) Follow(context.Context, protocol.Certificate) error { return nil }
func (f fixedEpoch) Stale(context.Context, protocol.Peer)               {}
func (f fixedEpoch) Verify(c protocol.Certificate) error                { return f.v.Verify(c) }

// counting stands for the fake Telegram and GitHub: every request that
// reaches it is a call made outside the Guild.
type counting struct{ n atomic.Int64 }

func (c *counting) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	c.n.Add(1)
	w.WriteHeader(http.StatusOK)
}

// CA-CIDADELA-02
func TestACitadelOfAnOldEpochStepsDownForATowerOfTheNewOneWithoutActingOutside(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	clk := clock.Real{}
	dir := t.TempDir()
	cfg := app.Config{Instance: "a", Name: "a", Hostname: "a", Bind: "a", EnginePort: 7443, NoDiscovery: true, Synchronous: "OFF"}
	paths := app.DirPaths(dir)
	paths.CLISocket = ""
	seedGuild(t, paths)
	founded, rk, err := app.Found(ctx, cfg, paths, clk)
	if err != nil {
		t.Fatal(err)
	}
	rk.Destroy()

	// the Tower b follows the epoch 2, which names it: its engine
	// certificate and the certificate of the epoch, both of the Guild's CA
	const bID = "01M4VDXFRGP2SENAXXMTWCN9CF"
	key, err := transport.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	csr, err := transport.NewCSR(key)
	if err != nil {
		t.Fatal(err)
	}
	bCert, err := founded.CA.IssueMachine(csr, transport.Names{MachineID: bID, Hostname: "b"})
	if err != nil {
		t.Fatal(err)
	}
	cert2, err := guild.SignEpoch(founded.CA, protocol.Certificate{
		GuildID: founded.GuildID, Epoch: 2, CitadelMachineID: bID, ApprovedBy: "dono", ApprovalID: "p1", IssuedAt: protocol.Timestamp{Time: clk.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}

	// the fake Telegram and GitHub, reached only through the fence
	tg, gh := &counting{}, &counting{}
	external := func(ctx context.Context, fence *guild.Fence) error {
		call := func(h http.Handler, kind guild.External) {
			_, _ = fence.Do(ctx, kind, string(kind), func(ctx context.Context) error {
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.invalid/", nil)
				resp, err := fakenet.HandlerTransport(h).RoundTrip(req)
				if err == nil {
					resp.Body.Close()
				}
				return err
			})
		}
		call(tg, guild.ExternalTelegram)
		call(gh, guild.ExternalGitHub)
		<-ctx.Done()
		return nil
	}

	// the lock has not answered yet: the Citadel starts suspended
	lock := fakelock.New(1, founded.MachineID)
	lock.Hold()
	defer lock.Release()
	network := fakenet.New(clk)
	var (
		mu    sync.Mutex
		saw   []app.Status
		state = make(chan app.Status, 64)
	)
	e, err := app.New(cfg, paths, app.Deps{
		Clock: clk, Network: network.Host("a"), Lock: lock.For(founded.MachineID), External: []app.ExternalFunc{external},
		Sheet: sheetfake.Linux("a").Sources(),
		OnStatus: func(s app.Status) {
			mu.Lock()
			saw = append(saw, s)
			mu.Unlock()
			select {
			case state <- s:
			default:
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitStatus := func(what string, ok func(app.Status) bool) app.Status {
		t.Helper()
		deadline := clk.NewTimer(20 * time.Second)
		defer deadline.Stop()
		for {
			if s := e.Status(); ok(s) {
				return s
			}
			select {
			case <-state:
			case <-deadline.C():
				t.Fatalf("timed out waiting for %s: %+v", what, e.Status())
			}
		}
	}
	waitStatus("a to start as the suspended Citadel", func(s app.Status) bool {
		return s.Role == app.RoleCitadel && s.Fence == guild.FenceSuspended
	})

	// b connects with the certificate of epoch 2
	tc, err := transport.NewClient(transport.ClientConfig{
		CA: founded.CA.Certificate(), Cert: transport.NewCertSource(bCert, key), Network: network.Host("b"), Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	pcfg, _ := protorig.Config(clk, bID, 2, bID)
	epoch := fixedEpoch{cert: cert2, v: guild.NewVerifier(founded.CA.Certificate(), founded.GuildID)}
	pcfg.Epoch, pcfg.Certs = epoch, epoch
	client, err := protocol.NewClient(pcfg, protocol.TransportDialer(tc, func() (string, string) { return "a:7443", transport.RouteTower }))
	if err != nil {
		t.Fatal(err)
	}
	cctx, stopClient := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { _ = client.Run(cctx) })
	defer func() { stopClient(); wg.Wait() }()

	st := waitStatus("a to step down to a Tower of the epoch 2", func(s app.Status) bool {
		return s.Role == app.RoleTower && s.Epoch == 2
	})
	if st.Citadel != bID {
		t.Errorf("a follows the Citadel %q, want b", st.Citadel)
	}
	if _, err := os.Stat(filepath.Join(dir, "banco", "descartado-1.db")); err != nil {
		t.Errorf("a did not keep the database of its epoch: %v", err)
	}
	lock.Release() // the check that waited finds nobody left to act
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := clk.NewTimer(20 * time.Second)
		defer deadline.Stop()
		tick := clk.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for !ok() {
			select {
			case <-tick.C():
			case <-deadline.C():
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	waitFor("the released check to return", func() bool {
		return lock.Answered(founded.MachineID) >= lock.Checks(founded.MachineID)
	})
	if n, m := tg.n.Load(), gh.n.Load(); n != 0 || m != 0 {
		t.Errorf("the fake Telegram got %d calls and the GitHub %d from the old Citadel; want none", n, m)
	}
	if n := lock.Claims(founded.MachineID); n != 0 {
		t.Errorf("the old Citadel tried to claim the lock %d times", n)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, s := range saw {
		if s.Acting {
			t.Fatalf("the old Citadel acted outside at some point: %+v", s)
		}
	}
}
