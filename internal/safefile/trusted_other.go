//go:build !linux

package safefile

// ReadTrusted: the Mestre runs on Linux and Windows only. On Windows the ACL of the folder
// decides and it is Read; elsewhere nothing is supported.
func ReadTrusted(path string, limit int64) ([]byte, Info, error) {
	f, info, err := Open(path)
	if err != nil {
		return nil, Info{}, err
	}
	defer f.Close()
	return readOpen(path, f, info, limit)
}
