//go:build !linux

package launcher

import (
	"errors"
	"net"
	"os"
)

// Where the system has no SCM_RIGHTS (Windows), frames carry no descriptors:
// the secret travels in the request.
func readMsg(c net.Conn, b []byte) (int, []*os.File, error) {
	n, err := c.Read(b)
	return n, nil, err
}

func writeMsg(c net.Conn, b []byte, files []*os.File) error {
	if len(files) != 0 {
		return errors.New("descriptors cannot be passed on this system")
	}
	_, err := c.Write(b)
	return err
}
