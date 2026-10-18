package protocol_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/protocol"
)

// holdsOpen says whether this process has path open. Where there is no
// /proc/self/fd (Windows), a file that is open cannot be removed, which says the
// same; the file is gone then.
func holdsOpen(t *testing.T, path string) bool {
	t.Helper()
	if runtime.GOOS == "windows" {
		return os.Remove(path) != nil
	}
	fds, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("the open files of the process cannot be listed: %v", err)
	}
	for _, fd := range fds {
		if target, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name())); err == nil && target == path {
			return true
		}
	}
	return false
}

// partOf is the temporary file of the one transfer in progress in dir.
func partOf(t *testing.T, dir string) string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Fatalf("no transfer in progress in %s: %v", dir, entries)
	return ""
}

// CA-REPL-14
// A machine that leaves the Guild erases the folder of the transfers, and on
// Windows a file that is open cannot be removed: the transfers in progress are
// stopped first (docs/guilda/replicacao.md#sair-da-guilda).
func TestStoppingTheTransfersClosesTheFilesInProgress(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, _, dir := xferWorld(t)
		w.start()
		defer w.stop()
		settle()
		w.holdAt.Store(5) // the sender stops at part 5, with the file open
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			done <- w.client.Link().Transfer(ctx, protocol.Transfer{Kind: protocol.KindFile, Source: source(content(20 * 1024)), ChunkSize: 1024})
		}()
		settle()
		part := partOf(t, dir)
		if !holdsOpen(t, part) {
			t.Fatal("set-up: the file of the transfer in progress is not open")
		}

		w.server.Link(towerID).StopTransfers()
		if _, err := os.Stat(part); err != nil {
			t.Errorf("the temporary file is for the caller to erase, and %v", err)
		} else if holdsOpen(t, part) {
			t.Error("the file of the transfer in progress is still open: it cannot be erased on Windows")
		}
		cancel()
		<-done
	})
}

// CA-REPL-14
// What begins after the stop is refused, and the sender is told it will not do:
// the Wipe has erased the folder, and a transfer that began after it would put
// a copy of the database back.
func TestATransferThatBeginsAfterTheStopIsForbidden(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w, rec, dir := xferWorld(t)
		w.start()
		defer w.stop()
		settle()
		w.server.Link(towerID).StopTransfers()
		err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Source: source(content(10))})
		var pe *protocol.Error
		if !errors.As(err, &pe) || pe.Code != protocol.CodeForbidden {
			t.Errorf("a transfer after StopTransfers = %v, want forbidden", err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 || rec.count() != 0 {
			t.Errorf("after the stop the receiver got %d files and left %v", rec.count(), entries)
		}
	})
}
