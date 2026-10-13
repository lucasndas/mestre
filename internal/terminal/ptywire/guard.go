// Package ptywire is the protocol between the engine and a mestre-pty
// (docs/arquitetura/terminal.md#protocolo-motor--mestre-pty): JSON Lines for
// control messages, binary frames for terminal bytes, and the mutual HMAC
// handshake. It is shared by both sides and imports only the standard
// library, so the small mestre-pty binary does not link the engine.
package ptywire

import "errors"

// EnvTestSuite names the suite being run; set by tools/testrun.
const EnvTestSuite = "MESTRE_TEST_SUITE"

// ErrExternalProcessInFastSuite is returned when the fast suite asks for a
// real process (docs/qualidade/testes.md#regras-e-erros).
var ErrExternalProcessInFastSuite = errors.New("external process in fast suite: use the fake PTY")

// GuardSuite returns ErrExternalProcessInFastSuite when suite is "fast".
func GuardSuite(suite string) error {
	if suite == "fast" {
		return ErrExternalProcessInFastSuite
	}
	return nil
}
