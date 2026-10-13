//go:build full

package machinesheet_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

func issueMachine(t *testing.T, ca *transport.CA, id string) (*x509.Certificate, *ecdsa.PrivateKey) {
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
	return cert, key
}

// CA-FMAQ-06, on Linux and on Windows: a Citadel and a Tower on the same
// machine, over real TCP sockets with mTLS, the Guild database in a file and
// the real probes of the system. The sheet that reaches the registry has the
// system, the memory, the cores of this machine and the name of the instance.
func TestTheSheetOfARealTowerReachesTheRealCitadelWithTheNumbersOfThisMachine(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	clk := clock.Real{}
	sys := transport.SystemNetwork{Suite: os.Getenv("MESTRE_TEST_SUITE")}

	ca, err := transport.NewCA("guild-1", clk)
	if err != nil {
		t.Fatal(err)
	}
	citadelCert, citadelKey := issueMachine(t, ca, "citadel")
	towerCert, towerKey := issueMachine(t, ca, "tower1")

	// the Citadel: the protocol server on a real listener, the registry over a file
	db := newGuildDB(t)
	addMachine(t, db, "tower1", "this-machine", "torre-2")
	tlsSrv, err := transport.NewServer(transport.ServerConfig{CA: ca.Certificate(), Cert: transport.NewCertSource(citadelCert, citadelKey), Credentials: transport.NewMemCredentials(), Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	towers := tlsSrv.Route(transport.RouteTower, transport.KindMachine)
	ln, err := sys.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	served := make(chan struct{})
	go func() { _ = tlsSrv.Serve(ln); close(served) }()
	defer func() { _ = tlsSrv.Close(transport.StatusGoingAway, "test over"); <-served }()

	cfgC, _ := protorig.Config(clk, "citadel", 1, "citadel")
	cfgC.Router = protocol.NewRouter()
	var srv *protocol.Server
	reg, err := machinesheet.NewRegistry(machinesheet.RegistryConfig{
		DB: db, Presence: presenceFunc(func(id string) bool { return srv.Link(id).Connected() }), Self: func() string { return "citadel" },
	})
	if err != nil {
		t.Fatal(err)
	}
	reg.Register(cfgC.Router)
	cfgC.Hooks.OnHello = reg.OnHello
	if srv, err = protocol.NewServer(cfgC); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Go(func() { _ = srv.Serve(ctx, protocol.FromTransportListener(towers)) })
	defer wg.Wait()
	defer cancel()

	// the Tower: the real probes of the system, the instance "torre-2"
	dir := t.TempDir()
	b, err := machinesheet.NewBuilder(machinesheet.Sources{System: machinesheet.NewSystemProbe(dir)}, "tower1", "torre-2")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := machinesheet.NewAgent(machinesheet.AgentConfig{Builder: b, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	tc, err := transport.NewClient(transport.ClientConfig{CA: ca.Certificate(), Cert: transport.NewCertSource(towerCert, towerKey), Network: sys, Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	cfgT, _ := protorig.Config(clk, "tower1", 1, "citadel")
	cfgT.Hello = func(ctx context.Context) (protocol.HelloInfo, error) {
		sheet, err := agent.Hello(ctx)
		return protocol.HelloInfo{Version: "0.0.0-test", Sheet: sheet}, err
	}
	cli, err := protocol.NewClient(cfgT, protocol.TransportDialer(tc, func() (string, string) {
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), transport.RouteTower
	}))
	if err != nil {
		t.Fatal(err)
	}
	agent.Attach(cli.Link())
	wg.Go(func() { _ = cli.Run(ctx) })

	dl, stop := clock.WithTimeout(ctx, clk, 30*time.Second)
	defer stop()
	for !srv.Link("tower1").Connected() {
		if err := clock.Sleep(dl, clk, 20*time.Millisecond); err != nil {
			t.Fatal("the Tower did not connect")
		}
	}

	var s machinesheet.Sheet
	for {
		if s, err = reg.Sheet(ctx, "tower1"); err != nil {
			t.Fatal(err)
		}
		if s.OS != "" {
			break
		}
		if err := clock.Sleep(dl, clk, 20*time.Millisecond); err != nil {
			t.Fatal("the hello never stored the sheet")
		}
	}
	host, _ := os.Hostname()
	switch {
	case s.OS != runtime.GOOS || s.Arch != runtime.GOARCH || s.Host != host:
		t.Errorf("system = %s/%s on %q, want %s/%s on %q", s.OS, s.Arch, s.Host, runtime.GOOS, runtime.GOARCH, host)
	case s.OSVersion == "":
		t.Error("no version of the system")
	case s.Instance != "torre-2" || s.Name != "this-machine" || !s.Online:
		t.Errorf("instance %q, name %q, online %v", s.Instance, s.Name, s.Online)
	case s.Resources.CPUCores != runtime.NumCPU():
		t.Errorf("cores = %d, want %d", s.Resources.CPUCores, runtime.NumCPU())
	case s.Resources.MemBytes < 128<<20 || s.Resources.DiskFreeBytes == 0:
		t.Errorf("memory %d, free disk %d", s.Resources.MemBytes, s.Resources.DiskFreeBytes)
	case s.Providers == nil || s.Resources.GPUs == nil:
		t.Errorf("the lists are null: %+v", s)
	}
	if err := s.Validate(); err != nil {
		t.Error(err)
	}
}

type presenceFunc func(string) bool

func (f presenceFunc) Connected(id string) bool { return f(id) }

// The real probes read this machine on Linux and on Windows, each in its own
// way, and agree with what the Go runtime knows.
func TestTheRealProbesReadThisMachine(t *testing.T) {
	t.Parallel()
	p := machinesheet.NewSystemProbe(t.TempDir())
	info, err := p.Info(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.OS != runtime.GOOS || info.Arch != runtime.GOARCH || info.Host == "" || info.OSVersion == "" {
		t.Errorf("info = %+v", info)
	}
	res, err := p.Resources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res.CPUCores != runtime.NumCPU() || res.MemBytes < 128<<20 || res.DiskFreeBytes == 0 || res.GPUs == nil {
		t.Errorf("resources = %+v", res)
	}
	t.Logf("%s %s: %d cores, %d MiB, %d MiB free, %d GPUs %v", info.OS, info.OSVersion, res.CPUCores, res.MemBytes>>20, res.DiskFreeBytes>>20, len(res.GPUs), res.GPUs)
	if _, err := p.GUI(t.Context()); err != nil {
		t.Errorf("GUI: %v", err)
	}
	ips, err := p.IPs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range ips {
		if net.ParseIP(ip).To4() == nil {
			t.Errorf("%q is not an IPv4 address", ip)
		}
	}
	if _, err := machinesheet.NewBrowserProbe(nil).Browser(t.Context()); err != nil {
		t.Errorf("Browser: %v", err)
	}
}
