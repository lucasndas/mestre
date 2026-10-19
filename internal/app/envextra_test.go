package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
)

// noGit opens nothing: the test looks at what the environment carries.
type noGit struct{}

func (noGit) OpenWorktree(context.Context, gitx.WorktreeRecord) (gitx.Repo, error) {
	return nil, errors.New("not here")
}

func writeEvent(kind providers.EventKind, agent, path string, tool providers.ToolKind, ok bool) providers.Event {
	ev := providers.Event{Version: providers.EventVersion, Event: kind, AgentID: agent, TableID: "42-login"}
	if kind == providers.EventPostTool || kind == providers.EventPreTool {
		ev.Tool = &providers.Tool{NativeName: "Edit", Kind: tool, Paths: []string{path}}
		if kind == providers.EventPostTool {
			ev.ToolResult = &providers.ToolResult{OK: ok}
		}
	}
	return ev
}

// CA-HOOK-09
func TestTheEditsOfAnUnfinishedTurnDenyTheWriteOfAnotherCharacterOfTheTable(t *testing.T) {
	t.Parallel()
	wt := t.TempDir()
	path := filepath.Join(wt, "src", "a.go")
	tracker := classes.NewEditTracker()
	var d Deps
	d.UseEdits(tracker, func(id string) string { return map[string]string{"dev1": "Dev 1"}[id] })
	feed := func(ev providers.Event) { d.OnHookEvent(ev, hook.Answer{}) }

	p, err := classes.ParsePermissions([]byte("version: 1\nfiles: {write: [\"**\"]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	rules, err := classes.Build(classes.Layers{AgentID: "dev2", Class: "dev", Permissions: p, Now: time.Unix(1_700_000_000, 0)})
	if err != nil {
		t.Fatal(err)
	}
	env := classes.Env{AgentID: "dev2", TableID: "42-login", Worktree: wt, Edits: tracker}
	decide := func() classes.Decision {
		return classes.Decide(context.Background(), env, rules, writeEvent(providers.EventPreTool, "dev2", path, providers.KindEdit, true))
	}
	if got := decide(); got.Verdict != classes.Allow {
		t.Fatalf("before any edit: %+v", got)
	}

	// a write that failed, a read and a shell do not record
	feed(writeEvent(providers.EventPostTool, "dev1", path, providers.KindEdit, false))
	feed(writeEvent(providers.EventPostTool, "dev1", path, providers.KindRead, true))
	feed(writeEvent(providers.EventPostTool, "dev1", path, providers.KindShell, true))
	if got := decide(); got.Verdict != classes.Allow {
		t.Fatalf("after writes that did not happen: %+v", got)
	}

	// the write that went through
	feed(writeEvent(providers.EventPostTool, "dev1", path, providers.KindEdit, true))
	if got := decide(); got.Verdict != classes.Deny || got.Layer != classes.LayerFixed || !strings.Contains(got.Reason, "Dev 1") {
		t.Fatalf("during the turn of the other: %+v; want a fixed denial citing Dev 1", got)
	}
	// the one who edited is not stopped by its own edit
	own := classes.Env{AgentID: "dev1", TableID: "42-login", Worktree: wt, Edits: tracker}
	ownRules, _ := classes.Build(classes.Layers{AgentID: "dev1", Class: "dev", Permissions: p, Now: time.Unix(1_700_000_000, 0)})
	if got := classes.Decide(context.Background(), own, ownRules, writeEvent(providers.EventPreTool, "dev1", path, providers.KindEdit, true)); got.Verdict != classes.Allow {
		t.Fatalf("own edit: %+v", got)
	}
	// another table is not affected
	other := env
	other.TableID = "43-other"
	if got := classes.Decide(context.Background(), other, rules, writeEvent(providers.EventPreTool, "dev2", path, providers.KindEdit, true)); got.Verdict != classes.Allow {
		t.Fatalf("another table: %+v", got)
	}

	// the end of the turn (and the other ways it ends) frees the file
	for _, end := range []providers.EventKind{providers.EventTurnEnd, providers.EventTurnFailure, providers.EventInterrupt, providers.EventSessionEnd} {
		feed(writeEvent(providers.EventPostTool, "dev1", path, providers.KindWrite, true))
		if got := decide(); got.Verdict != classes.Deny {
			t.Fatalf("before %s: %+v", end, got)
		}
		feed(writeEvent(end, "dev1", "", providers.KindEdit, true))
		if got := decide(); got.Verdict != classes.Allow {
			t.Fatalf("after %s: %+v", end, got)
		}
	}
}

func TestTheEnvironmentOfACharacterGetsWhatOnlyTheMachineKnows(t *testing.T) {
	t.Parallel()
	tracker := classes.NewEditTracker()
	risk := &classes.RiskPolicy{}
	rec := gitx.WorktreeRecord{Dir: "/w/42", GitDir: "/g/42", Common: "/g"}
	var asked []string
	extra := NewEnvExtra(EnvExtraOptions{
		Edits: tracker, Git: noGit{}, Moved: func(error) {},
		Worktrees: func(_ context.Context, ws, table string) (WorktreeInfo, error) {
			asked = append(asked, ws+"/"+table)
			if table != "42-login" {
				return WorktreeInfo{}, errors.New("no record")
			}
			return WorktreeInfo{Record: rec, BaseCommit: "abc", Main: "refs/remotes/origin/main"}, nil
		},
		Risk: func(ws string) *classes.RiskPolicy {
			if ws == "meu-app" {
				return risk
			}
			return nil
		},
	})

	var env classes.Env
	extra(characters.Launch{Workspace: "meu-app", Table: "42-login"}, &env)
	g, ok := env.Git.(classes.GitxState)
	if !ok || g.Worktree != rec || g.Base != "abc" || g.Main != "refs/remotes/origin/main" || g.Git == nil {
		t.Fatalf("git state = %+v", env.Git)
	}
	if env.Normalize == nil || env.Edits != tracker || env.Risk != risk {
		t.Fatalf("env = %+v", env)
	}

	// a worktree without a record, a character without a table (the Grand Master)
	// and a workspace without policy: no git state, the default risk, a commit is denied
	env = classes.Env{}
	extra(characters.Launch{Workspace: "outro", Table: "9-x"}, &env)
	extra(characters.Launch{Workspace: "outro"}, &env)
	if env.Git != nil || env.Risk != nil || env.Normalize == nil || env.Edits != tracker {
		t.Fatalf("env without record = %+v", env)
	}
	if len(asked) != 2 {
		t.Fatalf("asked %v: a character without a table has no worktree to look for", asked)
	}
}
