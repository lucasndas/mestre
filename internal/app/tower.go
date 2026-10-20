package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/discovery"
	"github.com/lucasndas/mestre/internal/gitx"
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

// DiscardedRetention is how long the database an old Citadel discarded is
// kept (docs/guilda/replicacao.md#perda-na-troca).
const DiscardedRetention = 30 * 24 * time.Hour

// towerRun is one period of this machine as a Tower.
type towerRun struct {
	db      *store.DB
	client  *protocol.Client
	replica *replication.Replica
	locator *discovery.Locator
	agent   *machinesheet.Agent
	renewer *guild.Renewer
}

func (e *Engine) sheetBuilder() (*machinesheet.Builder, error) {
	src := e.deps.Sheet
	if src.System == nil {
		src.System = machinesheet.NewSystemProbe(e.paths.Maquina)
	}
	return machinesheet.NewBuilder(src, e.id.MachineID, e.cfg.Instance)
}

// sheetStore keeps the last sheet of this machine across restarts.
func (e *Engine) sheetStore() machinesheet.SheetStore {
	return machinesheet.FileStore(filepath.Join(e.paths.Maquina, "ficha.json"))
}

func (e *Engine) vaultCopy() *vault.Copier {
	return &vault.Copier{Keys: vault.NewFileKeyStore(e.paths.vaultKey()), EnvelopePath: e.paths.envelope()}
}

// replicaConfig is the replication of this Tower over its replica db: the copy
// of the vault, the transfers, and what it erases if the machine leaves the
// Guild, besides the replica (Paths.leftovers).
func (e *Engine) replicaConfig(db *store.DB) (replication.ReplicaConfig, error) {
	files, err := e.paths.replicationFiles()
	if err != nil {
		return replication.ReplicaConfig{}, err
	}
	cfg := replication.ReplicaConfig{
		DB: db, Clock: e.clk, GuildID: e.id.GuildID, Vault: e.vaultCopy(),
		TransferDir: e.paths.transfers(), Leftovers: e.paths.leftovers(), Logger: e.log, Files: files,
	}
	if e.paths.Workspaces != "" {
		// the replica of each mirror lives where the worktrees of the tables of the Tower come from
		masker := e.deps.Masker
		if masker == nil {
			masker = logging.NewMasker()
		}
		git, err := gitx.New(gitx.Options{Clock: e.clk, Masker: masker, Suite: os.Getenv("MESTRE_TEST_SUITE")})
		if err != nil {
			return replication.ReplicaConfig{}, err
		}
		cfg.Mirrors = &replication.MirrorStore{Root: e.paths.Workspaces, Git: git}
	}
	return cfg, nil
}

// towerWiper is what a Tower runs when the Citadel tells it to leave the Guild
// (guild.leave). The report of the databases it discarded stops first: it reads
// those files, and a report that waits for the ack would write its record after
// the Wipe took the files (docs/guilda/replicacao.md#cofre-em-toda-máquina).
type towerWiper struct {
	replica    guild.Wiper
	stopReport func()
}

