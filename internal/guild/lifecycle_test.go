package guild_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"errors"
	"net"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// CA-GUILDA-07
func TestACertificateInsideItsLastThirtyDaysRenewsOverTheOpenConnection(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *rig, w *pworld) {
		clk := r.clk.(*clock.Fake)
		n := r.newNode("tower1")
		j := r.enroll(n)
		tw := w.addTower(t, n, j)
		if !tw.link.Connected() {
			t.Fatal("the Tower is not connected")
		}
		session, old := tw.link.Session(), tw.src.Leaf()

		clk.Advance(334 * 24 * time.Hour)
		if tw.renewer.Due() {
			t.Fatal("a certificate with 31 days left is due")
		}
		clk.Advance(24 * time.Hour)
		if !tw.renewer.Due() {
			t.Fatal("a certificate with 30 days left is not due")
		}
		if err := tw.renewer.Renew(t.Context()); err != nil {
			t.Fatal(err)
		}

		leaf := tw.src.Leaf()
		oldID, _ := transport.IdentityOf(old)
		if id, err := transport.IdentityOf(leaf); err != nil || id != oldID {
			t.Errorf("identity after renewing = %v, %v; want %v", id, err, oldID)
		}
		if transport.Serial(leaf) == transport.Serial(old) || leaf.PublicKey.(*ecdsa.PublicKey).Equal(old.PublicKey) {
			t.Error("the renewed certificate has the old serial or the old key")
		}
		if d := leaf.NotAfter.Sub(clk.Now()); d < 364*24*time.Hour {
			t.Errorf("the new certificate lives %s", d)
		}
		if tw.renewer.Due() {
			t.Error("the new certificate is due at once")
		}
		if !tw.link.Connected() || tw.link.Session() != session {
			t.Error("renewing dropped the connection")
		}
		// what the machine keeps follows
		id, src, err := n.member.TLS(t.Context())
		if err != nil || transport.Serial(id.Motor) != transport.Serial(leaf) || transport.Serial(src.Leaf()) != transport.Serial(leaf) {
			t.Errorf("the identity kept the old certificate: %v", err)
		}
		if got := r.countWhere("machine_credentials", "machine_id = ? AND role = 'motor' AND revoked_at IS NULL", j.MachineID); got != 2 {
			t.Errorf("the registry has %d engine certificates of the machine, want the old one and the new one", got)
		}
	})
}

// A renewal that stops after the certificate was kept and before the key
// file was written leaves the machine able to start: the key waits in the
// pending file.
func TestARenewalStoppedBetweenTheCertificateAndTheKeyIsTakenAtTheNextStart(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *rig, w *pworld) {
		n := r.newNode("tower1")
		j := r.enroll(n)
		pending := &memKeys{}
		n.member.UsePendingKey(pending)
		key, _ := transport.GenerateKey()
		csr, _ := transport.NewCSR(key)
		cert, err := r.citadel.Renew(t.Context(), j.MachineID, transport.KindMachine, csr)
		if err != nil {
			t.Fatal(err)
		}
		// what SaveRenewal does before the stop: the pending key, then the
		// certificate
		pem, _ := guild.KeyPEM(key)
		if err := pending.Save(t.Context(), pem); err != nil {
			t.Fatal(err)
		}
		if err := n.member.SaveCertificate(t.Context(), transport.KindMachine, cert); err != nil {
			t.Fatal(err)
		}
		_, src, err := n.member.TLS(t.Context())
		if err != nil {
			t.Fatalf("the start after the stop: %v", err)
		}
		if transport.Serial(src.Leaf()) != transport.Serial(cert) {
			t.Error("the machine does not present the renewed certificate")
		}
		// and the key file has the new key now
		if _, _, err := guild.NewMember(n.mach, n.keys, r.clk).TLS(t.Context()); err != nil {
			t.Errorf("the key file after the start: %v", err)
		}
		// a whole SaveRenewal needs no pending file at the next start
		key2, _ := transport.GenerateKey()
		csr2, _ := transport.NewCSR(key2)
		cert2, err := r.citadel.Renew(t.Context(), j.MachineID, transport.KindMachine, csr2)
		if err != nil {
			t.Fatal(err)
		}
		if err := n.member.SaveRenewal(t.Context(), cert2, key2); err != nil {
			t.Fatal(err)
		}
		if _, src, err := guild.NewMember(n.mach, n.keys, r.clk).TLS(t.Context()); err != nil || transport.Serial(src.Leaf()) != transport.Serial(cert2) {
			t.Errorf("after SaveRenewal: %v", err)
		}
	})
}

