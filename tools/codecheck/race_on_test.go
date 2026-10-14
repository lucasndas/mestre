//go:build race

package main

// raceFactor scales the timing budget of TestCodeRules under the race detector,
// which slows the check down by several times; it is the factor testrun
// applies to the suite budgets with -race (tools/testrun RaceFactor).
const raceFactor = 3
