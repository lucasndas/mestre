package replication

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// ReplicaConfig wires the Tower side.
type ReplicaConfig struct {
	// DB is the replica of the Guild database (opened with Options.Replica).
	DB    *store.DB
	Clock clock.Clock
	// GuildID, when set, makes the Tower ignore records of another Guild.
	GuildID string
	// Vault keeps the copy of the vault that vault.replica brings; nil means
	// the machine takes none.
	Vault *vault.Copier
	// Files and Mirrors are where the files and the mirrors of the Citadel are
	// copied to. Both optional.
	Files   *Files
	Mirrors *MirrorStore
	// AckEvery and AckRecords: confirm every second or 256 records.
	AckEvery   time.Duration
	AckRecords int
	// MaxQueueBytes bounds the records waiting to be applied.
	MaxQueueBytes int
	Retention     Retention
	// TransferDir is where the transfers in progress are kept
	// (protocol.XferConfig.Dir). vault.drop removes from it the full copies
	// that were still arriving, which carry the vault tables. Optional.
	TransferDir string
	// ResyncRetry is how long a request for a full copy, or for the records
	// again, waits for the answer before it is made again (default 30 s).
	ResyncRetry time.Duration
	// OnApplied, when set, is told after each batch of records is committed
	// to the replica: the first and the last lsn of it.
	OnApplied func(first, last uint64)
	Logger    *slog.Logger
}

// Replica is the replication on a Tower.
type Replica struct {
	cfg  ReplicaConfig
	gid  protocol.ULID
	hasG bool

	// applyMu serializes the writes to the replica: applying records, swapping
	// the file for a full copy, erasing the vault.
	applyMu sync.Mutex

	mu          sync.Mutex
	link        *protocol.Link
	queue       []store.LogRow
	queueBytes  int
	lastQueued  uint64
	applied     uint64
	lastAcked   uint64
	kick        chan struct{}
	needCopy    bool
	resyncAsked bool
	excluded    bool
	state       string
	lastErr     string
	erasing     atomic.Bool // a retry of an incomplete erasing is under way

	// bg are the erasings in the background: Run waits for them (after
	// canceling bgCtx) before it returns, so none of them touches the files
	// of the replica once the owner of the replica closes it.
	bg      sync.WaitGroup
	bgCtx   context.Context
	bgStop  context.CancelFunc
	stopped bool
	oldest  time.Time // when the oldest record waiting in the queue was received
	askedAt time.Time // when the last request to the Citadel for records or a copy was made
}

