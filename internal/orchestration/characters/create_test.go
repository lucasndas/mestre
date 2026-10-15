package characters_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// makeTM starts the table master of a table, as the engine does it in the
// `table open` of the Grand Master.
func (r *rig) makeTM(table string) characters.Character {
	r.t.Helper()
	res, err := r.create(characters.CreateRequest{Sheet: "Dev Claude", Table: table, Role: characters.RoleTableMaster})
	if err != nil || res.Pending != "" {
		r.t.Fatalf("table master of %s: %v %+v", table, err, res)
	}
	return res.Character
}

func (r *rig) makeGM() characters.Character {
	r.t.Helper()
	res, err := r.create(characters.CreateRequest{Sheet: "Dev Claude", Role: characters.RoleGrandMaster})
	if err != nil || res.Pending != "" {
		r.t.Fatalf("grand master: %v %+v", err, res)
	}
	return res.Character
}

func byChar(c characters.Character) characters.Creator {
	return characters.Creator{Kind: characters.CreatedByCharacter, ID: c.ID}
}

// CA-PERS-01
func TestACharacterThatNeedsAGPUGoesToTheWindowsMachineAndFailsWithEachReasonWhenItIsOffline(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{machines: []machineSpec{linux("linux"), windowsGPU("winpc")}})
	c, err := r.create(characters.CreateRequest{Name: "Treino", Sheet: "GPU Claude", Table: "3-login"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Character.Machine != "winpc" {
		t.Fatalf("the character runs on %q, want winpc", c.Character.Machine)
	}
	if r.mach["winpc"].host.proc(c.Character.ID) == nil || len(r.mach["linux"].host.order) != 0 {
		t.Fatal("the process did not start on the Windows machine alone")
	}

	r.sheets.setOnline("winpc", false)
	_, err = r.create(characters.CreateRequest{Name: "Outro", Sheet: "GPU Claude", Table: "3-login"})
	var nm *machinesheet.NoMachineError
	if !errors.As(err, &nm) || !errors.Is(err, machinesheet.ErrNoMachine) {
		t.Fatalf("error = %v, want no_capable_machine", err)
	}
	for _, want := range []string{"linux: sem GPU", "winpc: offline"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the explanation lacks %q: %v", want, err)
		}
	}
	if len(r.mach["linux"].host.order) != 0 {
		t.Error("something ran on the Linux machine")
	}
	if ns := r.notes.of(characters.NoticeNoCapableMachine); len(ns) != 1 || ns[0].To.Kind != characters.ToOwner || !strings.Contains(ns[0].Text, "winpc: offline") {
		t.Errorf("the Owner was not told: %+v", ns)
	}
	if cs, _ := r.reg.Alive(r.ctx); len(cs) != 1 {
		t.Errorf("%d characters alive, want only the first", len(cs))
	}
}

// CA-PERS-02
func TestAtTheLimitOfTheTableARequestWaitsForAVacancyInOrderAndTimesOutWithoutEndingAnyone(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{limits: characters.Limits{PerTable: 2}})
	tm := r.makeTM("3-login")
	a := r.make("Dev Claude", "Dev A", "3-login")
	b := r.make("Dev Claude", "Dev B", "3-login")
	r.ready(a)
	r.ready(b)

	step, err := r.create(characters.CreateRequest{Name: "Etapa", Sheet: "Dev Claude", Table: "3-login", Quest: "q1"})
	if err != nil || step.Pending == "" {
		t.Fatalf("the step: %v %+v, want pending", err, step)
	}
	direct, err := r.create(characters.CreateRequest{Name: "Direto", Sheet: "Dev Claude", Table: "3-login", Creator: byChar(tm)})
	if err != nil || direct.Pending == "" {
		t.Fatalf("the recruit: %v %+v, want pending", err, direct)
	}
	if cs, _ := r.reg.AliveOf(r.ctx, workspace); len(cs) != 3 { // tm, a, b
		t.Fatalf("%d alive, nobody should have been created", len(cs))
	}

	// A vacancy opens: the first to arrive is made.
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: a.ID, By: engine}); err != nil {
		t.Fatal(err)
	}
	made, err := r.reg.ByName(r.ctx, workspace, "3-login", "Etapa")
	if err != nil || made.Quest != "q1" {
		t.Fatalf("the step was not created when the vacancy opened: %v %+v", err, made)
	}
	if _, err := r.reg.ByName(r.ctx, workspace, "3-login", "Direto"); err == nil {
		t.Fatal("the recruit was created too: there was one vacancy")
	}

	// The time of the other runs out: it fails, naming the limit and who holds it.
	r.advance(31 * time.Minute)
	ns := r.notes.of(characters.NoticeSlotTimeout)
	if len(ns) != 1 || ns[0].Creator != byChar(tm) || ns[0].To.Kind != characters.ToCreator {
		t.Fatalf("notices = %+v, want one to the table master", ns)
	}
	for _, want := range []string{"limite table (2)", "Dev B", "Etapa"} {
		if !strings.Contains(ns[0].Text, want) {
			t.Errorf("the notice lacks %q: %s", want, ns[0].Text)
		}
	}
	if w, err := r.reg.SlotWaitByID(r.ctx, direct.Pending); err != nil || w.State != characters.SlotExpired {
		t.Errorf("the wait of the recruit = %+v, %v; want expired", w, err)
	}
	// In no case was anyone ended to make room: only the one dismissed.
	for _, c := range []characters.Character{b, made} {
		if p := r.mach[c.Machine].host.proc(c.ID); p == nil || len(p.stopCalls()) != 0 {
			t.Errorf("%s was ended", c.Name)
		}
	}
}

