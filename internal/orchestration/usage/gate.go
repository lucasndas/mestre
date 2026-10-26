package usage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/providers"
)

// Verdict says whether new work may start with a provider and an account
// (docs/plataforma/limites-de-uso.md#limiar-de-início). The use only grows
// inside a window, so a reading at or above the threshold holds until the
// reset however old it is; one below it proves nothing after 15 minutes and
// the work starts with a warning. A window with no known reset is valid only
// while the reading is recent. It changes nothing.
func (s *Service) Verdict(ctx context.Context, provider providers.ID, profile, workspace string) (Verdict, error) {
	v, _, _, err := s.verdict(ctx, provider, profile, workspace)
	return v, err
}

// held is a window that holds the start, until when.
type held struct {
	w     window
	until time.Time
}

func (s *Service) verdict(ctx context.Context, provider providers.ID, profile, workspace string) (Verdict, []held, []window, error) {
	threshold, err := s.st.Threshold(ctx, workspace)
	if err != nil {
		return Verdict{}, nil, nil, err
	}
	ws, err := s.st.windows(ctx, string(provider), profile)
	if err != nil {
		return Verdict{}, nil, nil, err
	}
	now := s.clk.Now()
	var (
		v       Verdict
		holding []held
		stale   []window
	)
	for _, w := range ws {
		age := now.Sub(w.ReadAt)
		if !w.ResetsAt.IsZero() && !now.Before(w.ResetsAt) {
			continue // the window was renewed since the reading
		}
		switch {
		case w.UsedPct >= threshold && (!w.ResetsAt.IsZero() || age < StaleAfter):
			until := w.ResetsAt
			if until.IsZero() {
				until = w.ReadAt.Add(StaleAfter)
			}
			if !v.Hold || until.After(v.Until) {
				v.Until, v.Window, v.Percent = until, w.Name, w.UsedPct
			}
			v.Hold = true
			holding = append(holding, held{w, until})
		case age >= StaleAfter:
			v.Unconfirmed = true
			stale = append(stale, w)
		}
	}
	if v.Hold {
		v.Unconfirmed, stale = false, nil
	}
	return v, holding, stale, nil
}

// Held implements inbox.Holds: a character whose provider and account have a
// window at or above the start threshold gets nothing delivered until the
// reset, and the table waits for the quota; one whose use is not confirmed
// gets the delivery with a warning to the Mestre da mesa. Whoever is in a turn
// finishes it: a delivery only comes to a character that is not busy.
func (s *Service) Held(ctx context.Context, characterID string) (bool, error) {
	sub, ok, err := s.o.Directory.Character(ctx, characterID)
	if err != nil {
		return false, err
	}
	if !ok || sub.Provider == "" {
		return false, nil
	}
	v, holding, stale, err := s.verdict(ctx, sub.Provider, sub.Profile, sub.Workspace)
	if err != nil {
		return false, err
	}
	switch {
	case v.Hold:
		return true, s.announceHold(ctx, sub, v, holding)
	case v.Unconfirmed:
		return false, s.announceUnconfirmed(ctx, sub, stale)
	}
	return false, nil
}

// announceHold tells, once per window and reset, that the table waits for the
// quota. The moment the hold ends is remembered so Sweep delivers what waited
// when it passes.
func (s *Service) announceHold(ctx context.Context, sub Subject, v Verdict, holding []held) error {
	var errs []error
	fresh := false
	for _, h := range holding {
		ok, err := s.st.markHeld(ctx, h.w, h.until)
		if err != nil {
			errs = append(errs, err)
		}
		fresh = fresh || ok
	}
	if !fresh {
		return joinedOrNil(errs)
	}
	steps, _ := s.o.Directory.PendingSteps(ctx, sub.Workspace, sub.Table)
	line := fmt.Sprintf("⏸ Mesa %s pausada · %s %.0f%%", orElse(sub.TableName, sub.Table), v.Window, v.Percent)
	if !v.Until.IsZero() {
		line += " · volta às " + s.clock(ctx, v.Until)
	}
	if steps > 0 {
		line += fmt.Sprintf(" · faltam %d etapas", steps)
	}
	s.tell(ctx, sub.Workspace, sub.Table, line)
	return joinedOrNil(errs)
}

// announceUnconfirmed warns the Mestre da mesa that the use of the account is
// not confirmed and the step goes anyway; the next reading decides.
func (s *Service) announceUnconfirmed(ctx context.Context, sub Subject, stale []window) error {
	var errs []error
	var names []string
	for _, w := range stale {
		ok, err := s.st.markWarned(ctx, w)
		if err != nil {
			errs = append(errs, err)
		}
		if ok {
			names = append(names, w.Name)
		}
	}
	if len(names) == 0 {
		return joinedOrNil(errs)
	}
	text := fmt.Sprintf("O uso da conta %s de %s (janela %s) não está confirmado: a última leitura é antiga. A etapa foi entregue; a próxima leitura decide.",
		account(sub.Profile), sub.Provider, strings.Join(names, ", "))
	if err := s.notify(ctx, characters.ToTableMaster, sub, text); err != nil {
		errs = append(errs, err)
	}
	return joinedOrNil(errs)
}

// Sweep lifts what the clock lifts: it brings back the characters whose
// quota is due and delivers what waited for a window to be renewed. It is
// called by a loop; with a fake clock the test calls it after moving the
// time.
func (s *Service) Sweep(ctx context.Context) error {
	now := s.clk.Now()
	var errs []error
	due, err := s.st.dueWaits(ctx, now)
	if err != nil {
		return err
	}
	for _, id := range due {
		if err := s.release(ctx, id, true); err != nil {
			errs = append(errs, err)
		}
	}
	held, err := s.st.heldDue(ctx, now)
	if err != nil {
		errs = append(errs, err)
	}
	for _, w := range held {
		if err := s.st.clearHeld(ctx, w); err != nil {
			errs = append(errs, err)
		}
	}
	if len(held) > 0 && s.o.Pump != nil {
		if err := s.o.Pump.PumpAll(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Loop runs Sweep every interval until the context ends.
func (s *Service) Loop(ctx context.Context, every time.Duration) error {
	t := s.clk.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C():
			if err := s.Sweep(ctx); err != nil {
				s.log.Warn("usage: the sweep did not finish", "err", err)
			}
		}
	}
}