// NewReplica checks the configuration.
func NewReplica(ctx context.Context, cfg ReplicaConfig) (*Replica, error) {
	if cfg.DB == nil || !cfg.DB.IsReplica() {
		return nil, errors.New("replication: the Tower side needs a database opened as a replica")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.AckEvery <= 0 {
		cfg.AckEvery = DefaultAckEvery
	}
	if cfg.AckRecords <= 0 {
		cfg.AckRecords = DefaultAckRecords
	}
	if cfg.MaxQueueBytes <= 0 {
		cfg.MaxQueueBytes = DefaultMaxQueueBytes
	}
	cfg.Retention = cfg.Retention.withDefaults()
	if cfg.ResyncRetry <= 0 {
		cfg.ResyncRetry = DefaultResyncRetry
	}
	r := &Replica{cfg: cfg, kick: make(chan struct{}, 1), state: "ok"}
	r.bgCtx, r.bgStop = context.WithCancel(context.Background())
	// A machine that was told to erase the vault stays that way across a
	// restart, until the vault comes back.
	if _, err := os.Stat(r.droppedMarker()); err == nil {
		r.excluded = true
	}
	if cfg.GuildID != "" {
		g, err := protocol.ParseULID(cfg.GuildID)
		if err != nil {
			return nil, fmt.Errorf("replication: guild_id %q: %w", cfg.GuildID, err)
		}
		r.gid, r.hasG = g, true
	}
	lsn, err := cfg.DB.LastLSN(ctx)
	if err != nil {
		// A replica that never got a copy has no log yet.
		lsn = 0
	}
	r.lastQueued, r.applied, r.lastAcked = lsn, lsn, lsn
	return r, nil
}

// Attach tells the Replica which Link to send through (the Client's).
func (r *Replica) Attach(l *protocol.Link) {
	r.mu.Lock()
	r.link = l
	r.mu.Unlock()
}

// droppedMarker is the file that remembers, across restarts, that the vault
// was erased on an order of the Citadel: this machine is excluded.
func (r *Replica) droppedMarker() string { return r.cfg.DB.Path() + ".excluded" }

// The marker holds the order (first line) and, while the receipt is owed
// because the WAL could not be truncated, a second one.
const (
	markerDrop       = "vault.drop\n"
	markerIncomplete = "purge incomplete\n"
)

// purgeOwed says whether an erasing ended with the WAL not truncated, so the
// secrets may still be in the -wal file and the receipt was not sent.
func (r *Replica) purgeOwed() bool {
	b, err := os.ReadFile(r.droppedMarker())
	return err == nil && strings.Contains(string(b), markerIncomplete)
}

// setPurgeOwed records, on disk, that the purge is incomplete (or that it is
// done). It never makes a machine excluded that is not.
func (r *Replica) setPurgeOwed(on bool) {
	r.mu.Lock()
	excluded := r.excluded
	r.mu.Unlock()
	if !excluded {
		return
	}
	content := markerDrop
	if on {
		content += markerIncomplete
	}
	if err := os.WriteFile(r.droppedMarker(), []byte(content), 0o600); err != nil {
		r.cfg.Logger.Error("replication: remembering the state of the erasing", "err", err)
	}
}

// Hello fills what the hello says of the replica: the last lsn applied and the
// epoch it belongs to. A replica that waits for a full copy says it has
// nothing (lsn 0), so the Citadel starts with one however the request for it
// went.
func (r *Replica) Hello(ctx context.Context, info *protocol.HelloInfo) {
	r.mu.Lock()
	waiting := r.needCopy
	r.mu.Unlock()
	if waiting {
		return
	}
	lsn, err := r.cfg.DB.LastLSN(ctx)
	if err != nil {
		lsn = 0
	}
	info.ReplicaLSN = lsn
	if lsn > 0 {
		_, info.ReplicaEpoch, _ = readGuild(ctx, r.cfg.DB)
	}
}

// Register adds the receivers of what the Citadel sends: the records, the vault
// and the order to erase it.
func (r *Replica) Register(router *protocol.Router) {
	router.HandleData(protocol.DataReplica, protocol.DataHandlerFunc(r.handleData))
	router.HandleFunc(protocol.TypeVaultReplica, r.handleVaultReplica)
	router.HandleFunc(protocol.TypeVaultDrop, r.handleVaultDrop)
}

// Finalizers are the receivers of the transfers that belong to the
// replication: the full copy, the files and the mirrors.
func (r *Replica) Finalizers() map[string]protocol.Finalizer {
	m := map[string]protocol.Finalizer{protocol.KindDBSnapshot: protocol.FinalizerFunc(r.finalizeSnapshot)}
	if r.cfg.Files != nil {
		m[protocol.KindFile] = &fileReceiver{files: r.cfg.Files}
	}
	if r.cfg.Mirrors != nil {
		m[protocol.KindGitBundle] = newMirrorReceiver(*r.cfg.Mirrors)
	}
	return m
}

// Status is the state of the replica, for the checklist and the panel.
type Status struct {
	// LSN is the last record applied; Queued how many wait to be applied.
	LSN    uint64
	Queued int
	// State is ok, waiting-copy (a full copy was asked for), disk-full or failed;
	// Err says why when it is not ok.
	State string
	Err   string
	// Lag is how long the oldest record in the queue has been waiting.
	Lag time.Duration
	// VaultDropped is true after vault.drop, until the vault comes back.
	VaultDropped bool
}

// Status returns the state of the replica.
func (r *Replica) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := Status{LSN: r.applied, Queued: len(r.queue), State: r.state, Err: r.lastErr, VaultDropped: r.excluded}
	if len(r.queue) > 0 {
		st.Lag = max(0, r.cfg.Clock.Now().Sub(r.oldest))
	}
	return st
}

