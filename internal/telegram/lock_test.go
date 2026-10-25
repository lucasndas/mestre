package telegram_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/faketelegram"
	"github.com/lucasndas/mestre/internal/users"
)

const (
	lairRepo  = "lucas/mestre-guilda-abc12345"
	lairToken = "ghs_lock-token"
)

// lockWorld is the bot of the Citadel of the epoch 3 with the lock on the GitHub
// falso: its fence is the Gate of the bot and the 409 goes to the fence.
type lockWorld struct {
	*world
	gh      *fakegithub.Server
	fence   *guild.Fence
	changed chan guild.FenceState
	higher  chan uint64
	own     chan struct{}
	tgStop  chan bool
	done    chan struct{}
}

func newLockWorld(t *testing.T) *lockWorld {
	t.Helper()
	w := newWorld(t)
	gh, err := fakegithub.New(w.clk, t)
	if err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("lucas", "mestre-guilda-abc12345")
	gh.IssueToken(lairToken, "grand_master")
	gh.AddRef(lairRepo, "refs/mestre/epoca-3", "machine a, epoch 3")
	lock := &github.LairLock{Repo: func(context.Context) (string, error) { return lairRepo, nil }, Token: func(context.Context) (string, error) { return lairToken, nil },
		GuildID: "01GUILD", BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(gh)}, Clock: w.clk}
	lw := &lockWorld{world: w, gh: gh, changed: make(chan guild.FenceState, 16), higher: make(chan uint64, 4), own: make(chan struct{}, 4), tgStop: make(chan bool, 4), done: make(chan struct{})}
	lw.fence = guild.NewFence(guild.FenceConfig{Lock: lock, Clock: w.clk, Epoch: func() uint64 { return 3 },
		OnChange: func(s guild.FenceState) { lw.changed <- s }, OnHigher: func(n uint64) { lw.higher <- n },
		OnConflictOwn: func() { lw.own <- struct{}{} }, OnTelegram: func(stopped bool) { lw.tgStop <- stopped }})
	w.opts.Gate = lw.fence.Gate(guild.ExternalTelegram)
	w.opts.OnConflict = func(context.Context) { lw.fence.TelegramConflict() }
	if w.bot, err = telegram.New(w.opts); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	go func() { defer close(lw.done); _ = lw.fence.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-lw.done })
	if s := <-lw.changed; s != guild.FenceConfirmed {
		t.Fatalf("first check: %s", s)
	}
	return lw
}

// conflict makes the next getUpdates answer 409 and polls once.
func (lw *lockWorld) conflict() {
	lw.t.Helper()
	lw.fake.FailNext("getUpdates", faketelegram.Failure{Status: http.StatusConflict})
	if err := lw.bot.PollOnce(lw.ctx); err == nil {
		lw.t.Fatal("the 409 was not reported")
	}
}

// CA-TRAVA-04
func TestA409WithAValidLeaseAsksForACheckAndOnlyAHigherEpochSuspends(t *testing.T) {
	t.Parallel()
	t.Run("own epoch", func(t *testing.T) {
		t.Parallel()
		lw := newLockWorld(t)
		lw.conflict()
		<-lw.own
		before := lw.fake.Calls("getUpdates")
		if err := lw.bot.PollOnce(lw.ctx); err != nil || lw.fake.Calls("getUpdates") != before+1 {
			t.Fatalf("after the check of its own epoch the bot did not go on reading: %v", err)
		}
	})
	t.Run("higher epoch", func(t *testing.T) {
		t.Parallel()
		lw := newLockWorld(t)
		lw.gh.AddRef(lairRepo, "refs/mestre/epoca-4", "machine b, epoch 4")
		lw.conflict()
		if n := <-lw.higher; n != 4 {
			t.Fatalf("higher = %d", n)
		}
		before := lw.fake.Calls("getUpdates")
		if err := lw.bot.PollOnce(lw.ctx); err != nil || lw.fake.Calls("getUpdates") != before {
			t.Fatalf("a suspended Citadel read the bot: %v", err)
		}
	})
}

// CA-BOT-11
func TestA409OfTheGetUpdatesMakesTheBotCheckTheLockAndStopOnlyWithAHigherEpoch(t *testing.T) {
	t.Parallel()
	for _, higher := range []bool{true, false} {
		lw := newLockWorld(t)
		a, ta := lw.user("Ana", users.Approver)
		if _, err := lw.bot.Send(lw.ctx, telegram.Out{ChatID: ta.ID, UserID: a.ID, Text: "na fila"}); err != nil {
			t.Fatal(err)
		}
		if higher {
			lw.gh.AddRef(lairRepo, "refs/mestre/epoca-4", "machine b, epoch 4")
		}
		lw.conflict()
		if higher {
			<-lw.higher
			lw.flush()
			if q, s, _, _ := lw.bot.OutboxState(lw.ctx); q != 1 || s != 0 || lw.fake.Calls("sendMessage") != 0 {
				t.Fatalf("suspended: queued %d, sent %d; want the message kept and nothing sent", q, s)
			}
			// the engine stops reading and the Citadel is suspended
			reads := lw.fake.Calls("getUpdates")
			if err := lw.bot.PollOnce(lw.ctx); err != nil || lw.fake.Calls("getUpdates") != reads {
				t.Fatalf("a suspended Citadel read the bot: %v", err)
			}
			if st := lw.fence.State(); st != guild.FenceDemoted && st != guild.FenceSuspended {
				t.Fatalf("fence state = %s, want the Citadel suspended", st)
			}
			continue
		}
		<-lw.own // it goes on reading and sending
		lw.flush()
		if q, s, _, _ := lw.bot.OutboxState(lw.ctx); q != 0 || s != 1 {
			t.Fatalf("own epoch: queued %d, sent %d", q, s)
		}
		reads := lw.fake.Calls("getUpdates")
		if err := lw.bot.PollOnce(lw.ctx); err != nil || lw.fake.Calls("getUpdates") != reads+1 {
			t.Fatalf("own epoch: the bot did not go on reading: %v", err)
		}
		if st := lw.fence.State(); st != guild.FenceConfirmed || lw.fence.TelegramStopped() {
			t.Fatalf("own epoch: fence state = %s (telegram stopped %v), want confirmed", st, lw.fence.TelegramStopped())
		}
	}
}

