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
	// exit is the exit code a failure gives, code its error code.
	exit int
	code string
}

// runDebug is `mestre debug` (docs/cli/convencoes.md#ajuda-e-diagnóstico):
// the items in order, each ok or fail with a hint; exit 0 when all pass,
// otherwise the code of the first failure. Item 5, whether the engine
// reaches the current Citadel with a compatible protocol and epoch, is the
// answer of the engine itself.
func runDebug(ctx context.Context, env Env, p *parsed) int {
	var checks []Check
	checks = append(checks, checkVersion(env))
	checks = append(checks, checkEnv(env))

	agent := env.Getenv(localchan.EnvAgentID)
	c3, c4, c5 := checkEngine(ctx, env, agent)
	checks = append(checks, c3, c4, c5)

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
	if c.code != "" {
		return c.code
	}
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

// checkEngine is items 3, 4 and 5: the engine answers on the endpoint (with
// the time it took), accepts the identification (skipped without an id) and
// reaches the current Citadel.
func checkEngine(ctx context.Context, env Env, agent string) (c3, c4, c5 Check) {
	c3 = Check{Name: "engine"}
	c4 = Check{Name: "identity"}
	c5 = Check{Name: "citadel"}
	skip := func(why string) {
		c4.Status, c4.Detail = StatusSkipped, why
		c5.Status, c5.Detail = StatusSkipped, why
	}
	cl, err := client(env, -1)
	if err != nil {
		c3.Status, c3.Detail, c3.exit = StatusFail, err.Error(), localchan.ExitUnavailable
		c3.Hint = "fix MESTRE_ENDPOINT and run mestre debug again"
		skip("needs the engine")
		return c3, c4, c5
	}
	start := env.Clock.Now()
	resp, err := cl.Call(ctx, localchan.Request{AgentID: agent, Cmd: localchan.CmdDebug, Cwd: env.Cwd}, localchan.CallOptions{})
	took := env.Clock.Now().Sub(start)
	if err != nil {
		c3.Status, c3.Detail, c3.exit = StatusFail, "interrupted: "+err.Error(), localchan.ExitInternal
		skip("needs the engine")
		return c3, c4, c5
	}
	if !resp.OK {
		c3.Status, c3.exit, c3.code = StatusFail, resp.ExitCode(), resp.Error.Code
		c3.Detail = resp.Error.Message
		c3.Hint = resp.Error.Hint
		if c3.Hint == "" {
			c3.Hint = "the Guardian restarts the engine"
		}
		skip("needs the engine")
		return c3, c4, c5
	}
	var data struct {
		Engine struct {
			Version  string `json:"version"`
			Protocol int    `json:"protocol"`
		} `json:"engine"`
		Caller struct {
			Identified bool `json:"identified"`
		} `json:"caller"`
		Citadel *struct {
			Reachable bool   `json:"reachable"`
			Detail    string `json:"detail"`
			Code      string `json:"code"`
			Protocol  int    `json:"protocol"`
			Epoch     uint64 `json:"epoch"`
		} `json:"citadel"`
	}
	if err := resp.DecodeData(&data); err != nil {
		c3.Status, c3.Detail, c3.exit = StatusFail, "the engine answered with data this mestre cannot read: "+err.Error(), localchan.ExitInternal
		c3.Hint = "this mestre and the engine are of different versions; update mestre"
		skip("needs the engine")
		return c3, c4, c5
	}
	c3.Status = StatusOK
	c3.Detail = fmt.Sprintf("answered in %s (engine %s, protocol v%d)", took, data.Engine.Version, data.Engine.Protocol)
	switch {
	case agent == "":
		c4.Status, c4.Detail = StatusSkipped, localchan.EnvAgentID+" is not set"
	case data.Caller.Identified:
		c4.Status, c4.Detail = StatusOK, "the engine accepts this process as a process of the terminal of "+agent
	default:
		c4.Status, c4.exit, c4.code = StatusFail, localchan.ExitDenied, localchan.CodeNotIdentified
		c4.Detail = "the engine does not accept this process as a process of the terminal of " + agent
		c4.Hint = "run mestre inside the terminal of that character; another terminal or a shell is refused"
	}
	switch c := data.Citadel; {
	case c == nil:
		c5.Status, c5.Detail = StatusSkipped, "the engine did not say whether it reaches the Citadel"
	case c.Reachable:
		c5.Status = StatusOK
		c5.Detail = fmt.Sprintf("the Citadel answers (protocol v%d, epoch %d)", c.Protocol, c.Epoch)
		if c.Detail != "" {
			c5.Detail = c.Detail
		}
	default:
		c5.Status, c5.exit, c5.code = StatusFail, localchan.ExitUnavailable, firstNonEmpty(c.Code, localchan.CodeCitadelUnavailable)
		c5.Detail = firstNonEmpty(c.Detail, "the engine does not reach the Citadel")
		c5.Hint = "the Owner is warned when the Citadel stays away; calls that need an answer now fail until it is back"
		if c.Code == localchan.CodeCitadelInFailover {
			c5.Hint = "another machine is taking the place of the Citadel; try again in a few minutes"
		}
	}
	return c3, c4, c5
}