// LagSeconds is the lag for machine.heartbeat: how long records have waited
// to be applied.
func (r *Replica) LagSeconds() *float64 {
	l := r.Status().Lag.Seconds()
	return &l
}

// handleData runs on the read goroutine of the connection and so only queues.
func (r *Replica) handleData(_ context.Context, _ protocol.Peer, f protocol.DataFrame) {
	if r.hasG && f.ID != r.gid {
		return
	}
	rows, err := parseRecords(f.Pos, f.Data)
	if err != nil {
		r.cfg.Logger.Warn("replication: bad frame of records", "err", err)
		r.askFrom(true)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.needCopy || len(rows) == 0 {
		return
	}
	skip := 0
	for skip < len(rows) && rows[skip].LSN <= r.lastQueued {
		skip++ // a repeat
	}
	rows = rows[skip:]
	if len(rows) == 0 {
		return
	}
	if rows[0].LSN != r.lastQueued+1 {
		r.askFromLocked(false)
		return
	}
	size := 0
	for _, row := range rows {
		size += len(row.Body)
	}
	if r.queueBytes+size > r.cfg.MaxQueueBytes {
		r.askFromLocked(false)
		return
	}
	if len(r.queue) == 0 {
		r.oldest = r.cfg.Clock.Now()
	}
	r.queue = append(r.queue, rows...)
	r.queueBytes += size
	r.lastQueued = rows[len(rows)-1].LSN
	r.resyncAsked = false
	select {
	case r.kick <- struct{}{}:
	default:
	}
}

// askFrom asks the Citadel to send again from the last record queued (a hole)
// or, when full is true, for a full copy.
func (r *Replica) askFrom(full bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.askFromLocked(full)
}

func (r *Replica) askFromLocked(full bool) {
	var from *uint64
	if !full {
		if r.resyncAsked {
			return // already asked; the answer is on its way
		}
		r.resyncAsked = true
		v := r.lastQueued
		from = &v
	} else {
		// Remembered even when there is nobody to ask now: the hello of the
		// next connection and the retry take it from here.
		r.needCopy = true
		r.state = "waiting-copy"
		r.queue, r.queueBytes = nil, 0
	}
	r.sendResyncLocked(from)
}

// sendResyncLocked makes the request, in the background.
func (r *Replica) sendResyncLocked(from *uint64) {
	link := r.link
	if link == nil {
		return
	}
	r.askedAt = r.cfg.Clock.Now()
	go func() {
		if _, err := link.Send(context.Background(), protocol.Outgoing{Type: protocol.TypeReplicaResync, Payload: protocol.ReplicaResync{FromLSN: from}}); err != nil {
			r.cfg.Logger.Warn("replication: replica.resync", "err", err)
			r.mu.Lock()
			r.askedAt = time.Time{} // not made: the next tick makes it again
			r.mu.Unlock()
		}
	}()
}

// retryAsk makes again a request the Citadel did not answer in ResyncRetry:
// the request may have been lost on the way, or arrived before the Citadel had
// the connection ready.
func (r *Replica) retryAsk() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.link == nil || (!r.needCopy && !r.resyncAsked) || r.cfg.Clock.Now().Sub(r.askedAt) < r.cfg.ResyncRetry {
		return
	}
	if r.needCopy {
		r.cfg.Logger.Warn("replication: the full copy did not come; asking again")
		r.sendResyncLocked(nil)
		return
	}
	r.resyncAsked = false
	r.askFromLocked(false)
}

// Run applies the records as they come and confirms them, and applies the
// retention of the log of the replica, until ctx ends.
func (r *Replica) Run(ctx context.Context) error {
	defer r.waitBackground()
	ack := r.cfg.Clock.NewTicker(r.cfg.AckEvery)
	defer ack.Stop()
	prune := r.cfg.Clock.NewTicker(PruneEvery)
	defer prune.Stop()
	retry := r.cfg.Clock.NewTicker(max(r.cfg.ResyncRetry/4, time.Second))
	defer retry.Stop()
	for {
		r.drain(ctx)
		r.sendAck(false)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.kick:
		case <-retry.C():
			r.retryAsk()
			r.retryErasing()
		case <-ack.C():
			r.sendAck(true)
		case <-prune.C():
			r.applyMu.Lock()
			if _, err := r.cfg.DB.PruneLog(ctx, r.cfg.Retention.MaxAge, r.cfg.Retention.MaxBytes); err != nil && ctx.Err() == nil {
				r.cfg.Logger.Warn("replication: pruning the log of the replica", "err", err)
			}
			r.applyMu.Unlock()
		}
	}
}

