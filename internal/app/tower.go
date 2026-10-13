package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/discovery"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// DiscardedRetention is how long the database an old Citadel discarded is
// kept (docs/guilda/replicacao.md#perda-na-troca).
const DiscardedRetention = 30 * 24 * time.Hour

// towerRun is one period of this machine as a Tower.
type towerRun struct {
	db      *store.DB
	client  *protocol.Client
	replica *replication.Replica
	locator *discovery.Locator
}

func (e *Engine) sheetBuilder() (*machinesheet.Builder, error) {
	src := e.deps.Sheet
	if src.System == nil {
		src.System = machinesheet.NewSystemProbe(e.paths.Maquina)
	}
	return machinesheet.NewBuilder(src, e.id.MachineID, e.cfg.Instance)
}

func (e *Engine) vaultCopy() *vault.Copier {
	return &vault.Copier{Keys: vault.NewFileKeyStore(e.paths.vaultKey()), EnvelopePath: e.paths.envelope()}
}

// runTower serves as a Tower until the role changes or ctx ends: the replica,
// the connection to the Citadel with its outbox, the machine sheet, and the
// answer 4007 to whoever takes this machine for the Citadel.
func (e *Engine) runTower(ctx context.Context, unknown bool) (next transition) {
	db, err := guilddb.Open(ctx, e.paths.guildDB(), store.Options{Clock: e.clk, Synchronous: e.cfg.Synchronous, Replica: true})
	if err != nil {
		return transition{err: err}
	}
	pctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		e.setGuildDB(nil)
		e.mu.Lock()
		e.tow, e.connected = nil, false
		e.mu.Unlock()
		cancel()
		wg.Wait()
		db.Close()
		e.changed()
	}()
	if err := e.creds.Reload(ctx, db); err != nil {
		e.log.Debug("app: no credentials in the replica yet", "err", err)
	}
	e.pruneDiscarded()

	replica, err := replication.NewReplica(ctx, replication.ReplicaConfig{DB: db, Clock: e.clk, GuildID: e.id.GuildID, Vault: e.vaultCopy(), Logger: e.log})
	if err != nil {
		return transition{err: err}
	}
	builder, err := e.sheetBuilder()
	if err != nil {
		return transition{err: err}
	}
	agent, err := machinesheet.NewAgent(machinesheet.AgentConfig{Builder: builder, Clock: e.clk, Logger: e.log})
	if err != nil {
		return transition{err: err}
	}
	locator, err := discovery.NewLocator(discovery.LocatorConfig{
		Clock: e.clk, Logger: e.log, Configured: e.cfg.Addresses, Disabled: e.cfg.NoDiscovery,
		Cache: discovery.NewSQLCache(e.mdb), Known: e.known, Verifier: discovery.TLSVerifier{Prober: e.tcli},
		Search:    discovery.SearchConfig{Net: e.deps.Packets, Port: e.cfg.DiscoveryPort, GuildID: e.id.GuildID},
		AskOthers: true,
	})
	if err != nil {
		return transition{err: err}
	}
	router := protocol.NewRouter()
	replica.Register(router)
	guild.Register(router, guild.Wiring{Tower: &guild.TowerLeave{Member: e.member, Epochs: e.epochs, Wiper: replica}})
	router.HandleFunc(protocol.TypeCLIResult, e.results.handle)
	router.HandleFunc(protocol.TypeCitadelHandover, e.handleHandover)
	msql := protocol.NewSQLStore(e.mdb)
	client, err := protocol.NewClient(protocol.Config{
		MachineID: e.id.MachineID, Clock: e.clk, Router: router, Epoch: e.epochs, Certs: e.epochs, Outbox: msql, Processed: msql,
		BinaryVersion: e.deps.Version, Logger: e.log,
		Hello: func(ctx context.Context) (protocol.HelloInfo, error) {
			sheet, err := agent.Hello(ctx)
			info := protocol.HelloInfo{Version: e.deps.Version, Sheet: sheet}
			replica.Hello(ctx, &info)
			return info, err
		},
		Heartbeat: func(context.Context) (protocol.MachineHeartbeat, error) {
			return protocol.MachineHeartbeat{ReplicaLagS: replica.LagSeconds()}, nil
		},
		Xfer: &protocol.XferConfig{Dir: e.paths.transfers(), Finalizers: replica.Finalizers()},
		Hooks: protocol.Hooks{
			OnConnect: func(*protocol.Session) {
				locator.Connected(pctx)
				e.setConnected(true)
				e.down.Back()
			},
			OnDisconnect: func(*protocol.Session, error) {
				e.setConnected(false)
				e.down.Lost()
			},
			Rediscover: locator.Rediscover,
		},
	}, protocol.TransportDialer(e.tcli, locator.Target))
	if err != nil {
		return transition{err: err}
	}
	replica.Attach(client.Link())
	agent.Attach(client.Link())
	// Whoever takes this machine for the Citadel is told 4007, with the
	// certificate this machine follows.
	refuser, err := protocol.NewServer(protocol.Config{
		MachineID: e.id.MachineID, Clock: e.clk, Epoch: e.epochs, Certs: e.epochs, Outbox: msql, Processed: msql,
		IsCitadel: func() bool { return false }, BinaryVersion: e.deps.Version, Logger: e.log,
	})
	if err != nil {
		return transition{err: err}
	}
	run := &towerRun{db: db, client: client, replica: replica, locator: locator}
	e.setGuildDB(db)
	e.mu.Lock()
	e.role, e.tow, e.cit, e.unknown = RoleTower, run, nil, unknown
	e.mu.Unlock()
	e.down.Lost()
	wg.Go(func() { _ = refuser.Serve(pctx, e.towers) })
	wg.Go(func() {
		if _, err := locator.Run(pctx); err != nil {
			return
		}
		_ = client.Run(pctx)
	})
	wg.Go(func() { _ = replica.Run(pctx) })
	wg.Go(func() { _ = agent.Run(pctx) })
	wg.Go(func() { e.reportDiscarded(pctx, db, client.Link()) })
	e.changed()
	e.log.Info("app: serving as a Tower", "epoch", e.epochs.Epoch(), "citadel", e.known().CitadelMachineID)

	t, ok := e.wait(ctx)
	if !ok {
		return transition{}
	}
	return t
}

