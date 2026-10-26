// Package accounts keeps the account profiles of this machine
// (docs/orquestracao/bases-e-fichas.md#perfis-de-conta): the isolated
// configuration folder of each account of a provider, under the machine's
// contas folder, used by the agents' account and never by the user's own
// logins. It registers them, resolves the one a sheet asks for into the
// variable the provider reads, tells the machine sheet how each one stands,
// and runs the guided setup the panel shows once per profile.
package accounts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Step is where the setup of a profile stands.
type Step string

// The steps, in order. Hooks is the Codex's alone.
const (
	StepInstall     Step = "install"
	StepLogin       Step = "login"
	StepHooks       Step = "hooks"
	StepConformance Step = "conformance"
	StepDone        Step = "done"
)

// Profile is an account profile of this machine.
type Profile struct {
	Name     string
	Provider providers.ID
	// Dir is the provider folder: CLAUDE_CONFIG_DIR, CODEX_HOME, or the
	// HOME of Antigravity.
	Dir string
	// Default marks the profile a sheet without `account` uses.
	Default bool
	Step    Step
	// Detail is what the setup waits for, or why it stopped.
	Detail      string
	CreatedAt   time.Time
	SetupDoneAt time.Time
}

// Env is the variable that points the provider at the folder of the profile.
func (p Profile) Env() []string { return providers.ProfileEnv(p.Provider, p.Dir) }

// Folders makes the folder of a profile. The installer gives the contas
// folder its owner and its ACLs (docs/arquitetura/persistencia.md#arquivos);
// the folders under it inherit them.
type Folders interface {
	Ensure(dir string) error
}

// OSFolders makes the folders on disk, with the group of the agents' account
// able to write.
type OSFolders struct{}

// Ensure implements Folders.
func (OSFolders) Ensure(dir string) error { return os.MkdirAll(dir, 0o770) }

// Registry is the profiles of this machine, in the machine database.
type Registry struct {
	DB    *store.DB
	Clock clock.Clock
	// Root is the contas folder of the machine.
	Root string
	// OS is the system of the machine; Antigravity keeps its login for the
	// whole Windows account, so there is one profile of it per Windows
	// machine (docs/provedores/antigravity.md).
	OS      string
	Folders Folders
}

var nameRe = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// Errors of the registry.
var (
	ErrName    = errors.New("nome de perfil inválido")
	ErrExists  = errors.New("o perfil já existe")
	ErrUnknown = errors.New("o perfil não existe nesta máquina")
	// ErrNoProfile: a sheet asks for a profile this machine does not have,
	// or has no default for its provider.
	ErrNoProfile = errors.New("perfil de conta indisponível")
)

const cols = `name, provider, dir, is_default, setup_step, setup_detail, created_at, setup_done_at`

func scan(sc interface{ Scan(...any) error }) (Profile, error) {
	var (
		p        Profile
		def      int
		step     string
		created  int64
		done     sql.NullInt64
		provider string
	)
	if err := sc.Scan(&p.Name, &provider, &p.Dir, &def, &step, &p.Detail, &created, &done); err != nil {
		return Profile{}, err
	}
	p.Provider, p.Default, p.Step = providers.ID(provider), def != 0, Step(step)
	p.CreatedAt = time.UnixMilli(created).UTC()
	if done.Valid {
		p.SetupDoneAt = time.UnixMilli(done.Int64).UTC()
	}
	return p, nil
}

