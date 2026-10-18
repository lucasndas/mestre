package classes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/localchan"
)

// CmdCheck is the command `mestre class check`.
const CmdCheck = "class check"

// CheckCommand is the engine side of `mestre class check "<classe>"`
// (docs/cli/coordenacao-classes-e-rituais.md): the "casca" of the command
// that classes.md used before the CLI of classes (M4, step 11). It validates
// the class of the caller's workspace and shows the report.
type CheckCommand struct {
	// Scope finds the library of the workspace of the caller and the table he
	// works in ("" for the Grand Master). The registry of characters provides
	// it, once the engine hosts the orchestration (docs/desenvolvimento/
	// ordem-de-implementacao.md, "M5, antes da etapa 1").
	Scope func(ctx context.Context, c *localchan.Call) (lib *Library, table string, err error)
}

var _ localchan.Handler = CheckCommand{}

// checkData is the data of the response: the report, and its text for the
// terminal.
type checkData struct {
	Text string `json:"text"`
	Report
}

// Handle serves the command: exit 0 with the report of a valid class, 2
// invalid_class with the list of problems, 4 not_visible for a class the
// caller does not see.
func (cc CheckCommand) Handle(ctx context.Context, c *localchan.Call) localchan.Response {
	args, ok := c.Request.Strings()
	if !ok || len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre class check "<class>"`, "")
	}
	if cc.Scope == nil {
		return localchan.Fail(localchan.CodeUnavailable, "this machine does not know the workspace of the caller yet", "")
	}
	lib, table, err := cc.Scope(ctx, c)
	if err != nil {
		return localchan.Fail(localchan.CodeDenied, err.Error(), "")
	}
	rep, err := lib.Check(ctx, Slug(args[0]), table)
	switch {
	case errors.Is(err, ErrNotVisible):
		return localchan.Fail("not_visible", fmt.Sprintf("no class %q is visible to you", args[0]), "").WithExit(localchan.ExitNotFound)
	case err != nil:
		return localchan.Fail(localchan.CodeInternal, err.Error(), "")
	}
	if !rep.Valid() {
		r := localchan.Fail("invalid_class", rep.String(), "fix the problems and check again")
		r.Error.Details["problems"] = rep.Problems
		return r.WithExit(localchan.ExitUsage)
	}
	return localchan.OK(checkData{Text: rep.String(), Report: rep})
}
