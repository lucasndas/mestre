package chain

import (
	"context"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/localchan"
)

// Handlers are the commands of the supervision to register in the local
// forwarder of the Citadel: `approval autonomy pause|resume`
// (docs/cli/grao-mestre.md#mestre-approval). The CLI definition of `approval`
// comes with milestone 5, with the rest of the commands of the Grão-Mestre;
// the engine already answers these two.
func (s *Supervisor) Handlers() map[string]localchan.Handler {
	return map[string]localchan.Handler{
		"approval autonomy pause":  localchan.HandlerFunc(func(ctx context.Context, c *localchan.Call) localchan.Response { return s.handle(ctx, c, true) }),
		"approval autonomy resume": localchan.HandlerFunc(func(ctx context.Context, c *localchan.Call) localchan.Response { return s.handle(ctx, c, false) }),
	}
}

// Register registers the handlers in the local forwarder of the Citadel.
func (s *Supervisor) Register(fwd *localchan.LocalForwarder) {
	for cmd, h := range s.Handlers() {
		fwd.Handle(cmd, h)
	}
}

// SupervisionRefusal turns an error of the supervision into the answer of the CLI.
func SupervisionRefusal(err error) localchan.Response {
	switch {
	case errors.Is(err, ErrLevelDenied):
		return localchan.Fail("level_denied", err.Error(), "only the Grand Master of the workspace, or an Approver or above, changes the autonomy of a table").WithExit(localchan.ExitDenied)
	case errors.Is(err, ErrNoTable):
		return localchan.Fail("not_found", err.Error(), "see `mestre table list`").WithExit(localchan.ExitNotFound)
	case errors.Is(err, ErrTableOver):
		return localchan.Fail("table_over", err.Error(), "the table already ended").WithExit(localchan.ExitConflict)
	case errors.Is(err, ErrReasonRequired):
		return localchan.Fail(localchan.CodeUsage, err.Error(), `use --reason "<reason>"`)
	}
	return localchan.Fail(localchan.CodeInternal, err.Error(), "")
}

func (s *Supervisor) handle(ctx context.Context, c *localchan.Call, pause bool) localchan.Response {
	if !c.Caller.Identified {
		return localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character", "run mestre debug")
	}
	who, err := s.o.Characters.Get(ctx, c.Caller.AgentID)
	if err != nil {
		return localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this character", "run mestre debug")
	}
	table, _ := c.Request.Flags["table"].(string)
	reason, _ := c.Request.Flags["reason"].(string)
	if table == "" {
		return localchan.Fail(localchan.CodeUsage, `usage: mestre approval autonomy pause|resume --table "<table>" --reason "<reason>"`, "")
	}
	req := PauseRequest{Workspace: who.Workspace, Table: table, Reason: reason, By: Actor{Character: &who}}
	var changed bool
	verb := "paused"
	if pause {
		changed, err = s.PauseAutonomy(ctx, req)
	} else {
		verb = "resumed"
		changed, err = s.ResumeAutonomy(ctx, req)
	}
	if err != nil {
		return SupervisionRefusal(err)
	}
	text := fmt.Sprintf("autonomy of table %s %s", table, verb)
	if !changed {
		text = fmt.Sprintf("autonomy of table %s was already %s", table, map[bool]string{true: "paused", false: "running"}[pause])
	}
	return localchan.OK(map[string]any{"table": table, "paused": pause, "changed": changed, "text": text})
}
