// Package stats has small helpers over lists of numbers.
package stats

import "errors"

// ErrEmpty is returned when a function needs at least one number.
var ErrEmpty = errors.New("stats: empty list")

// Sum returns the sum of xs (0 for an empty list).
func Sum(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s
}

// Max returns the largest number of xs.
func Max(xs []float64) (float64, error) {
	if len(xs) == 0 {
		return 0, ErrEmpty
	}
	var m float64
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m, nil
}

// CountAbove returns how many numbers of xs are greater than limit.
func CountAbove(xs []float64, limit float64) int {
	n := 0
	for _, x := range xs {
		if x > limit {
			n++
		}
	}
	return n
}
