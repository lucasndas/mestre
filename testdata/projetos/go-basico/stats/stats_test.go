package stats_test

import (
	"errors"
	"testing"

	"example.com/gobasico/stats"
)

func TestSum(t *testing.T) {
	t.Parallel()
	if got := stats.Sum([]float64{1, 2, 3.5}); got != 6.5 {
		t.Fatalf("Sum = %v, want 6.5", got)
	}
	if got := stats.Sum(nil); got != 0 {
		t.Fatalf("Sum(nil) = %v, want 0", got)
	}
}

func TestMax(t *testing.T) {
	t.Parallel()
	if got, err := stats.Max([]float64{3, 7, 5}); err != nil || got != 7 {
		t.Fatalf("Max = %v, %v; want 7", got, err)
	}
	if _, err := stats.Max(nil); !errors.Is(err, stats.ErrEmpty) {
		t.Fatalf("Max(nil) error = %v, want ErrEmpty", err)
	}
}

func TestCountAbove(t *testing.T) {
	t.Parallel()
	if got := stats.CountAbove([]float64{1, 5, 10, 5}, 4); got != 3 {
		t.Fatalf("CountAbove = %d, want 3", got)
	}
}
