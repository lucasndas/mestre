package rituals

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
)

// stderrTail is how much of the error output of a failed pre-run goes back to
// the owner.
const stderrTail = 2 << 10

// Tick looks at the clock once: the rituals whose slot came make their firings,
// then every firing that waits is tried against its target. It is idempotent:
// a slot is taken once, and a closed firing is never touched again.
func (s *Service) Tick(ctx context.Context) error {
	s.tick.Lock()
	defer s.tick.Unlock()
	now := s.clk.Now()
	due, err := s.st.Due(ctx, now)
	if err != nil {
		return err
	}
	for _, r := range due {
		if err := s.fireDue(ctx, r, now); err != nil {
			s.log.Warn("rituals: the slot was not taken", "ritual", r.ID, "err", err)
		}
	}
	return s.process(ctx, now)
}

// fireDue takes the slots of a ritual that came. Only the latest of them can
// fire, and a recurring one only if it is not too late (the engine may have
// been down); the others are recorded as skipped.
func (s *Service) fireDue(ctx context.Context, r Ritual, now time.Time) error {
	loc := s.zone(ctx, r.Workspace)
	slots, next := r.Schedule.Slots(r.NextRun, now, loc)
	if len(slots) == 0 {
		return nil
	}
	state := r.State
	if r.Schedule.Kind == Once {
		state = Finished
	}
	latest := slots[len(slots)-1]
	late := now.Sub(latest)
	fires := r.Schedule.Kind == Once || late < r.Schedule.MaxLate()
	firings := make([]Firing, 0, len(slots))
	for _, slot := range slots[:len(slots)-1] {
		firings = append(firings, Firing{Ritual: r.ID, ScheduledFor: slot, CreatedAt: now, State: Skipped, UpdatedAt: now,
			Reason: "perdido com o motor fora; só o disparo mais recente conta"})
	}
	last := Firing{Ritual: r.ID, ScheduledFor: latest, CreatedAt: now, UpdatedAt: now, State: Queued}
	if r.Schedule.Kind == Once {
		last.Late = late > OnTimeSlack
	}
	if !fires {
		last.State = Skipped
		last.Reason = fmt.Sprintf("perdido com o motor fora: %s de atraso, e o limite é %s", late.Round(time.Second), r.Schedule.MaxLate())
	}
	firings = append(firings, last)
	done, _, err := s.st.Fire(ctx, r.ID, r.NextRun, next, state, firings)
	if err != nil || !done {
		return err
	}
	if !fires {
		s.tell(ctx, r, fmt.Sprintf("O ritual %q perdeu %d disparo(s) com o motor fora (o último, previsto para %s, estava %s atrasado, além do limite de %s) e nada foi disparado. "+
			"Rode `ritual run` se ainda for preciso.", r.Name, len(slots), latest.In(loc).Format("2006-01-02 15:04"), late.Round(time.Minute), r.Schedule.MaxLate()))
	}
	return nil
}

// Run fires a ritual now, by the same way as the schedule: it enters the queue
// of the target (or is skipped if the previous firing is still there).
func (s *Service) Run(ctx context.Context, id string, by Actor) (Firing, error) {
	r, err := s.st.Ritual(ctx, id)
	if err != nil {
		return Firing{}, err
	}
	if err := s.authorize(by, r.Workspace, r.Table); err != nil {
		return Firing{}, err
	}
	if r.State == Disabled {
		return Firing{}, fmt.Errorf("%w: the ritual is disabled; enable it first", ErrBadRequest)
	}
	now := s.clk.Now()
	var f Firing
	err = s.st.Do(ctx, func(t *Tx) (err error) {
		f, err = t.AddFiring(Firing{Ritual: r.ID, ScheduledFor: now, CreatedAt: now, UpdatedAt: now, State: Queued})
		return err
	})
	if err != nil {
		return Firing{}, err
	}
	if err := s.Tick(ctx); err != nil {
		return Firing{}, err
	}
	return s.st.Firing(ctx, f.ID)
}