func TestTheGrandMasterAndTheTableMastersCountNothingAndAreNeverHeldBack(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{limits: characters.Limits{PerTable: 1, PerMachine: 1, PerWorkspace: 1}})
	r.make("Dev Claude", "Dev", "3-login")
	r.makeGM()
	r.makeTM("3-login")
	r.makeTM("4-api")
	if _, err := r.create(characters.CreateRequest{Name: "Segundo", Sheet: "Dev Claude", Table: "4-api"}); err != nil {
		t.Fatal(err)
	}
	res, _ := r.create(characters.CreateRequest{Name: "Terceiro", Sheet: "Dev Claude", Table: "4-api"})
	if res.Pending == "" {
		t.Error("the limits hold nothing: the third character should wait")
	}
}

func TestTheOtherLimitsHoldToo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		limits characters.Limits
		sheet  string
		limit  string
	}{
		{"workspace", characters.Limits{PerWorkspace: 2}, "Dev Claude", characters.LimitWorkspace},
		{"machine", characters.Limits{PerMachine: 2}, "Dev Claude", characters.LimitMachine},
		{"copies of the sheet", characters.Limits{}, "Dupla", characters.LimitSheet},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, rigOptions{limits: tt.limits})
			r.make(tt.sheet, "Um", "3-login")
			r.make(tt.sheet, "Dois", "4-api")
			res, err := r.create(characters.CreateRequest{Name: "Tres", Sheet: tt.sheet, Table: "3-login"})
			if err != nil || res.Pending == "" {
				t.Fatalf("%v %+v, want pending", err, res)
			}
			w, err := r.reg.SlotWaitByID(r.ctx, res.Pending)
			if err != nil || w.Limit != tt.limit || !strings.Contains(w.Holders, "Um") {
				t.Errorf("wait = %+v (%v), want the limit %s held by Um and Dois", w, err, tt.limit)
			}
		})
	}
}

func TestAMachineIsChosenByPreferenceTheWorktreeTheFewestAndTheId(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{machines: []machineSpec{linux("c"), linux("a"), linux("b")}})
	// Nobody yet: the id.
	first := r.make("Dev Claude", "Um", "3-login")
	if first.Machine != "a" {
		t.Errorf("first machine = %s, want a (alphabetical)", first.Machine)
	}
	// Fewest active: a has one.
	if got := r.make("Dev Claude", "Dois", "3-login").Machine; got != "b" {
		t.Errorf("second machine = %s, want b (fewest characters, then id)", got)
	}
	// The table already has a worktree on c: before the number of characters.
	r.tables.mu.Lock()
	r.tables.tables["4-api"] = characters.TableInfo{ID: "4-api", Workspace: workspace, State: characters.TableWorking, Worktrees: map[string]string{"c": "/wt/c/4-api"}}
	r.tables.mu.Unlock()
	if got := r.make("Dev Claude", "Tres", "4-api").Machine; got != "c" {
		t.Errorf("third machine = %s, want c (where the table has its worktree)", got)
	}
	// The sheet prefers b; --machine narrows to one.
	if _, err := r.cat.SaveSheet(r.ctx, mustSheet(t, r, "Dev Claude", func(s *sheetEdit) { s.PreferredMachine = "b" })); err != nil {
		t.Fatal(err)
	}
	if got := r.make("Dev Claude", "Quatro", "3-login").Machine; got != "b" {
		t.Errorf("fourth machine = %s, want b (preferred by the sheet)", got)
	}
	res, err := r.create(characters.CreateRequest{Name: "Cinco", Sheet: "Dev Claude", Table: "3-login", Machine: "c"})
	if err != nil || res.Character.Machine != "c" {
		t.Errorf("--machine c: %v %+v", err, res)
	}
	if _, err := r.create(characters.CreateRequest{Name: "Seis", Sheet: "Dev Claude", Table: "3-login", Machine: "zz"}); !errors.Is(err, machinesheet.ErrNoMachine) {
		t.Errorf("--machine zz: %v, want no_capable_machine", err)
	}
	// All the machines that fit are full: it waits for a vacancy.
	r2 := newRig(t, rigOptions{machines: []machineSpec{linux("a"), linux("b")}, limits: characters.Limits{PerMachine: 1}})
	r2.make("Dev Claude", "Um", "3-login")
	r2.make("Dev Claude", "Dois", "3-login")
	if res, err := r2.create(characters.CreateRequest{Name: "Tres", Sheet: "Dev Claude", Table: "3-login"}); err != nil || res.Pending == "" {
		t.Errorf("full machines: %v %+v, want pending", err, res)
	}
}

