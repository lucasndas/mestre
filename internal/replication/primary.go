package replication

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/privdir"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/vault"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Roster tells whether a machine is excluded from the secrets.
type Roster interface {
	IsExcluded(ctx context.Context, machineID string) (bool, error)
}

// RosterFunc adapts a function to Roster.
type RosterFunc func(ctx context.Context, machineID string) (bool, error)

// IsExcluded calls f.
func (f RosterFunc) IsExcluded(ctx context.Context, id string) (bool, error) { return f(ctx, id) }

// GuildRoster is the Roster of a guild.Citadel.
func GuildRoster(c interface {
	Machine(ctx context.Context, id string) (guild.Machine, error)
}) Roster {
	return RosterFunc(func(ctx context.Context, id string) (bool, error) {
		m, err := c.Machine(ctx, id)
		return m.Excluded, err
	})
}

// Retention is how long the Citadel keeps the rows of the log
// (docs/guilda/replicacao.md): a Tower that comes back later gets a full copy.
type Retention struct {
	MaxAge   time.Duration
	MaxBytes int64
}

func (r Retention) withDefaults() Retention {
	if r.MaxAge <= 0 {
		r.MaxAge = store.DefaultLogMaxAge
	}
	if r.MaxBytes <= 0 {
		r.MaxBytes = store.DefaultLogMaxBytes
	}
	return r
}

// PrimaryConfig wires the Citadel side.
type PrimaryConfig struct {
	// DB is the Guild database, the main one (opened with the log).
	DB    *store.DB
	Clock clock.Clock
	// Roster says which machines are excluded.
	Roster Roster
	// Vault, when set, sends the key and the envelope to the machines that are
	// not excluded.
	Vault *vault.Copier
	// Files and Cites configure the files of the Citadel; Mirrors the mirrors
	// of the workspaces. Both optional.
	Files   *Files
	Cites   Cites
	Mirrors *Mirrors
	// CitesMirrors says which mirrors a change of the database cites (a row that
	// names a commit of a workspace): the mirror is brought up to date on the
	// Tower before the record is sent.
	CitesMirrors func(c store.Change) []string
	// Dir holds the files of the full copies while they are sent. It is made
	// private.
	Dir       string
	Retention Retention
	// LagThreshold and LagWindow: a Tower over the first for more than the
	// second is an alert (5 s and 1 min).
	LagThreshold, LagWindow time.Duration
	// BatchBytes and BatchRecords bound what goes in one frame and what is read
	// from the log at once.
	BatchBytes, BatchRecords int
	// GuildConfig, when set, is sent to every connected Tower when an
	// exclusion changes, so the machine renews its certificates.
	GuildConfig func(ctx context.Context) (protocol.GuildConfig, error)
	// OnDropped, when set, is told of the receipt of a vault.drop after it was
	// written to the audit (action vault.dropped).
	OnDropped func(machineID string)
	// Gate, when set, wraps the handler of the receipt vault.dropped: the
	// Citadel that hands over refuses it (retryable), so the Tower sends it
	// to the next one, which audits it.
	Gate func(protocol.HandlerFunc) protocol.HandlerFunc
	// Paused, when set and true, holds the retention of the log back: a
	// Citadel that does not act yet (suspended at the start) prunes nothing
	// of what a demotion would report as discarded.
	Paused func() bool
	Logger *slog.Logger
}

// Primary is the replication on the Citadel.
type Primary struct {
	cfg     PrimaryConfig
	guildID protocol.ULID

	mu     sync.Mutex
	srv    *protocol.Server
	base   context.Context
	hellos map[string]helloInfo
	peers  map[string]*peer
	// early holds a replica.resync that came before the stream of its Tower
	// existed; the stream takes it up when it starts.
	early map[string]resyncRequest

	// bg are the goroutines of the Towers (the stream, the files, the
	// mirrors): Run waits for them (after canceling the peers) before it
	// returns, so none of them reads the database or writes a copy once the
	// owner of the Primary closes the database and lets go of the folder.
	bg      sync.WaitGroup
	stopped bool
}

type helloInfo struct{ lsn, epoch uint64 }

// peer is one connected Tower.
type peer struct {
	id     string
	sess   *protocol.Session
	cancel context.CancelFunc
	files  *fileSender

	resync chan resyncRequest

	mu        sync.Mutex
	excluded  bool
	acked     uint64
	ackedAt   time.Time
	overSince time.Time
	state     string
	// tips are the refs of each mirror as last sent on this connection.
	tips map[string]map[string]gitx.Hash
}