// process tries every firing that waits.
func (s *Service) process(ctx context.Context, now time.Time) error {
	open, err := s.st.Open(ctx)
	if err != nil {
		return err
	}
	for _, f := range open {
		if err := s.deliver(ctx, f, now); err != nil {
			s.log.Warn("rituals: the firing was not handled", "firing", f.ID, "err", err)
		}
	}
	return nil
}

// close ends a firing; a firing that failed or expired goes back to the owner
// of the ritual with the reason.
func (s *Service) end(ctx context.Context, r Ritual, f Firing, to FiringState, reason, output string) error {
	changed, err := s.st.SetFiring(ctx, f.ID, to, reason, output, s.clk.Now())
	if err != nil || !changed || (to != Failed && to != Expired) {
		return err
	}
	text := fmt.Sprintf("O disparo do ritual %q (previsto para %s) %s: %s", r.Name, f.ScheduledFor.In(s.zone(ctx, r.Workspace)).Format("2006-01-02 15:04"),
		map[FiringState]string{Failed: "falhou", Expired: "foi cancelado"}[to], reason)
	if output != "" {
		text += "\n\n" + inbox.Say("Fim da saída de erro (da pré-execução, não verificada):", output, "|")
		if inbox.Multiline(output) {
			text += "\n" + inbox.QuoteNote
		}
	}
	if to == Expired {
		text += "\nRode `ritual run` para disparar de novo, ajuste o ritual ou aja de outra forma."
	} else {
		text += "\nAjuste o ritual (`ritual edit`) e rode `ritual run`."
	}
	s.tell(ctx, r, text)
	return nil
}

// tell puts an item `ritual` in the inbox of the owner of the ritual.
func (s *Service) tell(ctx context.Context, r Ritual, text string) {
	o, ok, err := s.owner(ctx, r)
	if err != nil || !ok {
		s.log.Warn("rituals: the owner is gone, the notice is dropped", "ritual", r.ID, "err", err)
		return
	}
	if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: r.Workspace, Target: o.ID, Kind: inbox.KindRitual, Sender: "ritual " + r.Name, Text: text}); err != nil {
		s.log.Warn("rituals: the notice was not queued", "ritual", r.ID, "err", err)
	}
}

func clockOf(t time.Time, loc *time.Location) string { return t.In(loc).Format("15:04") }

func shortDuration(d time.Duration) string {
	out := d.String()
	out = strings.TrimSuffix(out, "0s")
	return strings.TrimSuffix(out, "m0")
}

