package protocol_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/protocol"
)

// OnReady fires once the connection is the current one of its link, with the
// peer; the replication starts its stream there.
func TestOnReadyFiresWhenTheConnectionIsTheCurrentOneOfTheLink(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		var mu sync.Mutex
		var ready []string
		var current bool
		w.cfgC.Hooks.OnReady = func(s *protocol.Session) {
			mu.Lock()
			defer mu.Unlock()
			ready = append(ready, s.Peer().ID)
			current = w.server.Link(s.Peer().ID).Session() == s
		}
		w.start()
		defer w.stop()
		settle()
		mu.Lock()
		defer mu.Unlock()
		if len(ready) != 1 || ready[0] != towerID || !current {
			t.Fatalf("OnReady = %v, current %v; want once, for the Tower, with the session already the link's", ready, current)
		}
	})
}

// The hello carries the epoch of the replica next to its lsn, and the Citadel
// sees both before it chooses between continuing and a full copy.
func TestTheHelloCarriesTheLSNAndTheEpochOfTheReplica(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.cfgT.Hello = func(context.Context) (protocol.HelloInfo, error) {
			return protocol.HelloInfo{Version: "0.0.0-test", Sheet: protocol.Object{}, ReplicaLSN: 41, ReplicaEpoch: 3}, nil
		}
		got := make(chan protocol.Hello, 1)
		w.cfgC.Hooks.OnHello = func(_ context.Context, _ protocol.Peer, h *protocol.Hello) error {
			got <- *h
			return nil
		}
		w.start()
		defer w.stop()
		settle()
		select {
		case h := <-got:
			if h.ReplicaLSN != 41 || h.ReplicaEpoch != 3 {
				t.Fatalf("hello = lsn %d epoch %d, want 41 and 3", h.ReplicaLSN, h.ReplicaEpoch)
			}
		default:
			t.Fatal("no hello")
		}
	})
}

// A finalizer that already has the content ends the transfer at xfer.begin:
// every part is taken as received and no part crosses the connection.
func TestAFinalizerThatAlreadyHasTheContentEndsTheTransferWithNoPartOnTheWire(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		fin := &haveFinalizer{have: true}
		w.cfgC.Xfer = &protocol.XferConfig{Dir: filepath.Join(t.TempDir(), "xfer"), Finalizers: map[string]protocol.Finalizer{protocol.KindFile: fin}}
		w.start()
		defer w.stop()
		settle()
		data := content(5 * 1024)
		err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Meta: protocol.Object{"path": json.RawMessage(`"x"`)}, Source: source(data), ChunkSize: 1024})
		if err != nil {
			t.Fatal(err)
		}
		if parts(w.sent(), 100) != 0 {
			t.Fatalf("%d parts crossed although the receiver had the content", parts(w.sent(), 100))
		}
		if fin.finalized != 0 || fin.asked != 1 || fin.sha != sum(data) {
			t.Fatalf("finalized %d times, asked %d times about %q", fin.finalized, fin.asked, fin.sha)
		}
		// When it does not have it, the content goes and the finalizer runs.
		fin.have = false
		if err := w.client.Link().Transfer(t.Context(), protocol.Transfer{Kind: protocol.KindFile, Meta: protocol.Object{"path": json.RawMessage(`"y"`)}, Source: source(data), ChunkSize: 1024}); err != nil {
			t.Fatal(err)
		}
		if parts(w.sent(), 100) != 5 || fin.finalized != 1 {
			t.Fatalf("parts = %d, finalized = %d; want 5 and 1", parts(w.sent(), 100), fin.finalized)
		}
	})
}

// The key ring travels in vault.replica: the type is volatile (never in the
// durable outbox, which is replicated) and the ring prints redacted.
func TestVaultReplicaIsVolatileAndItsKeyringNeverPrints(t *testing.T) {
	t.Parallel()
	spec, ok := protocol.DefaultRegistry().Spec(protocol.TypeVaultReplica)
	if !ok || !spec.Volatile || !spec.Ack || spec.Dir != protocol.CitadelToTower {
		t.Fatalf("vault.replica = %+v, want a volatile C→T type with ack", spec)
	}
	p := protocol.VaultReplica{Keyring: protocol.Secret("a2V5cmluZy1zZWNyZXQ="), Envelope: []byte{1}}
	if s := fmt.Sprintf("%v %+v", p, p); strings.Contains(s, "a2V5cmluZy1zZWNyZXQ=") {
		t.Fatalf("the key ring was printed: %s", s)
	}
}

type haveFinalizer struct {
	mu        sync.Mutex
	have      bool
	asked     int
	finalized int
	sha       string
}

func (h *haveFinalizer) Have(_ context.Context, b protocol.XferBegin) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asked++
	h.sha = b.SHA256
	return h.have
}

func (h *haveFinalizer) Finalize(context.Context, protocol.XferBegin, string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.finalized++
	return nil
}
