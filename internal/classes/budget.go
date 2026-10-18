package classes

// The analysis of a command line has a bound (docs/classes/formato-das-permissoes.md#comandos):
// a line longer than maxAnalysedCommand, or one whose analysis (the line,
// its nested shells, substitutions, here-documents and pipes, for the
// decision and for the risk) passes maxAnalysisWork units, is denied by a
// fixed rule instead of being analysed, and so are the expansions nested
// deeper than maxExpansionDepth. The hook then answers well within its
// deadline for any input it reads, and an input built to take long is
// never allowed by running out of time.

const (
	// maxAnalysedCommand is the longest command line analysed (64 KiB),
	// without the bodies of its here-documents, which are data; the
	// whole line is never longer than maxCommandWithData (1 MiB). A body a
	// shell reads is a command line, and has the same bound.
	maxAnalysedCommand = 64 << 10
	maxCommandWithData = 1 << 20
	// maxAnalysisWork bounds the units of one decision: a unit is about
	// what lexing a byte costs; a line read (a nested shell, a
	// substitution, a body) costs costLine more, a part costPart and each
	// reader of a here-document costReader.
	maxAnalysisWork = 4 << 20
	costLine        = 1024
	costPart        = 32
	costReader      = 8
	// costNested is the cost of a command that another one runs (a
	// launcher, a package runner, a wrapper): each can start more, and the
	// work of a chain of them must not grow without bound.
	costNested = 256
	// maxExpansionDepth bounds the nesting of ${…} and $((…)) read for
	// the substitutions inside them.
	maxExpansionDepth = 32
)

// The reasons of the denials of the bound.
const (
	reasonTooLong = "comando longo demais para ser analisado"
	reasonTooMuch = "comando trabalhoso demais para ser analisado"
	reasonTooDeep = "expansões aninhadas demais para serem analisadas"
	// reasonControl is the denial of a line the analysis cannot be sure to
	// read as the shell does.
	reasonControl = "a linha traz NUL ou outro byte de controle, que o shell pode ler de outro modo que o analisador"
)

// hasControlByte reports NUL or a control character other than the tab, the
// line feed and the carriage return that a line is made of. A shell may drop
// it, keep it in the name of a program or take it for a space (PowerShell
// does with the vertical tab and the form feed), so the analysis, which sees
// one of them, may differ from what runs: the line is denied whole.
func hasControlByte(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 && c != '\t' && c != '\n' && c != '\r' || c == 0x7f {
			return true
		}
	}
	return false
}

// spend adds n units of work to the analysis; true is over the bound.
func (e *evaluator) spend(n int) bool {
	e.work += n
	return e.work > maxAnalysisWork
}

// analysable reports a line within the bound of length: up to
// maxAnalysedCommand, or up to maxCommandWithData when the bodies of its
// here-documents (cat <<'EOF' > data.json) take the rest.
func analysable(cmd string) bool {
	switch {
	case len(cmd) <= maxAnalysedCommand:
		return true
	case len(cmd) > maxCommandWithData:
		return false
	}
	lx := &lexer{s: cmd}
	lx.run()
	data := 0
	for _, t := range lx.toks {
		if t.doc != nil && t.doc.read {
			data += len(t.doc.body)
		}
	}
	return len(cmd)-data <= maxAnalysedCommand
}