// deliver tries a firing against its target.
func (s *Service) deliver(ctx context.Context, f Firing, now time.Time) error {
	r, err := s.st.Ritual(ctx, f.Ritual)
	if err != nil {
		_, e := s.st.SetFiring(ctx, f.ID, Failed, "o ritual não existe mais", "", now)
		return e
	}
	if r.State == Disabled {
		_, err := s.st.SetFiring(ctx, f.ID, Skipped, "ritual desativado", "", now)
		return err
	}
	loc := s.zone(ctx, r.Workspace)
	if r.Action == ActionQuest {
		return s.deliverQuest(ctx, r, f, now, loc)
	}
	if f.State == PreRunning || f.State == Delivering {
		return s.resume(ctx, r, f)
	}
	c, err := s.target(ctx, r)
	if err != nil || c.State == characters.Stopped || c.State == characters.Stopping {
		return s.end(ctx, r, f, Failed, fmt.Sprintf("o alvo %q não existe mais", r.Target), "")
	}
	// The target was checked when the ritual was made; it may have changed
	// since (a role, a kind): check again before anything is sent.
	if err := s.checkTarget(ctx, &r); err != nil {
		return s.end(ctx, r, f, Failed, fmt.Sprintf("o alvo %q deixou de servir a este ritual: %v", r.Target, err), "")
	}
	paused := false
	if s.o.Pauses != nil {
		if paused, err = s.o.Pauses.Paused(ctx, r.Workspace, c.Table); err != nil {
			return err
		}
	}
	if paused || !c.State.Receives() {
		if now.Sub(f.CreatedAt) >= r.Timeout {
			why := fmt.Sprintf("%s ocupado (%s) desde %s, e o limite na fila é %s", c.Name, c.State, clockOf(f.CreatedAt, loc), shortDuration(r.Timeout))
			if paused {
				why = fmt.Sprintf("a mesa de %s está pausada desde %s, e o limite na fila é %s", c.Name, clockOf(f.CreatedAt, loc), shortDuration(r.Timeout))
			}
			return s.end(ctx, r, f, Expired, why, "")
		}
		return nil
	}
	content := r.Content
	sender := "ritual " + r.Name
	if r.PreRun != "" {
		// The intent goes to the database before the command runs: a crash
		// from here on never runs the pre-run (or the delivery) again.
		if ok, err := s.st.markPreRunning(ctx, f.ID, now); err != nil || !ok {
			return err
		}
		out, why, tail := s.preRun(ctx, c, r.PreRun)
		if why != "" {
			return s.end(ctx, r, f, Failed, why, tail)
		}
		if r.Action == ActionCommand {
			if bad := unsafeInShell(out); bad != "" {
				return s.end(ctx, r, f, Failed, "a saída da pré-execução tem "+bad+", que não vai para um comando; nada foi digitado", "")
			}
		}
		shown := out
		quotedAlone := false
		if r.Action != ActionCommand && strings.TrimSpace(out) != "" {
			// a placeholder alone on its line stands where the author's own words would:
			// the output is quoted there even when it is one line
			lines := strings.Split(content, "\n")
			for i, l := range lines {
				if strings.TrimSpace(l) == Placeholder {
					lines[i] = inbox.Quote(out)
					quotedAlone = true
				}
			}
			content = strings.Join(lines, "\n")
		}
		if r.Action != ActionCommand && inbox.Multiline(out) {
			// the output is text from outside: with more than one line it is quoted, so
			// it cannot pass for the words of the author of the ritual
			shown = "\n" + inbox.Quote(out) + "\n"
		}
		content = strings.ReplaceAll(content, Placeholder, shown)
		if r.Action != ActionCommand && strings.Contains(r.Content, Placeholder) {
			sender += " (" + UnverifiedOutput + ")"
			if shown != out || quotedAlone {
				content += "\n" + inbox.QuoteNote
			}
		}
	}
	switch r.Action {
	case ActionCommand:
		if strings.ContainsFunc(content, func(r rune) bool { return r < 0x20 && r != '\t' || r == 0x7f }) {
			return s.end(ctx, r, f, Failed, "o comando, com a saída da pré-execução, tem quebra de linha ou caractere de controle; nada foi digitado", "")
		}
		if s.o.Terminals == nil {
			return s.end(ctx, r, f, Failed, "os terminais não estão ao alcance deste motor", "")
		}
		if ok, err := s.st.markDelivering(ctx, f.ID, now); err != nil || !ok {
			return err
		}
		if err := s.o.Terminals.Input(ctx, c.Machine, c.ID, []byte(content+"\r")); err != nil {
			return s.end(ctx, r, f, Failed, "o terminal não recebeu o comando: "+err.Error(), "")
		}
	default:
		if f.Late {
			content = fmt.Sprintf("(disparo atrasado: previsto para %s)\n%s", f.ScheduledFor.In(loc).Format("2006-01-02 15:04"), content)
		}
		if ok, err := s.st.markDelivering(ctx, f.ID, now); err != nil || !ok {
			return err
		}
		if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: r.Workspace, Target: c.ID, Kind: inbox.KindRitual, Sender: sender, Text: content,
			Refs: inbox.Refs{Key: firingKey(f)}}); err != nil {
			return s.end(ctx, r, f, Failed, "a entrega falhou: "+err.Error(), "")
		}
	}
	return s.end(ctx, r, f, Delivered, "", "")
}

