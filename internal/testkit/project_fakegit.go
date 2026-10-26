//go:build !full

package testkit

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/testkit/fakegit"
)

// initRepo keeps the copy in the fake git: the fast suite starts no process
// (docs/qualidade/testes.md#regras-e-erros).
func initRepo(t testing.TB, p *Project, files map[string]string) {
	t.Helper()
	p.Git = fakegit.New()
	p.Head = string(p.Git.Commit(ProjectBranch, ProjectCommitMessage, files))
}
