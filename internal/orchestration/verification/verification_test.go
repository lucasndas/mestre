package verification_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/verification"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

const goodDoc = "version: 1\ncomandos: [\"go build ./...\", \"go test ./...\"]\ntimeout: 5m\n"

func TestTheDocumentIsCheckedBeforeItIsKept(t *testing.T) {
	t.Parallel()
	d, err := verification.Parse([]byte(goodDoc))
	if err != nil || len(d.Commands) != 2 || d.Timeout != 5*time.Minute {
		t.Fatalf("Parse = %+v, %v", d, err)
	}
	if d, err := verification.Parse([]byte("version: 1\ncomandos: [\"make\"]\n")); err != nil || d.Timeout != verification.DefaultTimeout {
		t.Errorf("default timeout = %+v, %v", d, err)
	}
	long := strings.Repeat("a", verification.MaxCommandLen+1)
	many := "version: 1\ncomandos: [" + strings.Repeat(`"x",`, verification.MaxCommands+1) + "]\n"
	for name, doc := range map[string]string{
		"empty":           "",
		"no version":      "comandos: [\"make\"]\n",
		"other version":   "version: 2\ncomandos: [\"make\"]\n",
		"no command":      "version: 1\ncomandos: []\n",
		"blank command":   "version: 1\ncomandos: [\"  \"]\n",
		"line break":      "version: 1\ncomandos: [\"make\\nrm -rf /\"]\n",
		"control char":    "version: 1\ncomandos: [\"make\\u0007\"]\n",
		"long command":    "version: 1\ncomandos: [\"" + long + "\"]\n",
		"too many":        many,
		"unknown field":   "version: 1\ncomandos: [\"make\"]\nextra: 1\n",
		"timeout too big": "version: 1\ncomandos: [\"make\"]\ntimeout: 3h\n",
		"timeout zero":    "version: 1\ncomandos: [\"make\"]\ntimeout: 0s\n",
		"not yaml":        "{{{",
	} {
		if _, err := verification.Parse([]byte(doc)); !errors.Is(err, verification.ErrInvalid) {
			t.Errorf("%s: %v, want an invalid document", name, err)
		}
	}
}