func (w towerWiper) Wipe(ctx context.Context, reason string) error {
	w.stopReport()
	return w.replica.Wipe(ctx, reason)
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
	if err := e.reloadCreds(ctx, db); err != nil {
		return transition{err: fmt.Errorf("app: the refused credentials of the replica: %w", err)}
	}
	e.pruneDiscarded()

	rcfg, err := e.replicaConfig(db)
	if err != nil {
		return transition{err: err}
	}
	replica, err := replication.NewReplica(ctx, rcfg)
	if err != nil {
		return transition{err: err}
	}
	builder, err := e.sheetBuilder()
	if err != nil {
		return transition{err: err}
	}
	agent, err := machinesheet.NewAgent(machinesheet.AgentConfig{Builder: builder, Clock: e.clk, Store: e.sheetStore(), Logger: e.log})
	if err != nil {
		return transition{err: err}
	}
	locator, err := discovery.NewLocator(discovery.LocatorConfig{
		Clock: e.clk, Logger: e.log, Configured: e.cfg.Addresses, Disabled: e.cfg.NoDiscovery,
		Cache: discovery.NewSQLCache(e.mdb), Known: e.known, Verifier: discovery.TLSVerifier{Prober: e.tcli},
		Search:    discovery.SearchConfig{Net: e.deps.Packets, Browser: e.deps.Browser, Port: e.cfg.DiscoveryPort, GuildID: e.id.GuildID},
		AskOthers: true,
	})
	if err != nil {
		return transition{err: err}
	}
	sys := e.deps.Sheet.System
	if sys == nil {
		sys = machinesheet.NewSystemProbe(e.paths.Maquina)
	}
	router := protocol.NewRouter()
	replica.Register(router)
	router.HandleFunc(protocol.TypeCLIResult, e.results.handle)
	router.HandleFunc(protocol.TypeCitadelHandover, e.handleHandover)
	renewKick := make(chan struct{}, 1)
	kickRenew := func() {
		select {
		case renewKick <- struct{}{}:
		default:
		}
	}
	router.HandleFunc(protocol.TypeGuildConfig, e.handleGuildConfig(db, locator, kickRenew))
	// The effective rules of the characters of this machine: kept in the
	// machine database, with the snapshot for the degraded hook
	// (docs/classes/permissoes.md#regras-efetivas).
	rulesStore := &classes.Store{DB: e.mdb, Clock: e.clk, Agents: e.paths.Agentes}
	router.HandleFunc(protocol.TypeAgentRules, rulesStore.Handler(func(p protocol.Peer) bool {
		return p.Role == protocol.RoleMachine && p.ID == e.epochs.Citadel()
	}))
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
		Heartbeat: func(ctx context.Context) (protocol.MachineHeartbeat, error) {
			return towerHeartbeat(ctx, sys, replica.LagSeconds()), nil
		},
		Xfer: &protocol.XferConfig{Dir: e.paths.transfers(), Finalizers: replica.Finalizers()},
		Hooks: protocol.Hooks{
			OnConnect: func(*protocol.Session) {
				locator.Connected(pctx)
				e.setConnected(true)
				e.down.Back()
				kickRenew()
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
	// The engine certificate renews over the connection to the Citadel, 30
	// days before it expires and when the registry gives it other names
	// (docs/guilda/guilda.md#credenciais).
	renewer, err := guild.NewRenewer(guild.RenewerConfig{
		Kind: transport.KindMachine, CA: e.id.CA, Source: e.machine, Link: client.Link(), Clock: e.clk, Logger: e.log,
		Names: func() transport.Names {
			m, _ := guild.MachineOf(context.Background(), db, e.id.MachineID)
			return m.Names()
		},
		Save: func(ctx context.Context, cert *x509.Certificate, key *ecdsa.PrivateKey) error {
			if err := e.member.SaveRenewal(ctx, cert, key); err != nil {
				return err
			}
			e.cert.Set(cert, key) // a Tower presents its machine certificate
			return nil
		},
	})
	if err != nil {
		return transition{err: err}
	}
	reportCtx, stopReporting := context.WithCancel(pctx)
	defer stopReporting()
	reported := make(chan struct{}) // closed when the report of the discarded databases ends
	wiper := towerWiper{replica: replica, stopReport: func() { stopReporting(); <-reported }}
	guild.Register(router, guild.Wiring{Tower: &guild.TowerLeave{Member: e.member, Epochs: e.epochs, Wiper: wiper, OnLeft: e.stopEngine}, Renewer: renewer})
	// Whoever takes this machine for the Citadel is told 4007, with the
	// certificate this machine follows.
	refuser, err := protocol.NewServer(protocol.Config{
		MachineID: e.id.MachineID, Clock: e.clk, Epoch: e.epochs, Certs: e.epochs, Outbox: msql, Processed: msql,
		IsCitadel: func() bool { return false }, BinaryVersion: e.deps.Version, Logger: e.log,
	})
	if err != nil {
		return transition{err: err}
	}
	run := &towerRun{db: db, client: client, replica: replica, locator: locator, agent: agent, renewer: renewer}
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
	wg.Go(func() { e.renewTower(pctx, db, renewer, client.Link(), renewKick) })
	wg.Go(func() {
		defer close(reported)
		e.reportDiscarded(reportCtx, db, client.Link())
	})
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

// DecideEntry is the Owner's answer to the entry of a machine, given by the
// bot or the panel after the PIN (docs/guilda/guilda.md#entrada-na-guilda).
func (e *Engine) DecideEntry(ctx context.Context, joinID string, d guild.Decision) error {
	e.mu.Lock()
	run := e.cit
	e.mu.Unlock()
	if run == nil {
		return errors.New("app: only the Citadel takes entries")
	}
	c := run.guild()
	if c == nil {
		if why := run.whyDown(); why != "" {
			return fmt.Errorf("app: the Citadel cannot take entries, %s: %w", why, vault.ErrClosed)
		}
		return guild.ErrSuspended
	}
	return c.Decide(ctx, joinID, d)
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
	}
	// the key first: a key that is refused, or wrong, says why, where "no copy" would not
	if err := vault.Check(ctx, e.vaultCopy().Keys, db, transport.CAKeySecret); err != nil {
		return fmt.Errorf("%w: %w", guild.ErrNotPromotable, err)
	}
	if !e.vaultCopy().Holds(ctx) { // and the envelope
		return fmt.Errorf("%w: the machine has no copy of the vault", guild.ErrNotPromotable)
	}
	return nil
}

// vaultOpens says whether the copy of the vault this Tower holds would open over
// its replica and give it the key of the CA (vault.Check). The promotion asks
// before it wins the epoch: after, the failure would leave the epoch in the lock
// without a Citadel (and the old Citadel of a planned handover, which sees the
// new epoch, steps down).
func (e *Engine) vaultOpens(ctx context.Context) error {
	db, err := guilddb.Open(ctx, e.paths.guildDB(), store.Options{Clock: e.clk, Synchronous: e.cfg.Synchronous, Replica: true})
	if err != nil {
		return err
	}
	defer db.Close()
	if err := vault.Check(ctx, e.vaultCopy().Keys, db, transport.CAKeySecret); err != nil {
		return fmt.Errorf("app: the promotion cannot go on, the vault of this machine does not serve: %w", err)
	}
	return nil
}

// promote takes the command, once the Tower period stopped applying the
// replication: it reads the last lsn of the replica, checks that its copy of
// the vault would open (without it there is no CA to sign with, and the epoch
// would be lost) and wins the next epoch in the lock before anything writes,
// so losing the lock, or not reaching it, leaves the replica exactly as the
// Citadel sent it and the machine goes on as its Tower. Winning, it opens the
// replica as the main database with the
// vault and the CA, signs its certificate and becomes the Citadel, which
// waits before acting outside. A failure after the lock was won leaves the
// epoch in the lock without a certificate: the Owner is warned and promotes
// again (the next epoch).
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
	from, err := e.replicaLSN(ctx)
	if err != nil {
		return fail(err)
	}
	if from < p.wantLSN {
		return fail(fmt.Errorf("app: the replica has the lsn %d, the handover %d", from, p.wantLSN))
	}
	if err := e.vaultOpens(ctx); err != nil {
		return fail(err)
	}
	n, err := guild.ClaimEpoch(ctx, e.deps.Lock, e.epochs.Epoch(), e.id.MachineID)
	if err != nil {
		return fail(err)
	}
	take, err := e.openMain(ctx)
	if err != nil {
		return fail(fmt.Errorf("app: won the epoch %d in the lock, but cannot take the command: %w", n, err))
	}
	release := func() { take.vault.Close(); take.db.Close() }
	if take.ca, err = guild.LoadCA(ctx, take.db, take.vault, caPurpose, e.clk); err != nil {
		release()
		return fail(fmt.Errorf("app: won the epoch %d in the lock, but cannot take the command: %w", n, err))
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
	// the promoted machine issues its own certificate of mestre.local
	if c, err := guild.IssueCitadelCertificate(ctx, take.db, take.ca, e.id.MachineID, e.machine.Key(), e.clk); err != nil {
		e.log.Error("app: the certificate of the Citadel", "err", err)
	} else {
		e.cert.Set(c, e.machine.Key())
	}
	p.done <- nil
	take.promoted = true
	return transition{to: RoleCitadel, take: take}
}

// replicaLSN is the last lsn the replica on this disk applied, read without
// writing anything.
func (e *Engine) replicaLSN(ctx context.Context) (uint64, error) {
	db, err := guilddb.Open(ctx, e.paths.guildDB(), store.Options{Clock: e.clk, Synchronous: e.cfg.Synchronous, Replica: true})
	if err != nil {
		return 0, err
	}
	defer db.Close()
	if _, err := guild.GuildCertificate(ctx, db); err != nil {
		return 0, fmt.Errorf("app: the replica holds no Guild: %w", err)
	}
	return db.LastLSN(ctx)
}

// handleGuildConfig is guild.config on a Tower (docs/conectividade/mensagens.md):
// the refusals of the credentials (the replica's and the list it carries),
// with the open connections they now refuse closed, the addresses and ports
// of the machines for a change of Citadel outside the local network
// (docs/conectividade/descoberta.md#máquina-fora-da-rede-local), and a check
// of the engine certificate, whose names may have changed (an exclusion, new
// addresses).
func (e *Engine) handleGuildConfig(db *store.DB, locator *discovery.Locator, kickRenew func()) protocol.HandlerFunc {
	return func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		cfg, ok := m.Payload.(*protocol.GuildConfig)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "guild.config payload is %T", m.Payload)
		}
		if m.Peer.Role != protocol.RoleMachine || m.Peer.ID != e.epochs.Citadel() {
			return nil, protocol.NewError(protocol.CodeForbidden, "only the Citadel sends guild.config")
		}
		if err := e.creds.Reload(ctx, db); err != nil {
			e.log.Warn("app: the refused credentials of the replica", "err", err)
		}
		e.creds.Learn(cfg.Revoked, cfg.Retired)
		e.tls.CloseRevoked()
		var ms []discovery.MachineAddrs
		for _, mc := range cfg.Machines {
			if mc.MachineID == e.id.MachineID || mc.Port <= 0 || len(mc.Addresses) == 0 {
				continue
			}
			ms = append(ms, discovery.MachineAddrs{MachineID: mc.MachineID, Addrs: mc.Addresses, Port: mc.Port})
		}
		if err := locator.RememberMachines(ctx, ms); err != nil {
			e.log.Warn("app: keeping the addresses of guild.config", "err", err)
		}
		kickRenew()
		return nil, nil
	}
}

