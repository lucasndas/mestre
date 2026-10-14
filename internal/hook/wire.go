package hook

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
)

// Defaults of the dispatcher (docs/provedores/despachante-de-hooks.md#uma-chamada).
const (
	// DecisionDeadline is how long the dispatcher waits for the engine's
	// decision (pre_tool, permission_request).
	DecisionDeadline = 800 * time.Millisecond
	// InfoDeadline is how long it waits for the engine to acknowledge an
	// informative event, which the engine processes afterwards.
	InfoDeadline = 300 * time.Millisecond
	// MaxStdin is the most of the payload the dispatcher reads.
	MaxStdin = 4 << 20
)

// Verdicts of an Answer.
const (
	VerdictAllow   = "allow"
	VerdictDeny    = "deny"
	VerdictNeutral = "neutral"
)

// Answer is the data of the engine's response to `cmd: "hook"`.
type Answer struct {
	// Verdict is allow or deny for pre_tool and permission_request, and
	// neutral for the informative events; anything else is a denial.
	Verdict string `json:"verdict"`
	// Reason is what the agent sees with a denial.
	Reason string `json:"reason,omitempty"`
	// Context is the context of session_start and turn_start.
	Context string `json:"context,omitempty"`
	// Request is the number of the approval request a denial opened.
	Request int64 `json:"request,omitempty"`
	// ClassHooks are the synchronous hooks of the class the dispatcher runs
	// now (docs/classes/classes.md#hooks-extras-da-classe): in pre_tool,
	// only after an allow, and they can only deny.
	ClassHooks []ClassHook `json:"class_hooks,omitempty"`
}

// ClassHook is one synchronous hook of the class, as the engine sends it:
// the command already for this system, its folder (the worktree) and its
// time limit, already cut to 2 s below the provider's.
type ClassHook struct {
	Name      string `json:"name"`
	Run       string `json:"run"`
	Dir       string `json:"dir"`
	TimeoutMS int64  `json:"timeout_ms"`
	// Blocking makes a failure or a timeout deny, in pre_tool.
	Blocking bool `json:"blocking,omitempty"`
	// Refused is why the hook must not run: its `run` does not pass the
	// permission pipeline of the class, like a command of the character.
	// It counts as a failure of the hook (it denies when it is blocking).
	Refused string `json:"refused,omitempty"`
}

// Timeout is the time limit of the hook.
func (h ClassHook) Timeout() time.Duration { return time.Duration(h.TimeoutMS) * time.Millisecond }

// CharacterEnv is env.json, next to policy.json in the folder of the
// character: what the degraded decision knows about the character besides
// its rules (classes.Env without the live state). The engine writes it
// when it learns the character's environment; the dispatcher only reads it.
type CharacterEnv struct {
	Version         int      `json:"version"`
	AgentID         string   `json:"agent_id"`
	TableID         string   `json:"table_id,omitempty"`
	Worktree        string   `json:"worktree"`
	Tmp             string   `json:"tmp,omitempty"`
	Home            string   `json:"home,omitempty"`
	OperatorBase    bool     `json:"operator_base,omitempty"`
	ComputerControl []string `json:"computer_control,omitempty"`
}

// EnvName is the file of the environment snapshot of a character.
const EnvName = "env.json"

// EnvVersion is the version of env.json.
const EnvVersion = 1

func envOf(agentID string, e classes.Env) CharacterEnv {
	return CharacterEnv{
		Version: EnvVersion, AgentID: agentID, TableID: e.TableID, Worktree: e.Worktree, Tmp: e.Tmp, Home: e.Home,
		OperatorBase: e.OperatorBase, ComputerControl: e.ComputerControl,
	}
}

func parseEnv(data []byte, agentID string) (CharacterEnv, error) {
	var c CharacterEnv
	if err := json.Unmarshal(data, &c); err != nil {
		return CharacterEnv{}, fmt.Errorf("env.json: %w", err)
	}
	switch {
	case c.Version != EnvVersion:
		return CharacterEnv{}, fmt.Errorf("env.json: version %d", c.Version)
	case c.AgentID != agentID:
		return CharacterEnv{}, fmt.Errorf("env.json is of another character")
	}
	return c, nil
}
