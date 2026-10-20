package app

import (
	"context"
	"testing"

	"github.com/lucasndas/mestre/internal/github"
)

type closedCall struct {
	workspace  string
	epic       int
	notPlanned bool
}

type fakeClosers struct{ calls []closedCall }

func (f *fakeClosers) EpicClosed(_ context.Context, ws string, epic int, notPlanned bool) error {
	f.calls = append(f.calls, closedCall{ws, epic, notPlanned})
	return nil
}

// Part of CA-DEPMESA-05: an epic closed straight on GitHub tells the dependencies, as
// completed or as not planned; an open one, a closed issue that is not an epic and a
// pull request do not.
func TestAnEpicClosedOnGitHubTellsTheDependenciesTheWayItWasClosed(t *testing.T) {
	t.Parallel()
	f := &fakeClosers{}
	w := github.Watch{Workspace: "meu-app", Repo: "lucas/meu-app"}
	err := epicClosedSink{f}.Deliver(t.Context(), w, "", []github.Event{
		{Kind: github.EventIssueChanged, Number: 12, Closed: true, Labels: []string{"epico"}},
		{Kind: github.EventIssueChanged, Number: 13, Closed: true, NotPlanned: true, Labels: []string{"Epico", "x"}},
		{Kind: github.EventIssueChanged, Number: 14, Closed: false, Labels: []string{"epico"}},
		{Kind: github.EventIssueChanged, Number: 15, Closed: true, Labels: []string{"bug"}},
		{Kind: github.EventPRClosed, Number: 16, Closed: true, Labels: []string{"epico"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []closedCall{{"meu-app", 12, false}, {"meu-app", 13, true}}
	if len(f.calls) != len(want) || f.calls[0] != want[0] || f.calls[1] != want[1] {
		t.Errorf("EpicClosed was called with %+v; want %+v", f.calls, want)
	}
}
