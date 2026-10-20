package app

import (
	"context"
	"fmt"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/protocol"
)

// rulesProfiles is the profile of a character as this machine knows it: the
// cli.allow of the effective rules it keeps. The level (Master of the table,
// Grand Master) arrives with the registry of characters, which the engine that
// hosts the orchestration sets in Deps.Profiles ("M5, antes da etapa 1" in
// docs/desenvolvimento/ordem-de-implementacao.md); until then everybody is an
// ordinary character.
type rulesProfiles struct{ src classes.RulesSource }

func (p rulesProfiles) Profile(ctx context.Context, agentID string) cli.Profile {
	r, err := p.src.Rules(ctx, agentID)
	if err != nil || r == nil {
		return cli.Profile{}
	}
	allow := r.CLI.Allow
	return cli.Profile{AllowsCLI: func(command string) bool { return classes.CLIAllows(allow, command) }}
}

// RegistryProfiles is the profile of a character as the Citadel knows it: the
// level comes from the registry of the characters (never from the caller) and
// the cli.allow from its effective rules. Whoever the registry does not know,
// or any failure to read it, is an ordinary character that allows nothing; a
// character whose rules cannot be read keeps its level and allows nothing of
// the class. It is the value of Deps.Profiles of the Citadel.
func RegistryProfiles(reg characters.Registry, rules classes.RulesSource) cli.Profiles {
	return registryProfiles{reg: reg, rules: rules}
}

type registryProfiles struct {
	reg   characters.Registry
	rules classes.RulesSource
}

func (p registryProfiles) Profile(ctx context.Context, agentID string) cli.Profile {
	c, err := p.reg.Get(ctx, agentID)
	if err != nil {
		return cli.Profile{}
	}
	out := cli.Profile{}
	if p.rules != nil {
		out = rulesProfiles{src: p.rules}.Profile(ctx, agentID)
	}
	switch c.Role {
	case characters.RoleTableMaster:
		out.Role = cli.RoleTableMaster
	case characters.RoleGrandMaster:
		out.Role = cli.RoleGrandMaster
	default:
		out.Role = cli.RoleCharacter
	}
	return out
}

// citadelDebug is item 5 of `mestre debug`: whether this engine reaches the
// current Citadel, with the protocol and the epoch it works with. A Tower
// reaches it when the connection is open (the opening already checked the
// protocol and the epoch); a Citadel when it acts.
func (e *Engine) citadelDebug() map[string]any {
	st := e.Status()
	out := map[string]any{"epoch": st.Epoch, "protocol": protocol.Version}
	unreachable := func(code, detail string) map[string]any {
		out["reachable"], out["code"], out["detail"] = false, code, detail
		return out
	}
	switch {
	case st.Failover:
		return unreachable(localchan.CodeCitadelInFailover, "another machine is taking the place of the Citadel")
	case st.Role == RoleCitadel && !st.Acting && st.Waiting != "":
		return unreachable(localchan.CodeCitadelUnavailable, st.Waiting)
	case st.Role == RoleCitadel && !st.Acting:
		return unreachable(localchan.CodeCitadelUnavailable, fmt.Sprintf("this machine is the Citadel but does not act (%s)", st.Fence))
	case st.Role == RoleCitadel:
		out["reachable"], out["detail"] = true, fmt.Sprintf("this machine is the Citadel (epoch %d)", st.Epoch)
		return out
	case !st.Connected:
		return unreachable(localchan.CodeCitadelUnavailable, "this Tower has no connection to the Citadel")
	}
	e.mu.Lock()
	tow := e.tow
	e.mu.Unlock()
	if tow != nil {
		if s := tow.client.Link().Session(); s != nil {
			out["protocol"] = s.Version()
		}
	}
	out["reachable"] = true
	out["detail"] = fmt.Sprintf("connected to the Citadel (protocol v%v, epoch %d)", out["protocol"], st.Epoch)
	return out
}

// auditCall records a call of the CLI in the log of the engine, with who
// called, what and the result. The trail of audit of the Guild (milestone 5)
// reads the same record.
func (e *Engine) auditCall(a localchan.Audit) {
	e.log.Info("cli call", "id", a.ID, "cmd", a.Cmd, "agent_id", a.AgentID, "terminal_id", a.TerminalID, "exit", a.Exit, "code", a.Code)
}
