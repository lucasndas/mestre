package characters_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
)

const (
	claudeBusy = "✻ Working… (esc to interrupt)\n\n──────────────────────────────\n❯ \n──────────────────────────────\n  ? for shortcuts"
	// unknownScreen is a screen of no adapter: it waits for an answer.
	unknownScreen = "A new dialog nobody knows\n\nPress Enter to continue"
)

// paste is what the adapter of Claude Code types for a message.
func paste(text string) string { return "\x1b[200~" + text + "\x1b[201~\r" }

func (r *rig) stable(c characters.Character, screen string, row int) {
	r.t.Helper()
	m := r.machineOf(c)
	m.host.proc(c.ID).setScreen(screen, row)
	m.host.emit(characters.HostEvent{Terminal: m.host.proc(c.ID).id, Kind: characters.HostStable})
}

func TestTheMestreLayerAsTheFirstMessageIsTypedOnceWhenTheTerminalIsReady(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{first: "THE LAYER"})
	c := r.make("Dev Claude", "Dev", "3-login")
	proc := r.mach[c.Machine].host.proc(c.ID)
	if got := proc.typedAll(); len(got) != 0 {
		t.Fatalf("typed before it was ready: %q", got)
	}
	r.hook(c, providers.EventSessionStart)
	if got := proc.typedAll(); len(got) != 1 || got[0] != paste("THE LAYER") {
		t.Fatalf("typed = %q, want the layer once", got)
	}
	if r.state(c) != characters.Busy {
		t.Errorf("state = %s, want busy: the layer started a turn", r.state(c))
	}
	r.hook(c, providers.EventTurnStart) // confirms the delivery
	r.hook(c, providers.EventTurnEnd)
	r.advance(time.Minute)
	if r.state(c) != characters.Idle || len(r.notes.of(characters.NoticeUnconfirmed)) != 0 {
		t.Errorf("state %s, notices %v; want idle and no complaint", r.state(c), r.notes.of(characters.NoticeUnconfirmed))
	}
	r.hook(c, providers.EventSessionStart) // a repeated event does not type it again
	if len(proc.typedAll()) != 1 {
		t.Errorf("typed again: %q", proc.typedAll())
	}
}

func TestWhenEvenTheReserveCannotBeDeliveredTheCharacterIsStuckAndTheTableMasterTold(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{first: "THE LAYER"})
	tm := r.makeTM("3-login")
	// 1. It cannot be typed.
	broken := r.make("Dev Claude", "Quebrado", "3-login")
	r.mach[broken.Machine].host.proc(broken.ID).inputErr = errors.New("the terminal is gone")
	r.hook(broken, providers.EventSessionStart)
	if r.state(broken) != characters.Stuck {
		t.Errorf("a layer that cannot be typed: %s, want stuck", r.state(broken))
	}
	// 2. It is typed and no turn begins.
	mute := r.make("Dev Claude", "Mudo", "3-login")
	r.hook(mute, providers.EventSessionStart)
	r.advance(9 * time.Second)
	if r.state(mute) != characters.Busy {
		t.Fatalf("before the 10 seconds: %s, want busy", r.state(mute))
	}
	r.advance(time.Second)
	if r.state(mute) != characters.Stuck {
		t.Errorf("no turn after the layer: %s, want stuck", r.state(mute))
	}
	for _, c := range []characters.Character{broken, mute} {
		told := false
		for _, n := range append(r.notes.of(characters.NoticeStuck), r.notes.of(characters.NoticeUnconfirmed)...) {
			told = told || n.Character == c.ID && n.To.Kind == characters.ToTableMaster
		}
		if !told {
			t.Errorf("the table master was not told about %s", c.Name)
		}
	}
	_ = tm
}

