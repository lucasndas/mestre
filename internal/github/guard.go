package github

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
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

// Target is the issue or the PR an action is about (a comment is authorized
// through the issue or the PR it is on). The branch of a PR is read from GitHub,
// never taken from the one who asks.
type Target struct {
	Repo   string
	Kind   ObjectKind
	Number int
}

// TableRef is where a table works: the repository, the number of its epic there
// and its branch.
type TableRef struct {
	Repo   string
	Epic   int
	Branch string
}

// Tables gives the epic and the branch of a table of the workspace.
type Tables interface {
	Epic(ctx context.Context, table string) (ref TableRef, ok bool, err error)
}

func isEpic(labels []string) bool {
	return slices.ContainsFunc(labels, func(l string) bool { return strings.EqualFold(strings.TrimSpace(l), EpicLabel) })
}

// Guard checks that an action of a table is about what is the table's
// (docs/github/acoes-por-nivel.md#como-o-motor-confere-que-o-alvo-é-da-mesa).
type Guard struct {
	Objects *Objects
	GH      GitHub
	Tables  Tables

	postMu sync.Mutex
}

// Authorize is nil when the actor may act on the target. The Grão-Mestre may act
// anywhere; a table only on its own issues and PRs. The result of the climb of
// the tree is registered, so the next request is answered from the database.
func (g *Guard) Authorize(ctx context.Context, a Actor, t Target) error {
	switch a.Kind {
	case ActorGrandMaster, ActorPerson:
		return nil
	}
	repo, err := NormalizeRepo(t.Repo)
	if err != nil {
		return refuse(CodeNotAllowed, "repositório inválido")
	}
	t.Repo = repo
	if t.Kind != KindIssue && t.Kind != KindPR {
		return refuse(CodeNotAllowed, "o alvo é uma issue ou um PR; um comentário vale pelo que o contém")
	}
	ref, ok, err := g.Tables.Epic(ctx, a.Table)
	if err != nil {
		return err
	}
	if !ok {
		return refuse(CodeNotAllowed, "a mesa %s não tem épico registrado", a.Table)
	}
	if want, err := NormalizeRepo(ref.Repo); err != nil || want != t.Repo {
		return refuse(CodeOtherTable, "a mesa %s trabalha em %s, não em %s; peça ao Grão-Mestre", a.Table, ref.Repo, t.Repo)
	}
	if obj, found, err := g.Objects.Get(ctx, t.Repo, t.Kind, t.Number); err != nil {
		return err
	} else if found {
		if obj.Table == a.Table {
			return nil
		}
		return g.otherTable(ctx, t, obj.Table)
	}
	if t.Number == ref.Epic {
		return nil
	}
	pr, err := g.GH.Pull(ctx, t.Repo, t.Number)
	switch {
	case err == nil && t.Kind == KindIssue:
		return refuse(CodeNotAllowed, "#%d é um PR, não uma issue", t.Number)
	case err == nil:
		if ref.Branch != "" && pr.HeadRef == ref.Branch && strings.EqualFold(pr.HeadRepo, t.Repo) {
			return g.register(ctx, a, t, 0)
		}
		return refuse(CodeOtherTable, "o PR #%d não é da mesa %s; peça ao Grão-Mestre", t.Number, a.Table)
	case !errors.Is(err, ErrNotFound):
		return err
	case t.Kind == KindPR:
		return refuse(CodeNotAllowed, "#%d não é um PR", t.Number)
	}
	return g.climb(ctx, a, t, ref.Epic)
}

func (g *Guard) otherTable(ctx context.Context, t Target, owner string) error {
	if r, ok, err := g.Tables.Epic(ctx, owner); err == nil && ok && owner != "" {
		return refuse(CodeOtherTable, "a issue #%d pertence ao épico #%d, de outra mesa; peça ao Grão-Mestre", t.Number, r.Epic)
	}
	return refuse(CodeOtherTable, "#%d pertence a outra mesa; peça ao Grão-Mestre", t.Number)
}

