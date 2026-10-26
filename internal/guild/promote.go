package guild

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Errors of the promotion (docs/guilda/cidadela-e-torres.md#promoção).
var (
	// ErrNoLock: the Guild has no lock, so the epoch cannot change.
	ErrNoLock = errors.New("guild: the Guild has no lock: no promotion")
	// ErrLostLock: another machine won the epoch in the lock.
	ErrLostLock = errors.New("guild: another machine won the epoch in the lock")
	// ErrNotPromotable: the machine may not take the place of the Citadel.
	ErrNotPromotable = errors.New("guild: this machine cannot be promoted")
	// ErrRefusedByOwner: the Owner said no, or the question was withdrawn.
	ErrRefusedByOwner = errors.New("guild: the Owner did not approve the promotion")
)

// PromotionRequest is the question to the Owner when the Citadel does not
// come back: the request of type promocao_cidadela, only the Owner, with the
// PIN (docs/aprovacoes/fluxo.md#pedidos-da-guilda).
type PromotionRequest struct {
	// MachineID and Name are the machine to promote.
	MachineID, Name string
	// Epoch is the epoch it would leave; ReplicaLSN the last lsn it applied,
	// so the Owner sees how far behind it is.
	Epoch      uint64
	ReplicaLSN uint64
	// Reason is the diagnosis so far.
	Reason    string
	CreatedAt time.Time
}

// PromotionApprover puts the question to the Owner, through the Telegram in
// reduced mode and the fallback page (milestones 5 and 7), with the button
// and the PIN: the Guild only asks, and the answer comes back as
// Promoter.Decide. In the tests of this milestone it is a double.
type PromotionApprover interface {
	AskPromotion(ctx context.Context, r PromotionRequest) (approvalID string, err error)
	// WithdrawPromotion takes the question down: the Citadel came back, or a
	// newer request replaced it.
	WithdrawPromotion(ctx context.Context, approvalID string)
}

// Promoter keeps the one promotion that waits for the Owner: a second
// request cancels the first (docs/guilda/cidadela-e-torres.md#regras-e-erros).
type Promoter struct {
	approver PromotionApprover

	mu      sync.Mutex
	pending string
	answer  chan Decision
	// early are the answers given while AskPromotion ran, before its id was
	// known, by id: only the one of that id counts.
	early map[string]Decision
}

// maxEarlyAnswers bounds the answers kept while a question is being put.
const maxEarlyAnswers = 8

// NewPromoter returns the promoter over the Owner's channel.
func NewPromoter(a PromotionApprover) *Promoter { return &Promoter{approver: a} }

// Ask puts the request to the Owner and waits for the answer or for ctx. It
// returns the id of the approval and who approved; a refusal, a withdrawal or
// the end of ctx is ErrRefusedByOwner (the end of ctx wrapped).
func (p *Promoter) Ask(ctx context.Context, r PromotionRequest) (approvalID, approvedBy string, err error) {
	if p == nil || p.approver == nil {
		return "", "", fmt.Errorf("%w: no channel to the Owner", ErrRefusedByOwner)
	}
	p.mu.Lock()
	if old := p.pending; old != "" {
		p.pending = ""
		close(p.answer)
		p.answer = nil
		p.mu.Unlock()
		p.approver.WithdrawPromotion(ctx, old)
		p.mu.Lock()
	}
	answer := make(chan Decision, 1)
	p.answer, p.early = answer, map[string]Decision{}
	p.mu.Unlock()
	id, err := p.approver.AskPromotion(ctx, r)
	if err != nil {
		p.mu.Lock()
		if p.answer == answer {
			p.answer, p.early = nil, nil
		}
		p.mu.Unlock()
		return "", "", err
	}
	p.mu.Lock()
	if p.answer != answer { // replaced while asking
		p.mu.Unlock()
		return "", "", fmt.Errorf("%w: replaced by a newer request", ErrRefusedByOwner)
	}
	p.pending = id
	if d, ok := p.early[id]; ok {
		answer <- d
	}
	p.early = nil
	p.mu.Unlock()
	// An answer given while AskPromotion ran is already in the channel.
	select {
	case d, ok := <-answer:
		p.clear(id)
		if !ok || !d.Accept {
			return "", "", ErrRefusedByOwner
		}
		return id, d.ApprovedBy, nil
	case <-ctx.Done():
		p.clear(id)
		p.approver.WithdrawPromotion(context.WithoutCancel(ctx), id)
		return "", "", fmt.Errorf("%w: %w", ErrRefusedByOwner, ctx.Err())
	}
}

func (p *Promoter) clear(id string) {
	p.mu.Lock()
	if p.pending == id {
		p.pending = ""
	}
	p.mu.Unlock()
}

// Decide is the Owner's answer, given by the bot or the fallback page after
// the PIN. It counts only for the exact id of the question that waits; an
// answer given while the question is still being put is kept by id and
// counts only if the question gets that id.
func (p *Promoter) Decide(approvalID string, d Decision) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.answer == nil || approvalID == "":
		return ErrNotPending
	case p.pending == "":
		if len(p.early) < maxEarlyAnswers {
			p.early[approvalID] = d
		}
		return nil
	case p.pending != approvalID:
		return ErrNotPending
	}
	select {
	case p.answer <- d:
	default:
	}
	return nil
}