func TestAStartThatIsNotReadyIn120SecondsIsInterruptedAndTheTableMasterTold(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	r.makeTM("3-login")
	c := r.make("Dev Claude", "Dev", "3-login")
	r.stable(c, claudeReady, 3) // the prompt is there, and no hook ever came
	r.advance(119 * time.Second)
	if r.state(c) != characters.Starting {
		t.Fatalf("at 119 s: %s, want starting", r.state(c))
	}
	r.advance(time.Second)
	got := r.get(c.ID)
	if got.State != characters.Interrupted || !strings.Contains(got.Reason, "not ready in 2m0s") {
		t.Errorf("at 120 s: %s (%s), want interrupted", got.State, got.Reason)
	}
	if ns := r.notes.of(characters.NoticeInterrupted); len(ns) != 1 || ns[0].To.Kind != characters.ToTableMaster {
		t.Errorf("notices = %+v", ns)
	}
}

// CA-PERS-09
func TestAScreenNoAdapterKnowsBlocksTheCharacterOnInputAndARawInputBringsItBack(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		after  string
		want   characters.State
		unlock func(*rig, characters.Character)
	}{
		{"back at the prompt", claudeReady, characters.Idle, nil},
		{"back to work", claudeBusy, characters.Busy, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, rigOptions{})
			r.makeTM("3-login")
			c := r.make("Dev Claude", "Dev", "3-login")
			r.stable(c, unknownScreen, 0)
			r.advance(29 * time.Second)
			if r.state(c) != characters.Starting {
				t.Fatalf("before 30 s: %s", r.state(c))
			}
			r.advance(time.Second)
			got := r.get(c.ID)
			if got.State != characters.Blocked || got.Reason != characters.ReasonInput {
				t.Fatalf("the screen stayed still: %s (%s), want blocked (input)", got.State, got.Reason)
			}
			var ns []characters.Notice
			for _, n := range r.notes.of(characters.NoticeBlockedInput) {
				if n.Character == c.ID {
					ns = append(ns, n)
				}
			}
			if len(ns) != 1 || ns[0].To.Kind != characters.ToTableMaster || ns[0].Character != c.ID || !strings.Contains(ns[0].Screen, "Press Enter") {
				t.Fatalf("the table master was not told with the screen: %+v", ns)
			}
			// `ask --raw` answers; the next screen says where the agent is.
			if err := r.mach[c.Machine].rt.Input(r.ctx, c.ID, []byte("\r")); err != nil {
				t.Fatal(err)
			}
			if typed := r.mach[c.Machine].host.proc(c.ID).typedAll(); len(typed) != 1 || typed[0] != "\r" {
				t.Errorf("typed = %q", typed)
			}
			if r.state(c) != characters.Blocked {
				t.Fatalf("the input alone changes nothing: %s", r.state(c))
			}
			r.stable(c, tt.after, 3)
			if r.state(c) != tt.want {
				t.Errorf("after the input: %s, want %s", r.state(c), tt.want)
			}
		})
	}
}

