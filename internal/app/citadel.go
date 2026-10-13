package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// lazyPresence and lazyNames reach the server and the Citadel of the period,
// which are built after the registry that needs them.
type lazyPresence struct{ srv **protocol.Server }

func (p lazyPresence) Connected(id string) bool {
	return *p.srv != nil && (*p.srv).Link(id).Connected()
}

type lazyNames struct{ c **guild.Citadel }

func (n lazyNames) Machine(ctx context.Context, id string) (guild.Machine, error) {
	return (*n.c).Machine(ctx, id)
}

func (n lazyNames) UpdateNames(ctx context.Context, id, hostname string, ips []net.IP) error {
	return (*n.c).UpdateNames(ctx, id, hostname, ips)
}

// actingSender sends the sheet of the Citadel itself only while the Citadel
// acts (the fence allows it), like every other write of the Guild.
type actingSender struct {
	fence *guild.Fence
	to    machinesheet.Sender
}

func (a actingSender) Send(ctx context.Context, o protocol.Outgoing) (string, error) {
	if !a.fence.Acting() {
		return "", errors.New("app: the Citadel is not acting")
	}
	return a.to.Send(ctx, o)
}

// takeover is what a promotion already opened for the Citadel it becomes.
type takeover struct {
	db *store.DB
	// lastLSN is the last lsn of the database before the vault and the CA
	// were opened (opening them writes: the use of the key of the CA).
	lastLSN  uint64
	vault    *vault.Vault
	ca       *transport.CA
	promoted bool
}

// citadelRun is one period of this machine as the Citadel.
type citadelRun struct {
	db      *store.DB
	epoch   uint64
	fence   *guild.Fence
	srv     *protocol.Server
	primary *replication.Primary
	citadel *guild.Citadel

	base      context.Context
	external  []ExternalFunc
	firstAct  func()
	once      sync.Once
	extMu     sync.Mutex
	extCancel context.CancelFunc
	extWG     sync.WaitGroup
}

// setActing turns the external functions on or off.
func (r *citadelRun) setActing(on bool) {
	r.extMu.Lock()
	defer r.extMu.Unlock()
	switch {
	case on && r.extCancel == nil:
		ctx, cancel := context.WithCancel(r.base)
		r.extCancel = cancel
		if r.firstAct != nil {
			r.once.Do(func() { r.extWG.Go(r.firstAct) })
		}
		for _, fn := range r.external {
			r.extWG.Go(func() { _ = fn(ctx, r.fence) })
		}
	case !on && r.extCancel != nil:
		r.extCancel()
		r.extCancel = nil
	}
}

// openMain opens the Guild database as the main one, the vault and the CA.
func (e *Engine) openMain(ctx context.Context) (*takeover, error) {
	db, err := guilddb.Open(ctx, e.paths.guildDB(), store.Options{Clock: e.clk, Synchronous: e.cfg.Synchronous})
	if err != nil {
		return nil, err
	}
	last, err := db.LastLSN(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	var recovery []byte
	if err := db.Reader().QueryRowContext(ctx, `SELECT recovery_public_key FROM guild WHERE id = 1`).Scan(&recovery); err != nil {
		db.Close()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("app: the Guild database holds no Guild")
		}
		return nil, fmt.Errorf("app: read the Guild: %w", err)
	}
	v, err := vault.Open(ctx, vault.Config{
		DB: db, Keys: vault.NewFileKeyStore(e.paths.vaultKey()), EnvelopePath: e.paths.envelope(),
		RecoveryPublicKey: recovery, Masker: logging.NewMasker(), Clock: e.clk,
	})
	if err == nil {
		err = v.Err()
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("app: the vault does not open: %w", err)
	}
	ca, err := guild.LoadCA(ctx, db, v, "citadel", e.clk)
	if err != nil {
		v.Close()
		db.Close()
		return nil, err
	}
	return &takeover{db: db, lastLSN: last, vault: v, ca: ca}, nil
}

