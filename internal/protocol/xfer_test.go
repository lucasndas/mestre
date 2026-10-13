package protocol_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
)

// content is a reproducible blob of n bytes.
func content(n int) []byte {
	r := rand.New(rand.NewPCG(1, 2))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(r.UintN(256))
	}
	return b
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// received is the Finalizer of kind "file" on the Citadel: it keeps what it was
// handed.
type received struct {
	mu    sync.Mutex
	files [][]byte
	metas []protocol.Object
	err   error
}

func (r *received) Finalize(_ context.Context, b protocol.XferBegin, file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.files = append(r.files, data)
	r.metas = append(r.metas, b.Meta)
	return nil
}

func (r *received) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.files)
}

// xferWorld is a world whose Citadel receives transfers of kind file.
func xferWorld(t *testing.T) (*world, *received, string) {
	t.Helper()
	w := newWorld(t, opts{})
	rec := &received{}
	dir := filepath.Join(t.TempDir(), "xfer")
	w.cfgC.Xfer = &protocol.XferConfig{Dir: dir, Finalizers: map[string]protocol.Finalizer{protocol.KindFile: rec}}
	return w, rec, dir
}

func source(b []byte) protocol.Source {
	return protocol.Source{ReaderAt: bytes.NewReader(b), Size: int64(len(b))}
}

func TestAFileCrossesInPartsAndTheReceiverChecksTheSHA256(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		size  int
		chunk int
	}{
		{"zero bytes", 0, 1024},
		{"less than one part", 100, 1024},
		{"exactly two parts", 2048, 1024},
		{"an incomplete last part", 10*1024 + 7, 1024},
		{"many parts, more than the window", 300 * 1024, 1024},
		{"the default part size", 3*protocol.DefaultChunkSize + 5, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w, rec, dir := xferWorld(t)
				w.start()
				defer w.stop()
				settle()
				data := content(tc.size)
				meta := protocol.Object{"path": []byte(`"/srv/x/out.bin"`)}
				err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Meta: meta, Source: source(data), ChunkSize: tc.chunk})
				if err != nil {
					t.Fatal(err)
				}
				if rec.count() != 1 || sum(rec.files[0]) != sum(data) || string(rec.metas[0]["path"]) != `"/srv/x/out.bin"` {
					t.Fatalf("the Citadel got %d files, the first with %d bytes", rec.count(), len(rec.files[0]))
				}
				if entries, _ := os.ReadDir(dir); len(entries) != 0 {
					t.Fatalf("temporary files left: %v", entries)
				}
			})
		})
	}
}

// An interruption at part 40: when the connection returns the transfer goes
// on from part 40 and the final SHA-256 matches.
// CA-PROTO-04
func TestATransferInterruptedAtPart40ResumesFrom40(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, rec, _ := xferWorld(t)
		w.start()
		defer w.stop()
		settle()
		data := content(100 * 1024)
		w.holdAt.Store(40) // part 40 never leaves the Tower

		done := make(chan error, 1)
		go func() {
			done <- w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Source: source(data), ChunkSize: 1024})
		}()
		settle()
		select {
		case err := <-done:
			t.Fatalf("finished before the cut: %v", err)
		default:
		}
		if got := parts(w.sent(), 40); got != 40 {
			t.Fatalf("parts on the wire before the cut = %d, want 40 (0 to 39)", got)
		}

		w.holdAt.Store(-1)
		w.net.Cut(towerHost, citadelID)
		advance(5 * time.Second)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		// after the reconnection the first part sent is the 40th, not the 0th
		var after []uint64
		for _, f := range w.sent() {
			switch {
			case f.kind == protocol.FrameText && f.typ == protocol.TypeHello:
				after = after[:0] // a new connection: only what follows its opening counts
			case f.kind == protocol.FrameBinary:
				after = append(after, f.pos)
			}
		}
		if len(after) != 60 || after[0] != 40 || after[len(after)-1] != 99 {
			t.Fatalf("parts sent after reconnecting: %d, from %v", len(after), first(after, 3))
		}
		if rec.count() != 1 || sum(rec.files[0]) != sum(data) {
			t.Fatal("the content that arrived does not match")
		}
	})
}

func first(s []uint64, n int) []uint64 { return s[:min(n, len(s))] }

// parts counts the parts of a transfer on the wire.
func parts(frames []sentFrame, upTo uint64) int {
	seen := map[uint64]bool{}
	for _, f := range frames {
		if f.kind == protocol.FrameBinary && f.pos < upTo {
			seen[f.pos] = true
		}
	}
	return len(seen)
}