// Promote asks the Owner to make this Tower the Citadel and, with the
// approval, takes the command (docs/guilda/cidadela-e-torres.md#promoção): it
// returns once the machine is the Citadel of the new epoch, waiting before
// any external action, or why it is not. The request comes from the
// conducting Tower's Guardian (citadel.promote, milestone 7).
func (e *Engine) Promote(ctx context.Context, reason string) error {
	e.mu.Lock()
	run, failover := e.tow, e.failover
	e.mu.Unlock()
	switch {
	case run == nil:
		return fmt.Errorf("%w: only a Tower is promoted", guild.ErrNotPromotable)
	case failover:
		return fmt.Errorf("%w: a promotion is already under way", guild.ErrNotPromotable)
	case e.deps.Lock == nil:
		return guild.ErrNoLock
	}
	var lsn uint64
	if err := e.withGuildDB(func(db *store.DB) error {
		if err := e.promotable(ctx, db); err != nil {
			return err
		}
		lsn, _ = db.LastLSN(ctx)
		return nil
	}); err != nil {
		return err
	}
	approvalID, by, err := e.promoter.Ask(ctx, guild.PromotionRequest{
		MachineID: e.id.MachineID, Name: e.cfg.Name, Epoch: e.epochs.Epoch(), ReplicaLSN: lsn, Reason: reason, CreatedAt: e.clk.Now(),
	})
	if err != nil {
		return err
	}
	return e.startPromotion(ctx, approvalID, by, 0)
}

// DecidePromotion is the Owner's answer to the request of Promote, given by
// the bot or the fallback page after the PIN.
func (e *Engine) DecidePromotion(approvalID string, d guild.Decision) error {
	return e.promoter.Decide(approvalID, d)
}

