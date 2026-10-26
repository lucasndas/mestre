package protocol_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/testkit/protorig"
	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// pipeNet is a transport.Dialer and Listener over transport.Pipe: the
// adapter runs against the contract of the real transport without TLS.
type pipeNet struct {
	accept chan transport.Conn
	wrap   func(transport.Conn) transport.Conn // the end the listener accepts, when set
}

func (n pipeNet) Dial(ctx context.Context, _, _ string) (transport.Conn, error) {
	id := func(machine string) transport.Peer {
		return transport.Peer{Identity: transport.Identity{Kind: transport.KindMachine, MachineID: machine}}
	}
	towerEnd, citadelEnd := transport.Pipe(id(citadelID), id(towerID)) // each sees the other
	select {
	case n.accept <- citadelEnd:
		return towerEnd, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (n pipeNet) Accept(ctx context.Context) (transport.Conn, error) {
	select {
	case c := <-n.accept:
		if n.wrap != nil {
			c = n.wrap(c)
		}
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (n pipeNet) Close() error { return nil }

func TestTheProtocolRunsOverTheConnectionOfTheTransport(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		cfgC, _ := protorig.Config(clock.Real{}, citadelID, 1, citadelID)
		cfgT, _ := protorig.Config(clock.Real{}, towerID, 1, citadelID)
		typed := make(chan string, 1)
		cfgT.Router = protocol.NewRouter()
		cfgT.Router.HandleFunc(protocol.TypeTerminalInput, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
			typed <- m.Payload.(*protocol.TerminalInput).Data
			return nil, nil
		})
		srv, err := protocol.NewServer(cfgC)
		if err != nil {
			t.Fatal(err)
		}
		n := pipeNet{accept: make(chan transport.Conn)}
		cli, err := protocol.NewClient(cfgT, protocol.TransportDialer(n, func() (string, string) { return "citadel:7443", transport.RouteTower }))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{}, 2)
		go func() { _ = srv.Serve(ctx, protocol.FromTransportListener(n)); done <- struct{}{} }()
		go func() { _ = cli.Run(ctx); done <- struct{}{} }()
		defer func() {
			cancel()
			<-done
			<-done
		}()
		synctest.Wait()
		to := srv.Link(towerID)
		if !to.Connected() {
			t.Fatal("not connected over the transport")
		}
		if s := to.Session(); s.Peer().Role != protocol.RoleMachine || s.Peer().ID != towerID {
			t.Fatalf("peer = %+v", s.Peer())
		}
		callCtx, stop := clock.WithTimeout(ctx, clock.Real{}, 10*time.Second)
		defer stop()
		r, err := to.Call(callCtx, input("ls\n"))
		if err != nil || r.Status != protocol.AckOK || <-typed != "ls\n" {
			t.Fatalf("Call = %+v, %v", r, err)
		}
	})
}
