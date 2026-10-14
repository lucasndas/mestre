package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/discovery"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Role is the role of the instance now: it comes from the certificate of
// epoch, never from the installation (docs/guilda/cidadela-e-torres.md#papéis).
type Role string

// The roles.
const (
	RoleCitadel Role = "citadel"
	RoleTower   Role = "tower"
)

// Status is what the instance says of itself: the panel, the checklist and
// the tests read it.
type Status struct {
	Instance  string `json:"instance"`
	MachineID string `json:"machine_id"`
	Role      Role   `json:"role"`
	Epoch     uint64 `json:"epoch"`
	// Citadel is the machine the certificate followed names; empty while a
	// demoted Citadel has not found the new one.
	Citadel string `json:"citadel"`
	// Fence is where the Citadel stands with the lock; Acting is true while
	// the external functions are on.
	Fence  guild.FenceState `json:"fence,omitempty"`
	Acting bool             `json:"acting"`
	// Failover is true while this machine takes the command.
	Failover bool `json:"failover"`
	// Connected is true on a Tower connected to the Citadel.
	Connected bool `json:"connected"`
	// LSN is the last lsn of the Guild database here (the last applied on a
	// Tower).
	LSN uint64 `json:"lsn"`
	// Discarded counts the transactions this machine lost when it stopped
	// being the Citadel, as it reported them.
	Discarded int `json:"discarded"`
}

// QueuedResult is the outcome of a call of the CLI that was accepted as
// pending (`"queued": true`) and ran when the Citadel acted: the success or
// the error, with the op_id the caller was given
// (docs/cli/convencoes.md#saída---json).
type QueuedResult struct {
	OpID     string
	AgentID  string
	Cmd      string
	Response localchan.Response
}

// ExternalFunc is one of the functions the Citadel runs outside the Guild
// (the bot of the Telegram, the GitHub: milestone 5). It runs while the
// fence lets the Citadel act and its context ends when it stops; every
// action goes through the fence.
type ExternalFunc func(ctx context.Context, fence *guild.Fence) error

// Deps are the pieces that come from outside the configuration; the zero
// value is production. Tests replace the network, the clock, the lock and the
// Owner.
type Deps struct {
	Clock clock.Clock
	// Network defaults to the TCP of the system; Packets to its UDP and
	// Browser to its mDNS (both off with no_discovery).
	Network transport.Network
	Packets discovery.PacketNetwork
	Browser discovery.Browser
	// Masker hides the secrets the vault opens in the logs: the one of the
	// Logger, so a secret read here never reaches the log. Nil makes one.
	Masker *logging.Masker
	// Lock is the lock of the epoch (milestone 5); nil means no promotion.
	Lock guild.Lock
	// Approver asks the Owner about an entry; Promotions about a promotion.
	// Both arrive with the bot (milestone 5); nil refuses.
	Approver   guild.Approver
	Promotions guild.PromotionApprover
	// External are the functions the Citadel turns on and off.
	External []ExternalFunc
	// Identifier identifies the callers of the local channel; nil identifies
	// nobody (no terminal runs before milestone 4).
	Identifier localchan.Identifier
	// Commands is the definition of the commands of the CLI the engine
	// checks every call against (nil: cli.Default). Profiles gives the level
	// and the class of a character (nil: an ordinary character, with the
	// cli.allow of the effective rules kept on this machine).
	Commands *cli.Registry
	Profiles cli.Profiles
	// OnQueuedResult is told, on the Citadel, the result of a call that
	// waited in the outbox of a Tower; it goes to the inbox of whoever asked
	// (milestone 4 delivers it; until then it is only logged).
	OnQueuedResult func(QueuedResult)
	// Characters, Approvals and States are what the hook dispatcher needs
	// from the terminal registry (M4) and the approvals (M5); nil: no
	// character is known, so every tool call is denied.
	Characters hook.Characters
	Approvals  hook.Approvals
	States     hook.States
	// ClassHooks gives the dispatcher the synchronous hooks of the class of
	// a character (hook.ClassHookSource, over the classes of the workspace,
	// when the registry of characters exists, M4); OnHookEvent is told every
	// event after it is answered (the asynchronous hooks of the classes,
	// hook.AsyncRunner). nil: none.
	ClassHooks  hook.ClassHooks
	OnHookEvent func(providers.Event, hook.Answer)
	// Sheet are the probes of the machine sheet; a nil System reads the
	// system.
	Sheet machinesheet.Sources
	// OnStatus is told every change of the status.
	OnStatus func(Status)
	// OnCitadelDown is the notice to the Owner that the Citadel is away
	// (milestones 5 and 7 show it); OnPromotionFailed that a promotion did
	// not happen.
	OnCitadelDown     func(since time.Time, n int)
	OnPromotionFailed func(err error)
	// RenewEvery is how often a machine sees whether its engine certificate
	// is due (a minute by default).
	RenewEvery time.Duration
	Version    string
	Logger     *slog.Logger
}