// CA-GUILDA-07
func TestWhenTheIPsOfAMachineChangeItsCertificateFollowsAtOnce(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *rig, w *pworld) {
		n := r.newNode("tower1")
		j := r.enroll(n)
		tw := w.addTower(t, n, j)
		if tw.renewer.Due() {
			t.Fatal("a new certificate is due")
		}
		// the machine sheet brings the new address to the Citadel; the machine
		// sees its own address change
		ip := net.ParseIP("192.168.7.7")
		if err := r.citadel.UpdateNames(t.Context(), j.MachineID, "tower1", []net.IP{ip}); err != nil {
			t.Fatal(err)
		}
		tw.names.Store(&transport.Names{MachineID: j.MachineID, Hostname: "tower1", IPs: []net.IP{ip}})
		if !tw.renewer.Due() {
			t.Fatal("a changed IP does not make the certificate due")
		}
		session := tw.link.Session()
		if err := tw.renewer.Renew(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := tw.src.Leaf().IPAddresses; len(got) != 1 || !got[0].Equal(ip) {
			t.Errorf("IPs of the new certificate = %v, want %v", got, ip)
		}
		if tw.renewer.Due() || tw.link.Session() != session {
			t.Error("still due, or the connection fell")
		}
	})
}

// CA-GUILDA-07: the other half of the rule, "an expired certificate needs a
// new entry".
func TestAnExpiredCertificateIsNotRenewed(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *rig, w *pworld) {
		n := r.newNode("tower1")
		j := r.enroll(n)
		tw := w.addTower(t, n, j)
		r.clk.(*clock.Fake).Advance(366 * 24 * time.Hour)
		err := tw.renewer.Renew(t.Context())
		if !errors.Is(err, guild.ErrRenewal) {
			t.Fatalf("Renew = %v, want a refusal", err)
		}
		if got := r.countWhere("machine_credentials", "machine_id = ?", j.MachineID); got != 2 {
			t.Errorf("%d credentials of the machine, want the original 2", got)
		}
		key, err := transport.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		csr, err := transport.NewCSR(key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.citadel.Renew(t.Context(), j.MachineID, transport.KindMachine, csr); !errors.Is(err, guild.ErrExpiredCredential) {
			t.Errorf("Citadel.Renew of an expired certificate = %v, want ErrExpiredCredential", err)
		}
	})
}