// drain applies what is queued.
func (r *Replica) drain(ctx context.Context) {
	for ctx.Err() == nil {
		r.mu.Lock()
		if len(r.queue) == 0 || r.needCopy {
			r.mu.Unlock()
			return
		}
		n, bytes := 0, 0
		for n < len(r.queue) && n < 1000 && bytes < 8<<20 {
			bytes += len(r.queue[n].Body)
			n++
		}
		batch := append([]store.LogRow(nil), r.queue[:n]...)
		excluded := r.excluded
		r.mu.Unlock()

		if excluded { // vault.drop may overtake records still on their way
			tables := tableSet(VaultTables)
			for i := range batch {
				if row, err := strip(batch[i], tables); err == nil {
					batch[i] = row
				}
			}
		}
		r.applyMu.Lock()
		_, err := r.cfg.DB.ApplyLog(ctx, batch)
		if err == nil && excluded && hasSchemaChange(batch) {
			// A migration runs as SQL, and what it does to the vault tables
			// (seed them, copy from them) is not in the records that were
			// stripped: empty them again.
			err = r.purgeVault(ctx)
		}
		r.applyMu.Unlock()
		if err != nil {
			r.failed(err)
			return
		}
		r.mu.Lock()
		r.queue = r.queue[n:]
		for _, row := range batch {
			r.queueBytes -= len(row.Body)
		}
		if len(r.queue) == 0 {
			r.queue, r.queueBytes = nil, 0
		} else {
			r.oldest = r.cfg.Clock.Now()
		}
		r.applied = batch[len(batch)-1].LSN
		if r.state == "disk-full" {
			r.state, r.lastErr = "ok", ""
		}
		r.mu.Unlock()
		if cb := r.cfg.OnApplied; cb != nil {
			cb(batch[0].LSN, batch[len(batch)-1].LSN)
		}
	}
}

// hasSchemaChange says whether a batch carries a migration.
func hasSchemaChange(rows []store.LogRow) bool {
	for _, row := range rows {
		cs, err := store.DecodeChanges(row.Body)
		if err != nil {
			return true
		}
		for _, c := range cs {
			if c.Op == store.OpSQL {
				return true
			}
		}
	}
	return false
}

// failed decides what an error of the apply means.
func (r *Replica) failed(err error) {
	var gap *store.GapError
	switch {
	case errors.As(err, &gap):
		r.mu.Lock()
		r.queue, r.queueBytes = nil, 0
		r.lastQueued, r.applied = gap.Have, gap.Have
		r.resyncAsked = false
		r.askFromLocked(false)
		r.mu.Unlock()
	case strings.Contains(err.Error(), "disk is full"):
		// The records stay queued and are tried again; the Citadel sees the
		// acks stop and marks the replica as late.
		r.mu.Lock()
		if r.state != "disk-full" {
			r.cfg.Logger.Error("replication: the disk is full; replication paused", "err", err)
		}
		r.state, r.lastErr = "disk-full", err.Error()
		r.mu.Unlock()
	default:
		// The replica no longer matches the primary: throw it away and ask for a
		// full copy.
		r.cfg.Logger.Error("replication: could not apply a record; asking for a full copy", "err", err)
		r.mu.Lock()
		r.lastErr = err.Error()
		r.mu.Unlock()
		r.askFrom(true)
	}
}