func TestInputIsRefusedToABusyCharacterAndToAPermissionPromptAndAllowedToASimpleTerminal(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	rt := r.mach["linux"].rt
	dev := r.make("Dev Claude", "Dev", "3-login")
	r.ready(dev)
	r.busy(dev)
	if err := rt.Input(r.ctx, dev.ID, []byte("1")); !errors.Is(err, characters.ErrBusy) {
		t.Errorf("busy: %v, want busy", err)
	}
	r.hook(dev, providers.EventPermissionRequest)
	if err := rt.Input(r.ctx, dev.ID, []byte("1")); !errors.Is(err, characters.ErrPermissionPrompt) {
		t.Errorf("permission prompt: %v, want permission_prompt", err)
	}
	if typed := r.mach["linux"].host.proc(dev.ID).typedAll(); len(typed) != 0 {
		t.Errorf("something was typed in the refusals: %q", typed)
	}

	// A simple terminal: busy after an input, idle when the prompt comes back.
	res, err := r.create(characters.CreateRequest{Name: "srv", Base: "bash", Table: "3-login"})
	if err != nil {
		t.Fatal(err)
	}
	sh := res.Character
	if sh.Kind != characters.KindTerminal {
		t.Fatalf("kind = %s", sh.Kind)
	}
	r.stable(sh, "user@host:~$ ", 0)
	if r.state(sh) != characters.Idle {
		t.Fatalf("the prompt of the base: %s, want idle", r.state(sh))
	}
	if err := rt.Input(r.ctx, sh.ID, []byte("sleep 30\r")); err != nil {
		t.Fatal(err)
	}
	if r.state(sh) != characters.Busy {
		t.Fatalf("after the input: %s, want busy", r.state(sh))
	}
	if err := rt.Input(r.ctx, sh.ID, []byte("\x03")); err != nil { // a terminal takes input in any state
		t.Errorf("busy terminal: %v", err)
	}
	r.stable(sh, "^C\nuser@host:~$ ", 1)
	if r.state(sh) != characters.Idle {
		t.Errorf("the prompt came back: %s, want idle", r.state(sh))
	}
	// A terminal never ends by itself.
	r.advance(10 * time.Hour)
	if r.state(sh) != characters.Idle {
		t.Errorf("a simple terminal was ended by the lifecycle: %s", r.state(sh))
	}
}

func TestAnApprovalAskLeavesTheCharacterWaitingForApprovalNotBlockedOnPermission(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.make("Dev Claude", "Dev", "3-login")
	r.ready(c)
	r.busy(c)
	rt := r.mach[c.Machine].rt
	if err := rt.WaitingApproval(r.ctx, c.ID, 3); err != nil {
		t.Fatal(err)
	}
	r.hook(c, providers.EventTurnEnd)
	got := r.get(c.ID)
	if got.State != characters.Waiting || got.Reason != "approval #3" {
		t.Fatalf("state = %s (%s), want waiting (approval #3)", got.State, got.Reason)
	}
	r.hook(c, providers.EventTurnStart) // the decision arrived
	r.hook(c, providers.EventTurnEnd)
	if r.state(c) != characters.Idle {
		t.Errorf("after the decision: %s, want idle", r.state(c))
	}
	// And a permission prompt of the provider is a different thing.
	r.busy(c)
	r.hook(c, providers.EventPermissionRequest)
	if got := r.get(c.ID); got.State != characters.Blocked || got.Reason != characters.ReasonPermission {
		t.Errorf("native prompt = %s (%s), want blocked (permission)", got.State, got.Reason)
	}
	// Part of CA-CADEIA-13: the two are told apart on the canvas.
	if got := r.get(c.ID).Status().Label(); got != "aguardando permissão" {
		t.Errorf("label of the native prompt = %q", got)
	}
	if got := (characters.Status{State: characters.Waiting, Reason: "approval #3"}).Label(); got != "aguardando aprovação" {
		t.Errorf("label of the approval wait = %q", got)
	}
}

func TestADeliveryIsTypedOnlyToAnIdleOrWaitingCharacterAndMarksItBusy(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.make("Dev Claude", "Dev", "3-login")
	rt := r.mach[c.Machine].rt
	if err := rt.Deliver(r.ctx, c.ID, "m1", "olá"); !errors.Is(err, characters.ErrBusy) {
		t.Fatalf("a starting character: %v, want busy", err)
	}
	r.ready(c)
	if err := rt.Deliver(r.ctx, c.ID, "m1", "olá\ncom duas linhas"); err != nil {
		t.Fatal(err)
	}
	if typed := r.mach[c.Machine].host.proc(c.ID).typedAll(); len(typed) != 1 || typed[0] != paste("olá\ncom duas linhas") {
		t.Errorf("typed = %q", typed)
	}
	if r.state(c) != characters.Busy {
		t.Errorf("state = %s, want busy", r.state(c))
	}
	if err := rt.Deliver(r.ctx, c.ID, "m2", "outra"); !errors.Is(err, characters.ErrBusy) {
		t.Errorf("a busy character: %v, want busy", err)
	}
}

