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
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// caPurpose is what the audit of the vault records when the Citadel opens the
// key of the CA (docs/plataforma/cofre.md): once per period as the Citadel,
// when it first acts or when it wins a promotion. The key then stays in
// memory for the period, to issue and renew the certificates of the machines
// and sign the certificates of epoch, and is wiped when the period ends.
const caPurpose = "citadel: issue and renew the machine certificates and sign the certificates of epoch; held in memory while this machine is the Citadel"

// renewCheck is how often a machine sees whether its engine certificate is
// due: inside its last 30 days, or with names the registry no longer gives.
const renewCheck = time.Minute

// renewNamesHold is how long a machine waits before it renews again for
// names that a renewal did not change: the registry and the certificate
// disagree on something only a person can fix.
const renewNamesHold = time.Hour

// lazyPresence reaches the server of the period, which is built after the
// registry that needs it.
type lazyPresence struct{ srv **protocol.Server }

func (p lazyPresence) Connected(id string) bool {
	return *p.srv != nil && (*p.srv).Link(id).Connected()
}

// citadelNames is the NameBook of the registry of the sheets: it reads the
// registry in the main database; a change of names waits for the Citadel to
// act (the next sheet brings it again), and goes to the machine in
// guild.config, so it renews its certificate at once.
type citadelNames struct {
	e   *Engine
	run *citadelRun
}

func (n citadelNames) Machine(ctx context.Context, id string) (guild.Machine, error) {
	return guild.MachineOf(ctx, n.run.db, id)
}

func (n citadelNames) UpdateNames(ctx context.Context, id, hostname string, ips []net.IP) error {
	c := n.run.guild()
	if c == nil {
		if why := n.run.whyDown(); why != "" {
			return fmt.Errorf("app: the Citadel cannot act, %s; the names wait for the next sheet", why)
		}
		return errors.New("app: the Citadel does not act yet; the names wait for the next sheet")
	}
	if err := c.UpdateNames(ctx, id, hostname, ips); err != nil {
		return err
	}
	if id != n.e.id.MachineID { // the Citadel renews its own by itself (renewOwn)
		n.e.sendConfig(ctx, n.run, id)
	}
	return nil
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
	// lastLSN is the last lsn of the replica, read before anything of the
	// promotion wrote (opening the key of the CA writes its use).
	lastLSN uint64
	vault   *vault.Vault
	// ca is the CA a promotion opened to sign its certificate; a Citadel that
	// starts opens it when it first acts.
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
	vault   *vault.Vault
	gate    gate
	// ran keeps the results of the calls that may come back from the
	// outbox of a Tower (ranCalls).
	ran *ranCalls

	mu      sync.Mutex
	ca      *transport.CA
	citadel *guild.Citadel  // nil until the Citadel first acts
	refused map[string]bool // Towers whose messages waited for it
	// down is why the Citadel cannot act (the vault is closed, so the CA does
	// not open), from the start of the period; empty otherwise.
	down string

	base      context.Context
	external  []ExternalFunc
	firstAct  func()
	once      sync.Once
	extMu     sync.Mutex
	extCancel context.CancelFunc
	extWG     sync.WaitGroup
}

// waiting notes a Tower whose message waited for the Citadel to act.
func (r *citadelRun) waiting(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.refused == nil {
		r.refused = map[string]bool{}
	}
	r.refused[id] = true
}

// resendWaiting closes, with 4005, the connections of the Towers whose
// messages waited for the Citadel to act: they reconnect at once and their
// outbox sends those messages again.
func (r *citadelRun) resendWaiting() {
	r.mu.Lock()
	ids := r.refused
	r.refused = nil
	r.mu.Unlock()
	for id := range ids {
		if s := r.srv.Link(id).Session(); s != nil {
			s.Close(protocol.CloseShuttingDown, "the Citadel acts now: send again")
		}
	}
}

// guild is the Citadel of the Guild of this period: nil until it first acts
// (a Citadel that starts suspended opens neither the CA nor the entries).
func (r *citadelRun) guild() *guild.Citadel {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.citadel
}