type resyncRequest struct{ from *uint64 }

// NewPrimary checks the configuration. The guild_id of the Guild database is
// the ID of the frames.
func NewPrimary(ctx context.Context, cfg PrimaryConfig) (*Primary, error) {
	if cfg.DB == nil || cfg.Roster == nil || cfg.Dir == "" {
		return nil, errors.New("replication: PrimaryConfig needs DB, Roster and Dir")
	}
	if cfg.DB.IsReplica() {
		return nil, errors.New("replication: the primary side runs on the main database, not on a replica")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	cfg.Retention = cfg.Retention.withDefaults()
	if cfg.LagThreshold <= 0 {
		cfg.LagThreshold = DefaultLagThreshold
	}
	if cfg.LagWindow <= 0 {
		cfg.LagWindow = DefaultLagWindow
	}
	if cfg.BatchBytes <= 0 {
		cfg.BatchBytes = DefaultBatchBytes
	}
	cfg.BatchBytes = min(cfg.BatchBytes, frameLimit)
	if cfg.BatchRecords <= 0 {
		cfg.BatchRecords = DefaultBatchRecords
	}
	id, _, err := readGuild(ctx, cfg.DB)
	if err != nil {
		return nil, err
	}
	gid, err := protocol.ParseULID(id)
	if err != nil {
		return nil, fmt.Errorf("replication: guild_id %q: %w", id, err)
	}
	if err := privdir.Ensure(cfg.Dir); err != nil {
		return nil, fmt.Errorf("replication: %w", err)
	}
	return &Primary{cfg: cfg, guildID: gid, hellos: map[string]helloInfo{}, peers: map[string]*peer{}, early: map[string]resyncRequest{}}, nil
}

// readGuild reads the guild_id and the epoch from the one row of the Guild.
func readGuild(ctx context.Context, db *store.DB) (id string, epoch uint64, err error) {
	err = db.Reader().QueryRowContext(ctx, `SELECT guild_id, epoch FROM guild WHERE id = 1`).Scan(&id, &epoch)
	if err != nil {
		return "", 0, fmt.Errorf("replication: reading the guild: %w", err)
	}
	return id, epoch, nil
}

// readCopyGuild reads the guild_id and the epoch from the Guild row of a
// database file that nobody has open.
func readCopyGuild(ctx context.Context, path string) (id string, epoch uint64, err error) {
	db, err := sql.Open("sqlite", path+"?_pragma=query_only(1)")
	if err != nil {
		return "", 0, fmt.Errorf("replication: reading the copy: %w", err)
	}
	defer db.Close()
	if err := db.QueryRowContext(ctx, `SELECT guild_id, epoch FROM guild WHERE id = 1`).Scan(&id, &epoch); err != nil {
		return "", 0, fmt.Errorf("replication: reading the Guild in the copy: %w", err)
	}
	return id, epoch, nil
}

// Attach tells the Primary which Server to send through.
func (p *Primary) Attach(srv *protocol.Server) {
	p.mu.Lock()
	p.srv = srv
	p.mu.Unlock()
}

// Hooks adds the hooks of the replication to base: it learns what the Tower
// has in the hello and starts the stream when the connection is ready.
func (p *Primary) Hooks(base protocol.Hooks) protocol.Hooks {
	h := base
	h.OnHello = func(ctx context.Context, peer protocol.Peer, hello *protocol.Hello) error {
		if base.OnHello != nil {
			if err := base.OnHello(ctx, peer, hello); err != nil {
				return err
			}
		}
		p.mu.Lock()
		p.hellos[peer.ID] = helloInfo{lsn: hello.ReplicaLSN, epoch: hello.ReplicaEpoch}
		p.mu.Unlock()
		return nil
	}
	h.OnReady = func(s *protocol.Session) {
		if base.OnReady != nil {
			base.OnReady(s)
		}
		p.onReady(s)
	}
	h.OnDisconnect = func(s *protocol.Session, cause error) {
		p.onDisconnect(s)
		if base.OnDisconnect != nil {
			base.OnDisconnect(s, cause)
		}
	}
	return h
}

// Register adds the handlers of the messages a Tower sends: replica.ack,
// replica.resync and the receipt vault.dropped.
func (p *Primary) Register(r *protocol.Router) {
	r.HandleFunc(protocol.TypeReplicaAck, p.handleAck)
	r.HandleFunc(protocol.TypeReplicaResync, p.handleResync)
	dropped := protocol.HandlerFunc(p.handleDropped)
	if p.cfg.Gate != nil {
		dropped = p.cfg.Gate(dropped)
	}
	r.Handle(protocol.TypeVaultDropped, dropped)
}

func (p *Primary) baseCtx() context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.base == nil {
		return context.Background()
	}
	return p.base
}

