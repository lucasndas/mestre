package telegram

import (
	"context"
	"strings"
	"sync"

	"github.com/mestre-ia/mestre/internal/users"
)

// PINRouter takes the answers of the PIN to whoever asked for them: a request of
// PIN has an action ("approval:7", "unlock"), and what is done when the PIN is
// right (or fails for good) is of the owner of the action. The bot has one
// capture of the PIN; this is what it calls.
type PINRouter struct {
	Users *users.Service

	mu     sync.Mutex
	routes map[string]func(ctx context.Context, user users.User, requestID string, err error)
}

// On registers who handles the requests whose action starts with prefix.
func (r *PINRouter) On(prefix string, f func(ctx context.Context, user users.User, requestID string, err error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.routes == nil {
		r.routes = map[string]func(context.Context, users.User, string, error){}
	}
	r.routes[prefix] = f
}

// Capture is the CaptureFunc of PINKind.
func (r *PINRouter) Capture() CaptureFunc { return PINCapture(r.Users, r.done) }

func (r *PINRouter) done(ctx context.Context, u users.User, requestID string, err error) {
	action, aerr := r.Users.PINAction(ctx, requestID)
	if aerr != nil {
		return
	}
	r.mu.Lock()
	var f func(context.Context, users.User, string, error)
	for prefix, h := range r.routes {
		if strings.HasPrefix(action, prefix) {
			f = h
		}
	}
	r.mu.Unlock()
	if f != nil {
		f(ctx, u, requestID, err)
	}
}