// Engine is one instance of the engine: the parts every machine runs (the
// machine database, the engine port, the local channel, the discovery) and
// the functions the Citadel turns on and off as the epoch says.
type Engine struct {
	cfg   Config
	paths Paths
	deps  Deps
	clk   clock.Clock
	log   *slog.Logger

	mdb      *store.DB
	member   *guild.Member
	id       guild.Identity
	epochs   *guild.Epochs
	creds    *guild.Credentials
	cert     *transport.CertSource // presented: the machine's, or the Citadel's with mestre.local
	machine  *transport.CertSource // the engine certificate of the machine
	tcli     *transport.Client
	tls      *transport.Server
	towers   protocol.Listener
	promoter *guild.Promoter
	local    *localchan.LocalForwarder
	down     *guild.DownWatch
	results  *results
	joins    *joinMux
	kick     chan struct{}
	statusCh chan struct{}
	up       atomic.Bool // Run set up the parts above

	// dbMu keeps the Guild database of the period open while someone reads
	// it: a period clears gdb under the write lock before it closes it.
	dbMu sync.RWMutex
	gdb  *store.DB

	mu        sync.Mutex
	role      Role
	unknown   bool // the Citadel is unknown: a demoted Citadel before it finds the new one
	failover  bool
	connected bool
	next      *transition
	cit       *citadelRun
	tow       *towerRun
	discarded int
	// discardIDs are the ids of the reports of the discarded transactions, by epoch.
	discardIDs map[uint64]string
}

// New returns the engine of an instance; Run starts it.
func New(cfg Config, paths Paths, deps Deps) (*Engine, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	if deps.Clock == nil {
		deps.Clock = clock.Real{}
	}
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.DiscardHandler)
	}
	if deps.Network == nil {
		deps.Network = transport.SystemNetwork{Suite: os.Getenv("MESTRE_TEST_SUITE")}
	}
	if deps.Packets == nil && !cfg.NoDiscovery {
		deps.Packets = discovery.SystemPacketNetwork{Suite: os.Getenv("MESTRE_TEST_SUITE")}
	}
	if deps.Browser == nil && !cfg.NoDiscovery {
		deps.Browser = discovery.SystemBrowser{Suite: os.Getenv("MESTRE_TEST_SUITE")}
	}
	if deps.Masker == nil {
		deps.Masker = logging.NewMasker()
	}
	if deps.Approver == nil {
		deps.Approver = noOwner{}
	}
	if deps.Version == "" {
		deps.Version = "dev"
	}
	if deps.RenewEvery <= 0 {
		deps.RenewEvery = renewCheck
	}
	e := &Engine{
		cfg: cfg, paths: paths, deps: deps, clk: deps.Clock, log: deps.Logger.With("instance", cfg.Instance),
		promoter: guild.NewPromoter(deps.Promotions), local: localchan.NewLocalForwarder(),
		results: newResults(), kick: make(chan struct{}, 1), statusCh: make(chan struct{}, 1), role: RoleTower,
	}
	e.down = guild.NewDownWatch(guild.DownWatchConfig{Clock: e.clk, OnDown: e.citadelDown, OnBack: func(time.Time) { e.promoter.Cancel(context.Background()) }})
	return e, nil
}

// Commands is where the Citadel's handlers of the CLI are registered: a call
// made on this machine and the cli.call of a Tower land on the same code.
func (e *Engine) Commands() *localchan.LocalForwarder { return e.local }

// transition is what a period of a role ends with.
type transition struct {
	to Role
	// unknown: demoted without the certificate of the new epoch.
	unknown bool
	// promote: the Owner approved this Tower's promotion.
	promote *promotion
	// take: the promotion's open database, vault and CA.
	take *takeover
	err  error
}

