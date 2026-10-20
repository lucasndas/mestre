package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/testkit/fakelock"
	"github.com/lucasndas/mestre/pkg/clock"
)

// fenceLease is the lease of GitHub of the commands as the Citadel has it: the
// fence of the period.
type fenceLease struct{ f *guild.Fence }

func (l fenceLease) Allow() error { return l.f.Allow(guild.ExternalGitHub) }
func (l fenceLease) Do(ctx context.Context, name string, fn func(context.Context) error) (bool, error) {
	return l.f.Do(ctx, guild.ExternalGitHub, name, fn)
}

// CA-CLI-CONV-11
func TestPROpenWithTheLeaseOfTheLockExpiredIsPendingAndItsResultReachesTheInboxWithTheSameOpID(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"json", "text"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			m := &mm{}
			lock := fakelock.New(1, "a")
			confirmed := make(chan struct{}, 4)
			var w *commWorld
			fclk := clock.NewFake(time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))
			fence := guild.NewFence(guild.FenceConfig{Lock: lock.For("a"), Clock: fclk, Epoch: func() uint64 { return 1 }, OnChange: func(s guild.FenceState) {
				if s == guild.FenceConfirmed {
					confirmed <- struct{}{}
				}
			}})
			reported := make(chan string, 4)
			m.lease = fenceLease{fence}
			m.onQueued = func(ctx context.Context, opID, agentID, cmd string, resp localchan.Response) {
				if err := w.orc.Inbox.QueuedResult(ctx, opID, agentID, cmd, resp); err != nil {
					t.Errorf("the result of %s in the inbox: %v", opID, err)
				}
				reported <- opID
			}
			w = newCommWorld(t, m.setup)
			m.work()
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			go func() { _ = fence.Run(ctx) }()
			<-confirmed

			// the lease of the lock runs out: the lock is out of reach for more than 60 s
			lock.SetAllDown(true)
			fclk.Advance(61 * time.Second)
			if fence.LeaseValid() {
				t.Fatal("the lease is still valid after 61 s without a check that worked")
			}

			w.mach.Report(w.ctx, w.master.ID, characters.Busy) // busy: what arrives waits in its inbox
			args := []string{"pr", "open", "--title", "Login"}
			if mode == "json" {
				args = append(args, "--json")
			}
			r := w.run(w.master, args...)
			if r.code != 0 || r.stderr != "" {
				t.Fatalf("pr open with the lease expired = %+v; want exit 0", r)
			}
			var op string
			if mode == "json" {
				v := decodeJSON(t, r)
				op, _ = v.Data["op_id"].(string)
				if !v.OK || v.Data["queued"] != true || op == "" {
					t.Fatalf("pr open --json = %s; want ok with queued and op_id", r.stdout)
				}
			} else {
				if !strings.HasPrefix(r.stdout, "PENDING:") || strings.Contains(r.stdout, "opened") {
					t.Fatalf("pr open in text = %q; want it to start with PENDING: and not say opened", r.stdout)
				}
				_, rest, _ := strings.Cut(r.stdout, "(op ")
				op, _, _ = strings.Cut(rest, ")")
				if op == "" {
					t.Fatalf("pr open in text = %q; want the op id", r.stdout)
				}
			}
			if m.pushed() {
				t.Fatal("the branch went to GitHub while the lease was expired")
			}

			// the queue is sent when the lock answers again: the result is in the inbox of the caller, with the op_id
			lock.SetAllDown(false)
			fence.CheckNow()
			if got := <-reported; got != op {
				t.Fatalf("the result came with the op id %q; want %q", got, op)
			}
			if !m.pushed() {
				t.Error("the queue was sent and the branch did not go to GitHub")
			}
			inbox := w.run(w.master, "inbox", "--full")
			if inbox.code != 0 || !strings.Contains(inbox.stdout, op) || !strings.Contains(inbox.stdout, "concluído") {
				t.Errorf("the inbox of the caller = %+v; want an item with the op id %s and the result", inbox, op)
			}
		})
	}
}
