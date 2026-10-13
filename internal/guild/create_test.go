package guild_test

import (
	"errors"
	"net"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/discovery"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/protocol"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/testkit/discorig"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// answerer puts a machine of an existing Guild on the fake network that
// answers the broadcast of a new install.
func answerer(t *testing.T, n *fakenet.Network, host, ip string, info discovery.Info) {
	t.Helper()
	n.SetIP(host, ip)
	resp, err := discovery.StartResponder(t.Context(), discovery.ResponderConfig{
		Net: discorig.Packets{Host: n.Host(host)}, Clock: clock.Real{},
		Info: func() (discovery.Info, bool) { return info, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resp.Close)
}

func searchFrom(n *fakenet.Network, host string) discovery.SearchConfig {
	return discovery.SearchConfig{Net: discorig.Packets{Host: n.Host(host)}, Clock: clock.Real{}}
}

func citadelInfo(guildID, id, name, ip string, machines int) discovery.Info {
	return discovery.Info{
		GuildID: guildID, MachineID: id, Name: name, Role: discovery.RoleCitadel, Epoch: 1, Port: 7443,
		Citadel:  &discovery.CitadelRef{MachineID: id, Addrs: []string{ip}, Port: 7443},
		Machines: machines, CAFp: "sha256:test",
	}
}

// CA-GUILDA-01
func TestAnInstallWithNoGuildOnTheNetworkCreatesOneAndBecomesItsCitadel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		clk := clock.Real{}
		nw := fakenet.New(clk)
		nw.SetIP("primeira", "192.168.1.30")
		n := bareNode(t, clk, "primeira")
		gdb := openDB(t, guildTemplate, "mestre.db", clk, guilddb.Open)
		secrets := &memSecrets{}

		res, err := guild.Install(ctx, guild.InstallConfig{
			Plan:   guild.PlanInput{Search: searchFrom(nw, "primeira")},
			Create: guild.CreateConfig{GuildDB: gdb, Secrets: secrets, Clock: clk, Name: "Primeira", Hostname: "primeira", IPs: []net.IP{net.ParseIP("192.168.1.30")}, Timezone: "America/Sao_Paulo"},
			Member: n.member,
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Plan.Action != guild.PlanCreate || res.Founded == nil || res.Joined != nil {
			t.Fatalf("result = %+v", res)
		}
		f := res.Founded
		if _, err := protocol.ParseULID(f.GuildID); err != nil {
			t.Errorf("guild_id %q is not a ULID: %v", f.GuildID, err)
		}
		if f.Epoch.Epoch != 1 || f.Epoch.CitadelMachineID != f.MachineID || !f.IsCitadel() {
			t.Errorf("certificate of epoch = %+v, want epoch 1 naming this machine", f.Epoch)
		}
		if err := guild.NewVerifier(f.CA.Certificate(), f.GuildID).Verify(f.Epoch); err != nil {
			t.Errorf("the certificate of epoch 1 does not verify: %v", err)
		}
		// the CA key is in the vault, and there only
		if got := secrets.names(); !slices.Equal(got, []string{"guild_ca_key"}) || secrets.types["guild_ca_key"] != vault.TypeCAKey {
			t.Errorf("vault holds %v", got)
		}
		loaded, err := guild.LoadCA(ctx, gdb, secrets, "issue a certificate", clk)
		if err != nil || string(loaded.DER()) != string(f.CA.DER()) || !slices.Equal(secrets.uses, []string{"issue a certificate"}) {
			t.Errorf("LoadCA = %v, %v; uses %v", loaded, err, secrets.uses)
		}
		// the Guild database and the machine's record
		var tz string
		var epoch int
		if err := gdb.Reader().QueryRowContext(ctx, `SELECT timezone, epoch FROM guild WHERE guild_id = ?`, f.GuildID).Scan(&tz, &epoch); err != nil || tz != "America/Sao_Paulo" || epoch != 1 {
			t.Errorf("guild row: %q, %d, %v", tz, epoch, err)
		}
		id, err := n.member.Identity(ctx)
		if err != nil || id.MachineID != f.MachineID || id.GuildID != f.GuildID || !id.IsCitadel() {
			t.Errorf("identity = %+v, %v", id, err)
		}
		if !n.keys.has() {
			t.Error("the engine key was not kept")
		}
		var machines, creds int
		_ = gdb.Reader().QueryRowContext(ctx, `SELECT count(*) FROM machines WHERE machine_id = ? AND state = 'active'`, f.MachineID).Scan(&machines)
		_ = gdb.Reader().QueryRowContext(ctx, `SELECT count(*) FROM machine_credentials WHERE machine_id = ?`, f.MachineID).Scan(&creds)
		if machines != 1 || creds != 2 {
			t.Errorf("registry: %d machines, %d credentials", machines, creds)
		}

		// a machine that is in a Guild does not make another
		if _, err := guild.Install(ctx, guild.InstallConfig{
			Plan: guild.PlanInput{NewGuild: true}, Member: n.member,
			Create: guild.CreateConfig{GuildDB: gdb, Secrets: secrets, Clock: clk, Name: "again", Hostname: "again"},
		}); !errors.Is(err, guild.ErrAlreadyMember) {
			t.Errorf("a second Create = %v, want ErrAlreadyMember", err)
		}
	})
}

// CA-GUILDA-01
func TestAnInstallThatOnlyHearsTowersCreatesNothingAndSaysTheCitadelIsAway(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		clk := clock.Real{}
		nw := fakenet.New(clk)
		nw.SetIP("novo", "192.168.1.40")
		answerer(t, nw, "torre", "192.168.1.21", discovery.Info{
			GuildID: "guild-1", MachineID: "torre", Name: "torre", Role: discovery.RoleTower, Epoch: 1, Port: 7443,
			Citadel:  &discovery.CitadelRef{MachineID: "citadel", Addrs: []string{"192.168.1.10"}, Port: 7443},
			Machines: 2, CAFp: "sha256:test",
		})
		n := bareNode(t, clk, "novo")
		gdb := openDB(t, guildTemplate, "mestre.db", clk, guilddb.Open)
		secrets := &memSecrets{}
		_, err := guild.Install(ctx, guild.InstallConfig{
			Plan:   guild.PlanInput{Search: searchFrom(nw, "novo")},
			Create: guild.CreateConfig{GuildDB: gdb, Secrets: secrets, Clock: clk, Name: "novo", Hostname: "novo"},
			Member: n.member,
		})
		if !errors.Is(err, guild.ErrCitadelDown) {
			t.Fatalf("Install = %v, want ErrCitadelDown", err)
		}
		if err.Error() != "a Guilda existe, mas a Cidadela está fora; tente de novo depois" {
			t.Errorf("message = %q", err)
		}
		var rows int
		_ = gdb.Reader().QueryRowContext(ctx, `SELECT count(*) FROM guild`).Scan(&rows)
		if _, err := n.member.Identity(ctx); !errors.Is(err, guild.ErrNotMember) || rows != 0 || len(secrets.names()) != 0 || n.keys.has() {
			t.Errorf("something was created: identity %v, %d Guilds, vault %v", err, rows, secrets.names())
		}
	})
}