type promotion struct {
	approvalID, approvedBy string
	// wantLSN is the last lsn of a planned handover: the replica has it.
	wantLSN uint64
	done    chan error
}

// signal ends the current period with t. A demotion wins over anything
// pending.
func (e *Engine) signal(t transition) {
	e.mu.Lock()
	if e.next == nil || t.to == RoleTower && e.next.promote == nil {
		e.next = &t
	}
	e.mu.Unlock()
	select {
	case e.kick <- struct{}{}:
	default:
	}
}

func (e *Engine) takeNext() *transition {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.next
	e.next = nil
	return t
}

// wait blocks until the period must end.
func (e *Engine) wait(ctx context.Context) (transition, bool) {
	for {
		select {
		case <-ctx.Done():
			return transition{}, false
		case <-e.kick:
			if t := e.takeNext(); t != nil {
				return *t, true
			}
		}
	}
}

// Run starts the instance and keeps it until ctx ends.
func (e *Engine) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	defer wg.Wait()
	if err := e.paths.ensure(); err != nil {
		return err
	}
	mdb, err := machine.Open(ctx, e.paths.machineDB(), store.Options{Clock: e.clk, Synchronous: e.cfg.Synchronous})
	if err != nil {
		return err
	}
	defer mdb.Close()
	e.mdb = mdb
	e.member = guild.NewMember(mdb, vault.NewFileKeyStore(e.paths.engineKey()), e.clk)
	e.member.UsePendingKey(vault.NewFileKeyStore(e.paths.pendingKey()))
	id, src, err := e.member.TLS(ctx)
	if errors.Is(err, guild.ErrNotMember) {
		return fmt.Errorf("app: the instance %s is in no Guild: create one or join one first", e.cfg.Instance)
	}
	if err != nil {
		return err
	}
	// cert is what this machine presents; machine its engine certificate of
	// one year, which an acting Citadel keeps aside while it serves the
	// certificate of mestre.local
	e.id, e.machine, e.cert = id, src, transport.NewCertSource(src.Leaf(), src.Key())
	if e.epochs, err = guild.LoadEpochs(ctx, guild.EpochsConfig{Member: e.member, Logger: e.log, OnFollow: e.onFollow, OnRefused: e.refusedCert}); err != nil {
		return err
	}
	e.creds = guild.NewCredentials()
	if err := e.preflight(ctx); err != nil {
		return err
	}
	if e.tls, err = transport.NewServer(transport.ServerConfig{CA: id.CA, Cert: e.cert, Credentials: e.creds, Clock: e.clk, Logger: e.log}); err != nil {
		return err
	}
	e.towers = keepOpen{protocol.FromTransportListener(e.tls.Route(transport.RouteTower, transport.KindMachine))}
	e.joins = &joinMux{m: e.tls, h: map[string]http.Handler{}}
	ln, err := e.deps.Network.Listen(net.JoinHostPort(e.cfg.Bind, strconv.Itoa(e.cfg.EnginePort)))
	if err != nil {
		return err
	}
	wg.Go(func() {
		if err := e.tls.Serve(ln); err != nil {
			e.log.Error("app: the engine port stopped", "err", err)
		}
	})
	defer func() { _ = e.tls.Close(transport.StatusGoingAway, "the engine is stopping") }()
	if e.tcli, err = transport.NewClient(transport.ClientConfig{CA: id.CA, Cert: e.cert, Network: e.deps.Network, Clock: e.clk, Credentials: e.creds}); err != nil {
		return err
	}
	if err := e.serveCLI(ctx, &wg); err != nil {
		return err
	}
	if err := e.respond(ctx); err != nil {
		return err
	}
	e.up.Store(true)
	wg.Go(func() { e.statusLoop(ctx) })

	next := e.initial()
	for ctx.Err() == nil {
		switch {
		case next.err != nil:
			return next.err
		case next.promote != nil:
			next = e.promote(ctx, next.promote)
		case next.to == RoleCitadel:
			next = e.runCitadel(ctx, next.take)
		default:
			next = e.runTower(ctx, next.unknown)
		}
	}
	return nil
}

