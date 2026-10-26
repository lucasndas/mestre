package app

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/mestre-ia/mestre/internal/github"
)

// epicClosers is what the epic sink tells: *epicdeps.Service.
type epicClosers interface {
	EpicClosed(ctx context.Context, workspace string, epic int, notPlanned bool) error
}

// epicClosedSink is the github.Sink that tells the dependencies between epics when
// an epic is closed straight on GitHub (by a person, not by `mestre gh epic update
// --state closed`): closed as completed it counts as merged work, as not planned
// the tables that wait for it stay held until the Grand Master decides
// (docs/orquestracao/dependencia-entre-mesas.md). Telling the same closure twice is
// harmless, as a Sink must be.
type epicClosedSink struct{ deps epicClosers }

var _ github.Sink = epicClosedSink{}

// Deliver implements github.Sink.
func (s epicClosedSink) Deliver(ctx context.Context, w github.Watch, _ string, events []github.Event) error {
	var errs []error
	for _, ev := range events {
		if ev.Kind != github.EventIssueChanged || !ev.Closed || !slices.ContainsFunc(ev.Labels, func(l string) bool { return strings.EqualFold(strings.TrimSpace(l), github.EpicLabel) }) {
			continue
		}
		errs = append(errs, s.deps.EpicClosed(ctx, w.Workspace, ev.Number, ev.NotPlanned))
	}
	return errors.Join(errs...)
}
