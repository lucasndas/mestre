package protocol_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// gated is a Finalizer that waits for release, so the transfer stays in
// progress (its parts are all here, the end is not).
type gated struct {
	release chan struct{}
	next    protocol.Finalizer
}

func (g gated) Finalize(ctx context.Context, b protocol.XferBegin, file string) error {
	select {
	case <-g.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return g.next.Finalize(ctx, b, file)
}

// Two transfers that begin one after the other cannot both take the same free
// space: what the first still needs is reserved while it is in progress, and
// given back when it ends.
func TestATransferInProgressReservesItsSpaceForTheNextBegin(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, rec, _ := xferWorld(t)
		free := uint64(protocol.XferDiskReserve + 30*1024)
		w.cfgC.Xfer.FreeSpace = func(string) (uint64, error) { return free, nil }
		g := gated{release: make(chan struct{}), next: rec}
		w.cfgC.Xfer.Finalizers[protocol.KindFile] = g
		w.start()
		defer w.stop()
		settle()
		first := make(chan error, 1)
		go func() {
			first <- w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Source: source(content(20 * 1024)), ChunkSize: 1024})
		}()
		settle() // every part of the first is there; its end waits

		err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Source: source(content(20 * 1024)), ChunkSize: 1024})
		var pe *protocol.Error
		if !errors.As(err, &pe) || pe.Code != protocol.CodeUnavailable {
			t.Fatalf("the second transfer = %v, want unavailable: the first holds its 20 KiB of the 30", err)
		}
		close(g.release)
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		// the space is back
		if err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Source: source(content(20 * 1024)), ChunkSize: 1024}); err != nil {
			t.Fatalf("after the first ended: %v", err)
		}
		if rec.count() != 2 {
			t.Fatalf("%d transfers arrived, want 2", rec.count())
		}
	})
}

// A data file whose description is gone (a crash between the two) is removed
// when it is as old as the TTL, with no description to say when it was
// touched.
func TestAPartFileWithoutItsDescriptionIsRemovedAfterTheTTL(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, _, dir := xferWorld(t)
		quiet := protocol.Timing{Ping: 48 * time.Hour, PongTimeout: 48 * time.Hour, Heartbeat: 48 * time.Hour, HeartbeatLost: 72 * time.Hour}
		w.cfgC.Timing, w.cfgT.Timing = quiet, quiet
		w.start()
		defer w.stop()
		settle()
		w.holdAt.Store(5)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- w.client.Link().Transfer(ctx, protocol.Transfer{Kind: protocol.KindFile, Source: source(content(20 * 1024)), ChunkSize: 1024})
		}()
		settle()
		cancel()
		<-done
		entries, _ := os.ReadDir(dir)
		if len(entries) != 2 {
			t.Fatalf("temporary files = %v, want the data and its description", entries)
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			if filepath.Ext(e.Name()) == ".json" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				continue
			}
			// stamped with the clock of the bubble, as the engine stamps its files
			now := clock.Real{}.Now()
			if err := os.Chtimes(path, now, now); err != nil {
				t.Fatal(err)
			}
		}
		advance(23 * time.Hour)
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Fatalf("after 23 hours: %v, want the data file still", entries)
		}
		advance(2 * time.Hour)
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatalf("after 25 hours: %v, want none", entries)
		}
	})
}
