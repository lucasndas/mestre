package characters

import (
	"context"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/terminal"
)

// HookRunner is a Process that can run the hooks of the class as its agent:
// the terminal asks its mestre-pty, which is the account of the agent
// (docs/arquitetura/terminal.md, message hook). A process that is not one
// cannot run a hook, and the hook does not run: it is never run as another
// account.
type HookRunner interface {
	RunHook(ctx context.Context, run, dir, event string, stdin []byte, timeout time.Duration) (terminal.HookResult, error)
}

// RunHook implements HookRunner.
func (p *terminalProcess) RunHook(ctx context.Context, run, dir, event string, stdin []byte, timeout time.Duration) (terminal.HookResult, error) {
	return p.t.Client().RunHook(ctx, run, dir, event, stdin, timeout)
}

// HookExec is the hook.HookExec of the machine: the asynchronous hooks of the
// classes run in the terminal of the character, as the character.
func (rt *Runtime) HookExec() hook.HookExec { return runtimeHooks{rt} }

type runtimeHooks struct{ rt *Runtime }

// Exec implements hook.HookExec.
func (h runtimeHooks) Exec(ctx context.Context, agentID string, ah hook.AsyncHook, event providers.EventKind, stdin []byte) (hook.HookResult, error) {
	s := h.rt.session(agentID)
	if s == nil {
		return hook.HookResult{}, noProcess(agentID)
	}
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	runner, ok := proc.(HookRunner)
	if !ok {
		return hook.HookResult{}, fmt.Errorf("the terminal of %s cannot run a hook as its agent", agentID)
	}
	res, err := runner.RunHook(ctx, ah.Run, ah.Dir, string(event), stdin, time.Duration(ah.Timeout)*time.Second)
	if err != nil {
		return hook.HookResult{}, err
	}
	if res.TimedOut {
		return hook.HookResult{}, fmt.Errorf("the hook %q ran out of its %d s", ah.Name, ah.Timeout)
	}
	return hook.HookResult{Exit: res.Exit, Output: res.Output}, nil
}
