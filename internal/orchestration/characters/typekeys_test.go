package characters_test

import (
	"errors"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/providers"
)

// The keys of a person at the panel go to a busy character (the panel asked for the
// confirmation), but never to one that waits on a native permission prompt: that is
// answered by the approval flow, as for the raw input.
func TestKeysOfAPersonReachABusyCharacterButNotAPermissionPrompt(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	rt := r.mach["linux"].rt
	dev := r.make("Dev Claude", "Dev", "3-login")
	r.ready(dev)
	r.busy(dev)
	if err := rt.Type(r.ctx, dev.ID, []byte("ls")); err != nil {
		t.Fatalf("busy: %v", err)
	}
	if typed := r.mach["linux"].host.proc(dev.ID).typedAll(); len(typed) != 1 || typed[0] != "ls" {
		t.Fatalf("typed = %q", typed)
	}
	r.hook(dev, providers.EventPermissionRequest)
	if err := rt.Type(r.ctx, dev.ID, []byte("1")); !errors.Is(err, characters.ErrPermissionPrompt) {
		t.Fatalf("permission prompt: %v", err)
	}
	if typed := r.mach["linux"].host.proc(dev.ID).typedAll(); len(typed) != 1 {
		t.Fatalf("keys reached a permission prompt: %q", typed)
	}
}
