package launcher

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// oobSize has room for maxFiles descriptors.
var oobSize = unix.CmsgSpace(maxFiles * 4)

// readMsg reads from c, taking the descriptors (SCM_RIGHTS) that come with
// the bytes. The descriptors are close-on-exec (the Go runtime asks for
// MSG_CMSG_CLOEXEC), so they only reach a child through ExtraFiles.
func readMsg(c net.Conn, b []byte) (int, []*os.File, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		n, err := c.Read(b)
		return n, nil, err
	}
	oob := make([]byte, oobSize)
	n, oobn, flags, _, err := uc.ReadMsgUnix(b, oob)
	var files []*os.File
	if oobn > 0 {
		fs, perr := parseRights(oob[:oobn])
		files = fs
		if perr != nil && err == nil {
			err = perr
		}
	}
	if flags&unix.MSG_CTRUNC != 0 && err == nil {
		err = fmt.Errorf("more than %d descriptors", maxFiles)
	}
	return n, files, err
}

func parseRights(oob []byte) ([]*os.File, error) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, fmt.Errorf("control message: %w", err)
	}
	var files []*os.File
	for i := range msgs {
		fds, err := unix.ParseUnixRights(&msgs[i])
		if err != nil {
			continue // not SCM_RIGHTS
		}
		for _, fd := range fds {
			files = append(files, os.NewFile(uintptr(fd), "received"))
		}
	}
	return files, nil
}

// writeMsg writes all of b, the descriptors going with its first bytes.
func writeMsg(c net.Conn, b []byte, files []*os.File) error {
	if len(files) == 0 {
		_, err := c.Write(b)
		return err
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("descriptors cannot go over %T", c)
	}
	fds := make([]int, len(files))
	for i, f := range files {
		fds[i] = int(f.Fd())
	}
	n, _, err := uc.WriteMsgUnix(b, unix.UnixRights(fds...), nil)
	if err != nil {
		return err
	}
	if n < len(b) {
		_, err = c.Write(b[n:])
	}
	return err
}
