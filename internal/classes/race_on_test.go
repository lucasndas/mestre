//go:build race

package classes_test

// raceFactor scales the time bounds of the analysis under the race
// detector, which slows the parser down by several times.
const raceFactor = 10
