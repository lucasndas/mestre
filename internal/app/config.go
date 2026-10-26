package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/privdir"
	"github.com/mestre-ia/mestre/internal/replication"
	"github.com/mestre-ia/mestre/internal/terminal"
)

// Defaults of an instance (docs/arquitetura/componentes.md#instâncias).
const (
	// DefaultInstance is the name of the default instance.
	DefaultInstance = "principal"
	// DefaultEnginePort is the engine port of the default instance; the
	// others take blocks of 10 above it.
	DefaultEnginePort = 7443
)

// Config is the configuration of an instance, the file config.json of its
// configuration folder. The installer writes it (milestone 7); the full
// suite writes it for the instances it runs as ordinary processes.
type Config struct {
	Instance string   `json:"instance"`
	Name     string   `json:"name"`
	Hostname string   `json:"hostname"`
	IPs      []string `json:"ips,omitempty"`
	// Bind is the address the engine port listens on; empty is every one.
	Bind       string `json:"bind,omitempty"`
	EnginePort int    `json:"engine_port"`
	// DiscoveryPort is the UDP port of the broadcast (7445, shared by the
	// instances); NoDiscovery is `descoberta: false`.
	DiscoveryPort int  `json:"discovery_port,omitempty"`
	NoDiscovery   bool `json:"no_discovery,omitempty"`
	// Addresses is guild.endereco: the addresses tried first, "host:port".
	Addresses []string `json:"addresses,omitempty"`
	// Synchronous is the synchronous level of the databases ("" is NORMAL).
	Synchronous string `json:"synchronous,omitempty"`
	// Terminais is where the terminals of the characters come from (the
	// folder of the mestre-pty, the socket of the launcher); nil starts none,
	// and the start of a character says so.
	Terminais *Terminals `json:"terminais,omitempty"`
}

// PortBlock is the engine port of the instance with this index: 7443 for the
// default one, then 7453, 7463…
func PortBlock(index int) int { return DefaultEnginePort + 10*index }

// Paths are the folders of an instance (docs/arquitetura/persistencia.md#arquivos).
type Paths struct {
	// Config holds config.json, the key of the vault and the key of the
	// engine; Banco the Guild database; Maquina the machine database.
	Config, Banco, Maquina string
	// Agentes holds the folder of each character: the snapshot policy.json
	// of its effective rules and its MESTRE_TMP.
	Agentes string
	// Workspaces, ClassesGlobais, Modelos and Operador are the folders of the
	// Citadel that every Tower keeps a copy of (replication.Files); empty
	// leaves that folder out.
	Workspaces, ClassesGlobais, Modelos, Operador string
	// CLISocket is the socket of the local channel; empty serves none.
	CLISocket string
	// HookQueue is where `mestre hook` leaves the events while the engine
	// is away; empty reads none.
	HookQueue string
	// CLIAccess is who may connect to CLISocket: on an installed instance
	// the access of docs/arquitetura/canal-local.md#endpoint (0660 and the
	// group of the agents of the instance on Linux, the ACL on Windows); nil leaves the
	// socket as the system created it (`mestre run --dir`, the tests).
	CLIAccess localchan.Access
	// TerminalAccess grants the logon SID of each terminal on CLISocket
	// while the terminal lives (Windows; nil elsewhere and in the tests).
	TerminalAccess terminal.Access
}

// cliAccess is CLIAccess, or none.
func (p Paths) cliAccess() localchan.Access {
	if p.CLIAccess == nil {
		return localchan.NoAccess{}
	}
	return p.CLIAccess
}

// DefaultPaths are the folders of an installed instance.
func DefaultPaths(instance string) (Paths, error) {
	if !localchan.ValidInstance(instance) {
		return Paths{}, fmt.Errorf("app: invalid instance name %q", instance)
	}
	sock, err := localchan.SocketPath(instance)
	if err != nil {
		return Paths{}, err
	}
	if runtime.GOOS == "windows" {
		// The root of the socket (%ProgramData%, or C:\ProgramData when it
		// is not set): <root>\Mestre\<inst>\run\cli.sock. The dispatcher
		// finds the folders from the socket the same way.
		programData := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(sock))))
		root := filepath.Join(programData, "Mestre", instance)
		return Paths{
			Config: filepath.Join(root, "config"), Banco: filepath.Join(root, "banco"),
			Workspaces: filepath.Join(root, "workspaces"), ClassesGlobais: filepath.Join(root, "classes-globais"),
			Modelos: filepath.Join(root, "modelos"), Operador: filepath.Join(root, "operador"),
			Maquina: filepath.Join(root, "maquina"), Agentes: filepath.Join(root, "maquina", "agentes"), CLISocket: sock,
			HookQueue: hook.QueueDir("windows", programData, instance), CLIAccess: localchan.DefaultAccess(instance),
			TerminalAccess: localchan.DefaultTerminalAccess(sock),
		}, nil
	}
	return Paths{
		Config: filepath.Join("/etc/mestre", instance), Banco: filepath.Join("/var/lib/mestre", instance, "banco"),
		Workspaces: filepath.Join("/var/lib/mestre", instance, "workspaces"), ClassesGlobais: filepath.Join("/var/lib/mestre", instance, "classes-globais"),
		Modelos: filepath.Join("/var/lib/mestre", instance, "modelos"), Operador: filepath.Join("/var/lib/mestre", instance, "operador"),
		Maquina: filepath.Join("/var/lib/mestre", instance, "maquina"), Agentes: filepath.Join("/var/lib/mestre", instance, "agentes"),
		CLISocket: sock, HookQueue: hook.QueueDir("linux", "", instance), CLIAccess: localchan.DefaultAccess(instance),
	}, nil
}

