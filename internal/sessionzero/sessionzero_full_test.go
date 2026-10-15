//go:build full

package sessionzero_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

const projectAgents = "# Meu App\n\nConvenções do projeto, escritas à mão.\r\n\tcom bytes estranhos \xc3\xa9\n"

// The mestre-owned part of the exclusions, as the document lists it (the
// hooks file only because the Mestre made it).
var localExcludes = []string{
	"/.mestre/agents/", "/.mestre/anexos/", "/.mestre/run/",
	"/.agents/skills/mestre-*/", "/.claude/skills/mestre-*/", "/.agents/agents/mestre-*.md",
	"/.claude/settings.local.json", "/.agents/hooks.json", "/.mestre/",
}

// Part of CA-SZ-WS-01 (heavy, MVP-3, closed at the end of the project in the
// laboratory): a repository with AGENTS.md and a rule of required review. The
// full suite checks, with the real git and a fake GitHub, that .mestre/, the
// skills and the hooks are generated, the AGENTS.md is the same byte for
// byte, git status in the main clone is clean, a new clone brings no file of
// the Mestre and the result of the approval test is where the panel reads it.
func TestRealSessionZeroLeavesTheProjectUntouched(t *testing.T) {
	t.Parallel()
	e := newEnv(t, map[string]string{"AGENTS.md": projectAgents, ".gitignore": "/build/\n"})
	e.f.rules = sessionzero.Rules{RequiresReview: true, Summary: "1 aprovação exigida"}
	w := e.active()
	if w.State != workspaces.StateActive {
		t.Fatalf("state = %s/%s; want active", w.State, w.Stage)
	}
	for _, rel := range []string{
		".mestre/regras/teto.yaml", ".mestre/regras/politica-de-aprovacao.yaml", ".mestre/regras/instrucoes/grao-mestre.md",
		".mestre/regras/instrucoes/mestre-da-mesa.md", ".mestre/modelos/VERSAO",
		".agents/skills/mestre-comunicacao/SKILL.md", ".claude/skills/mestre-comunicacao/SKILL.md",
		".claude/settings.local.json", ".agents/hooks.json",
	} {
		if !e.exists(w.Slug, rel) {
			t.Errorf("%s was not generated", rel)
		}
	}
	for _, d := range []string{".mestre/agents", ".mestre/anexos", ".mestre/run", ".mestre/classes", ".mestre/mesas"} {
		if fi, err := os.Stat(filepath.Join(e.principal(w.Slug), filepath.FromSlash(d))); err != nil || !fi.IsDir() {
			t.Errorf("folder %s = %v, %v", d, fi, err)
		}
	}
	if !strings.Contains(string(e.readPrincipal(w.Slug, ".claude/settings.local.json")), "mestre") || !strings.Contains(string(e.readPrincipal(w.Slug, ".agents/hooks.json")), "mestre") {
		t.Error("the hook files do not call mestre")
	}
	if got := e.readPrincipal(w.Slug, "AGENTS.md"); !bytes.Equal(got, []byte(projectAgents)) {
		t.Errorf("AGENTS.md changed: %q", got)
	}
	if got := e.readPrincipal(w.Slug, ".gitignore"); string(got) != "/build/\n" {
		t.Errorf(".gitignore changed: %q", got)
	}
	if status := e.gitOut(e.principal(w.Slug), "status", "--porcelain", "--untracked-files=all"); strings.TrimSpace(status) != "" {
		t.Errorf("git status in the main clone is not clean:\n%s", status)
	}
	// Someone else clones the project: nothing of the Mestre comes along.
	other := filepath.Join(e.root, "outra-pessoa")
	if _, err := e.git.Clone(e.ctx, e.remote.Dir(), other, gitx.CloneOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, want := walkFiles(t, other), []string{".gitignore", "AGENTS.md", "README.md"}; !slices.Equal(got, want) {
		t.Errorf("a new clone has %v; want %v", got, want)
	}
	// And nothing was committed or pushed from the main clone.
	if a, b := e.gitOut(e.principal(w.Slug), "rev-parse", "HEAD"), e.gitOut(e.remote.Dir(), "rev-parse", "HEAD"); a != b {
		t.Errorf("the main clone is at %s, the project at %s", strings.TrimSpace(a), strings.TrimSpace(b))
	}
	if files := walkFiles(t, filepath.Join(e.principal(w.Slug), ".mestre")); slices.ContainsFunc(files, func(f string) bool { return strings.HasPrefix(f, "agents/") || strings.HasPrefix(f, "run/") }) {
		t.Errorf("execution files appeared: %v", files)
	}
	st := e.state(w.Slug)
	if st.ApprovalTest != sessionzero.ResultCounts || st.ApprovalTestSummary != "1 aprovação exigida" || !st.ApprovalTestAt.Equal(t0) {
		t.Errorf("the approval test as the panel reads it = %q (%q) at %v", st.ApprovalTest, st.ApprovalTestSummary, st.ApprovalTestAt)
	}
	if st.AgentsMD != sessionzero.AgentsMDPresent || len(e.f.requests) != 0 {
		t.Errorf("AGENTS.md = %q, proposals = %d; the project's own is never proposed over", st.AgentsMD, len(e.f.requests))
	}
}

// CA-WS-02: the workspace of a local repository has the mirror (bare, with
// the refspec) and the main clone on the main branch; it goes through apps,
// mirror, files, analysis and approval_test in this order and only then is
// active; before that a table does not open (CheckOpenTable is the gate
// `mestre table open` asks, a later stage of milestone 4).
func TestRealStagesRunInOrderAndOnlyThenTheTablesOpen(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	var refused []bool
	e.f.rules = sessionzero.Rules{Summary: "sem regras"}
	w := e.create()
	if w.State != workspaces.StatePreparing || w.Stage != workspaces.StageApps {
		t.Fatalf("created = %s/%s; want preparing/apps", w.State, w.Stage)
	}
	check := func() {
		refused = append(refused, errors.Is(e.ws.CheckOpenTable(e.ctx, w.Slug), workspaces.ErrNotActive))
	}
	check()
	if err := e.ws.Prepare(e.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if got, want := e.f.stagesSeen(), []string{"apps", "mirror", "files", "analysis", "approval_test"}; !slices.Equal(got, want) {
		t.Fatalf("the stages ran as %v; want %v", got, want)
	}
	if got := e.get(w.Slug); got.State != workspaces.StateActive || got.Stage != workspaces.StageNone || got.StageFailure != nil {
		t.Fatalf("after the stages = %s/%q; want active", got.State, got.Stage)
	}
	check()
	if want := []bool{true, false}; !slices.Equal(refused, want) {
		t.Fatalf("table open refused (before, after) = %v; want %v", refused, want)
	}
	mirror, principal := e.ws.Layout().Mirror(w.Slug), e.principal(w.Slug)
	if got := strings.TrimSpace(e.gitOut(mirror, "rev-parse", "--is-bare-repository")); got != "true" {
		t.Errorf("the mirror is not bare: %q", got)
	}
	if got := strings.TrimSpace(e.gitOut(mirror, "config", "--get", "remote.origin.fetch")); got != "+refs/heads/*:refs/remotes/origin/*" {
		t.Errorf("remote.origin.fetch = %q", got)
	}
	if got := strings.TrimSpace(e.gitOut(principal, "symbolic-ref", "--short", "HEAD")); got != "main" {
		t.Errorf("the main clone is on %q; want main", got)
	}
}

// CA-SZ-WS-02: a repository without the fixed Apps. The workspace stays
// preparing at apps, the owner gets the links once, nothing is cloned and no
// credential is asked for; installed the Apps, it goes on by itself to the
// mirror and the rest.
func TestRealMissingAppsHoldTheWorkspaceAtAppsAndItGoesOnWhenInstalled(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.f.missing = []string{sessionzero.AppGrandMaster, sessionzero.AppMestre}
	e.f.addErr = sessionzero.ErrNoPersonalCredential
	w := e.create()
	err := e.ws.Prepare(e.ctx, w.Slug)
	if !errors.Is(err, workspaces.ErrWaiting) {
		t.Fatalf("Prepare = %v; want it waiting", err)
	}
	got := e.get(w.Slug)
	if got.State != workspaces.StatePreparing || got.Stage != workspaces.StageApps || got.StageFailure != nil {
		t.Fatalf("workspace = %s/%s failure=%v; want preparing at apps, no error", got.State, got.Stage, got.StageFailure)
	}
	if reason, ok := e.ws.Waiting(w.Slug); !ok || !strings.Contains(reason, "Grão-Mestre") || !strings.Contains(reason, "Mestre") {
		t.Fatalf("waiting = %q, %v; want the Apps that are missing", reason, ok)
	}
	if len(e.f.ownerLinks) != 1 || len(e.f.ownerLinks[0]) != 2 || !strings.HasPrefix(e.f.ownerLinks[0][0].URL, "https://") {
		t.Fatalf("links sent to the owner = %+v; want one message with the two links", e.f.ownerLinks)
	}
	if e.f.addCalls != 1 {
		t.Fatalf("the API was tried %d times; want 1 (with the personal credential of the owner)", e.f.addCalls)
	}
	if _, err := os.Stat(e.ws.Layout().Mirror(w.Slug)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the mirror exists while the Apps are not installed: %v", err)
	}
	// The engine looks again: still waiting, the owner is not asked twice.
	e.clk.Advance(sessionzero.AppsPoll)
	if _, _, err := e.ws.Tick(e.ctx); err != nil {
		t.Fatal(err)
	}
	if e.get(w.Slug).Stage != workspaces.StageApps || len(e.f.ownerLinks) != 1 {
		t.Fatalf("after another look: stage %s, %d messages to the owner", e.get(w.Slug).Stage, len(e.f.ownerLinks))
	}
	if e.f.credentialUses != 0 {
		t.Fatalf("a credential was used %d times while waiting; no clone is made without the Apps", e.f.credentialUses)
	}
	e.f.install()
	e.clk.Advance(sessionzero.AppsPoll)
	if _, _, err := e.ws.Tick(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.get(w.Slug); got.State != workspaces.StateActive {
		t.Fatalf("after the install: %s/%s; want it to go on by itself to active", got.State, got.Stage)
	}
	if _, err := os.Stat(e.principal(w.Slug)); err != nil {
		t.Fatalf("the main clone: %v", err)
	}
	if e.f.credentialUses == 0 {
		t.Fatal("the mirror was cloned without the credential of the App Grão-Mestre")
	}
}

// With the credential of the owner the engine adds the repository to the
// installation by itself and does not bother the owner.
func TestRealMissingAppsAreAddedWithTheCredentialOfTheOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.f.missing = []string{sessionzero.AppMestre}
	w := e.active()
	if e.f.addCalls != 1 || len(e.f.ownerLinks) != 0 || w.State != workspaces.StateActive {
		t.Fatalf("add calls = %d, links = %d, state = %s", e.f.addCalls, len(e.f.ownerLinks), w.State)
	}
}

// CA-SZ-WS-03: a repository without AGENTS.md. The proposal reaches the users
// as an approval with the whole text, the workspace goes to active without
// waiting and no AGENTS.md exists before the approval; approved, the pull
// request mestre/sessao-zero has the file, authored by the App.
func TestRealMissingAgentsMDIsProposedAndOnlyApprovedGoesIntoThePR(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	w := e.active()
	if w.State != workspaces.StateActive {
		t.Fatalf("state = %s; the workspace must not wait for the answer", w.State)
	}
	if len(e.f.requests) != 1 || e.f.requests[0] != e.f.analysis.ProposedAgentsMD {
		t.Fatalf("approvals asked = %q; want the proposal with its whole text", e.f.requests)
	}
	if st := e.state(w.Slug); st.AgentsMD != sessionzero.AgentsMDProposed || st.AgentsMDRequest != "req-1" {
		t.Fatalf("state = %+v; want the proposal pending", st)
	}
	if e.exists(w.Slug, "AGENTS.md") || len(e.f.published) != 0 {
		t.Fatal("an AGENTS.md or a pull request exists before the approval")
	}
	if err := e.sz.AgentsMDDecided(e.ctx, w.Slug, true); err != nil {
		t.Fatal(err)
	}
	if len(e.f.published) != 1 {
		t.Fatalf("pull requests = %d; want 1", len(e.f.published))
	}
	pr := e.f.published[0]
	if pr.Branch != "mestre/sessao-zero" || pr.Base != "main" || pr.Repo.String() != "lucas/Meu_App" {
		t.Fatalf("pull request = %+v", pr)
	}
	if got := e.f.branchFiles[0]; !slices.Equal(keys(got), []string{"AGENTS.md", "README.md"}) || got["AGENTS.md"] != e.f.analysis.ProposedAgentsMD {
		t.Fatalf("the branch holds %v; want the project plus the AGENTS.md approved", keys(got))
	}
	if len(e.f.branchLog) != 1 || e.f.branchLog[0].Author.Name != "mestre-grao-mestre[bot]" {
		t.Fatalf("commits of the branch = %+v; want one by the App Grão-Mestre", e.f.branchLog)
	}
	if st := e.state(w.Slug); st.AgentsMD != sessionzero.AgentsMDApproved || st.PRURL != "https://github.example/lucas/Meu_App/pull/1" {
		t.Fatalf("state = %+v", st)
	}
	// The main clone only gets the file when the pull request is merged.
	if e.exists(w.Slug, "AGENTS.md") {
		t.Fatal("the AGENTS.md is in the main clone before the merge")
	}
	if tmp := filepath.Join(e.ws.Layout().Dir(w.Slug), "sessao-zero-pr"); dirExists(tmp) {
		t.Fatalf("the worktree of the commit was left in %s", tmp)
	}
}

// A refused proposal opens no pull request and is not proposed again.
func TestRealRefusedAgentsMDOpensNothing(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	w := e.active()
	if err := e.sz.AgentsMDDecided(e.ctx, w.Slug, false); err != nil {
		t.Fatal(err)
	}
	if st := e.state(w.Slug); st.AgentsMD != sessionzero.AgentsMDRejected || len(e.f.published) != 0 {
		t.Fatalf("state = %+v, pull requests = %d", st, len(e.f.published))
	}
	if err := e.sz.AgentsMDDecided(e.ctx, w.Slug, true); err == nil {
		t.Fatal("a decision with no proposal waiting was accepted")
	}
}

// CA-SZ-WS-04: a repository with CLAUDE.md and AGENTS.md. The users get the
// warning that the Claude agents read another file, the checklist shows the
// item and the AGENTS.md is neither proposed nor touched.
func TestRealClaudeMDWarnsTheUsersAndTheChecklistShowsIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t, map[string]string{"AGENTS.md": projectAgents, "CLAUDE.md": "# Claude\n"})
	w := e.active()
	if !slices.Contains(e.f.noticeKinds(), "claude_md") {
		t.Fatalf("notices = %v; want the warning about CLAUDE.md", e.f.noticeKinds())
	}
	var text string
	for _, n := range e.f.notices {
		if n.Kind == "claude_md" {
			text = n.Text
		}
	}
	if !strings.Contains(text, "CLAUDE.md") || !strings.Contains(text, "AGENTS.md") || !strings.Contains(text, "Claude") {
		t.Errorf("the warning = %q", text)
	}
	if len(e.f.checklist) != 1 || e.f.checklist[0].ID != "ALL-14" || e.f.checklist[0].Workspace != w.Slug {
		t.Fatalf("checklist = %+v; want the item ALL-14", e.f.checklist)
	}
	if got := e.readPrincipal(w.Slug, "AGENTS.md"); string(got) != projectAgents || len(e.f.requests) != 0 {
		t.Fatalf("AGENTS.md = %q, proposals = %d", got, len(e.f.requests))
	}
}

