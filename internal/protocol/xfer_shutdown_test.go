package protocol_test

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/protocol"
)

// stuck is a Finalizer that never finishes by itself: it ends when its context
// does. It says whether it started and whether it came back.
type stuck struct {
	entered, left atomic.Bool
}

func (s *stuck) Finalize(ctx context.Context, _ protocol.XferBegin, _ string) error {
	s.entered.Store(true)
	<-ctx.Done()
	s.left.Store(true)
	return ctx.Err()
}

// The Finalizer of a transfer runs after the session that received its end
// may be gone, and it writes where the owner of the Server keeps its files and
// databases: Serve does not return before it is done, and ends it by
// canceling its context.
func TestTheCitadelWaitsForTheFinalizerOfATransferWhenItStops(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		fin := &stuck{}
		w.cfgC.Xfer = &protocol.XferConfig{Dir: filepath.Join(t.TempDir(), "xfer"), Finalizers: map[string]protocol.Finalizer{protocol.KindFile: fin}}
		w.start()
		settle()
		ctx, cancel := context.WithCancel(t.Context())
		sent := make(chan struct{})
		go func() {
			defer close(sent)
			_ = w.client.Link().Transfer(ctx, protocol.Transfer{Kind: protocol.KindFile, Source: source(content(4 * 1024)), ChunkSize: 1024})
		}()
		settle() // every part is there; the Finalizer holds the end
		if !fin.entered.Load() {
			t.Fatal("the Finalizer did not start")
		}

		w.stop()
		if !fin.left.Load() {
			t.Error("Serve returned while the Finalizer of a transfer was still running")
		}
		cancel()
		<-sent
	})
}

// The same on a Tower: Client.Run does not return before the Finalizer of a
// transfer from the Citadel is done, so the engine closes the replica after it.
func TestTheTowerWaitsForTheFinalizerOfATransferWhenItStops(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		fin := &stuck{}
		w.cfgT.Xfer = &protocol.XferConfig{Dir: filepath.Join(t.TempDir(), "xfer"), Finalizers: map[string]protocol.Finalizer{protocol.KindFile: fin}}
		w.start()
		settle()
		ctx, cancel := context.WithCancel(t.Context())
		sent := make(chan struct{})
		go func() {
			defer close(sent)
			_ = w.toTower().Transfer(ctx, protocol.Transfer{Kind: protocol.KindFile, Source: source(content(4 * 1024)), ChunkSize: 1024})
		}()
		settle()
		if !fin.entered.Load() {
			t.Fatal("the Finalizer did not start")
		}

		w.stop()
		if !fin.left.Load() {
			t.Error("Client.Run returned while the Finalizer of a transfer was still running")
		}
		cancel()
		<-sent
	})
}
