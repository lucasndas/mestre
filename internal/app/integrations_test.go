package app

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/faketelegram"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

type integ struct {
	t     *testing.T
	ctx   context.Context
	clk   *clock.Fake
	db    *store.DB
	vault *vault.Vault
	tg    *faketelegram.Server
	std   StandardIntegration
	mu    sync.Mutex
	uses  []string
}

func newInteg(t *testing.T, withToken bool) *integ {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	db, err := guildtpl.Open(ctx, filepath.Join(dir, "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rk, err := vault.NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	i := &integ{t: t, ctx: ctx, clk: clk, db: db}
	v, err := vault.Create(ctx, vault.Config{DB: db, Keys: &memKeyStore{}, EnvelopePath: filepath.Join(dir, "cofre-envelope.bin"),
		RecoveryPublicKey: rk.PublicKey(), Masker: logging.NewMasker(), Clock: clk,
		OnUse: func(u vault.UseEvent) { i.mu.Lock(); i.uses = append(i.uses, u.Name); i.mu.Unlock() }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	i.vault = v
	const token = "123456:fake-bot-token"
	if i.tg, err = faketelegram.New(clk, t, token); err != nil {
		t.Fatal(err)
	}
	if withToken {
		if err := v.Put(ctx, TelegramTokenSecret, vault.TypeTelegramBotToken, []byte(token), nil); err != nil {
			t.Fatal(err)
		}
		i.mu.Lock()
		i.uses = nil
		i.mu.Unlock()
	}
	i.std = StandardIntegration{TelegramBase: "https://api.telegram.org", HTTP: &http.Client{Transport: fakenet.HandlerTransport(i.tg)}}
	return i
}

func (i *integ) usesOfVault() []string {
	i.mu.Lock()
	defer i.mu.Unlock()
	return slices.Clone(i.uses)
}

func (i *integ) open() *stdRun {
	i.t.Helper()
	run, err := i.std.Open(i.ctx, IntegrationEnv{DB: i.db, Vault: i.vault, Clock: i.clk, Machine: "m1", Masker: logging.NewMasker()})
	if err != nil {
		i.t.Fatal(err)
	}
	return run.(*stdRun)
}

func TestOpeningTheIntegrationsReadsNothingFromTheVaultNorWritesToTheGuild(t *testing.T) {
	t.Parallel()
	i := newInteg(t, true)
	var before int
	if err := i.db.Reader().QueryRowContext(i.ctx, `SELECT COUNT(*) FROM telegram_outbox`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	run := i.open()
	var o OrchestrationOptions
	run.Extend(&o)
	if o.ChainUsers == nil {
		t.Error("the people of the chain were not put")
	}
	if u := i.usesOfVault(); len(u) != 0 {
		t.Errorf("a Citadel that may not act has read the vault: %v", u)
	}
	run.Close()
}

func TestTheBotIsMadeWhenTheCitadelActsAndOnlyWithItsToken(t *testing.T) {
	t.Parallel()
	for _, withToken := range []bool{true, false} {
		i := newInteg(t, withToken)
		run := i.open()
		_, err := run.startBot(i.ctx)
		if withToken != (err == nil) {
			t.Fatalf("token %v: startBot = %v", withToken, err)
		}
		if withToken {
			if u := i.usesOfVault(); len(u) != 1 || u[0] != TelegramTokenSecret {
				t.Errorf("the vault was used %v, want the token once", u)
			}
			if again, err := run.startBot(i.ctx); err != nil || again != run.bot {
				t.Errorf("the second start made another bot: %v", err)
			}
		} else if strings.Contains(err.Error(), "123456") {
			t.Errorf("the error says too much: %v", err)
		}
		run.Close()
	}
}

func TestTheExternalFunctionEndsWithItsContextWhetherOrNotTheBotCanRun(t *testing.T) {
	t.Parallel()
	for _, withToken := range []bool{true, false} {
		i := newInteg(t, withToken)
		run := i.open()
		ext := run.Attach(&Orchestration{Registry: characters.Registry{DB: i.db}}, localchan.NewLocalForwarder())
		if len(ext) != 1 {
			t.Fatalf("%d functions", len(ext))
		}
		ctx, cancel := context.WithCancel(i.ctx)
		cancel()
		_ = ext[0](ctx, nil) // returns: no fence, no bot calls
		if n := i.tg.Calls("deleteWebhook") + i.tg.Calls("getUpdates"); n != 0 {
			t.Errorf("token %v: %d calls to the Telegram with no fence", withToken, n)
		}
		run.Close()
	}
}

func TestTheCommandsOfIssuesAndPRsAreInTheLocalChannelAndRefuseOutsideTheFence(t *testing.T) {
	t.Parallel()
	i := newInteg(t, false)
	run := i.open()
	fwd := localchan.NewLocalForwarder()
	run.Attach(&Orchestration{Registry: characters.Registry{DB: i.db}}, fwd)
	for _, cmd := range []string{"issue create", "issue update", "issue comment", "issue list", "pr status", "pr comment", "pr reply", "pr note"} {
		got := fwd.Forward(i.ctx, &localchan.Call{Request: localchan.Request{V: 1, ID: "x", AgentID: "ghost", Cmd: cmd}, Caller: localchan.Caller{Identified: true, AgentID: "ghost"}})
		if got.OK || got.Error == nil || got.Error.Code != "github_unavailable" {
			t.Errorf("%s with no fence: %+v, want github_unavailable", cmd, got.Error)
		}
	}
	run.Close()
	if (stdLease{run}).Allow() == nil {
		t.Error("the lease is valid after the period ended")
	}
}

func TestTheOwnerIsToldWhenGitHubGoesOutAndIsBackEvenIfTheBotWasNotUpYet(t *testing.T) {
	t.Parallel()
	i := newInteg(t, true)
	svc, err := users.New(users.Options{DB: i.db, Clock: i.clk, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.CreateOwner(i.ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	code, err := svc.NewCode(i.ctx, owner, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pair(i.ctx, users.PairRequest{Text: code.Code, TelegramID: 77, Private: true}); err != nil {
		t.Fatal(err)
	}
	run := i.open()
	run.FenceChanged(guild.FenceLockDown) // the bot is not up yet
	run.FenceChanged(guild.FenceLockDown) // once
	if _, err := run.startBot(i.ctx); err != nil {
		t.Fatal(err)
	}
	run.FenceChanged(guild.FenceConfirmed)
	rows, err := i.db.Reader().QueryContext(i.ctx, `SELECT params FROM telegram_outbox WHERE chat_id = 77 ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var texts []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, p)
	}
	if len(texts) != 2 || !strings.Contains(texts[0], "GitHub fora") || !strings.Contains(texts[1], "GitHub de volta") {
		t.Errorf("the notices = %q", texts)
	}
}

// What the period gave out stops working when the period ends: the gate of the
// bot and the lease of GitHub (the handlers stay in the local channel until the next
// period replaces them).
func TestWhatThePeriodGaveOutStopsWorkingWhenItEnds(t *testing.T) {
	t.Parallel()
	i := newInteg(t, false)
	run := i.open()
	run.BindFence(guild.NewFence(guild.FenceConfig{Clock: i.clk, Epoch: func() uint64 { return 1 }})) // no lock: it acts, GitHub waits
	if !(lateGate{run}).Allowed(i.ctx) {
		t.Fatal("the gate of the bot is closed for a fence that acts")
	}
	if queued, err := (stdLease{run}).Do(i.ctx, "x", func(context.Context) error { return nil }); err != nil || !queued {
		t.Fatalf("the lease: queued %v, %v", queued, err)
	}
	run.Close()
	if (lateGate{run}).Allowed(i.ctx) {
		t.Error("the bot may still call the Telegram after the period")
	}
	if _, err := (stdLease{run}).Do(i.ctx, "x", func(context.Context) error { return nil }); err == nil {
		t.Error("the lease still queues after the period")
	}
	if (stdLease{run}).Allow() == nil {
		t.Error("the lease allows after the period")
	}
}

// The bot calls the Telegram only when the fence says so: before the fence is
// made (the integration opens first) nothing goes out.
func TestTheGateOfTheBotIsClosedUntilTheFenceExists(t *testing.T) {
	t.Parallel()
	r := &stdRun{}
	if (lateGate{r}).Allowed(context.Background()) {
		t.Fatal("the gate let a call through with no fence")
	}
}

// The lock of the channel is one state for the whole Citadel: the engine, the
// commands of GitHub and the orchestration read the same one.
func TestTheLockOfTheChannelReachesTheEngineAndTheOrchestration(t *testing.T) {
	t.Parallel()
	i := newInteg(t, false)
	var seen func(context.Context) bool
	run, err := i.std.Open(i.ctx, IntegrationEnv{DB: i.db, Vault: i.vault, Clock: i.clk, Machine: "m1", Masker: logging.NewMasker(),
		SetLocked: func(f func(context.Context) bool) { seen = f }})
	if err != nil {
		t.Fatal(err)
	}
	defer run.Close()
	var o OrchestrationOptions
	run.Extend(&o)
	if seen == nil || o.Locked == nil {
		t.Fatal("the lock did not reach the engine or the orchestration")
	}
	if seen(i.ctx) || o.Locked(i.ctx) {
		t.Fatal("locked with nothing locking")
	}
	if _, err := i.db.Writer().ExecContext(i.ctx, `UPDATE channel_lock SET locked = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if !seen(i.ctx) || !o.Locked(i.ctx) {
		t.Error("the lock is not seen by the engine and the orchestration")
	}
}

// memKeyStore keeps the key of the vault in memory.
type memKeyStore struct {
	mu   sync.Mutex
	data []byte
}

func (m *memKeyStore) Load(context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		return nil, vault.ErrNoKey
	}
	return slices.Clone(m.data), nil
}

func (m *memKeyStore) Save(_ context.Context, d []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = slices.Clone(d)
	return nil
}

func TestTheEngineIsLockedByTheLockOfThePeriodAndByTheOneItWasGiven(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	e := &Engine{}
	if e.locked(ctx) {
		t.Fatal("locked with no lock")
	}
	e.setLocked(func(context.Context) bool { return true })
	if !e.locked(ctx) {
		t.Fatal("the lock of the period is not seen")
	}
	e.setLocked(nil)
	if e.locked(ctx) {
		t.Fatal("the lock of a period outlived it")
	}
	e.deps.Locked = func(context.Context) bool { return true }
	if !e.locked(ctx) {
		t.Fatal("the lock given in the dependencies is not seen")
	}
}
