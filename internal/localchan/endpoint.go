package localchan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Environment variables of a character (docs/cli/convencoes.md#identificação-do-chamador).
const (
	EnvAgentID  = "MESTRE_AGENT_ID"
	EnvEndpoint = "MESTRE_ENDPOINT"
	EnvCLI      = "MESTRE_CLI"
)

// DefaultInstance is the instance the CLI talks to when MESTRE_ENDPOINT is
// not set (docs/arquitetura/componentes.md#instâncias).
const DefaultInstance = "principal"

// instancePattern is the name of an instance other than the default: lower
// case, up to 6 characters. The account of its agents is mestre-agente-<inst>
// (14 characters of prefix), and a local account of Windows has at most 20
// characters and ignores the case, so a longer name would not fit and X and x
// would be the same account (docs/arquitetura/componentes.md#instâncias).
// guild.ValidInstance and winsvc.AgentAccountName follow the same rule.
var instancePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,5}$`)

// AgentAccount is the account the agents of an instance run as, and, on Linux,
// the group of its socket: mestre-agente for the principal instance and
// mestre-agente-<inst> for the others (docs/arquitetura/componentes.md#instâncias).
func AgentAccount(instance string) string {
	if instance == "" || instance == DefaultInstance {
		return "mestre-agente"
	}
	return "mestre-agente-" + instance
}

// ValidInstance reports whether name can name an instance.
func ValidInstance(name string) bool {
	return name == DefaultInstance || instancePattern.MatchString(name)
}

// SocketPath is the path of the socket of the local channel of an instance
// on this system: /run/mestre/<inst>/cli.sock on Linux and
// %ProgramData%\Mestre\<inst>\run\cli.sock on Windows. See socketPath in
// endpoint_linux.go and endpoint_windows.go.
func SocketPath(instance string) (string, error) {
	if !ValidInstance(instance) {
		return "", fmt.Errorf("instance name %q: use up to 6 lower case letters, digits, '-' and '_' (or %q)", instance, DefaultInstance)
	}
	return socketPath(instance), nil
}

// EndpointURL is the MESTRE_ENDPOINT of a socket: unix:///run/mestre/<inst>/cli.sock
// or unix://C:/ProgramData/Mestre/<inst>/run/cli.sock.
func EndpointURL(socket string) string { return "unix://" + filepath.ToSlash(socket) }

var drivePath = regexp.MustCompile(`^[A-Za-z]:/`)

// ParseEndpoint returns the socket path of a MESTRE_ENDPOINT value, in the
// form of this system's paths.
func ParseEndpoint(endpoint string) (string, error) {
	rest, ok := strings.CutPrefix(endpoint, "unix://")
	if !ok {
		return "", fmt.Errorf("endpoint %q: only unix:// is supported", endpoint)
	}
	if !strings.HasPrefix(rest, "/") && !drivePath.MatchString(rest) {
		return "", fmt.Errorf("endpoint %q: expected unix:///path or unix://C:/path", endpoint)
	}
	return filepath.FromSlash(rest), nil
}

// EndpointFromEnv is the socket the CLI talks to: MESTRE_ENDPOINT, or the
// one of the principal instance.
func EndpointFromEnv(getenv func(string) string) (string, error) {
	if v := getenv(EnvEndpoint); v != "" {
		return ParseEndpoint(v)
	}
	return SocketPath(DefaultInstance)
}

// InstanceOfSocket is the instance a Windows socket path belongs to:
// <...>\Mestre\<inst>\run\cli.sock. A path of another shape says nothing.
// The separators of both systems count, so the same text reads the same
// everywhere.
func InstanceOfSocket(sock string) (string, bool) {
	p := strings.FieldsFunc(sock, func(r rune) bool { return r == '/' || r == '\\' })
	n := len(p)
	if n < 4 || p[n-1] != "cli.sock" || p[n-2] != "run" || !strings.EqualFold(p[n-4], "Mestre") {
		return "", false
	}
	// Windows paths ignore the case: the folder of instance x may be spelled X.
	inst := strings.ToLower(p[n-3])
	if !ValidInstance(inst) {
		return "", false
	}
	return inst, true
}

// Access sets who may connect to the socket of the engine. The engine
// creates the socket and then applies it: on Linux the mode and owner
// (LinuxAccess), on Windows the ACL (winsvc.CLIAccess).
type Access interface {
	Apply(path string) error
}

// NoAccess leaves the socket as the system created it: the tests.
type NoAccess struct{}

// Apply does nothing.
func (NoAccess) Apply(string) error { return nil }

// Listen creates the socket at path, applies access to it, and listens. A
// socket left by an engine that died is replaced; one that answers belongs
// to a running engine, and Listen fails.
func Listen(path string, access Access) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("folder of the local channel: %w", err)
	}
	// A socket that answers belongs to a running engine; one that does not
	// is the file an engine that died left behind.
	if c, derr := net.Dial("unix", path); derr == nil {
		_ = c.Close()
		return nil, fmt.Errorf("the local channel %s is served by another engine", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove the stale socket %s: %w", path, err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	if access != nil {
		if err := access.Apply(path); err != nil {
			_ = ln.Close()
			return nil, fmt.Errorf("access to %s: %w", path, err)
		}
	}
	return ln, nil
}

// UnixDialer connects to the socket at path.
func UnixDialer(path string) Dialer {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}
}

// Dialer opens a connection to the engine.
type Dialer func(ctx context.Context) (net.Conn, error)
