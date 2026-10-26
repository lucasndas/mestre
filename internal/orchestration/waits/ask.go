package waits

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
)

// AskRequest is `mestre ask "<Name>" "<prompt>"`.
type AskRequest struct {
	From, To characters.Character
	Prompt   string
	// Timeout defaults to the one of the workspace (90 s) and is at most 9
	// minutes.
	Timeout time.Duration
	// NoWait delivers and returns at once.
	NoWait bool
	// Gone is closed when the call of the CLI that waits for the answer is
	// gone; the ask stays open and its answer goes to the inbox.
	Gone <-chan struct{}
	// Progress hears `queued`, `delivered` and `answering` while the call
	// waits (nil: nobody listens).
	Progress func(status string)
}

// AskStatus says how an ask ended for the caller.
type AskStatus string

// The outcomes.
const (
	// AskAnswered: the answer came in time.
	StatusAnswered AskStatus = "answered"
	// StatusPending: the time limit ran out. The ask stays open, the answer comes
	// to the inbox, and the request is never sent again.
	StatusPending AskStatus = "pending"
	// StatusSent: delivered, without waiting (--no-wait).
	StatusSent AskStatus = "sent"
	// StatusDelivered: an ask back, which resolved a request of the target (or a
	// wait for it) and returns at once.
	StatusDelivered AskStatus = "delivered"
	// StatusEnded: the target ended before it answered.
	StatusEnded AskStatus = "cancelled"
)

// AskOutcome is the result of Ask.
type AskOutcome struct {
	ID       string
	To       string
	Status   AskStatus
	Response string
}

type askResult struct {
	Response  string
	Cancelled bool
}

// Ask puts the prompt in the inbox of the target and, unless NoWait, waits for
// the answer: the done of the turn that handled it, or the last message of the
// agent in it. When the time limit runs out the ask returns pending: it stays
// open and the answer comes to the inbox of the asker as an `answer` item, never
// sent again by the engine.
//
// An ask to a character that has an open ask to the caller is the "ask back":
// it is the answer of the caller to that request (or the result a wait of the
// target was waiting for), and it returns at once.
func (s *Service) Ask(ctx context.Context, r AskRequest) (AskOutcome, error) {
	if r.From.ID == r.To.ID {
		return AskOutcome{}, fmt.Errorf("%w: %s", ErrSelf, r.To.Name)
	}
	out := AskOutcome{To: r.To.Name}
	// Ask back: the caller answers the request the target left open for it.
	open, err := s.st.OpenAsksTo(ctx, r.From.ID, r.To.ID)
	if err != nil {
		return out, err
	}
	if len(open) > 0 {
		out.ID, out.Status = open[0].ID, StatusDelivered
		s.resolveAsk(ctx, open[0], r.Prompt)
		s.targetDone(ctx, KindCharacter, r.From.ID, r.To.ID, r.Prompt)
		return out, nil
	}
	if s.targetDone(ctx, KindCharacter, r.From.ID, r.To.ID, r.Prompt) > 0 {
		out.Status = StatusDelivered
		return out, nil
	}
	tm := s.timeouts(ctx, r.From.Workspace)
	timeout := r.Timeout
	switch {
	case timeout <= 0:
		timeout = tm.Ask
	case timeout > tm.MaxAsk:
		timeout = tm.MaxAsk
	}
	table := r.To.Table
	a, err := s.st.InsertAsk(ctx, Ask{Workspace: r.From.Workspace, Table: table, Asker: r.From.ID, Target: r.To.ID, Blocking: !r.NoWait, CreatedAt: s.clk.Now()})
	if err != nil {
		return out, err
	}
	out.ID = a.ID
	var ch chan askResult
	if !r.NoWait {
		ch = make(chan askResult, 1)
		s.mu.Lock()
		s.blockers[a.ID] = ch
		if r.Progress != nil {
			s.progress[a.ID] = r.Progress
			defer func() {
				s.mu.Lock()
				delete(s.progress, a.ID)
				delete(s.reached, a.ID)
				s.mu.Unlock()
			}()
		}
		s.mu.Unlock()
		if r.Progress != nil {
			r.Progress("queued")
		}
	}
	item, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: r.From.Workspace, Target: r.To.ID, Kind: inbox.KindAgent, Sender: r.From.Name, SenderID: r.From.ID,
		Text: r.Prompt, Refs: inbox.Refs{Ask: a.ID, Table: table}})
	if err != nil {
		s.endAsk(ctx, a, AskCancelled, "")
		return out, err
	}
	if err := s.st.SetAskItem(ctx, a.ID, item.ID); err != nil {
		s.log.Warn("waits: the item of an ask was not recorded", "ask", a.ID, "err", err)
	}
	if r.NoWait {
		out.Status = StatusSent
		return out, nil
	}
	s.detect(ctx, r.From.ID)
	timer := s.clk.NewTimer(timeout)
	defer timer.Stop()
	var cause string
	select {
	case res := <-ch:
		return s.finish(out, res), nil
	case <-timer.C():
		cause = "timeout"
	case <-r.Gone:
		cause = "gone"
	case <-ctx.Done():
		cause = "gone"
	}
	// Nobody waits any more. If the answer came at the same moment, it is the
	// answer of this call; otherwise the ask stays open without a caller.
	s.mu.Lock()
	_, still := s.blockers[a.ID]
	delete(s.blockers, a.ID)
	s.mu.Unlock()
	if !still {
		return s.finish(out, <-ch), nil
	}
	var at time.Time
	if cause == "timeout" {
		at = s.clk.Now()
	}
	if err := s.st.Detach(ctx, a.ID, at); err != nil {
		s.log.Warn("waits: an ask was not detached", "ask", a.ID, "err", err)
	}
	s.refreshCycles(ctx)
	out.Status = StatusPending
	return out, nil
}

