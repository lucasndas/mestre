package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
)

// EntryApprover is the guild.Approver of production: the entry of a machine in the
// Guild is a request of the type entrada_na_guilda, put in front of the Owner
// (and only the Owner) through the flow of approval, with the button and the
// PIN (docs/aprovacoes/fluxo.md#pedidos-da-guilda). The answer comes back as the
// chain.Sink of that type, and what the Citadel does with it is Citadel.Decide.
type EntryApprover struct {
	// Chain is the chain of approval of the period (nil result: not built yet), Decide
	// gives the answer to the Citadel and Tell warns the Owner of an answer that
	// could not be applied.
	Chain  func() *chain.Service
	Decide func(ctx context.Context, joinID string, d guild.Decision) error
	Tell   func(ctx context.Context, text string)
	Log    *slog.Logger
}

var (
	_ guild.Approver = EntryApprover{}
	_ chain.Sink     = EntryApprover{}
)

// entryTarget is what the request is bound to: the join it was made for, as the
// Citadel recorded it. It is read back from the request when the Owner answers.
type entryTarget struct {
	JoinID   string `json:"join_id"`
	Name     string `json:"name"`
	OS       string `json:"os"`
	Hostname string `json:"hostname"`
	Instance string `json:"instance"`
	Code     string `json:"code"`
	RemoteIP string `json:"remote_ip"`
}

// errNoEntryChain: the period has no chain of approval to ask the Owner through.
var errNoEntryChain = errors.New("app: this period has no chain of approval to ask the Owner about the entry")

// Ask puts the entry in front of the Owner. The name, the system and the host
// are what the new machine said about itself: they are shown quoted, as text of
// the machine, never as a field of the engine.
func (a EntryApprover) Ask(ctx context.Context, r guild.ApprovalRequest) (string, error) {
	svc := a.Chain()
	if svc == nil {
		return "", errNoEntryChain
	}
	t := entryTarget{JoinID: r.JoinID, Name: r.Name, OS: r.OS, Hostname: r.Hostname, Instance: r.Instance, Code: r.Code, RemoteIP: r.RemoteIP}
	target, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	// the code is the engine's, first and alone; what the machine said about itself
	// comes after, without the marks the channel turns into a link or bold
	about := fmt.Sprintf("Entrada na Guilda. Código para conferir com o da tela do instalador: %s. A máquina diz ser %q (sistema %q, host %q, instância %q, vinda de %q).",
		r.Code, plainText(r.Name), plainText(r.OS), plainText(r.Hostname), plainText(r.Instance), plainText(r.RemoteIP))
	id, err := svc.RequestSystem(ctx, chain.SystemRequest{Type: classes.RequestGuildEntry, About: about, Target: target})
	if err != nil {
		return "", fmt.Errorf("app: asking the Owner about the entry: %w", err)
	}
	return strconv.FormatInt(id, 10), nil
}

// Withdraw takes the question down: the request of the machine is over.
func (a EntryApprover) Withdraw(ctx context.Context, approvalID string) {
	svc := a.Chain()
	id, err := strconv.ParseInt(approvalID, 10, 64)
	if svc == nil || err != nil {
		return
	}
	if err := svc.Withdraw(ctx, id, "a entrada da máquina acabou sem resposta"); err != nil {
		a.Log.Warn("app: the question about an entry was not taken down", "request", id, "err", err)
	}
}

// Decided applies the answer of the Owner to the entry the request was made for.
// Only an approval or a denial that a user gave counts: an expired, cancelled or
// invalidated request leaves the entry to its own hour.
func (a EntryApprover) Decided(ctx context.Context, res chain.Result) error {
	r := res.Request
	if r.Type != classes.RequestGuildEntry || (r.State != chain.Approved && r.State != chain.Denied) {
		return nil
	}
	by, user := "", false
	if r.Decision != nil {
		by, user = strings.CutPrefix(r.Decision.By, "user:")
	}
	if !user || by == "" {
		return fmt.Errorf("app: the entry of request %d was %s by no user", r.ID, r.State)
	}
	var t entryTarget
	if err := json.Unmarshal(r.Target, &t); err != nil || t.JoinID == "" {
		return fmt.Errorf("app: the request %d names no entry", r.ID)
	}
	err := a.Decide(ctx, t.JoinID, guild.Decision{Accept: r.State == chain.Approved, ApprovedBy: by})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, guild.ErrExpired), errors.Is(err, guild.ErrNotPending):
		a.Tell(ctx, fmt.Sprintf("A entrada da máquina %q já não estava esperando (a hora acabou ou foi decidida): nada foi emitido. Peça que ela tente de novo.", plainText(t.Name)))
	default:
		a.Tell(ctx, fmt.Sprintf("A resposta sobre a entrada da máquina %q não pôde ser aplicada agora (%v). Nada foi emitido; peça que ela tente de novo.", plainText(t.Name), err))
	}
	return err
}

// plainText drops the marks the Telegram turns into links, bold or code, so a
// name the new machine chose cannot dress as a field of the engine.
func plainText(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune("[]()*_`<>~|\\#", r) {
			return -1
		}
		return r
	}, s)
}
