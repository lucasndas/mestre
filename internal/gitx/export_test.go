package gitx

import "fmt"

// EnvironFrom exposes the environment builder to the tests.
func EnvironFrom(g *Real, base []string) []string { return g.environFrom(base) }

// MaskedError exposes the error builder to the tests.
func MaskedError(g *Real, args []string, code int, stderr string) *Error {
	return g.newError(args, code, fmt.Errorf("exit status %d", code), stderr)
}

// AuthEnv exposes the environment of an Auth to the tests.
func AuthEnv(a Auth) []string { return a.env() }

// GlobalArgs exposes the -c settings of every call to the tests.
var GlobalArgs = globalArgs

// LockKey exposes the canonical lock key to the tests.
var LockKey = lockKey

// ClassifyPush exposes the reading of a refusal of the remote to the tests.
func ClassifyPush(stderr string) error {
	return classifyPush(&Error{Stderr: stderr, ExitCode: 1, Err: fmt.Errorf("exit status 1")})
}
