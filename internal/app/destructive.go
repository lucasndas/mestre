package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/rituals"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// The destructive actions of the characters go through the chain of approval
// (docs/cli/convencoes.md#regras-de-segurança-da-cli): the first try opens a
// request of type acao_destrutiva bound to the exact command and object and to
// the state of the object, and answers "pending"; the try after the approval,
// by the same character, spends it once if the object is still as it was.

// destructiveBroker joins the owners of the four destructive commands to
// chain.Service.Destructive. The services are read when used: they are made
// after the chain.
type destructiveBroker struct {
	chain   **chain.Service
	scrolls **scrolls.Service
	rituals **rituals.Service
	tables  **tables.Service
	classes func(workspace string) *classes.Library
}

func (b destructiveBroker) ask(ctx context.Context, a chain.DestructiveAsk) (bool, int64, error) {
	if b.chain == nil || *b.chain == nil {
		return false, 0, errors.New("app: this period has no chain of approval")
	}
	return (*b.chain).Destructive(ctx, a)
}

// noteGate is scrolls.Approvals (and DeleteGate) over the broker.
type noteGate struct{ b destructiveBroker }

var (
	_ scrolls.Approvals  = noteGate{}
	_ scrolls.DeleteGate = noteGate{}
)

func (g noteGate) DeleteAllowed(ctx context.Context, by scrolls.Actor, n scrolls.Note) (bool, int64, error) {
	if by.Kind != scrolls.ByCharacter {
		return false, 0, errors.New("app: only a character asks for the approval of a deletion")
	}
	return g.b.ask(ctx, chain.DestructiveAsk{Requester: by.ID, Command: "note delete", Object: n.Key, Version: strconv.FormatInt(n.Revision, 10),
		About: fmt.Sprintf("%q (%s)", n.Name, n.ID)})
}

func (g noteGate) RequestDelete(ctx context.Context, by scrolls.Actor, n scrolls.Note) (int64, error) {
	_, id, err := g.DeleteAllowed(ctx, by, n)
	return id, err
}

// ritualGate is rituals.Approvals (and DeleteGate) over the broker.
type ritualGate struct{ b destructiveBroker }

var (
	_ rituals.Approvals  = ritualGate{}
	_ rituals.DeleteGate = ritualGate{}
)

func (g ritualGate) DeleteAllowed(ctx context.Context, by rituals.Actor, r rituals.Ritual) (bool, int64, error) {
	if by.Kind != rituals.ByCharacter || by.Character.ID == "" {
		return false, 0, errors.New("app: only a character asks for the approval of a deletion")
	}
	return g.b.ask(ctx, chain.DestructiveAsk{Requester: by.Character.ID, Command: "ritual delete", Object: r.ID, Version: strconv.FormatInt(r.CreatedAt.UnixMilli(), 10) + "." + strconv.FormatInt(r.UpdatedAt.UnixMilli(), 10),
		About: fmt.Sprintf("%q", r.Name)})
}

func (g ritualGate) RequestDelete(ctx context.Context, by rituals.Actor, r rituals.Ritual) (int64, error) {
	_, id, err := g.DeleteAllowed(ctx, by, r)
	return id, err
}

// tableGate is tables.Destructive over the broker: the cancellation of a table.
type tableGate struct{ b destructiveBroker }

var _ tables.Destructive = tableGate{}

func (g tableGate) Allowed(ctx context.Context, a tables.DestructiveAction) (bool, error) {
	if a.By == "" {
		return false, errors.New("app: the cancellation of a table is asked by a character")
	}
	version, err := g.b.tableVersion(ctx, a.Workspace, a.Table)
	if err != nil {
		return false, err
	}
	ok, _, err := g.b.ask(ctx, chain.DestructiveAsk{Requester: a.By, Command: "table close --cancel", Object: a.Workspace + "/" + a.Table, Version: version,
		About: "cancelar a mesa " + a.Table, Reason: a.Reason})
	return ok, err
}

