package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/orchestration/accounts"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/terminal"
)

// The terminals of the characters of this machine (docs/desenvolvimento/ligacao-de-producao.md):
// the Runtime of internal/orchestration/characters over a terminal host. What
// the installation brings (the socket of the launcher service, the folder and
// the version of the mestre-pty it installed, the accounts of the agents) is
// read from Config.Terminais; without it the machine starts no character and
// says why, it never pretends.

// Terminals is Config.Terminais: where the terminals of the characters of a
// machine come from.
type Terminals struct {
	// Dir holds the installed mestre-pty, <Dir>/<Version>/mestre-pty (mestre-pty.exe).
	Dir string `json:"pasta"`
	// Socket is the socket of the launcher that starts the mestre-pty as the
	// account of the agents; empty uses Deps.Spawner (the full suite and development).
	Socket string `json:"socket,omitempty"`
	// CgroupBase is the cgroup of the launcher service that holds the
	// term-<id> cgroups of the terminals (Linux, with the launcher).
	CgroupBase string `json:"cgroup,omitempty"`
	// Version is the mestre-pty version new terminals are created with.
	Version string `json:"versao"`
}

func (t Terminals) check() error {
	switch {
	case t.Dir == "" || !filepath.IsAbs(t.Dir):
		return errors.New("app: terminais.pasta must be the absolute folder of the installed mestre-pty")
	case t.Version == "":
		return errors.New("app: terminais.versao names no mestre-pty version")
	}
	return nil
}

// ptyBinary is the mestre-pty of a version in the installed folder.
func (t Terminals) ptyBinary(version string) (string, error) {
	if version == "" || version == "." || version == ".." || filepath.Base(version) != version {
		return "", fmt.Errorf("mestre-pty version %q", version)
	}
	name := "mestre-pty"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(t.Dir, version, name), nil
}

// errTerminalsNotWired is why a machine without Config.Terminais starts no
// character.
var errTerminalsNotWired = errors.New("the terminals of the characters are not wired on this machine: the configuration has no `terminais` (the folder of the mestre-pty and the socket of the launcher, which the installation brings)")

// unwiredMachines are the machines of a Citadel whose own machine has no
// terminals: the start of a character there refuses with the reason.
type unwiredMachines struct {
	characters.MachineSet
	local string
}

func (u unwiredMachines) Start(ctx context.Context, machine string, cmd characters.StartCommand) (characters.StartResult, error) {
	if machine == u.local {
		return characters.StartResult{}, errTerminalsNotWired
	}
	return u.MachineSet.Start(ctx, machine, cmd)
}

// periodSinks are the Citadel of the period seen from the Runtime of the
// machine, which outlives the periods: what a character reports, the notices,
// the waits, the inbox and the end of a turn go to the services of the period
// that acts, and to nobody between periods.
type periodSinks struct {
	orc *atomic.Pointer[Orchestration]
}

// errNoCitadelHere: this machine does not host the Citadel's orchestration now.
var errNoCitadelHere = errors.New("app: this machine hosts no orchestration now")

var (
	_ characters.Reporter     = periodSinks{}
	_ characters.Notifier     = periodSinks{}
	_ characters.WaitProbe    = periodSinks{}
	_ characters.TurnObserver = periodSinks{}
	_ providers.Inbox         = periodSinks{}
)

func (p periodSinks) Report(ctx context.Context, r characters.Report) error {
	orc := p.orc.Load()
	if orc == nil {
		return errNoCitadelHere
	}
	return orc.Characters.Report(ctx, r)
}

func (p periodSinks) Notify(ctx context.Context, n characters.Notice) error {
	orc := p.orc.Load()
	if orc == nil {
		return errNoCitadelHere
	}
	return orc.Inbox.Notify(ctx, n)
}

func (p periodSinks) Active(ctx context.Context, id string) (string, bool, error) {
	orc := p.orc.Load()
	if orc == nil {
		return "", false, nil
	}
	return orc.Waits.Active(ctx, id)
}

func (p periodSinks) TurnEnded(ctx context.Context, id, last string) error {
	orc := p.orc.Load()
	if orc == nil {
		return errNoCitadelHere
	}
	return orc.Characters.TurnEnded(ctx, id, last)
}

func (p periodSinks) Confirmed(ctx context.Context, delivery string) error {
	orc := p.orc.Load()
	if orc == nil {
		return errNoCitadelHere
	}
	return orc.Inbox.Confirmed(ctx, delivery)
}

func (p periodSinks) Unconfirmed(ctx context.Context, delivery string) error {
	orc := p.orc.Load()
	if orc == nil {
		return errNoCitadelHere
	}
	return orc.Inbox.Unconfirmed(ctx, delivery)
}

// machineRuntime is the Runtime of this machine with what must be closed with it.
type machineRuntime struct {
	rt   *characters.Runtime
	host *characters.TerminalHost
}

func (m *machineRuntime) close() {
	m.rt.Close()
	m.host.Close()
}