// sendAck confirms the last record applied when the Citadel is 256 records
// behind the last confirmation or, with force, whenever something is new.
func (r *Replica) sendAck(force bool) {
	r.mu.Lock()
	lsn, link := r.applied, r.link
	due := lsn > r.lastAcked && (force || lsn-r.lastAcked >= uint64(r.cfg.AckRecords))
	if due {
		r.lastAcked = lsn
	}
	r.mu.Unlock()
	if !due || link == nil {
		return
	}
	if _, err := link.Send(context.Background(), protocol.Outgoing{Type: protocol.TypeReplicaAck, Payload: protocol.ReplicaAck{LSN: lsn}}); err != nil {
		if !errors.Is(err, protocol.ErrNotConnected) {
			r.cfg.Logger.Warn("replication: replica.ack", "err", err)
		}
		r.mu.Lock()
		r.lastAcked = min(r.lastAcked, lsn-1) // try again at the next tick
		r.mu.Unlock()
	}
}

// ---- full copy ----

func (r *Replica) finalizeSnapshot(ctx context.Context, b protocol.XferBegin, file string) error {
	var meta snapshotMeta
	raw, _ := json.Marshal(b.Meta)
	if err := json.Unmarshal(raw, &meta); err != nil {
		return protocol.NewError(protocol.CodeInvalidPayload, "db-snapshot meta: %v", err)
	}
	// The copy is of this Guild and of the epoch the Citadel says: a copy of
	// another Guild, or of an epoch that is not the one announced (a stale
	// transfer, a confusion of files), is not a replica of anything we follow.
	id, epoch, err := readCopyGuild(ctx, file)
	if err != nil {
		return protocol.NewError(protocol.CodeHashMismatch, "%v", err)
	}
	if r.hasG && id != r.gid.String() {
		return protocol.NewError(protocol.CodeForbidden, "the copy is of the Guild %s, not %s", id, r.gid)
	}
	if epoch != meta.Epoch {
		return protocol.NewError(protocol.CodeHashMismatch, "the copy is of epoch %d, not the %d announced", epoch, meta.Epoch)
	}
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	if err := r.cfg.DB.Replace(ctx, file, meta.LSN); err != nil {
		var ns *store.NewerSchemaError
		if errors.As(err, &ns) || errors.Is(err, store.ErrClosed) {
			return protocol.NewError(protocol.CodeUnavailable, "%v", err)
		}
		return protocol.NewError(protocol.CodeHashMismatch, "%v", err)
	}
	r.mu.Lock()
	excluded := r.excluded
	r.mu.Unlock()
	if excluded {
		// The copy was made before the Citadel knew this machine is excluded
		// (or the order overtook it): it carries the vault tables, and the
		// order to erase them was given already.
		if err := r.purgeVault(ctx); err != nil {
			return protocol.NewError(protocol.CodeUnavailable, "erasing the vault tables of the copy: %v", err)
		}
	}
	r.mu.Lock()
	r.queue, r.queueBytes = nil, 0
	r.lastQueued, r.applied, r.lastAcked = meta.LSN, meta.LSN, 0
	r.needCopy, r.resyncAsked = false, false
	r.state, r.lastErr = "ok", ""
	r.mu.Unlock()
	r.sendAck(true)
	return nil
}

// purgeVault empties the vault tables of the replica and scrubs the log of
// what they held, trying again a few times if a reader keeps the WAL.
func (r *Replica) purgeVault(ctx context.Context) error {
	var err error
	for range purgeTries {
		if err = r.cfg.DB.Purge(ctx, VaultTables...); !errors.Is(err, store.ErrPurgeIncomplete) {
			return err
		}
	}
	// Remembered on disk: the erasing is retried (the next vault.drop, or the
	// retry of the replica) and the receipt waits for it.
	r.setPurgeOwed(true)
	return err
}

// purgeTries is how many times Purge is repeated when it reports a WAL it could
// not truncate (each already tries more than once).
const purgeTries = 3

// ---- the vault ----