func TestWhoMayCreateWhatAndWhere(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	gm := r.makeGM()
	tm := r.makeTM("3-login")
	dev := r.make("Dev Claude", "Dev", "3-login")
	admin := characters.Creator{Kind: characters.CreatedByUser, ID: "ana", Level: characters.LevelAdministrator}
	observer := characters.Creator{Kind: characters.CreatedByUser, ID: "bia", Level: characters.LevelObserver}
	tests := []struct {
		name string
		req  characters.CreateRequest
		ok   bool
	}{
		{"the table master, in its table", characters.CreateRequest{Sheet: "Dev Claude", Table: "3-login", Creator: byChar(tm)}, true},
		{"the table master, a simple terminal", characters.CreateRequest{Base: "bash", Table: "3-login", Creator: byChar(tm)}, true},
		{"the table master, in another table", characters.CreateRequest{Sheet: "Dev Claude", Table: "4-api", Creator: byChar(tm)}, false},
		{"the table master, another table master", characters.CreateRequest{Sheet: "Dev Claude", Table: "3-login", Role: characters.RoleTableMaster, Creator: byChar(tm)}, false},
		{"the Grand Master, a table master", characters.CreateRequest{Sheet: "Dev Claude", Table: "4-api", Role: characters.RoleTableMaster, Creator: byChar(gm)}, true},
		{"the Grand Master, a common character", characters.CreateRequest{Sheet: "Dev Claude", Table: "3-login", Creator: byChar(gm)}, false},
		{"a common character", characters.CreateRequest{Sheet: "Dev Claude", Table: "3-login", Creator: byChar(dev)}, false},
		{"an Administrator, on the canvas", characters.CreateRequest{Sheet: "Dev Claude", Table: "3-login", Creator: admin}, true},
		{"the Owner, on the canvas", characters.CreateRequest{Sheet: "Dev Claude", Table: "4-api", Creator: owner}, true},
		{"an Observer", characters.CreateRequest{Sheet: "Dev Claude", Table: "3-login", Creator: observer}, false},
		{"a user, a Grand Master", characters.CreateRequest{Sheet: "Dev Claude", Role: characters.RoleGrandMaster, Creator: owner}, false},
	}
	for _, tt := range tests {
		_, err := r.create(tt.req)
		if tt.ok && err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
		if !tt.ok && !errors.Is(err, characters.ErrLevelDenied) {
			t.Errorf("%s: %v, want level_denied", tt.name, err)
		}
	}
}

