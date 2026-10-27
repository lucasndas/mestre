package hook_test

import (
	"context"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

type noHooks struct{}

func (noHooks) Async(context.Context, providers.Event) ([]hook.AsyncHook, []string, error) {
	return nil, nil, nil
}

// An event that arrives while the runner is being waited for (the Tower
// stopping) is not a data race, and Wait returns once the queues are empty.
func TestAnEventThatArrivesWhileTheRunnerIsWaitedForIsNotARace(t *testing.T) {
	t.Parallel()
	for range 200 {
		ar := &hook.AsyncRunner{Source: noHooks{}, Exec: &fakeExec{}, Out: &outputs{}, Clock: clock.Real{}}
		ar.Start(t.Context())
		var wg sync.WaitGroup
		wg.Go(ar.Wait)
		for range 3 {
			wg.Go(func() {
				ar.OnEvent(providers.Event{Version: 1, Provider: providers.ClaudeCode, Event: providers.EventTurnEnd, AgentID: "a"}, hook.Answer{})
			})
		}
		wg.Wait()
		ar.Wait()
	}
}