func (p *Primary) server() *protocol.Server {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.srv
}

// Run applies the retention of the log and watches the lag until ctx ends.
func (p *Primary) Run(ctx context.Context) error {
	defer p.waitBackground()
	p.mu.Lock()
	p.base = ctx
	p.mu.Unlock()
	prune := p.cfg.Clock.NewTicker(PruneEvery)
	defer prune.Stop()
	lag := p.cfg.Clock.NewTicker(max(p.cfg.LagThreshold, time.Second))
	defer lag.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-prune.C():
			if p.cfg.Paused != nil && p.cfg.Paused() {
				continue
			}
			if _, err := p.cfg.DB.PruneLog(ctx, p.cfg.Retention.MaxAge, p.cfg.Retention.MaxBytes); err != nil && ctx.Err() == nil {
				p.cfg.Logger.Warn("replication: pruning the log", "err", err)
			}
		case <-lag.C():
			_, _ = p.LagAlerts(ctx)
		}
	}
}

// background runs f in a goroutine that Run waits for when it ends; after that
// it runs nothing.
func (p *Primary) background(f func()) {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.bg.Add(1)
	p.mu.Unlock()
	go func() {
		defer p.bg.Done()
		f()
	}()
}

// waitBackground cancels the streams of the Towers and waits for the
// goroutines of the Primary.
func (p *Primary) waitBackground() {
	p.mu.Lock()
	p.stopped = true
	for _, pe := range p.peers {
		pe.cancel()
	}
	p.mu.Unlock()
	p.bg.Wait()
}

func (p *Primary) onReady(s *protocol.Session) {
	id := s.Peer().ID
	p.mu.Lock()
	hello := p.hellos[id]
	if old := p.peers[id]; old != nil {
		old.cancel()
	}
	base := p.base
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	pe := &peer{
		id: id, sess: s, cancel: cancel, files: nil, resync: make(chan resyncRequest, 1),
		state: "starting", ackedAt: p.cfg.Clock.Now(),
	}
	if p.cfg.Files != nil {
		pe.files = newFileSender(p.cfg.Files, p.cfg.Logger)
	}
	p.peers[id] = pe
	if req, ok := p.early[id]; ok {
		delete(p.early, id)
		pe.resync <- req
	}
	p.mu.Unlock()
	p.background(func() {
		select {
		case <-s.Done():
		case <-ctx.Done():
		}
		cancel()
	})
	p.background(func() {
		err := p.serve(ctx, pe, hello)
		if err != nil && ctx.Err() == nil {
			p.cfg.Logger.Error("replication: the stream to a Tower failed; closing the connection", "machine", id, "err", err)
			s.Close(1011, "replication failed")
		}
	})
}

func (p *Primary) onDisconnect(s *protocol.Session) {
	id := s.Peer().ID
	p.mu.Lock()
	defer p.mu.Unlock()
	if pe := p.peers[id]; pe != nil && pe.sess == s {
		pe.cancel()
		delete(p.peers, id)
		delete(p.early, id)
	}
}

func (p *Primary) peer(id string) *peer {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peers[id]
}

func (p *Primary) handleAck(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
	a := m.Payload.(*protocol.ReplicaAck)
	pe := p.peer(m.Peer.ID)
	if pe == nil {
		return nil, nil
	}
	if last, err := p.cfg.DB.LastLSN(ctx); err == nil && a.LSN > last {
		p.cfg.Logger.Warn("replication: an ack above the log", "machine", m.Peer.ID, "lsn", a.LSN, "last", last)
		return nil, nil
	}
	pe.mu.Lock()
	if a.LSN > pe.acked {
		pe.acked = a.LSN
	}
	pe.ackedAt = p.cfg.Clock.Now()
	pe.mu.Unlock()
	return nil, nil
}

