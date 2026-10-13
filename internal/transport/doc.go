// Package transport is the only network layer between the machines of the
// Guild: the Guild CA and its certificates, TLS 1.3 with mTLS, and the WSS
// connections of port 7443 (docs/arquitetura/seguranca.md#tls-e-ca-interna,
// docs/conectividade/protocolo.md#endpoints-e-portas).
//
// # Contract for the protocol
//
// The protocol (internal/protocol: envelope, epoch, versions, sessions)
// sees only these interfaces, never the WebSocket library:
//
//	type Conn interface {
//		Read(ctx context.Context) (MessageType, []byte, error)
//		Write(ctx context.Context, typ MessageType, p []byte) error
//		Close(code StatusCode, reason string) error
//		CloseNow() error
//		Peer() Peer
//		Done() <-chan struct{}
//	}
//	type Dialer interface {
//		Dial(ctx context.Context, addr, route string) (Conn, error)
//	}
//	type Listener interface {
//		Accept(ctx context.Context) (Conn, error)
//		Close() error
//	}
//
// Rules of the contract:
//
//   - Messages are whole: MessageText carries the JSON control envelope and
//     MessageBinary the data frames. Every message is limited to
//     MaxMessageSize (1 MiB): Write refuses a larger one with ErrTooLarge
//     and Read discards a received one and returns ErrTooLarge, keeping the
//     connection, so the protocol can answer too_large.
//   - One reader at a time; writes may be concurrent and are never
//     interleaved. Canceling the context of a Read or Write in progress
//     closes the connection (the WebSocket cannot resume a half frame).
//   - Close codes: StatusCode, with the protocol's 4001 to 4007. After the
//     peer closes, Read returns a *CloseError (CloseStatus(err)).
//   - Peer is the identity taken from the verified certificate: Kind
//     (machine or guardian), MachineID and the certificate Serial. The
//     address never identifies anyone. Whether that machine is the Citadel
//     is the protocol's decision, by the epoch certificate.
//   - Keepalive: a WebSocket ping every 15 s; without pong in 45 s the
//     connection is dropped and Read returns ErrNoPong. The pong is read by
//     Read, so the protocol keeps a reader running. The application
//     heartbeat (machine.heartbeat) belongs to the protocol.
//   - Pipe gives two connected in-memory Conns for the protocol's tests.
//
// # Server and client
//
// Server serves RouteTower and RouteGuardian through Route (Listener),
// RouteCACert (the CA in DER, without authentication) and RouteHealth. A
// client certificate is optional in the handshake, so the public routes
// work, but a presented one must carry a mestre:// identity and not be
// revoked, or the handshake fails; the mTLS routes refuse a request without
// a certificate (401) or of the wrong kind (403) before the upgrade.
// CloseRevoked closes the open connections of revoked credentials with
// 4002. Client (Dialer) verifies the server by chain to the Guild CA, by its
// identity SAN and by the revocation hook, never by host name or IP.
//
// Time comes only from the injected clock.Clock, including the validity of
// certificates. Sockets come from a Network: SystemNetwork refuses the fast
// suite (MESTRE_TEST_SUITE=fast); fast tests use fakenet.Host, which runs
// the same real TLS in memory.
package transport
