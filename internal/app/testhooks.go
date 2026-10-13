//go:build !full

package app

import "context"

// testHooks is empty in the binaries of release: the hooks of the full suite
// (testhooks_full.go) exist only with the tag full.
func testHooks(context.Context, *Deps, Config, func(string) string) (after func(*Engine) error, cleanup func(), err error) {
	return func(*Engine) error { return nil }, func() {}, nil
}
