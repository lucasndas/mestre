package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

type spyRefresher struct{ calls []string }

func (s *spyRefresher) Refresh(_ context.Context, id string, d sessionzero.Dynamic) (sessionzero.RefreshReport, error) {
	s.calls = append(s.calls, id+":"+d.Instructions)
	return sessionzero.RefreshReport{}, nil
}

type spyInner struct{ calls int }

func (s *spyInner) Context(context.Context, providers.Event) (string, error) {
	s.calls++
	return "inner", nil
}

func TestTheLayerIsBroughtUpToDateOnlyAtTheStartOfATurnAndNeverHoldsIt(t *testing.T) {
	t.Parallel()
	e := &Engine{log: slog.New(slog.DiscardHandler)}
	r, in := &spyRefresher{}, &spyInner{}
	c := refreshingContexts{inner: in, r: r, e: e}
	turn := providers.Event{Event: providers.EventTurnStart, AgentID: "gm"}

	// No Citadel that acts: the turn goes on with the layer as it is.
	if got, err := c.Context(t.Context(), turn); got != "inner" || err != nil || len(r.calls) != 0 {
		t.Fatalf("without a period: %q, %v, refreshed %v", got, err, r.calls)
	}
	fail := true
	e.period.Store(&periodHooks{layers: func(context.Context, string) (sessionzero.Dynamic, error) {
		if fail {
			return sessionzero.Dynamic{}, errors.New("db down")
		}
		return sessionzero.Dynamic{Instructions: "x"}, nil
	}})
	if got, err := c.Context(t.Context(), turn); got != "inner" || err != nil || len(r.calls) != 0 {
		t.Fatalf("with a Citadel that cannot answer: %q, %v, refreshed %v", got, err, r.calls)
	}
	fail = false
	if _, err := c.Context(t.Context(), providers.Event{Event: providers.EventPreTool, AgentID: "gm"}); err != nil || len(r.calls) != 0 {
		t.Fatalf("an event in the middle of a turn refreshed: %v, %v", r.calls, err)
	}
	if got, err := c.Context(t.Context(), turn); got != "inner" || err != nil || len(r.calls) != 1 || r.calls[0] != "gm:x" {
		t.Fatalf("the start of the turn: %q, %v, refreshed %v", got, err, r.calls)
	}
	if in.calls != 4 {
		t.Errorf("the inner contexts were asked %d times; want every time", in.calls)
	}
}