// A CLAUDE.local.md is local to the person and not versioned: the warning
// comes from the folder too.
func TestRealClaudeLocalMDWarnsToo(t *testing.T) {
	t.Parallel()
	e := newEnv(t, map[string]string{"AGENTS.md": projectAgents})
	e.f.analyzeErr = errors.New("o Grão-Mestre ainda não respondeu")
	w := e.create()
	if err := e.ws.Prepare(e.ctx, w.Slug); err == nil {
		t.Fatal("Prepare = nil; the analysis failed")
	}
	if len(e.f.checklist) != 0 {
		t.Fatalf("checklist = %+v before the file exists", e.f.checklist)
	}
	if err := os.WriteFile(filepath.Join(e.principal(w.Slug), "CLAUDE.local.md"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.f.analyzeErr = nil
	e.clk.Advance(workspaces.BackoffMax)
	if _, _, err := e.ws.Tick(e.ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(e.f.noticeKinds(), "claude_md") || len(e.f.checklist) != 1 {
		t.Fatalf("notices = %v, checklist = %+v", e.f.noticeKinds(), e.f.checklist)
	}
}

// CA-SZ-WS-05: the answer "only local". .git/info/exclude has /.mestre/ in
// the block of the Mestre and the .gitignore did not change. Before the
// answer .mestre/ is already local.
func TestRealOnlyLocalExcludesMestreAndLeavesGitignoreAlone(t *testing.T) {
	t.Parallel()
	const gitignore = "/build/\n*.log\n"
	e := newEnv(t, map[string]string{".gitignore": gitignore})
	w := e.active()
	if e.f.versioning != 1 {
		t.Fatalf("the users were asked about versioning %d times; want once", e.f.versioning)
	}
	if st := e.state(w.Slug); st.Versioning != sessionzero.VersioningUnset || st.VersioningAskedAt.IsZero() {
		t.Fatalf("before the answer = %+v; want a pending question", st)
	}
	if !blockHas(e.excludeLines(w.Slug), "/.mestre/") {
		t.Fatal("until the answer .mestre/ is not local")
	}
	if err := e.sz.AnswerVersioning(e.ctx, w.Slug, sessionzero.VersioningLocal); err != nil {
		t.Fatal(err)
	}
	lines := e.excludeLines(w.Slug)
	if !blockHas(lines, "/.mestre/") {
		t.Fatalf("info/exclude = %q; want /.mestre/ inside the block of the Mestre", lines)
	}
	if got := e.readPrincipal(w.Slug, ".gitignore"); string(got) != gitignore {
		t.Fatalf(".gitignore = %q; want it unchanged", got)
	}
	if st := e.state(w.Slug); st.Versioning != sessionzero.VersioningLocal || len(e.f.published) != 0 {
		t.Fatalf("state = %+v, pull requests = %d", st, len(e.f.published))
	}
}

// Versioning .mestre/: the block now excludes only the execution files and the
// content goes into the pull request of the session zero, without them.
func TestRealVersionedMestreGoesIntoThePRWithoutTheExecutionFiles(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	w := e.active()
	run := filepath.Join(e.principal(w.Slug), ".mestre", "run", "estado.json")
	if err := os.WriteFile(run, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.sz.AnswerVersioning(e.ctx, w.Slug, sessionzero.VersioningVersioned); err != nil {
		t.Fatal(err)
	}
	if blockHas(e.excludeLines(w.Slug), "/.mestre/") || !blockHas(e.excludeLines(w.Slug), "/.mestre/run/") {
		t.Fatalf("info/exclude = %q; want only the execution files excluded", e.excludeLines(w.Slug))
	}
	if len(e.f.published) != 1 {
		t.Fatalf("pull requests = %d; want 1", len(e.f.published))
	}
	got := e.f.branchFiles[0]
	for _, want := range []string{".mestre/regras/teto.yaml", ".mestre/regras/politica-de-aprovacao.yaml", ".mestre/modelos/VERSAO", "README.md"} {
		if _, ok := got[want]; !ok {
			t.Errorf("the branch lacks %s: %v", want, keys(got))
		}
	}
	for p := range got {
		if strings.HasPrefix(p, ".mestre/run/") || strings.HasPrefix(p, ".mestre/agents/") || strings.HasPrefix(p, ".mestre/anexos/") || strings.HasPrefix(p, ".agents/") || strings.HasPrefix(p, ".claude/") {
			t.Errorf("the branch has %s, a file that never goes into git", p)
		}
	}
	// Going back to local deletes nothing in the project and sends nothing.
	if err := e.sz.AnswerVersioning(e.ctx, w.Slug, sessionzero.VersioningLocal); err != nil {
		t.Fatal(err)
	}
	if len(e.f.published) != 1 || !blockHas(e.excludeLines(w.Slug), "/.mestre/") {
		t.Fatalf("after going back to local: pull requests = %d, info/exclude = %q", len(e.f.published), e.excludeLines(w.Slug))
	}
}

// When the AGENTS.md and the versioned .mestre/ are both ready they go in a
// single pull request.
func TestRealAgentsMDAndMestreShareOnePullRequest(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	w := e.active()
	if err := e.sz.AnswerVersioning(e.ctx, w.Slug, sessionzero.VersioningVersioned); err != nil {
		t.Fatal(err)
	}
	if err := e.sz.AgentsMDDecided(e.ctx, w.Slug, true); err != nil {
		t.Fatal(err)
	}
	if len(e.f.published) != 2 {
		t.Fatalf("publications = %d", len(e.f.published))
	}
	last := e.f.branchFiles[1]
	if _, ok := last["AGENTS.md"]; !ok {
		t.Fatalf("the last version of the branch lacks AGENTS.md: %v", keys(last))
	}
	if _, ok := last[".mestre/regras/teto.yaml"]; !ok {
		t.Fatalf("the last version of the branch lacks .mestre/: %v", keys(last))
	}
	if len(e.f.branchLog) != 1 {
		t.Fatalf("the branch has %d commits over the main branch; want the single one the engine rebuilds", len(e.f.branchLog))
	}
}

// CA-SZ-WS-06: a fake GitHub where the review of the App does not satisfy the
// rule. The result nao_conta is recorded, the stage ends and the workspace is
// active; the test pull request is closed.
func TestRealAppReviewThatDoesNotCountIsRecordedAndTheWorkspaceGoesActive(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.f.rules = sessionzero.Rules{RequiresReview: true, Summary: "2 aprovações exigidas, code owners"}
	e.f.decision = "REVIEW_REQUIRED"
	w := e.active()
	if w.State != workspaces.StateActive {
		t.Fatalf("state = %s/%s; want active", w.State, w.Stage)
	}
	st := e.state(w.Slug)
	if st.ApprovalTest != sessionzero.ResultDoesNotCount || st.ApprovalTestSummary != "2 aprovações exigidas, code owners" {
		t.Fatalf("result = %q (%q); want nao_conta with the rules read", st.ApprovalTest, st.ApprovalTestSummary)
	}
	if e.f.opened != 1 || e.f.approvals != 1 || e.f.closed != 1 {
		t.Fatalf("test pull request opened %d, approved %d, closed %d times", e.f.opened, e.f.approvals, e.f.closed)
	}
}

// With no rule requiring a review the test ends at once with sem_exigencia and
// opens nothing in the project.
func TestRealNoRequiredReviewOpensNoTestPR(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	w := e.active()
	if st := e.state(w.Slug); st.ApprovalTest != sessionzero.ResultNoRequirement || e.f.opened != 0 {
		t.Fatalf("result = %q, test pull requests = %d", st.ApprovalTest, e.f.opened)
	}
}

// A step that fails halfway still closes the test pull request, the stage
// fails with its code and reason (the workspace stays preparing) and the
// engine tries it again.
func TestRealApprovalTestFailureCleansUpAndIsRetried(t *testing.T) {
	t.Parallel()
	for _, at := range []string{"open", "approve", "decision"} {
		t.Run(at, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t, nil)
			e.f.rules = sessionzero.Rules{RequiresReview: true, Summary: "1 aprovação"}
			e.f.failAt = at
			w := e.create()
			if err := e.ws.Prepare(e.ctx, w.Slug); err == nil {
				t.Fatal("Prepare = nil; the step failed")
			}
			got := e.get(w.Slug)
			if got.State != workspaces.StatePreparing || got.Stage != workspaces.StageApprovalTest || got.StageFailure == nil || got.StageFailure.Code != "approval_test_failed" {
				t.Fatalf("workspace = %s/%s failure=%+v", got.State, got.Stage, got.StageFailure)
			}
			if e.f.closed != 1 {
				t.Fatalf("the test pull request was closed %d times; want once, whatever failed", e.f.closed)
			}
			if st := e.state(w.Slug); st.ApprovalTest != "" {
				t.Fatalf("a result was recorded: %q", st.ApprovalTest)
			}
			e.f.failAt = ""
			e.clk.Advance(workspaces.BackoffMax)
			if _, _, err := e.ws.Tick(e.ctx); err != nil {
				t.Fatal(err)
			}
			if got := e.get(w.Slug); got.State != workspaces.StateActive || e.state(w.Slug).ApprovalTest != sessionzero.ResultCounts {
				t.Fatalf("after the retry: %s/%s, result %q", got.State, got.Stage, e.state(w.Slug).ApprovalTest)
			}
		})
	}
}

// The engine runs the test again on demand (a merge approved by the App that
// the repository refused).
func TestRealApprovalTestCanBeRunAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.f.rules = sessionzero.Rules{RequiresReview: true, Summary: "1"}
	w := e.active()
	e.f.decision = "REVIEW_REQUIRED"
	if err := e.sz.RunApprovalTest(e.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if st := e.state(w.Slug); st.ApprovalTest != sessionzero.ResultDoesNotCount || e.f.opened != 2 || e.f.closed != 2 {
		t.Fatalf("result = %q, opened %d, closed %d", st.ApprovalTest, e.f.opened, e.f.closed)
	}
}

// A stage that failed is tried again and what the earlier stages did is not
// repeated: the Grand Master started once, the users were asked about
// versioning once and the AGENTS.md is proposed once.
func TestRealRetriedAnalysisDoesNotRepeatTheEarlierStages(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.f.analyzeErr = errors.New("o Grão-Mestre não respondeu")
	w := e.create()
	if err := e.ws.Prepare(e.ctx, w.Slug); err == nil {
		t.Fatal("Prepare = nil")
	}
	if got := e.get(w.Slug); got.Stage != workspaces.StageAnalysis || got.StageFailure == nil || got.StageFailure.Code != "analysis_failed" {
		t.Fatalf("workspace = %s failure=%+v", got.Stage, got.StageFailure)
	}
	if e.f.versioning != 1 || e.f.started != 1 {
		t.Fatalf("files stage effects: asked %d, started %d", e.f.versioning, e.f.started)
	}
	e.f.analyzeErr = nil
	e.clk.Advance(workspaces.BackoffMax)
	if _, _, err := e.ws.Tick(e.ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.get(w.Slug); got.State != workspaces.StateActive || len(e.f.requests) != 1 {
		t.Fatalf("after the retry: %s, proposals = %d", got.State, len(e.f.requests))
	}
}

func blockHas(lines []string, pattern string) bool {
	in := false
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "# >>> mestre:"):
			in = true
		case strings.HasPrefix(l, "# <<< mestre:"):
			in = false
		case in && l == pattern:
			return true
		}
	}
	return false
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
