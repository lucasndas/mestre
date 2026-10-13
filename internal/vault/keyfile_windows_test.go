package vault

import (
	"errors"
	"path/filepath"
	"testing"
)

// Until internal/winsvc brings DPAPI, the Windows key store must keep the
// vault closed with a grave alert rather than store the key unprotected.
func TestWindowsKeyStoreIsNotImplementedYet(t *testing.T) {
	t.Parallel()
	f := newFixture(t, NewFileKeyStore(filepath.Join(t.TempDir(), "cofre.key")))
	if _, err := Create(f.ctx, f.cfg); !errors.Is(err, ErrProtectorNotImplemented) {
		t.Fatalf("Create = %v, want ErrProtectorNotImplemented", err)
	}
	assertClosed(t, f, f.open(t), ErrProtectorNotImplemented)
}