// runCitadel serves as the Citadel until the role changes or ctx ends: the
// main database, the vault, the entries, the Towers, the replication and,
// while the fence allows, the external functions.
func (e *Engine) runCitadel(ctx context.Context, take *takeover) (next transition) {
	if take == nil {
		var err error
		if take, err = e.openMain(ctx); err != nil {
			return transition{err: err}
		}
	}
	db := take.db
	pctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	run := &citadelRun{db: db, epoch: e.epochs.Epoch(), base: pctx, external: e.deps.External}
	defer func() {
		e.joins.clear()
		run.setActing(false)
		e.setGuildDB(nil)
		e.mu.Lock()
		e.cit = nil
		if next.to == RoleTower {
			e.role, e.unknown = RoleTower, next.unknown
		}
		e.mu.Unlock()
		if next.to == RoleTower {
			run.fence.Demote()
			if run.srv != nil {
				run.srv.CloseAll(protocol.CloseNotCitadel, "this machine is not the Citadel")
			}
		} else if run.srv != nil {
			run.srv.Shutdown()
		}
		cancel()
		wg.Wait()
		run.extWG.Wait()
		take.vault.Close()
		if next.to == RoleTower {
			if err := e.archive(db, run.epoch); err != nil {
				e.log.Error("app: keeping the discarded database", "err", err)
			}
		} else {
			db.Close()
		}
		e.changed()
	}()

	run.fence = guild.NewFence(guild.FenceConfig{
		Lock: e.deps.Lock, Clock: e.clk, Epoch: func() uint64 { return run.epoch }, Promoted: take.promoted, Logger: e.log,
		OnHigher: func(uint64) { e.signal(transition{to: RoleTower, unknown: true}) },
		OnChange: func(s guild.FenceState) {
			run.setActing(s == guild.FenceConfirmed || s == guild.FenceLockDown || s == guild.FenceNoLock)
			e.changed()
		},
		OnDiscard: func(names []string) { e.log.Warn("app: external actions discarded at the demotion", "actions", names) },
	})
	if err := e.creds.Reload(ctx, db); err != nil {
		e.log.Warn("app: reading the revoked credentials", "err", err)
	}
	router := protocol.NewRouter()
	sqls := protocol.NewSQLStore(db)
	pcfg := protocol.Config{
		MachineID: e.id.MachineID, Clock: e.clk, Router: router, Epoch: e.epochs, Certs: e.epochs, Outbox: sqls, Processed: sqls,
		BinaryVersion: e.deps.Version, Logger: e.log,
		IsCitadel: func() bool {
			e.mu.Lock()
			current := e.cit == run && e.role == RoleCitadel
			e.mu.Unlock()
			return current && run.fence.State() != guild.FenceDemoted && e.epochs.Citadel() == e.id.MachineID
		},
	}
	var (
		citadel *guild.Citadel
		srv     *protocol.Server
	)
	primary, err := replication.NewPrimary(ctx, replication.PrimaryConfig{
		DB: db, Clock: e.clk, Dir: e.paths.snapshots(), Logger: e.log,
		Roster: replication.RosterFunc(func(ctx context.Context, id string) (bool, error) {
			m, err := citadel.Machine(ctx, id)
			return m.Excluded, err
		}),
		Vault:       &vault.Copier{Keys: vault.NewFileKeyStore(e.paths.vaultKey()), EnvelopePath: e.paths.envelope()},
		GuildConfig: func(ctx context.Context) (protocol.GuildConfig, error) { return citadel.Config(ctx) },
	})
	if err != nil {
		return transition{err: err}
	}
	registry, err := machinesheet.NewRegistry(machinesheet.RegistryConfig{
		DB: db, Presence: lazyPresence{&srv}, Self: func() string { return e.id.MachineID }, Names: lazyNames{&citadel}, Logger: e.log,
	})
	if err != nil {
		return transition{err: err}
	}
	pcfg.Hooks = primary.Hooks(protocol.Hooks{OnHello: registry.OnHello})
	if srv, err = protocol.NewServer(pcfg); err != nil {
		return transition{err: err}
	}
	run.srv, run.primary = srv, primary
	primary.Attach(srv)
	if citadel, err = guild.NewCitadel(ctx, guild.CitadelConfig{
		DB: db, CA: take.ca, Clock: e.clk, Cert: e.cert, Approver: e.deps.Approver, Credentials: e.creds,
		Peers:     guild.ProtocolPeers{Server: srv, Transport: e.tls, Clock: e.clk},
		Hooks:     guild.Hooks{ExclusionChanged: primary.ExclusionChanged},
		Suspended: func() bool { return !run.fence.Acting() },
		Logger:    e.log,
	}); err != nil {
		return transition{err: err}
	}
	run.citadel = citadel
	guild.Register(router, guild.Wiring{Citadel: func() *guild.Citadel { return citadel }})
	primary.Register(router)
	registry.Register(router)
	router.HandleFunc(protocol.TypeCLICall, e.handleCLICall)
	router.HandleFunc(protocol.TypeGuildDiscarded, guild.DiscardedHandler(func() *store.DB { return db }, e.clk))

	e.setGuildDB(db)
	e.mu.Lock()
	e.role, e.cit, e.tow, e.unknown, e.failover = RoleCitadel, run, nil, false, false
	e.mu.Unlock()
	citadel.Mount(e.joins)
	// The Citadel's own sheet is written once it acts, and kept up to date every
	// five minutes like the others (an agent whose link is the registry itself):
	// a Citadel that only starts, suspended, and steps down writes nothing of
	// the Guild.
	run.firstAct = func() {
		builder, err := e.sheetBuilder()
		if err != nil {
			return
		}
		agent, err := machinesheet.NewAgent(machinesheet.AgentConfig{Builder: builder, Clock: e.clk, Store: e.sheetStore(), Logger: e.log})
		if err != nil {
			return
		}
		agent.Attach(actingSender{fence: run.fence, to: registry.Local(e.id.MachineID)})
		if sheet, err := agent.Hello(pctx); err == nil {
			_ = registry.Replace(pctx, e.id.MachineID, sheet)
		}
		_ = agent.Run(pctx)
	}
	wg.Go(func() { _ = srv.Serve(pctx, e.towers) })
	wg.Go(func() { _ = primary.Run(pctx) })
	wg.Go(func() { _ = run.fence.Run(pctx) })
	run.setActing(run.fence.Acting())
	e.changed()
	e.log.Info("app: serving as the Citadel", "epoch", run.epoch, "fence", run.fence.State())

	t, ok := e.wait(ctx)
	if !ok {
		return transition{}
	}
	return t
}

