package app

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/store"
	guilddb "github.com/mestre-ia/mestre/internal/store/guild"
	"github.com/mestre-ia/mestre/internal/store/machine"
	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/internal/vault"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Found creates a Guild in the folders of an instance, which becomes the
// Citadel of epoch 1 (docs/guilda/guilda.md#formação): the vault with its
// recovery key, the CA inside it, the certificates and the certificate of
// epoch 1, and config.json. It returns the recovery key, which the Owner
// keeps; the installer (milestone 7) shows it.
func Found(ctx context.Context, cfg Config, paths Paths, clk clock.Clock) (*guild.Founded, *vault.RecoveryKey, error) {
	if clk == nil {
		clk = clock.Real{}
	}
	if err := SaveConfig(paths, cfg); err != nil {
		return nil, nil, err
	}
	opts := store.Options{Clock: clk, Synchronous: cfg.Synchronous}
	mdb, err := machine.Open(ctx, paths.machineDB(), opts)
	if err != nil {
		return nil, nil, err
	}
	defer mdb.Close()
	gdb, err := guilddb.Open(ctx, paths.guildDB(), opts)
	if err != nil {
		return nil, nil, err
	}
	defer gdb.Close()
	rk, err := vault.NewRecoveryKey()
	if err != nil {
		return nil, nil, err
	}
	v, err := vault.Create(ctx, vault.Config{
		DB: gdb, Keys: vault.NewFileKeyStore(paths.vaultKey()), EnvelopePath: paths.envelope(),
		RecoveryPublicKey: rk.PublicKey(), Masker: logging.NewMasker(), Clock: clk,
	})
	if err != nil {
		rk.Destroy()
		return nil, nil, err
	}
	defer v.Close()
	f, err := guild.Create(ctx, guild.CreateConfig{
		GuildDB: gdb, Member: guild.NewMember(mdb, vault.NewFileKeyStore(paths.engineKey()), clk), Secrets: v, Clock: clk,
		Name: cfg.Name, Hostname: cfg.Hostname, Instance: cfg.Instance, IPs: parseIPs(cfg.IPs), EnginePort: cfg.EnginePort,
		RecoveryPublicKey: rk.PublicKey(), Timezone: systemTimezone(),
	})
	if err != nil {
		rk.Destroy()
		return nil, nil, err
	}
	return f, rk, nil
}

// Enroll makes an instance enter the Guild whose Citadel answers at addr
// (docs/guilda/guilda.md#entrada-na-guilda): the code goes to onCode, and
// the Owner answers through the Approver of the Citadel.
func Enroll(ctx context.Context, cfg Config, paths Paths, addr string, network transport.Network, clk clock.Clock, onCode func(string)) (*guild.Joined, error) {
	if clk == nil {
		clk = clock.Real{}
	}
	if network == nil {
		return nil, errors.New("app: Enroll needs a network")
	}
	if err := SaveConfig(paths, cfg); err != nil {
		return nil, err
	}
	mdb, err := machine.Open(ctx, paths.machineDB(), store.Options{Clock: clk, Synchronous: cfg.Synchronous})
	if err != nil {
		return nil, err
	}
	defer mdb.Close()
	return guild.Enroll(ctx, guild.NewMember(mdb, vault.NewFileKeyStore(paths.engineKey()), clk), guild.JoinConfig{
		Addr: addr, Network: network, Clock: clk, Name: cfg.Name, Hostname: cfg.Hostname, OS: osName(),
		Instance: cfg.Instance, IPs: parseIPs(cfg.IPs), EnginePort: cfg.EnginePort, OnCode: onCode,
	})
}

func parseIPs(ss []string) []net.IP {
	var out []net.IP
	for _, s := range ss {
		if ip := net.ParseIP(s); ip != nil {
			out = append(out, ip)
		}
	}
	return out
}

// systemTimezone is the IANA name of the time zone of the system of this
// machine, which becomes the zone of a new Guild (docs/guilda/guilda.md); UTC
// when the system does not say.
func systemTimezone() string {
	known := func(name string) bool {
		if name == "" || name == "Local" {
			return false
		}
		_, err := time.LoadLocation(name)
		return err == nil
	}
	if tz := strings.TrimPrefix(os.Getenv("TZ"), ":"); known(tz) {
		return tz
	}
	if link, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(filepath.ToSlash(link), "zoneinfo/"); ok && known(name) {
			return name
		}
	}
	if b, err := os.ReadFile("/etc/timezone"); err == nil {
		if name := strings.TrimSpace(string(b)); known(name) {
			return name
		}
	}
	if name := time.Local.String(); known(name) {
		return name
	}
	return "UTC"
}
