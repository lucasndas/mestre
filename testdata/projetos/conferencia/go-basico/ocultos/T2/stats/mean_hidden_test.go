package stats_test

import (
	"errors"
	"testing"

	"example.com/gobasico/stats"
)

// Hidden test of task T2 (testdata/projetos/conferencia/go-basico/tarefas.yaml).
func TestHiddenT2Mean(t *testing.T) {
	t.Parallel()
	if got, err := stats.Mean([]float64{1, 2, 3, 4}); err != nil || got != 2.5 {
		t.Fatalf("Mean(1, 2, 3, 4) = %v, %v; want 2.5", got, err)
	}
	if _, err := stats.Mean(nil); !errors.Is(err, stats.ErrEmpty) {
		t.Fatalf("Mean(nil) error = %v, want ErrEmpty", err)
	}
}
