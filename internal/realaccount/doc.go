// Package realaccount holds the tests that need real operating system
// accounts: the mestre-launcher switching to another user (CAP_SETUID and
// CAP_SETGID on Linux, the batch LogonUser of the agent account on Windows),
// the isolation of the agents' account from the engine's, and the vault key
// under a real virtual service account.
//
// The tests are a subset of the heavy suite (build tag realaccount, files
// *_realaccount_test.go), run by tools/realaccount on a disposable machine
// only (docs/qualidade/testes.md#contas-reais): that tool creates the
// accounts, installs the units or services, runs this package's test binary
// as the account of the engine, and removes everything. Without the tool, the
// test binary refuses to run.
package realaccount
