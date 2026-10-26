package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mestre-ia/mestre/internal/localchan"
)

// runHelp is `mestre help [command]`. The list is the one the engine
// returns for the caller (only what the caller may use); without an engine
// to ask, or with one that did not filter, it shows every command and says
// it did not filter (docs/cli/convencoes.md#ajuda-e-diagnóstico).
func runHelp(ctx context.Context, env Env, p *parsed) int {
	o := out{env: env, json: p.json}
	if len(p.args) == 1 {
		spec, _, ok := env.Commands.Resolve(strings.Fields(p.args[0]))
		if !ok {
			return o.fail(localchan.Fail(localchan.CodeUsage,
				fmt.Sprintf("unknown command %q", p.args[0]), "mestre help lists the commands"))
		}
		return o.commandHelp(spec)
	}
	allowed, filtered := engineCommands(ctx, env)
	var specs []Spec
	for _, s := range env.Commands.Specs() {
		if !filtered || s.Local || slices.Contains(allowed, s.Name) {
			specs = append(specs, s)
		}
	}
	if p.json {
		names := make([]string, len(specs))
		for i, s := range specs {
			names[i] = s.Name
		}
		o.printJSON(localchan.OK(map[string]any{"commands": names, "filtered": filtered}))
		return localchan.ExitOK
	}
	for i, g := range groups(specs) {
		if i > 0 {
			fmt.Fprintln(env.Stdout)
		}
		fmt.Fprintln(env.Stdout, g[0].Group)
		for _, s := range g {
			fmt.Fprintf(env.Stdout, "  %-12s %s\n", s.Name, s.Usage)
		}
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
		Filtered bool     `json:"filtered"`
	}
	if resp.DecodeData(&data) != nil || !data.Filtered {
		return nil, false
	}
	return data.Commands, true
}

// commandHelp is `mestre help <command>` and `mestre <command> --help`: the
// usage, the arguments, the flags, the level, an example and the exit codes
// of the command. It needs no engine.
func (o out) commandHelp(s Spec) int {
	exits := append([]int{localchan.ExitOK, localchan.ExitUsage, localchan.ExitDenied}, s.Exits...)
	slices.Sort(exits)
	exits = slices.Compact(exits)
	if o.json {
		type flag struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
			Desc string `json:"description"`
		}
		var flags []flag
		for _, f := range s.Flags {
			flags = append(flags, flag{f.Name, kindName(f.Kind), f.Desc})
		}
		var args []Arg
		args = append(args, s.Args...)
		o.printJSON(localchan.OK(map[string]any{
			"name": s.Name, "summary": s.Summary, "usage": s.Usage, "example": s.Example,
			"level": s.Who.String(), "class_can_grant": s.ClassCanGrant, "args": nonNil(args), "flags": nonNil(flags), "exit_codes": exits,
		}))
		return localchan.ExitOK
	}
	w := o.env.Stdout
	fmt.Fprintf(w, "%s\n\nusage: %s\n", s.Summary, s.Usage)
	for _, a := range s.Args {
		fmt.Fprintf(w, "  %-12s %s\n", a.Name, a.Desc)
	}
	for _, f := range s.Flags {
		fmt.Fprintf(w, "  --%-10s %s\n", f.Name, f.Desc)
	}
	fmt.Fprintf(w, "  --json       print one JSON object instead of text\n  --debug      show on stderr the partial answers the CLI does not print\n")
	level := s.Who.String()
	if s.ClassCanGrant {
		level += " (or a class that lists it in cli.allow)"
	}
	fmt.Fprintf(w, "\nwho: %s\n", level)
	if s.Example != "" {
		fmt.Fprintf(w, "example: %s\n", s.Example)
	}
	fmt.Fprintln(w, "exit codes:")
	for _, c := range exits {
		fmt.Fprintf(w, "  %d  %s\n", c, exitMeaning[c])
	}
	return localchan.ExitOK
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func kindName(k FlagKind) string {
	switch k {
	case FlagDuration:
		return "duration"
	case FlagBool:
		return "bool"
	}
	return "string"
}

// exitMeaning is the table of exit codes of docs/cli/convencoes.md.
var exitMeaning = map[int]string{
	localchan.ExitOK:          "success",
	localchan.ExitInternal:    "internal: unexpected error (a bug; it went to the log)",
	localchan.ExitUsage:       "usage: invalid, missing or forbidden argument or flag",
	localchan.ExitDenied:      "denied: no permission, or the caller is not identified",
	localchan.ExitNotFound:    "not_found: no such target, or none that you can see",
	localchan.ExitUnavailable: "unavailable: the engine, the Citadel or the target machine is out of reach",
	localchan.ExitTimeout:     "timeout: the time limit passed; the request stays pending, do not send it again",
	localchan.ExitConflict:    "conflict: the state does not allow it (target busy, name taken, no active task)",
	localchan.ExitLimit:       "limit: a limit was reached",
	localchan.ExitPending:     "pending: accepted, waiting for approval or a slot; the result arrives in the inbox",
}