func (e *Engine) startPromotion(ctx context.Context, approvalID, by string, wantLSN uint64) error {
	done := make(chan error, 1)
	e.setFailover(true)
	e.signal(transition{promote: &promotion{approvalID: approvalID, approvedBy: by, wantLSN: wantLSN, done: done}})
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// promotable says whether this machine may take the place of the Citadel:
// active, not excluded, with the copy of the vault.
func (e *Engine) promotable(ctx context.Context, db *store.DB) error {
	var (
		state    string
		excluded bool
	)
	err := db.Reader().QueryRowContext(ctx, `SELECT state, excluded FROM machines WHERE machine_id = ?`, e.id.MachineID).Scan(&state, &excluded)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: the replica does not know this machine yet", guild.ErrNotPromotable)
	case err != nil:
		return fmt.Errorf("%w: %v", guild.ErrNotPromotable, err)
	case state != string(guild.MachineActive):
		return fmt.Errorf("%w: the machine is %s", guild.ErrNotPromotable, state)
	case excluded:
		return fmt.Errorf("%w: %w", guild.ErrNotPromotable, guild.ErrExcludedMachine)
	case !e.vaultCopy().Holds(ctx):
		return fmt.Errorf("%w: the machine has no copy of the vault", guild.ErrNotPromotable)
	}
	return nil
}

// promote takes the command, once the Tower period stopped applying the
// replication: it opens the replica as the main database with the vault and
// the CA, wins the next epoch in the lock, signs its certificate and becomes
// the Citadel, which waits before acting outside. Losing the lock, or
// anything before it, leaves the machine a Tower and warns the Owner.
func (e *Engine) promote(ctx context.Context, p *promotion) transition {
	fail := func(err error) transition {
		e.log.Error("app: the promotion did not happen", "err", err)
		e.setFailover(false)
		p.done <- err
		if e.deps.OnPromotionFailed != nil {
			e.deps.OnPromotionFailed(err)
		}
		return transition{to: RoleTower}
	}
	take, err := e.openMain(ctx)
	if err != nil {
		return fail(err)
	}
	release := func() { take.vault.Close(); take.db.Close() }
	from := take.lastLSN
	if from < p.wantLSN {
		release()
		return fail(fmt.Errorf("app: the replica has the lsn %d, the handover %d", from, p.wantLSN))
	}
	n, err := guild.ClaimEpoch(ctx, e.deps.Lock, e.epochs.Epoch(), e.id.MachineID)
	if err != nil {
		release()
		return fail(err)
	}
	cert, err := guild.TakeCommand(ctx, take.db, take.ca, e.clk, guild.Command{
		Epoch: n, MachineID: e.id.MachineID, ApprovalID: p.approvalID, ApprovedBy: p.approvedBy, FromLSN: from,
	})
	if err != nil {
		release()
		return fail(err)
	}
	if err := e.epochs.Follow(ctx, cert); err != nil {
		release()
		return fail(err)
	}
	e.log.Warn("app: this machine took the command", "epoch", n, "from_lsn", from)
	p.done <- nil
	take.promoted = true
	return transition{to: RoleCitadel, take: take}
}

// handleHandover is citadel.handover on the machine the Citadel chose: when
// the replica has the last lsn, it promotes itself through the lock.
func (e *Engine) handleHandover(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
	h, ok := m.Payload.(*protocol.CitadelHandover)
	if !ok {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "citadel.handover payload is %T", m.Payload)
	}
	if m.Peer.ID != e.epochs.Citadel() || h.TargetMachineID != e.id.MachineID {
		return nil, protocol.NewError(protocol.CodeForbidden, "only the Citadel hands over, and to the machine it names")
	}
	if e.deps.Lock == nil {
		return nil, protocol.NewError(protocol.CodeForbidden, "%v", guild.ErrNoLock)
	}
	e.mu.Lock()
	run := e.tow
	e.mu.Unlock()
	if run == nil {
		return nil, protocol.NewError(protocol.CodeUnavailable, "this machine is changing its role")
	}
	if err := e.withGuildDB(func(db *store.DB) error { return e.promotable(ctx, db) }); err != nil {
		return nil, protocol.NewError(protocol.CodeForbidden, "%v", err)
	}
	go func() {
		bg := context.WithoutCancel(ctx)
		wctx, cancel := clock.WithTimeout(bg, e.clk, time.Minute)
		defer cancel()
		tick := e.clk.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			var lsn uint64
			if err := e.withGuildDB(func(db *store.DB) (err error) { lsn, err = db.LastLSN(wctx); return err }); err == nil && lsn >= h.LastLSN {
				break
			}
			select {
			case <-tick.C():
			case <-wctx.Done():
				e.log.Error("app: the handover lsn did not arrive", "lsn", h.LastLSN)
				return
			}
		}
		if err := e.startPromotion(bg, h.ApprovalID, "", h.LastLSN); err != nil {
			e.log.Error("app: the handover failed", "err", err)
		}
	}()
	return nil, nil
}

