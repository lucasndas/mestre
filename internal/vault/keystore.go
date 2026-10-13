package vault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// KeyStore keeps the vault data key protected by the operating system
// (docs/plataforma/cofre.md#chave-do-cofre): on Linux a 0600 file owned by
// the service account (FileKeyStore); on Windows a DPAPI machine-scope blob
// with an ACL for NT SERVICE\Mestre only (internal/winsvc, not yet
// implemented). Save must replace the stored bytes atomically: a crash
// leaves either the old or the new content.
type KeyStore interface {
	Load(ctx context.Context) ([]byte, error)
	Save(ctx context.Context, data []byte) error
}

// ErrNoKey is returned by KeyStore.Load when no key was ever saved.
var ErrNoKey = errors.New("vault: no key stored")

// ErrProtectorNotImplemented is returned by the Windows key store until the
// DPAPI protector exists in internal/winsvc.
var ErrProtectorNotImplemented = errors.New("vault: DPAPI key protection is not implemented yet (internal/winsvc)")

// UnsafeKeyFileError is returned when the key file is readable by more than
// the service account. The vault refuses to open; the checklist item is
// Check.
type UnsafeKeyFileError struct {
	Path   string
	Reason string
	Check  string // checklist item, e.g. LIN-05
}

func (e *UnsafeKeyFileError) Error() string {
	return fmt.Sprintf("vault: refusing key file %s: %s", e.Path, e.Reason)
}

// Severity of an Alert.
type Severity string

// SeverityGrave is the checklist's "grave" level.
const SeverityGrave Severity = "grave"

// Alert is raised when the vault cannot open. Err never carries a secret.
type Alert struct {
	Severity Severity
	Check    string // checklist item when known (LIN-05), else empty
	Err      error
}

// writeFileAtomic replaces path with data: a 0600 temporary file in the same
// directory, synced, then renamed over path, then the directory synced.
func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err = f.Chmod(0o600); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncDir(dir)
}
