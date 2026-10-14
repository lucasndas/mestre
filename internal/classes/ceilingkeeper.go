package classes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// DefaultCeilingInterval is how often the engine rereads teto.yaml for a
// change made outside the Mestre.
const DefaultCeilingInterval = 30 * time.Second

// CeilingSource is the teto.yaml of one workspace on disk. Read returns
// fs.ErrNotExist when there is no file.
type CeilingSource interface {
	Read(ctx context.Context) ([]byte, error)
	Write(ctx context.Context, data []byte) error
}

// CeilingPath is the teto.yaml under the main clone of a workspace, on
// disk.
type CeilingPath string

// Read reads the file.
func (p CeilingPath) Read(context.Context) ([]byte, error) { return os.ReadFile(string(p)) }

// Write replaces the file whole: a temporary file renamed over it.
func (p CeilingPath) Write(_ context.Context, data []byte) error {
	return writeAtomic(string(p), data, 0o644)
}

// CeilingRecord is the approved version of the ceiling of a workspace, as the
// database keeps it: the hash and the file, so the approved version holds
// whatever the file on disk says.
type CeilingRecord struct {
	Hash       string
	Doc        []byte
	ApprovedAt time.Time
	By         Authority
}

// CeilingRecords keeps the approved version of each workspace (the Guild
// database: CeilingDB).
type CeilingRecords interface {
	// Approved is the approved version; ok is false when none was recorded.
	Approved(ctx context.Context, workspace string) (rec CeilingRecord, ok bool, err error)
	Approve(ctx context.Context, workspace string, rec CeilingRecord) error
}

// CeilingAlertKind is what the engine noticed in teto.yaml.
type CeilingAlertKind string

// The alerts to the Administrators.
const (
	// CeilingInvalid: the file does not parse.
	CeilingInvalid CeilingAlertKind = "invalid"
	// CeilingMissing: the file is gone.
	CeilingMissing CeilingAlertKind = "missing"
	// CeilingRestricts: a version that only restricts; an Administrator
	// approves it on the panel.
	CeilingRestricts CeilingAlertKind = "restricts"
	// CeilingWidens: a version that widens; only a decree applies it.
	CeilingWidens CeilingAlertKind = "widens"
)

// CeilingAlert tells the Administrators that teto.yaml changed outside the
// panel and the decrees.
type CeilingAlert struct {
	Workspace string
	Kind      CeilingAlertKind
	// Hash is the version on disk ("" when missing); Approved the one that
	// holds.
	Hash, Approved string
	Detail         string
	Diff           CeilingDiff
}

// AdminAlerts reaches the Administrators of the workspace (Telegram and
// panel, in their milestones).
type AdminAlerts interface {
	CeilingChanged(ctx context.Context, a CeilingAlert) error
}

// CeilingKeeper keeps the ceiling of one workspace: the version that holds is
// always the last approved one (or the default ceiling before any), and a
// change of teto.yaml that did not go through the panel or a decree is only
// noticed, alerted and kept as pending until it is approved
// (docs/classes/teto-do-workspace.md#arquivo-mudado-por-fora).
type CeilingKeeper struct {
	Workspace string
	File      CeilingSource
	Records   CeilingRecords
	Alerts    AdminAlerts
	Clock     clock.Clock
	// Interval of the watch; zero is DefaultCeilingInterval.
	Interval time.Duration

	mu      sync.Mutex
	alerted string // the last change alerted, so one change gives one alert
}

// Current is the ceiling that holds: the approved version, or the default
// ceiling when none was approved yet.
func (k *CeilingKeeper) Current(ctx context.Context) (*Ceiling, error) {
	rec, ok, err := k.Records.Approved(ctx, k.Workspace)
	if err != nil {
		return nil, fmt.Errorf("ceiling of %s: %w", k.Workspace, err)
	}
	if !ok {
		return DefaultCeiling(), nil
	}
	c, err := ParseCeiling(rec.Doc)
	if err != nil {
		// The database holds only versions that parsed; this one does not
		// with this binary. Nothing looser than the default holds.
		return nil, fmt.Errorf("ceiling of %s: approved version %s: %w", k.Workspace, rec.Hash, err)
	}
	return c, nil
}

// CeilingState is what Check found.
type CeilingState struct {
	Approved string // hash of the version that holds
	// Pending is the version on disk when it differs (nil: the file is the
	// approved version, or it is invalid or missing).
	Pending *Ceiling
	Alert   *CeilingAlert // the alert of the difference, if any
}