// openRuntime builds the Runtime of this machine from Config.Terminais, and
// returns nil, nil when the machine has none. The directory of the terminals
// becomes the identifier of the local channel, so a caller is known by the
// terminal it runs in.
func (e *Engine) openRuntime(ctx context.Context) (*machineRuntime, error) {
	t := e.cfg.Terminais
	if t == nil {
		return nil, nil
	}
	if err := t.check(); err != nil {
		return nil, err
	}
	spawner, mode, base := e.deps.Spawner, localchan.ModeTree, ""
	if spawner == nil {
		var err error
		if spawner, err = launcherSpawner(*t); err != nil {
			return nil, err
		}
		mode, base = localchan.ModeCgroup, t.CgroupBase
	}
	sys := terminal.NewSystem()
	dir := localchan.NewDirectory(sys)
	run := filepath.Join(e.paths.Maquina, "terminais")
	host, err := characters.NewTerminalHost(terminal.ManagerConfig{
		DB: e.mdb, Clock: e.clk, System: sys, Spawner: spawner, Access: e.paths.TerminalAccess, MachineID: e.id.MachineID,
		SocketDir: filepath.Join(run, "s"), LogDir: filepath.Join(run, "l"),
		PTYBinary: t.ptyBinary, PTYVersion: t.Version, Masker: e.deps.Masker, Logger: e.log,
	}, dir)
	if err != nil {
		return nil, err
	}
	prep := e.deps.CharacterPrep
	if prep == nil {
		if prep, err = e.characterPrep(); err != nil {
			host.Close()
			return nil, err
		}
	}
	sinks := periodSinks{&e.orc}
	rt, err := characters.NewRuntime(characters.RuntimeOptions{
		Machine: e.id.MachineID, Clock: e.clk, DB: e.mdb, Host: host, Prep: prep, Providers: providers.NewRegistry(providers.Options{}),
		Reporter: sinks, Notifier: sinks, Waits: sinks, Inbox: sinks, Turns: sinks, OS: runtime.GOOS, Logger: e.log,
	})
	if err != nil {
		host.Close()
		return nil, err
	}
	// A row that does not come back is a character the Citadel learns is
	// interrupted; it never keeps the engine from starting.
	if _, err := rt.Recover(ctx); err != nil {
		e.log.Warn("app: some terminals that survived the engine were not found again", "err", err)
	}
	e.deps.UseCharacters(rt, localchan.NewSystemIdentifier(localchan.SystemConfig{Terminals: dir, Jobs: dir, System: sys, Mode: mode, CgroupBase: base}))
	return &machineRuntime{rt: rt, host: host}, nil
}

// characterPrep is the session zero of the characters of this machine over the
// accounts of its database. A sheet that asks for an account the machine lacks
// fails with the reason (docs/sessao-zero/personagem.md#regras-e-erros); the
// accounts themselves are what the installation brings.
func (e *Engine) characterPrep() (characters.Preparer, error) {
	binary := e.deps.Binary
	if binary == "" {
		var err error
		if binary, err = os.Executable(); err != nil {
			return nil, fmt.Errorf("app: the path of the binary: %w", err)
		}
	}
	reg := providers.NewRegistry(providers.Options{})
	agents, err := filepath.Abs(e.paths.Agentes)
	if err != nil {
		return nil, err
	}
	files := &classes.OSNativeFiles{}
	return sessionzero.NewCharacter(sessionzero.CharacterOptions{
		Providers: reg, OS: runtime.GOOS, Binary: binary, Endpoint: localchan.EndpointURL(e.paths.CLISocket), Agents: agents,
		Accounts: &accounts.Registry{DB: e.mdb, Clock: e.clk, Root: filepath.Join(e.paths.Maquina, "contas"), OS: runtime.GOOS, Folders: accounts.OSFolders{}},
		Births:   sessionzero.BirthsDB{DB: e.mdb, Clock: e.clk}, Native: &classes.NativeSync{Adapters: reg, Files: files}, Files: files,
		Masker: e.deps.Masker,
	})
}

// machines are the engines the Citadel of a period reaches: this machine's
// Runtime, or the refusal that says its terminals are not wired.
func (e *Engine) machines() OrchestrationMachines {
	if e.rt == nil {
		return unwiredMachines{MachineSet: characters.MachineSet{}, local: e.id.MachineID}
	}
	return characters.MachineSet{e.id.MachineID: e.rt.rt}
}

// machineSheets are the sheets of the machines of the Guild the Citadel of the
// period chooses a machine from (machinesheet.Registry, which the period makes
// after the orchestration): before it exists there is none to choose from.
type machineSheets struct {
	reg *atomic.Pointer[machinesheet.Registry]
}

func (m machineSheets) Sheets(ctx context.Context) ([]machinesheet.Sheet, error) {
	r := m.reg.Load()
	if r == nil {
		return nil, errors.New("app: the sheets of the machines are not known yet")
	}
	return r.Sheets(ctx)
}

// reconcileLocal tells the Citadel that just took the period the states the
// Runtime of this machine has: the reports it made while no Citadel was there
// (a restart that found a terminal dead, a change of role) were refused, and a
// terminal the registry has alive and the machine does not is interrupted.
func (e *Engine) reconcileLocal(ctx context.Context, orc *Orchestration) {
	if e.rt == nil {
		return
	}
	if err := orc.Characters.Reconcile(ctx, e.id.MachineID, e.rt.rt.Live()); err != nil {
		e.log.Warn("app: the characters of this machine were not reconciled with the registry", "err", err)
	}
}
