package terminal_test

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/terminal/ptywire"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// slowWriter is the end of a connection whose writes wait for the gate: a
// mestre-pty that read the hello of the engine and was not scheduled to answer.
type slowWriter struct {
	net.Conn
	gate    chan struct{}
	answers chan struct{} // closed when the mestre-pty has the hello and goes to answer
	once    *sync.Once
}

func (s slowWriter) Write(b []byte) (int, error) {
	s.once.Do(func() { close(s.answers) })
	<-s.gate
	return s.Conn.Write(b)
}

// handshakeWithASlowPty runs the handshake of the engine against a mestre-pty
// that holds its answer until release is called. It returns once the engine
// armed its timer and the mestre-pty read the hello (the sync points); the result
// of NewClient arrives on the channel.
func handshakeWithASlowPty(t *testing.T, clk *clock.Fake, timeout time.Duration) (release func(), result <-chan error) {
	t.Helper()
	secret := bytes.Repeat([]byte{7}, ptywire.SecretSize)
	server, client := net.Pipe()
	gate, answers := make(chan struct{}), make(chan struct{})
	go func() {
		defer server.Close()
		_, _ = ptywire.ServerHandshake(ptywire.NewConn(slowWriter{server, gate, answers, new(sync.Once)}), secret, ptywire.Info{Proto: ptywire.ProtoVersion, Version: "1.0.0", PID: 1})
	}()
	res := make(chan error, 1)
	go func() {
		c, err := terminal.NewClient(client, secret, clk, timeout)
		if err == nil {
			_ = c.Close()
		}
		res <- err
	}()
	clk.BlockUntil(1) // the engine has its timer armed
	<-answers         // and the mestre-pty read the hello: the engine is waiting for the answer
	var once sync.Once
	return func() { once.Do(func() { close(gate) }) }, res
}

// The cause of the "use of closed network connection" the full suite got on
// Windows: the handshake timeout of the engine runs from the connect, so a mestre-pty the
// machine is too busy to schedule is cut by the engine's own timer, which
// closes the socket under the read of the hello.
func TestAnEngineThatWaitsForTheHelloLongerThanItsTimeoutClosesItsOwnSocket(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	release, result := handshakeWithASlowPty(t, clk, 0)
	defer release()
	clk.Advance(ptywire.EngineHandshakeTimeout)
	err := <-result
	if !errors.Is(err, ptywire.ErrAuth) || !strings.Contains(err.Error(), "reading hello") || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("NewClient = %v, want a refused handshake reading the hello from a closed connection", err)
	}
}

// The defaults are 30 s for the mestre-pty and 35 s for the engine: a busy user
// machine is slow to schedule the process it just started, and the engine,
// which counts from the connect, never gives up before the mestre-pty does.
func TestTheHandshakeDefaultsAre30SecondsAndTheEngineWaitsLongerThanThePty(t *testing.T) {
	t.Parallel()
	if ptywire.HandshakeTimeout != 30*time.Second {
		t.Errorf("HandshakeTimeout = %v, want 30s", ptywire.HandshakeTimeout)
	}
	if ptywire.EngineHandshakeTimeout != 35*time.Second || ptywire.EngineHandshakeTimeout < ptywire.HandshakeTimeout {
		t.Errorf("EngineHandshakeTimeout = %v, want 35s and not shorter than the mestre-pty's", ptywire.EngineHandshakeTimeout)
	}
}

// The timeout is the engine's to choose (Deps.TerminalHandshake): a mestre-pty
// that answers after the default, within the one given, is accepted.
func TestAnEngineWithALongerHandshakeTimeoutWaitsForASlowMestrePty(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	release, result := handshakeWithASlowPty(t, clk, 2*ptywire.EngineHandshakeTimeout)
	defer release()
	clk.Advance(ptywire.EngineHandshakeTimeout + time.Second) // past the default: nothing is closed
	release()
	if err := <-result; err != nil {
		t.Fatalf("NewClient = %v, want the slow mestre-pty accepted", err)
	}
}
