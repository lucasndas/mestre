package guild

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// What a request may carry (docs/guilda/guilda.md#regras-e-erros): the route is
// open and the record of the request goes to every Tower, so none of it is as big
// as the body the route reads. A certificate request of the Mestre takes about
// 140 bytes (a public key and no name), and a sheet a few KiB.
const (
	maxCSR   = 1 << 10
	maxSheet = 16 << 10
)

// Defaults of the entry (docs/guilda/guilda.md#regras-e-erros).
const (
	// JoinValidity is how long a request lives and how long the bundle of an
	// accepted one can be fetched again.
	JoinValidity = time.Hour
	// RevealWithin is how long a request may wait for its nonce: the machine
	// reveals it right after the request, so one that does not was not made
	// to be answered. It expires then, frees its place among the pending
	// ones and counts toward the alert of the refusals.
	RevealWithin = 90 * time.Second
	// DefaultMaxPending is how many requests may wait for the Owner at once.
	DefaultMaxPending = 5
	// DefaultMaxPerIP is how many requests one address may make per hour.
	DefaultMaxPerIP = 10
	// DefaultRefusalAlert is how many refusals in 24 hours warn the Owner.
	DefaultRefusalAlert = 20
	// maxJoinName bounds the name of a machine.
	maxJoinName = 64
)

// Limits are the limits of the entry; the zero value of each is its default.
type Limits struct {
	MaxPending   int
	MaxPerIPHour int
	// RefusalAlert is the number of refusals in 24 hours that warns the Owner;
	// the requests that never revealed their nonce count as refusals.
	RefusalAlert int
}

func (l Limits) withDefaults() Limits {
	if l.MaxPending <= 0 {
		l.MaxPending = DefaultMaxPending
	}
	if l.MaxPerIPHour <= 0 {
		l.MaxPerIPHour = DefaultMaxPerIP
	}
	if l.RefusalAlert <= 0 {
		l.RefusalAlert = DefaultRefusalAlert
	}
	return l
}

// CitadelConfig wires a Citadel.
type CitadelConfig struct {
	// DB is the Guild database, the main one.
	DB *store.DB
	// CA is the Guild CA with its key loaded (LoadCA).
	CA    *transport.CA
	Clock clock.Clock
	// Cert is the engine certificate this Citadel serves: its fingerprint
	// enters the join code, so it is the one the new machine sees in TLS.
	Cert     *transport.CertSource
	Approver Approver
	// Credentials is the revocation list the TLS checks; the Citadel adds to
	// it when it revokes. Nil makes a new one from the database.
	Credentials *Credentials
	// Peers reaches the machines that are connected; nil means none is (a
	// removal then revokes without telling the machine).
	Peers  Peers
	Hooks  Hooks
	Limits Limits
	// Suspended, when set and true, makes /join answer 503 (a suspended
	// Citadel takes no entries, docs/guilda/cidadela-e-torres.md#cidadela-suspensa).
	Suspended func() bool
	// Admit, when set, is asked before a write that must not reach a Citadel
	// that hands over (the decision on an entry, its own certificate): it
	// returns the function that ends the write, or false to refuse it with
	// ErrSuspended.
	Admit func() (leave func(), ok bool)
	// CheckSheet, when set, judges the sheet of an entry request (it is
	// JSON): an error refuses the request as ErrBadRequest. It lives outside
	// this package because the sheet type does, in a package that imports this.
	CheckSheet func(sheet json.RawMessage) error
	Rand       io.Reader    // nil means crypto/rand
	Logger     *slog.Logger // nil discards
}

// Citadel is the side of the Guild that admits and removes machines: it
// receives the entry requests, asks the Owner, issues the certificates, keeps
// the registry of the machines and of their credentials, and renews, excludes
// and removes. Only the Citadel of the current epoch runs one.
type Citadel struct {
	cfg     CitadelConfig
	db      *store.DB
	clk     clock.Clock
	ids     *protocol.IDGen
	guildID string
	creds   *Credentials

	mu      sync.Mutex
	changed map[string]*watcher      // closed when a request changes
	asking  map[string]chan struct{} // the reveals asking the Owner now, by request
}

