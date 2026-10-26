// Command gen writes internal/glossary/terms_gen.go from the glossary
// document (docs/produto/glossario.md). It runs through `go generate
// ./internal/glossary`; a fast test regenerates in memory and fails when the
// committed file is stale (docs/desenvolvimento/convencoes.md#código-gerado).
//
//	gen <glossario.md> <glossario-en.md> <output.go> <output.ts>
package main

import (
	"fmt"
	"os"

	"github.com/mestre-ia/mestre/internal/glossary"
)

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: gen <glossario.md> <glossario-en.md> <output.go> <output.ts>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2], os.Args[3], os.Args[4]); err != nil {
		fmt.Fprintln(os.Stderr, "glossary gen:", err)
		os.Exit(1)
	}
}

func run(in, inEN, out, outTS string) error {
	terms, err := glossary.Load(in, inEN)
	if err != nil {
		return err
	}
	src, err := glossary.Generate(terms)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, src, 0o644); err != nil {
		return err
	}
	ts, err := glossary.GenerateTS(terms)
	if err != nil {
		return err
	}
	return os.WriteFile(outTS, ts, 0o644)
}
