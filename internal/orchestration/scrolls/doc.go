// Package scrolls keeps the scrolls and tomes of the tables
// (docs/orquestracao/pergaminhos-e-tomos.md): Markdown notes that users and
// connected agents read and write. Each scroll is a file in the main clone of
// the workspace, `.mestre/mesas/<table>/pergaminhos/<id>.md`, and the database
// keeps only the index (name, revision, size, author, tome).
//
// The Service applies the operations (create, read, write, edit, stack, delete)
// one at a time, in the order they arrive, each change adding one to the
// revision; an agent only operates the scrolls the connections let it see; the
// engine appends sections to the scroll of a quest (AppendSection) and writes
// the summaries of the characters that end (Record). The copies in the
// worktrees of the machines that have characters are kept by Copies. The
// Service is the connections.Scrolls of the Guild.
package scrolls