func (p *Primary) handleResync(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
	r := m.Payload.(*protocol.ReplicaResync)
	req := resyncRequest{from: r.FromLSN}
	p.mu.Lock()
	pe := p.peers[m.Peer.ID]
	if pe == nil {
		// Not started yet (or already gone): kept for the stream that starts, and
		// the Tower asks again if it never comes.
		p.early[m.Peer.ID] = req
		p.mu.Unlock()
		return nil, nil
	}
	p.mu.Unlock()
	if req.from == nil && pe.currentState() == "snapshot" {
		return nil, nil // a full copy is on its way already
	}
	select { // the newest request wins
	case <-pe.resync:
	default:
	}
	pe.resync <- req
	return nil, nil
}

// handleDropped takes the receipt of vault.drop: from a machine that is
// excluded, it goes in the audit of the Guild (action vault.dropped, no value
// of the vault anywhere in it) and to OnDropped. A receipt from any other
// machine is ignored.
func (p *Primary) handleDropped(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
	id := m.Peer.ID
	if excluded, err := p.cfg.Roster.IsExcluded(ctx, id); err != nil || !excluded {
		p.cfg.Logger.Warn("replication: a receipt of vault.drop from a machine that is not excluded", "machine", id, "err", err)
		return nil, nil
	}
	_, err := p.cfg.DB.Writer().ExecContext(ctx,
		`INSERT INTO audit_log (id, at, machine_id, action, detail) VALUES (?, ?, ?, ?, '{}')`,
		protocol.NewIDGen(p.cfg.Clock).NewString(), p.cfg.Clock.Now().UnixMilli(), id, AuditVaultDropped)
	if err != nil {
		p.cfg.Logger.Error("replication: auditing the receipt of vault.drop", "machine", id, "err", err)
	}
	if p.cfg.OnDropped != nil {
		p.cfg.OnDropped(id)
	}
	return nil, nil
}

// AuditVaultDropped is the action of the audit row written when an excluded
// machine confirms it erased the vault.
const AuditVaultDropped = "vault.dropped"

// ---- one Tower ----

func (pe *peer) isExcluded() bool {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	return pe.excluded
}

func (pe *peer) currentState() string {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	return pe.state
}

func (pe *peer) setState(s string) {
	pe.mu.Lock()
	pe.state = s
	pe.mu.Unlock()
}

func (pe *peer) setAcked(lsn uint64, now time.Time) {
	pe.mu.Lock()
	pe.acked, pe.ackedAt = lsn, now
	pe.mu.Unlock()
}

// serve is the life of the stream to one Tower: what it needs besides the
// records (the vault, the files), the choice between continuing and a full
// copy, and then the records, as they are committed.
func (p *Primary) serve(ctx context.Context, pe *peer, hello helloInfo) error {
	srv := p.server()
	if srv == nil {
		return errors.New("replication: no Server attached")
	}
	link := srv.Link(pe.id)
	excluded, err := p.cfg.Roster.IsExcluded(ctx, pe.id)
	if err != nil {
		return fmt.Errorf("replication: is %s excluded: %w", pe.id, err)
	}
	pe.mu.Lock()
	pe.excluded = excluded
	pe.mu.Unlock()

	p.sendVault(ctx, link, excluded)
	if err := p.syncFiles(ctx, link, pe); err != nil {
		return err
	}
	if err := p.syncMirrors(ctx, link, pe); err != nil {
		if ctx.Err() != nil {
			return err
		}
		// the mirrors that failed are told; the Tower still gets the database
		p.cfg.Logger.Warn("replication: a mirror was not sent", "machine", pe.id, "err", err)
	}

	last, err := p.start(ctx, link, pe, hello)
	if err != nil {
		return err
	}
	pe.setAcked(last, p.cfg.Clock.Now())
	pe.setState("streaming")
	return p.stream(ctx, link, pe, last)
}

// start chooses where the stream begins: right after what the Tower has, if
// the log still holds it and the epoch is the same, otherwise after a full
// copy.
func (p *Primary) start(ctx context.Context, link *protocol.Link, pe *peer, hello helloInfo) (uint64, error) {
	_, epoch, err := readGuild(ctx, p.cfg.DB)
	if err != nil {
		return 0, err
	}
	lo, hi, err := p.cfg.DB.LogBounds(ctx)
	if err != nil {
		return 0, err
	}
	if lo > 0 && hello.lsn > 0 && hello.epoch == epoch && hello.lsn >= lo-1 && hello.lsn <= hi {
		return hello.lsn, nil
	}
	p.cfg.Logger.Info("replication: full copy", "machine", pe.id, "replica_lsn", hello.lsn, "replica_epoch", hello.epoch, "epoch", epoch, "log", fmt.Sprintf("%d..%d", lo, hi))
	return p.snapshot(ctx, link, pe)
}