// tableVersion is the head of the branch of the table in the mirror: what the
// cancellation would put aside.
func (b destructiveBroker) tableVersion(ctx context.Context, workspace, table string) (string, error) {
	if b.tables == nil || *b.tables == nil {
		return "", errors.New("app: no tables to read the branch from")
	}
	ch, err := (*b.tables).Changes(ctx, workspace, table, "", "")
	if err != nil {
		return "", fmt.Errorf("the head of the branch of the table could not be read: %w", err)
	}
	return ch.Head, nil
}

// classGate is the ClassDestructive of the commands of classes.
func (b destructiveBroker) classGate(ctx context.Context, d classes.DeleteRequest) (bool, error) {
	version, err := b.classVersion(ctx, d.Workspace, d.Key)
	if err != nil {
		return false, err
	}
	ok, _, err := b.ask(ctx, chain.DestructiveAsk{Requester: d.By, Command: "class delete", Object: classObject(d.Workspace, d.Key), Version: version, About: d.Key.String()})
	return ok, err
}

func classObject(workspace string, k classes.ClassKey) string {
	return strings.Join([]string{workspace, string(k.Scope), k.Table, k.Name}, "/")
}

func (b destructiveBroker) classVersion(ctx context.Context, workspace string, k classes.ClassKey) (string, error) {
	if b.classes == nil {
		return "", errors.New("app: no library of classes")
	}
	lib := b.classes(workspace)
	if lib == nil {
		return "", errors.New("app: the workspace has no library of classes")
	}
	rec, ok, err := lib.Records.Approved(ctx, workspace, k)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("app: the class %s has no approved version", k)
	}
	return rec.Hash, nil
}

// Live is approvals.Targets for the destructive requests: the target of the
// request with the state the object has now, so an approval is applied only to
// what was asked about.
func (b destructiveBroker) Live(ctx context.Context, r chain.Request) (json.RawMessage, error) {
	if r.Type != classes.RequestDestructive {
		return nil, fmt.Errorf("app: no live target for requests of type %q", r.Type)
	}
	var t chain.DestructiveTarget
	if err := json.Unmarshal(r.Target, &t); err != nil {
		return nil, err
	}
	switch t.Command {
	case "note delete":
		if b.scrolls == nil || *b.scrolls == nil {
			return nil, errors.New("app: no scrolls")
		}
		n, ok, err := (*b.scrolls).Store().Get(ctx, t.Object)
		if err != nil || !ok {
			return nil, fmt.Errorf("the scroll %s could not be read: %v", t.Object, err)
		}
		t.Version = strconv.FormatInt(n.Revision, 10)
	case "ritual delete":
		if b.rituals == nil || *b.rituals == nil {
			return nil, errors.New("app: no rituals")
		}
		rt, err := (*b.rituals).Store().Ritual(ctx, t.Object)
		if err != nil {
			return nil, err
		}
		t.Version = strconv.FormatInt(rt.CreatedAt.UnixMilli(), 10) + "." + strconv.FormatInt(rt.UpdatedAt.UnixMilli(), 10)
	case "table close --cancel":
		ws, table, ok := strings.Cut(t.Object, "/")
		if !ok {
			return nil, fmt.Errorf("app: the object %q is not a table", t.Object)
		}
		v, err := b.tableVersion(ctx, ws, table)
		if err != nil {
			return nil, err
		}
		t.Version = v
	case "class delete":
		parts := strings.SplitN(t.Object, "/", 4)
		if len(parts) != 4 {
			return nil, fmt.Errorf("app: the object %q is not a class", t.Object)
		}
		v, err := b.classVersion(ctx, parts[0], classes.ClassKey{Scope: classes.ClassScope(parts[1]), Table: parts[2], Name: parts[3]})
		if err != nil {
			return nil, err
		}
		t.Version = v
	default:
		return nil, fmt.Errorf("app: no live target for the command %q", t.Command)
	}
	return t.JSON(), nil
}

// withDestructive puts the broker in the options the owners of the destructive
// commands read, wherever the caller gave none.
func (o *OrchestrationOptions) withDestructive(b destructiveBroker) {
	if o.Destructive == nil {
		o.Destructive = tableGate{b}
	}
	if o.RitualApprovals == nil {
		o.RitualApprovals = ritualGate{b}
	}
	if o.ClassDestructive == nil {
		o.ClassDestructive = b.classGate
	}
}

var _ = characters.Character{}