// Cancel withdraws the pending request: the Citadel came back.
func (p *Promoter) Cancel(ctx context.Context) {
	p.mu.Lock()
	id := p.pending
	if id != "" {
		p.pending = ""
		close(p.answer)
		p.answer = nil
	}
	p.mu.Unlock()
	if id != "" {
		p.approver.WithdrawPromotion(ctx, id)
	}
}

// ClaimEpoch wins the next epoch in the lock (docs/guilda/trava-no-github.md#promoção-pela-trava):
// it reads the highest epoch the lock shows and tries to create the next one
// for machineID. Losing is ErrLostLock; a lock that cannot be reached is no
// promotion.
func ClaimEpoch(ctx context.Context, lock Lock, current uint64, machineID string) (uint64, error) {
	if lock == nil {
		return 0, ErrNoLock
	}
	n, err := lock.Check(ctx)
	if err != nil {
		return 0, fmt.Errorf("guild: the lock cannot be read, no promotion: %w", err)
	}
	next := max(n, current) + 1
	won, err := lock.Claim(ctx, next, machineID)
	if err != nil {
		return 0, fmt.Errorf("guild: epoch %d in the lock: %w", next, err)
	}
	if !won {
		return 0, fmt.Errorf("%w: epoch %d", ErrLostLock, next)
	}
	return next, nil
}

// Command is what the promoted machine writes when it takes the command.
type Command struct {
	Epoch      uint64
	MachineID  string
	ApprovalID string
	ApprovedBy string
	// FromLSN is the last lsn the machine applied as a replica: the new
	// Citadel goes on from there, and what the old one committed after it is
	// lost (docs/guilda/replicacao.md#perda-na-troca).
	FromLSN uint64
}