// snapshot sends a full copy and returns its lsn.
func (p *Primary) snapshot(ctx context.Context, link *protocol.Link, pe *peer) (uint64, error) {
	pe.setState("snapshot")
	excluded := pe.isExcluded()
	path := filepath.Join(p.cfg.Dir, "snapshot-"+protocol.NewIDGen(p.cfg.Clock).NewString()+".db")
	var tables []string
	if excluded {
		tables = VaultTables
	}
	lsn, err := p.cfg.DB.Snapshot(ctx, path, store.SnapshotOptions{EmptyTables: tables})
	if err != nil {
		return 0, err
	}
	defer removeAll(path)
	// The epoch announced is the one the copy holds, not the one read before it
	// was made: a promotion in between would announce the wrong one.
	_, epoch, err := readCopyGuild(ctx, path)
	if err != nil {
		return 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("replication: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("replication: %w", err)
	}
	meta, _ := json.Marshal(snapshotMeta{LSN: lsn, Epoch: epoch})
	var obj protocol.Object
	_ = json.Unmarshal(meta, &obj)
	if err := link.Transfer(ctx, protocol.Transfer{Kind: protocol.KindDBSnapshot, Meta: obj, Source: protocol.Source{ReaderAt: f, Size: fi.Size()}}); err != nil {
		return 0, fmt.Errorf("replication: sending the full copy: %w", err)
	}
	return lsn, nil
}

type snapshotMeta struct {
	LSN   uint64 `json:"lsn"`
	Epoch uint64 `json:"epoch"`
}

func removeAll(path string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(path + suffix)
	}
}

type errTooBig struct{ lsn uint64 }

func (e errTooBig) Error() string {
	return fmt.Sprintf("record %d does not fit in a frame", e.lsn)
}

// stream ships the records after last until ctx ends.
func (p *Primary) stream(ctx context.Context, link *protocol.Link, pe *peer, last uint64) error {
	for {
		if req, ok := takeResync(pe); ok {
			var err error
			if last, err = p.resync(ctx, link, pe, req); err != nil {
				return err
			}
		}
		watch := p.cfg.DB.LogWatch()
		rows, err := p.cfg.DB.LogSince(ctx, last, p.cfg.BatchRecords, 4*p.cfg.BatchBytes)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			select {
			case <-watch:
			case req := <-pe.resync:
				select { // handled at the top of the loop
				case pe.resync <- req:
				default:
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}
		if rows[0].LSN != last+1 { // the log no longer holds what comes next
			if last, err = p.snapshot(ctx, link, pe); err != nil {
				return err
			}
			pe.setAcked(last, p.cfg.Clock.Now())
			pe.setState("streaming")
			continue
		}
		sent, err := p.ship(ctx, link, pe, rows)
		var big errTooBig
		if errors.As(err, &big) {
			p.cfg.Logger.Info("replication: a record over a frame goes by full copy", "machine", pe.id, "lsn", big.lsn)
			if last, err = p.snapshot(ctx, link, pe); err != nil {
				return err
			}
			pe.setAcked(last, p.cfg.Clock.Now())
			pe.setState("streaming")
			continue
		}
		if err != nil {
			return err
		}
		last = sent
	}
}

func takeResync(pe *peer) (resyncRequest, bool) {
	select {
	case r := <-pe.resync:
		return r, true
	default:
		return resyncRequest{}, false
	}
}

// resync answers replica.resync: from the lsn asked, if the log holds it, or
// a full copy.
func (p *Primary) resync(ctx context.Context, link *protocol.Link, pe *peer, req resyncRequest) (uint64, error) {
	if req.from != nil {
		lo, hi, err := p.cfg.DB.LogBounds(ctx)
		if err != nil {
			return 0, err
		}
		if lo > 0 && *req.from >= lo-1 && *req.from <= hi {
			p.cfg.Logger.Info("replication: resending", "machine", pe.id, "from", *req.from)
			return *req.from, nil
		}
	}
	last, err := p.snapshot(ctx, link, pe)
	if err != nil {
		return 0, err
	}
	pe.setAcked(last, p.cfg.Clock.Now())
	pe.setState("streaming")
	return last, nil
}

// ship sends the rows in frames. A file that a record cites goes before the
// record: the frames already built are flushed first, the transfer finishes,
// and only then does the record go. It returns the lsn of the last record
// sent.
func (p *Primary) ship(ctx context.Context, link *protocol.Link, pe *peer, rows []store.LogRow) (uint64, error) {
	excluded := pe.isExcluded()
	vaultTables := tableSet(VaultTables)
	var buf []byte
	var first uint64
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		err := link.SendData(ctx, protocol.DataFrame{Type: protocol.DataReplica, ID: p.guildID, Pos: first, Data: buf})
		buf = nil
		return err
	}
	var sent uint64
	syncedMirrors := map[string]bool{}
	for _, r := range rows {
		row := r
		if excluded {
			var err error
			if row, err = strip(r, vaultTables); err != nil {
				return sent, err
			}
		}
		if p.cfg.Cites != nil || p.cfg.CitesMirrors != nil {
			files, mirrors, err := p.cited(row)
			if err != nil {
				return sent, err
			}
			// a mirror is brought up to date once per batch: it holds every commit the
			// rows of the batch cite (the rows are written after the commits arrive)
			mirrors = slices.DeleteFunc(mirrors, func(id string) bool {
				seen := syncedMirrors[id]
				syncedMirrors[id] = true
				return seen
			})
			if len(files)+len(mirrors) > 0 {
				if err := flush(); err != nil {
					return sent, err
				}
				for _, ref := range files {
					if pe.files == nil {
						break
					}
					if err := pe.files.ensure(ctx, link, excluded, ref); err != nil {
						if skip := p.skippable(ctx, link, "file", ref.Root+"/"+ref.Path, err); skip != nil {
							return sent, skip
						}
					}
				}
				for _, id := range mirrors {
					if p.cfg.Mirrors == nil {
						break
					}
					if err := p.cfg.Mirrors.sync(ctx, link, pe, id); err != nil {
						if skip := p.skippable(ctx, link, "mirror", id, err); skip != nil {
							return sent, skip
						}
					}
				}
			}
		}
		size := recordSize(row)
		if size > frameLimit {
			return sent, errTooBig{lsn: row.LSN}
		}
		if len(buf) > 0 && len(buf)+size > p.cfg.BatchBytes {
			if err := flush(); err != nil {
				return sent, err
			}
		}
		if len(buf) == 0 {
			first = row.LSN
		}
		buf = appendRecord(buf, row)
		sent = row.LSN
	}
	return sent, flush()
}

