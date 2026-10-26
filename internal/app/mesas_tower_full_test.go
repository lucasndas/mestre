//go:build full

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// CA-MESAS-03: two real instances on the same machine, a Citadel and a Tower
// joined by the connection, with the real git and the fake agent in a terminal of
// the Tower. A character of the first step commits on the Tower and gives `done`:
// the commit is in the branch of the worktree of the Citadel. Meanwhile the
// Citadel moves ahead; when the next step runs on the Tower, the worktree of the
// Tower has the commits of the Citadel before the step begins (the fake agent
// writes down the commit it found when it was given the step).
func TestTheCommitOfTheTowerReachesTheCitadelAndTheNextStepFindsTheCommitsOfTheCitadel(t *testing.T) {
	t.Parallel()
	x := newChannelRigWith(t, channelOpts{chain: true})
	ctx := t.Context()
	const table, branch = "4-busca", "mestre/4-busca"
	citadelTree := x.seedTable(t, table, branch, 4)
	home := filepath.Join(x.root, "home")
	script := "prompts:\n" +
		"  - match: \"(?s).*PASSO-UM.*\"\n    steps:\n" +
		"      - edit: {path: torre.go, content: \"package torre\\n\"}\n" +
		"      - run: 'git add torre.go'\n" +
		"      - run: 'git commit -q -m \"feat: da torre\"'\n" +
		"      - run: 'mestre done \"um feito\"'\n" +
		"  - match: \"(?s).*PASSO-DOIS.*\"\n    steps:\n" +
		"      - run: 'git rev-parse HEAD > head-ao-comecar.txt'\n" +
		"      - run: 'mestre done \"dois feito\"'\n"
	if err := os.WriteFile(filepath.Join(home, "quick.yaml"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	approveClassOn(t, x.a, "meu-app", "dev", devClass("dev", "")...)

	now := clock.Real{}.Now()
	master := characters.Character{ID: "m4", Name: "Mestre", Kind: characters.KindCharacter, Role: characters.RoleTableMaster, Workspace: "meu-app", Table: table, Machine: x.a.id,
		Sheet: "Dev Claude", Base: "claude-code", Class: "dev", State: characters.Idle, CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Persistent, CreatedAt: now}
	if err := x.orc.Registry.Insert(ctx, master); err != nil {
		t.Fatal(err)
	}
	def := "name: Busca\nsteps:\n" +
		"  - {id: um, sheet: Dev Claude, machine: b, instructions: \"PASSO-UM: commite um arquivo e dê done\"}\n" +
		"  - {id: dois, sheet: Dev Claude, machine: b, after: [um], approval: before, instructions: \"PASSO-DOIS: veja onde a mesa está\"}\n"
	v, err := x.orc.Quests.Create(ctx, quests.CreateRequest{Workspace: "meu-app", Table: table, Data: []byte(def), Designer: master.ID})
	if err != nil {
		t.Fatal(err)
	}
	q, err := x.orc.Quests.Run(ctx, v.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	stateOf := func(step string) quests.StepState {
		st, err := x.orc.Quests.Store().Step(ctx, q.ID+"/"+step)
		if err != nil {
			t.Fatal(err)
		}
		return st.State
	}

	// the first step: the commit made on the Tower is in the branch of the worktree of the Citadel
	x.r.waitFor("the first step to end and the second to wait for its approval", func() bool {
		return stateOf("um") == quests.StepCompleted && stateOf("dois") == quests.StepPreparing
	})
	if got := git(t, citadelTree, "log", "-1", "--format=%s"); got != "feat: da torre" {
		t.Fatalf("the last commit in the worktree of the Citadel is %q; want the one of the Tower", got)
	}
	if _, err := os.Stat(filepath.Join(citadelTree, "torre.go")); err != nil {
		t.Errorf("the file of the commit of the Tower is not in the worktree of the Citadel: %v", err)
	}
	if got := git(t, citadelTree, "rev-parse", "HEAD"); git(t, citadelTree, "rev-parse", branch) != got {
		t.Errorf("the branch %s is not at the head of the worktree of the Citadel", branch)
	}

	// the Citadel moves ahead while the second step waits
	if err := os.WriteFile(filepath.Join(citadelTree, "cidadela.go"), []byte("package cidadela\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, citadelTree, "add", "cidadela.go")
	git(t, citadelTree, "commit", "-q", "-m", "feat: da cidadela")
	ahead := git(t, citadelTree, "rev-parse", "HEAD")

	// the approval of the step is given: it runs on the Tower
	var open []int64
	x.r.waitFor("the request of the approval of the second step", func() bool {
		rs, err := x.orc.Chain.Store().Open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		open = open[:0]
		for _, r := range rs {
			open = append(open, r.ID)
		}
		return len(open) == 1
	})
	if _, err := x.orc.Chain.DecideByPerson(ctx, chain.PersonDecision{Request: open[0], User: "ana", Level: "owner", Answer: chain.AnswerApprove}); err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("the second step to end", func() bool { return stateOf("dois") == quests.StepCompleted })

	// the worktree of the Tower had the commits of the Citadel when the step began
	b, err := os.ReadFile(filepath.Join(x.b.paths.Workspaces, "meu-app", "mesas", table, "head-ao-comecar.txt"))
	if err != nil {
		t.Fatalf("the fake agent of the second step wrote nothing: %v", err)
	}
	if got := strings.TrimSpace(string(b)); got != ahead {
		t.Errorf("the worktree of the Tower was at %s when the second step began; want the commit of the Citadel, %s", got, ahead)
	}
	_ = context.Background
}
