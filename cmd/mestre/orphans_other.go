//go:build !windows && !linux

package main

// orphanSweep is nothing where the system has no subreaper: only the process
// group kill ends the tree of a hook.
type orphanSweep struct{}

func newOrphanSweep() *orphanSweep { return &orphanSweep{} }

func (*orphanSweep) kill() {}
