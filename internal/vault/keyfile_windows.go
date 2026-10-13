package vault

import "context"

// FileKeyStore on Windows will keep the data key in
// %ProgramData%\Mestre\<inst>\config\cofre.key, encrypted with machine-scope
// DPAPI and with an ACL for NT SERVICE\Mestre only
// (docs/plataforma/cofre.md#chave-do-cofre). The protector belongs to
// internal/winsvc, which waits for the pending Windows spike; until then
// every call fails with ErrProtectorNotImplemented and the vault stays
// closed.
type FileKeyStore struct {
	path string
}

// NewFileKeyStore returns the key store at path.
func NewFileKeyStore(path string) *FileKeyStore { return &FileKeyStore{path: path} }

// Path is the key file.
func (s *FileKeyStore) Path() string { return s.path }

// Load fails with ErrProtectorNotImplemented.
func (s *FileKeyStore) Load(context.Context) ([]byte, error) { return nil, ErrProtectorNotImplemented }

// Save fails with ErrProtectorNotImplemented.
func (s *FileKeyStore) Save(context.Context, []byte) error { return ErrProtectorNotImplemented }

// syncDir is a no-op: Windows cannot open a directory for FlushFileBuffers
// without backup semantics, and MoveFileEx already replaces atomically.
func syncDir(string) error { return nil }
