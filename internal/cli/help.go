package cli

import (
	"context"
	"fmt"
	"slices"

	"github.com/lucasndas/mestre/internal/localchan"
)

// runHelp is `mestre help [command]`. The list is the one the engine
// returns for the caller (only what the caller may use); without an engine
// to ask, or with one that has no filter yet, it shows every command and
// says it did not filter (docs/cli/convencoes.md#ajuda-e-diagnóstico).
func runHelp(ctx context.Context, env Env, p *parsed) int {
	if len(p.args) == 1 {
		spec, _, ok := resolve([]string{p.args[0]})
		if !ok {
			return out{env: env, json: p.json}.fail(localchan.Fail(localchan.CodeUsage,
				fmt.Sprintf("unknown command %q", p.args[0]), "mestre help lists the commands"))
		}
		fmt.Fprintf(env.Stdout, "usage: %s\n", spec.Usage)
		return localchan.ExitOK
	}
	allowed, filtered := engineCommands(ctx, env)
	var names []string
	for _, s := range commands {
		if !filtered || s.Local || slices.Contains(allowed, s.Name) {
			names = append(names, s.Name)
		}
	}
	if p.json {
		out{env: env, json: true}.printJSON(localchan.OK(map[string]any{"commands": names, "filtered": filtered}))
		return localchan.ExitOK
	}
	for _, n := range names {
		s, _, _ := resolve([]string{n})
		fmt.Fprintf(env.Stdout, "%-8s %s\n", n, s.Usage)
	}
	if !filtered {
		fmt.Fprintln(env.Stderr, "note: the list is not filtered by what you may use (the engine did not say)")
	}
	return localchan.ExitOK
}

// engineCommands asks the engine which commands the caller may use.
func engineCommands(ctx context.Context, env Env) (names []string, filtered bool) {
	c, err := client(env, -1)
	if err != nil {
		return nil, false
	}
	resp, err := c.Call(ctx, localchan.Request{AgentID: env.Getenv(localchan.EnvAgentID), Cmd: localchan.CmdHelp, Cwd: env.Cwd},
		localchan.CallOptions{})
	if err != nil || !resp.OK {
		return nil, false
	}
	var data struct {
		Commands []string `json:"commands"`
	}
	if resp.DecodeData(&data) != nil || len(data.Commands) == 0 {
		return nil, false
	}
	return data.Commands, true
}
