package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/internal/vault"
)

// CA-GM-17: the Grand Master starts through the CheckStart of the session zero. With the
// files of its provider not generated in the main clone (and the ones of another provider
// generated) the start is refused with the provider and the problems; the messages of the
// users stay in the queue and the user gets from the "Sistema", over the real bot, that
// it is unavailable and why; with the files corrected the Grand Master starts. (The same
// refusal by the Runtime of the terminals is in
// internal/orchestration/characters/grandmaster_test.go.)
func TestTheGrandMasterRefusedByTheCheckStartLeavesTheMessagesQueuedAndTheSystemTellsTheUser(t *testing.T) {
	t.Parallel()
	c := newGHComposeCfg(t, false, true)
	ctx := c.ctx
	owner, err := c.run.users.CreateOwner(ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	code, err := c.run.users.NewCode(ctx, owner, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.run.users.Pair(ctx, users.PairRequest{Text: code.Code, TelegramID: 77, Private: true}); err != nil {
		t.Fatal(err)
	}
	if owner, err = c.run.users.Get(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.vault.Put(ctx, TelegramTokenSecret, vault.TypeTelegramBotToken, []byte("123456:fake-bot-token"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.run.startBot(ctx); err != nil {
		t.Fatal(err)
	}
	c.run.BindFence(guild.NewFence(guild.FenceConfig{Clock: c.clk, Epoch: func() uint64 { return 1 }}))

	// the main clone of the workspace: the file of Claude Code is the person's, and broken
	main := filepath.Join(t.TempDir(), "principal")
	repo, err := c.remote.Init(ctx, main, gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(main, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("{isto não é json"), 0o644); err != nil {
		t.Fatal(err)
	}
	table, err := sessionzero.NewTable(sessionzero.TableOptions{Worktree: sessionzero.Worktree{Providers: providers.NewRegistry(providers.Options{NoFlush: true}), Binary: filepath.Join(t.TempDir(), "mestre")}})
	if err != nil {
		t.Fatal(err)
	}
	files := characters.TableFiles{Table: table, Requests: func(_ context.Context, ws, tbl, dir string) (sessionzero.TableRequest, error) {
		return sessionzero.TableRequest{Workspace: ws, Table: tbl, Dir: dir, Repo: repo}, nil
	}}
	c.machines.Check = func(ctx context.Context, cmd characters.StartCommand) error {
		return files.CheckStart(ctx, cmd.Request.Workspace, "", main, providers.ClaudeCode)
	}
	if err := files.CheckStart(ctx, c.ws.Slug, "", main, providers.Codex); err != nil {
		t.Fatalf("CheckStart(codex) = %v; want the files of another provider free", err)
	}

	// the Grand Master was interrupted: the resumption tries to start it and is refused
	gm := c.gm
	if _, err := c.db.Writer().ExecContext(ctx, `UPDATE characters SET state = ? WHERE id = ?`, string(characters.Interrupted), gm.ID); err != nil {
		t.Fatal(err)
	}
	err = c.orc.Characters.ResumeMachine(ctx, gm.Machine)
	var blocked *sessionzero.BlockedError
	if !errors.As(err, &blocked) || blocked.Provider != providers.ClaudeCode || !strings.Contains(err.Error(), ".claude/settings.local.json") {
		t.Fatalf("the start = %v; want the refusal of claude-code with the problem", err)
	}
	if got, err := c.orc.Characters.Get(ctx, gm.ID); err != nil || got.State != characters.Interrupted || !strings.Contains(got.Reason, "claude-code") || !strings.Contains(got.Reason, ".claude/settings.local.json") {
		t.Fatalf("the Grand Master = %+v, %v; want it interrupted with the provider and the problem", got, err)
	}

	// the user writes: the message waits and the "Sistema" tells the user why
	deliver := func(text string, id int64) {
		t.Helper()
		in := telegram.Inbound{User: owner, Kind: telegram.KindText, Text: text, ChatID: 77, MessageID: id}
		if err := (inboxDeliverer{c.orc}).Deliver(ctx, telegram.Delivery{Workspace: c.ws.Slug, Inbound: in}); err != nil {
			t.Fatal(err)
		}
	}
	queued := func() int {
		t.Helper()
		var n int
		if err := c.db.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM inbox_messages WHERE target = ? AND state = 'queued'`, gm.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := len(c.chatTexts(77))
	deliver("como está o login?", 5)
	if n := queued(); n != 1 {
		t.Fatalf("%d messages queued; want the one of the user", n)
	}
	told := c.chatTexts(77)[before:]
	if len(told) != 1 || !strings.Contains(told[0], telegram.SystemHeader) || !strings.Contains(told[0], "indisponível") || !strings.Contains(told[0], ".claude/settings.local.json") {
		t.Fatalf("the user was told %q; want the \"Sistema\" saying it is unavailable and why", told)
	}
	// a second message waits too, and the user is not told again
	deliver("e agora?", 6)
	if n, m := queued(), len(c.chatTexts(77)); n != 2 || m != before+1 {
		t.Fatalf("%d queued and %d notices; want 2 and 1", n, m)
	}

	// the person fixes the file: the next resumption starts it
	if err := os.WriteFile(broken, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.orc.Characters.ResumeMachine(ctx, gm.Machine); err != nil {
		t.Fatalf("the resumption after the fix: %v", err)
	}
	if got, err := c.orc.Characters.Get(ctx, gm.ID); err != nil || got.State == characters.Interrupted {
		t.Fatalf("the Grand Master = %+v, %v; want it started", got, err)
	}
}
