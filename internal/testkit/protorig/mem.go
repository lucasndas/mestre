package protorig

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/pkg/clock"
)

// MemStore is the in-memory Outbox and Processed. It survives a "restart" of
// the machine in a test because the test keeps the value and builds a new
// Client or Server over it.
type MemStore struct {
	mu        sync.Mutex
	outbox    map[string][]protocol.Envelope
	processed map[[2]string]*claimed
	claims    int
}

type claimed struct {
	at  time.Time
	ack []byte
}

var (
	_ protocol.Outbox    = (*MemStore)(nil)
	_ protocol.Processed = (*MemStore)(nil)
)

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{outbox: map[string][]protocol.Envelope{}, processed: map[[2]string]*claimed{}}
}

// Put implements protocol.Outbox.
func (m *MemStore) Put(_ context.Context, peer string, env protocol.Envelope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.outbox[peer] {
		if e.ID == env.ID {
			return nil
		}
	}
	m.outbox[peer] = append(m.outbox[peer], env)
	return nil
}

// Done implements protocol.Outbox.
func (m *MemStore) Done(_ context.Context, peer, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.outbox[peer]
	for i, e := range q {
		if e.ID == id {
			m.outbox[peer] = append(q[:i:i], q[i+1:]...)
			break
		}
	}
	return nil
}

// Pending implements protocol.Outbox.
func (m *MemStore) Pending(_ context.Context, peer string) ([]protocol.Envelope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]protocol.Envelope(nil), m.outbox[peer]...), nil
}

// Waiting is how many messages of the peer wait for an ack.
func (m *MemStore) Waiting(peer string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.outbox[peer])
}

// Claim implements protocol.Processed.
func (m *MemStore) Claim(_ context.Context, sender, id, _ string, now time.Time) (bool, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := [2]string{sender, id}
	if c, ok := m.processed[k]; ok {
		return false, c.ack, nil
	}
	m.processed[k] = &claimed{at: now}
	m.claims++
	return true, nil, nil
}

// Finish implements protocol.Processed.
func (m *MemStore) Finish(_ context.Context, sender, id string, ack []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.processed[[2]string{sender, id}]; ok {
		c.ack = ack
	}
	return nil
}

// Release implements protocol.Processed.
func (m *MemStore) Release(_ context.Context, sender, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.processed, [2]string{sender, id})
	return nil
}

// Prune implements protocol.Processed.
func (m *MemStore) Prune(_ context.Context, cutoff time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, c := range m.processed {
		if c.at.Before(cutoff) {
			delete(m.processed, k)
			n++
		}
	}
	return n, nil
}

// Claims is how many ids were ever claimed as new.
func (m *MemStore) Claims() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.claims
}

// Epochs is a fake protocol.EpochState and protocol.CertVerifier: a
// certificate is valid when its signature is "ok".
type Epochs struct {
	mu       sync.Mutex
	cert     protocol.Certificate
	followed []uint64
	stale    int
	onFollow func(protocol.Certificate) error
}

var (
	_ protocol.EpochState   = (*Epochs)(nil)
	_ protocol.CertVerifier = (*Epochs)(nil)
)

// SignedOK is the signature that the fake verifier accepts.
const SignedOK = "ok"

// Cert builds the certificate of an epoch; signature is SignedOK for a valid
// one.
func Cert(epoch uint64, citadel, signature string) protocol.Certificate {
	return protocol.Certificate{
		GuildID: "guild-1", Epoch: epoch, CitadelMachineID: citadel, ApprovedBy: "dono", ApprovalID: "ap-1",
		IssuedAt: protocol.Timestamp{Time: time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)}, Signature: signature,
	}
}

// NewEpochs returns the state of a machine that follows the epoch, whose
// Citadel is the machine `citadel`.
func NewEpochs(epoch uint64, citadel string) *Epochs {
	return &Epochs{cert: Cert(epoch, citadel, SignedOK)}
}

// Epoch implements protocol.EpochState.
func (e *Epochs) Epoch() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cert.Epoch
}

// Certificate implements protocol.EpochState.
func (e *Epochs) Certificate() *protocol.Certificate {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.cert
	return &c
}

// Follow implements protocol.EpochState.
func (e *Epochs) Follow(_ context.Context, cert protocol.Certificate) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.onFollow != nil {
		if err := e.onFollow(cert); err != nil {
			return err
		}
	}
	e.cert = cert
	e.followed = append(e.followed, cert.Epoch)
	return nil
}

// Stale implements protocol.EpochState.
func (e *Epochs) Stale(context.Context, protocol.Peer) {
	e.mu.Lock()
	e.stale++
	e.mu.Unlock()
}

// Set makes the machine follow cert without going through Follow, as if it
// had learned the epoch some other way (a test of the machine that is ahead).
func (e *Epochs) Set(cert protocol.Certificate) {
	e.mu.Lock()
	e.cert = cert
	e.mu.Unlock()
}

// OnFollow makes Follow call f first; an error refuses the certificate.
func (e *Epochs) OnFollow(f func(protocol.Certificate) error) {
	e.mu.Lock()
	e.onFollow = f
	e.mu.Unlock()
}

// Followed lists the epochs that Follow took, in order.
func (e *Epochs) Followed() []uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]uint64(nil), e.followed...)
}

// StaleCalls is how many times a peer said our epoch was old.
func (e *Epochs) StaleCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stale
}

// Verify implements protocol.CertVerifier.
func (e *Epochs) Verify(c protocol.Certificate) error {
	if c.Signature != SignedOK {
		return errors.New("bad signature")
	}
	return nil
}

// Parts are the pieces of a protocol.Config that the test may want to look at.
type Parts struct {
	Store  *MemStore
	Epochs *Epochs
}

// Config returns a configuration of a machine that follows the epoch, with
// the in-memory stores, the fake epoch and a minimal hello; the test adjusts
// it before building the Client or the Server.
func Config(clk clock.Clock, machineID string, epoch uint64, citadel string) (protocol.Config, Parts) {
	p := Parts{Store: NewMemStore(), Epochs: NewEpochs(epoch, citadel)}
	cfg := protocol.Config{
		MachineID: machineID, Clock: clk, Epoch: p.Epochs, Certs: p.Epochs, Outbox: p.Store, Processed: p.Store,
		BinaryVersion: "0.0.0-test",
		Rand:          func() float64 { return 0.5 }, // no jitter
		Hello: func(context.Context) (protocol.HelloInfo, error) {
			return protocol.HelloInfo{Version: "0.0.0-test", Sheet: protocol.Object{}}, nil
		},
	}
	return cfg, p
}
