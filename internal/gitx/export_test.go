package gitx

import "fmt"

// EnvironFrom exposes the environment builder to the tests.
func EnvironFrom(g *Real, base []string) []string { return g.environFrom(base) }

// MaskedError exposes the error builder to the tests.
func MaskedError(g *Real, args []string, code int, stderr string) *Error {
	return g.newError(args, code, fmt.Errorf("exit status %d", code), stderr)
}
