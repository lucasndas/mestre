package protocol

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// nopEpoch, nopCerts and nopStore are the least a Config needs for a test of
// the link alone (protorig imports this package and cannot be used here).
type (
	nopEpoch struct{}
	nopStore struct{}
)

func (nopEpoch) Epoch() uint64                             { return 1 }
func (nopEpoch) Certificate() *Certificate                 { return &Certificate{Epoch: 1} }
func (nopEpoch) Follow(context.Context, Certificate) error { return nil }
func (nopEpoch) Stale(context.Context, Peer)               {}
func (nopEpoch) Verify(Certificate) error                  { return nil }

func (nopStore) Put(context.Context, string, Envelope) error                   { return nil }
func (nopStore) Done(context.Context, string, string) error                    { return nil }
func (nopStore) Pending(context.Context, string) ([]Envelope, error)           { return nil, nil }
func (nopStore) Finish(context.Context, string, string, []byte) error          { return nil }
func (nopStore) Release(context.Context, string, string) error                 { return nil }
func (nopStore) Prune(context.Context, string, time.Time, string) (int, error) { return 0, nil }
func (nopStore) Claim(context.Context, string, string, string, time.Time) (bool, []byte, error) {
	return true, nil, nil
}

// A message sent now to a connection that ended but is not yet detached from
// the link went nowhere: it is the error of no connection, as it is one moment
// later, not a closed-connection error that reads like a lost answer.
func TestASendOnAConnectionThatEndedBeforeItDetachedIsNotConnected(t *testing.T) {
	t.Parallel()
	cfg := Config{MachineID: "citadel", Clock: clock.Real{}, Epoch: nopEpoch{}, Certs: nopEpoch{}, Outbox: nopStore{}, Processed: nopStore{}}
	if err := cfg.prepare(); err != nil {
		t.Fatal(err)
	}
	l := newLink(&cfg, "tower1", SideCitadel)
	s := newSession(&cfg, l, &sink{}, 1, SideCitadel, 1, nil)
	l.sess = s
	s.lanes.stop(ErrSessionClosed) // the end of the connection, before l.detach(s)

	_, err := l.Send(t.Context(), Outgoing{Type: TypeTableWhere, Payload: TableRef{Workspace: "w", Table: "t"}, Transient: true})
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("send on an ended connection: %v", err)
	}
}

// An older connection of a machine that ends after a newer one took its
// place must not close the temporary file that the newer one is writing: the
// parts would be dropped and the transfer would stall.
func TestAReplacedConnectionThatEndsLateLeavesTheTransferOfTheNewOneOpen(t *testing.T) {
	t.Parallel()
	cfg := Config{
		MachineID: "citadel", Clock: clock.Real{}, Epoch: nopEpoch{}, Certs: nopEpoch{}, Outbox: nopStore{}, Processed: nopStore{},
		Xfer: &XferConfig{
			Dir:        filepath.Join(t.TempDir(), "xfer"),
			Finalizers: map[string]Finalizer{KindFile: FinalizerFunc(func(context.Context, XferBegin, string) error { return nil })},
			FreeSpace:  func(string) (uint64, error) { return 1 << 40, nil },
		},
	}
	if err := cfg.prepare(); err != nil {
		t.Fatal(err)
	}
	l := newLink(&cfg, "tower1", SideCitadel)
	older := newSession(&cfg, l, &sink{}, 1, SideCitadel, 1, nil)
	newer := newSession(&cfg, l, &sink{}, 2, SideCitadel, 1, nil)
	l.sess = newer // the newer connection is the current one

	id := "01J9Z0000000000000000000S3"
	begin := &XferBegin{XferID: id, Kind: KindFile, Size: 4096, SHA256: strings.Repeat("0", 64), ChunkSize: 1024}
	if _, err := l.xfer.onBegin(t.Context(), begin); err != nil {
		t.Fatal(err)
	}
	l.detach(older) // the older one ends now, late
	r := l.xfer.recv[id]
	r.mu.Lock()
	open := r.f != nil
	r.mu.Unlock()
	if !open {
		t.Fatal("the end of the replaced connection closed the file of the transfer on the newer one")
	}
	l.detach(newer) // the current one ending does release it, to be opened again by the next xfer.begin
	r.mu.Lock()
	open = r.f != nil
	r.mu.Unlock()
	if open {
		t.Fatal("the end of the current connection left the file open")
	}
}