// archive keeps the database the old Citadel discards as
// descartado-<época>.db (docs/guilda/replicacao.md#perda-na-troca): the
// replica starts empty and receives the full copy of the new Citadel.
func (e *Engine) archive(db *store.DB, epoch uint64) error {
	path := db.Path()
	if err := db.Close(); err != nil {
		return err
	}
	dst := e.paths.discarded(epoch)
	if _, err := os.Stat(dst); err == nil {
		dst = fmt.Sprintf("%s.%d", dst, e.clk.Now().UnixMilli())
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Rename(path+suffix, dst+suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	e.log.Warn("app: the database of the old epoch is kept", "file", dst, "epoch", epoch)
	return nil
}

// Handover passes the Citadel to the machine target, with the approval of the
// Owner (docs/guilda/cidadela-e-torres.md#troca-planejada): the Citadel stops
// acting outside, waits for the target to confirm the last lsn, and tells it
// to promote itself; it steps down when the lock shows the new epoch.
func (e *Engine) Handover(ctx context.Context, target, approvalID string, wait time.Duration) error {
	e.mu.Lock()
	run := e.cit
	e.mu.Unlock()
	if run == nil {
		return errors.New("app: only the Citadel hands over")
	}
	if target == e.id.MachineID {
		return errors.New("app: the Citadel cannot hand over to itself")
	}
	if wait <= 0 {
		wait = time.Minute
	}
	run.fence.Demote()
	restore := func(err error) error {
		e.signal(transition{to: RoleCitadel})
		return err
	}
	last, err := run.db.LastLSN(ctx)
	if err != nil {
		return restore(err)
	}
	wctx, cancel := clock.WithTimeout(ctx, e.clk, wait)
	defer cancel()
	tick := e.clk.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		st, _ := run.primary.Status(wctx)
		done := false
		for _, s := range st {
			done = done || s.Machine == target && s.Acked >= last
		}
		if done {
			break
		}
		select {
		case <-tick.C():
		case <-wctx.Done():
			return restore(fmt.Errorf("app: %s did not confirm the lsn %d: %w", target, last, wctx.Err()))
		}
	}
	// The target promotes itself through the lock and the old Citadel steps
	// down when the lock shows the new epoch, whether or not the ack made it
	// before the target closed its connection as a Tower.
	refused := make(chan error, 1)
	go func() {
		res, err := run.srv.Link(target).Call(wctx, protocol.Outgoing{
			Type: protocol.TypeCitadelHandover, Payload: protocol.CitadelHandover{TargetMachineID: target, LastLSN: last, ApprovalID: approvalID},
		})
		if err == nil && res.Status == protocol.AckError {
			refused <- fmt.Errorf("app: %s refused citadel.handover: %v", target, res.Error)
		}
	}()
	for {
		if e.deps.Lock != nil {
			if n, err := e.deps.Lock.Check(wctx); err == nil && n > run.epoch {
				e.signal(transition{to: RoleTower, unknown: true})
				return nil
			}
		}
		select {
		case <-tick.C():
		case err := <-refused:
			return restore(err)
		case <-wctx.Done():
			return restore(fmt.Errorf("app: %s did not take the command: %w", target, wctx.Err()))
		}
	}
}
