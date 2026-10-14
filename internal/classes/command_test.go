package classes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/localchan"
)

func checkCall(name string) *localchan.Call {
	return &localchan.Call{Request: localchan.Request{Cmd: classes.CmdCheck, Args: localchan.TextArgs(name)}}
}

// `mestre class check` is a command of the registry for the Masters, and the
// engine side answers with the report: exit 0 and the sections of the
// providers for a valid class, 2 invalid_class with the problems for an
// invalid one, 4 not_visible for a class the caller does not see.
func TestTheClassCheckCommandAnswersWithTheReport(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.save(classes.SaveRequest{Name: "qa", Scope: classes.ClassTable, Table: tableID, App: "novo", By: tableMaster, Files: pkg(t, "qa", "", defaultPerms, nil)})
	f.put(classes.ClassKey{Scope: classes.ClassTable, Table: tableID, Name: "quebrada"}, pkg(t, "quebrada", "novo", "version: 9\n", nil))
	cmd := classes.CheckCommand{Scope: func(context.Context, *localchan.Call) (*classes.Library, string, error) { return f.lib, tableID, nil }}

	resp := cmd.Handle(context.Background(), checkCall("qa"))
	if !resp.OK || resp.ExitCode() != 0 {
		t.Fatalf("response = %+v", resp)
	}
	var data struct {
		Text      string
		Providers []struct{ Name string }
		App       struct{ Label string }
	}
	if err := resp.DecodeData(&data); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Claude Code", "Codex", "Antigravity", "App novo: em criação"} {
		if !strings.Contains(data.Text, want) {
			t.Errorf("the text has no %q:\n%s", want, data.Text)
		}
	}
	if len(data.Providers) != 3 || data.App.Label != "em criação" {
		t.Errorf("data = %+v", data)
	}

	resp = cmd.Handle(context.Background(), checkCall("quebrada"))
	if resp.OK || resp.Error.Code != "invalid_class" || resp.ExitCode() != localchan.ExitUsage || !strings.Contains(resp.Error.Message, "version 9") {
		t.Errorf("invalid class = %+v", resp.Error)
	}
	resp = cmd.Handle(context.Background(), checkCall("fantasma"))
	if resp.OK || resp.Error.Code != "not_visible" || resp.ExitCode() != localchan.ExitNotFound {
		t.Errorf("unknown class = %+v exit %d", resp.Error, resp.ExitCode())
	}
	if resp := cmd.Handle(context.Background(), &localchan.Call{Request: localchan.Request{Cmd: classes.CmdCheck}}); resp.OK || resp.ExitCode() != localchan.ExitUsage {
		t.Errorf("no argument = %+v", resp)
	}

	// The command is in the registry of the CLI, for the Masters (and for a
	// character whose class lists it), and takes exactly one argument.
	spec, ok := cli.Default().Get(classes.CmdCheck)
	if !ok || !spec.Who.Has(cli.RoleTableMaster) || !spec.Who.Has(cli.RoleGrandMaster) || spec.Who.Has(cli.RoleCharacter) || !spec.ClassCanGrant || spec.MinArgs != 1 || spec.MaxArgs != 1 {
		t.Errorf("spec = %+v", spec)
	}
}
