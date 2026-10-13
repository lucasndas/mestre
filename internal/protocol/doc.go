// Package protocol is the protocol between the machines of a Guild
// (docs/conectividade/protocolo.md): the envelope with the epoch, the opening
// with the version and the capabilities, ack and idempotency, the control and
// data lanes with their priority, the heartbeat and the reconnection, the
// catalogue of message types (docs/conectividade/mensagens.md) and the
// transfer in parts.
//
// The package owns no socket. A connection is a Conn: framed messages with a
// context, a close code, a ping and the identity of the peer read from the
// mTLS certificate. internal/transport adapts WSS to it; tests adapt the
// in-memory network (internal/testkit/protorig). Time comes from pkg/clock.
//
// The two ends are symmetric. A Link is the durable relation with one peer:
// it outlives the connections, owns the outbox of the messages that wait for
// an ack and resends them, in order and with the same id, on every new
// connection. A Session is one connection. The Tower side is a Client (it
// dials, with backoff); the Citadel side is a Server (it accepts, one
// connection per machine).
package protocol
