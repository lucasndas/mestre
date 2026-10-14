package hook

import (
	"errors"
	"os"
)

// Files is what the dispatcher touches on disk: the snapshots it reads and
// the files it creates in the hook queue. Tests give a double; OSFiles is
// the disk.
type Files interface {
	// ReadFile reads a snapshot.
	ReadFile(path string) ([]byte, error)
	// CreateNew creates path with data and fails if it exists: in the hook
	// queue the dispatcher only creates files.
	CreateNew(path string, data []byte) error
}

// OSFiles is Files on the disk.
type OSFiles struct{}

// ReadFile reads path.
func (OSFiles) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// CreateNew creates path, readable by the engine through the agents' group
// (0640), and writes data. Only creating is needed (on Windows the account
// of the agents can create files in the queue and nothing else); the engine
// leaves alone a file younger than QueueSettle, so it does not read one
// half written.
func (OSFiles) CreateNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	return errors.Join(werr, f.Close())
}