// Check rereads teto.yaml and compares it with the approved version. A
// difference never changes the ceiling that holds: it is alerted to the
// Administrators, once per change.
func (k *CeilingKeeper) Check(ctx context.Context) (CeilingState, error) {
	cur, err := k.Current(ctx)
	if err != nil {
		return CeilingState{}, err
	}
	st := CeilingState{Approved: cur.Hash}
	data, err := k.File.Read(ctx)
	var a *CeilingAlert
	switch {
	case errors.Is(err, fs.ErrNotExist):
		a = &CeilingAlert{Kind: CeilingMissing, Detail: CeilingFile + " não existe; vale a última versão aprovada"}
	case err != nil:
		return st, fmt.Errorf("ceiling of %s: %w", k.Workspace, err)
	default:
		next, perr := ParseCeiling(data)
		switch {
		case perr != nil:
			a = &CeilingAlert{Kind: CeilingInvalid, Hash: hashOf(data), Detail: perr.Error()}
		case next.Hash == cur.Hash:
			k.mu.Lock()
			k.alerted = "" // back to the approved file: a new change alerts again
			k.mu.Unlock()
			return st, nil
		default:
			st.Pending = next
			d := DiffCeilings(cur, next)
			a = &CeilingAlert{Kind: CeilingRestricts, Hash: next.Hash, Diff: d,
				Detail: "a nova versão só restringe; um Administrador aprova pelo painel"}
			if !d.OnlyRestricts() {
				a.Kind, a.Detail = CeilingWidens, "a nova versão amplia o teto; só vale com decreto"
			}
		}
	}
	a.Workspace, a.Approved = k.Workspace, cur.Hash
	st.Alert = a
	key := string(a.Kind) + ":" + a.Hash
	k.mu.Lock()
	fresh := k.alerted != key
	k.alerted = key
	k.mu.Unlock()
	if fresh && k.Alerts != nil {
		if err := k.Alerts.CeilingChanged(ctx, *a); err != nil {
			k.mu.Lock()
			k.alerted = "" // try again at the next check
			k.mu.Unlock()
			return st, fmt.Errorf("ceiling of %s: alert: %w", k.Workspace, err)
		}
	}
	return st, nil
}

// ErrCeilingChanged is an approval of a version that is no longer the one
// on disk.
var ErrCeilingChanged = errors.New("teto.yaml changed since the version shown")

// ApprovePending approves the version on disk, by its hash (the one the
// Administrator saw): a version that only restricts on the panel, one that
// widens only by decree.
func (k *CeilingKeeper) ApprovePending(ctx context.Context, hash string, by Authority) error {
	data, err := k.File.Read(ctx)
	if err != nil {
		return fmt.Errorf("ceiling of %s: %w", k.Workspace, err)
	}
	next, err := ParseCeiling(data)
	if err != nil {
		return err
	}
	if next.Hash != hash {
		return fmt.Errorf("%w (%s on disk)", ErrCeilingChanged, next.Hash)
	}
	return k.approve(ctx, next, by)
}

// Change is a change through the Mestre (panel or decree): checked, written
// to teto.yaml and recorded as approved. The file is written first: if the
// record fails, the change is only pending.
func (k *CeilingKeeper) Change(ctx context.Context, data []byte, by Authority) error {
	next, err := ParseCeiling(data)
	if err != nil {
		return err
	}
	if err := k.authorize(ctx, next, by); err != nil {
		return err
	}
	if err := k.File.Write(ctx, next.Raw()); err != nil {
		return fmt.Errorf("ceiling of %s: %w", k.Workspace, err)
	}
	return k.approve(ctx, next, by)
}

func (k *CeilingKeeper) authorize(ctx context.Context, next *Ceiling, by Authority) error {
	cur, err := k.Current(ctx)
	if err != nil {
		return err
	}
	return AuthorizeCeiling(DiffCeilings(cur, next), by)
}

func (k *CeilingKeeper) approve(ctx context.Context, next *Ceiling, by Authority) error {
	if err := k.authorize(ctx, next, by); err != nil {
		return err
	}
	rec := CeilingRecord{Hash: next.Hash, Doc: next.Raw(), ApprovedAt: k.Clock.Now().UTC(), By: by}
	if err := k.Records.Approve(ctx, k.Workspace, rec); err != nil {
		return fmt.Errorf("ceiling of %s: %w", k.Workspace, err)
	}
	k.mu.Lock()
	k.alerted = ""
	k.mu.Unlock()
	return nil
}

// Run watches teto.yaml until ctx ends: a Check at once and then at every
// Interval. Errors go to report (nil drops them); the ceiling that holds
// does not change with them.
func (k *CeilingKeeper) Run(ctx context.Context, report func(error)) {
	every := k.Interval
	if every <= 0 {
		every = DefaultCeilingInterval
	}
	check := func() {
		if _, err := k.Check(ctx); err != nil && report != nil && ctx.Err() == nil {
			report(err)
		}
	}
	check()
	// The ticker is armed after the first check: a test that waits for it
	// knows that check is done.
	t := k.Clock.NewTicker(every)
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

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeAtomic writes path whole: a temporary file in the same folder,
// renamed over it, so a reader never sees half a file.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
