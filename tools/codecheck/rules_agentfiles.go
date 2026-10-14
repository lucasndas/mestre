package main

import (
	"go/ast"
	"strings"
)

// agentFilePkgs are the packages whose files the engine keeps in folders the
// account of the agents writes (worktrees, account profiles), with the os
// calls that open, write or change a file by its path there. Those go through
// internal/safefile, which refuses a link or a junction on the path and never
// changes a mode by path.
var agentFilePkgs = []struct {
	prefix string
	calls  []string
}{
	{"internal/providers/", []string{"ReadFile", "WriteFile", "OpenFile", "Open", "Create", "CreateTemp", "Rename", "Chmod"}},
	{"internal/classes/", []string{"Chmod"}},
}

// checkAgentFiles (agent-files) forbids those calls in non-test code of the
// packages above.
func checkAgentFiles(s *source, add func(Issue)) {
	var calls []string
	for _, p := range agentFilePkgs {
		if strings.HasPrefix(s.path, p.prefix) {
			calls = p.calls
		}
	}
	osName := importName(s.file, "os")
	if len(calls) == 0 || osName == "" {
		return
	}
	ast.Inspect(s.file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		for _, name := range calls {
			if selector(call.Fun, osName, name) {
				add(Issue{s.path, s.line(call.Pos()), ruleAgentFiles, "os." + name + " on a file the agents' account can reach: use internal/safefile (no link or junction on the path, regular files only, the mode set on the open file)"})
			}
		}
		return true
	})
}
