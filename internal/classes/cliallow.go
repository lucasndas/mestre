package classes

// CLIAllows reports whether the cli.allow patterns of the effective rules
// list the command of the CLI (its name as typed: "issue comment"). The
// engine asks it at every call of a command that a class may allow
// (docs/classes/permissoes.md, docs/cli/convencoes.md#quem-pode-usar-nível).
// A pattern is a text pattern: * matches any text, and "issue *" also
// matches "issue".
func CLIAllows(patterns []string, command string) bool {
	for _, p := range patterns {
		if matchText(p, command) {
			return true
		}
	}
	return false
}
