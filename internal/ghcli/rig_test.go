package ghcli_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/ghcli"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

const repoFull = "lucas/meu-app"

type chars map[string]characters.Character

func (c chars) Get(_ context.Context, id string) (characters.Character, error) {
	if x, ok := c[id]; ok {
		return x, nil
	}
	return characters.Character{}, characters.ErrNoCharacter
}

type tables map[string]github.TableRef

func (t tables) Epic(_ context.Context, table string) (github.TableRef, bool, error) {
	v, ok := t[table]
	return v, ok, nil
}

type repos struct{}

func (repos) Repo(context.Context, string) (string, error) { return repoFull, nil }

// clients gives each character the REST of its App: the token tells the App to
// the double.
type clients struct{ gh *fakegithub.Server }

func (c clients) For(_ context.Context, ch characters.Character, _ string) (github.IssueAPI, error) {
	tok := "tok_mestre"
	switch {
	case ch.Role == characters.RoleGrandMaster:
		tok = "tok_gm"
	case ch.Class == "qa":
		tok = "tok_qa"
	}
	return &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(c.gh)},
		Token: func(context.Context, string) (string, error) { return tok, nil }}, nil
}

type rig struct {
	t    *testing.T
	ctx  context.Context
	gh   *fakegithub.Server
	objs *github.Objects
	gate *cli.Gate
}

// newRig has a table 42-login (its epic is the issue #1) with its Mestre da mesa "tm", a QA
// "qa" of the class qa that may use issue commands, a Dev "dev" whose class
// does not, and the Grão-Mestre "gm".
func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gh, err := fakegithub.New(clk, t)
	if err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("lucas", "meu-app")
	gh.IssueToken("tok_gm", "grand_master")
	gh.IssueToken("tok_mestre", "mestre")
	gh.IssueToken("tok_qa", "qa")
	gh.AddIssue(repoFull, "épico 42", github.EpicLabel) // #1... the numbers of the double start at 1
	objs := &github.Objects{DB: db, Clock: clk}
	cs := chars{
		"tm":  {ID: "tm", Role: characters.RoleTableMaster, Workspace: "meu-app", Table: "42-login", Class: "mestre-da-mesa"},
		"qa":  {ID: "qa", Role: characters.RoleCharacter, Workspace: "meu-app", Table: "42-login", Class: "qa"},
		"dev": {ID: "dev", Role: characters.RoleCharacter, Workspace: "meu-app", Table: "42-login", Class: "dev"},
		"gm":  {ID: "gm", Role: characters.RoleGrandMaster, Workspace: "meu-app"},
	}
	h := &ghcli.Handlers{Characters: cs, Clients: clients{gh}, Repos: repos{}, Tables: tables{"42-login": {Repo: repoFull, Epic: 1}}, Objects: objs, Clock: clk}
	fwd := localchan.NewLocalForwarder()
	h.Register(fwd)
	profiles := cli.ProfilesFunc(func(_ context.Context, id string) cli.Profile {
		switch id {
		case "tm":
			return cli.Profile{Role: cli.RoleTableMaster}
		case "gm":
			return cli.Profile{Role: cli.RoleGrandMaster}
		case "qa":
			return cli.Profile{Role: cli.RoleCharacter, AllowsCLI: func(c string) bool { return len(c) > 6 && c[:6] == "issue " || len(c) > 3 && c[:3] == "pr " }}
		}
		return cli.Profile{Role: cli.RoleCharacter}
	})
	return &rig{t: t, ctx: ctx, gh: gh, objs: objs, gate: cli.NewGate(cli.GateConfig{Profiles: profiles, Next: fwd})}
}

// result is the answer of a command.
type result struct {
	OK    bool
	Exit  int
	Code  string
	Data  map[string]any
	Error string
}

// run sends a command of a character through the gate, as the CLI would.
func (r *rig) run(who, cmd string, flags map[string]any, args ...string) result {
	r.t.Helper()
	id, err := localchan.NewID(clock.Real{})
	if err != nil {
		r.t.Fatal(err)
	}
	return r.runID(id, who, cmd, flags, args...)
}

// runID is run with the id of the request: the same one again is a resending.
func (r *rig) runID(id, who, cmd string, flags map[string]any, args ...string) result {
	r.t.Helper()
	if flags == nil {
		flags = map[string]any{}
	}
	resp := r.gate.Forward(r.ctx, &localchan.Call{
		Request: localchan.Request{V: 1, ID: id, AgentID: who, Cmd: cmd, Args: localchan.TextArgs(args...), Flags: flags},
		Caller:  localchan.Caller{Identified: true, AgentID: who, TerminalID: "t-" + who},
	})
	out := result{OK: resp.OK, Exit: resp.ExitCode()}
	if resp.Error != nil {
		out.Code, out.Error = resp.Error.Code, resp.Error.Message
	}
	if resp.OK {
		var m map[string]any
		_ = json.Unmarshal(resp.Data, &m)
		out.Data = m
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }
