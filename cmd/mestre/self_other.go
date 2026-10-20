//go:build !windows

package main

// allowEngine does nothing outside Windows: the engine identifies the caller
// by the cgroup or the pidfd, which need no right over the process.
func allowEngine() {}