// vaultClosed records that the vault of this period did not open, and so its
// CA does not either: what needs the CA answers that, instead of saying that the
// Citadel does not act yet and waiting for it. The key is fixed, and the engine
// restarted, by the Owner.
func (e *Engine) vaultClosed(run *citadelRun) {
	const why = "its vault is closed"
	run.mu.Lock()
	run.down = why
	run.mu.Unlock()
	e.joins.unavailable("the Citadel cannot take entries: " + why)
}

// primaryVault is the copy of the vault that the Citadel sends to its Towers:
// none while the vault of this Citadel is closed. The key it would send is
// the one that did not open the vault, perhaps a wrong one, and a Tower accepts
// a key ring of the versions of its envelope and replaces its own: the good
// copies of the Towers, which may be the only ones, would be lost.
func (e *Engine) primaryVault(v *vault.Vault) *vault.Copier {
	if v.Err() != nil {
		e.log.Warn("app: the vault is closed, so this Citadel sends no copy of its key to the Towers")
		return nil
	}
	return &vault.Copier{Keys: vault.NewFileKeyStore(e.paths.vaultKey()), EnvelopePath: e.paths.envelope()}
}

// needsCitadel is the handler of what needs the CA, which the Citadel opens
// when it first acts: until then a Tower is answered with a retryable error,
// and, if it cannot act at all, with why, and not retryable: the message must
// not wait in the outbox of the Tower (a renewal asks again by itself, with a
// new message each time) for a Citadel that does not come until the Owner fixes
// the key and restarts it.
func (r *citadelRun) needsCitadel(handle func(c *guild.Citadel) protocol.HandlerFunc) protocol.HandlerFunc {
	return r.gated(func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		c := r.guild()
		if c == nil {
			if why := r.whyDown(); why != "" {
				return nil, protocol.NewError(protocol.CodeForbidden, "the Citadel cannot act: %s", why)
			}
			// retryable: the outbox of the Tower keeps it, and the Citadel
			// makes it reconnect (and send it again) once it acts
			r.waiting(m.Peer.ID)
			return nil, protocol.NewError(protocol.CodeUnavailable, "the Citadel does not act yet")
		}
		return handle(c)(ctx, m)
	})
}

// whyDown is why the Citadel cannot act, or empty.
func (r *citadelRun) whyDown() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.down
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

// gate holds the messages and calls that change the state of the Guild
// while a planned handover reads the last lsn: once it drains, they are
// answered with a retryable unavailable, so the outbox of each Tower sends
// them again to the new Citadel (docs/guilda/cidadela-e-torres.md#troca-planejada).
type gate struct {
	mu       sync.Mutex
	draining bool
	inFlight int
	idle     chan struct{}
}

// enter admits one change; false while draining. leave must follow.
func (g *gate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.draining {
		return false
	}
	g.inFlight++
	return true
}

func (g *gate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inFlight--
	if g.inFlight == 0 && g.idle != nil {
		close(g.idle)
		g.idle = nil
	}
}

// drain stops admitting changes; the channel closes when the ones admitted
// before ended.
func (g *gate) drain() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.draining = true
	ch := make(chan struct{})
	if g.inFlight == 0 {
		close(ch)
	} else {
		g.idle = ch
	}
	return ch
}

func (g *gate) isDraining() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.draining
}

// errHandingOver is the answer to a change while the Citadel hands over:
// retryable, so the message stays in the outbox of its sender.
func errHandingOver() error {
	return protocol.NewError(protocol.CodeUnavailable, "the Citadel is handing over the command; send it again to the new one")
}

// gated runs h only while the Citadel admits changes.
func (r *citadelRun) gated(h protocol.HandlerFunc) protocol.HandlerFunc {
	return func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		if !r.gate.enter() {
			return nil, errHandingOver()
		}
		defer r.gate.leave()
		return h(ctx, m)
	}
}

