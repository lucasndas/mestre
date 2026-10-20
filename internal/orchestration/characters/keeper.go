package characters

import (
	"context"
	"fmt"
	"sync"
	"time"
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
}

// Keeper starts the Grand Master of the workspaces of a machine again when it
// is interrupted.
type Keeper struct {
	s       *Service
	machine string

	mu sync.Mutex
	st map[string]*keepState
}

// NewKeeper returns the Keeper of the Grand Masters that run on a machine.
func (s *Service) NewKeeper(machine string) *Keeper {
	return &Keeper{s: s, machine: machine, st: map[string]*keepState{}}
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
	for _, c := range all {
		if c.Role != RoleGrandMaster {
			continue
		}
		due, alert := k.observe(c, now)
		if alert != "" {
			_ = k.s.o.Notifier.Notify(ctx, Notice{Kind: NoticeGrandMasterFailing, To: Recipient{Kind: ToOwner}, Workspace: c.Workspace,
				Character: c.ID, Name: c.Name, Text: alert})
		}
		if !due {
			continue
		}
		if err := k.s.resume(ctx, c); err != nil {
			k.s.log.Warn("characters: the Grand Master was not started again", "workspace", c.Workspace, "err", err)
		}
	}
	return nil
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
		if len(st.fails) < KeepAlertAfter {
			st.alerted = false
		} else if !st.alerted {
			st.alerted = true
			alert = fmt.Sprintf("O Grão-Mestre de %s caiu %d vezes em %d minutos (%s). O motor continua tentando iniciá-lo de novo a cada 2 minutos.",
				c.Workspace, len(st.fails), int(KeepWindow/time.Minute), c.Reason)
		}
	}
	if now.Before(st.next) {
		return false, alert
	}
	st.armed = false
	return true, alert
}

func (st *keepState) last() time.Time {
	if len(st.fails) == 0 {
		return time.Time{}
	}
	return st.fails[len(st.fails)-1]
}