// preflight reads, before the engine port serves anything, what the Guild
// database on this disk says: the refusals of the credentials, without which
// the engine does not start (a connection of a removed machine would pass
// meanwhile), and a certificate of epoch higher than the one the machine
// kept, which a stop between the promotion and the record of the identity
// left there (the machine follows it, verified).
func (e *Engine) preflight(ctx context.Context) error {
	db, err := guilddb.Open(ctx, e.paths.guildDB(), store.Options{Clock: e.clk, Synchronous: e.cfg.Synchronous, Replica: true})
	if err != nil {
		return err
	}
	defer db.Close()
	if err := e.creds.Reload(ctx, db); err != nil {
		return fmt.Errorf("app: the engine does not start without the list of refused credentials: %w", err)
	}
	cert, err := guild.GuildCertificate(ctx, db)
	switch {
	case errors.Is(err, guild.ErrNoGuild):
		return nil // a replica that did not receive its copy yet
	case err != nil:
		return err
	case cert.Epoch <= e.epochs.Epoch():
		return nil
	}
	if err := e.epochs.Follow(ctx, cert); err != nil {
		e.log.Warn("app: the Guild database holds a higher certificate of epoch that does not hold", "epoch", cert.Epoch, "err", err)
		return nil
	}
	e.log.Warn("app: following the higher certificate of epoch the Guild database holds", "epoch", cert.Epoch, "citadel", cert.CitadelMachineID)
	return nil
}

// reloadCreds reads the refusals of the credentials from db again and drops
// at once the open connections they refuse now (4002).
func (e *Engine) reloadCreds(ctx context.Context, db *store.DB) error {
	err := e.creds.Reload(ctx, db)
	if e.tls != nil {
		e.tls.CloseRevoked()
	}
	return err
}

// refusedCert is the audit of a certificate of epoch this machine refused
// (a bad signature, another Guild, not higher): on the Citadel a row of the
// audit, once per epoch, Citadel and reason; on a Tower the log has it.
func (e *Engine) refusedCert(c protocol.Certificate, reason error) {
	e.mu.Lock()
	run := e.cit
	e.mu.Unlock()
	if run == nil {
		return
	}
	go func() {
		if err := guild.RecordRefused(context.Background(), run.db, e.clk, e.id.MachineID, c, reason); err != nil {
			e.log.Warn("app: the audit of a refused certificate of epoch", "err", err)
		}
	}()
}

// initial is the role at the start: the Citadel if the certificate names this
// machine, unless it was demoted from that epoch already (it kept the
// database it discarded and waits for the certificate of the new epoch).
func (e *Engine) initial() transition {
	c := e.epochs.Certificate()
	if c.CitadelMachineID != e.id.MachineID {
		return transition{to: RoleTower}
	}
	if _, err := os.Stat(e.paths.discarded(c.Epoch)); err == nil {
		return transition{to: RoleTower, unknown: true}
	}
	return transition{to: RoleCitadel}
}

// onFollow runs before the machine keeps a higher certificate: a Citadel
// stops every external action at once and becomes a Tower
// (docs/guilda/cidadela-e-torres.md#volta-da-antiga-cidadela). It runs under
// the lock of Epochs: it only flips state and signals.
func (e *Engine) onFollow(_ context.Context, cert protocol.Certificate) error {
	e.mu.Lock()
	run, role := e.cit, e.role
	e.unknown = false
	e.mu.Unlock()
	if role == RoleCitadel && run != nil && cert.CitadelMachineID != e.id.MachineID && run.fence.Demote() {
		e.log.Warn("app: a higher epoch names another Citadel; stepping down", "epoch", cert.Epoch, "citadel", cert.CitadelMachineID)
		e.cert.Set(e.machine.Leaf(), e.machine.Key()) // mestre.local is the new Citadel's
		e.signal(transition{to: RoleTower})
	}
	e.changed()
	return nil
}

// known is what the discovery looks for.
func (e *Engine) known() discovery.Known {
	e.mu.Lock()
	unknown := e.unknown
	e.mu.Unlock()
	k := discovery.Known{Epoch: e.epochs.Epoch(), CitadelMachineID: e.epochs.Citadel()}
	if unknown || k.CitadelMachineID == e.id.MachineID {
		k.CitadelMachineID = ""
	}
	return k
}