// renewTower renews the engine certificate of this Tower when it is due: in
// its last 30 days, or when the registry (the replica) gives it other names
// than it carries. It checks every renewCheck and when kicked (a connection,
// guild.config). Names that a renewal did not change wait renewNamesHold.
func (e *Engine) renewTower(ctx context.Context, db *store.DB, r *guild.Renewer, link *protocol.Link, kick <-chan struct{}) {
	var hold time.Time
	check := func() {
		if !link.Connected() {
			return
		}
		now := e.clk.Now()
		leaf := e.machine.Leaf()
		byTime := transport.NeedsRenewal(leaf, now)
		m, err := guild.MachineOf(ctx, db, e.id.MachineID)
		byNames := err == nil && transport.NamesChanged(leaf, m.Names())
		if !byTime && (!byNames || now.Before(hold)) {
			return
		}
		if err := r.Renew(ctx); err != nil {
			if ctx.Err() == nil {
				e.log.Warn("app: renewing the engine certificate", "err", err)
			}
			return
		}
		if byNames && transport.NamesChanged(e.machine.Leaf(), m.Names()) {
			hold = e.clk.Now().Add(renewNamesHold)
		}
	}
	t := e.clk.NewTicker(e.deps.RenewEvery)
	defer t.Stop()
	for {
		check()
		select {
		case <-ctx.Done():
			return
		case <-t.C():
		case <-kick:
		}
	}
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
		if err := e.startPromotion(bg, h.ApprovalID, h.ApprovedBy, h.LastLSN); err != nil {
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
	// Dropped are the external actions the fence dropped at the demotion.
	Dropped []string `json:"dropped,omitempty"`
}

// saveDropped keeps the names of the external actions a demotion dropped,
// until the report of what was discarded takes them to the new Citadel.
func saveDropped(path string, names []string) error {
	b, err := json.Marshal(names)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func loadDropped(path string) []string {
	var names []string
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &names)
	}
	return names
}

