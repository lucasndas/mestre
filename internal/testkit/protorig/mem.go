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
	outbox    map[string]*queue
	processed map[[2]string]*claimed
	claims    int
}

// queue is the outbox of one peer, in the order of Put; Done leaves a hole
// that a later Put sweeps away, so thousands of messages cost little.
type queue struct {
	envs []protocol.Envelope
	gone []bool
	at   map[string]int // id -> index in envs, for the ones not done
}

func (q *queue) live() []protocol.Envelope {
	out := make([]protocol.Envelope, 0, len(q.at))
	for i, e := range q.envs {
		if !q.gone[i] {
			out = append(out, e)
		}
	}
	return out
}

func (q *queue) compact() {
	envs := q.live()
	q.envs, q.gone, q.at = envs, make([]bool, len(envs)), make(map[string]int, len(envs))
	for i, e := range envs {
		q.at[e.ID] = i
	}
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
	return &MemStore{outbox: map[string]*queue{}, processed: map[[2]string]*claimed{}}
}

func (m *MemStore) queue(peer string) *queue {
	q := m.outbox[peer]
	if q == nil {
		q = &queue{at: map[string]int{}}
		m.outbox[peer] = q
	}
	return q
}

// Put implements protocol.Outbox.
func (m *MemStore) Put(_ context.Context, peer string, env protocol.Envelope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queue(peer)
	if _, ok := q.at[env.ID]; ok {
		return nil
	}
	if len(q.envs) > 64 && len(q.at) < len(q.envs)/2 {
		q.compact()
	}
	q.at[env.ID] = len(q.envs)
	q.envs = append(q.envs, env)
	q.gone = append(q.gone, false)
	return nil
}

// Done implements protocol.Outbox.
func (m *MemStore) Done(_ context.Context, peer, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queue(peer)
	if i, ok := q.at[id]; ok {
		q.gone[i] = true
		delete(q.at, id)
	}
	return nil
}

// Pending implements protocol.Outbox.
func (m *MemStore) Pending(_ context.Context, peer string) ([]protocol.Envelope, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.queue(peer).live(), nil
}

// Waiting is how many messages of the peer wait for an ack.
func (m *MemStore) Waiting(peer string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue(peer).at)
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
func (m *MemStore) Prune(_ context.Context, sender string, cutoff time.Time, floor string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k, c := range m.processed {
		if k[0] == sender && c.at.Before(cutoff) && k[1] < floor {
			delete(m.processed, k)
			n++
		}
	}
	return n, nil
}

// Remembered is how many ids of the sender are remembered now.
func (m *MemStore) Remembered(sender string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for k := range m.processed {
		if k[0] == sender {
			n++
		}
	}
	return n
}

// Clone is a copy of the store, as the replication hands the outbox and the
// processed ids of an old Citadel to the one promoted.
func (m *MemStore) Clone() *MemStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := NewMemStore()
	for peer, q := range m.outbox {
		cq := c.queue(peer)
		for _, e := range q.live() {
			cq.at[e.ID] = len(cq.envs)
			cq.envs = append(cq.envs, e)
			cq.gone = append(cq.gone, false)
		}
	}
	for k, v := range m.processed {
		cp := *v
		c.processed[k] = &cp
	}
	return c
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
