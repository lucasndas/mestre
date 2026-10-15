//go:build full

package replication_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The full suite runs the Citadel and a Tower of the same computer as two
// processes would: real TCP on the loopback with the mTLS of the transport,
// databases and files on disk, the real git for the mirror, the real clock.

func issueMachine(t *testing.T, ca *transport.CA, id string) *transport.CertSource {
	t.Helper()
	key, err := transport.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	csr, err := transport.NewCSR(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.IssueMachine(csr, transport.Names{MachineID: id, Hostname: "localhost", IPs: []net.IP{net.ParseIP("127.0.0.1")}})
	if err != nil {
		t.Fatal(err)
	}
	return transport.NewCertSource(cert, key)
}

type fullEvents struct {
	mu sync.Mutex
	ev []string
}

func (e *fullEvents) add(s string) {
	e.mu.Lock()
	e.ev = append(e.ev, s)
	e.mu.Unlock()
}

func (e *fullEvents) list() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.ev...)
}

// CA-REPL-08, the part of milestone 2: the lag stays under 5 s while the Citadel
// writes at a steady pace, and the files and the mirror that records cite are
// on the Tower before the records are applied.
func TestACitadelWritingAtASteadyPaceKeepsTheLagUnderFiveSecondsAndFilesAndMirrorArriveFirst(t *testing.T) {
	t.Parallel()
	clk := clock.Real{}
	ctx, cancel := context.WithCancel(t.Context())
	// Everything that uses the databases ends before they are closed (by
	// t.Cleanup, after these defers): a handler that outlives the test would
	// use a closed database.
	var running sync.WaitGroup
	defer running.Wait()
	defer cancel()
	dir := t.TempDir()
	sys := transport.SystemNetwork{Suite: os.Getenv("MESTRE_TEST_SUITE")}

	ca, err := transport.NewCA("guild-1", clk)
	if err != nil {
		t.Fatal(err)
	}
	citadelCert, towerCert := issueMachine(t, ca, citadelID), issueMachine(t, ca, "tower1")
	tlsSrv, err := transport.NewServer(transport.ServerConfig{CA: ca.Certificate(), Cert: citadelCert, Credentials: transport.NewMemCredentials(), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	towers := tlsSrv.Route(transport.RouteTower, transport.KindMachine)
	ln, err := sys.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	served := make(chan struct{})
	go func() { _ = tlsSrv.Serve(ln); close(served) }()
	defer func() { _ = tlsSrv.Close(transport.StatusGoingAway, "test over"); <-served }()

	// the real git, a mirror on the Citadel with one commit
	g, err := gitx.New(gitx.Options{Clock: clk, Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	wsDir := filepath.Join(dir, "citadel", "ws1")
	ws, err := g.Init(ctx, wsDir, gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	author := gitx.Signature{Name: "Alice Dev", Email: "alice@example.invalid", When: time.Unix(1767225600, 0)}
	commit := func(body string) gitx.Hash {
		if err := os.WriteFile(filepath.Join(wsDir, "a.txt"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ws.Stage(ctx); err != nil {
			t.Fatal(err)
		}
		h, err := ws.Commit(ctx, gitx.CommitOptions{Message: "feat: " + body, Author: author})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	commit("one")
	src := &mirrorSrc{g: g, dirs: map[string]string{"ws1": wsDir}}

	// The Citadel
	cpath := filepath.Join(dir, "citadel", "mestre.db")
	if err := os.WriteFile(cpath, template, 0o600); err != nil {
		t.Fatal(err)
	}
	cdb, err := guildtpl.Open(ctx, cpath, store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cdb.Close() })
	filesDir := filepath.Join(dir, "citadel", "classes")
	cfiles, err := replication.NewFiles(replication.FileRoot{Name: "classes", Dir: filesDir})
	if err != nil {
		t.Fatal(err)
	}
	primary, err := replication.NewPrimary(ctx, replication.PrimaryConfig{
		DB: cdb, Clock: clk, Roster: &roster{excluded: map[string]bool{}}, Dir: filepath.Join(dir, "citadel", "snapshots"),
		Files: cfiles,
		Cites: func(c store.Change) []replication.FileRef { // the name of a machine stands for a path
			if c.Table == "machines" && c.Op == store.OpInsert && strings.HasSuffix(c.New[1].(string), ".md") {
				return []replication.FileRef{{Root: "classes", Path: c.New[1].(string)}}
			}
			return nil
		},
		Mirrors: replication.NewMirrors(src, filepath.Join(dir, "citadel")),
		CitesMirrors: func(c store.Change) []string {
			if c.Table == "machines" && c.Op == store.OpInsert && c.New[2] != "" {
				return []string{c.New[2].(string)}
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfgC, _ := protorig.Config(clk, citadelID, 1, citadelID)
	cfgC.Router = protocol.NewRouter()
	primary.Register(cfgC.Router)
	cfgC.Hooks = primary.Hooks(protocol.Hooks{})
	protoSrv, err := protocol.NewServer(cfgC)
	if err != nil {
		t.Fatal(err)
	}
	primary.Attach(protoSrv)
	running.Go(func() { _ = protoSrv.Serve(ctx, protocol.FromTransportListener(towers)) })
	running.Go(func() { _ = primary.Run(ctx) })

	// The Tower
	events := &fullEvents{}
	tpath := filepath.Join(dir, "tower", "mestre.db")
	if err := os.MkdirAll(filepath.Dir(tpath), 0o750); err != nil {
		t.Fatal(err)
	}
	tdb, err := guilddb.Open(ctx, tpath, store.Options{Replica: true, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tdb.Close() })
	tfilesDir := filepath.Join(dir, "tower", "classes")
	tfiles, err := replication.NewFiles(replication.FileRoot{Name: "classes", Dir: tfilesDir})
	if err != nil {
		t.Fatal(err)
	}
	mirrorsDir := filepath.Join(dir, "tower", "mirrors")
	rep, err := replication.NewReplica(ctx, replication.ReplicaConfig{
		DB: tdb, Clock: clk, GuildID: guildULID, Files: tfiles, Mirrors: &replication.MirrorStore{Root: mirrorsDir, Git: g},
		OnApplied: func(first, last uint64) { events.add(fmt.Sprintf("applied %d-%d", first, last)) },
	})
	if err != nil {
		t.Fatal(err)
	}
	epochs := protorig.NewEpochs(1, citadelID)
	cfgT, parts := protorig.Config(clk, "tower1", 1, citadelID)
	cfgT.Epoch, cfgT.Certs = epochs, epochs
	_ = parts
	cfgT.Router = protocol.NewRouter()
	rep.Register(cfgT.Router)
	cfgT.Hello = func(ctx context.Context) (protocol.HelloInfo, error) {
		info := protocol.HelloInfo{Version: "0.0.0-test", Sheet: protocol.Object{}}
		rep.Hello(ctx, &info)
		return info, nil
	}
	fins := rep.Finalizers()
	fins[protocol.KindFile] = &fullFile{Finalizer: fins[protocol.KindFile], ev: events}
	fins[protocol.KindGitBundle] = &fullBundle{Finalizer: fins[protocol.KindGitBundle], ev: events}
	cfgT.Xfer = &protocol.XferConfig{Dir: filepath.Join(dir, "tower", "xfer"), Finalizers: fins}
	tc, err := transport.NewClient(transport.ClientConfig{CA: ca.Certificate(), Cert: towerCert, Network: sys, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	cli, err := protocol.NewClient(cfgT, protocol.TransportDialer(tc, func() (string, string) { return addr, transport.RouteTower }))
	if err != nil {
		t.Fatal(err)
	}
	rep.Attach(cli.Link())
	running.Go(func() { _ = cli.Run(ctx) })
	running.Go(func() { _ = rep.Run(ctx) })

	deadline, stop := clock.WithTimeout(ctx, clk, 60*time.Second)
	defer stop()
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		tick := clk.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for !ok() {
			select {
			case <-tick.C():
			case <-deadline.Done():
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}
	waitFor("the Tower to get its full copy", func() bool {
		n, _ := tdb.LastLSN(ctx)
		return n > 0
	})
	// The lag counts from the commit of the oldest record the Tower has not
	// confirmed. While the full copy is going the records of the copy are the
	// oldest, and they were committed when the process built its template, as
	// long ago as the process is old: the pace starts when the Citadel
	// confirms that the Tower has them and streams.
	waitFor("the Citadel to stream to the Tower", func() bool {
		st, err := primary.Status(ctx)
		return err == nil && len(st) == 1 && st[0].State == "streaming" && st[0].Behind == 0
	})

	// A steady pace: a record every 25 ms for three seconds; every fifth cites a
	// file written just before, and one cites the mirror after a new commit.
	type cited struct {
		lsn  uint64
		what string
	}
	var citations []cited
	var maxLag time.Duration
	insert := func(i int, name, instance string) uint64 {
		if _, err := cdb.Writer().ExecContext(ctx, `INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES (?, ?, ?, 0, 0, 'active', '{}', 0)`,
			fmt.Sprintf("m%04d", i), name, instance); err != nil {
			t.Fatal(err)
		}
		n, err := cdb.LastLSN(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	tick := clk.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	var mirrorTip gitx.Hash
	for i := range 120 {
		<-tick.C()
		switch {
		case i%5 == 0:
			name := fmt.Sprintf("c%03d.md", i)
			if err := os.MkdirAll(filesDir, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(filesDir, name), []byte("# "+name), 0o640); err != nil {
				t.Fatal(err)
			}
			citations = append(citations, cited{insert(i, name, ""), "file " + name})
		case i == 62:
			mirrorTip = commit("two")
			citations = append(citations, cited{insert(i, "n", "ws1"), "bundle"})
		default:
			insert(i, "n", "")
		}
		if st, err := primary.Status(ctx); err == nil {
			for _, s := range st {
				maxLag = max(maxLag, s.Lag)
			}
		}
	}
	last, err := cdb.LastLSN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitFor("the Tower to apply everything", func() bool {
		n, _ := tdb.LastLSN(ctx)
		return n == last
	})
	if maxLag >= 5*time.Second {
		t.Errorf("the lag reached %s, want under 5 s", maxLag)
	}
	if a, b := dump(t, cdb), dump(t, tdb); a != b {
		t.Fatalf("the replica differs from the primary\n%s\n%s", a, b)
	}
	ev := events.list()
	for _, c := range citations {
		f, a := indexOf(ev, c.what), appliedAt(ev, c.lsn)
		if c.what == "bundle" {
			f = lastIndexOf(ev[:max(a, 0)], "bundle")
		}
		if f < 0 || a < 0 || f > a {
			t.Errorf("%s arrived at event %d and the record %d that cites it was applied at %d", c.what, f, c.lsn, a)
		}
	}
	repo, err := g.Open(ctx, filepath.Join(mirrorsDir, "ws1"))
	if err != nil {
		t.Fatalf("the Tower has no mirror: %v", err)
	}
	if h, err := repo.ResolveRef(ctx, "refs/heads/main"); err != nil || h != mirrorTip {
		t.Errorf("the mirror on the Tower is at %s (%v), want %s", h, err, mirrorTip)
	}
	for _, c := range citations {
		if rel, ok := strings.CutPrefix(c.what, "file "); ok {
			if _, err := os.Stat(filepath.Join(tfilesDir, rel)); err != nil {
				t.Errorf("%s is not on the Tower: %v", rel, err)
			}
		}
	}
}

type fullFile struct {
	protocol.Finalizer
	ev *fullEvents
}

func (f *fullFile) Have(ctx context.Context, b protocol.XferBegin) bool {
	return f.Finalizer.(protocol.Skipper).Have(ctx, b)
}

func (f *fullFile) Finalize(ctx context.Context, b protocol.XferBegin, file string) error {
	err := f.Finalizer.Finalize(ctx, b, file)
	if err == nil {
		f.ev.add("file " + strings.Trim(string(b.Meta["path"]), `"`))
	}
	return err
}

type fullBundle struct {
	protocol.Finalizer
	ev *fullEvents
}

func (f *fullBundle) Have(ctx context.Context, b protocol.XferBegin) bool {
	return f.Finalizer.(protocol.Skipper).Have(ctx, b)
}

func (f *fullBundle) Finalize(ctx context.Context, b protocol.XferBegin, file string) error {
	err := f.Finalizer.Finalize(ctx, b, file)
	if err == nil {
		f.ev.add("bundle")
	}
	return err
}

// CA-REPL-08, the mirrors with the real git: the scenario of the fast suite.
func TestTheMirrorFollowsTheCitadelByBundleWithTheRealGit(t *testing.T) {
	t.Parallel()
	g, err := gitx.New(gitx.Options{Clock: clock.Real{}, Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	mirrorScenario(t, g, func(t testing.TB, r gitx.Repo, rel, content string) {
		p := filepath.Join(r.Dir(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	})
}
