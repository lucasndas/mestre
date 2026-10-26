// Package launcher is the mestre-launcher: the minimal service that starts
// each mestre-pty with the account of the agents, so that the engine never
// holds the power to switch accounts (docs/arquitetura/componentes.md,
// "Iniciador").
//
// This package has the parts common to both systems: the request and
// response protocol, the server that checks who asks and what is asked
// before any process is created, and the client the engine uses, which
// implements terminal.Spawner. What differs by system is behind two small
// interfaces: Backend (create and end processes) and PeerCheck (who is on
// the other end). The Linux ones are in this package (*_linux.go); the
// Windows ones are in internal/winsvc.
//
// The protocol has two requests only, start-pty and kill, one per
// connection: a frame (4-byte big-endian length, then JSON) and a frame as
// the answer. A request never names an account, an executable or arguments:
// the server decodes it strictly (an unknown field is a refusal) and takes
// those from its own configuration.
package launcher

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"

	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/terminal/ptywire"
)

// The requests.
const (
	// OpStartPTY starts the mestre-pty of a terminal.
	OpStartPTY = "start-pty"
	// OpKill ends the process tree of a terminal: with only the id, the
	// cgroup of the terminal (Linux: cgroup.kill); with a PID, its start
	// identity and the sessions, the mestre-pty and the sessions by identity,
	// for a terminal that has no cgroup (Linux without a delegated one).
	OpKill = "kill"
)

// Codes of a refused or failed Response.
const (
	// CodeRefused: the request is not one the launcher may do.
	CodeRefused = "refused"
	// CodeFailed: the request was allowed, and doing it failed.
	CodeFailed = "failed"
)

// Roles of the descriptors that go with a start-pty request (Linux), in
// order: the listening socket, the pipe with the secret, optionally the
// file the stderr of the mestre-pty goes to.
const (
	RoleListen = "listen"
	RoleSecret = "secret"
	RoleLog    = "log"
)

// Request is one request of the engine to the launcher.
type Request struct {
	Op string `json:"op"`
	// ID is the terminal.
	ID string `json:"id"`
	// Version is the mestre-pty version, from the installed binaries.
	Version string `json:"version,omitempty"`
	// Socket is where the terminal listens; the launcher only accepts the
	// one it computes itself (its socket folder + <id>.sock).
	Socket string `json:"socket,omitempty"`
	// Secret is the 32-byte secret of the terminal, when descriptors cannot
	// be passed (Windows: the pipe is private to the engine and the
	// launcher).
	Secret []byte `json:"secret,omitempty"`
	// Files names the descriptors passed along (Linux, SCM_RIGHTS).
	Files []string `json:"files,omitempty"`
	// PID and Start (kill only) are the mestre-pty to end, by PID and start
	// identity: nothing is ended on a PID alone. Sessions are the session IDs
	// of its agent, whose members that started after it are ended too.
	PID      int    `json:"pid,omitempty"`
	Start    string `json:"start,omitempty"`
	Sessions []int  `json:"sessions,omitempty"`
}

// maxSessions bounds the sessions of one kill request.
const maxSessions = 16

// Response is the answer.
type Response struct {
	OK    bool   `json:"ok"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
	// PID and Start identify the mestre-pty that started (see
	// terminal.ProcID).
	PID   int    `json:"pid,omitempty"`
	Start string `json:"start,omitempty"`
	// CgroupDir is the cgroup of the terminal (Linux with a delegated one).
	CgroupDir string `json:"cgroup,omitempty"`
	// Logon is the logon SID of the terminal (Windows): what the engine
	// grants on the socket of the local channel while the terminal lives.
	Logon string `json:"logon,omitempty"`
}

// ErrRefused is wrapped by the error of a request the launcher refused.
var ErrRefused = errors.New("refused by the mestre-launcher")

// Error is a failed Response as an error.
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return "mestre-launcher: " + e.Code + ": " + e.Msg }

// Is makes errors.Is(err, ErrRefused) true for a refusal.
func (e *Error) Is(target error) bool { return target == ErrRefused && e.Code == CodeRefused }

// refusedError is what a Backend returns for a request it will not do.
type refusedError struct{ msg string }

func (e refusedError) Error() string { return e.msg }

// Refused makes an error the server answers (and records) as a refusal.
func Refused(format string, args ...any) error { return refusedError{fmt.Sprintf(format, args...)} }

// maxFrame bounds a frame: the requests are tiny.
const maxFrame = 64 << 10

// maxFiles bounds the descriptors of one frame.
const maxFiles = 4

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$`)

// ValidateVersion refuses a version that could name anything but a folder of
// the installed binaries.
func ValidateVersion(v string) error {
	if len(v) > 64 || !versionPattern.MatchString(v) {
		return fmt.Errorf("mestre-pty version %q: use MAJOR.MINOR.PATCH", v)
	}
	return nil
}

// PTYBinary is the mestre-pty of version in the folder of installed
// binaries (<binDir>/<version>/mestre-pty, with .exe on Windows). It must be
// a regular file, not a link.
func PTYBinary(binDir, version string) (string, error) {
	if err := ValidateVersion(version); err != nil {
		return "", err
	}
	name := "mestre-pty"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(binDir, version, name)
	fi, err := os.Lstat(p)
	if err != nil {
		return "", fmt.Errorf("mestre-pty %s is not installed", version)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("mestre-pty %s is not a regular file", version)
	}
	return p, nil
}