func (r *Replica) handleVaultReplica(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
	if r.cfg.Vault == nil {
		return nil, protocol.NewError(protocol.CodeUnavailable, "this machine takes no copy of the vault")
	}
	p := m.Payload.(*protocol.VaultReplica)
	ring, err := base64.StdEncoding.DecodeString(p.Keyring.Reveal())
	if err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "keyring: not base64")
	}
	defer clear(ring)
	held := r.cfg.Vault.Holds(ctx)
	if err := r.cfg.Vault.Import(ctx, vault.Copy{Keyring: ring, Envelope: p.Envelope}); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
	}
	r.mu.Lock()
	r.excluded = false
	have := r.applied
	r.mu.Unlock()
	if err := os.Remove(r.droppedMarker()); err != nil && !errors.Is(err, os.ErrNotExist) {
		r.cfg.Logger.Warn("replication: forgetting the order to erase the vault", "err", err)
	}
	if !held && have > 0 {
		// The machine had no vault and has a replica: the exclusion was undone,
		// and the replica lacks the vault tables. A full copy has them.
		r.askFrom(true)
	}
	return nil, nil
}

func (r *Replica) handleVaultDrop(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
	// Remembered on disk before anything else, so a restart in the middle of the
	// erasing, or before the next connection, still filters what comes. An
	// erasing that was left incomplete stays so until it is done.
	content := markerDrop
	if r.purgeOwed() {
		content += markerIncomplete
	}
	if err := os.WriteFile(r.droppedMarker(), []byte(content), 0o600); err != nil {
		r.cfg.Logger.Error("replication: remembering the order to erase the vault", "err", err)
	}
	r.mu.Lock()
	r.excluded = true
	r.mu.Unlock()
	// The full copies that were arriving go now, in the order of the link: the
	// messages of a link run one at a time, so a copy whose xfer.begin comes
	// after this order is not one of them. The Citadel sends the order at every
	// connection of an excluded machine, just before the full copy it makes
	// without the vault tables, and erasing later, in the background, would
	// sometimes remove that copy while it arrives.
	pending := r.dropPendingCopies()
	r.background(func(ctx context.Context) { r.eraseAndReceipt(ctx, m.Env.ID, pending) })
	return nil, nil
}

// background runs f in a goroutine that Run waits for when it ends.
func (r *Replica) background(f func(ctx context.Context)) {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.bg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.bg.Done()
		f(r.bgCtx)
	}()
}

// waitBackground cancels the erasings in the background and waits for them.
func (r *Replica) waitBackground() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	r.bgStop()
	r.bg.Wait()
}

// eraseAndReceipt erases the vault and, when there was something to erase and
// it is done, sends the receipt vault.dropped. A purge rewrites the file: the
// receipt comes later. A machine that had nothing to erase (the order comes
// again at every connection of an excluded machine) gives no receipt: there
// is nothing to put in the audit. An erasing that failed on the WAL still held
// by a reader leaves the state on disk: the next order, or the retry of the
// replica, erases again and sends the receipt then. pending says that the
// order already removed full copies that were arriving (dropPendingCopies):
// that is something erased, so it counts for the receipt.
func (r *Replica) eraseAndReceipt(ctx context.Context, replyTo string, pending bool) {
	r.applyMu.Lock()
	erased, err := r.dropVault(ctx)
	r.applyMu.Unlock()
	if err != nil {
		r.cfg.Logger.Error("replication: erasing the vault", "err", err)
		if pending {
			r.setPurgeOwed(true) // the copies are gone, the receipt is owed
		}
		return
	}
	erased = erased || pending
	if !erased {
		return
	}
	r.mu.Lock()
	link := r.link
	r.mu.Unlock()
	if link == nil {
		return // the state stays owed: the receipt goes with the next try
	}
	if _, err := link.Send(ctx, protocol.Outgoing{Type: protocol.TypeVaultDropped, Payload: protocol.Empty{}, ReplyTo: replyTo}); err != nil {
		r.cfg.Logger.Warn("replication: vault.dropped", "err", err)
		return
	}
	r.setPurgeOwed(false)
}

// retryErasing erases again, in the background, an erasing that was left
// incomplete.
func (r *Replica) retryErasing() {
	if !r.purgeOwed() || !r.erasing.CompareAndSwap(false, true) {
		return
	}
	r.background(func(ctx context.Context) {
		defer r.erasing.Store(false)
		r.eraseAndReceipt(ctx, "", false)
	})
}

