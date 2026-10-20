//go:build full

package app

// RuntimeBuildsDir is the folder of the binaries the full tests of this package
// built (empty when none needed them), for the TestMain of app_test: its
// processes are the ones a test must not leave alive.
func RuntimeBuildsDir() string { return runtimeBuilds.dir }