// skippable decides what a failure to send a cited file or mirror does: it is
// logged and the record goes on (as the sending of every mirror and folder does),
// unless the context ended or the connection is gone, which stops the batch to
// be sent again.
func (p *Primary) skippable(ctx context.Context, link *protocol.Link, what, name string, err error) error {
	if ctx.Err() != nil || !link.Connected() {
		return err
	}
	p.cfg.Logger.Warn("replication: a cited "+what+" was not sent, the record goes on", "name", name, "err", err)
	return nil
}

// cited lists the files and the mirrors a record cites.
func (p *Primary) cited(r store.LogRow) (files []FileRef, mirrors []string, err error) {
	cs, err := store.DecodeChanges(r.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("record %d: %w", r.LSN, err)
	}
	for _, c := range cs {
		if p.cfg.Cites != nil {
			files = append(files, p.cfg.Cites(c)...)
		}
		if p.cfg.CitesMirrors != nil {
			mirrors = append(mirrors, p.cfg.CitesMirrors(c)...)
		}
	}
	return files, mirrors, nil
}

// ---- files and mirrors ----

// syncFiles sends every file of the folders marked Walk that the Tower does
// not have yet, before the first record: a Tower that connects finds the
// folders as the Citadel has them.
func (p *Primary) syncFiles(ctx context.Context, link *protocol.Link, pe *peer) error {
	if p.cfg.Files == nil {
		return nil
	}
	excluded := pe.isExcluded()
	for _, name := range p.cfg.Files.order {
		root := p.cfg.Files.roots[name]
		if !root.Walk || (excluded && root.NotForExcluded) {
			continue
		}
		refs, err := p.cfg.Files.walk(name)
		if err != nil {
			return fmt.Errorf("replication: listing %s: %w", name, err)
		}
		for _, ref := range refs {
			if err := pe.files.ensure(ctx, link, excluded, ref); err != nil {
				return err
			}
		}
	}
	return nil
}