func newStore(t *testing.T) verification.Store {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(t.Context(), filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return verification.Store{DB: db, Clock: clk}
}

func TestOnlyTheConfirmedVersionCountsAndAnInvalidOneIsNeverKept(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	if _, ok, err := s.Confirmed(t.Context(), "loja"); err != nil || ok {
		t.Fatalf("before any confirmation: %v, %v", ok, err)
	}
	if _, err := s.Confirm(t.Context(), "loja", "u1", []byte("version: 1\ncomandos: []\n")); !errors.Is(err, verification.ErrInvalid) {
		t.Fatalf("an invalid document was confirmed: %v", err)
	}
	if _, err := s.Confirm(t.Context(), "loja", "", []byte(goodDoc)); err == nil {
		t.Error("a confirmation by nobody was kept")
	}
	if _, ok, _ := s.Confirmed(t.Context(), "loja"); ok {
		t.Fatal("something was kept")
	}
	h1, err := s.Confirm(t.Context(), "loja", "u1", []byte(goodDoc))
	if err != nil || len(h1) != 64 {
		t.Fatalf("Confirm = %q, %v", h1, err)
	}
	h2, err := s.Confirm(t.Context(), "loja", "u2", []byte("version: 1\ncomandos: [\"make\"]\n"))
	if err != nil || h2 == h1 {
		t.Fatalf("the second confirmation = %q, %v", h2, err)
	}
	d, ok, err := s.Confirmed(t.Context(), "loja")
	if err != nil || !ok || len(d.Commands) != 1 || d.Commands[0] != "make" {
		t.Fatalf("the confirmed version = %+v, %v, %v", d, ok, err)
	}
	if _, ok, _ := s.Confirmed(t.Context(), "blog"); ok {
		t.Error("the commands of one workspace are those of another")
	}
}

type fakeWorktrees struct {
	dir     string
	added   int
	removed int
	err     error
	asked   []verification.TreeRequest
}

func (f *fakeWorktrees) Add(_ context.Context, r verification.TreeRequest) (verification.Tree, func(), error) {
	f.asked = append(f.asked, r)
	if f.err != nil {
		return verification.Tree{}, nil, f.err
	}
	f.added++
	return verification.Tree{Dir: f.dir, Home: f.dir + ".home"}, func() { f.removed++ }, nil
}

type step struct {
	res Result
	err error
}

type Result = verification.Result

type fakeRunner struct {
	steps []step
	ran   []verification.Run
}

func (f *fakeRunner) Run(_ context.Context, r verification.Run) (verification.Result, error) {
	f.ran = append(f.ran, r)
	s := f.steps[len(f.ran)-1]
	return s.res, s.err
}

func newExecutor(t *testing.T, doc string, steps ...step) (*verification.Executor, *fakeRunner, *fakeWorktrees) {
	t.Helper()
	s := newStore(t)
	if doc != "" {
		if _, err := s.Confirm(t.Context(), "loja", "u1", []byte(doc)); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"src/a.go", "src/b.go", "src/c.go", "src/d.go"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fw, fr := &fakeWorktrees{dir: dir}, &fakeRunner{steps: steps}
	return &verification.Executor{Store: s, Worktrees: fw, Runner: fr, Mask: func(s string) string { return strings.ReplaceAll(s, "SEGREDO", "***") }}, fr, fw
}

var req = quests.VerifyRequest{Workspace: "loja", Table: "42-login", Branch: "epico/42-login"}

func TestAGreenVerificationRunsEveryCommandInTheWorktreeAndRemovesIt(t *testing.T) {
	t.Parallel()
	e, fr, fw := newExecutor(t, goodDoc, step{}, step{})
	if ok, err := e.Confirmed(t.Context(), "loja"); err != nil || !ok {
		t.Fatalf("Confirmed = %v, %v", ok, err)
	}
	res, err := e.Verify(t.Context(), req)
	if err != nil || !res.OK {
		t.Fatalf("Verify = %+v, %v", res, err)
	}
	if len(fr.ran) != 2 || fr.ran[0].Command != "go build ./..." || fr.ran[1].Command != "go test ./..." || fr.ran[0].Dir != fw.dir || fr.ran[0].Timeout != 5*time.Minute {
		t.Errorf("ran %+v", fr.ran)
	}
	if fw.added != 1 || fw.removed != 1 {
		t.Errorf("worktree added %d, removed %d", fw.added, fw.removed)
	}
}

func TestARedVerificationStopsAtTheFirstFailureWithTheEndOfTheOutputMaskedAndThePlaces(t *testing.T) {
	t.Parallel()
	out := strings.Repeat("ruído\n", 2000) + "src/a.go:12: erro SEGREDO\nsrc/b.go:7: outro\n../fora.go:3: não\nsrc/falta.go:1: não existe\nsrc/a.go:12: de novo\n"
	e, fr, fw := newExecutor(t, goodDoc, step{res: verification.Result{ExitCode: 1, Output: out}})
	res, err := e.Verify(t.Context(), req)
	if err != nil || res.OK || res.Command != "go build ./..." || res.TimedOut {
		t.Fatalf("Verify = %+v, %v", res, err)
	}
	if len(res.Output) > quests.MaxVerifyOutput || !strings.HasSuffix(res.Output, "de novo\n") || strings.Contains(res.Output, "SEGREDO") {
		t.Errorf("output = %d bytes, %q", len(res.Output), res.Output[len(res.Output)-80:])
	}
	if got := strings.Join(res.Places, ","); got != "src/a.go:12,src/b.go:7" {
		t.Errorf("places = %q, want only files of the worktree, sorted, once", got)
	}
	if len(fr.ran) != 1 || fw.removed != 1 {
		t.Errorf("ran %d commands, removed %d", len(fr.ran), fw.removed)
	}
}

func TestACommandThatRunsOutOfTimeIsRed(t *testing.T) {
	t.Parallel()
	e, _, _ := newExecutor(t, goodDoc, step{res: verification.Result{ExitCode: 0, TimedOut: true, Output: "parado"}})
	res, err := e.Verify(t.Context(), req)
	if err != nil || res.OK || !res.TimedOut {
		t.Fatalf("Verify = %+v, %v", res, err)
	}
}

// What cannot be verified is not verified: no confirmed commands, nothing to run
// them with, a worktree that cannot be made or a command that cannot start are
// never a green.
func TestWhatCannotRunIsNeverGreen(t *testing.T) {
	t.Parallel()
	e, _, _ := newExecutor(t, "")
	if ok, err := e.Confirmed(t.Context(), "loja"); err != nil || ok {
		t.Errorf("no confirmed commands: %v, %v", ok, err)
	}
	if res, err := e.Verify(t.Context(), req); err == nil || res.OK {
		t.Errorf("Verify with none confirmed = %+v, %v", res, err)
	}
	e, _, _ = newExecutor(t, goodDoc)
	e.Runner = nil
	if ok, _ := e.Confirmed(t.Context(), "loja"); ok {
		t.Error("confirmed with nothing to run the commands")
	}
	if res, err := e.Verify(t.Context(), req); err == nil || res.OK {
		t.Errorf("Verify with no runner = %+v, %v", res, err)
	}
	e, _, fw := newExecutor(t, goodDoc)
	fw.err = errors.New("no git")
	if res, err := e.Verify(t.Context(), req); err == nil || res.OK {
		t.Errorf("Verify with no worktree = %+v, %v", res, err)
	}
	e, _, fw = newExecutor(t, goodDoc, step{err: errors.New("no terminal")})
	if res, err := e.Verify(t.Context(), req); err == nil || res.OK || fw.removed != 1 {
		t.Errorf("Verify with a command that cannot start = %+v, %v (removed %d)", res, err, fw.removed)
	}
}

// The mask runs on the whole output before the end is kept, so a secret that the
// cut would have split is still hidden.
func TestTheSecretAtTheCutOfTheOutputIsHidden(t *testing.T) {
	t.Parallel()
	e, _, _ := newExecutor(t, goodDoc, step{res: verification.Result{ExitCode: 1, Output: strings.Repeat("x", quests.MaxVerifyOutput-3) + "SEGREDO fim"}})
	res, err := e.Verify(t.Context(), req)
	if err != nil || res.OK {
		t.Fatal(res, err)
	}
	if strings.Contains(res.Output, "GREDO") || strings.Contains(res.Output, "SEGR") {
		t.Errorf("a piece of the secret reached the character: %q", res.Output[len(res.Output)-30:])
	}
}

// The request carries the commit to prove and the base to integrate to the
// worktree, and the commands run with the scratch folder of that worktree.
func TestTheHeadAndTheBaseOfTheRequestReachTheWorktree(t *testing.T) {
	t.Parallel()
	e, fr, fw := newExecutor(t, goodDoc, step{}, step{})
	r := req
	r.Head, r.Base = "aaaa", "bbbb"
	if res, err := e.Verify(t.Context(), r); err != nil || !res.OK {
		t.Fatalf("Verify = %+v, %v", res, err)
	}
	if len(fw.asked) != 1 || fw.asked[0] != (verification.TreeRequest{Workspace: "loja", Table: "42-login", Branch: "epico/42-login", Head: "aaaa", Base: "bbbb"}) {
		t.Errorf("worktree asked: %+v", fw.asked)
	}
	if fr.ran[0].Home != fw.dir+".home" {
		t.Errorf("the commands ran with the home %q", fr.ran[0].Home)
	}
}

// A branch that moved, or a base that does not merge, is a red result with no
// command run: the commands are never run on something else than what the gate
// asked to prove, and what could not be put together is never green.
func TestABranchThatMovedAndABaseThatDoesNotMergeAreRedWithoutRunningAnything(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		err     error
		command string
	}{
		"head moved":    {fmt.Errorf("%w: epico at 1234, not at 5678", verification.ErrHeadMoved), "integração: o commit a provar"},
		"base conflict": {fmt.Errorf("%w: abc into epico: conflict SEGREDO", verification.ErrBaseConflict), "integração: a base na branch"},
	} {
		e, fr, fw := newExecutor(t, goodDoc)
		fw.err = c.err
		res, err := e.Verify(t.Context(), req)
		if err != nil || res.OK || res.TimedOut || res.Command != c.command || res.Output == "" || strings.Contains(res.Output, "SEGREDO") {
			t.Errorf("%s: Verify = %+v, %v", name, res, err)
		}
		if len(fr.ran) != 0 {
			t.Errorf("%s: ran %+v", name, fr.ran)
		}
	}
}

// The whole of a verification has a bound, whatever the commands add up to.
func TestAWholeVerificationHasATimeBound(t *testing.T) {
	t.Parallel()
	e, _, _ := newExecutor(t, goodDoc)
	var deadline time.Time
	e.Runner = runnerFunc(func(ctx context.Context, _ verification.Run) (verification.Result, error) {
		deadline, _ = ctx.Deadline()
		return verification.Result{}, nil
	})
	if res, err := e.Verify(t.Context(), req); err != nil || !res.OK {
		t.Fatal(res, err)
	}
	if want := e.Store.Clock.Now().Add(verification.MaxTotal); !deadline.Equal(want) {
		t.Errorf("deadline = %v, want %v", deadline, want)
	}
}

type runnerFunc func(context.Context, verification.Run) (verification.Result, error)

func (f runnerFunc) Run(ctx context.Context, r verification.Run) (verification.Result, error) {
	return f(ctx, r)
}

// The limits agree: a command is bounded by the time of the whole, and the whole
// by a little less than the bound the engine of the quests gives a verification,
// so the executor is the one to give up (red), never the engine of the quests.
func TestTheTimeLimitsAgree(t *testing.T) {
	t.Parallel()
	if verification.MaxTimeout > verification.MaxTotal || verification.MaxTotal >= quests.VerifyTimeout {
		t.Errorf("per command %s, whole %s, bound of the quests %s", verification.MaxTimeout, verification.MaxTotal, quests.VerifyTimeout)
	}
}

// Escapes that are not closed do not swallow the output, and what a terminal can
// put inside a secret (a lone ESC, overstrike, C1) does not keep it from the mask.
func TestOddControlsDoNotHideOutputOrASecret(t *testing.T) {
	t.Parallel()
	out := "a\x1b]0;aberto sem fim\nSEG\x1bREDO e SE\rGR\u009bED\x08O\nfim\n"
	e, _, _ := newExecutor(t, goodDoc, step{res: verification.Result{ExitCode: 1, Output: out}})
	res, err := e.Verify(t.Context(), req)
	if err != nil || res.OK {
		t.Fatal(res, err)
	}
	if !strings.Contains(res.Output, "fim") || strings.Contains(res.Output, "SEGREDO") || strings.ContainsAny(res.Output, "\x1b\r\x08") {
		t.Errorf("output = %q", res.Output)
	}
}

// A suite that runs out of the time of the whole is red, not an error to try again.
func TestTheTimeOfTheWholeRunningOutIsRed(t *testing.T) {
	t.Parallel()
	e, _, _ := newExecutor(t, goodDoc)
	e.Runner = runnerFunc(func(ctx context.Context, _ verification.Run) (verification.Result, error) {
		clk := e.Store.Clock.(*clock.Fake)
		clk.Advance(verification.MaxTotal + time.Second)
		<-ctx.Done()
		return verification.Result{}, ctx.Err()
	})
	res, err := e.Verify(t.Context(), req)
	if err != nil || res.OK || !res.TimedOut {
		t.Fatalf("Verify = %+v, %v", res, err)
	}
}

// A secret that the terminal broke with escapes is still hidden, and the output
// that reaches the character has no escapes.
func TestASecretSplitByEscapesIsHiddenAndTheOutputHasNoEscapes(t *testing.T) {
	t.Parallel()
	out := "\x1b[31merro\x1b[0m SEG\x1b[1mRE\x1b]0;titulo\x07DO e \x1b[2Ksrc/a.go:12: x\n"
	e, _, _ := newExecutor(t, goodDoc, step{res: verification.Result{ExitCode: 1, Output: out}})
	res, err := e.Verify(t.Context(), req)
	if err != nil || res.OK {
		t.Fatal(res, err)
	}
	if strings.Contains(res.Output, "SEGREDO") || strings.Contains(res.Output, "\x1b") || strings.Contains(res.Output, "titulo") || !strings.Contains(res.Output, "erro *** e src/a.go:12: x") {
		t.Errorf("output = %q", res.Output)
	}
	if got := strings.Join(res.Places, ","); got != "src/a.go:12" {
		t.Errorf("places = %q", got)
	}
}

// CA-SZ-WS-08: the stage analysis leaves a proposal that does not hold before an
// Administrator confirms it, and a file with content is never overwritten.
func TestTheProposalIsWrittenOnceAndHoldsNothingUntilConfirmed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var told []string
	k := verification.Keeper{Notify: func(_ context.Context, _, text string) error { told = append(told, text); return nil }}
	if err := k.Propose(context.Background(), "ws", dir, []string{"go build ./...", "go test ./..."}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, filepath.FromSlash(verification.File))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verification.Parse(b); err != nil || !strings.Contains(string(b), "go test ./...") {
		t.Fatalf("proposal %q: %v", b, err)
	}
	if len(told) != 1 {
		t.Fatalf("Administrators told %d times", len(told))
	}
	if err := k.Propose(context.Background(), "ws", dir, []string{"make"}); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(path); string(again) != string(b) || len(told) != 1 {
		t.Fatal("a second run rewrote or told again")
	}
}
