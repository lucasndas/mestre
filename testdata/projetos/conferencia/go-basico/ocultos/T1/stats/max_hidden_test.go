package stats_test

import (
	"testing"

	"example.com/gobasico/stats"
)

// Hidden test of task T1 (testdata/projetos/conferencia/go-basico/tarefas.yaml).
func TestHiddenT1MaxAllNegative(t *testing.T) {
	t.Parallel()
	got, err := stats.Max([]float64{-3, -1, -2})
	if err != nil || got != -1 {
		t.Fatalf("Max(-3, -1, -2) = %v, %v; want -1", got, err)
	}
}