// FileChanged tells the replication a file of one of the folders is new or
// changed: it goes to every connected Tower, in the background. The records
// that cite a file do not need this; it is for the files nobody cites.
func (p *Primary) FileChanged(root, rel string) {
	srv := p.server()
	if srv == nil {
		return
	}
	p.mu.Lock()
	peers := make([]*peer, 0, len(p.peers))
	for _, pe := range p.peers {
		peers = append(peers, pe)
	}
	p.mu.Unlock()
	ctx := p.baseCtx()
	for _, pe := range peers {
		if pe.files == nil {
			continue
		}
		p.background(func() {
			if err := pe.files.ensure(ctx, srv.Link(pe.id), pe.isExcluded(), FileRef{Root: root, Path: rel}); err != nil && ctx.Err() == nil {
				p.cfg.Logger.Warn("replication: sending a file", "machine", pe.id, "file", root+"/"+rel, "err", err)
			}
		})
	}
}

// ErrNoTower is returned by SendFile for a machine that is not connected.
var ErrNoTower = errors.New("replication: the machine is not connected")

// SendFile sends one file of the folders to a Tower now and returns when the
// Tower has it (or already had it): for a command that needs the file there
// before it goes on, as the copy of an attachment into a table of the Tower.
// FileChanged is for the files nobody waits for.
func (p *Primary) SendFile(ctx context.Context, machine string, ref FileRef) error {
	srv := p.server()
	p.mu.Lock()
	pe := p.peers[machine]
	p.mu.Unlock()
	if srv == nil || pe == nil || pe.files == nil {
		return fmt.Errorf("%w: %s", ErrNoTower, machine)
	}
	link := srv.Link(machine)
	if !link.Connected() {
		return fmt.Errorf("%w: %s", ErrNoTower, machine)
	}
	return pe.files.ensure(ctx, link, pe.isExcluded(), ref)
}

func (p *Primary) syncMirrors(ctx context.Context, link *protocol.Link, pe *peer) error {
	if p.cfg.Mirrors == nil {
		return nil
	}
	return p.cfg.Mirrors.syncAll(ctx, link, pe)
}

// MirrorChanged tells the replication a ref of a mirror moved: the bundle goes
// to every connected Tower, in the background.
func (p *Primary) MirrorChanged(id string) {
	srv := p.server()
	if srv == nil || p.cfg.Mirrors == nil {
		return
	}
	p.mu.Lock()
	peers := make([]*peer, 0, len(p.peers))
	for _, pe := range p.peers {
		peers = append(peers, pe)
	}
	p.mu.Unlock()
	ctx := p.baseCtx()
	for _, pe := range peers {
		p.background(func() {
			if err := p.cfg.Mirrors.sync(ctx, srv.Link(pe.id), pe, id); err != nil && ctx.Err() == nil {
				p.cfg.Logger.Warn("replication: sending a mirror", "machine", pe.id, "mirror", id, "err", err)
			}
		})
	}
}

// ---- the vault and the exclusion ----

// sendVault sends the key and the envelope to a machine that is not excluded,
// or tells an excluded one to erase them. It does not wait: the types are
// volatile and go again on the next connection, which is when it matters.
func (p *Primary) sendVault(ctx context.Context, link *protocol.Link, excluded bool) {
	if excluded {
		if _, err := link.Send(ctx, protocol.Outgoing{Type: protocol.TypeVaultDrop, Payload: protocol.Empty{}}); err != nil {
			p.cfg.Logger.Warn("replication: vault.drop", "err", err)
		}
		return
	}
	if p.cfg.Vault == nil {
		return
	}
	cp, err := p.cfg.Vault.Export(ctx)
	if err != nil {
		p.cfg.Logger.Error("replication: reading the vault copy", "err", err)
		return
	}
	payload := protocol.VaultReplica{Keyring: protocol.Secret(base64.StdEncoding.EncodeToString(cp.Keyring)), Envelope: cp.Envelope}
	clear(cp.Keyring)
	if _, err := link.Send(ctx, protocol.Outgoing{Type: protocol.TypeVaultReplica, Payload: payload}); err != nil {
		p.cfg.Logger.Warn("replication: vault.replica", "err", err)
	}
}