func TestTheNameFollowsTheRulesOfTheDocument(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	bad := []string{"Mestre", "grão-mestre", "Dev/1", "x" + strings.Repeat("a", 32), " Dev", "Dev!"}
	for _, name := range bad {
		if _, err := r.create(characters.CreateRequest{Name: name, Sheet: "Dev Claude", Table: "3-login"}); !errors.Is(err, characters.ErrInvalidName) {
			t.Errorf("name %q: %v, want invalid_name", name, err)
		}
	}
	a := r.make("Dev Claude", "Dev_1 b", "3-login")
	if _, err := r.create(characters.CreateRequest{Name: "DEV_1 B", Sheet: "Dev Claude", Table: "3-login"}); !errors.Is(err, characters.ErrNameTaken) {
		t.Errorf("same name without difference of case: %v, want name_taken", err)
	}
	if _, err := r.create(characters.CreateRequest{Name: "dev_1 b", Sheet: "Dev Claude", Table: "4-api"}); err != nil {
		t.Errorf("the same name in another table: %v", err)
	}
	// Without a name: <sheet> <n>.
	x, y := r.make("Dev Claude", "", "3-login"), r.make("Dev Claude", "", "3-login")
	if x.Name != "Dev Claude 1" || y.Name != "Dev Claude 2" {
		t.Errorf("default names = %q, %q", x.Name, y.Name)
	}
	// A dismissed name is free again.
	r.ready(a)
	if _, err := r.svc.Dismiss(r.ctx, characters.DismissRequest{ID: a.ID, By: engine}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.create(characters.CreateRequest{Name: "Dev_1 b", Sheet: "Dev Claude", Table: "3-login"}); err != nil {
		t.Errorf("a name of a character that ended: %v", err)
	}
}

func TestNothingIsCreatedInATableThatIsClosingOrOver(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	for _, st := range []characters.TableState{characters.TableClosing, characters.TableClosed, characters.TableCancelling, characters.TableCancelled} {
		r.tables.setState("3-login", st)
		if _, err := r.create(characters.CreateRequest{Sheet: "Dev Claude", Table: "3-login"}); !errors.Is(err, characters.ErrTableClosed) {
			t.Errorf("table %s: %v, want a refusal", st, err)
		}
	}
	r.tables.setState("3-login", characters.TableInReview)
	if _, err := r.create(characters.CreateRequest{Sheet: "Dev Claude", Table: "3-login"}); err != nil {
		t.Errorf("a table in review takes characters: %v", err)
	}
}

func TestASheetOrBaseThatDoesNotExistFailsWithTheValidOnes(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	_, err := r.create(characters.CreateRequest{Sheet: "Fantasma", Table: "3-login"})
	if !errors.Is(err, characters.ErrBadSheet) || !strings.Contains(err.Error(), "Dev Claude") {
		t.Errorf("sheet: %v, want the list of sheets", err)
	}
	_, err = r.create(characters.CreateRequest{Base: "nada", Table: "3-login"})
	if !errors.Is(err, characters.ErrBadSheet) || !strings.Contains(err.Error(), "bash") {
		t.Errorf("base: %v, want the list of bases", err)
	}
	if _, err := r.create(characters.CreateRequest{Table: "3-login"}); !errors.Is(err, characters.ErrBadSheet) {
		t.Errorf("neither: %v", err)
	}
}

func TestACharacterRecordsWhoCreatedItAndTheCreationThatFailsLeavesNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	tm := r.makeTM("3-login")
	c, err := r.create(characters.CreateRequest{Name: "Dev", Sheet: "Dev Claude", Table: "3-login", Creator: byChar(tm)})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.get(c.Character.ID); got.CreatedBy != byChar(tm) || got.Sheet != "Dev Claude" || got.Base != "claude-code" || got.Class != "Dev" || got.Dir == "" {
		t.Errorf("registry row = %+v", got)
	}
	if got := r.get(c.Character.ID); got.PTY.PID == 0 || got.PTY.Started == "" || got.PTY.Version != "1.0.0" {
		t.Errorf("the mestre-pty of the terminal is not in the registry: %+v", got.PTY)
	}
	in := r.builder.input(c.Character.ID)
	if in.NewSessionID == "" || in.Snapshot.Sheet.Name != "Dev Claude" {
		t.Errorf("build input = %+v", in)
	}
	// The verification of the command fails: the start fails, nothing is left.
	r.mach["linux"].prep.verify = func() error { return errors.New("the agent file is missing") }
	if _, err := r.create(characters.CreateRequest{Name: "Falha", Sheet: "Dev Claude", Table: "3-login", Creator: byChar(tm)}); err == nil || !strings.Contains(err.Error(), "agent file is missing") {
		t.Fatalf("error = %v", err)
	}
	if _, err := r.reg.ByName(r.ctx, workspace, "3-login", "Falha"); err == nil {
		t.Error("the character that did not start is in the registry")
	}
	if ends := r.mach["linux"].prep.ended; len(ends) == 0 || ends[len(ends)-1].Final != true {
		t.Errorf("what the failed start wrote was not taken away: %+v", ends)
	}
	if ns := r.notes.of(characters.NoticeStartFailed); len(ns) == 0 {
		t.Error("the table master was not told")
	}
}