// NewCitadel checks cfg against the Guild database.
func NewCitadel(ctx context.Context, cfg CitadelConfig) (*Citadel, error) {
	if cfg.DB == nil || cfg.CA == nil || cfg.Cert == nil || cfg.Approver == nil {
		return nil, errors.New("guild: CitadelConfig needs DB, CA, Cert and Approver")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	cfg.Limits = cfg.Limits.withDefaults()
	g, err := readGuild(ctx, cfg.DB.Reader())
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(g.CA, cfg.CA.DER()) {
		return nil, errors.New("guild: the CA is not the CA of this Guild")
	}
	creds := cfg.Credentials
	if creds == nil {
		if creds, err = LoadCredentials(ctx, cfg.DB); err != nil {
			return nil, err
		}
	}
	return &Citadel{
		cfg: cfg, db: cfg.DB, clk: cfg.Clock, ids: protocol.NewIDGen(cfg.Clock), guildID: g.GuildID, creds: creds,
		changed: map[string]*watcher{}, asking: map[string]chan struct{}{},
	}, nil
}

// GuildID is the id of the Guild.
func (c *Citadel) GuildID() string { return c.guildID }

// Credentials is the revocation list this Citadel keeps up to date: give it
// to the transport server.
func (c *Citadel) Credentials() *Credentials { return c.creds }

// joinRow is a row of join_requests.
type joinRow struct {
	ID          string
	State       JoinState
	RemoteIP    string
	Name        string
	Req         JoinRequest
	Commitment  []byte
	NonceC      []byte
	NonceN      []byte
	Fingerprint [32]byte
	Code        string
	ApprovalID  string
	MachineID   string
	Bundle      string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	DecidedAt   time.Time
}

const joinColumns = `join_id, state, remote_ip, name, request, csr_motor, csr_guard, commitment, nonce_c, nonce_n, fingerprint,
	code, approval_id, machine_id, bundle, created_at, expires_at, decided_at`

func scanJoin(r scanner) (joinRow, error) {
	var (
		j                          joinRow
		state, req                 string
		fp, csrMotor, csrGuard     []byte
		nonceN                     []byte
		code, approval, mid, bndle sql.NullString
		created, expires           int64
		decided                    sql.NullInt64
	)
	if err := r.Scan(&j.ID, &state, &j.RemoteIP, &j.Name, &req, &csrMotor, &csrGuard, &j.Commitment, &j.NonceC,
		&nonceN, &fp, &code, &approval, &mid, &bndle, &created, &expires, &decided); err != nil {
		return joinRow{}, err
	}
	if err := json.Unmarshal([]byte(req), &j.Req); err != nil {
		return joinRow{}, fmt.Errorf("guild: join request %s: %w", j.ID, err)
	}
	j.Req.CSRMotor, j.Req.CSRGuardian, j.Req.Commitment = csrMotor, csrGuard, j.Commitment
	j.State, j.NonceN, j.Code, j.ApprovalID, j.MachineID, j.Bundle = JoinState(state), nonceN, code.String, approval.String, mid.String, bndle.String
	copy(j.Fingerprint[:], fp)
	j.CreatedAt, j.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC()
	if decided.Valid {
		j.DecidedAt = time.UnixMilli(decided.Int64).UTC()
	}
	return j, nil
}

func loadJoin(ctx context.Context, q querier, id string) (joinRow, error) {
	j, err := scanJoin(q.QueryRowContext(ctx, `SELECT `+joinColumns+` FROM join_requests WHERE join_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return joinRow{}, ErrJoinNotFound
	}
	return j, err
}

// enter asks Admit; without one every write is admitted.
func (c *Citadel) enter() (func(), bool) {
	if c.cfg.Admit == nil {
		return func() {}, true
	}
	return c.cfg.Admit()
}

// Begin takes the first step of an entry: the request of a new machine,
// from the address remoteIP. It refuses beyond the limits without asking the
// Owner (5 pending at once and 10 requests per address per hour, by
// default).
func (c *Citadel) Begin(ctx context.Context, req JoinRequest, remoteIP string) (JoinTicket, error) {
	if c.cfg.Suspended != nil && c.cfg.Suspended() {
		return JoinTicket{}, ErrSuspended
	}
	name, err := c.checkRequest(&req)
	if err != nil {
		return JoinTicket{}, err
	}
	nonceC := make([]byte, NonceSize)
	if _, err := io.ReadFull(c.cfg.Rand, nonceC); err != nil {
		return JoinTicket{}, fmt.Errorf("guild: draw a nonce: %w", err)
	}
	now := c.clk.Now()
	t := JoinTicket{JoinID: c.ids.NewString(), NonceC: nonceC, ExpiresAt: now.Add(JoinValidity).UTC()}
	fp := Fingerprint(c.cfg.Cert.Leaf().Raw)
	body := req
	body.Name = name                                                 // the clean one, as the question to the Owner has it
	body.CSRMotor, body.CSRGuardian, body.Commitment = nil, nil, nil // kept in their own columns
	reqJSON, _ := json.Marshal(body)                                 // a JoinRequest always encodes

	tx, err := c.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return JoinTicket{}, fmt.Errorf("guild: begin a join: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var perIP, pending int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM join_requests WHERE remote_ip = ? AND created_at > ?`,
		remoteIP, now.Add(-time.Hour).UnixMilli()).Scan(&perIP); err != nil {
		return JoinTicket{}, fmt.Errorf("guild: count the requests: %w", err)
	}
	if perIP >= c.cfg.Limits.MaxPerIPHour {
		return JoinTicket{}, ErrRateLimited
	}
	// a request that did not reveal its nonce in time holds no place
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM join_requests WHERE state = 'pending' AND expires_at > ? AND (code IS NOT NULL OR created_at > ?)`,
		now.UnixMilli(), now.Add(-RevealWithin).UnixMilli()).Scan(&pending); err != nil {
		return JoinTicket{}, fmt.Errorf("guild: count the requests: %w", err)
	}
	if pending >= c.cfg.Limits.MaxPending {
		return JoinTicket{}, ErrTooManyPending
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO join_requests (join_id, state, remote_ip, name, request, csr_motor, csr_guard, commitment, nonce_c, fingerprint, created_at, expires_at)
		 VALUES (?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.JoinID, remoteIP, name, string(reqJSON), req.CSRMotor, req.CSRGuardian, req.Commitment, nonceC, fp[:],
		now.UnixMilli(), t.ExpiresAt.UnixMilli()); err != nil {
		return JoinTicket{}, fmt.Errorf("guild: record a join request: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return JoinTicket{}, fmt.Errorf("guild: record a join request: %w", err)
	}
	return t, nil
}

// checkRequest validates the request and returns the clean name. The route is
// open (outside mTLS), and what it takes is recorded in a table that every Tower
// replicates, so what goes on is what was checked or bounded: the system reaches
// the question to the Owner and the instance the registry, and neither may be text
// of the requester's choice; the sheet is an object of a few KiB, the way the
// registry reads it; the certificate requests are small and P-256.
func (c *Citadel) checkRequest(req *JoinRequest) (string, error) {
	if len(req.Commitment) != 32 {
		return "", fmt.Errorf("%w: the commitment is a SHA-256", ErrBadRequest)
	}
	if req.OS != "linux" && req.OS != "windows" {
		return "", fmt.Errorf("%w: the system is linux or windows", ErrBadRequest)
	}
	if req.Instance != "" && !ValidInstance(req.Instance) {
		return "", fmt.Errorf("%w: the name of the instance is principal or up to 6 lower case letters, digits, '-' and '_', and starts with a letter or a digit", ErrBadRequest)
	}
	for _, csr := range [][]byte{req.CSRMotor, req.CSRGuardian} {
		if len(csr) > maxCSR {
			return "", fmt.Errorf("%w: a certificate request of %d bytes, the most is %d", ErrBadRequest, len(csr), maxCSR)
		}
		parsed, err := x509.ParseCertificateRequest(csr)
		if err != nil {
			return "", fmt.Errorf("%w: a certificate request: %v", ErrBadRequest, err)
		}
		if err := parsed.CheckSignature(); err != nil {
			return "", fmt.Errorf("%w: a certificate request: %v", ErrBadRequest, err)
		}
		if pub, ok := parsed.PublicKey.(*ecdsa.PublicKey); !ok || pub.Curve != elliptic.P256() {
			return "", fmt.Errorf("%w: a certificate request: the key is not ECDSA P-256", ErrBadRequest)
		}
	}
	if bytes.Equal(req.CSRMotor, req.CSRGuardian) {
		return "", fmt.Errorf("%w: the engine and the Guardian need keys of their own", ErrBadRequest)
	}
	// The CA is a root the browsers trust: no name goes in a certificate
	// unchecked (docs/arquitetura/seguranca.md#nomes-dos-certificados).
	host, err := transport.CheckHostname(req.Hostname)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	req.Hostname = host
	ips := make([]net.IP, 0, len(req.IPs))
	for _, s := range req.IPs {
		ip := net.ParseIP(s)
		if ip == nil {
			return "", fmt.Errorf("%w: %q is not an IP address", ErrBadRequest, s)
		}
		ips = append(ips, ip)
	}
	if err := transport.CheckIPs(ips); err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	if req.EnginePort < 0 || req.EnginePort > 65535 {
		return "", fmt.Errorf("%w: %d is not a port", ErrBadRequest, req.EnginePort)
	}
	if len(req.Sheet) > maxSheet {
		return "", fmt.Errorf("%w: the sheet has %d bytes, the most is %d", ErrBadRequest, len(req.Sheet), maxSheet)
	}
	if len(req.Sheet) > 0 {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(req.Sheet, &object); err != nil || object == nil {
			return "", fmt.Errorf("%w: the sheet is a JSON object", ErrBadRequest)
		}
	}
	if len(req.Sheet) > 0 && c.cfg.CheckSheet != nil {
		if err := c.cfg.CheckSheet(req.Sheet); err != nil {
			return "", fmt.Errorf("%w: the sheet: %v", ErrBadRequest, err)
		}
	}
	name := cleanName(req.Name)
	if name == "" {
		name = cleanName(req.Hostname)
	}
	if name == "" {
		return "", fmt.Errorf("%w: the machine has no name", ErrBadRequest)
	}
	return name, nil
}

// instanceRe is the rule of the name of an instance other than the principal
// one: lower case letters, digits, "-" and "_", from 1 to 6 characters, the
// first a letter or a digit, so that the account of its agents fits a Windows
// local account (docs/arquitetura/componentes.md#instâncias). localchan.ValidInstance, which
// the configuration and the local channel use, is the same rule; guild does not
// import the local channel, and a test keeps the two together.
var instanceRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,5}$`)

// ValidInstance says whether name can name an instance.
func ValidInstance(name string) bool {
	return name == "principal" || instanceRe.MatchString(name)
}

// cleanName keeps printable characters and bounds the length: the name is
// shown to the Owner in the question.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxJoinName {
		s = string(r[:maxJoinName])
	}
	return s
}

