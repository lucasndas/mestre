package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/grimoires"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The chain of approval of production (docs/desenvolvimento/ligacao-de-producao.md):
// the pieces the Citadel builds over its database and its folders, with the
// people (users, flow, decrees) the integration brings. Without the integration
// there are no people and no chain: the approvals stay the interfaces they were.

// ownerTeller is what the integration gives the chain to reach the Owner.
type ownerTeller interface {
	OwnerAlert(ctx context.Context, text string)
}

// chainPeople is what the integration gives the Citadel to build the chain: the
// decrees that apply to a character and the users that name who may use a
// workspace. StandardIntegration implements it.
type chainPeople interface {
	ownerTeller
	CharacterDecrees() CharacterDecrees
	Users() *users.Service
}

// classBooks gives the library of classes of each workspace, over the main
// clone of the workspace and the global folder of the Citadel. A workspace
// whose name is not a plain folder name has none (fail closed).
type classBooks struct {
	db     *store.DB
	clk    clock.Clock
	global string
	layout workspaces.Layout
	// alerts gives the library of a workspace the one who is told of its
	// proposals (nil: they are dropped); set before the first library is made.
	alerts func(workspace string) classes.ClassAlerts

	mu   sync.Mutex
	libs map[string]*classes.Library
}

func newClassBooks(db *store.DB, clk clock.Clock, global string, layout workspaces.Layout) *classBooks {
	return &classBooks{db: db, clk: clk, global: global, layout: layout, libs: map[string]*classes.Library{}}
}

// plainName says the name is one folder name, never a path.
func plainName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\:`) && filepath.Base(s) == s
}

// For is Orchestration options' ClassLibraries.
func (b *classBooks) For(workspace string) *classes.Library {
	if b.global == "" || !plainName(workspace) {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if lib, ok := b.libs[workspace]; ok {
		return lib
	}
	clone := b.layout.Principal(workspace)
	lib := &classes.Library{
		Workspace: workspace, Roots: classes.DefaultRoots(b.global, clone), Records: classes.ClassDB{DB: b.db, Clock: b.clk},
		Ceiling: &classes.CeilingKeeper{Workspace: workspace, File: classes.CeilingPath(filepath.Join(clone, filepath.FromSlash(classes.CeilingFile))),
			Records: classes.CeilingDB{DB: b.db}, Clock: b.clk},
		Clock: b.clk, OS: runtime.GOOS,
		Skill: func(name string) bool {
			if !plainName(name) {
				return false
			}
			st, err := os.Lstat(filepath.Join(clone, ".agents", "skills", name))
			return err == nil && st.IsDir()
		},
	}
	if b.alerts != nil {
		lib.Alerts = b.alerts(workspace)
	}
	b.libs[workspace] = lib
	return lib
}

// policyFileFor is the approval policy of the workspaces, read from the file in
// their main clone; the Owner is told, once per version, of a file that does
// not hold.
func policyFileFor(layout workspaces.Layout, tell ownerTeller, log *slog.Logger) *chain.PolicyFile {
	pf := &chain.PolicyFile{
		Path: func(workspace string) string {
			if !plainName(workspace) {
				return ""
			}
			return filepath.Join(layout.Principal(workspace), ".mestre", "regras", "politica-de-aprovacao.yaml")
		},
		Logger: log,
	}
	if tell != nil {
		pf.Alert = func(workspace string, err error) {
			// in its own goroutine: the alert is read by the sweeps of other services inside
			// a write of the database, and sending it writes to the same database
			go tell.OwnerAlert(context.Background(), fmt.Sprintf("O arquivo de política de aprovação do workspace %s não vale (%v); continua valendo a última versão válida.", workspace, err))
		}
	}
	return pf
}

// chainOwnerAlerts is chain.Alerts to the Owner: the people the chain tells.
type chainOwnerAlerts struct{ tell ownerTeller }

func (a chainOwnerAlerts) Notify(ctx context.Context, workspace, text string) error {
	if t, ok := a.tell.(workspaceTeller); ok && workspace != "" {
		t.OwnerWorkspaceAlert(ctx, workspace, text) // the header of the message is the workspace's
		return nil
	}
	a.tell.OwnerAlert(ctx, fmt.Sprintf("[%s] %s", workspace, text))
	return nil
}

// workspaceTeller is the Owner told about a workspace, with its header.
type workspaceTeller interface {
	OwnerWorkspaceAlert(ctx context.Context, workspace, text string)
}

// userGrimoires says which workspaces a user may use for the grimoires: the
// ones the user acts in (the Owner in all). An unknown or suspended user, or one
// that cannot be read, may use none.
type userGrimoires struct{ svc *users.Service }

func (g userGrimoires) CanUse(ctx context.Context, actor grimoires.Actor, workspace string) bool {
	u, err := g.svc.Get(ctx, actor.ID)
	return err == nil && u.Acts(workspace)
}

// periodHooks is what the Citadel that acts gives the hook engine of the
// machine, which outlives the periods: the chain that opens the approvals and
// the gate of the temporary table rules of the period.
type periodHooks struct {
	chain *chain.Service
	rules classes.TableRuleGate
	// usage takes the events of the characters; agentMachine says where the
	// registry puts a character (nil in a period without orchestration).
	usage *hookUsage
	// decrees registers the use of the decree an allow of the hook carries.
	decrees func(providers.Event, hook.Answer)
	// exceptions is the ledger of the decrees (the single authority of the
	// exceptions used once, for every character they reach).
	exceptions   classes.ExceptionLedger
	agentMachine func(ctx context.Context, id string) (string, bool)
	// siteRules are the effective rules of a character, for the request of a
	// site credential by a Tower (nil in a period without the classes).
	siteRules func(ctx context.Context, id string) (*classes.Rules, error)
	// risk is the risk policy of a workspace, from its approval policy file.
	risk func(workspace string) *classes.RiskPolicy
	// layers gives the dynamic part of the Mestre layer of a character as it
	// is now (nil when the Citadel has no session zero of its own).
	layers func(ctx context.Context, id string) (sessionzero.Dynamic, error)
	// classHooks are the hooks of the classes of the workspaces, and async the
	// runner of the asynchronous ones (nil without the libraries of classes).
	classHooks *classHooks
	async      *hook.AsyncRunner
	// hookOut writes the outputs the hooks of the characters of the Towers send (hook.output).
	hookOut hook.HookOutputs
}

// errNoPeriod is why an approval is not opened: this machine is not the
// Citadel of a period that has the chain.
var errNoPeriod = fmt.Errorf("app: this machine has no chain of approval now: %w", hook.ErrNoApprovals)

// periodApprovals is the hook.Approvals of the machine: the chain of the period
// of the Citadel, and nothing while there is none.
type periodApprovals struct{ e *Engine }

func (a periodApprovals) Open(ctx context.Context, req hook.ApprovalRequest) (int64, error) {
	p := a.e.period.Load()
	if p == nil || p.chain == nil {
		return 0, errNoPeriod
	}
	return p.chain.Open(ctx, req)
}

// periodTableRules is the classes.TableRuleGate of the machine: the temporary
// table rules of the period; with none, no rule lets a call through (the call
// asks, the safe side).
type periodTableRules struct{ e *Engine }

func (g periodTableRules) Use(ctx context.Context, u classes.TableRuleUse) (bool, error) {
	p := g.e.period.Load()
	if p == nil || p.rules == nil {
		return false, errNoPeriod
	}
	return p.rules.Use(ctx, u)
}