// ExclusionChanged is the hook of the Guild (guild.Hooks.ExclusionChanged): an
// excluded machine gets vault.drop and, from now on, the records without the
// vault tables; one whose exclusion was undone gets the vault again, which
// makes its replica ask for a full copy that has the vault tables. Every
// connected Tower gets guild.config, so the machine renews its certificates.
func (p *Primary) ExclusionChanged(m guild.Machine) {
	srv := p.server()
	if srv == nil {
		return
	}
	ctx := p.baseCtx()
	if pe := p.peer(m.ID); pe != nil {
		pe.mu.Lock()
		pe.excluded = m.Excluded
		pe.mu.Unlock()
		p.sendVault(ctx, srv.Link(m.ID), m.Excluded)
	}
	if p.cfg.GuildConfig == nil {
		return
	}
	cfg, err := p.cfg.GuildConfig(ctx)
	if err != nil {
		p.cfg.Logger.Warn("replication: guild.config", "err", err)
		return
	}
	p.mu.Lock()
	ids := make([]string, 0, len(p.peers))
	for id := range p.peers {
		ids = append(ids, id)
	}
	p.mu.Unlock()
	for _, id := range ids {
		if _, err := srv.Link(id).Send(ctx, protocol.Outgoing{Type: protocol.TypeGuildConfig, Payload: cfg}); err != nil {
			p.cfg.Logger.Warn("replication: guild.config", "machine", id, "err", err)
		}
	}
}

// VaultChanged sends the vault again to every connected machine that is not
// excluded (after the Owner changes the key).
func (p *Primary) VaultChanged(ctx context.Context) {
	srv := p.server()
	if srv == nil {
		return
	}
	p.mu.Lock()
	peers := make([]*peer, 0, len(p.peers))
	for _, pe := range p.peers {
		peers = append(peers, pe)
	}
	p.mu.Unlock()
	for _, pe := range peers {
		if !pe.isExcluded() {
			p.sendVault(ctx, srv.Link(pe.id), false)
		}
	}
}

// ---- lag ----

// PeerStatus is what the Citadel knows about the replica of one Tower.
type PeerStatus struct {
	Machine string
	// State is starting, snapshot or streaming.
	State string
	// Acked is the last lsn the Tower confirmed, and Behind how many records
	// the log has beyond it.
	Acked, Behind uint64
	// Lag is how long the oldest record the Tower has not confirmed has been
	// committed (0 when it is up to date).
	Lag time.Duration
}

// Status lists the connected Towers.
func (p *Primary) Status(ctx context.Context) ([]PeerStatus, error) {
	last, err := p.cfg.DB.LastLSN(ctx)
	if err != nil {
		return nil, err
	}
	now := p.cfg.Clock.Now()
	p.mu.Lock()
	peers := make([]*peer, 0, len(p.peers))
	for _, pe := range p.peers {
		peers = append(peers, pe)
	}
	p.mu.Unlock()
	out := make([]PeerStatus, 0, len(peers))
	for _, pe := range peers {
		pe.mu.Lock()
		st := PeerStatus{Machine: pe.id, State: pe.state, Acked: pe.acked}
		since := pe.ackedAt
		pe.mu.Unlock()
		if st.Acked < last {
			st.Behind = last - st.Acked
			at, ok, err := p.cfg.DB.LogTime(ctx, st.Acked+1)
			if err != nil {
				return nil, err
			}
			if ok {
				since = time.UnixMilli(at)
			}
			st.Lag = max(0, now.Sub(since))
		}
		out = append(out, st)
	}
	return out, nil
}

// LagAlert is a Tower whose replica is late for too long.
type LagAlert struct {
	Machine string
	Lag     time.Duration
	// Since is when the lag went over the threshold.
	Since time.Time
}

// LagAlerts evaluates the lag of every connected Tower: one over the threshold
// (5 s) for the whole window (1 min) is an alert of the checklist
// (docs/plataforma/checklist.md). Calling it is how the time over the
// threshold is counted, so Run calls it every few seconds.
func (p *Primary) LagAlerts(ctx context.Context) ([]LagAlert, error) {
	st, err := p.Status(ctx)
	if err != nil {
		return nil, err
	}
	now := p.cfg.Clock.Now()
	var alerts []LagAlert
	for _, s := range st {
		pe := p.peer(s.Machine)
		if pe == nil {
			continue
		}
		pe.mu.Lock()
		if s.Lag <= p.cfg.LagThreshold {
			pe.overSince = time.Time{}
		} else {
			if pe.overSince.IsZero() {
				pe.overSince = now
			}
			if now.Sub(pe.overSince) >= p.cfg.LagWindow {
				alerts = append(alerts, LagAlert{Machine: s.Machine, Lag: s.Lag, Since: pe.overSince})
			}
		}
		pe.mu.Unlock()
	}
	return alerts, nil
}
