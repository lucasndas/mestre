package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/localchan"
)

// Statuses of a check of `mestre debug`.
const (
	StatusOK      = "ok"
	StatusFail    = "fail"
	StatusSkipped = "skipped"
)

// Check is one item of `mestre debug`.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Hint   string `json:"hint,omitempty"`
	// exit is the exit code a failure gives.
	exit int
}

// runDebug is `mestre debug` (docs/cli/convencoes.md#ajuda-e-diagnóstico):
// the items in order, each ok or fail with a hint; exit 0 when all pass,
// otherwise the code of the first failure. Item 5, whether the engine
// reaches the current Citadel, needs the protocol of M2 and is skipped.
func runDebug(ctx context.Context, env Env, p *parsed) int {
	var checks []Check
	checks = append(checks, checkVersion(env))
	checks = append(checks, checkEnv(env))

	agent := env.Getenv(localchan.EnvAgentID)
	c3, c4 := checkEngine(ctx, env, agent)
	checks = append(checks, c3, c4)
	checks = append(checks, Check{Name: "citadel", Status: StatusSkipped,
		Detail: "needs the Citadel connection, which arrives with the protocol between machines (M2)"})

	code := localchan.ExitOK
	var first *Check
	for i := range checks {
		if checks[i].Status == StatusFail {
			first = &checks[i]
			code = checks[i].exit
			break
		}
	}
	o := out{env: env, json: p.json}
	if p.json {
		resp := localchan.OK(map[string]any{"checks": checks})
		if first != nil {
			data := resp.Data
			resp = localchan.Fail(failCode(first), first.Name+": "+first.Detail, first.Hint)
			resp.Data = data
		}
		o.printJSON(resp)
		return code
	}
	for i, c := range checks {
		fmt.Fprintf(env.Stdout, "%d. %-8s %-7s %s\n", i+1, c.Name, c.Status, c.Detail)
		if c.Status == StatusFail && c.Hint != "" {
			fmt.Fprintf(env.Stdout, "   hint: %s\n", c.Hint)
		}
	}
	return code
}

// failCode is the error code of a failed check.
func failCode(c *Check) string {
	switch c.Name {
	case "engine":
		return localchan.CodeEngineUnreachable
	case "identity":
		return localchan.CodeNotIdentified
	}
	return localchan.CodeUsage
}

func checkVersion(env Env) Check {
	return Check{Name: "version", Status: StatusOK,
		Detail: fmt.Sprintf("mestre %s, local channel protocol v%d", env.Version, localchan.Version)}
}

// checkEnv looks at the three variables of a character. An absent one is
// reported, not an error (a person in a shell has none of them, and the
// terminals made by `spawn` have no MESTRE_AGENT_ID); a set one that cannot
// be right is.
func checkEnv(env Env) Check {
	c := Check{Name: "env", Status: StatusOK}
	var notes []string
	if env.Getenv(localchan.EnvAgentID) == "" {
		notes = append(notes, localchan.EnvAgentID+" is not set (only help, debug and hook work)")
	}
	if v := env.Getenv(localchan.EnvEndpoint); v == "" {
		notes = append(notes, localchan.EnvEndpoint+" is not set (using the principal instance)")
	} else if _, err := localchan.ParseEndpoint(v); err != nil {
		return Check{Name: "env", Status: StatusFail, exit: localchan.ExitUsage,
			Detail: err.Error(), Hint: "the engine puts the right endpoint in the environment of each character"}
	}
	if v := env.Getenv(localchan.EnvCLI); v == "" {
		notes = append(notes, localchan.EnvCLI+" is not set")
	} else if !env.FileExists(v) {
		return Check{Name: "env", Status: StatusFail, exit: localchan.ExitInternal,
			Detail: localchan.EnvCLI + " points to " + v + ", which does not exist",
			Hint:   "reinstall mestre or restart the character"}
	}
	c.Detail = "present and coherent"
	if len(notes) > 0 {
		c.Detail = strings.Join(notes, "; ")
	}
	return c
}

// checkEngine is items 3 and 4: the engine answers on the endpoint (with
// the time it took) and accepts the identification (skipped without an id).
func checkEngine(ctx context.Context, env Env, agent string) (c3, c4 Check) {
	c3 = Check{Name: "engine"}
	c4 = Check{Name: "identity"}
	cl, err := client(env, -1)
	if err != nil {
		c3.Status, c3.Detail, c3.exit = StatusFail, err.Error(), localchan.ExitUnavailable
		c3.Hint = "run mestre debug after fixing MESTRE_ENDPOINT"
		c4.Status, c4.Detail = StatusSkipped, "needs the engine"
		return c3, c4
	}
	start := env.Clock.Now()
	resp, err := cl.Call(ctx, localchan.Request{AgentID: agent, Cmd: localchan.CmdDebug, Cwd: env.Cwd}, localchan.CallOptions{})
	took := env.Clock.Now().Sub(start)
	if err != nil {
		c3.Status, c3.Detail, c3.exit = StatusFail, "interrupted: "+err.Error(), localchan.ExitInternal
		c4.Status, c4.Detail = StatusSkipped, "needs the engine"
		return c3, c4
	}
	if !resp.OK {
		c3.Status, c3.exit = StatusFail, resp.ExitCode()
		c3.Detail = resp.Error.Message
		c3.Hint = resp.Error.Hint
		if c3.Hint == "" {
			c3.Hint = "the Guardian restarts the engine"
		}
		c4.Status, c4.Detail = StatusSkipped, "needs the engine"
		return c3, c4
	}
	var data struct {
		Engine struct {
			Version  string `json:"version"`
			Protocol int    `json:"protocol"`
		} `json:"engine"`
		Caller struct {
			Identified bool `json:"identified"`
		} `json:"caller"`
	}
	if err := resp.DecodeData(&data); err != nil {
		c3.Status, c3.Detail, c3.exit = StatusFail, "the engine answered with data this mestre cannot read: "+err.Error(), localchan.ExitInternal
		c4.Status, c4.Detail = StatusSkipped, "needs the engine"
		return c3, c4
	}
	c3.Status = StatusOK
	c3.Detail = fmt.Sprintf("answered in %s (engine %s, protocol v%d)", took, data.Engine.Version, data.Engine.Protocol)
	switch {
	case agent == "":
		c4.Status, c4.Detail = StatusSkipped, localchan.EnvAgentID+" is not set"
	case data.Caller.Identified:
		c4.Status, c4.Detail = StatusOK, "the engine accepts this process as a process of the terminal of "+agent
	default:
		c4.Status, c4.exit = StatusFail, localchan.ExitDenied
		c4.Detail = "the engine does not accept this process as a process of the terminal of " + agent
		c4.Hint = "run mestre inside the terminal of that character; another terminal or a shell is refused"
	}
	return c3, c4
}
