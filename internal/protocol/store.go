package protocol

import (
	"context"
	"time"
)

// Outbox is the durable queue of the messages that wait for an ack
// (docs/arquitetura/persistencia.md, table outbox). It is kept per peer; the
// messages come back in the order they were put in.
type Outbox interface {
	// Put stores the message durably before it is sent. Putting an id that
	// is already there is not an error and keeps its place in the line.
	Put(ctx context.Context, peer string, env Envelope) error
	// Done removes the message once its ack (or its final refusal) arrived.
	Done(ctx context.Context, peer, id string) error
	// Pending returns what still waits for an ack, oldest first.
	Pending(ctx context.Context, peer string) ([]Envelope, error)
}

// Processed is the memory of the receiver (table processed_commands): the
// ids it has already taken from each sender, with the answer given, kept for
// 24 hours by default so a message sent again never runs twice.
type Processed interface {
	// Claim records, durably and before the handler runs, that the message
	// of the sender is being taken. fresh is false when the id was already
	// claimed; prev then holds the ack given the first time (nil while the
	// first run has not finished: a crash in the middle counts as taken,
	// because running twice is worse than not running).
	Claim(ctx context.Context, sender, id, typ string, now time.Time) (fresh bool, prev []byte, err error)
	// Finish stores the ack that was given.
	Finish(ctx context.Context, sender, id string, ack []byte) error
	// Release forgets a claim, so a message the handler refused with a
	// retryable error can run when it is sent again.
	Release(ctx context.Context, sender, id string) error
	// Prune forgets what was claimed before cutoff and returns how many.
	Prune(ctx context.Context, cutoff time.Time) (int, error)
}

// DefaultProcessedTTL is how long the ids of the processed commands are kept.
const DefaultProcessedTTL = 24 * time.Hour