// UnverifiedOutput labels (in the sender of the item) a prompt that carries the
// output of a pre-run: that part came from a command, not from who wrote the
// ritual, so it is data and not an order.
const UnverifiedOutput = "saída da pré-execução: texto não verificado"

// firingKey is the idempotency key of the inbox item of a firing.
func firingKey(f Firing) string { return fmt.Sprintf("ritual-firing:%d", f.ID) }

// resume handles a firing found in the middle of its side effect (the engine
// stopped there). Nothing is run again: a prompt that reached the inbox is
// delivered (the item is found by its key) and one that did not and has no
// pre-run is queued once; a pre-run or a command that may have run is closed
// as failed, and the owner is told.
func (s *Service) resume(ctx context.Context, r Ritual, f Firing) error {
	if f.State == Delivering && r.Action == ActionPrompt && s.o.Inbox != nil {
		_, ok, err := s.o.Inbox.ByKey(ctx, firingKey(f))
		if err != nil {
			return err
		}
		if ok {
			return s.end(ctx, r, f, Delivered, "", "")
		}
		if r.PreRun == "" {
			c, err := s.target(ctx, r)
			if err != nil || c.State == characters.Stopped || c.State == characters.Stopping {
				return s.end(ctx, r, f, Failed, fmt.Sprintf("o alvo %q não existe mais", r.Target), "")
			}
			content := r.Content
			if f.Late {
				content = fmt.Sprintf("(disparo atrasado: previsto para %s)\n%s", f.ScheduledFor.In(s.zone(ctx, r.Workspace)).Format("2006-01-02 15:04"), content)
			}
			if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: r.Workspace, Target: c.ID, Kind: inbox.KindRitual, Sender: "ritual " + r.Name, Text: content,
				Refs: inbox.Refs{Key: firingKey(f)}}); err != nil {
				return s.end(ctx, r, f, Failed, "a entrega falhou: "+err.Error(), "")
			}
			return s.end(ctx, r, f, Delivered, "", "")
		}
	}
	why := "o motor parou no meio da pré-execução; ela pode ter rodado e não é repetida. Rode `ritual run` se ainda for preciso"
	if f.State == Delivering {
		why = "o motor parou no meio da entrega; o efeito pode ter acontecido e não é repetido. Confira o resultado e rode `ritual run` se ainda for preciso"
	}
	return s.end(ctx, r, f, Failed, why, "")
}

// unsafeInShell says what in the output of a pre-run must not go into a
// command line: a shell metacharacter or a C1 control (which some terminals
// read as an escape sequence). It returns "" when the output is plain.
func unsafeInShell(out string) string {
	for _, c := range out {
		switch {
		case c >= 0x80 && c <= 0x9f:
			return "um caractere de controle C1"
		case c < 0x20 || c == 0x7f:
			return "um caractere de controle"
		case strings.ContainsRune("`$&|;<>()[]{}\\\"'*?!~#^%\u2028\u2029", c):
			return fmt.Sprintf("o caractere %q (um metacaractere do shell)", c)
		}
	}
	return ""
}