// dropVault erases the key, the envelope, the vault tables, the log of what
// they held and the folders that are not for an excluded machine. It says
// whether there was anything to erase. The full copies still arriving are
// removed by handleVaultDrop, not here, which runs later and in the background.
func (r *Replica) dropVault(ctx context.Context) (bool, error) {
	held := r.cfg.Vault != nil && r.cfg.Vault.Holds(ctx)
	if r.cfg.Vault != nil {
		if err := r.cfg.Vault.Drop(ctx); err != nil {
			return false, err
		}
	}
	erased := held || r.purgeOwed()
	if r.dropRestrictedFolders() {
		erased = true
	}
	if !erased {
		var rows int
		for _, t := range VaultTables {
			var n int
			if err := r.cfg.DB.Reader().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, t).Scan(&n); err != nil {
				return false, err
			}
			if n == 0 {
				continue
			}
			if err := r.cfg.DB.Reader().QueryRowContext(ctx, `SELECT count(*) FROM `+t).Scan(&n); err != nil {
				return false, err
			}
			rows += n
		}
		if rows == 0 {
			return false, nil
		}
	}
	return true, r.purgeVault(ctx)
}

// dropPendingCopies removes the full copies still arriving: they hold the
// vault tables, and a copy that was made before the exclusion would put them
// back. It says whether there was one.
func (r *Replica) dropPendingCopies() bool {
	if r.cfg.TransferDir == "" {
		return false
	}
	entries, err := os.ReadDir(r.cfg.TransferDir)
	if err != nil {
		return false
	}
	found := false
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(r.cfg.TransferDir, e.Name()))
		if err != nil {
			continue
		}
		var b protocol.XferBegin
		if json.Unmarshal(raw, &b) != nil || b.Kind != protocol.KindDBSnapshot {
			continue
		}
		found = true
		for _, f := range []string{id + ".part", e.Name()} {
			if err := os.Remove(filepath.Join(r.cfg.TransferDir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
				r.cfg.Logger.Warn("replication: removing a full copy that was arriving", "file", f, "err", err)
			}
		}
	}
	return found
}

// dropRestrictedFolders empties the folders that an excluded machine does not
// get (the captures of the Operator): what the machine had before the
// exclusion goes with the vault. It says whether it removed anything.
func (r *Replica) dropRestrictedFolders() bool {
	if r.cfg.Files == nil {
		return false
	}
	removed := false
	for _, name := range r.cfg.Files.order {
		root := r.cfg.Files.roots[name]
		if !root.NotForExcluded {
			continue
		}
		entries, err := os.ReadDir(root.Dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(root.Dir, e.Name())); err != nil {
				r.cfg.Logger.Warn("replication: removing a folder that an excluded machine does not get", "folder", name, "err", err)
				continue
			}
			removed = true
		}
	}
	return removed
}

// ---- leaving the Guild ----

// Wipe erases this machine's share of the Guild when it leaves
// (guild.Wiper): the copy of the vault, the replica and the replicated
// files. The Replica is no good afterwards.
func (r *Replica) Wipe(ctx context.Context, _ string) error {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	var errs []error
	if r.cfg.Vault != nil {
		errs = append(errs, r.cfg.Vault.Drop(ctx))
	}
	path := r.cfg.DB.Path()
	errs = append(errs, r.cfg.DB.Close())
	for _, suffix := range []string{"", "-wal", "-shm", "-journal", ".incoming", ".excluded"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if r.cfg.Files != nil {
		for _, name := range r.cfg.Files.order {
			dir := r.cfg.Files.roots[name].Dir
			entries, err := os.ReadDir(dir)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					errs = append(errs, err)
				}
				continue
			}
			for _, e := range entries {
				errs = append(errs, os.RemoveAll(filepath.Join(dir, e.Name())))
			}
		}
	}
	if r.cfg.Mirrors != nil {
		entries, err := os.ReadDir(r.cfg.Mirrors.Root)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
		for _, e := range entries {
			errs = append(errs, os.RemoveAll(filepath.Join(r.cfg.Mirrors.Root, e.Name())))
		}
	}
	return errors.Join(errs...)
}