func TestAtMostSixteenPartsAreInFlight(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, _, _ := xferWorld(t)
		w.start()
		defer w.stop()
		settle()
		w.dropProgress.Store(true) // the receiver's progress is lost: the window never opens
		data := content(100 * 1024)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- w.client.Link().Transfer(ctx, protocol.Transfer{Kind: protocol.KindFile, Source: source(data), ChunkSize: 1024})
		}()
		advance(time.Minute)
		if got := parts(w.sent(), 1000); got != protocol.XferWindow {
			t.Fatalf("%d parts sent with no progress, want the window of %d", got, protocol.XferWindow)
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Transfer = %v", err)
		}
	})
}

func TestTheProgressOfTheReceiverFreesTheWindow(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, _, _ := xferWorld(t)
		w.start()
		defer w.stop()
		settle()
		data := content(64 * 1024)
		if err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Source: source(data), ChunkSize: 1024}); err != nil {
			t.Fatal(err)
		}
		var progress int
		w.citadelLog(func(typ string) {
			if typ == protocol.TypeXferProgress {
				progress++
			}
		})
		if progress < 64/protocol.XferProgressEvery {
			t.Fatalf("the receiver reported progress %d times", progress)
		}
	})
}

func TestAPartThatArrivesCorruptedFailsTheSHAAndTheTempFilesGo(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, rec, dir := xferWorld(t)
		w.start()
		defer w.stop()
		settle()
		w.corruptAt.Store(3)
		data := content(10 * 1024)
		err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Source: source(data), ChunkSize: 1024})
		var pe *protocol.Error
		if !errors.As(err, &pe) || pe.Code != protocol.CodeHashMismatch || !pe.Retryable {
			t.Fatalf("Transfer = %v, want hash_mismatch (retryable)", err)
		}
		if rec.count() != 0 {
			t.Fatal("a corrupted file reached the finalizer")
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Fatalf("temporary files left: %v", entries)
		}
		// a new transfer of the same content, with a new id, works
		if err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Source: source(data), ChunkSize: 1024}); err != nil {
			t.Fatal(err)
		}
		if rec.count() != 1 {
			t.Fatal("the retry did not arrive")
		}
	})
}

func TestTheErrorOfTheFinalizerReachesTheSender(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, rec, _ := xferWorld(t)
		rec.err = protocol.NewError(protocol.CodeForbidden, "not here")
		w.start()
		defer w.stop()
		settle()
		err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Source: source(content(2000)), ChunkSize: 1024})
		var pe *protocol.Error
		if !errors.As(err, &pe) || pe.Code != protocol.CodeForbidden {
			t.Fatalf("Transfer = %v, want forbidden", err)
		}
	})
}

func TestAKindWithoutAReceiverIsUnavailable(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, _, _ := xferWorld(t)
		w.start()
		defer w.stop()
		settle()
		err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindRelease, Source: source(content(10))})
		var pe *protocol.Error
		if !errors.As(err, &pe) || pe.Code != protocol.CodeUnavailable {
			t.Fatalf("Transfer = %v, want unavailable", err)
		}
	})
}

func TestTemporaryFilesNobodyTouchedFor24HoursAreRemoved(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, _, dir := xferWorld(t)
		w.start()
		defer w.stop()
		settle()
		w.holdAt.Store(5)
		data := content(20 * 1024)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- w.client.Link().Transfer(ctx, protocol.Transfer{Kind: protocol.KindFile, Source: source(data), ChunkSize: 1024})
		}()
		settle()
		cancel() // the sender gives up: the temporary file stays
		<-done
		if entries, _ := os.ReadDir(dir); len(entries) != 2 {
			t.Fatalf("temporary files = %v, want the data and its description", entries)
		}
		advance(23 * time.Hour)
		if n := w.server.Link(towerID).PruneTransfers(24 * time.Hour); n != 0 {
			t.Fatalf("pruned %d files after 23 hours", n)
		}
		advance(2 * time.Hour)
		if n := w.server.Link(towerID).PruneTransfers(24 * time.Hour); n != 2 {
			t.Fatalf("pruned %d files after 25 hours, want 2", n)
		}
	})
}

func TestARepeatedBeginOfAFinishedTransferDoesNotSendItAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, rec, _ := xferWorld(t)
		w.start()
		defer w.stop()
		settle()
		data := content(5 * 1024)
		id := "01J9Z0000000000000000000X1"
		tr := protocol.Transfer{ID: id, Kind: protocol.KindFile, Source: source(data), ChunkSize: 1024}
		if err := w.client.Link().Transfer(t.Context(), tr); err != nil {
			t.Fatal(err)
		}
		before := parts(w.sent(), 1000)
		if err := w.client.Link().Transfer(t.Context(), tr); err != nil { // the done was lost, say
			t.Fatal(err)
		}
		if parts(w.sent(), 1000) != before || rec.count() != 1 {
			t.Fatal("a finished transfer was sent again")
		}
	})
}
