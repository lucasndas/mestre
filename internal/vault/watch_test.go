package vault

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Part of CA-COFRE-17 (the notice by the bot is in internal/app): the watcher
// asks again for a notice that was not delivered, works with the vault closed and
// its loop looks once at the start and then every WatchEvery.
func TestTheWatcherRetriesWhatWasNotDeliveredAndRunsByTheClock(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	v := f.create(t)
	exp := f.clock.Now().Add(3 * 24 * time.Hour)
	site := *sampleSite
	site.ExpiresAt = &exp
	put(t, v, "site_loja", TypeSiteCredential, `{"url":"https://loja.example/login","user":"qa@x","password":"Abc123!senha-longa"}`, &site)
	v.Close() // the metadata is read with the vault closed

	var mu sync.Mutex
	var got []Notice
	fail := true
	w := &ExpiryWatcher{Vault: v, Clock: f.clock, Notify: func(_ context.Context, n Notice, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		if fail {
			return errors.New("nobody to tell")
		}
		got = append(got, n)
		return nil
	}}
	if n, err := w.Scan(f.ctx); err != nil || n != 0 {
		t.Fatalf("Scan with the delivery failing = %d, %v", n, err)
	}
	mu.Lock()
	fail = false
	mu.Unlock()

	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan struct{})
	armed := f.clock.Armed()
	go func() { defer close(done); _ = w.Run(ctx) }()
	f.clock.BlockUntil(armed + 1) // the first look is made, the ticker is armed
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0].Name != "site_loja" || got[0].Kind != NoticeExpiring || got[0].Owner != "ana" || got[0].Days != 3 {
		t.Fatalf("notices = %+v, want one: site_loja expiring in 3 days for ana", got)
	}
}

// Part of CA-COFRE-17: each person has a memory of his own. When the owner_user
// cannot be told and the Owner can, the Owner is not told again at the next look
// and the owner_user still gets his notice then.
func TestAPersonWhoCouldNotBeToldIsTriedAgainAndTheOthersAreNot(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	v := f.create(t)
	exp := f.clock.Now().Add(3 * 24 * time.Hour)
	site := *sampleSite
	site.ExpiresAt = &exp
	put(t, v, "site_loja", TypeSiteCredential, `{"url":"https://loja.example/login","user":"qa@x","password":"Abc123!senha-longa"}`, &site)

	told := map[string]int{}
	anaDown := true
	w := &ExpiryWatcher{Vault: v, Clock: f.clock,
		People: func(context.Context, Notice) ([]string, error) { return []string{"ana", "dono"}, nil },
		Notify: func(_ context.Context, _ Notice, person string) error {
			if person == "ana" && anaDown {
				return errors.New("the owner_user is out of reach")
			}
			told[person]++
			return nil
		}}
	if n, err := w.Scan(f.ctx); err != nil || n != 1 || told["dono"] != 1 || told["ana"] != 0 {
		t.Fatalf("first look = %d, %v, told %v; want the Owner told and the owner_user not", n, err, told)
	}
	anaDown = false
	if n, err := w.Scan(f.ctx); err != nil || n != 1 || told["dono"] != 1 || told["ana"] != 1 {
		t.Fatalf("second look = %d, %v, told %v; want the owner_user told now and the Owner not again", n, err, told)
	}
	if n, err := w.Scan(f.ctx); err != nil || n != 0 || told["dono"] != 1 || told["ana"] != 1 {
		t.Fatalf("third look = %d, %v, told %v; want nothing", n, err, told)
	}
}
