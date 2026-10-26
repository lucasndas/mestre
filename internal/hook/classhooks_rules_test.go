package hook_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/providers"
)

type rulesOf struct{ r *classes.Rules }

func (s rulesOf) Rules(context.Context, string) (*classes.Rules, error) { return s.r, nil }

type envOf struct{}

func (envOf) Env(_ context.Context, id string) (classes.Env, error) {
	return classes.Env{AgentID: id, TableID: "4-busca", Worktree: "/w", OS: "linux"}, nil
}

// Part of CA-CLASSES-04 on a machine with no library of classes (a Tower): the hooks are the ones the
// Citadel sent with the effective rules, synchronous and asynchronous, and when the rules came
// without them the hooks are unknown, never none.
func TestTheHooksOfTheClassComeWithTheEffectiveRulesOnAMachineWithNoLibrary(t *testing.T) {
	t.Parallel()
	perms, err := classes.ParsePermissions([]byte("version: 1\nfiles: {read: [\"**\"], write: [\"**\"]}\ncommands: {default: allow}\ntools: {default: allow}\n"))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := classes.Build(classes.Layers{AgentID: "c1", Class: "dev", Permissions: perms, Now: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	ev := providers.Event{Version: providers.EventVersion, Event: providers.EventPreTool, AgentID: "c1", Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "make test"}}
	src := func(r *classes.Rules) *hook.ClassHookSource {
		return &hook.ClassHookSource{FromRules: true, Rules: rulesOf{r}, Characters: envOf{}}
	}

	// the rules came without the hooks: unknown, for the synchronous, the asynchronous and the blocking question
	if _, err := src(rules).Sync(t.Context(), ev); !errors.Is(err, hook.ErrHooksNotSent) {
		t.Errorf("Sync without the hooks = %v, want ErrHooksNotSent", err)
	}
	if _, _, err := src(rules).Async(t.Context(), ev); !errors.Is(err, hook.ErrHooksNotSent) {
		t.Errorf("Async without the hooks = %v, want ErrHooksNotSent", err)
	}
	if has, err := src(rules).HasBlocking(t.Context(), "c1"); !has || err == nil {
		t.Errorf("HasBlocking without the hooks = %v, %v, want true (on any doubt) and the error", has, err)
	}

	// the rules came with them
	rules.ClassHooks = &classes.ShippedHooks{Hash: "h1", Hooks: classes.Hooks{
		providers.EventPreTool: {{Name: "guarda", Run: "make check", Mode: classes.ModeSync, Timeout: 20, Blocking: true}},
		providers.EventTurnEnd: {{Name: "depois", Run: "make report", Mode: classes.ModeAsync, Timeout: 60, Output: classes.OutputScroll}},
	}}
	sync, err := src(rules).Sync(t.Context(), ev)
	if err != nil || len(sync) != 1 || sync[0].Name != "guarda" || !sync[0].Blocking || sync[0].Run != "make check" || sync[0].Refused != "" {
		t.Fatalf("Sync = %+v, %v", sync, err)
	}
	end := ev
	end.Event, end.Tool = providers.EventTurnEnd, nil
	async, _, err := src(rules).Async(t.Context(), end)
	if err != nil || len(async) != 1 || async[0].Name != "depois" || async[0].Output != classes.OutputScroll {
		t.Fatalf("Async = %+v, %v", async, err)
	}
	if has, err := src(rules).HasBlocking(t.Context(), "c1"); err != nil || !has {
		t.Errorf("HasBlocking = %v, %v, want true", has, err)
	}

	// a class that has none: known, and none
	rules.ClassHooks = &classes.ShippedHooks{Hash: "h2"}
	if sync, err := src(rules).Sync(t.Context(), ev); err != nil || len(sync) != 0 {
		t.Errorf("Sync of a class with no hooks = %+v, %v", sync, err)
	}
}
