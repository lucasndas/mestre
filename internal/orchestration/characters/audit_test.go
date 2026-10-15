package characters_test

import (
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// A character whose terminal survives the restart of the engine before it
// was ready still gets its Mestre layer as the first message, and the session
// zero gets back the request it was prepared with (Refresh and UpdateIdentity
// work after a restart).
func TestRestartBeforeReadyKeepsTheLayerAndRestoresTheSessionZero(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{first: "THE LAYER"})
	r.makeTM("3-login")
	c := r.make("Dev Claude", "Dev", "3-login")
	r.restart(c.Machine, []characters.Character{c}, nil)
	rep, err := r.mach[c.Machine].rt.Recover(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(rep.Reconnected, c.ID) {
		t.Fatalf("recover = %+v", rep)
	}
	prep := r.mach[c.Machine].prep
	prep.mu.Lock()
	var restored []string
	for _, q := range prep.restored {
		restored = append(restored, q.ID)
	}
	prep.mu.Unlock()
	if !slices.Contains(restored, c.ID) {
		t.Fatalf("the session zero was not given back the request of %s: %v", c.ID, restored)
	}
	before := len(r.machineOf(c).host.proc(c.ID).typedAll())
	r.hook(c, providers.EventSessionStart)
	typed := r.machineOf(c).host.proc(c.ID).typedAll()
	if len(typed) != before+1 || typed[before] != paste("THE LAYER") {
		t.Fatalf("typed after the restart = %q, want the layer once", typed)
	}
}

// Two resumes of one interrupted character at once start one process: the
// one that does not win the transition leaves it alone.
func TestConcurrentResumeStartsOneProcess(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	r.makeTM("3-login")
	c := r.make("Dev Persistente", "Dev", "3-login")
	r.ready(c)
	mr := r.mach[c.Machine]
	mr.host.emit(characters.HostEvent{Terminal: c.ID, Kind: characters.HostExit, ExitCode: 137})
	if r.state(c) != characters.Interrupted {
		t.Fatalf("not interrupted: %s", r.state(c))
	}
	mr.host.mu.Lock()
	before := len(mr.host.order)
	mr.host.mu.Unlock()
	inVerify, release := make(chan struct{}), make(chan struct{})
	mr.prep.mu.Lock()
	mr.prep.verify = func() error {
		close(inVerify)
		<-release
		return nil
	}
	mr.prep.mu.Unlock()
	first := make(chan error, 1)
	go func() { first <- r.svc.ResumeMachine(r.ctx, c.Machine) }()
	<-inVerify
	if err := r.svc.ResumeMachine(r.ctx, c.Machine); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	mr.host.mu.Lock()
	n := len(mr.host.order)
	mr.host.mu.Unlock()
	if n != before+1 {
		t.Fatalf("%d processes after, %d before: want exactly one restart", n, before)
	}
}

// Two Starts of the same character never both run: the second is refused
// while the first still prepares, and what the first wrote stays.
func TestConcurrentStartOfTheSameCharacterRunsOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	r.makeTM("3-login")
	c := r.make("Dev Persistente", "Dev", "3-login")
	r.ready(c)
	mr := r.mach[c.Machine]
	mr.host.emit(characters.HostEvent{Terminal: c.ID, Kind: characters.HostExit, ExitCode: 137})
	in := r.builder.input(c.ID)
	mr.host.mu.Lock()
	before := len(mr.host.order)
	mr.host.mu.Unlock()
	inVerify, release := make(chan struct{}), make(chan struct{})
	mr.prep.mu.Lock()
	mr.prep.verify = func() error {
		close(inVerify)
		<-release
		return nil
	}
	mr.prep.mu.Unlock()
	cmd := func(term string) characters.StartCommand {
		return characters.StartCommand{Character: c.ID, Terminal: term, Kind: c.Kind, Resumed: true,
			Request: sessionzero.CharacterRequest{ID: c.ID, Name: c.Name, Role: c.Role, Workspace: c.Workspace, Dir: in.Dir,
				Table: sessionzero.TableContext{ID: c.Table}, Snapshot: in.Snapshot, ResumeSessionID: "s"}}
	}
	done := make(chan error, 1)
	go func() { _, err := mr.rt.Start(r.ctx, cmd(c.ID+"-2")); done <- err }()
	<-inVerify
	if _, err := mr.rt.Start(r.ctx, cmd(c.ID+"-3")); err == nil {
		t.Fatal("a second Start of the same character was accepted")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mr.host.mu.Lock()
	n := len(mr.host.order)
	mr.host.mu.Unlock()
	if n != before+1 {
		t.Fatalf("%d processes after, %d before: want exactly one restart", n, before)
	}
	if ended := mr.prep.endedOf(c.ID); len(ended) != 0 {
		t.Fatalf("a refused Start ended the files of the running one: %+v", ended)
	}
}

// An event whose caller was not identified as the character it names moves
// nothing: not its state, not its session id.
func TestUnidentifiedHookEventMovesNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	r.makeTM("3-login")
	c := r.make("Dev Persistente", "Dev", "3-login")
	r.ready(c)
	ev := event(c, providers.EventTurnStart)
	ev.SessionID = "forged-session"
	r.machineOf(c).rt.OnHook(ev, hook.Answer{Verdict: hook.VerdictDeny, Unidentified: true})
	if got := r.state(c); got != characters.Idle {
		t.Fatalf("state = %s, want idle", got)
	}
	if got := r.get(c.ID).ProviderSessionID; got == "forged-session" {
		t.Fatalf("session id overwritten by an unidentified event")
	}
}

// A session id that is not one (it would be an option of the resume command)
// is never kept, from the hook or from a report.
func TestInvalidProviderSessionIDIsNotKept(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	r.makeTM("3-login")
	c := r.make("Dev Persistente", "Dev", "3-login")
	r.ready(c)
	want := r.get(c.ID).ProviderSessionID
	ev := event(c, providers.EventTurnStart)
	ev.SessionID = "--dangerously-skip-permissions"
	r.machineOf(c).rt.OnHook(ev, hook.Answer{})
	if got := r.get(c.ID).ProviderSessionID; got != want {
		t.Fatalf("session id after the hook = %q, want %q", got, want)
	}
	err := r.svc.Report(r.ctx, characters.Report{Character: c.ID, State: characters.Busy, Reason: "turn", Session: "--yolo"})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.get(c.ID).ProviderSessionID; got != want {
		t.Fatalf("session id after the report = %q, want %q", got, want)
	}
}