// mtls dials the route of kind with the given credentials and returns both
// ends; a refusal at the handshake is the error.
func (r *rig) mtls(t *testing.T, kind transport.Kind, cert *x509.Certificate, key *ecdsa.PrivateKey, host string) (cl, srv transport.Conn, err error) {
	t.Helper()
	c, err := transport.NewClient(transport.ClientConfig{
		CA: r.founded.CA.Certificate(), Cert: transport.NewCertSource(cert, key), Network: r.net.Host(host), Clock: r.clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	route, ln := transport.RouteTower, r.towers
	if kind == transport.KindGuardian {
		route, ln = transport.RouteGuardian, r.guards
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	got := make(chan transport.Conn, 1)
	go func() {
		s, err := ln.Accept(ctx)
		if err != nil {
			got <- nil
			return
		}
		got <- s
	}()
	cl, err = c.Dial(ctx, citadelAddr, route)
	if err != nil {
		cancel()
		<-got
		return nil, nil, err
	}
	srv = <-got
	t.Cleanup(func() { _ = cl.CloseNow(); _ = srv.CloseNow() })
	return cl, srv, nil
}

// firstError keeps a reader on c and reports why it ended.
func firstError(c transport.Conn) <-chan error {
	out := make(chan error, 1)
	go func() {
		for {
			if _, _, err := c.Read(context.Background()); err != nil && !errors.Is(err, transport.ErrTooLarge) {
				out <- err
				return
			}
		}
	}()
	return out
}

// Part of CA-GUILDA-06 (it closes with the Guardian), on the Guild's side: the Guardian's own binary and its
// connection belong to the Guardian's stage, but its certificate and its route
// are the Guild's and are checked here.
func TestRemovingAnOnlineMachineTellsItToLeaveThenRevokesAndRefusesItsHandshakes(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *rig, w *pworld) {
		ctx := t.Context()
		r.peers.set(guild.ProtocolPeers{Server: w.srv, Transport: r.srv, Clock: clock.Real{}})
		a, b := r.newNode("tower-a"), r.newNode("tower-b")
		ja, jb := r.enroll(a), r.enroll(b)
		twA, twB := w.addTower(t, a, ja), w.addTower(t, b, jb)

		engA, _, err := r.mtls(t, transport.KindMachine, ja.Motor, ja.MotorKey, "tower-a")
		if err != nil {
			t.Fatal(err)
		}
		guardA, _, err := r.mtls(t, transport.KindGuardian, ja.Guardian, ja.GuardianKey, "tower-a")
		if err != nil {
			t.Fatal(err)
		}
		engB, srvB, err := r.mtls(t, transport.KindMachine, jb.Motor, jb.MotorKey, "tower-b")
		if err != nil {
			t.Fatal(err)
		}
		errEngA, errGuardA, errEngB := firstError(engA), firstError(guardA), firstError(engB)

		// the Citadel and a machine that is not the Citadel cannot be removed
		if _, err := r.citadel.Remove(ctx, r.founded.MachineID, "dono"); !errors.Is(err, guild.ErrCitadelProtected) {
			t.Errorf("removing the Citadel = %v", err)
		}
		// a machine that does not know the Citadel's word is not told to leave
		_, err = (&guild.TowerLeave{Member: b.member, Epochs: twB.epochs, Wiper: twB.wiper}).Handle(ctx, &protocol.Message{Peer: protocol.Peer{Role: protocol.RoleMachine, ID: "impostor"}})
		var perr *protocol.Error
		if !errors.As(err, &perr) || perr.Code != protocol.CodeForbidden || len(twB.wiper.wiped()) != 0 {
			t.Errorf("guild.leave from a machine that is not the Citadel = %v, wiped %v", err, twB.wiper.wiped())
		}

		res, err := r.citadel.Remove(ctx, ja.MachineID, "dono")
		if err != nil {
			t.Fatal(err)
		}
		if !res.Notified {
			t.Error("an online machine was not notified")
		}
		synctest.Wait()

		// it erased its share of the Guild and is outside
		if got := twA.wiper.wiped(); !slices.Equal(got, []string{"removed"}) {
			t.Errorf("wiped = %v", got)
		}
		select {
		case <-twA.left:
		default:
			t.Error("the services were not told to stop")
		}
		if _, err := a.member.Identity(ctx); !errors.Is(err, guild.ErrNotMember) {
			t.Errorf("the removed machine still has an identity: %v", err)
		}
		// its connections fell with 4002; the other machine's did not
		for name, ch := range map[string]<-chan error{"engine": errEngA, "guardian": errGuardA} {
			if err := <-ch; transport.CloseStatus(err) != transport.StatusRevoked {
				t.Errorf("%s connection ended with %v, want 4002", name, err)
			}
		}
		select {
		case err := <-errEngB:
			t.Fatalf("the connection of another machine ended: %v", err)
		default:
		}
		if err := srvB.Write(ctx, transport.MessageText, []byte("still here")); err != nil {
			t.Errorf("write to the other machine: %v", err)
		}
		// and no new handshake of either certificate goes through
		for _, k := range []struct {
			kind transport.Kind
			c    *x509.Certificate
			key  *ecdsa.PrivateKey
		}{{transport.KindMachine, ja.Motor, ja.MotorKey}, {transport.KindGuardian, ja.Guardian, ja.GuardianKey}} {
			if _, _, err := r.mtls(t, k.kind, k.c, k.key, "tower-a"); err == nil {
				t.Errorf("a revoked %s certificate connected", k.kind)
			}
		}
		if _, _, err := r.mtls(t, transport.KindMachine, jb.Motor, jb.MotorKey, "tower-b"); err != nil {
			t.Errorf("the other machine cannot connect: %v", err)
		}
		if !twB.link.Connected() {
			t.Error("the other Tower fell")
		}

		if got := r.countWhere("machine_credentials", "machine_id = ? AND revoked_at IS NOT NULL", ja.MachineID); got != 2 {
			t.Errorf("%d revoked credentials, want 2", got)
		}
		if r.countWhere("machines", "machine_id = ? AND state = 'removed' AND removed_at IS NOT NULL", ja.MachineID) != 1 {
			t.Error("the machine is not marked removed")
		}
		if r.countWhere("outbox", "peer = ?", ja.MachineID) != 0 {
			t.Error("messages still wait for a removed machine")
		}
		if cfg, err := r.citadel.Config(ctx); err != nil || len(cfg.Machines) != 2 || len(cfg.Revoked) != 2 {
			t.Errorf("guild.config = %+v, %v; want the Citadel and tower-b, and the two serials", cfg, err)
		}
		r.hooks.mu.Lock()
		if !slices.Contains(r.hooks.removed, ja.MachineID+":removed") {
			t.Errorf("Removed hook: %v", r.hooks.removed)
		}
		r.hooks.mu.Unlock()
		if _, err := r.citadel.Remove(ctx, ja.MachineID, "dono"); !errors.Is(err, guild.ErrNotActive) {
			t.Errorf("removing it twice = %v", err)
		}
		_ = twA
	})
}

// slowWiper erases when it is let, not when the context ends.
type slowWiper struct {
	started chan struct{}
	release chan struct{}
}

func (w *slowWiper) Wipe(context.Context, string) error {
	close(w.started)
	<-w.release
	return nil
}

// CA-GUILDA-14
// The Citadel waits 10 s for the answer to guild.leave and revokes the machine
// whatever it says; the connection falls with 4002, and the context of the handler
// with it. A wipe that takes longer finishes all the same, and the machine
// forgets its identity: one that outlived the wipe would be a machine with
// nothing, that the Guild no longer takes and that cannot enter again.
func TestAMachineThatErasesForLongerThanTheCitadelWaitsStillLeaves(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *rig, w *pworld) {
		n := r.newNode("tower-a")
		tw := w.addTower(t, n, r.enroll(n))
		wiper := &slowWiper{started: make(chan struct{}), release: make(chan struct{})}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := (&guild.TowerLeave{Member: n.member, Epochs: tw.epochs, Wiper: wiper}).Handle(ctx, &protocol.Message{
				Peer: protocol.Peer{Role: protocol.RoleMachine, ID: tw.epochs.Citadel()}})
			done <- err
		}()
		synctest.Wait()
		<-wiper.started
		cancel() // the Citadel gave up and closed the connection
		synctest.Wait()
		close(wiper.release)
		if err := <-done; err != nil {
			t.Errorf("guild.leave = %v, want the departure done", err)
		}
		if _, err := n.member.Identity(t.Context()); !errors.Is(err, guild.ErrNotMember) {
			t.Errorf("the machine that erased still has an identity: %v", err)
		}
	})
}