// TakeCommand writes the new epoch in the Guild database that just became the
// main one: the certificate signed by the CA, the epoch in the guild row, the
// row of epochs with the last lsn applied, and the promotion in the audit.
// It returns the certificate, which the machine then follows.
func TakeCommand(ctx context.Context, db *store.DB, ca *transport.CA, clk clock.Clock, c Command) (protocol.Certificate, error) {
	if clk == nil {
		clk = clock.Real{}
	}
	g, err := readGuild(ctx, db.Reader())
	if err != nil {
		return protocol.Certificate{}, err
	}
	if c.Epoch <= g.Epoch.Epoch {
		return protocol.Certificate{}, fmt.Errorf("%w: epoch %d, the Guild is on %d", ErrStaleCertificate, c.Epoch, g.Epoch.Epoch)
	}
	now := clk.Now().UTC()
	cert, err := SignEpoch(ca, protocol.Certificate{
		GuildID: g.GuildID, Epoch: c.Epoch, CitadelMachineID: c.MachineID,
		ApprovedBy: c.ApprovedBy, ApprovalID: c.ApprovalID, IssuedAt: protocol.Timestamp{Time: now},
	})
	if err != nil {
		return protocol.Certificate{}, err
	}
	tx, err := db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return protocol.Certificate{}, fmt.Errorf("guild: take the command: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE guild SET epoch = ?, epoch_certificate = ? WHERE id = 1`, c.Epoch, encodeCert(cert)); err != nil {
		return protocol.Certificate{}, fmt.Errorf("guild: record the epoch: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO epochs (epoch, citadel_machine_id, from_lsn, approval_id, approved_by, started_at) VALUES (?, ?, ?, ?, ?, ?)`,
		c.Epoch, c.MachineID, c.FromLSN, c.ApprovalID, c.ApprovedBy, now.UnixMilli()); err != nil {
		return protocol.Certificate{}, fmt.Errorf("guild: record the epoch: %w", err)
	}
	detail, _ := json.Marshal(map[string]any{
		"epoch": c.Epoch, "from_lsn": c.FromLSN, "approval_id": c.ApprovalID, "approved_by": c.ApprovedBy, "previous": g.Epoch.CitadelMachineID,
	})
	if err := audit(ctx, tx, protocol.NewIDGen(clk).NewString(), now, c.MachineID, "citadel.promoted", detail); err != nil {
		return protocol.Certificate{}, err
	}
	if err := tx.Commit(); err != nil {
		return protocol.Certificate{}, fmt.Errorf("guild: take the command: %w", err)
	}
	return cert, nil
}

// EpochRecord is a row of epochs.
type EpochRecord struct {
	Epoch            uint64
	CitadelMachineID string
	FromLSN          uint64
	ApprovalID       string
	ApprovedBy       string
	StartedAt        time.Time
	// ReconciledAt is when the Citadel of the epoch finished reconciling GitHub
	// after taking it (zero until it did).
	ReconciledAt time.Time
}

// ErrNoEpoch: the database has no row for that epoch (yet: a replica learns
// it with the full copy).
var ErrNoEpoch = errors.New("guild: the database does not know this epoch")

// EpochStart reads the row of an epoch.
func EpochStart(ctx context.Context, db *store.DB, epoch uint64) (EpochRecord, error) {
	var (
		r  EpochRecord
		at int64
		rc int64
	)
	err := db.Reader().QueryRowContext(ctx,
		`SELECT epoch, citadel_machine_id, from_lsn, approval_id, approved_by, started_at, reconciled_at FROM epochs WHERE epoch = ?`, epoch).
		Scan(&r.Epoch, &r.CitadelMachineID, &r.FromLSN, &r.ApprovalID, &r.ApprovedBy, &at, &rc)
	if errors.Is(err, sql.ErrNoRows) {
		return EpochRecord{}, ErrNoEpoch
	}
	if err != nil {
		return EpochRecord{}, fmt.Errorf("guild: read the epoch %d: %w", epoch, err)
	}
	r.StartedAt = time.UnixMilli(at).UTC()
	if rc != 0 {
		r.ReconciledAt = time.UnixMilli(rc).UTC()
	}
	return r, nil
}

// MarkReconciled records that the Citadel of the epoch finished reconciling
// GitHub, so a restart does not do it again.
func MarkReconciled(ctx context.Context, db *store.DB, epoch uint64, at time.Time) error {
	return db.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE epochs SET reconciled_at = ? WHERE epoch = ?`, at.UnixMilli(), epoch)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNoEpoch
		}
		return nil
	})
}

// audit writes a row of audit_log in a transaction.
func audit(ctx context.Context, q querier, id string, at time.Time, machineID, action string, detail []byte) error {
	if _, err := q.ExecContext(ctx,
		`INSERT INTO audit_log (id, at, machine_id, action, detail) VALUES (?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		id, at.UnixMilli(), machineID, action, string(detail)); err != nil {
		return fmt.Errorf("guild: write the audit: %w", err)
	}
	return nil
}