// discardReport is what descartado-<época>.json keeps: the report was sent.
type discardReport struct {
	Epoch   uint64   `json:"epoch"`
	FromLSN uint64   `json:"from_lsn"`
	Total   int      `json:"total"`
	LSNs    []uint64 `json:"lsns"`
}

// discardedFiles lists the databases this machine discarded, by epoch.
func (e *Engine) discardedFiles() map[uint64]string {
	out := map[uint64]string{}
	matches, _ := filepath.Glob(filepath.Join(e.paths.Banco, "descartado-*.db"))
	for _, m := range matches {
		s := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), "descartado-"), ".db")
		if n, err := strconv.ParseUint(s, 10, 64); err == nil {
			out[n] = m
		}
	}
	return out
}

// reportDiscarded lists, for every database this machine discarded as an old
// Citadel and did not report yet, the transactions the new Citadel never
// received, and sends them to it for the audit (guild.discarded, through the
// outbox). It waits for the replica to know the epoch that replaced the old
// one, which says from which lsn the old transactions were lost.
func (e *Engine) reportDiscarded(ctx context.Context, db *store.DB, link *protocol.Link) {
	files := e.discardedFiles()
	total := 0
	for epoch, path := range files {
		var r discardReport
		if b, err := os.ReadFile(strings.TrimSuffix(path, ".db") + ".json"); err == nil && json.Unmarshal(b, &r) == nil {
			total += r.Total
			delete(files, epoch)
		}
	}
	e.mu.Lock()
	e.discarded = total
	e.mu.Unlock()
	if len(files) == 0 {
		return
	}
	tick := e.clk.NewTicker(time.Second)
	defer tick.Stop()
	for len(files) > 0 {
		for epoch, path := range files {
			if e.epochs.Epoch() <= epoch {
				continue // the certificate of the epoch that replaced it is not known yet
			}
			rec, err := guild.EpochStart(ctx, db, epoch+1)
			if err != nil {
				continue // the replica does not have it yet
			}
			r, err := e.listDiscarded(ctx, path, epoch, rec.FromLSN, link)
			if err != nil {
				e.log.Error("app: listing the discarded transactions", "file", path, "err", err)
				continue
			}
			delete(files, epoch)
			e.mu.Lock()
			e.discarded += r.Total
			e.mu.Unlock()
			e.changed()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C():
		}
	}
}

func (e *Engine) listDiscarded(ctx context.Context, path string, epoch, from uint64, link *protocol.Link) (discardReport, error) {
	old, err := guilddb.Open(ctx, path, store.Options{Clock: e.clk, Replica: true})
	if err != nil {
		return discardReport{}, err
	}
	total, txs, err := guild.ListDiscarded(ctx, old, from, guild.MaxDiscardedListed)
	old.Close()
	if err != nil {
		return discardReport{}, err
	}
	r := discardReport{Epoch: epoch, FromLSN: from, Total: total}
	for _, t := range txs {
		r.LSNs = append(r.LSNs, t.LSN)
	}
	if total > 0 {
		if _, err := link.Send(ctx, protocol.Outgoing{Type: protocol.TypeGuildDiscarded, Payload: protocol.GuildDiscarded{
			Epoch: epoch, FromLSN: from, Total: total, Transactions: txs,
		}}); err != nil {
			return discardReport{}, err
		}
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	if err := os.WriteFile(strings.TrimSuffix(path, ".db")+".json", append(b, '\n'), 0o600); err != nil {
		return discardReport{}, err
	}
	e.log.Warn("app: the transactions lost in the change of Citadel were reported", "epoch", epoch, "from_lsn", from, "total", total)
	return r, nil
}

// pruneDiscarded removes the discarded databases older than the retention.
func (e *Engine) pruneDiscarded() {
	cutoff := e.clk.Now().Add(-DiscardedRetention)
	for _, path := range e.discardedFiles() {
		fi, err := os.Stat(path)
		if err != nil || !fi.ModTime().Before(cutoff) {
			continue
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(path + suffix)
		}
		e.log.Info("app: a discarded database left after the retention", "file", path)
	}
}

// osName is the system for the entry request.
func osName() string { return runtime.GOOS }