// Reveal is the second step: the machine shows the nonce it committed to.
// The Citadel checks the commitment, computes the code and asks the Owner.
// A call repeated after the Owner was asked changes nothing; one repeated
// after the question failed asks again.
func (c *Citadel) Reveal(ctx context.Context, joinID string, nonceN []byte) error {
	// not once the Citadel hands over; a reveal that slips between the check
	// and the handover is lost with the request, and the machine asks again
	if c.cfg.Suspended != nil && c.cfg.Suspended() {
		return ErrSuspended
	}
	now := c.clk.Now()
	tx, err := c.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("guild: reveal: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	j, err := loadJoin(ctx, tx, joinID)
	if err != nil {
		return err
	}
	if overdue(j, now) {
		changed, err := markExpired(ctx, tx, j.ID, now)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("guild: reveal: %w", err)
		}
		if changed {
			c.expired(ctx, j)
		}
		return ErrExpired
	}
	if j.State != JoinPending {
		return ErrNotPending
	}
	if len(nonceN) != NonceSize || subtle.ConstantTimeCompare(commitment(nonceN), j.Commitment) != 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE join_requests SET state = 'refused', decided_at = ? WHERE join_id = ?`, now.UnixMilli(), j.ID); err != nil {
			return fmt.Errorf("guild: reveal: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("guild: reveal: %w", err)
		}
		c.notify(j.ID)
		c.refusalAlert(ctx)
		return ErrBadReveal
	}
	if len(j.NonceN) > 0 {
		if !bytes.Equal(j.NonceN, nonceN) {
			return ErrBadReveal
		}
	} else {
		j.NonceN = nonceN
		j.Code = JoinCode(j.Fingerprint, [][]byte{j.Req.CSRMotor, j.Req.CSRGuardian}, nonceN, j.NonceC)
		if _, err := tx.ExecContext(ctx, `UPDATE join_requests SET nonce_n = ?, code = ? WHERE join_id = ?`, nonceN, j.Code, j.ID); err != nil {
			return fmt.Errorf("guild: reveal: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("guild: reveal: %w", err)
	}
	return c.askOnce(ctx, j)
}

// askOnce asks the Owner about a revealed request, unless the question was
// already put. Reveals of the same request at once ask one time: the others
// wait for that question and only ask if it failed.
func (c *Citadel) askOnce(ctx context.Context, j joinRow) error {
	for {
		cur, err := loadJoin(ctx, c.db.Reader(), j.ID)
		if err != nil {
			return err
		}
		if cur.ApprovalID != "" || cur.State != JoinPending {
			return nil
		}
		c.mu.Lock()
		wait, busy := c.asking[j.ID]
		if !busy {
			c.asking[j.ID] = make(chan struct{})
		}
		c.mu.Unlock()
		if busy {
			select {
			case <-wait:
				continue // the question was put, or failed: read again
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		// read again holding the place: another reveal may have put the
		// question between the first read and now
		if cur, err = loadJoin(ctx, c.db.Reader(), j.ID); err == nil && (cur.ApprovalID != "" || cur.State != JoinPending) {
			err = nil
		} else if err == nil {
			err = c.ask(ctx, j)
		}
		c.mu.Lock()
		close(c.asking[j.ID])
		delete(c.asking, j.ID)
		c.mu.Unlock()
		return err
	}
}

func (c *Citadel) ask(ctx context.Context, j joinRow) error {
	approval, err := c.cfg.Approver.Ask(ctx, ApprovalRequest{
		JoinID: j.ID, Name: j.Name, OS: j.Req.OS, Hostname: j.Req.Hostname, Instance: j.Req.Instance,
		Code: j.Code, RemoteIP: j.RemoteIP, CreatedAt: j.CreatedAt, ExpiresAt: j.ExpiresAt,
	})
	if err != nil {
		return fmt.Errorf("guild: ask the Owner: %w", err)
	}
	res, err := c.db.Writer().ExecContext(ctx, `UPDATE join_requests SET approval_id = ? WHERE join_id = ? AND state = 'pending'`, approval, j.ID)
	if err != nil {
		c.cfg.Approver.Withdraw(context.WithoutCancel(ctx), approval)
		return fmt.Errorf("guild: reveal: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// answered (or over) while it was being asked: nobody would take the question down
		c.cfg.Approver.Withdraw(context.WithoutCancel(ctx), approval)
	}
	return nil
}

// overdue says whether a pending request is over: past its hour, or past
// RevealWithin without its nonce.
func overdue(j joinRow, now time.Time) bool {
	return j.State == JoinPending && (!now.Before(j.ExpiresAt) || j.Code == "" && !now.Before(j.CreatedAt.Add(RevealWithin)))
}

// expired ends what an expiry leaves: the question, the machine that waits,
// and, for a request that never revealed its nonce, the count of the
// refusals.
func (c *Citadel) expired(ctx context.Context, j joinRow) {
	c.finish(ctx, j)
	if j.Code == "" {
		c.refusalAlert(ctx)
	}
}

// refusalAlert warns the Owner when the refusals of the last 24 hours (the
// requests refused and those that never revealed their nonce) reach a
// multiple of Limits.RefusalAlert.
func (c *Citadel) refusalAlert(ctx context.Context) {
	h := c.cfg.Hooks.RefusalAlert
	if h == nil {
		return
	}
	var n int
	if err := c.db.Reader().QueryRowContext(ctx,
		`SELECT count(*) FROM join_requests WHERE decided_at > ? AND (state = 'refused' OR state = 'expired' AND code IS NULL)`,
		c.clk.Now().Add(-24*time.Hour).UnixMilli()).Scan(&n); err != nil {
		c.cfg.Logger.Warn("guild: counting the refused requests", "err", err)
		return
	}
	if n > 0 && n%c.cfg.Limits.RefusalAlert == 0 {
		h(n)
	}
}

// Code is the code of a request that was revealed, as the Owner sees it.
func (c *Citadel) Code(ctx context.Context, joinID string) (string, error) {
	j, err := loadJoin(ctx, c.db.Reader(), joinID)
	if err != nil {
		return "", err
	}
	if j.Code == "" {
		return "", ErrNotRevealed
	}
	return j.Code, nil
}

// markExpired ends a pending request; changed says whether it was pending.
func markExpired(ctx context.Context, q querier, id string, now time.Time) (changed bool, err error) {
	res, err := q.ExecContext(ctx, `UPDATE join_requests SET state = 'expired', decided_at = ? WHERE join_id = ? AND state = 'pending'`, now.UnixMilli(), id)
	if err != nil {
		return false, fmt.Errorf("guild: expire a request: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// finish tells the Owner's channel that a request is over without an answer
// and wakes the machine that waits.
func (c *Citadel) finish(ctx context.Context, j joinRow) {
	if j.ApprovalID != "" {
		c.cfg.Approver.Withdraw(ctx, j.ApprovalID)
	}
	c.notify(j.ID)
}

// Decide applies the answer of the Owner, who accepted or refused with the
// PIN (checked by whoever calls: the bot or the panel). Accepting issues the
// certificates, registers the machine and its credentials and keeps the
// bundle for the machine; refusing, or answering after the hour, issues
// nothing and registers nothing.
func (c *Citadel) Decide(ctx context.Context, joinID string, d Decision) error {
	leave, ok := c.enter()
	if !ok {
		return ErrSuspended
	}
	defer leave()
	now := c.clk.Now()
	tx, err := c.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("guild: decide: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	j, err := loadJoin(ctx, tx, joinID)
	if err != nil {
		return err
	}
	switch {
	case j.State == JoinExpired || overdue(j, now):
		changed, err := markExpired(ctx, tx, j.ID, now)
		if err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("guild: decide: %w", err)
		}
		if changed {
			c.expired(ctx, j)
		}
		return ErrExpired
	case j.State != JoinPending:
		return ErrNotPending
	case j.Code == "":
		return ErrNotRevealed
	}
	if !d.Accept {
		if _, err := tx.ExecContext(ctx,
			`UPDATE join_requests SET state = 'refused', decided_by = ?, decided_at = ? WHERE join_id = ?`, d.ApprovedBy, now.UnixMilli(), j.ID); err != nil {
			return fmt.Errorf("guild: decide: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("guild: decide: %w", err)
		}
		c.notify(j.ID)
		c.refusalAlert(ctx)
		return nil
	}
	m, err := c.admit(ctx, tx, j, d, now)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("guild: decide: %w", err)
	}
	c.notify(j.ID)
	c.cfg.Logger.Info("guild: a machine entered", "machine", m.ID, "name", m.Name, "excluded", m.Excluded)
	if h := c.cfg.Hooks.Joined; h != nil {
		h(m)
	}
	return nil
}

// admit does the work of an acceptance inside the transaction.
func (c *Citadel) admit(ctx context.Context, tx *sql.Tx, j joinRow, d Decision, now time.Time) (Machine, error) {
	machineID := c.ids.NewString()
	name, err := uniqueName(ctx, tx, j.Name)
	if err != nil {
		return Machine{}, err
	}
	var ips []net.IP
	for _, s := range j.Req.IPs {
		ips = append(ips, net.ParseIP(s))
	}
	m := Machine{
		ID: machineID, Name: name, Instance: j.Req.Instance, Excluded: d.Excluded, State: MachineActive,
		Hostname: j.Req.Hostname, IPs: ips, JoinedAt: now.UTC(),
	}
	motor, err := c.cfg.CA.IssueMachine(j.Req.CSRMotor, m.Names())
	if err != nil {
		return Machine{}, fmt.Errorf("guild: issue the engine certificate: %w", err)
	}
	guardian, err := c.cfg.CA.IssueGuardian(j.Req.CSRGuardian, m.Names())
	if err != nil {
		return Machine{}, fmt.Errorf("guild: issue the Guardian certificate: %w", err)
	}
	var sheet any
	if len(j.Req.Sheet) > 0 {
		sheet = string(j.Req.Sheet)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, sheet, joined_at) VALUES (?, ?, ?, 0, ?, 'active', `+withPort+`, ?, ?)`,
		m.ID, m.Name, m.Instance, boolInt(m.Excluded), encodeNames(m.Hostname, m.IPs), j.Req.EnginePort, sheet, now.UnixMilli()); err != nil {
		return Machine{}, fmt.Errorf("guild: register the machine: %w", err)
	}
	for _, cred := range []struct {
		kind transport.Kind
		cert *x509.Certificate
	}{{transport.KindMachine, motor}, {transport.KindGuardian, guardian}} {
		if err := insertCredential(ctx, tx, m.ID, roleOf(cred.kind), cred.cert.Raw, transport.Serial(cred.cert), cred.cert.NotAfter, now); err != nil {
			return Machine{}, err
		}
	}
	g, err := readGuild(ctx, tx)
	if err != nil {
		return Machine{}, err
	}
	cfg, err := c.configFrom(ctx, tx, g)
	if err != nil {
		return Machine{}, err
	}
	b, err := json.Marshal(Bundle{
		GuildID: g.GuildID, MachineID: m.ID, Name: m.Name, CA: c.cfg.CA.DER(), Motor: motor.Raw, Guardian: guardian.Raw,
		Epoch: g.Epoch, Excluded: m.Excluded, Config: cfg,
	})
	if err != nil {
		return Machine{}, fmt.Errorf("guild: encode the bundle: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE join_requests SET state = 'approved', machine_id = ?, bundle = ?, decided_by = ?, decided_at = ? WHERE join_id = ?`,
		m.ID, string(b), d.ApprovedBy, now.UnixMilli(), j.ID); err != nil {
		return Machine{}, fmt.Errorf("guild: decide: %w", err)
	}
	return m, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// uniqueName adds a numeric suffix to a name another machine already has;
// what identifies a machine is its machine_id (docs/guilda/guilda.md#regras-e-erros).
func uniqueName(ctx context.Context, q querier, base string) (string, error) {
	rows, err := q.QueryContext(ctx, `SELECT name FROM machines`)
	if err != nil {
		return "", fmt.Errorf("guild: read the names: %w", err)
	}
	defer rows.Close()
	taken := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return "", err
		}
		taken[strings.ToLower(n)] = true
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	name := base
	for i := 2; taken[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name, nil
}

// Status is what the machine asks while it waits (GET /join/<id>): it
// returns as soon as the request is no longer pending, or after wait. The
// bundle of an accepted request is served for an hour after the answer, so
// an entry interrupted before the first connection can be taken again.
func (c *Citadel) Status(ctx context.Context, joinID string, wait time.Duration) (JoinStatus, error) {
	deadline := c.clk.Now().Add(wait)
	for {
		st, again, err := c.poll(ctx, joinID, deadline)
		if err != nil || !again {
			return st, err
		}
	}
}

// poll reads the request once. When it is still pending and the wait has not
// ended, it waits for a change (or the end of the wait, or of the hour of the
// request) and reports again = true.
func (c *Citadel) poll(ctx context.Context, joinID string, deadline time.Time) (st JoinStatus, again bool, err error) {
	ch, release := c.watch(joinID)
	defer release()
	j, err := loadJoin(ctx, c.db.Reader(), joinID)
	if err != nil {
		return JoinStatus{}, false, err
	}
	now := c.clk.Now()
	if overdue(j, now) {
		changed, err := markExpired(ctx, c.db.Writer(), j.ID, now)
		if err != nil {
			return JoinStatus{}, false, err
		}
		if changed {
			c.expired(ctx, j)
		}
		return JoinStatus{State: JoinExpired}, false, nil
	}
	switch j.State {
	case JoinApproved:
		if !now.Before(j.DecidedAt.Add(JoinValidity)) {
			return JoinStatus{State: JoinExpired}, false, nil
		}
		var b Bundle
		if err := json.Unmarshal([]byte(j.Bundle), &b); err != nil {
			return JoinStatus{}, false, fmt.Errorf("guild: the bundle of %s: %w", j.ID, err)
		}
		return JoinStatus{State: JoinApproved, Bundle: &b}, false, nil
	case JoinPending:
	default:
		return JoinStatus{State: j.State}, false, nil
	}
	remaining := deadline.Sub(now)
	if remaining <= 0 {
		return JoinStatus{State: JoinPending}, false, nil
	}
	// wake at the end of the wait, or when the request ends: its hour, or
	// the time it has to reveal its nonce
	end := j.ExpiresAt
	if j.Code == "" {
		end = j.CreatedAt.Add(RevealWithin)
	}
	wctx, cancel := clock.WithTimeout(ctx, c.clk, min(remaining, end.Sub(now)))
	defer cancel()
	select {
	case <-ch:
	case <-wctx.Done():
	}
	if ctx.Err() != nil {
		return JoinStatus{}, false, ctx.Err()
	}
	return JoinStatus{}, true, nil
}

// watcher is the channel that is closed the next time a request changes, and
// how many pollers hold it.
type watcher struct {
	ch   chan struct{}
	refs int
}

// watch returns the channel that is closed the next time the request
// changes; it is taken before the row is read, so no change is missed.
// release lets go of it: a request nobody waits for leaves no trace.
func (c *Citadel) watch(id string) (<-chan struct{}, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	w, ok := c.changed[id]
	if !ok {
		w = &watcher{ch: make(chan struct{})}
		c.changed[id] = w
	}
	w.refs++
	return w.ch, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		w.refs--
		if w.refs == 0 && c.changed[id] == w {
			delete(c.changed, id)
		}
	}
}