// discardedRe matches the main file of a database this machine discarded:
// descartado-<epoch>.db, or the same followed by .<ms> when the epoch had been
// archived already (archive keeps the first and renames the second).
var discardedRe = regexp.MustCompile(`^` + regexp.QuoteMeta(discardedPrefix) + `(\d+)\.db(\.\d+)?$`)

// discardedAll lists the main file of every database this machine discarded,
// the ones kept under another name included, with the epoch of each.
func (e *Engine) discardedAll() map[string]uint64 {
	out := map[string]uint64{}
	entries, _ := os.ReadDir(e.paths.Banco)
	for _, en := range entries {
		m := discardedRe.FindStringSubmatch(en.Name())
		if m == nil {
			continue // its -wal and -shm, the report and the actions go with the database
		}
		if n, err := strconv.ParseUint(m[1], 10, 64); err == nil {
			out[filepath.Join(e.paths.Banco, en.Name())] = n
		}
	}
	return out
}

// discardedFiles lists the databases this machine discarded, by epoch, the ones
// that were kept under another name left out: there is one report for each epoch.
func (e *Engine) discardedFiles() map[uint64]string {
	out := map[uint64]string{}
	for path, epoch := range e.discardedAll() {
		if filepath.Base(path) == filepath.Base(e.paths.discarded(epoch)) {
			out[epoch] = path
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
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				delete(files, epoch) // erased by the exclusion or the leaving: nothing left to report
				continue
			}
			if e.epochs.Epoch() <= epoch {
				continue // the certificate of the epoch that replaced it is not known yet
			}
			rec, err := guild.EpochStart(ctx, db, epoch+1)
			if err != nil {
				continue // the replica does not have it yet
			}
			r, err := e.listDiscarded(ctx, path, epoch, rec.FromLSN, link)
			if err != nil {
				if fi, serr := os.Stat(path); serr == nil && fi.Size() == 0 {
					// The exclusion erased it while it was being opened and the
					// opening made an empty file again.
					_ = os.Remove(path)
					delete(files, epoch)
					continue
				}
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

// discardedID is the id of the message that reports the epoch: the same on
// every try of this process, so the outbox holds it once (a new process sends
// it again with a new id, and the Citadel records each transaction once).
func (e *Engine) discardedID(epoch uint64) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.discardIDs == nil {
		e.discardIDs = map[uint64]string{}
	}
	if id, ok := e.discardIDs[epoch]; ok {
		return id
	}
	id := protocol.NewIDGen(e.clk).NewString()
	e.discardIDs[epoch] = id
	return id
}

// discardedAckWait is how long the report of the discarded transactions waits
// for the ack before the next try.
const discardedAckWait = 30 * time.Second

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
	r := discardReport{Epoch: epoch, FromLSN: from, Total: total, Dropped: loadDropped(e.paths.droppedActions(epoch))}
	for _, t := range txs {
		r.LSNs = append(r.LSNs, t.LSN)
	}
	if total > 0 || len(r.Dropped) > 0 {
		// The record of what was reported is written only once the Citadel
		// acknowledged: a refusal (retryable, as while it hands over) leaves
		// the message in the outbox and the next try sends the same id, which
		// the Citadel runs once.
		cctx, cancel := clock.WithTimeout(ctx, e.clk, discardedAckWait)
		res, err := link.Call(cctx, protocol.Outgoing{Type: protocol.TypeGuildDiscarded, ID: e.discardedID(epoch), Payload: protocol.GuildDiscarded{
			Epoch: epoch, FromLSN: from, Total: total, Transactions: txs, Dropped: r.Dropped,
		}})
		cancel()
		if err != nil {
			return discardReport{}, err
		}
		if res.Status == protocol.AckError {
			return discardReport{}, fmt.Errorf("app: the Citadel refused guild.discarded: %v", res.Error)
		}
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	if err := os.WriteFile(strings.TrimSuffix(path, ".db")+".json", append(b, '\n'), 0o600); err != nil {
		return discardReport{}, err
	}
	e.log.Warn("app: the transactions lost in the change of Citadel were reported", "epoch", epoch, "from_lsn", from, "total", total)
	return r, nil
}

// pruneDiscarded removes the discarded databases older than the retention that
// were already reported,
// the ones kept under another name included, with what goes with each: its -wal
// and -shm, and, for the one of the epoch, the report and the dropped actions.
func (e *Engine) pruneDiscarded() {
	cutoff := e.clk.Now().Add(-DiscardedRetention)
	all := e.discardedAll()
	reported := map[uint64]bool{} // read before anything goes: the files of an epoch share one report
	for _, epoch := range all {
		_, err := os.Stat(strings.TrimSuffix(e.paths.discarded(epoch), ".db") + ".json")
		reported[epoch] = err == nil
	}
	for path, epoch := range all {
		fi, err := os.Stat(path)
		if err != nil || !fi.ModTime().Before(cutoff) {
			continue
		}
		// What was not reported to the new Citadel stays, whatever its age: the
		// report is the audit of the transactions that were lost.
		if !reported[epoch] {
			continue
		}
		if err := os.Remove(path); err != nil {
			e.log.Warn("app: removing a discarded database after the retention", "file", path, "err", err)
			continue
		}
		_ = os.Remove(path + "-wal")
		_ = os.Remove(path + "-shm")
		if filepath.Base(path) == filepath.Base(e.paths.discarded(epoch)) {
			_ = os.Remove(strings.TrimSuffix(path, ".db") + ".json")
			_ = os.Remove(e.paths.droppedActions(epoch))
		}
		e.log.Info("app: a discarded database left after the retention", "file", path)
	}
}

// osName is the system for the entry request.
func osName() string { return runtime.GOOS }

// leaveGrace is how long the engine of a machine that left the Guild stays up
// after the erasure, so that the answer to guild.leave reaches the Citadel.
const leaveGrace = 2 * time.Second

// stopEngine ends the engine after the machine left the Guild: it erased its
// share and has nothing left to serve (guild.TowerLeave.OnLeft). The end comes
// a moment later, once the Citadel has the answer.
func (e *Engine) stopEngine() {
	e.mu.Lock()
	stop := e.stop
	e.mu.Unlock()
	if stop != nil {
		e.clk.AfterFunc(leaveGrace, stop)
	}
}