// Status returns the status now.
func (e *Engine) Status() Status {
	if !e.up.Load() {
		return Status{Instance: e.cfg.Instance}
	}
	e.mu.Lock()
	st := Status{
		Instance: e.cfg.Instance, MachineID: e.id.MachineID, Failover: e.failover,
		Connected: e.connected, Discarded: e.discarded,
	}
	// The role is the period that runs; none while the role changes.
	switch {
	case e.cit != nil:
		st.Role = RoleCitadel
	case e.tow != nil:
		st.Role = RoleTower
	}
	unknown, run := e.unknown, e.cit
	e.mu.Unlock()
	st.Epoch, st.Citadel = e.epochs.Epoch(), e.epochs.Citadel()
	if unknown {
		st.Citadel = ""
	}
	if run != nil && st.Role == RoleCitadel {
		st.Fence, st.Acting = run.fence.State(), run.fence.Acting()
	}
	_ = e.withGuildDB(func(db *store.DB) error {
		st.LSN, _ = db.LastLSN(context.Background())
		return nil
	})
	return st
}

// changed tells OnStatus, from its own goroutine: it may be called under
// any lock.
func (e *Engine) changed() {
	select {
	case e.statusCh <- struct{}{}:
	default:
	}
}

func (e *Engine) statusLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.statusCh:
			if e.deps.OnStatus != nil {
				e.deps.OnStatus(e.Status())
			}
		}
	}
}

func (e *Engine) setConnected(on bool) {
	e.mu.Lock()
	e.connected = on
	e.mu.Unlock()
	e.changed()
}

func (e *Engine) setFailover(on bool) {
	e.mu.Lock()
	e.failover = on
	e.mu.Unlock()
	e.changed()
}

// errNoGuildDB: the role is changing, no Guild database is open.
var errNoGuildDB = errors.New("app: no Guild database open now")

// withGuildDB runs fn with the Guild database open now (the main one on the
// Citadel, the replica on a Tower); the period does not close it meanwhile.
func (e *Engine) withGuildDB(fn func(db *store.DB) error) error {
	e.dbMu.RLock()
	defer e.dbMu.RUnlock()
	if e.gdb == nil {
		return errNoGuildDB
	}
	return fn(e.gdb)
}

// setGuildDB makes db the Guild database of the period, or none (nil),
// waiting for whoever reads the one before.
func (e *Engine) setGuildDB(db *store.DB) {
	e.dbMu.Lock()
	e.gdb = db
	e.dbMu.Unlock()
}

func (e *Engine) citadelDown(since time.Time, n int) {
	e.log.Warn("app: the Citadel is away", "since", since, "notice", n)
	if e.deps.OnCitadelDown != nil {
		e.deps.OnCitadelDown(since, n)
	}
}

// serveCLI opens the local channel.
func (e *Engine) serveCLI(ctx context.Context, wg *sync.WaitGroup) error {
	if e.paths.CLISocket == "" {
		return nil
	}
	id := e.deps.Identifier
	if id == nil {
		id = nobody{}
	}
	commands := e.deps.Commands
	if commands == nil {
		commands = cli.Default()
	}
	// The effective rules kept on this machine: the cli.allow of the
	// characters, and the decision of every tool call of the hook
	// dispatcher (docs/provedores/despachante-de-hooks.md).
	rules := &classes.Store{DB: e.mdb, Clock: e.clk, Agents: e.paths.Agentes}
	profiles := e.deps.Profiles
	if profiles == nil {
		profiles = rulesProfiles{src: rules}
	}
	hk := &hook.Engine{
		Clock: e.clk, Rules: rules, Ledger: rules, Agents: e.paths.Agentes, Logger: e.log,
		Characters: e.deps.Characters, Approvals: e.deps.Approvals, States: e.deps.States,
		ClassHooks: e.deps.ClassHooks, OnEvent: e.deps.OnHookEvent,
	}
	gate := cli.NewGate(cli.GateConfig{Commands: commands, Profiles: profiles, Next: forwarder{e}, Masker: e.deps.Masker})
	srv := localchan.NewServer(localchan.Config{
		Clock: e.clk, Identifier: id, Forwarder: gate, Instance: e.cfg.Instance, EngineVersion: e.deps.Version, Logger: e.log,
		Local:      map[string]localchan.Handler{localchan.CmdHelp: gate.Help(), localchan.CmdHook: hk},
		DebugExtra: func() map[string]any { return map[string]any{"citadel": e.citadelDebug()} },
		OnAudit:    e.auditCall,
	})
	ln, err := localchan.Listen(e.paths.CLISocket, e.paths.cliAccess())
	if err != nil {
		return err
	}
	wg.Go(func() { _ = srv.Serve(ctx, ln) })
	if e.paths.HookQueue != "" {
		// What the dispatcher left while the engine was away.
		wg.Go(func() { hk.DrainLoop(ctx, e.paths.HookQueue, nil) })
	}
	return nil
}

