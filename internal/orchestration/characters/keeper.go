package characters

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The Grand Master is kept up (docs/orquestracao/grao-mestre.md#fixo-e-protegido):
// when its process dies the engine starts it again at once; the next failures
// wait 5 s, 30 s and then 2 min between one start and the next. With 3 failures
// in 10 min the users get one operational alert, and the engine goes on trying
// every 2 min, so a Grand Master that could not start (a provider that was
// logged out, files that were not generated) comes back when that is mended.
const (
	// KeepWindow is how long a failure counts.
	KeepWindow = 10 * time.Minute
	// KeepAlertAfter is the number of failures in the window that is alerted.
	KeepAlertAfter = 3
)

// keepBackoff is the wait before the start that follows the 1st, 2nd, 3rd and
// later failure in the window.
var keepBackoff = [...]time.Duration{0, 5 * time.Second, 30 * time.Second, 2 * time.Minute}

type keepState struct {
	fails   []time.Time
	next    time.Time
	armed   bool
	alerted bool
	// alertedAt is when the last alert was given: one is repeated each
	// RemindEvery while the Grand Master stays down.
	alertedAt time.Time
}

// RemindEvery is how often the alert is given again for a Grand Master that
// stays down: a reminder a day, not one at each attempt.
const RemindEvery = 24 * time.Hour

// Keeper starts the Grand Master of the workspaces of a machine again when it
// is interrupted.
type Keeper struct {
	s       *Service
	machine string
	// live says whether the workspace still exists (nil: it does); a Grand
	// Master of a workspace that is gone is not started again.
	live func(ctx context.Context, workspace string) bool

	mu sync.Mutex
	st map[string]*keepState
}

// NewKeeper returns the Keeper of the Grand Masters that run on a machine.
// live is optional: see Keeper.
func (s *Service) NewKeeper(machine string, live func(ctx context.Context, workspace string) bool) *Keeper {
	return &Keeper{s: s, machine: machine, live: live, st: map[string]*keepState{}}
}

// Run looks at the Grand Masters every second of the clock until the context
// ends.
func (k *Keeper) Run(ctx context.Context) error {
	t := k.s.clk.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C():
			if err := k.Tick(ctx); err != nil && ctx.Err() == nil {
				k.s.log.Warn("characters: the Grand Masters were not looked at", "err", err)
			}
		}
	}
}

// Tick starts again the Grand Masters whose time has come.
func (k *Keeper) Tick(ctx context.Context) error {
	all, err := k.s.o.Registry.OnMachine(ctx, k.machine)
	if err != nil {
		return err
	}
	now := k.s.clk.Now()
	known := map[string]bool{}
	for _, c := range all {
		if c.Role != RoleGrandMaster {
			continue
		}
		known[c.ID] = true
		// One whose end was asked for, or whose workspace is gone, is not ours
		// to start again.
		if !c.StopRequestedAt.IsZero() || k.live != nil && !k.live(ctx, c.Workspace) {
			continue
		}
		due, alert := k.observe(c, now)
		if alert != "" {
			if err := k.s.o.Notifier.Notify(ctx, Notice{Kind: NoticeGrandMasterFailing, To: Recipient{Kind: ToOwner}, Workspace: c.Workspace,
				Character: c.ID, Name: c.Name, Text: alert}); err != nil {
				k.s.log.Warn("characters: the alert of the Grand Master that keeps failing was not given", "workspace", c.Workspace, "err", err)
				k.unalert(c.ID) // the next failure of the window tells it
			}
		}
		if !due {
			continue
		}
		if err := k.s.resume(ctx, c); err != nil {
			k.s.log.Warn("characters: the Grand Master was not started again", "workspace", c.Workspace, "err", err)
		}
	}
	k.mu.Lock()
	for id := range k.st {
		if !known[id] {
			delete(k.st, id) // stopped, or gone with its workspace
		}
	}
	k.mu.Unlock()
	return nil
}

// unalert lets the alert be given again, when it could not be given.
func (k *Keeper) unalert(id string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if st := k.st[id]; st != nil {
		st.alerted = false
	}
}

// observe updates what is known of a Grand Master and says whether it is time
// to start it, and the alert to give, if this failure is the one to alert.
func (k *Keeper) observe(c Character, now time.Time) (due bool, alert string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	st := k.st[c.ID]
	if c.State != Interrupted {
		if st != nil && now.Sub(st.last()) >= KeepWindow {
			delete(k.st, c.ID)
		}
		return false, ""
	}
	if st == nil {
		st = &keepState{}
		k.st[c.ID] = st
	}
	if !st.armed {
		kept := st.fails[:0]
		for _, f := range st.fails {
			if now.Sub(f) < KeepWindow {
				kept = append(kept, f)
			}
		}
		st.fails = append(kept, now)
		n := min(len(st.fails), len(keepBackoff))
		st.next, st.armed = now.Add(keepBackoff[n-1]), true
		switch {
		case len(st.fails) < KeepAlertAfter && now.Sub(st.alertedAt) >= RemindEvery:
			st.alerted = false
		case len(st.fails) >= KeepAlertAfter && (!st.alerted || now.Sub(st.alertedAt) >= RemindEvery):
			st.alerted, st.alertedAt = true, now
			alert = fmt.Sprintf("O Grão-Mestre de %s caiu %d vezes em %d minutos (%s). O motor continua tentando iniciá-lo de novo a cada 2 minutos.",
				c.Workspace, len(st.fails), int(KeepWindow/time.Minute), untrustedReason(c.Reason))
		}
	}
	if now.Before(st.next) {
		return false, alert
	}
	st.armed = false
	return true, alert
}

// untrustedReason is the reason of a failure as the users may read it: one line
// without control or format characters, cut.
func untrustedReason(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

func (st *keepState) last() time.Time {
	if len(st.fails) == 0 {
		return time.Time{}
	}
	return st.fails[len(st.fails)-1]
}
