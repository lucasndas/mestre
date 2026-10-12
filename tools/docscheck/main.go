// Command docscheck verifies the structure of the documentation
// (docs/qualidade/verificacao-de-docs.md): indexes, links and anchors,
// size, header, sections, acceptance criteria, names, glossary and the
// implementation order. It runs in the fast suite as TestDocs and alone as
// `go run ./tools/docscheck [-root dir]`.
package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/lucasndas/mestre/pkg/clock"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	os.Exit(run(os.DirFS(*root), clock.Real{}, os.Stdout))
}

func run(fsys fs.FS, clk clock.Clock, out io.Writer) int {
	start := clk.Now()
	res := Check(fsys, clk.Now())
	for _, w := range res.Warnings {
		fmt.Fprintln(out, w)
	}
	for _, e := range res.Errors {
		fmt.Fprintln(out, e)
	}
	fmt.Fprintf(out, "docscheck: %d erro(s), %d aviso(s) em %v\n", len(res.Errors), len(res.Warnings), clock.Since(clk, start).Round(1e6))
	if len(res.Errors) > 0 {
		return 1
	}
	return 0
}