// openMain opens the Guild database as the main one and the vault, which stays
// closed when its key does not open it (openVault); the CA opens later, when
// the Citadel acts or wins the promotion, because opening its key writes its
// use.
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
	v, err := e.openVault(ctx, db)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &takeover{db: db, lastLSN: last, vault: v}, nil
}

// openVault opens the vault over the main database db, with the masker of
// the logger, and every read of it in the audit. A key that is missing,
// refused or wrong does not fail it: the vault comes back closed, every
// function that needs it fails with a clear error, the engine raises the
// grave alert and does the rest (docs/plataforma/cofre.md#regras-e-erros).
func (e *Engine) openVault(ctx context.Context, db *store.DB) (*vault.Vault, error) {
	var recovery []byte
	if err := db.Reader().QueryRowContext(ctx, `SELECT recovery_public_key FROM guild WHERE id = 1`).Scan(&recovery); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("app: the Guild database holds no Guild")
		}
		return nil, fmt.Errorf("app: read the Guild: %w", err)
	}
	v, err := vault.Open(ctx, vault.Config{
		DB: db, Keys: vault.NewFileKeyStore(e.paths.vaultKey()), EnvelopePath: e.paths.envelope(),
		RecoveryPublicKey: recovery, Masker: e.deps.Masker, Clock: e.clk,
		OnAlert: func(a vault.Alert) {
			if ctx.Err() == nil { // an engine that is stopping makes the opening fail, with no key to fix
				e.vaultAlert(a)
			}
		},
		OnUse: func(u vault.UseEvent) {
			if err := guild.RecordUse(context.WithoutCancel(ctx), db, e.clk, e.id.MachineID, u.Name, u.Purpose); err != nil {
				e.log.Warn("app: the audit of a read of the vault", "secret", u.Name, "err", err)
			}
		},
	})
	if err != nil {
		// only a wrong configuration: a closed vault is not an error here
		return nil, fmt.Errorf("app: open the vault: %w", err)
	}
	return v, nil
}

// vaultAlert is the grave alert of a vault that does not open: the log, and
// the notice to the Owner (Deps.OnVaultAlert).
func (e *Engine) vaultAlert(a vault.Alert) {
	e.log.Error("app: the vault does not open", "severity", a.Severity, "check", a.Check, "err", a.Err)
	if e.deps.OnVaultAlert != nil {
		e.deps.OnVaultAlert(a)
	}
}

// startCitadel opens the CA, if the period has not yet, and the Citadel of
// the Guild: the entries, the renewals, the exclusions and the removals. It
// runs when the Citadel first acts.
func (e *Engine) startCitadel(ctx context.Context, run *citadelRun) (*guild.Citadel, error) {
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.citadel != nil {
		return run.citadel, nil
	}
	if run.ca == nil {
		// opening the key of the CA writes its use in the audit: not once the
		// handover read its last lsn (it would be lost with this Citadel)
		if !run.gate.enter() {
			return nil, errHandingOver()
		}
		ca, err := guild.LoadCA(ctx, run.db, run.vault, caPurpose, e.clk)
		run.gate.leave()
		if err != nil {
			return nil, err
		}
		run.ca = ca
	}
	c, err := guild.NewCitadel(ctx, guild.CitadelConfig{
		DB: run.db, CA: run.ca, Clock: e.clk, Cert: e.cert, Approver: e.deps.Approver, Credentials: e.creds,
		Peers: guild.ProtocolPeers{Server: run.srv, Transport: e.tls, Clock: e.clk},
		Hooks: guild.Hooks{
			ExclusionChanged: run.primary.ExclusionChanged,
			Removed:          func(guild.Machine, string) { e.sendConfig(context.WithoutCancel(ctx), run) },
		},
		Suspended: func() bool { return !run.fence.Acting() || run.gate.isDraining() },
		Admit: func() (func(), bool) {
			if !run.gate.enter() {
				return nil, false
			}
			return run.gate.leave, true
		},
		Logger: e.log,
	})
	if err != nil {
		return nil, err
	}
	run.citadel = c
	return c, nil
}

