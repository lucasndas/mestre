package decrees

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
)

// Characters finds the caller and the Mestre da mesa the delegation is for
// (characters.Registry).
type Characters interface {
	Get(ctx context.Context, id string) (characters.Character, error)
	TableMaster(ctx context.Context, workspace, table string) (characters.Character, bool, error)
}

// SubHandler is the command `mestre approval delegate`
// (docs/cli/grao-mestre.md#mestre-approval): the Grand Master passes to the Mestre of
// a table a part of what it decides. The rules are the ones of Subdelegate; the
// command identifies who calls, finds the Mestre of the table and translates the
// refusals.
type SubHandler struct {
	Service    *Service
	Characters Characters
}

// Register registers the command in the local forwarder of the Citadel.
func (h *SubHandler) Register(fwd *localchan.LocalForwarder) {
	fwd.Handle("approval delegate", localchan.HandlerFunc(h.handle))
}

// requestTypes are the types of request the engine knows; a typo in a type would
// make a delegation that covers nothing and that the Grand Master takes for done.
var requestTypes = []classes.RequestType{
	classes.RequestMissionPoint, classes.RequestPR, classes.RequestMerge, classes.RequestExtraCost, classes.RequestWorkflows, classes.RequestRepoSettings,
	classes.RequestWorkspaceClass, classes.RequestDestructive, classes.RequestAgentsMD, classes.RequestOperator, classes.RequestMestreUpdate,
	classes.RequestGuildEntry, classes.RequestCitadelPromotion,
}

func knownType(t string) bool {
	for _, k := range requestTypes {
		if string(k) == t {
			return true
		}
	}
	return false
}

// maxReason is the longest reason kept (one line of text of an agent).
const maxReason = 500

func flagText(c *localchan.Call, name string) string {
	v, _ := c.Request.Flags[name].(string)
	return strings.TrimSpace(v)
}

func (h *SubHandler) handle(ctx context.Context, c *localchan.Call) localchan.Response {
	if !c.Caller.Identified {
		return localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this process as a process of a character", "run mestre debug")
	}
	by, err := h.Characters.Get(ctx, c.Caller.AgentID)
	if err != nil {
		return localchan.Fail(localchan.CodeNotIdentified, "the engine does not know this character", "run mestre debug")
	}
	usage := `usage: mestre approval delegate --table "<table>" --type <type> --until "<ISO 8601>" --reason "<reason>"`
	if args, ok := c.Request.Strings(); !ok || len(args) != 0 {
		return localchan.Fail(localchan.CodeUsage, usage, "")
	}
	if flagText(c, "pattern") != "" {
		return localchan.Fail(localchan.CodeUsage, "--pattern is not available: a subdelegation is for a type of request on a table", usage)
	}
	table, typ, reason := flagText(c, "table"), flagText(c, "type"), flagText(c, "reason")
	if table == "" || typ == "" || reason == "" {
		return localchan.Fail(localchan.CodeUsage, "--table, --type and --reason are required", usage)
	}
	if !knownType(typ) {
		return localchan.Fail(localchan.CodeUsage, fmt.Sprintf("--type %q is not a type of request", typ), usage)
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if r := []rune(reason); len(r) > maxReason {
		reason = string(r[:maxReason])
	}
	until, err := time.Parse(time.RFC3339, flagText(c, "until"))
	if err != nil {
		return localchan.Fail(localchan.CodeUsage, `--until is a date and time with its zone, like "2026-11-20T18:00:00-03:00"`, usage)
	}
	if by.Role != characters.RoleGrandMaster {
		return localchan.Fail("level_denied", "only the Grand Master of the workspace passes on what it decides", "").WithExit(localchan.ExitDenied)
	}
	grantee, ok, err := h.Characters.TableMaster(ctx, by.Workspace, table)
	if err != nil {
		return localchan.Fail(localchan.CodeInternal, "the Mestre of the table could not be looked up", "")
	}
	if !ok {
		return localchan.Fail("not_found", fmt.Sprintf("the table %q has no Mestre in this workspace", table), "see `mestre table list`").WithExit(localchan.ExitNotFound)
	}
	sub, err := h.Service.Subdelegate(ctx, SubRequest{By: by, Grantee: grantee, Mesa: table, Type: typ, Until: until, Reason: reason})
	if err != nil {
		var ref *Refusal
		if errors.As(err, &ref) && (ref.Code == CodeLevelDenied || ref.Code == CodeTooWide) {
			return localchan.FailMsg(ref.Code, ref.Msg, "").WithExit(localchan.ExitDenied)
		}
		return localchan.Fail(localchan.CodeInternal, "the delegation could not be made", "")
	}
	return localchan.OK(map[string]any{"id": sub.ID, "table": sub.Mesa, "type": sub.Type, "until": sub.Until.UTC().Format(time.RFC3339), "origin": sub.Origin,
		"text": fmt.Sprintf("subdelegation %d: the Table Master of %s decides %s until %s", sub.ID, sub.Mesa, sub.Type, sub.Until.UTC().Format(time.RFC3339))})
}
