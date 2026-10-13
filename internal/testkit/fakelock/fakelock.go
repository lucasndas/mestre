// Package fakelock is the fake of the lock of the epoch (guild.Lock) for the
// tests of milestone 2 (docs/qualidade/dubles.md): the refs epoca-N in
// memory, where creating one that exists fails as on the GitHub, the lock
// unreachable for the whole Guild or for one machine ("GitHub fora"), and a
// check that waits until the test lets it go.
package fakelock

import (
	"context"
	"errors"
	"sync"

	"github.com/lucasndas/mestre/internal/guild"
)

// ErrDown is the error of a lock that cannot be reached.
var ErrDown = errors.New("fakelock: the lock cannot be reached")

// Lock is the shared state; For gives the lock as one machine sees it.
type Lock struct {
	mu      sync.Mutex
	refs    map[uint64]string // epoch → the machine that created it
	down    map[string]bool
	allDown bool
	held    chan struct{} // while not nil, Check waits for it to close
	checks  map[string]int
	claims  map[string]int
}

// New returns a lock with the ref of epoch, created by machineID (the first
// ref, created with the repository of the lock).
func New(epoch uint64, machineID string) *Lock {
	l := &Lock{refs: map[uint64]string{}, down: map[string]bool{}, checks: map[string]int{}, claims: map[string]int{}}
	if epoch > 0 {
		l.refs[epoch] = machineID
	}
	return l
}

// For is the lock as the machine machineID reaches it.
func (l *Lock) For(machineID string) guild.Lock { return view{l: l, id: machineID} }

// SetDown makes the lock unreachable for one machine (a partition), or
// reachable again.
func (l *Lock) SetDown(machineID string, down bool) {
	l.mu.Lock()
	l.down[machineID] = down
	l.mu.Unlock()
}

// SetAllDown makes the lock unreachable for every machine.
func (l *Lock) SetAllDown(down bool) {
	l.mu.Lock()
	l.allDown = down
	l.mu.Unlock()
}

// Hold makes every Check wait until Release.
func (l *Lock) Hold() {
	l.mu.Lock()
	if l.held == nil {
		l.held = make(chan struct{})
	}
	l.mu.Unlock()
}

// Release lets the checks that wait go on.
func (l *Lock) Release() {
	l.mu.Lock()
	if l.held != nil {
		close(l.held)
		l.held = nil
	}
	l.mu.Unlock()
}

// Highest is the highest epoch of the lock.
func (l *Lock) Highest() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.highestLocked()
}

func (l *Lock) highestLocked() uint64 {
	var n uint64
	for e := range l.refs {
		n = max(n, e)
	}
	return n
}

// Owner is the machine that created the ref of epoch n ("" if none).
func (l *Lock) Owner(n uint64) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.refs[n]
}

// Set creates the ref of epoch n for machineID, as another machine would.
func (l *Lock) Set(n uint64, machineID string) {
	l.mu.Lock()
	l.refs[n] = machineID
	l.mu.Unlock()
}

// Checks and Claims count the calls of one machine.
func (l *Lock) Checks(machineID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.checks[machineID]
}

// Claims counts the claims of one machine.
func (l *Lock) Claims(machineID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.claims[machineID]
}

type view struct {
	l  *Lock
	id string
}

func (v view) reachable() error {
	if v.l.allDown || v.l.down[v.id] {
		return ErrDown
	}
	return nil
}

// Check implements guild.Lock.
func (v view) Check(ctx context.Context) (uint64, error) {
	v.l.mu.Lock()
	v.l.checks[v.id]++
	held := v.l.held
	v.l.mu.Unlock()
	if held != nil {
		select {
		case <-held:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	v.l.mu.Lock()
	defer v.l.mu.Unlock()
	if err := v.reachable(); err != nil {
		return 0, err
	}
	return v.l.highestLocked(), nil
}

// Claim implements guild.Lock: the ref is created only if it does not exist,
// and the machine wins only if the ref names it.
func (v view) Claim(_ context.Context, n uint64, machineID string) (bool, error) {
	v.l.mu.Lock()
	defer v.l.mu.Unlock()
	v.l.claims[v.id]++
	if err := v.reachable(); err != nil {
		return false, err
	}
	if _, ok := v.l.refs[n]; !ok {
		v.l.refs[n] = machineID
	}
	return v.l.refs[n] == machineID, nil
}