// DirPaths puts every folder of an instance under dir: the instances of the
// full suite and of development (`mestre run --dir`).
func DirPaths(dir string) Paths {
	return Paths{
		Config: filepath.Join(dir, "config"), Banco: filepath.Join(dir, "banco"),
		Workspaces: filepath.Join(dir, "workspaces"), ClassesGlobais: filepath.Join(dir, "classes-globais"),
		Modelos: filepath.Join(dir, "modelos"), Operador: filepath.Join(dir, "operador"),
		Maquina: filepath.Join(dir, "maquina"), Agentes: filepath.Join(dir, "agentes"), CLISocket: filepath.Join(dir, "run", "cli.sock"),
		HookQueue: filepath.Join(dir, "fila-hook"),
	}
}

// The files of an instance.
func (p Paths) configFile() string    { return filepath.Join(p.Config, "config.json") }
func (p Paths) engineKey() string     { return filepath.Join(p.Config, "motor.key") }
func (p Paths) pendingKey() string    { return filepath.Join(p.Config, "motor.key.nova") }
func (p Paths) vaultKey() string      { return filepath.Join(p.Config, "cofre.key") }
func (p Paths) guildDB() string       { return filepath.Join(p.Banco, "mestre.db") }
func (p Paths) envelope() string      { return filepath.Join(p.Banco, "cofre-envelope.bin") }
func (p Paths) mirrorBundles() string { return filepath.Join(p.Banco, "espelhos") }
func (p Paths) snapshots() string     { return filepath.Join(p.Banco, "copias") }
func (p Paths) transfers() string     { return filepath.Join(p.Banco, "transferencias") }
func (p Paths) machineDB() string     { return filepath.Join(p.Maquina, "maquina.db") }
func (p Paths) discarded(e uint64) string {
	return filepath.Join(p.Banco, fmt.Sprintf("descartado-%d.db", e))
}

// droppedActions keeps the external actions the demotion of epoch e dropped,
// until the report of what was discarded carries them.
func (p Paths) droppedActions(e uint64) string {
	return filepath.Join(p.Banco, fmt.Sprintf("descartado-%d-acoes.json", e))
}

// discardedPrefix starts the name of everything kept of a discarded database:
// the database (descartado-<epoch>.db, or with a suffix when the epoch was
// archived twice), its -wal and -shm, the report (descartado-<epoch>.json) and
// the dropped actions.
const discardedPrefix = "descartado-"

// leftovers are the files of the time this machine was the Citadel that a
// Tower does not use and leaving the Guild erases (replication.ReplicaConfig):
// the discarded databases with what goes with them, and the full copies that
// were being made to send. The transfers of the Tower are erased as well, by
// their own folder.
func (p Paths) leftovers() []replication.Leftover {
	return []replication.Leftover{{Dir: p.Banco, Prefix: discardedPrefix}, {Dir: p.snapshots()}}
}

// ensure makes the folders, private.
func (p Paths) ensure() error {
	for _, d := range []string{p.Config, p.Banco, p.Maquina} {
		if err := privdir.Ensure(d); err != nil {
			return fmt.Errorf("app: %w", err)
		}
	}
	return nil
}

// LoadConfig reads config.json.
func LoadConfig(p Paths) (Config, error) {
	b, err := os.ReadFile(p.configFile())
	if err != nil {
		return Config{}, fmt.Errorf("app: read the configuration: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("app: %s: %w", p.configFile(), err)
	}
	return c, c.check()
}

// SaveConfig writes config.json.
func SaveConfig(p Paths, c Config) error {
	if err := c.check(); err != nil {
		return err
	}
	if err := p.ensure(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	if err := os.WriteFile(p.configFile(), append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("app: write the configuration: %w", err)
	}
	return nil
}

func (c Config) check() error {
	switch {
	case !localchan.ValidInstance(c.Instance):
		return fmt.Errorf("app: invalid instance name %q", c.Instance)
	case c.EnginePort <= 0 || c.EnginePort > 65535:
		return fmt.Errorf("app: invalid engine port %d", c.EnginePort)
	case c.Name == "" && c.Hostname == "":
		return errors.New("app: the configuration names no machine")
	}
	if c.Terminais != nil {
		return c.Terminais.check()
	}
	return nil
}