// CA-BOT-12
func TestACitadelThatReadAHigherEpochCallsNothingOnTheTelegram(t *testing.T) {
	t.Parallel()
	ran := 0
	lw := newLockWorld(t)
	lw.opts.Actions = map[string]telegram.Action{"approve": func(context.Context, telegram.Click) (string, string, error) { ran++; return "", "", nil }}
	var err error
	if lw.bot, err = telegram.New(lw.opts); err != nil {
		t.Fatal(err)
	}
	a, ta := lw.user("Ana", users.Approver)
	if _, err := lw.bot.Send(lw.ctx, telegram.Out{ChatID: ta.ID, UserID: a.ID, Text: "Aprovar?", Buttons: [][]telegram.Button{{{Text: "Sim", Action: "approve", Capability: users.DecideApprovals}}}}); err != nil {
		t.Fatal(err)
	}
	lw.flush()
	key := lw.sent("sendMessage")[0].Params["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)["callback_data"].(string)
	// the click is waiting at the Telegram; the lock then shows epoch 4
	if _, err := lw.fake.UserClicks(ta, 1, key); err != nil {
		t.Fatal(err)
	}
	lw.gh.AddRef(lairRepo, "refs/mestre/epoca-4", "machine b, epoch 4")
	lw.clk.Advance(guild.LeaseInterval)
	<-lw.higher
	if _, err := lw.bot.Send(lw.ctx, telegram.Out{ChatID: ta.ID, UserID: a.ID, Text: "depois"}); err != nil {
		t.Fatal(err)
	}
	calls := map[string]int{}
	for _, m := range []string{"getUpdates", "sendMessage", "answerCallbackQuery", "editMessageText"} {
		calls[m] = lw.fake.Calls(m)
	}
	lw.clk.Advance(2 * time.Second)
	if err := lw.bot.PollOnce(lw.ctx); err != nil {
		t.Fatal(err)
	}
	lw.flush()
	for m, n := range calls {
		if lw.fake.Calls(m) != n {
			t.Errorf("%s was called after the epoch 4 was read", m)
		}
	}
	if ran != 0 {
		t.Fatal("the click was executed")
	}
}

// CA-TRAVA-07
func TestWithTheLeaseGoneA409StopsTheTelegramUntilTheEpochIsConfirmed(t *testing.T) {
	t.Parallel()
	lw := newLockWorld(t)
	a, ta := lw.user("Ana", users.Approver)
	lw.gh.DownGit(http.StatusServiceUnavailable)
	lw.clk.Advance(61 * time.Second)
	if s := <-lw.changed; s != guild.FenceLockDown {
		t.Fatalf("state %s, want GitHub fora", s)
	}
	if _, err := lw.bot.Send(lw.ctx, telegram.Out{ChatID: ta.ID, UserID: a.ID, Text: "oi"}); err != nil {
		t.Fatal(err)
	}
	lw.conflict()
	if stopped := <-lw.tgStop; !stopped || !lw.fence.TelegramStopped() {
		t.Fatal("the Telegram did not stop")
	}
	if got := lw.fence.TelegramNotice(); !strings.Contains(got, "Telegram parado") {
		t.Fatalf("the panel notice = %q, want \"Telegram parado\"", got)
	}
	reads, sends := lw.fake.Calls("getUpdates"), lw.fake.Calls("sendMessage")
	if err := lw.bot.PollOnce(lw.ctx); err != nil {
		t.Fatal(err)
	}
	lw.flush()
	if lw.fake.Calls("getUpdates") != reads || lw.fake.Calls("sendMessage") != sends {
		t.Fatal("the Telegram was called while stopped")
	}
	// GitHub back with its own epoch: it reads again, and the message goes out
	lw.gh.UpGit()
	lw.clk.Advance(guild.LeaseInterval)
	if stopped := <-lw.tgStop; stopped {
		t.Fatal("the Telegram stayed stopped")
	}
	if got := lw.fence.TelegramNotice(); got != "" {
		t.Fatalf("the panel notice = %q after the epoch was confirmed, want none", got)
	}
	lw.flush()
	if err := lw.bot.PollOnce(lw.ctx); err != nil || lw.fake.Calls("getUpdates") != reads+1 || lw.fake.Calls("sendMessage") != sends+1 {
		t.Fatalf("after the check: reads %d (was %d), sends %d (was %d), %v", lw.fake.Calls("getUpdates"), reads, lw.fake.Calls("sendMessage"), sends, err)
	}
}