// respond answers the broadcast of the discovery.
func (e *Engine) respond(ctx context.Context) error {
	if e.cfg.NoDiscovery || e.deps.Packets == nil {
		return nil
	}
	r, err := discovery.StartResponder(ctx, discovery.ResponderConfig{
		Net: e.deps.Packets, Port: e.cfg.DiscoveryPort, Clock: e.clk, Logger: e.log, Info: e.discoveryInfo,
	})
	if err != nil {
		return err
	}
	go func() { <-ctx.Done(); r.Close() }()
	return nil
}

func (e *Engine) discoveryInfo() (discovery.Info, bool) {
	st := e.Status()
	info := discovery.Info{
		GuildID: e.id.GuildID, MachineID: e.id.MachineID, Name: e.cfg.Name, Role: discovery.RoleTower,
		Epoch: st.Epoch, Port: e.cfg.EnginePort,
	}
	if st.Role == RoleCitadel {
		info.Role = discovery.RoleCitadel
		info.Citadel = &discovery.CitadelRef{MachineID: e.id.MachineID, Addrs: e.cfg.IPs, Port: e.cfg.EnginePort}
		return info, true
	}
	e.mu.Lock()
	run := e.tow
	e.mu.Unlock()
	if run != nil {
		if t, ok := run.locator.Current(); ok && st.Citadel != "" && t.MachineID == st.Citadel {
			if host, port, err := net.SplitHostPort(t.Addr); err == nil {
				p, _ := strconv.Atoi(port)
				info.Citadel = &discovery.CitadelRef{MachineID: t.MachineID, Addrs: []string{host}, Port: p}
			}
		}
	}
	return info, true
}

// keepOpen is the route of the Towers as each period serves it: the end of a
// period must not close the route for the next one.
type keepOpen struct{ l protocol.Listener }

func (k keepOpen) Accept(ctx context.Context) (protocol.Conn, error) { return k.l.Accept(ctx) }
func (keepOpen) Close() error                                        { return nil }

// joinMux keeps the routes of the entry on the engine port and serves them
// with the Citadel of now: 503 on a Tower and on a Citadel that is changing.
type joinMux struct {
	m guild.Mounter

	mu sync.Mutex
	h  map[string]http.Handler
}

// Handle implements guild.Mounter.
func (j *joinMux) Handle(pattern string, h http.Handler) {
	j.mu.Lock()
	_, known := j.h[pattern]
	j.h[pattern] = h
	j.mu.Unlock()
	if !known {
		j.m.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			j.mu.Lock()
			h := j.h[pattern]
			j.mu.Unlock()
			if h == nil {
				http.Error(w, "this machine is not the Citadel now", http.StatusServiceUnavailable)
				return
			}
			h.ServeHTTP(w, r)
		}))
	}
}

func (j *joinMux) clear() {
	j.mu.Lock()
	for p := range j.h {
		j.h[p] = nil
	}
	j.mu.Unlock()
}

// noOwner is the Approver until the bot exists: nobody to ask.
type noOwner struct{}

func (noOwner) Ask(context.Context, guild.ApprovalRequest) (string, error) {
	return "", errors.New("app: no channel to the Owner yet (the bot arrives with milestone 5)")
}
func (noOwner) Withdraw(context.Context, string) {}

// nobody is the Identifier of an engine that runs no terminal: every caller
// is unidentified, and only help and debug answer.
type nobody struct{}

func (nobody) Accept(net.Conn, time.Time) (localchan.Peer, error) {
	return localchan.Peer{}, errors.New("this engine runs no terminal")
}

func (nobody) Identify(context.Context, localchan.Peer, string) (localchan.Identity, error) {
	return localchan.Identity{}, &localchan.IdentifyError{Reason: "this engine runs no terminal"}
}