func (s *Service) finish(out AskOutcome, res askResult) AskOutcome {
	if res.Cancelled {
		out.Status, out.Response = StatusEnded, res.Response
		return out
	}
	out.Status, out.Response = StatusAnswered, res.Response
	return out
}

// resolveAsk answers an open ask. A call of the CLI that still waits gets the
// answer; otherwise it goes to the inbox of the asker, whole, as an `answer`.
func (s *Service) resolveAsk(ctx context.Context, a Ask, response string) {
	s.wmu.Lock()
	ok, err := s.st.EndAsk(ctx, a.ID, AskAnswered, response, s.clk.Now())
	s.wmu.Unlock()
	if err != nil {
		s.log.Warn("waits: an answer was not recorded", "ask", a.ID, "err", err)
		return
	}
	if !ok {
		return
	}
	s.mu.Lock()
	ch := s.blockers[a.ID]
	delete(s.blockers, a.ID)
	s.mu.Unlock()
	if ch != nil {
		ch <- askResult{Response: response}
		s.refreshCycles(ctx)
		return
	}
	s.answerLater(ctx, a, response)
	s.refreshCycles(ctx)
}

// answerLater puts the answer of an ask nobody waits for in the inbox of the
// asker.
func (s *Service) answerLater(ctx context.Context, a Ask, response string) {
	from := "alvo"
	if t, err := s.o.Registry.Get(ctx, a.Target); err == nil {
		from = t.Name
	}
	asker, err := s.o.Registry.Get(ctx, a.Asker)
	if err != nil || !asker.State.Alive() {
		return
	}
	text := fmt.Sprintf("Resposta de %s ao seu pedido %s:\n\n%s", from, a.ID, response)
	if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: a.Workspace, Target: a.Asker, Kind: inbox.KindAnswer, Sender: from, SenderID: a.Target, Text: text,
		Refs: inbox.Refs{Ask: a.ID, Table: a.Table}}); err != nil {
		s.log.Warn("waits: a late answer was not queued", "ask", a.ID, "err", err)
	}
}

// endAsk closes an ask without an answer: whoever waits is released, and the
// asker is told with text when it has one.
func (s *Service) endAsk(ctx context.Context, a Ask, to AskState, text string) {
	s.wmu.Lock()
	ok, err := s.st.EndAsk(ctx, a.ID, to, "", s.clk.Now())
	s.wmu.Unlock()
	if err != nil || !ok {
		return
	}
	s.mu.Lock()
	ch := s.blockers[a.ID]
	delete(s.blockers, a.ID)
	s.mu.Unlock()
	if ch != nil {
		ch <- askResult{Response: text, Cancelled: true}
	} else if text != "" {
		if asker, err := s.o.Registry.Get(ctx, a.Asker); err == nil && asker.State.Alive() {
			if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: a.Workspace, Target: a.Asker, Kind: inbox.KindSystem, Sender: inbox.SystemSender, Text: text,
				Refs: inbox.Refs{Ask: a.ID, Table: a.Table}}); err != nil {
				s.log.Warn("waits: the end of an ask was not told", "ask", a.ID, "err", err)
			}
		}
	}
	s.refreshCycles(ctx)
}

// TurnEnded implements characters.Observer: the turn of a character that
// handled an ask ends with its answer, the done of the turn or, without one,
// the last message of the agent.
func (s *Service) TurnEnded(ctx context.Context, c characters.Character, lastMessage string) error {
	s.mu.Lock()
	done, hasDone := s.dones[c.ID]
	delete(s.dones, c.ID)
	s.mu.Unlock()
	asks, err := s.st.OpenAsksTo(ctx, c.ID, "")
	if err != nil {
		return err
	}
	for _, a := range asks {
		// by the ref of the item, not by a.Item: the ask records its item after
		// the enqueue, which delivers at once, and a fast turn can end before that
		it, found, err := s.o.Inbox.Store().ByAsk(ctx, a.ID)
		if err != nil || !found || (it.State != inbox.Delivering && it.State != inbox.Delivered) {
			continue // this turn was not the one of the ask
		}
		answer := lastMessage
		if hasDone {
			answer = done
		}
		if strings.TrimSpace(answer) == "" {
			answer = "(o personagem encerrou o turno sem texto de resposta)"
		}
		s.resolveAsk(ctx, a, answer)
		// The turn that handled the ask answered it, with or without a done:
		// the task is over.
		if err := s.o.Registry.SetTaskOpen(ctx, c.ID, false); err != nil {
			s.log.Warn("waits: a task was not closed", "character", c.ID, "err", err)
		}
		break // one item, one turn
	}
	return nil
}

// AskProgress implements inbox.AskProgress: the call of the CLI that waits for
// the ask, if there is one, hears how far the item of the ask went.
func (s *Service) AskProgress(_ context.Context, ask, status string) {
	rank := map[string]int{"queued": 1, "delivered": 2, "answering": 3}[status]
	s.mu.Lock()
	f := s.progress[ask]
	if f == nil || rank <= s.reached[ask] { // a status never goes back: a fast turn can confirm before the delivery returns
		s.mu.Unlock()
		return
	}
	s.reached[ask] = rank
	s.mu.Unlock()
	f(status)
}