// Add registers a profile and makes its folder, under Root. The first
// profile of a provider is its default. The login, the CLI and the rest are
// the setup's.
func (r *Registry) Add(ctx context.Context, name string, provider providers.ID) (Profile, error) {
	if !nameRe.MatchString(name) {
		return Profile{}, fmt.Errorf("%w: `%s` (letras minúsculas, dígitos e `-`, de 1 a 64)", ErrName, name)
	}
	if !slices.Contains(machinesheet.KnownProviders(), string(provider)) {
		return Profile{}, fmt.Errorf("provedor `%s` desconhecido; os aceitos são %v", provider, machinesheet.KnownProviders())
	}
	p := Profile{Name: name, Provider: provider, Dir: filepath.Join(r.Root, name), Step: StepInstall, CreatedAt: r.Clock.Now().UTC()}
	err := r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_profiles WHERE name = ?`, name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: `%s`", ErrExists, name)
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM account_profiles WHERE provider = ?`, string(provider)).Scan(&n); err != nil {
			return err
		}
		if n > 0 && provider == providers.Antigravity && r.OS == machinesheet.OSWindows {
			return fmt.Errorf("%w: o login do Antigravity no Windows é um só por conta de sistema; esta máquina já tem um perfil dele", ErrExists)
		}
		p.Default = n == 0
		def := 0
		if p.Default {
			def = 1
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO account_profiles (`+cols+`) VALUES (?, ?, ?, ?, ?, '', ?, NULL)`,
			p.Name, string(provider), p.Dir, def, string(p.Step), p.CreatedAt.UnixMilli())
		return err
	})
	if err != nil {
		return Profile{}, err
	}
	if err := r.Folders.Ensure(p.Dir); err != nil {
		return Profile{}, fmt.Errorf("create the folder of profile %s: %w", name, err)
	}
	return p, nil
}

// EnsureDefault gives the provider a profile of the Mestre when the machine
// has none: `<provider>-padrao`. The Codex always uses a profile of the
// Mestre, never the personal ~/.codex.
func (r *Registry) EnsureDefault(ctx context.Context, provider providers.ID) (Profile, error) {
	all, err := r.Profiles(ctx)
	if err != nil {
		return Profile{}, err
	}
	for _, p := range all {
		if p.Provider == provider {
			if p.Default {
				return p, nil
			}
			return Profile{}, fmt.Errorf("%w: o %s tem perfis mas nenhum é o padrão", ErrNoProfile, provider)
		}
	}
	return r.Add(ctx, string(provider)+"-padrao", provider)
}

// Remove unregisters a profile. The folder, which holds the login, stays on
// disk: deleting a login is the Owner's, by hand.
func (r *Registry) Remove(ctx context.Context, name string) error {
	return r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM account_profiles WHERE name = ?`, name)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: `%s`", ErrUnknown, name)
		}
		return nil
	})
}

// SetDefault makes the profile the default of its provider.
func (r *Registry) SetDefault(ctx context.Context, name string) error {
	return r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		p, err := scan(tx.QueryRowContext(ctx, `SELECT `+cols+` FROM account_profiles WHERE name = ?`, name))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: `%s`", ErrUnknown, name)
		} else if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE account_profiles SET is_default = 0 WHERE provider = ? AND is_default = 1 AND name != ?`, string(p.Provider), name); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE account_profiles SET is_default = 1 WHERE name = ?`, name)
		return err
	})
}

// Profile is the profile of that name.
func (r *Registry) Profile(ctx context.Context, name string) (Profile, bool, error) {
	p, err := scan(r.DB.Reader().QueryRowContext(ctx, `SELECT `+cols+` FROM account_profiles WHERE name = ?`, name))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Profile{}, false, nil
	case err != nil:
		return Profile{}, false, err
	}
	return p, true, nil
}

// Profiles are all the profiles, by name.
func (r *Registry) Profiles(ctx context.Context) ([]Profile, error) {
	rows, err := r.DB.Reader().QueryContext(ctx, `SELECT `+cols+` FROM account_profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Profile
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Resolve is the profile a character of that provider and `account` uses on
// this machine: the one named, or, without a name, the default of the
// provider (or the only one the machine has). A profile the machine lacks
// does not serve the sheet: the machine does not fit it.
func (r *Registry) Resolve(ctx context.Context, provider providers.ID, account string) (Profile, error) {
	all, err := r.Profiles(ctx)
	if err != nil {
		return Profile{}, err
	}
	var of []Profile
	for _, p := range all {
		if p.Provider == provider {
			of = append(of, p)
		}
	}
	if account != "" {
		for _, p := range of {
			if p.Name == account {
				return p, nil
			}
		}
		return Profile{}, fmt.Errorf("%w: o perfil `%s` do %s não existe nesta máquina", ErrNoProfile, account, provider)
	}
	for _, p := range of {
		if p.Default {
			return p, nil
		}
	}
	if len(of) == 1 {
		return of[0], nil
	}
	return Profile{}, fmt.Errorf("%w: o %s não tem perfil de conta padrão nesta máquina", ErrNoProfile, provider)
}

func (r *Registry) setStep(ctx context.Context, name string, step Step, detail string) error {
	var done sql.NullInt64
	if step == StepDone {
		done = sql.NullInt64{Int64: r.Clock.Now().UTC().UnixMilli(), Valid: true}
	}
	return r.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE account_profiles SET setup_step = ?, setup_detail = ?, setup_done_at = ? WHERE name = ?`,
			string(step), detail, done, name)
		return err
	})
}
