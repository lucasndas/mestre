//go:build ptylegacy

package main

// The tests build a "previous version" of the mestre-pty with this tag (and
// -ldflags "-X main.version=0.9.0"): it announces protocol 1, so it lacks
// the "ping" message and the history of "screen" that protocol 2 added
// (CA-TERM-10). Release builds never carry the tag.
func init() { protoVersion = 1 }
