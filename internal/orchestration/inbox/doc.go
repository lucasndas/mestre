// Package inbox is the inbox of the characters
// (docs/orquestracao/esperas-e-caixa-de-entrada.md): one queue per character in
// the Guild database, the delivery of an item only when its character is free
// (idle or waiting) and its table is not paused, one item per turn, in the order
// of the Owner first, the other users, then the rest; the answer to a user
// through the channel the message came from, kept while the channel is away;
// and the notices of the engine, which become `system` items.
//
// The Service delivers through the Deliverer (inbox.deliver of the protocol, or
// characters.MachineSet in process), is told of the changes of state of the
// characters by characters.Observer, and records the outcome of a delivery
// through providers.Inbox (Confirmed, Unconfirmed). An item that is typed and
// whose turn never began is never typed again; one that was being delivered
// when the engine fell goes back to the queue (Recover).
package inbox
