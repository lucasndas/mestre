package basesync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// NoCommandWarning is what the Grão-Mestre reads when the workspace has no
// verification command confirmed: nothing is approved alone
// (docs/github/acoes-por-nivel.md#merge).
const NoCommandWarning = "sem comando de verificação configurado"

// BaseMovedWarning is why the Grão-Mestre does not decide a request whose gate
// ran on a tip of the base that is no longer the tip.
const BaseMovedWarning = "a branch principal andou depois do gate de frescor: o pedido será invalidado e o gate roda de novo"

// NotGatedWarning is why the Grão-Mestre does not decide a request that was born
// when the gate did not apply and does now.
const NotGatedWarning = "o gate de frescor não rodou neste pedido: abra o pedido de novo para a base ser verificada"

// BaseTip implements chain.Freshness.
func (g *Gate) BaseTip(ctx context.Context, r chain.Request) (string, error) {
	t, err := g.o.Tables.Get(ctx, r.Workspace, r.Table)
	if err != nil {
		return "", err
	}
	mirror, err := g.o.Mirrors.OpenMirror(ctx, t.Workspace)
	if err != nil {
		return "", err
	}
	tip, err := mirror.ResolveRef(ctx, "refs/remotes/origin/"+t.BaseBranch)
	return string(tip), err
}

// Rules reads what the branch of a repository asks of a PR
// (github.REST.ReadRules, with the App of the workspace).
type Rules interface {
	ReadRules(ctx context.Context, workspace, repo, branch string) (github.ReviewRules, error)
}

// Requests is what the gate needs of the chain of approval (*chain.Service):
// told the new tip of the base, it ends the requests whose gate ran on another.
type Requests interface {
	BaseMoved(ctx context.Context, workspace, table, tip string) error
}

// GateOptions are the pieces of the Gate.
type GateOptions struct {
	Rules Rules
	// Verifier is the one of the done of the quests (quests.Verifier). Nil: no
	// workspace has a command confirmed, and no request is gated through.
	Verifier quests.Verifier
	Tables   Tables
	Mirrors  Mirrors
	// Base makes the step `sync` when the gate fails.
	Base   *Service
	Logger *slog.Logger
}

// Gate is the gate of freshness (chain.Freshness): before the request of a PR
// is asked of anyone, and when the repository does not require the branch to be
// up to date, it proves the base integrated with the head by the verification
// of the workspace. The commands and the worktree are the Verifier's: the gate
// hands it the table, the branch and the tip of the base, never a command.
type Gate struct {
	o   GateOptions
	log *slog.Logger
}

var _ chain.Freshness = (*Gate)(nil)

// NewGate returns the Gate.
func NewGate(o GateOptions) (*Gate, error) {
	if o.Rules == nil || o.Tables == nil || o.Mirrors == nil || o.Base == nil {
		return nil, errors.New("basesync: the gate needs the rules of the branch, the tables, the mirrors and the base sync")
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Gate{o: o, log: o.Logger}, nil
}

// applies says whether the gate is the one that guards the merge: the rule
// "branch atualizada" is what makes GitHub itself refuse the merge of a head
// that lacks the base.
func (g *Gate) applies(ctx context.Context, r chain.Request, base string) (bool, error) {
	rules, err := g.o.Rules.ReadRules(ctx, r.Workspace, r.PR.Repo, base)
	if err != nil {
		return false, fmt.Errorf("the rules of the branch %s: %w", base, err)
	}
	return !rules.UpToDate, nil
}

func (g *Gate) confirmed(ctx context.Context, workspace string) (bool, error) {
	if g.o.Verifier == nil {
		return false, nil
	}
	return g.o.Verifier.Confirmed(ctx, workspace)
}

// Open implements chain.Freshness.
func (g *Gate) Open(ctx context.Context, r chain.Request) (chain.FreshnessVerdict, error) {
	if r.Table == "" {
		return chain.FreshnessVerdict{}, errors.New("the request has no table")
	}
	t, err := g.o.Tables.Get(ctx, r.Workspace, r.Table)
	if err != nil {
		return chain.FreshnessVerdict{}, err
	}
	run, err := g.applies(ctx, r, t.BaseBranch)
	if err != nil || !run {
		return chain.FreshnessVerdict{}, err
	}
	ok, err := g.confirmed(ctx, r.Workspace)
	if err != nil {
		return chain.FreshnessVerdict{}, fmt.Errorf("the verification commands of %s: %w", r.Workspace, err)
	}
	if !ok {
		// the request opens, and the Grão-Mestre is told at the decision
		return chain.FreshnessVerdict{}, nil
	}
	mirror, err := g.o.Mirrors.OpenMirror(ctx, t.Workspace)
	if err != nil {
		return chain.FreshnessVerdict{}, err
	}
	tip, err := mirror.ResolveRef(ctx, "refs/remotes/origin/"+t.BaseBranch)
	if err != nil {
		return chain.FreshnessVerdict{}, fmt.Errorf("the base origin/%s: %w", t.BaseBranch, err)
	}
	head, err := mirror.ResolveRef(ctx, "refs/heads/"+t.Branch)
	if err != nil {
		return chain.FreshnessVerdict{}, fmt.Errorf("the branch %s: %w", t.Branch, err)
	}
	if string(head) != r.PR.SHA {
		// what is proved must be what GitHub merges
		return chain.FreshnessVerdict{}, fmt.Errorf("the branch %s is at %.8s in the mirror, not at the %.8s of the PR", t.Branch, head, r.PR.SHA)
	}
	res, err := g.o.Verifier.Verify(ctx, quests.VerifyRequest{Workspace: t.Workspace, Table: t.ID, Branch: t.Branch, Base: string(tip), Head: string(head)})
	if err != nil {
		return chain.FreshnessVerdict{}, fmt.Errorf("the verification: %w", err)
	}
	if res.OK {
		return chain.FreshnessVerdict{BaseSHA: string(tip)}, nil
	}
	// red: the table integrates the base (the step sync) and the request does not exist
	out, err := g.o.Base.Ensure(ctx, t.Workspace, t.ID)
	if err != nil {
		g.log.Warn("basesync: the step sync was not made after a red gate", "table", t.ID, "err", err)
	}
	return chain.FreshnessVerdict{Refused: true, Reason: refusedText(string(tip), res, out, err)}, nil
}

// refusedText is what the Grão-Mestre reads of a red gate. The output is the
// project's own text, quoted.
func refusedText(tip string, res quests.VerifyResult, sync Result, syncErr error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "o gate de frescor reprovou: a base %.8s integrada à branch da mesa não passa na verificação", tip)
	switch {
	case res.TimedOut:
		fmt.Fprintf(&b, " (o comando %q passou do tempo)", res.Command)
	case res.Command != "":
		fmt.Fprintf(&b, " (o comando %q falhou)", res.Command)
	}
	b.WriteString(". Nenhum pedido de aprovação foi aberto. ")
	switch {
	case syncErr != nil:
		b.WriteString("A etapa sync não pôde ser criada (veja o log do motor).")
	case sync.Outcome == Created || sync.Outcome == Open:
		fmt.Fprintf(&b, "A mesa recebeu a etapa sync (missão %s); peça de novo quando ela concluir.", sync.Quest)
	case sync.Outcome == NoDev:
		b.WriteString("A mesa não tem quem faça a etapa sync: o Mestre da mesa foi avisado.")
	case sync.Outcome == UpToDate:
		b.WriteString("A branch da mesa já tem a base: a verificação falha na própria branch.")
	default:
		b.WriteString("A etapa sync nasce quando a mesa puder recebê-la.")
	}
	if out := strings.TrimSpace(res.Output); out != "" {
		b.WriteString("\n" + inbox.Say("Saída da verificação (texto do projeto, não verificado):", out, "|"))
	}
	return b.String()
}

