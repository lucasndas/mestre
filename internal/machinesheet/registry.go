package machinesheet

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/transport"
)

// Presence says whether a machine is connected to the Citadel now.
type Presence interface {
	Connected(machineID string) bool
}

// ServerPresence is the presence as the protocol server of the Citadel knows
// it: a machine is online while it has a connection. The application adds the
// heartbeat on top of it (app.heartbeatPresence): a Tower that missed its
// heartbeat for 90 s is offline until the next one, with the connection open.
func ServerPresence(srv *protocol.Server) Presence { return serverPresence{srv} }

type serverPresence struct{ srv *protocol.Server }

func (p serverPresence) Connected(id string) bool { return p.srv.Link(id).Connected() }

// NameBook is the registry of the machines of the Guild as far as the names of
// a machine go: the sheet brings the host name and the addresses, and the
// certificates follow them. *guild.Citadel is one.
type NameBook interface {
	Machine(ctx context.Context, id string) (guild.Machine, error)
	UpdateNames(ctx context.Context, machineID, hostname string, ips []net.IP) error
}

// RegistryConfig configures the Citadel side.
type RegistryConfig struct {
	// DB is the Guild database, with the machines table. Required.
	DB *store.DB
	// Presence tells which machines are connected. Required.
	Presence Presence
	// Self, if set, returns the machine that holds the Citadel now: it has no
	// connection to itself and is always online.
	Self func() string
	// Names, if set, is told the host name and the addresses of a machine
	// when its sheet changes them.
	Names NameBook
	// OnChange, if set, is called after a sheet is stored with the one kept
	// before (the zero Sheet for the first) and the new one, without the
	// fields of the Guild. The checklist and the panel read the changes here.
	OnChange func(machineID string, before, after Sheet)
	Logger   *slog.Logger
}

// Registry keeps the sheet of every machine in machines.sheet.
type Registry struct {
	cfg RegistryConfig
	// mu serializes the read-merge-write of one sheet.
	mu sync.Mutex
	// warned is the reason last logged for each machine whose stored sheet is
	// unreadable, so the log says it once.
	warnMu sync.Mutex
	warned map[string]string
}

// ErrNotActive: the machine is not an active machine of the Guild.
var ErrNotActive = errors.New("machinesheet: the machine is not active in the Guild")

// NewRegistry returns the registry over the Guild database.
func NewRegistry(cfg RegistryConfig) (*Registry, error) {
	if cfg.DB == nil || cfg.Presence == nil {
		return nil, errors.New("machinesheet: the registry needs the Guild database and the presence")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Registry{cfg: cfg}, nil
}

// Register puts the handler of machine.sheet on the router.
func (r *Registry) Register(router *protocol.Router) {
	router.Handle(protocol.TypeMachineSheet, r.Handler())
}

// Handler is the handler of machine.sheet: it merges the patch into the sheet
// kept. A patch older than the sheet is acknowledged and dropped; a sheet
// that is not valid is `invalid_payload`.
func (r *Registry) Handler() protocol.Handler {
	return protocol.HandlerFunc(func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		var patch protocol.Object
		switch p := m.Payload.(type) {
		case *protocol.Object:
			patch = *p
		case protocol.Object:
			patch = p
		default:
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "machine.sheet payload is %T", m.Payload)
		}
		return nil, r.apply(ctx, m.Peer.ID, patch, false)
	})
}

// OnHello is the Hooks.OnHello of the Citadel: the sheet of the hello replaces
// the one kept. A hello without the required fields is refused with
// `invalid_payload`, and one from a machine outside the Guild with
// `forbidden`.
func (r *Registry) OnHello(ctx context.Context, peer protocol.Peer, h *protocol.Hello) error {
	return r.apply(ctx, peer.ID, h.Sheet, true)
}

// Replace stores a whole sheet, as the hello does, for the machine that holds
// the Citadel and so has no hello.
func (r *Registry) Replace(ctx context.Context, machineID string, obj protocol.Object) error {
	return r.apply(ctx, machineID, obj, true)
}

// Local is the Sender of the Citadel's own agent: its patches are applied to
// the registry directly.
func (r *Registry) Local(machineID string) Sender { return localSender{r, machineID} }

type localSender struct {
	r  *Registry
	id string
}