func (c *Citadel) notify(id string) {
	c.mu.Lock()
	if w, ok := c.changed[id]; ok {
		close(w.ch)
		delete(c.changed, id)
	}
	c.mu.Unlock()
}

// Sweep ends the requests that outlived their hour, or their time to reveal
// the nonce, taking their question down, and returns how many. Run calls it
// every 30 s.
func (c *Citadel) Sweep(ctx context.Context) (int, error) {
	now := c.clk.Now()
	rows, err := c.db.Reader().QueryContext(ctx,
		`SELECT `+joinColumns+` FROM join_requests WHERE state = 'pending' AND (expires_at <= ? OR code IS NULL AND created_at <= ?)`,
		now.UnixMilli(), now.Add(-RevealWithin).UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("guild: sweep: %w", err)
	}
	var due []joinRow
	for rows.Next() {
		j, err := scanJoin(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, j)
	}
	rows.Close()
	n := 0
	for _, j := range due {
		changed, err := markExpired(ctx, c.db.Writer(), j.ID, now)
		if err != nil {
			return n, err
		}
		if changed {
			n++
			c.expired(ctx, j)
		}
	}
	return n, nil
}

// Run sweeps the expired requests every 30 s until ctx ends.
func (c *Citadel) Run(ctx context.Context) {
	t := c.clk.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			if _, err := c.Sweep(ctx); err != nil && ctx.Err() == nil {
				c.cfg.Logger.Error("guild: sweeping the join requests", "err", err)
			}
		}
	}
}
