// Package waits holds the waits and the asks of the characters
// (docs/orquestracao/esperas-e-caixa-de-entrada.md). Waiting is the job of the
// engine, never of an agent looping: `wait` registers and returns at once, and
// when the wait is satisfied the engine puts a `wake` item with the result in the
// inbox of who waits. An `ask` is the one thing that blocks, for a short time; it
// is answered by the done of the turn that handled it, and an answer that comes
// late goes to the inbox as an `answer`.
//
// The Service keeps the waits for characters, for steps of a quest, for a time,
// for the decision of an approval and for a synchronization (the wait for a
// vacancy is kept by the characters), their time limits, and the graph of who
// waits for whom, in which it looks for a deadlock at every new arrow and warns
// of it once. Handlers gives the engine side of `ask`, `check`, `wait` and
// `waits`.
package waits