// checkFake is the session zero of the table.
type checkFake struct {
	mu  sync.Mutex
	err error
	// only: the error is for this provider alone ("" for all of them).
	only  providers.ID
	calls []string
}

func (c *checkFake) CheckStart(_ context.Context, workspace, table, dir string, p providers.ID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, workspace+"/"+table+" "+dir+" "+string(p))
	if c.only != "" && c.only != p {
		return nil
	}
	return c.err
}

func TestTheFilesOfTheTableAreCheckedBeforeEachCharacterAndAProviderThatIsHeldBackDoesNotStart(t *testing.T) {
	t.Parallel()
	check := &checkFake{}
	r := newRig(t, rigOptions{tables: check})
	tm := r.makeTM("3-login")
	r.make("Dev Claude", "Dev", "3-login")
	if len(check.calls) != 2 || !strings.HasSuffix(check.calls[1], " claude-code") || !strings.HasPrefix(check.calls[1], "meu-app/3-login ") {
		t.Fatalf("checks = %q, want one before each character", check.calls)
	}
	check.err = &sessionzero.BlockedError{Provider: providers.ClaudeCode, Problems: []sessionzero.Problem{{Path: ".claude/settings.local.json", Reason: "not generated"}}}
	_, err := r.create(characters.CreateRequest{Name: "Barrado", Sheet: "Dev Claude", Table: "3-login", Creator: byChar(tm)})
	var blocked *sessionzero.BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("error = %v, want the refusal of the table", err)
	}
	if prepared := r.mach["linux"].prep.prepared; len(prepared) != 2 {
		t.Errorf("%d characters were prepared, the held back one must not be", len(prepared))
	}
	if _, err := r.reg.ByName(r.ctx, workspace, "3-login", "Barrado"); err == nil {
		t.Error("the character that was held back is in the registry")
	}
}

// Part of CA-SZ-MESA-03 (the file check and the alert are in internal/sessionzero):
// the start refuses only the provider whose files are held back, and the
// character of another provider starts in the same table.
func TestOnlyTheProviderThatIsHeldBackIsRefusedAtTheStart(t *testing.T) {
	t.Parallel()
	check := &checkFake{only: providers.Codex}
	r := newRig(t, rigOptions{tables: check, machines: []machineSpec{withCodex(linux("linux"))}})
	tm := r.makeTM("3-login")
	check.err = &sessionzero.BlockedError{Provider: providers.Codex, Problems: []sessionzero.Problem{{Path: ".codex/hooks.json", Reason: "invalid JSON"}}}
	var blocked *sessionzero.BlockedError
	if _, err := r.create(characters.CreateRequest{Name: "Barrado", Sheet: "Revisor Codex", Table: "3-login", Creator: byChar(tm)}); !errors.As(err, &blocked) {
		t.Fatalf("the held back provider: %v", err)
	}
	r.make("Dev Claude", "Livre", "3-login")
	if _, err := r.reg.ByName(r.ctx, workspace, "3-login", "Barrado"); err == nil {
		t.Error("the character that was held back is in the registry")
	}
}

func TestTableFilesNeedsTheSessionZeroOfTheTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if err := (characters.TableFiles{}).CheckStart(ctx, "w", "t", "/d", providers.ClaudeCode); err == nil {
		t.Error("CheckStart without the table")
	}
	if err := (characters.TableFiles{}).Retrust(ctx, "w", "t", "/d"); err == nil {
		t.Error("Retrust without the table")
	}
	table, err := sessionzero.NewTable(sessionzero.TableOptions{Worktree: sessionzero.Worktree{Providers: providers.NewRegistry(providers.Options{})}})
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("the table is not on this machine")
	files := characters.TableFiles{Table: table, Requests: func(context.Context, string, string, string) (sessionzero.TableRequest, error) {
		return sessionzero.TableRequest{}, boom
	}}
	if err := files.CheckStart(ctx, "w", "t", "/d", providers.ClaudeCode); !errors.Is(err, boom) {
		t.Errorf("CheckStart = %v", err)
	}
	if err := files.Retrust(ctx, "w", "t", "/d"); !errors.Is(err, boom) {
		t.Errorf("Retrust = %v", err)
	}
	// A request the table refuses (no worktree) is the refusal of the start.
	files.Requests = func(context.Context, string, string, string) (sessionzero.TableRequest, error) {
		return sessionzero.TableRequest{Workspace: "w", Table: "t"}, nil
	}
	if err := files.CheckStart(ctx, "w", "t", "/d", providers.ClaudeCode); err == nil {
		t.Error("a request without a worktree passed")
	}
}