// The three other outcomes of the search of an installer.
func TestTheInstallerPlansToEnterChooseOrCreateFromWhatAnswered(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		nw := fakenet.New(clock.Real{})
		nw.SetIP("novo", "192.168.1.40")
		answerer(t, nw, "c1", "192.168.1.10", citadelInfo("guild-1", "m1", "servidor", "192.168.1.10", 3))

		plan, err := guild.PlanInstall(ctx, guild.PlanInput{Search: searchFrom(nw, "novo")})
		if err != nil || plan.Action != guild.PlanJoin || plan.Join.GuildID != "guild-1" || plan.Join.Addr != "192.168.1.10:7443" ||
			plan.Join.CitadelName != "servidor" || plan.Join.Machines != 3 {
			t.Fatalf("one Guild: %+v, %v", plan, err)
		}
		// --new-guild creates even though another answers; --join uses only its address
		if plan, err := guild.PlanInstall(ctx, guild.PlanInput{NewGuild: true, Search: searchFrom(nw, "novo")}); err != nil || plan.Action != guild.PlanCreate {
			t.Errorf("--new-guild: %+v, %v", plan, err)
		}
		if plan, err := guild.PlanInstall(ctx, guild.PlanInput{JoinAddr: "casa.example:7443"}); err != nil || plan.Action != guild.PlanJoin || plan.Join.Addr != "casa.example:7443" {
			t.Errorf("--join: %+v, %v", plan, err)
		}

		answerer(t, nw, "c2", "192.168.1.11", citadelInfo("guild-2", "m2", "casa", "192.168.1.11", 1))
		plan, err = guild.PlanInstall(ctx, guild.PlanInput{Search: searchFrom(nw, "novo")})
		if err != nil || plan.Action != guild.PlanChoose || len(plan.Guilds) != 2 || plan.Guilds[0].CitadelName != "casa" || plan.Guilds[1].CitadelName != "servidor" {
			t.Fatalf("two Guilds: %+v, %v", plan, err)
		}
		// the installer asks the user, who picks the second
		n := bareNode(t, clock.Real{}, "novo")
		_, err = guild.Install(ctx, guild.InstallConfig{
			Plan: guild.PlanInput{Search: searchFrom(nw, "novo")}, Member: n.member,
			Choose: func(gs []guild.FoundGuild) (guild.FoundGuild, error) { return gs[1], nil },
			Join:   guild.JoinConfig{Network: nw.Host("novo"), Name: "novo", Hostname: "novo"},
		})
		if err == nil || errors.Is(err, guild.ErrAlreadyMember) {
			t.Errorf("entering the chosen Guild with nothing serving /join = %v, want a connection failure", err)
		}
		if _, err := guild.Install(ctx, guild.InstallConfig{Plan: guild.PlanInput{Search: searchFrom(nw, "novo")}, Member: n.member}); err == nil {
			t.Error("several Guilds and nobody to choose went on")
		}
	})
}