// Part of CA-GUILDA-06, which closes with the Guardian.
func TestAMachineRemovedWhileOfflineIsRevokedAndNotNotified(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *rig, w *pworld) {
		r.peers.set(guild.ProtocolPeers{Server: w.srv, Transport: r.srv, Clock: clock.Real{}})
		n := r.newNode("sumida")
		j := r.enroll(n) // never connected over the protocol
		res, err := r.citadel.Remove(t.Context(), j.MachineID, "dono")
		if err != nil {
			t.Fatal(err)
		}
		if res.Notified {
			t.Error("an offline machine cannot have been notified: the panel must recommend replacing the secrets")
		}
		if _, _, err := r.mtls(t, transport.KindMachine, j.Motor, j.MotorKey, "sumida"); err == nil {
			t.Error("a removed machine connected")
		}
		if got := r.citadel.Credentials().Revoked(); len(got) != 2 {
			t.Errorf("revoked = %v", got)
		}
	})
}

// Part of CA-GUILDA-08 (it closes in M7), on the Guild's side: the installer that refuses is milestone 7.
func TestTheCitadelCannotBeUninstalledWhileTowersAreInTheGuild(t *testing.T) {
	t.Parallel()
	bubble(t, func(t *testing.T, r *rig, w *pworld) {
		ctx := t.Context()
		if err := r.citadel.CanUninstall(ctx); err != nil {
			t.Fatalf("a Citadel alone cannot be uninstalled: %v", err)
		}
		n := r.newNode("tower1")
		j := r.enroll(n)
		tw := w.addTower(t, n, j)
		if err := r.citadel.CanUninstall(ctx); !errors.Is(err, guild.ErrTowersRemain) {
			t.Fatalf("CanUninstall with a Tower = %v, want ErrTowersRemain", err)
		}

		// the Tower uninstalls: it asks to leave, erases its share, and the
		// Citadel is free to uninstall afterwards
		ack, err := n.member.Uninstall(ctx, tw.link, tw.wiper, r.clk, 10*time.Second)
		if err != nil || !ack {
			t.Fatalf("Uninstall = %v, %v", ack, err)
		}
		if got := tw.wiper.wiped(); !slices.Equal(got, []string{"uninstall"}) {
			t.Errorf("wiped = %v", got)
		}
		if _, err := n.member.Identity(ctx); !errors.Is(err, guild.ErrNotMember) {
			t.Errorf("identity after uninstalling: %v", err)
		}
		if r.countWhere("machines", "machine_id = ? AND state = 'left'", j.MachineID) != 1 ||
			r.countWhere("machine_credentials", "machine_id = ? AND revoked_at IS NULL", j.MachineID) != 0 {
			t.Error("the Citadel did not mark the machine as left and revoke its certificates")
		}
		if err := r.citadel.CanUninstall(ctx); err != nil {
			t.Errorf("CanUninstall after the Tower left = %v", err)
		}
		r.hooks.mu.Lock()
		if !slices.Contains(r.hooks.removed, j.MachineID+":uninstall") {
			t.Errorf("Removed hook: %v", r.hooks.removed)
		}
		r.hooks.mu.Unlock()
	})
}

