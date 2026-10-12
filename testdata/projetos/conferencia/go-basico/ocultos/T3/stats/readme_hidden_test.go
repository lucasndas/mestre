package stats_test

import (
	"os"
	"strings"
	"testing"
)

// Hidden test of task T3 (testdata/projetos/conferencia/go-basico/tarefas.yaml).
func TestHiddenT3ReadmeListsMax(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "- `Max`") {
		t.Fatal("README.md does not list `Max` among the functions")
	}
}
