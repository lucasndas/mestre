// Package clocksock is the test's end of MESTRE_TEST_CLOCK
// (docs/qualidade/dubles.md): one fake clock for every process of a test of
// the full suite. The test serves a Unix socket; each process compiled with
// the tag full connects (clock.Remote), receives the time, and applies every
// advance the test makes. The test goes on only after every process answered.
package clocksock

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// answerWithin bounds the wait for a process to answer: a process that died
// is dropped.
const answerWithin = 10 * time.Second

// Server is the clock of the test.
type Server struct {
	ln net.Listener

	mu    sync.Mutex
	now   time.Time
	peers []*peer
	done  chan struct{}
}

type peer struct {
	c net.Conn
	r *bufio.Reader
}

// Start serves the clock at path, starting at start.
func Start(path string, start time.Time) (*Server, error) {
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("clocksock: %w", err)
	}
	s := &Server{ln: ln, now: start, done: make(chan struct{})}
	go s.accept()
	return s, nil
}

func (s *Server) accept() {
	defer close(s.done)
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		p := &peer{c: c, r: bufio.NewReader(c)}
		s.mu.Lock()
		if err := p.send("set " + s.now.Format(time.RFC3339Nano)); err != nil {
			c.Close()
		} else {
			s.peers = append(s.peers, p)
		}
		s.mu.Unlock()
	}
}

func (p *peer) send(line string) error {
	_ = p.c.SetDeadline(clock.Real{}.Now().Add(answerWithin))
	if _, err := fmt.Fprintf(p.c, "%s\n", line); err != nil {
		return err
	}
	ans, err := p.r.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(ans, "ok ") {
		return errors.New("clocksock: " + strings.TrimSpace(ans))
	}
	return nil
}

// Now is the time of the test.
func (s *Server) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Peers is how many processes follow the clock.
func (s *Server) Peers() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.peers)
}

// Advance moves the clock of every process forward by d and returns when
// each one applied it. A process that does not answer (it was stopped, or it
// answered something else than ok) is dropped, and the error names it: the
// test must not go on as if every process had applied the new time.
func (s *Server) Advance(d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = s.now.Add(d)
	var errs []error
	live := s.peers[:0]
	for _, p := range s.peers {
		if err := p.send("advance " + d.String()); err != nil {
			errs = append(errs, fmt.Errorf("clocksock: a process did not apply advance %v: %w", d, err))
			p.c.Close()
			continue
		}
		live = append(live, p)
	}
	s.peers = live
	return errors.Join(errs...)
}

// Close stops the server.
func (s *Server) Close() {
	_ = s.ln.Close()
	<-s.done
	s.mu.Lock()
	for _, p := range s.peers {
		p.c.Close()
	}
	s.peers = nil
	s.mu.Unlock()
}
