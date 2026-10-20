package app

import (
	"context"
	"sync"
)

// towerResumes are the Towers whose characters the Citadel must resume: the ones
// that came back (their resync, character.resync, left some interrupted). The
// resumption runs in a loop of the orchestration, so it only happens while the
// Citadel acts (the same rule as the resumption of this machine's characters),
// and never inside the handler of the Tower's message: the start of a character
// asks the Tower, over the same connection, and the Tower is waiting for the
// answer of the resync.
type towerResumes struct {
	mu      sync.Mutex
	pending map[string]bool
	kick    chan struct{}
}

func (r *towerResumes) init() {
	if r.kick == nil {
		r.kick = make(chan struct{}, 1)
		r.pending = map[string]bool{}
	}
}

// request marks a machine to be resumed and wakes the loop.
func (r *towerResumes) request(machine string) {
	r.mu.Lock()
	r.init()
	r.pending[machine] = true
	kick := r.kick
	r.mu.Unlock()
	select {
	case kick <- struct{}{}:
	default:
	}
}

// take removes and returns the machines waiting to be resumed.
func (r *towerResumes) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	out := make([]string, 0, len(r.pending))
	for m := range r.pending {
		out = append(out, m)
	}
	clear(r.pending)
	return out
}

// wake is the channel the loop waits on.
func (r *towerResumes) wake() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	return r.kick
}

// resumeTowers is the loop of the resumption of the Towers that came back: for each
// one, in the order of the document (the Grand Master first), the characters its resync
// left interrupted start again with their saved session (Service.ResumeMachine).
func (e *Engine) resumeTowers(ctx context.Context, orc *Orchestration) error {
	wake := e.resumes.wake()
	for {
		for _, machine := range e.resumes.take() {
			if err := orc.Characters.ResumeMachine(ctx, machine); err != nil && ctx.Err() == nil {
				e.log.Warn("app: some characters of a Tower that came back were not resumed", "machine", machine, "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		}
	}
}