// runCitadel serves as the Citadel until the role changes or ctx ends: the
// main database, the vault, the Towers, the replication and, once the fence
// lets it act, the CA, the entries and the external functions.
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
	run := &citadelRun{db: db, epoch: e.epochs.Epoch(), base: pctx, external: e.deps.External, vault: take.vault, ca: take.ca,
		ran: newRanCalls(e.clk, e.queuedResult)}
	if take.vault.Err() != nil {
		// no CA, so no entries and no certificates, and the Towers hear it from
		// the start, not from the first time the Citadel acts
		e.vaultClosed(run)
	}
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
		// mestre.local is only the acting Citadel's: back to the machine
		// certificate (a new period issues it again when it acts)
		e.cert.Set(e.machine.Leaf(), e.machine.Key())
		if next.to == RoleTower {
			run.fence.Demote()
			if run.srv != nil {
				run.srv.CloseAll(protocol.CloseNotCitadel, "this machine is not the Citadel")
			}
		} else if run.srv != nil {
			run.srv.Shutdown()
		}
		cancel()
		run.extWG.Wait() // firstAct starts goroutines of wg: it ends first
		wg.Wait()
		e.joins.clear() // and what firstAct mounted or said, if it ran while the period ended
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

	promoted, wait := take.promoted, guild.PromotedWait
	if !promoted {
		// A Citadel that restarts during the wait of its promotion (or soon after,
		// as a clock that stepped forward may hide) waits it whole again.
		if rec, err := guild.EpochStart(ctx, db, run.epoch); err == nil && rec.Epoch > 1 && rec.CitadelMachineID == e.id.MachineID {
			if left := guild.WaitLeft(e.clk, rec.StartedAt, guild.PromotedWait); left > 0 {
				promoted, wait = true, left
				e.log.Warn("app: the wait of the promotion was not over; waiting it again", "wait", left)
			}
		}
	}
	run.fence = guild.NewFence(guild.FenceConfig{
		Lock: e.deps.Lock, Clock: e.clk, Epoch: func() uint64 { return run.epoch }, Promoted: promoted, Wait: wait, Logger: e.log,
		OnHigher: func(uint64) { e.signal(transition{to: RoleTower, unknown: true}) },
		OnChange: func(s guild.FenceState) {
			run.setActing(s == guild.FenceConfirmed || s == guild.FenceLockDown || s == guild.FenceNoLock)
			e.changed()
		},
		OnDiscard: func(names []string) { e.dropped(run.epoch, names) },
	})
	if err := e.reloadCreds(ctx, db); err != nil {
		return transition{err: fmt.Errorf("app: the refused credentials of the Guild: %w", err)}
	}
	router := protocol.NewRouter()
	sqls := protocol.NewSQLStore(db)
	pcfg := protocol.Config{
		MachineID: e.id.MachineID, Clock: e.clk, Router: router, Epoch: e.epochs, Certs: e.epochs, Outbox: sqls,
		Processed:     dupWatch{Processed: sqls, onDup: run.ran.duplicate},
		BinaryVersion: e.deps.Version, Logger: e.log,
		IsCitadel: func() bool {
			e.mu.Lock()
			current := e.cit == run && e.role == RoleCitadel
			e.mu.Unlock()
			// a planned handover demotes the fence but keeps serving the
			// Towers (the chosen machine confirms the last lsn) until the lock
			// shows the new epoch
			serving := run.fence.State() != guild.FenceDemoted || run.gate.isDraining()
			return current && serving && e.epochs.Citadel() == e.id.MachineID
		},
	}
	var srv *protocol.Server
	primary, err := replication.NewPrimary(ctx, replication.PrimaryConfig{
		DB: db, Clock: e.clk, Dir: e.paths.snapshots(), Logger: e.log,
		Roster: replication.RosterFunc(func(ctx context.Context, id string) (bool, error) {
			m, err := guild.MachineOf(ctx, db, id)
			return m.Excluded, err
		}),
		Vault:       e.primaryVault(take.vault),
		GuildConfig: func(ctx context.Context) (protocol.GuildConfig, error) { return guild.ConfigOf(ctx, db, e.creds) },
		Paused:      func() bool { return !run.fence.Acting() },
		Gate:        run.gated,
	})
	if err != nil {
		return transition{err: err}
	}
	run.primary = primary
	registry, err := machinesheet.NewRegistry(machinesheet.RegistryConfig{
		DB: db, Presence: lazyPresence{&srv}, Self: func() string { return e.id.MachineID }, Names: citadelNames{e: e, run: run}, Logger: e.log,
	})
	if err != nil {
		return transition{err: err}
	}
	pcfg.Hooks = primary.Hooks(protocol.Hooks{
		OnHello: func(ctx context.Context, peer protocol.Peer, h *protocol.Hello) error {
			// the sheet of the hello is a write: during the handover the
			// opening is refused (retryable) and the Tower sends it again to
			// the new Citadel
			if !run.gate.enter() {
				return errHandingOver()
			}
			defer run.gate.leave()
			return registry.OnHello(ctx, peer, h)
		},
		// every Tower that connects learns the machines and the refusals
		OnReady: func(s *protocol.Session) { go e.sendConfig(pctx, run, s.Peer().ID) },
	})
	if srv, err = protocol.NewServer(pcfg); err != nil {
		return transition{err: err}
	}
	run.srv = srv
	primary.Attach(srv)
	// The handlers of what changes the Guild pass the gate of the handover;
	// those that need the CA wait for the Citadel to act.
	guild.Register(router, guild.Wiring{Citadel: run.guild})
	primary.Register(router)
	registry.Register(router)
	router.HandleFunc(protocol.TypeCertRenew, run.needsCitadel(func(c *guild.Citadel) protocol.HandlerFunc { return c.HandleCertRenew }))
	router.HandleFunc(protocol.TypeGuildLeave, run.needsCitadel(func(c *guild.Citadel) protocol.HandlerFunc { return c.HandleLeave }))
	router.HandleFunc(protocol.TypeMachineSheet, run.gated(registry.Handler().Handle))
	router.HandleFunc(protocol.TypeCLICall, e.handleCLICall(run)) // it answers in cli.result, gate included
	router.HandleFunc(protocol.TypeGuildDiscarded, run.gated(guild.DiscardedHandler(func() *store.DB { return db }, e.clk)))

	e.setGuildDB(db)
	e.mu.Lock()
	e.role, e.cit, e.tow, e.unknown, e.failover = RoleCitadel, run, nil, false, false
	e.mu.Unlock()
	// What needs the CA starts when the Citadel first acts: a Citadel that
	// only starts, suspended, and steps down opens nothing and writes nothing
	// of the Guild (the use of the key of the CA, its own sheet). Its own
	// sheet is then kept up to date every five minutes like the others (an
	// agent whose link is the registry itself).
	// A promotion opened the CA already: the Citadel of the Guild is there
	// during the wait, for the renewals and departures the Towers resend.
	if take.ca != nil {
		if _, err := e.startCitadel(pctx, run); err != nil {
			return transition{err: err}
		}
	}
	run.firstAct = func() {
		c, err := e.startCitadel(pctx, run)
		switch {
		case errors.Is(err, vault.ErrClosed):
			// said at the start (vaultClosed); its own sheet needs no CA, and goes on
			e.log.Error("app: the Citadel does not open its CA", "err", err)
		case err != nil:
			e.log.Error("app: the Citadel does not open its CA", "err", err)
			return
		default:
			run.resendWaiting()
			c.Mount(e.joins)
			wg.Go(func() { c.Run(pctx) })
			wg.Go(func() { e.renewOwn(pctx, run) })
		}
		builder, err := e.sheetBuilder()
		if err != nil {
			return
		}
		agent, err := machinesheet.NewAgent(machinesheet.AgentConfig{Builder: builder, Clock: e.clk, Store: e.sheetStore(), Logger: e.log})
		if err != nil {
			return
		}
		agent.Attach(actingSender{fence: run.fence, to: registry.Local(e.id.MachineID)})
		if sheet, err := agent.Hello(pctx); err == nil && run.gate.enter() {
			_ = registry.Replace(pctx, e.id.MachineID, sheet)
			run.gate.leave()
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

// sendConfig sends guild.config to the machines ids, or to every connected
// Tower without ids: the machines of the Guild with their addresses and
// ports, and the revoked certificates.
func (e *Engine) sendConfig(ctx context.Context, run *citadelRun, ids ...string) {
	if run.srv == nil {
		return
	}
	cfg, err := guild.ConfigOf(ctx, run.db, e.creds)
	if err != nil {
		e.log.Warn("app: guild.config", "err", err)
		return
	}
	if len(ids) == 0 {
		st, err := run.primary.Status(ctx)
		if err != nil {
			e.log.Warn("app: guild.config", "err", err)
			return
		}
		for _, s := range st {
			ids = append(ids, s.Machine)
		}
	}
	for _, id := range ids {
		if _, err := run.srv.Link(id).Send(ctx, protocol.Outgoing{Type: protocol.TypeGuildConfig, Payload: cfg}); err != nil && ctx.Err() == nil {
			e.log.Warn("app: guild.config", "machine", id, "err", err)
		}
	}
}

// renewOwn keeps the certificates of the Citadel itself, with its own CA:
// nobody else issues them. At every check, its engine certificate of one
// year, when it is due (its last 30 days, or names the registry no longer
// gives), and the certificate of mestre.local the acting Citadel presents
// (renewCitadelName).
func (e *Engine) renewOwn(ctx context.Context, run *citadelRun) {
	var hold time.Time
	machine := func(c *guild.Citadel) {
		leaf := e.machine.Leaf()
		m, err := guild.MachineOf(ctx, run.db, e.id.MachineID)
		if err != nil {
			e.log.Warn("app: the names of the Citadel", "err", err)
			return
		}
		byTime := transport.NeedsRenewal(leaf, e.clk.Now())
		byNames := transport.NamesChanged(leaf, m.Names())
		if !byTime && (!byNames || e.clk.Now().Before(hold)) {
			return
		}
		// the renewal is a write of the Guild: not once the handover read its
		// last lsn (the certificate would not reach the new Citadel)
		if !run.gate.enter() {
			return
		}
		defer run.gate.leave()
		key, err := transport.GenerateKey()
		if err != nil {
			return
		}
		csr, err := transport.NewCSR(key)
		if err != nil {
			return
		}
		cert, err := c.Renew(ctx, e.id.MachineID, transport.KindMachine, csr)
		if err == nil {
			err = e.member.SaveRenewal(ctx, cert, key)
		}
		if err != nil {
			e.log.Warn("app: renewing the certificate of the Citadel", "err", err)
			return
		}
		e.machine.Set(cert, key)
		if transport.NamesChanged(cert, m.Names()) {
			hold = e.clk.Now().Add(renewNamesHold)
		}
		e.log.Info("app: the Citadel renewed its machine certificate", "serial", transport.Serial(cert), "not_after", cert.NotAfter)
	}
	check := func() {
		c := run.guild()
		if c == nil {
			return
		}
		machine(c)
		if err := e.renewCitadelName(ctx, c); err != nil {
			e.log.Warn("app: the certificate of the Citadel", "err", err)
		}
	}
	check()
	t := e.clk.NewTicker(e.deps.RenewEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			check()
		}
	}
}

// renewCitadelName issues, when it is due, the certificate the acting
// Citadel presents: the only one with mestre.local and the extra names of
// the Guild, for 7 days (docs/arquitetura/seguranca.md#nomes-dos-certificados),
// for the key of its machine certificate, which it keeps aside for when it is
// a Tower again. It runs when the Citadel first acts and at every check of
// renewOwn; a promotion issues it at once (promote).
func (e *Engine) renewCitadelName(ctx context.Context, c *guild.Citadel) error {
	due, err := c.CitadelCertificateDue(ctx, e.cert.Leaf())
	if err != nil || !due {
		return err
	}
	key := e.machine.Key()
	cert, err := c.CitadelCertificate(ctx, e.id.MachineID, key)
	if err != nil {
		return err
	}
	e.cert.Set(cert, key)
	e.log.Info("app: the Citadel presents its certificate of mestre.local", "serial", transport.Serial(cert), "not_after", cert.NotAfter)
	return nil
}

// dropped keeps the names of the external actions the fence dropped when the
// Citadel of epoch stepped down: the report of what it discarded carries
// them to the audit of the new Citadel (guild.discarded).
func (e *Engine) dropped(epoch uint64, names []string) {
	e.log.Warn("app: external actions discarded at the demotion", "epoch", epoch, "actions", names)
	if err := saveDropped(e.paths.droppedActions(epoch), names); err != nil {
		e.log.Error("app: keeping the discarded external actions", "err", err)
	}
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
	// The retention counts from now: the rename keeps the time of the last
	// write the database had as the main one, which can be weeks old.
	now := e.clk.Now()
	if err := os.Chtimes(dst, now, now); err != nil {
		e.log.Warn("app: dating the database of the old epoch", "file", dst, "err", err)
	}
	e.log.Warn("app: the database of the old epoch is kept", "file", dst, "epoch", epoch)
	return nil
}

// handoverPoll is how often the old Citadel reads the lock while the chosen
// machine takes the command: from the first value to the last, doubling; each
// read is a request to the GitHub of the Guild (docs/guilda/trava-no-github.md#cota).
var handoverPoll = [2]time.Duration{time.Second, 10 * time.Second}

// Handover passes the Citadel to the machine target, with the approval of the
// Owner (docs/guilda/cidadela-e-torres.md#troca-planejada): the Citadel stops
// acting outside and stops taking changes (a retryable unavailable, which the
// outbox of each Tower sends again to the new Citadel), waits for the changes
// it took to end, reads the last lsn, waits for the target to confirm it, and
// tells it to promote itself; it steps down when the lock shows the new
// epoch.
func (e *Engine) Handover(ctx context.Context, target, approvalID, approvedBy string, wait time.Duration) error {
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
	wctx, cancel := clock.WithTimeout(ctx, e.clk, wait)
	defer cancel()
	idle := run.gate.drain()
	run.fence.Demote()
	restore := func(err error) error {
		// a new period of the Citadel: the Towers reconnect (4005) and their
		// outbox sends what was refused meanwhile
		e.signal(transition{to: RoleCitadel})
		return err
	}
	select {
	case <-idle:
	case <-wctx.Done():
		return restore(fmt.Errorf("app: the changes under way did not end: %w", wctx.Err()))
	}
	last, err := run.db.LastLSN(ctx)
	if err != nil {
		return restore(err)
	}
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
			Type: protocol.TypeCitadelHandover, Payload: protocol.CitadelHandover{
				TargetMachineID: target, LastLSN: last, ApprovalID: approvalID, ApprovedBy: approvedBy,
			},
		})
		if err == nil && res.Status == protocol.AckError {
			refused <- fmt.Errorf("app: %s refused citadel.handover: %v", target, res.Error)
		}
	}()
	poll := handoverPoll[0]
	for {
		if e.deps.Lock != nil {
			if n, err := e.deps.Lock.Check(wctx); err == nil && n > run.epoch {
				e.signal(transition{to: RoleTower, unknown: true})
				return nil
			}
		}
		t := e.clk.NewTimer(poll)
		select {
		case <-t.C():
		case err := <-refused:
			t.Stop()
			return restore(err)
		case <-wctx.Done():
			t.Stop()
			return restore(fmt.Errorf("app: %s did not take the command: %w", target, wctx.Err()))
		}
		poll = min(poll*2, handoverPoll[1])
	}
}