func TestATowerUninstallsEvenWithTheCitadelAway(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	n := r.newNode("tower1")
	r.enroll(n)
	w := &wiper{}
	ack, err := n.member.Uninstall(t.Context(), nil, w, r.clk, time.Second)
	if err != nil || ack {
		t.Fatalf("Uninstall = %v, %v; want it done and not acknowledged", ack, err)
	}
	if got := w.wiped(); !slices.Equal(got, []string{"uninstall"}) {
		t.Errorf("wiped = %v", got)
	}
	if _, err := n.member.Identity(t.Context()); !errors.Is(err, guild.ErrNotMember) {
		t.Errorf("identity: %v", err)
	}
	// the Guild still counts it, for the Owner to remove
	if ms, _ := r.citadel.Machines(t.Context()); len(ms) != 2 || ms[0].State != guild.MachineActive || ms[1].State != guild.MachineActive {
		t.Errorf("machines = %+v", ms)
	}

	// a wipe that fails leaves the identity for another try
	n2 := r.newNode("tower2")
	r.enroll(n2)
	bad := &wiper{err: errors.New("disk busy")}
	if _, err := n2.member.Uninstall(t.Context(), nil, bad, r.clk, time.Second); err == nil {
		t.Fatal("a failed erasure was reported as done")
	}
	if _, err := n2.member.Identity(t.Context()); err != nil {
		t.Errorf("the identity was forgotten before the erasure: %v", err)
	}
}

