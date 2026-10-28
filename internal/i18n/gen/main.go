// Command gen writes web/src/gen/messages.ts from the catalogs of the engine
// (internal/i18n/locales). It runs through `go generate ./internal/i18n`; a fast
// test regenerates in memory and fails when the committed file is stale
// (docs/desenvolvimento/convencoes.md#código-gerado).
//
//	gen <output.ts>
package main

import (
	"fmt"
	"os"

	"github.com/mestre-ia/mestre/internal/i18n"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen <output.ts>")
		os.Exit(2)
	}
	ts, err := i18n.GenerateTS()
	if err != nil {
		fmt.Fprintln(os.Stderr, "i18n gen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[1], ts, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "i18n gen:", err)
		os.Exit(1)
	}
}
