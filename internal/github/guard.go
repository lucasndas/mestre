package github

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// The reserved labels (docs/github/acoes-por-nivel.md#árvore-do-épico).
const (
	EpicLabel  = "epico"
	HumanLabel = "mestre:humano"

	epicLabelHelp  = "Épico do Mestre: uma mesa"
	humanLabelHelp = "Só uma pessoa aprova"
	// maxClimb is how many levels of the tree the guard goes up (GitHub allows 8).
	maxClimb = 8
)

// Codes of a refusal of the guard; the CLI maps them to its exit codes.
const (
	CodeOtherTable    = "other_table"
	CodeNotInEpic     = "not_in_epic"
	CodeReservedLabel = "reserved_label"
	CodeForce         = "force_not_allowed"
	CodeNotAllowed    = "not_allowed"
)

// Refusal is an action the engine does not do, with the reason in plain words.
type Refusal struct {
	Code    string
	Message string
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Message }

func refuse(code, format string, a ...any) *Refusal {
	return &Refusal{Code: code, Message: fmt.Sprintf(format, a...)}
}

// ActorKind is who asks.
type ActorKind int

// The actors: the table (its Mestre da mesa and the classes that may), the
// Grão-Mestre and the people (acting on GitHub itself).
const (
	ActorTable ActorKind = iota
	ActorGrandMaster
	ActorPerson
)

// Actor is who asks for an action.
type Actor struct {
	Kind  ActorKind
	Table string
	// MestreDaMesa says the one who asks is the Mestre of the table (the push
	// forçado is only his).
	MestreDaMesa bool
}

// Target is the issue or the PR an action is about.
type Target struct {
	Repo   string
	Kind   ObjectKind
	Number int
	// Branch is the source branch of a PR.
	Branch string
}

// Tables gives the epic and the branch of a table of the workspace.
type Tables interface {
	Epic(ctx context.Context, table string) (epic int, branch string, ok bool, err error)
}

// Guard checks that an action of a table is about what is the table's
// (docs/github/acoes-por-nivel.md#como-o-motor-confere-que-o-alvo-é-da-mesa).
type Guard struct {
	Objects *Objects
	GH      GitHub
	Tables  Tables
}

// Authorize is nil when the actor may act on the target. The Grão-Mestre may act
// anywhere; a table only on its own issues and PRs. The result of the climb of
// the tree is registered, so the next request is answered from the database.
func (g *Guard) Authorize(ctx context.Context, a Actor, t Target) error {
	switch a.Kind {
	case ActorGrandMaster, ActorPerson:
		return nil
	}
	epic, branch, ok, err := g.Tables.Epic(ctx, a.Table)
	if err != nil {
		return err
	}
	if !ok {
		return refuse(CodeNotAllowed, "a mesa %s não tem épico registrado", a.Table)
	}
	if obj, found, err := g.Objects.Get(ctx, t.Repo, t.Kind, t.Number); err != nil {
		return err
	} else if found {
		if obj.Table == a.Table {
			return nil
		}
		return g.otherTable(ctx, t, obj.Table)
	}
	if t.Kind == KindPR {
		if branch != "" && t.Branch == branch {
			return g.register(ctx, a, t, 0)
		}
		return refuse(CodeOtherTable, "o PR #%d não é da mesa %s; peça ao Grão-Mestre", t.Number, a.Table)
	}
	if t.Number == epic {
		return nil
	}
	return g.climb(ctx, a, t, epic)
}

func (g *Guard) otherTable(ctx context.Context, t Target, owner string) error {
	if e, _, ok, err := g.Tables.Epic(ctx, owner); err == nil && ok {
		return refuse(CodeOtherTable, "a issue #%d pertence ao épico #%d, de outra mesa; peça ao Grão-Mestre", t.Number, e)
	}
	return refuse(CodeOtherTable, "#%d pertence a outra mesa; peça ao Grão-Mestre", t.Number)
}

// climb goes up the parents of an issue nobody registered, to the first issue
// with the label epico.
func (g *Guard) climb(ctx context.Context, a Actor, t Target, epic int) error {
	n := t.Number
	for range maxClimb {
		p, ok, err := g.GH.Parent(ctx, t.Repo, n)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		info, err := g.GH.Issue(ctx, t.Repo, p)
		if err != nil {
			return err
		}
		if slices.Contains(info.Labels, EpicLabel) {
			if p != epic {
				return refuse(CodeOtherTable, "a issue #%d pertence ao épico #%d, de outra mesa; peça ao Grão-Mestre", t.Number, p)
			}
			return g.register(ctx, a, t, p)
		}
		n = p
	}
	return refuse(CodeNotInEpic, "a issue #%d não está na árvore do épico #%d da mesa; peça ao Grão-Mestre", t.Number, epic)
}