// preRun runs the pre-run for the target after the decision of permissions. It
// returns the output, or why it failed with the tail of the error output.
func (s *Service) preRun(ctx context.Context, c characters.Character, command string) (out, why, tail string) {
	if s.o.PreRunner == nil || s.o.Permissions == nil {
		return "", "a pré-execução não está disponível neste motor; nada foi rodado", ""
	}
	dec, err := s.o.Permissions.Allow(ctx, c, command)
	switch {
	case err != nil:
		return "", "as permissões de " + c.Name + " não puderam decidir a pré-execução: " + err.Error(), ""
	case !dec.Allow:
		return "", fmt.Sprintf("a pré-execução foi negada pelas permissões de %s: %s", c.Name, dec.Reason), ""
	}
	res, err := s.o.PreRunner.Run(ctx, PreRunRequest{Machine: c.Machine, Dir: c.Dir, Command: command, Character: c.ID,
		Timeout: s.o.PreRunTimeout, MaxOutput: s.o.PreRunOutput})
	switch {
	case err != nil:
		return "", "a pré-execução não rodou: " + err.Error(), ""
	case res.TimedOut:
		return "", "a pré-execução passou do tempo limite de " + shortDuration(s.o.PreRunTimeout), lastBytes(res.Stderr, stderrTail)
	case res.ExitCode != 0:
		return "", fmt.Sprintf("a pré-execução saiu com o código %d", res.ExitCode), lastBytes(res.Stderr, stderrTail)
	}
	o := res.Output
	if len(o) > s.o.PreRunOutput {
		o = o[:s.o.PreRunOutput]
	}
	return strings.TrimRight(o, "\r\n"), "", ""
}

func lastBytes(s string, n int) string {
	if len(s) <= n {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(strings.ToValidUTF8(s[len(s)-n:], ""))
}

// deliverQuest starts the quest of a ritual; a quest that is under way counts
// as a busy target until it ends.
func (s *Service) deliverQuest(ctx context.Context, r Ritual, f Firing, now time.Time, loc *time.Location) error {
	if s.o.Quests == nil {
		return s.end(ctx, r, f, Failed, "as missões não estão ao alcance deste motor", "")
	}
	q, err := s.o.Quests.Get(ctx, r.Workspace, r.Table, r.Quest)
	if err != nil {
		return s.end(ctx, r, f, Failed, fmt.Sprintf("a missão %q não existe mais", r.Quest), "")
	}
	if q.State == quests.Cancelled {
		return s.end(ctx, r, f, Failed, fmt.Sprintf("a missão %q foi cancelada", r.Quest), "")
	}
	paused := false
	if s.o.Pauses != nil {
		if paused, err = s.o.Pauses.Paused(ctx, r.Workspace, r.Table); err != nil {
			return err
		}
	}
	if paused || q.State == quests.Running || q.State == quests.Paused {
		if now.Sub(f.CreatedAt) >= r.Timeout {
			why := fmt.Sprintf("a missão %q segue %s desde %s, e o limite na fila é %s", r.Quest, q.State, clockOf(f.CreatedAt, loc), shortDuration(r.Timeout))
			if paused {
				why = fmt.Sprintf("a mesa está pausada desde %s, e o limite na fila é %s", clockOf(f.CreatedAt, loc), shortDuration(r.Timeout))
			}
			return s.end(ctx, r, f, Expired, why, "")
		}
		return nil
	}
	if _, err := s.o.Quests.Run(ctx, q.ID, ""); err != nil {
		return s.end(ctx, r, f, Failed, "a missão não iniciou: "+err.Error(), "")
	}
	return s.end(ctx, r, f, Delivered, "", "")
}

// StateChanged is a characters.Observer: a character that becomes free may
// have firings waiting for it; the loop looks again at once.
func (s *Service) StateChanged(_ context.Context, c characters.Character, _ characters.State) error {
	if c.State.Receives() {
		select {
		case s.kick <- struct{}{}:
		default:
		}
	}
	return nil
}

// Done and TurnEnded complete characters.Observer: they do nothing here.
func (s *Service) Done(context.Context, characters.Character, string) error { return nil }

// TurnEnded does nothing: a firing waits for the state, not for the turn.
func (s *Service) TurnEnded(context.Context, characters.Character, string) error { return nil }

// Loop looks at the clock every `every` (and when a target becomes free) until
// the context ends. Every must be well below MinEvery.
func (s *Service) Loop(ctx context.Context, every time.Duration) error {
	if every <= 0 || every > MinEvery/5 {
		every = time.Minute
	}
	tk := s.clk.NewTicker(every)
	defer tk.Stop()
	for {
		if err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("rituals: tick", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tk.C():
		case <-s.kick:
		}
	}
}