// climb goes up the parents of an issue nobody registered, to the first issue
// with the label epico. An issue that is an epic itself is never taken this way:
// only the epic of the table is the table's.
func (g *Guard) climb(ctx context.Context, a Actor, t Target, epic int) error {
	self, err := g.GH.Issue(ctx, t.Repo, t.Number)
	if err != nil {
		return err
	}
	if isEpic(self.Labels) {
		return refuse(CodeOtherTable, "a issue #%d é um épico de outra mesa; peça ao Grão-Mestre", t.Number)
	}
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
		if isEpic(info.Labels) {
			if p != epic {
				return refuse(CodeOtherTable, "a issue #%d pertence ao épico #%d, de outra mesa; peça ao Grão-Mestre", t.Number, p)
			}
			return g.register(ctx, a, t, n)
		}
		n = p
	}
	return refuse(CodeNotInEpic, "a issue #%d não está na árvore do épico #%d da mesa; peça ao Grão-Mestre", t.Number, epic)
}

func (g *Guard) register(ctx context.Context, a Actor, t Target, parent int) error {
	err := g.Objects.Register(ctx, Object{Repo: t.Repo, Kind: t.Kind, Number: t.Number, Table: a.Table, ParentNumber: parent})
	if errors.Is(err, ErrOtherTable) {
		return refuse(CodeOtherTable, "#%d pertence a outra mesa; peça ao Grão-Mestre", t.Number)
	}
	return err
}

// CheckLabels refuses a change of the reserved labels. epico is only the
// Grão-Mestre's (to put or to take); mestre:humano is put by the Grão-Mestre and
// the people, and taken only by the people: a table, and the Grão-Mestre, are
// refused when they try to take it.
func CheckLabels(a Actor, add, remove []string) error {
	// GitHub keeps the names case-insensitive and trims them: the checks do too.
	has := func(l []string, want string) bool {
		return slices.ContainsFunc(l, func(s string) bool { return strings.EqualFold(strings.TrimSpace(s), want) })
	}
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
		help := map[string]string{EpicLabel: epicLabelHelp, HumanLabel: humanLabelHelp}[strings.ToLower(strings.TrimSpace(l))]
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
	case defaultBranch == "" || strings.EqualFold(tableBranch, defaultBranch):
		return refuse(CodeForce, "sem a branch principal conhecida, ou a da mesa é a principal, o push forçado não é feito")
	case branch == "" || strings.EqualFold(branch, defaultBranch) || strings.EqualFold(branch, "main") || strings.EqualFold(branch, "master"):
		return refuse(CodeForce, "o push forçado na branch principal não é feito")
	case a.Kind != ActorTable || !a.MestreDaMesa:
		return refuse(CodeForce, "o push forçado é só do Mestre da mesa")
	case tableBranch == "" || branch != tableBranch:
		return refuse(CodeForce, "o push forçado é só na branch da mesa (%s)", tableBranch)
	}
	return nil
}

// PostOnce creates a comment unless the operation already made it: it looks for
// the marker of the uuid (at the end of a comment) before it creates, so a
// resending finds what the first sending did and only records it. The issue or
// the PR must be the actor's (Authorize); the uuid is the engine's, and one that
// was used for another repository, issue or table is an error, not a success.
func (g *Guard) PostOnce(ctx context.Context, a Actor, repo string, issue int, body, op string) (CommentInfo, error) {
	repo, err := NormalizeRepo(repo)
	if err != nil {
		return CommentInfo{}, err
	}
	if err := g.Authorize(ctx, a, Target{Repo: repo, Kind: KindIssue, Number: issue}); err != nil {
		return CommentInfo{}, err
	}
	table := ""
	if a.Kind == ActorTable {
		table = a.Table
	}
	g.postMu.Lock() // one at a time: two resendings of an operation make one comment
	defer g.postMu.Unlock()
	if obj, found, err := g.Objects.ByOp(ctx, op); err != nil {
		return CommentInfo{}, err
	} else if found {
		if obj.Repo != repo || obj.ParentNumber != issue || obj.Table != table {
			return CommentInfo{}, fmt.Errorf("github: the operation %s was made for something else", op)
		}
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
	return c, g.Objects.Register(ctx, Object{Repo: repo, Kind: KindComment, Number: int(c.ID), GitHubID: c.ID, Table: table, ParentNumber: issue, Op: op})
}