func TestEnvIsTheEnvironmentOfTheCharacterForThePermissionPipeline(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.make("Dev Claude", "Dev", "3-login")
	env, err := r.mach[c.Machine].rt.Env(r.ctx, c.ID)
	if err != nil || env.AgentID != c.ID || env.TableID != "3-login" || env.Worktree != filepath.Join("/wt", "linux", "3-login") || !strings.HasSuffix(env.Tmp, c.ID) {
		t.Errorf("env = %+v, %v", env, err)
	}
	if _, err := r.mach[c.Machine].rt.Env(r.ctx, "ninguem"); err == nil {
		t.Error("a character this machine does not run has an environment")
	}
}

func TestStopOnlyFromTheSafePointStuckOrInterrupted(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.make("Dev Claude", "Dev", "3-login")
	rt := r.mach[c.Machine].rt
	r.ready(c)
	r.busy(c)
	if err := rt.Stop(r.ctx, characters.StopCommand{Character: c.ID, Final: true}); !errors.Is(err, characters.ErrNotAtSafePoint) {
		t.Errorf("busy: %v, want not at a safe point", err)
	}
	if err := rt.Stop(r.ctx, characters.StopCommand{Character: "ninguem"}); !errors.Is(err, characters.ErrNoProcess) {
		t.Errorf("unknown: %v", err)
	}
	if r.state(c) != characters.Busy {
		t.Errorf("a refused stop changed the state to %s", r.state(c))
	}
}

func TestAnAgentThatEndsOnItsOwnIsInterruptedAndNotForgotten(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	tm := r.makeTM("3-login")
	c := r.make("Dev Claude", "Dev", "3-login")
	r.ready(c)
	r.busy(c)
	r.mach[c.Machine].host.emit(characters.HostEvent{Terminal: c.ID, Kind: characters.HostExit, ExitCode: 137})
	got := r.get(c.ID)
	if got.State != characters.Interrupted || !strings.Contains(got.Reason, "137") {
		t.Fatalf("state = %s (%s), want interrupted", got.State, got.Reason)
	}
	if ns := r.notes.of(characters.NoticeInterrupted); len(ns) != 1 || ns[0].To.Kind != characters.ToTableMaster {
		t.Errorf("the table master was not told: %+v", ns)
	}
	// A terminal alive but out of reach is not an interruption.
	d := r.make("Dev Claude", "Dev 2", "3-login")
	r.ready(d)
	r.mach[d.Machine].host.emit(characters.HostEvent{Terminal: d.ID, Kind: characters.HostLost})
	if r.state(d) != characters.Idle {
		t.Errorf("an unreachable terminal: %s, want idle", r.state(d))
	}
	// A terminal simple warns its creator, who ran the commands.
	res, err := r.create(characters.CreateRequest{Name: "srv", Base: "bash", Table: "3-login", Creator: byChar(tm)})
	if err != nil {
		t.Fatal(err)
	}
	r.mach[res.Character.Machine].host.emit(characters.HostEvent{Terminal: res.Character.ID, Kind: characters.HostExit})
	if ns := r.notes.of(characters.NoticeNotResumed); len(ns) != 1 || ns[0].Creator != byChar(tm) {
		t.Errorf("the creator of the terminal was not told: %+v", ns)
	}
}

func TestABaseThatIsReadyAfterADelayIsReadyWhenItPasses(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	sh := mustCreate(t, r, characters.CreateRequest{Name: "lento", Base: "slow", Table: "3-login"})
	r.clk.Advance(4 * time.Second)
	if r.state(sh) != characters.Starting {
		t.Fatalf("before the delay: %s", r.state(sh))
	}
	r.clk.Advance(time.Second)
	if r.state(sh) != characters.Idle {
		t.Errorf("after the delay: %s, want idle", r.state(sh))
	}
}
