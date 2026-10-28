//go:build !windows

package app

// lockRulesFromAgents does nothing here: the mode of the files of regras/ does it.
func lockRulesFromAgents(string) {}
