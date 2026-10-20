package app

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/testkit/fakelock"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/internal/vault"
)

// fencedGitHub is the standard integration over the double of GitHub with the
// fence of the period bound to a fake lock, the Owner paired on the Telegram (the
// notice "GitHub fora" is told to him) and `mestre pr comment` registered.
type fencedGitHub struct {
	*ghCompose
	lock   *fakelock.Lock
	fence  *guild.Fence
	fwd    *localchan.LocalForwarder
	states chan guild.FenceState
}

// newFencedGitHub binds a fence over a lock with the epoch 1 (the Citadel
// holds it). hold makes the first check wait (the Citadel that has just started).
func newFencedGitHub(t *testing.T, hold bool) *fencedGitHub {
	t.Helper()
	c := newGHCompose(t)
	f := &fencedGitHub{ghCompose: c, lock: fakelock.New(1, "m1"), fwd: localchan.NewLocalForwarder(), states: make(chan guild.FenceState, 64)}
	owner, err := c.run.users.CreateOwner(c.ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	code, err := c.run.users.NewCode(c.ctx, owner, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.run.users.Pair(c.ctx, users.PairRequest{Text: code.Code, TelegramID: 77, Private: true}); err != nil {
		t.Fatal(err)
	}
	// the bot is made (not run) so that the notices to the Owner reach the outbox
	if err := c.vault.Put(c.ctx, TelegramTokenSecret, vault.TypeTelegramBotToken, []byte("123456:fake-bot-token"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.run.startBot(c.ctx); err != nil {
		t.Fatal(err)
	}
	c.run.Attach(c.orc, f.fwd)
	if hold {
		f.lock.Hold()
	}
	f.fence = guild.NewFence(guild.FenceConfig{Lock: f.lock.For("m1"), Clock: c.clk, Epoch: func() uint64 { return 1 },
		OnChange: func(s guild.FenceState) { c.run.FenceChanged(s); f.states <- s }})
	c.run.BindFence(f.fence)
	ctx, cancel := context.WithCancel(c.ctx)
	done := make(chan struct{})
	go func() { _ = f.fence.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return f
}

// state waits for the fence to report s (the states that come before are skipped).
func (f *fencedGitHub) state(s guild.FenceState) {
	f.t.Helper()
	for {
		select {
		case got := <-f.states:
			if got == s {
				return
			}
		case <-time.After(30 * time.Second):
			f.t.Fatalf("the fence did not report %s", s)
		}
	}
}

// comment is `mestre pr comment <text>` of the Master of the table, with the id of the request.
func (f *fencedGitHub) comment(id, text string) localchan.Response {
	f.t.Helper()
	return f.fwd.Forward(f.ctx, &localchan.Call{
		Request: localchan.Request{V: 1, ID: id, AgentID: f.master.ID, Cmd: "pr comment", Args: localchan.TextArgs(text), Flags: map[string]any{}},
		Caller:  localchan.Caller{Identified: true, AgentID: f.master.ID, TerminalID: "t-master"},
	})
}

// until waits for cond, yielding the processor: the work it waits for runs on the
// goroutines of the fence and of the double of GitHub, with no timer of the fake clock.
func (f *fencedGitHub) until(what string, cond func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			f.t.Fatalf("timed out waiting for %s", what)
		}
		runtime.Gosched()
	}
}

// ownerNotices are the messages about GitHub the Owner got from the bot, in order.
func (f *fencedGitHub) ownerNotices() []string {
	f.t.Helper()
	rows, err := f.db.Reader().QueryContext(f.ctx, `SELECT params FROM telegram_outbox WHERE chat_id = 77 ORDER BY id`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			f.t.Fatal(err)
		}
		if strings.Contains(p, "GitHub") { // not the notice of the pairing
			out = append(out, p)
		}
	}
	return out
}

const repoFull = "lucas/meu_app"

// CA-CIDADELA-03
func TestAnIsolatedCitadelWithTheLeaseGoneQueuesTheGitHubCommentAndSendsItOnceWhenTheSameEpochIsConfirmed(t *testing.T) {
	t.Parallel()
	f := newFencedGitHub(t, false)
	f.state(guild.FenceConfirmed)

	// the network is cut and the lease of 60 s is gone: GitHub fora
	f.lock.SetAllDown(true)
	f.clk.Advance(2 * guild.LeaseValidity)
	f.state(guild.FenceLockDown)
	if f.fence.State() != guild.FenceLockDown || f.fence.LeaseValid() {
		t.Fatalf("state %s, lease valid %v; want the lease gone", f.fence.State(), f.fence.LeaseValid())
	}
	if !f.run.notice.Down() {
		t.Error("the panel does not show GitHub fora")
	}
	if got := f.ownerNotices(); len(got) != 1 || !strings.Contains(got[0], "GitHub fora") {
		t.Fatalf("notices to the Owner = %q, want one saying GitHub fora", got)
	}

	// it needs to comment: it does not suspend, the comment waits in the queue
	if got := f.comment("op-1", "feito"); !got.OK {
		t.Fatalf("the command = %+v; want it accepted (queued)", got.Error)
	}
	if f.fence.State() == guild.FenceSuspended || f.fence.State() == guild.FenceDemoted {
		t.Fatalf("the Citadel went to %s for a comment it could not send", f.fence.State())
	}
	if f.fence.Queued() != 1 || len(f.gh.Comments(repoFull, f.pr)) != 0 {
		t.Fatalf("queued %d, comments on GitHub %q; want the comment held in the queue", f.fence.Queued(), f.gh.Comments(repoFull, f.pr))
	}

	// the network is back and the same epoch is confirmed: the comment goes out once
	f.lock.SetAllDown(false)
	f.clk.Advance(guild.LeaseInterval)
	f.state(guild.FenceConfirmed)
	f.until("the queued comment on GitHub", func() bool { return len(f.gh.Comments(repoFull, f.pr)) > 0 })
	f.clk.Advance(2 * guild.LeaseInterval) // more checks: nothing more goes out
	f.until("the notice that GitHub is back", func() bool { return len(f.ownerNotices()) == 2 })
	if got := f.gh.Comments(repoFull, f.pr); len(got) != 1 || !strings.Contains(got[0], "feito") || !strings.Contains(got[0], github.Marker("op-1")) {
		t.Fatalf("comments on GitHub = %q, want one, with the marker of the request", got)
	}
	if f.fence.Queued() != 0 || f.run.notice.Down() {
		t.Errorf("queued %d, down %v after it came back", f.fence.Queued(), f.run.notice.Down())
	}
	if got := f.ownerNotices(); !strings.Contains(got[1], "GitHub de volta") {
		t.Errorf("notices to the Owner = %q, want the second to say GitHub de volta", got)
	}
}

// CA-CIDADELA-12
func TestACommentOfTheOldCitadelNotReplicatedAndTheSameOneInTheQueueMakeOneCommentAndNoExternalEvent(t *testing.T) {
	t.Parallel()
	f := newFencedGitHub(t, true) // the new Citadel has not confirmed its epoch yet
	const op = "op-resend"

	// the old Citadel created the comment, with its marker, and fell before it
	// replicated it; the new one has the same request in its queue of the output
	if got := f.comment(op, "feito"); !got.OK || f.fence.Queued() != 1 {
		t.Fatalf("the command = %+v, queued %d; want it queued", got.Error, f.fence.Queued())
	}
	rest := &github.REST{BaseURL: f.std.GitHubBase, HTTP: f.std.HTTP, Token: func(ctx context.Context, repo string) (string, error) {
		tok, err := f.run.tokens.Installation(ctx, github.Request{App: "lucas-mestre-mestre", Repo: repo, Permissions: issuePermissions})
		if err != nil {
			return "", err
		}
		return tok.Secret(), nil
	}}
	f.clk.Advance(time.Minute)
	if _, err := rest.PostComment(f.ctx, repoFull, f.pr, github.WithMarker("feito", op)); err != nil {
		t.Fatal(err)
	}
	if obj, found, _ := (&github.Objects{DB: f.db, Clock: f.clk}).ByOp(f.ctx, op); found {
		t.Fatalf("the comment is already registered here: %+v", obj)
	}

	// the new Citadel reconciles (its epoch is confirmed) and the queue empties
	f.lock.Release()
	f.state(guild.FenceConfirmed)
	f.until("the queue to empty and the comment to be registered", func() bool {
		_, found, _ := (&github.Objects{DB: f.db, Clock: f.clk}).ByOp(f.ctx, op)
		return found && f.fence.Queued() == 0
	})
	if got := f.gh.Comments(repoFull, f.pr); len(got) != 1 {
		t.Fatalf("comments on GitHub = %q, want one only", got)
	}

	// its event is not an external event, while another person's comment is
	poll, err := f.run.poller(f.orc)
	if err != nil {
		t.Fatal(err)
	}
	f.clk.Advance(time.Minute)
	f.gh.AddIssueComment(repoFull, f.pr, "ana", "olha o titulo")
	if err := poll.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	gm, _, err := f.orc.Registry.GrandMaster(f.ctx, f.ws.Slug)
	if err != nil {
		t.Fatal(err)
	}
	var fromGitHub []string
	for _, id := range []string{gm.ID, f.master.ID} {
		items, err := f.orc.Inbox.Store().All(f.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if it.Kind == inbox.KindGitHub {
				fromGitHub = append(fromGitHub, it.Text)
			}
		}
	}
	all := strings.Join(fromGitHub, "\n")
	if !strings.Contains(all, "olha o titulo") {
		t.Fatalf("events from GitHub = %q; the comment of the person did not arrive (the poller is not reading)", fromGitHub)
	}
	if strings.Contains(all, "feito") {
		t.Fatalf("events from GitHub = %q; the comment of the Citadel became an external event", fromGitHub)
	}
}