// `mestre join` on a machine that left: it enters again with a new machine_id.
func TestAMachineThatLeftCanEnterAgainButAMemberCannotJoinTwice(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	n := r.newNode("volta")
	first := r.enroll(n)
	cfg, _ := r.joinConfig(n, citadelAddr)
	if _, err := guild.Enroll(t.Context(), n.member, cfg); !errors.Is(err, guild.ErrAlreadyMember) {
		t.Fatalf("joining while in the Guild = %v, want ErrAlreadyMember", err)
	}
	if err := n.member.Leave(t.Context(), &wiper{}, "uninstall"); err != nil {
		t.Fatal(err)
	}
	second := r.enroll(n)
	if second.MachineID == first.MachineID {
		t.Error("the machine came back with its old machine_id")
	}
	if got := r.count("machines"); got != 3 {
		t.Errorf("%d machines; the history of the old one stays", got)
	}
}

// CA-GUILDA-15
// Leaving takes the private key of the engine with it, the one waiting for a
// renewal included, whether the Citadel removed the machine or `mestre
// uninstall` asked; a failed wipe keeps the keys, with the identity (CA-GUILDA-15).
func TestLeavingErasesTheEngineKeyAndTheOneWaitingForARenewal(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"removed", "uninstall"} {
		t.Run(how, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, clock.NewFake(day0))
			n := r.newNode("tower-" + how)
			pending := &memKeys{}
			n.member.UsePendingKey(pending)
			r.enroll(n)
			if err := pending.Save(t.Context(), []byte("a key waiting for its certificate")); err != nil {
				t.Fatal(err)
			}
			if !n.keys.has() {
				t.Fatal("the machine has no engine key after the entry")
			}
			if err := n.member.Leave(t.Context(), &wiper{err: errors.New("disk busy")}, how); err == nil {
				t.Fatal("a failed wipe was reported as done")
			}
			if !n.keys.has() || !pending.has() {
				t.Error("a failed wipe erased the keys: the machine would keep its identity without them")
			}
			if err := n.member.Leave(t.Context(), &wiper{}, how); err != nil {
				t.Fatal(err)
			}
			if n.keys.has() {
				t.Error("the engine key is still there")
			}
			if pending.has() {
				t.Error("the key waiting for a renewal is still there")
			}
		})
	}
}

// CA-GUILDA-16
// Leaving empties what the machine database held of the Guild (the outbox for
// the old Citadel above all: a new Citadel must not receive it) and keeps the
// account profiles and the worktrees, whose folders stay on disk
// (CA-GUILDA-16).
func TestLeavingEmptiesWhatTheMachineDatabaseHeldOfTheGuildAndKeepsTheLocalProfiles(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	n := r.newNode("tower-leaves")
	r.enroll(n)
	ctx := t.Context()
	exec := func(q string) {
		t.Helper()
		if _, err := n.mach.Writer().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO outbox (peer, id, type, body, created_at) VALUES ('citadel', 'm1', 'guild.discarded', x'7b7d', 1)`)
	exec(`INSERT INTO processed_commands (sender, id, type, processed_at) VALUES ('citadel', 'c1', 'x', 1)`)
	exec(`INSERT INTO known_addresses (addr, source, added_at) VALUES ('10.0.0.1:7000', 'mdns', 1)`)
	exec(`INSERT INTO account_profiles (name, provider, dir, is_default, setup_step, setup_detail, created_at) VALUES ('p', 'claude', '/contas/p', 1, 'done', '', 1)`)
	count := func(table string) int {
		t.Helper()
		var c int
		if err := n.mach.Reader().QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	for _, tb := range []string{"outbox", "processed_commands", "known_addresses"} {
		if count(tb) != 1 {
			t.Fatalf("%s: setup", tb)
		}
	}
	if err := n.member.Leave(ctx, &wiper{err: errors.New("disk busy")}, "removed"); err == nil {
		t.Fatal("a failed wipe was reported as done")
	}
	if count("outbox") != 1 {
		t.Error("a failed wipe emptied the outbox")
	}
	if err := n.member.Leave(ctx, &wiper{}, "removed"); err != nil {
		t.Fatal(err)
	}
	for _, tb := range machine.GuildBound {
		if c := count(tb); c != 0 {
			t.Errorf("%s still has %d rows", tb, c)
		}
	}
	if count("account_profiles") != 1 {
		t.Error("the account profile was erased: its login stays in contas/")
	}
}