// Conn is a connection that carries frames, and descriptors with them where
// the system allows. The fakes of the tests implement it too.
type Conn interface {
	// ReadFrame returns the next frame and the descriptors that came with it.
	ReadFrame() ([]byte, []*os.File, error)
	// WriteFrame sends a frame with the descriptors files (Linux).
	WriteFrame(b []byte, files []*os.File) error
	// Raw is the connection underneath, for who-is-it checks.
	Raw() net.Conn
	Close() error
}

// NewConn frames a stream connection.
func NewConn(c net.Conn) Conn { return &streamConn{c: c} }

type streamConn struct{ c net.Conn }

func (s *streamConn) Raw() net.Conn { return s.c }
func (s *streamConn) Close() error  { return s.c.Close() }

func (s *streamConn) ReadFrame() ([]byte, []*os.File, error) {
	var files []*os.File
	fail := func(err error) ([]byte, []*os.File, error) {
		closeAll(files)
		return nil, nil, err
	}
	var hdr [4]byte
	n, err := readFull(s.c, hdr[:], &files)
	if err != nil {
		if n == 0 && errors.Is(err, io.EOF) {
			return fail(io.EOF)
		}
		return fail(fmt.Errorf("read frame header: %w", err))
	}
	size := binary.BigEndian.Uint32(hdr[:])
	if size == 0 || size > maxFrame {
		return fail(fmt.Errorf("frame of %d bytes: the limit is %d", size, maxFrame))
	}
	body := make([]byte, size)
	if _, err := readFull(s.c, body, &files); err != nil {
		return fail(fmt.Errorf("read frame: %w", err))
	}
	if len(files) > maxFiles {
		return fail(fmt.Errorf("%d descriptors in a frame: the limit is %d", len(files), maxFiles))
	}
	return body, files, nil
}

// readFull fills b, collecting the descriptors that arrive meanwhile.
func readFull(c net.Conn, b []byte, files *[]*os.File) (int, error) {
	n := 0
	for n < len(b) {
		m, fs, err := readMsg(c, b[n:])
		*files = append(*files, fs...)
		n += m
		if err != nil {
			if n == len(b) && errors.Is(err, io.EOF) {
				return n, nil
			}
			if errors.Is(err, io.EOF) && n > 0 {
				err = io.ErrUnexpectedEOF
			}
			return n, err
		}
	}
	return n, nil
}

func (s *streamConn) WriteFrame(b []byte, files []*os.File) error {
	if len(b) == 0 || len(b) > maxFrame {
		return fmt.Errorf("frame of %d bytes: the limit is %d", len(b), maxFrame)
	}
	buf := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(buf, uint32(len(b)))
	copy(buf[4:], b)
	return writeMsg(s.c, buf, files)
}

func closeAll(files []*os.File) {
	for _, f := range files {
		_ = f.Close()
	}
}

// decodeRequest decodes strictly: an unknown field (an account, a path,
// arguments) is a refusal, and so is anything after the object.
func decodeRequest(b []byte) (Request, error) {
	var r Request
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Request{}, fmt.Errorf("malformed request: %w", err)
	}
	if dec.More() {
		return Request{}, errors.New("malformed request: data after the object")
	}
	return r, nil
}

// validateRequest checks what does not depend on the system.
func validateRequest(r Request) error {
	if r.Op != OpStartPTY && r.Op != OpKill {
		return fmt.Errorf("request %q is not one the launcher does", r.Op)
	}
	if err := terminal.ValidateID(r.ID); err != nil {
		return err
	}
	if r.Op == OpKill {
		if r.Version != "" || r.Socket != "" || len(r.Secret) != 0 || len(r.Files) != 0 {
			return errors.New("kill takes only the terminal id, or the mestre-pty and its sessions")
		}
		return validateKillTarget(r)
	}
	if r.PID != 0 || r.Start != "" || len(r.Sessions) != 0 {
		return errors.New("start-pty takes no process to end")
	}
	if len(r.Secret) != 0 && len(r.Secret) != ptywire.SecretSize {
		return fmt.Errorf("secret of %d bytes: want %d", len(r.Secret), ptywire.SecretSize)
	}
	return ValidateVersion(r.Version)
}

// validateKillTarget checks the identity a kill by process carries: a PID
// above 1 with its start identity, and a few session IDs above 1.
func validateKillTarget(r Request) error {
	if r.PID == 0 {
		if r.Start != "" || len(r.Sessions) != 0 {
			return errors.New("kill: sessions and a start identity need the PID of the mestre-pty")
		}
		return nil
	}
	if r.PID <= 1 {
		return fmt.Errorf("kill: PID %d", r.PID)
	}
	if r.Start == "" || len(r.Start) > 128 {
		return errors.New("kill: a PID needs its start identity")
	}
	if len(r.Sessions) > maxSessions {
		return fmt.Errorf("kill: %d sessions, the limit is %d", len(r.Sessions), maxSessions)
	}
	for _, s := range r.Sessions {
		if s <= 1 {
			return fmt.Errorf("kill: session %d", s)
		}
	}
	return nil
}
