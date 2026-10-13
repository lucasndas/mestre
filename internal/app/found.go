package app

import (
	"context"
	"errors"
	"net"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
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
		RecoveryPublicKey: rk.PublicKey(),
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
