package protocol

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
)

// sink is a Conn that records what is written and can hold the writer.
type sink struct {
	mu     sync.Mutex
	frames []Frame
	gate   chan struct{} // when not nil, Send waits for it to be closed
	closed []int
}

func (s *sink) Write(ctx context.Context, kind FrameKind, data []byte) error {
	f := Frame{Kind: kind, Data: data}
	s.mu.Lock()
	g := s.gate
	s.mu.Unlock()
	if g != nil {
		select {
		case <-g:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	s.frames = append(s.frames, f)
	s.mu.Unlock()
	return nil
}

func (s *sink) Read(ctx context.Context) (FrameKind, []byte, error) {
	<-ctx.Done()
	return 0, nil, ctx.Err()
}
func (s *sink) Peer() Peer { return Peer{} }
func (s *sink) Close(code int, _ string) error {
	s.mu.Lock()
	s.closed = append(s.closed, code)
	s.mu.Unlock()
	return nil
}

func (s *sink) sent() []Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Frame(nil), s.frames...)
}

func ctrl(tag string) Frame { return Frame{Kind: FrameText, Data: []byte(tag)} }
func data(n int) Frame      { return Frame{Kind: FrameBinary, Data: make([]byte, n)} }

func runLanes(t *testing.T, l *lanes, c Conn) (wait func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- l.run(ctx, c) }()
	return func() error { return <-done }
}

func TestTheControlLaneIsEmptiedBeforeTheDataLane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l, c := newLanes(), &sink{gate: make(chan struct{})}
		for range 10 {
			if err := l.pushData(t.Context(), data(1024)); err != nil {
				t.Fatal(err)
			}
		}
		_ = l.pushControl(ctrl("a"))
		_ = l.pushControl(ctrl("b"))
		runLanes(t, l, c)
		synctest.Wait()
		close(c.gate)
		synctest.Wait()
		got := c.sent()
		// the writer took the control frames first: the data frames wait
		if len(got) < 12 || string(got[0].Data) != "a" || string(got[1].Data) != "b" {
			t.Fatalf("the first frames were %q %q (of %d)", got[0].Data, got[1].Data, len(got))
		}
		l.stop(ErrSessionClosed)
	})
}

func TestNoMoreThanTheBudgetOfDataGoesBetweenTwoLooksAtTheControlLane(t *testing.T) {
	t.Parallel()
	for _, frame := range []int{1 << 10, 16 << 10, 20 << 10} {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				l, c := newLanes(), &sink{}
				l.limit = 1 << 30
				for range 200 {
					_ = l.pushData(t.Context(), data(frame))
				}
				// hold the writer inside its first batch, then add a control frame
				c.gate = make(chan struct{})
				runLanes(t, l, c)
				synctest.Wait()
				_ = l.pushControl(ctrl("hb"))
				close(c.gate)
				synctest.Wait()
				l.stop(ErrSessionClosed)

				var bytes, idx int
				for i, f := range c.sent() {
					if f.Kind == FrameText {
						idx = i
						break
					}
					bytes += len(f.Data)
				}
				// the batch that was already taken may finish (budget), and then
				// the control frame goes before any other data
				if bytes > DataBudget {
					t.Fatalf("%d bytes of data (%d frames) before the control frame, budget is %d", bytes, idx, DataBudget)
				}
				if idx == 0 || idx == len(c.sent())-1 {
					t.Fatalf("control frame at %d of %d: the test did not interleave", idx, len(c.sent()))
				}
			})
		})
	}
}

func TestAFrameBiggerThanTheBudgetGoesWholeInItsOwnTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l, c := newLanes(), &sink{gate: make(chan struct{})}
		for range 3 {
			_ = l.pushData(t.Context(), data(256<<10)) // a part of a transfer
		}
		runLanes(t, l, c)
		synctest.Wait()
		_ = l.pushControl(ctrl("hb"))
		close(c.gate)
		synctest.Wait()
		l.stop(ErrSessionClosed)
		got := c.sent()
		if len(got) < 2 || got[0].Kind != FrameBinary || len(got[0].Data) != 256<<10 || string(got[1].Data) != "hb" {
			t.Fatalf("frames = %d, second is %q: want one whole part and then the control frame", len(got), got[1].Data)
		}
	})
}

func TestAProducerWaitsWhileTheDataQueueIsFull(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l, c := newLanes(), &sink{gate: make(chan struct{})}
		l.limit = 64 << 10
		runLanes(t, l, c)
		pushed := make(chan int, 1)
		go func() {
			n := 0
			for range 100 {
				if err := l.pushData(t.Context(), data(16<<10)); err != nil {
					break
				}
				n++
			}
			pushed <- n
		}()
		synctest.Wait()
		select {
		case <-pushed:
			t.Fatal("the producer did not wait for the writer")
		default:
		}
		close(c.gate)
		synctest.Wait()
		if n := <-pushed; n != 100 {
			t.Fatalf("pushed %d frames", n)
		}
		l.stop(ErrSessionClosed)
	})
}

func TestACloseGoesAfterWhatWasQueuedBeforeIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		l, c := newLanes(), &sink{}
		_ = l.pushControl(ctrl("error"))
		l.pushClose(4006, "stale epoch")
		_ = l.pushControl(ctrl("late"))
		wait := runLanes(t, l, c)
		if err := wait(); err != errLocalClose {
			t.Fatalf("run = %v", err)
		}
		got := c.sent()
		if len(got) != 1 || string(got[0].Data) != "error" || len(c.closed) != 1 || c.closed[0] != 4006 {
			t.Fatalf("frames = %v, closed = %v", got, c.closed)
		}
		if err := l.pushControl(ctrl("after")); err == nil {
			t.Fatal("a frame was accepted after the close")
		}
	})
}
