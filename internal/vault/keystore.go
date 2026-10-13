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
// the service account; on Windows a DPAPI machine-scope blob with an ACL for
// NT SERVICE\Mestre only (internal/winsvc/dpapi). Both are FileKeyStore.
// Save must replace the stored bytes atomically: a crash leaves either the
// old or the new content.
type KeyStore interface {
	Load(ctx context.Context) ([]byte, error)
	Save(ctx context.Context, data []byte) error
}

// ErrNoKey is returned by KeyStore.Load when no key was ever saved.
var ErrNoKey = errors.New("vault: no key stored")

// UnsafeKeyFileError is returned when the key file is readable by more than
// the service account. The vault refuses to open; the checklist item is
// Check.
type UnsafeKeyFileError struct {
	Path   string
	Reason string
	Check  string // checklist item: LIN-05 on Linux, WIN-14 on Windows
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
	Check    string // checklist item when known (LIN-05, WIN-14), else empty
	Err      error
}

// writeFileAtomic replaces path with data: a 0600 temporary file in the same
// directory, synced, then moved over path and the move made durable (the
// directory synced on Linux; MoveFileEx with MOVEFILE_WRITE_THROUGH on
// Windows, where a directory cannot be synced).
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
	if err = replaceFile(tmp, path); err != nil {
		return err
	}
	return syncDir(dir)
}