// Unverifiable implements chain.Freshness: with the repository not asking the
// branch to be up to date, the Grão-Mestre does not decide alone a request of a
// workspace with no verification command confirmed.
func (g *Gate) Unverifiable(ctx context.Context, r chain.Request) (string, error) {
	if r.Table == "" {
		return "", errors.New("the request has no table")
	}
	t, err := g.o.Tables.Get(ctx, r.Workspace, r.Table)
	if err != nil {
		return "", err
	}
	if gated := chain.BaseSHAOf(r.Target); gated != "" {
		// a base that moved after the gate ended the request (BaseMoved); a decision
		// that slips in before that is still not made alone
		mirror, err := g.o.Mirrors.OpenMirror(ctx, t.Workspace)
		if err != nil {
			return "", err
		}
		tip, err := mirror.ResolveRef(ctx, "refs/remotes/origin/"+t.BaseBranch)
		if err != nil {
			return "", fmt.Errorf("the base origin/%s: %w", t.BaseBranch, err)
		}
		if string(tip) != gated {
			return BaseMovedWarning, nil
		}
	}
	run, err := g.applies(ctx, r, t.BaseBranch)
	if err != nil || !run {
		return "", err
	}
	ok, err := g.confirmed(ctx, r.Workspace)
	if err != nil {
		return "", fmt.Errorf("the verification commands of %s: %w", r.Workspace, err)
	}
	switch {
	case !ok:
		return NoCommandWarning, nil
	case chain.BaseSHAOf(r.Target) == "":
		// born with no command or with the rule in the repository, which is gone now:
		// the base integrated was never proved for this request
		return NotGatedWarning, nil
	}
	return "", nil
}

// EventSink is the github.Sink that makes the step `sync` from the second
// signal of a base that moved: a changed PR of a table
// (docs/orquestracao/sincronia-com-a-base.md#quando-a-etapa-nasce). The event
// only wakes the check: Ensure reads the mirror, so a PR that is not behind
// changes nothing and one under way is not made twice.
type EventSink struct{ Base *Service }

var _ github.Sink = EventSink{}

// Deliver implements github.Sink.
func (s EventSink) Deliver(ctx context.Context, w github.Watch, table string, evs []github.Event) error {
	if table == "" {
		return nil
	}
	for _, e := range evs {
		if e.Kind != github.EventPRChanged {
			continue
		}
		// a table that is gone is nothing to retry: the event would come again forever
		if _, err := s.Base.Ensure(ctx, w.Workspace, table); err != nil && !errors.Is(err, tables.ErrNoTable) {
			return err
		}
		return nil // one check is all the batch needs
	}
	return nil
}
