// Command gen writes internal/glossary/terms_gen.go from the glossary
// document (docs/produto/glossario.md). It runs through `go generate
// ./internal/glossary`; a fast test regenerates in memory and fails when the
// committed file is stale (docs/desenvolvimento/convencoes.md#código-gerado).
//
//	gen <glossario.md> <output.go>
package main

import (
	"fmt"
	"os"

	"github.com/mestre-ia/mestre/internal/glossary"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: gen <glossario.md> <output.go>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "glossary gen:", err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	f, err := os.Open(in)
	if err != nil {
		return err
	}
	defer f.Close()
	terms, problems := glossary.Parse(f)
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(os.Stderr, "%s:%d: %s\n", in, p.Line, p.Msg)
		}
		return fmt.Errorf("%d problem(s) in %s", len(problems), in)
	}
	src, err := glossary.Generate(terms)
	if err != nil {
		return err
	}
	return os.WriteFile(out, src, 0o644)
}