func (g *Guard) register(ctx context.Context, a Actor, t Target, parent int) error {
	err := g.Objects.Register(ctx, Object{Repo: t.Repo, Kind: t.Kind, Number: t.Number, Table: a.Table, ParentNumber: parent})
	if errors.Is(err, ErrOtherTable) {
		return g.otherTable(ctx, t, "")
	}
	return err
}

// CheckLabels refuses a change of the reserved labels. epico is only the
// Grão-Mestre's (to put or to take); mestre:humano is put by the Grão-Mestre and
// the people, and taken only by the people: a table, and the Grão-Mestre, are
// refused when they try to take it.
func CheckLabels(a Actor, add, remove []string) error {
	has := func(l []string, s string) bool { return slices.Contains(l, s) }
	if (has(add, EpicLabel) || has(remove, EpicLabel)) && a.Kind == ActorTable {
		return refuse(CodeReservedLabel, "o rótulo %s é reservado: só o Grão-Mestre o põe ou tira", EpicLabel)
	}
	if has(add, HumanLabel) && a.Kind == ActorTable {
		return refuse(CodeReservedLabel, "o rótulo %s é reservado: só o Grão-Mestre e as pessoas o põem", HumanLabel)
	}
	if has(remove, HumanLabel) && a.Kind != ActorPerson {
		return refuse(CodeReservedLabel, "o rótulo %s é reservado: só as pessoas o tiram, pelo GitHub ou pelo painel", HumanLabel)
	}
	return nil
}

// EnsureLabels creates the reserved labels in the list that the repository does
// not have yet, before the issue or the PR that uses them.
func (g *Guard) EnsureLabels(ctx context.Context, repo string, labels []string) error {
	for _, l := range labels {
		help := map[string]string{EpicLabel: epicLabelHelp, HumanLabel: humanLabelHelp}[l]
		if help == "" {
			continue
		}
		has, err := g.GH.HasLabel(ctx, repo, l)
		if err != nil {
			return err
		}
		if !has {
			if err := g.GH.CreateLabel(ctx, repo, l, help); err != nil {
				return err
			}
		}
	}
	return nil
}

// CheckForce is the rule of the forced push: never the main branch, never a
// branch that is not the table's, and only when the Mestre da mesa asks.
func CheckForce(a Actor, branch, tableBranch, defaultBranch string) error {
	switch {
	case branch == "" || branch == defaultBranch || strings.EqualFold(branch, "main") || strings.EqualFold(branch, "master"):
		return refuse(CodeForce, "o push forçado na branch principal não é feito")
	case a.Kind != ActorTable || !a.MestreDaMesa:
		return refuse(CodeForce, "o push forçado é só do Mestre da mesa")
	case tableBranch == "" || branch != tableBranch:
		return refuse(CodeForce, "o push forçado é só na branch da mesa (%s)", tableBranch)
	}
	return nil
}

// PostOnce creates a comment unless the operation already made it: it looks for
// the marker of the uuid among the comments before it creates, so a resending
// finds what the first sending did and only records it.
func (g *Guard) PostOnce(ctx context.Context, a Actor, repo string, issue int, body, op string) (CommentInfo, error) {
	if obj, found, err := g.Objects.ByOp(ctx, op); err != nil {
		return CommentInfo{}, err
	} else if found {
		return CommentInfo{ID: obj.GitHubID}, nil
	}
	existing, err := g.GH.Comments(ctx, repo, issue)
	if err != nil {
		return CommentInfo{}, err
	}
	var c CommentInfo
	for _, e := range existing {
		if HasMarker(e.Body, op) {
			c = e
			break
		}
	}
	if c.ID == 0 {
		if c, err = g.GH.PostComment(ctx, repo, issue, WithMarker(body, op)); err != nil {
			return CommentInfo{}, err
		}
	}
	table := ""
	if a.Kind == ActorTable {
		table = a.Table
	}
	return c, g.Objects.Register(ctx, Object{Repo: repo, Kind: KindComment, Number: int(c.ID), GitHubID: c.ID, Table: table, ParentNumber: issue, Op: op})
}