func (l localSender) Send(ctx context.Context, o protocol.Outgoing) (string, error) {
	patch, ok := o.Payload.(protocol.Object)
	if !ok || o.Type != protocol.TypeMachineSheet {
		return "", fmt.Errorf("machinesheet: the local link carries machine.sheet, not %s", o.Type)
	}
	return "", l.r.apply(ctx, l.id, patch, false)
}

func (r *Registry) apply(ctx context.Context, machineID string, payload protocol.Object, replace bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, err := r.load(ctx, r.cfg.DB.Reader(), machineID)
	if err != nil {
		if errors.Is(err, ErrNotActive) {
			return protocol.NewError(protocol.CodeForbidden, "%v", err)
		}
		return protocol.NewError(protocol.CodeInternal, "read the sheet of %s: %v", machineID, err)
	}
	if !replace && row.status == sheetUnreadable {
		return protocol.NewError(protocol.CodeInvalidPayload, "the stored sheet of %s is unreadable: send the whole sheet (a hello) to replace it", machineID)
	}
	after, err := Merge(row.sheet, payload, replace)
	switch {
	case errors.Is(err, ErrStale):
		r.cfg.Logger.Debug("machinesheet: a stale patch was dropped", "machine", machineID, "err", err)
		return nil
	case err != nil:
		return protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
	}
	if replace && after.Rev < row.sheet.Rev {
		// A hello without a revision keeps the numbering going up.
		after.Rev = row.sheet.Rev
	}
	b, err := json.Marshal(after)
	if err != nil {
		return protocol.NewError(protocol.CodeInternal, "encode the sheet: %v", err)
	}
	res, err := r.cfg.DB.Writer().ExecContext(ctx, `UPDATE machines SET sheet = ? WHERE machine_id = ? AND state = 'active'`, string(b), machineID)
	if err != nil {
		return protocol.NewError(protocol.CodeInternal, "store the sheet of %s: %v", machineID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return protocol.NewError(protocol.CodeForbidden, "%v: %s", ErrNotActive, machineID)
	}
	r.followNames(ctx, machineID, after)
	if h := r.cfg.OnChange; h != nil && (row.status != sheetOK || !Equal(row.sheet, after)) {
		h(machineID, row.sheet.Clone(), after.Clone())
	}
	return nil
}

// followNames tells the registry the host name and the addresses the sheet
// reports, so the certificate follows them: the first label of the host name
// and only the addresses a certificate may carry (transport.HostLabel and
// CertificateIPs; a global IPv6 address of the machine is left out). The
// registry checks them again.
func (r *Registry) followNames(ctx context.Context, machineID string, s Sheet) {
	if r.cfg.Names == nil || s.Host == "" {
		return
	}
	m, err := r.cfg.Names.Machine(ctx, machineID)
	if err != nil {
		r.cfg.Logger.Warn("machinesheet: read the names of a machine", "machine", machineID, "err", err)
		return
	}
	var ips []net.IP
	for _, a := range s.IPs {
		if ip := net.ParseIP(a); ip != nil {
			ips = append(ips, ip)
		}
	}
	host, ips := transport.HostLabel(s.Host), transport.CertificateIPs(ips)
	if m.Hostname == host && sameIPs(m.IPs, ips) {
		return
	}
	if err := r.cfg.Names.UpdateNames(ctx, machineID, host, ips); err != nil {
		r.cfg.Logger.Warn("machinesheet: update the names of a machine", "machine", machineID, "err", err)
	}
}

func sameIPs(a, b []net.IP) bool {
	if len(a) != len(b) {
		return false
	}
	as, bs := ipStrings(a), ipStrings(b)
	slices.Sort(as)
	slices.Sort(bs)
	return slices.Equal(as, bs)
}

func ipStrings(ips []net.IP) []string {
	out := make([]string, len(ips))
	for i, ip := range ips {
		out[i] = ip.String()
	}
	return out
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// row is a machine of the registry with its sheet.
type row struct {
	sheet  Sheet
	status sheetStatus
	// err is why the stored sheet could not be read (sheetUnreadable): the row
	// is then taken as a machine with no sheet, until its next hello replaces it.
	err error
}

// sheetStatus says what the sheet column of a row held.
type sheetStatus int

const (
	sheetNone       sheetStatus = iota // no sheet stored yet
	sheetOK                            // a valid sheet
	sheetUnreadable                    // wrong types or not a valid sheet
)

const rowColumns = `machine_id, name, instance, excluded, state, sheet`

func scanRow(sc interface{ Scan(...any) error }) (row, string, error) {
	var (
		id, name, instance, state string
		excluded                  int
		sheet                     sql.NullString
	)
	if err := sc.Scan(&id, &name, &instance, &excluded, &state, &sheet); err != nil {
		return row{}, "", err
	}
	out := row{}
	if sheet.Valid && sheet.String != "" {
		if err := json.Unmarshal([]byte(sheet.String), &out.sheet); err != nil {
			out.sheet, out.status, out.err = Sheet{}, sheetUnreadable, fmt.Errorf("the sheet of %s: %w", id, err)
		} else if err := out.sheet.Validate(); err != nil {
			out.sheet, out.status, out.err = Sheet{}, sheetUnreadable, fmt.Errorf("the sheet of %s: %w", id, err)
		} else {
			out.status = sheetOK
		}
	}
	out.sheet.MachineID, out.sheet.Name, out.sheet.Excluded = id, name, excluded != 0
	if instance != "" {
		out.sheet.Instance = instance
	}
	return out, state, nil
}

func (r *Registry) load(ctx context.Context, q querier, machineID string) (row, error) {
	out, state, err := scanRow(q.QueryRowContext(ctx, `SELECT `+rowColumns+` FROM machines WHERE machine_id = ?`, machineID))
	if errors.Is(err, sql.ErrNoRows) {
		return row{}, fmt.Errorf("%w: %s", ErrNotActive, machineID)
	}
	if err != nil {
		return row{}, err
	}
	if state != string(guild.MachineActive) {
		return row{}, fmt.Errorf("%w: %s is %s", ErrNotActive, machineID, state)
	}
	r.warnUnreadable(out)
	return out, nil
}

// warnUnreadable logs a stored sheet that could not be read; the listing goes
// on without it, so one machine cannot stop the choice for the whole Guild.
// It logs once per machine and per reason, not on every read.
func (r *Registry) warnUnreadable(rw row) {
	id := rw.sheet.MachineID
	r.warnMu.Lock()
	defer r.warnMu.Unlock()
	if rw.status != sheetUnreadable {
		delete(r.warned, id)
		return
	}
	if r.warned[id] == rw.err.Error() {
		return
	}
	if r.warned == nil {
		r.warned = map[string]string{}
	}
	r.warned[id] = rw.err.Error()
	r.cfg.Logger.Warn("machinesheet: a stored sheet is unreadable and is taken as missing", "machine", id, "err", rw.err)
}

// online fills the field the connection decides.
func (r *Registry) online(s Sheet) Sheet {
	s.Online = (r.cfg.Self != nil && r.cfg.Self() == s.MachineID) || r.cfg.Presence.Connected(s.MachineID)
	return s
}

// Sheet returns the last sheet of an active machine, with the name, the
// exclusion and whether it is online now. A machine offline keeps its last
// sheet, with `online: false`.
func (r *Registry) Sheet(ctx context.Context, machineID string) (Sheet, error) {
	rw, err := r.load(ctx, r.cfg.DB.Reader(), machineID)
	if err != nil {
		return Sheet{}, err
	}
	return r.online(rw.sheet), nil
}

// Sheets lists the sheets of the active machines, oldest entry first.
func (r *Registry) Sheets(ctx context.Context) ([]Sheet, error) {
	rows, err := r.cfg.DB.Reader().QueryContext(ctx,
		`SELECT `+rowColumns+` FROM machines WHERE state = 'active' ORDER BY joined_at, machine_id`)
	if err != nil {
		return nil, fmt.Errorf("machinesheet: list the sheets: %w", err)
	}
	defer rows.Close()
	var out []Sheet
	for rows.Next() {
		rw, _, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("machinesheet: %w", err)
		}
		r.warnUnreadable(rw)
		out = append(out, r.online(rw.sheet))
	}
	return out, rows.Err()
}

// Choose matches the requirements of a character against every active machine
// and says, for each, whether it fits or the first requirement it does not
// meet. Choice.Err is the error of "no machine fits".
func (r *Registry) Choose(ctx context.Context, req Requirements) (Choice, error) {
	sheets, err := r.Sheets(ctx)
	if err != nil {
		return Choice{}, err
	}
	return Choose(sheets, req), nil
}
