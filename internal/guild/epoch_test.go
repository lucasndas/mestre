package guild_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// signedEpoch is the certificate of an epoch signed by ca.
func signedEpoch(t *testing.T, ca *transport.CA, guildID string, epoch uint64, citadel string) protocol.Certificate {
	t.Helper()
	c, err := guild.SignEpoch(ca, certOfEpoch(guildID, epoch, citadel, day0))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// CA-REPL-07
func TestTheCertificateOfEpochVerifiesOnlyWithTheSignatureOfTheCAOfTheGuild(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(day0)
	ca, err := transport.NewCA("guild-1", clk)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := transport.NewCA("guild-1", clk) // same guild_id, another CA
	if err != nil {
		t.Fatal(err)
	}
	v := guild.NewVerifier(ca.Certificate(), "guild-1")
	good := signedEpoch(t, ca, "guild-1", 2, "m1")
	if err := v.Verify(good); err != nil {
		t.Fatalf("a good certificate: %v", err)
	}
	for name, tc := range map[string]struct {
		cert protocol.Certificate
		want error
	}{
		"another epoch":        {func() protocol.Certificate { c := good; c.Epoch = 3; return c }(), transport.ErrBadSignature},
		"another Citadel":      {func() protocol.Certificate { c := good; c.CitadelMachineID = "m2"; return c }(), transport.ErrBadSignature},
		"another approval":     {func() protocol.Certificate { c := good; c.ApprovalID = "other"; return c }(), transport.ErrBadSignature},
		"another time":         {func() protocol.Certificate { c := good; c.IssuedAt.Time = c.IssuedAt.Add(time.Second); return c }(), transport.ErrBadSignature},
		"no signature":         {func() protocol.Certificate { c := good; c.Signature = ""; return c }(), transport.ErrBadSignature},
		"not base64":           {func() protocol.Certificate { c := good; c.Signature = "!!"; return c }(), transport.ErrBadSignature},
		"signed by another CA": {signedEpoch(t, foreign, "guild-1", 2, "m1"), transport.ErrBadSignature},
		"another Guild":        {signedEpoch(t, ca, "guild-2", 2, "m1"), guild.ErrWrongGuild},
	} {
		if err := v.Verify(tc.cert); !errors.Is(err, tc.want) {
			t.Errorf("%s: Verify = %v, want %v", name, err, tc.want)
		}
	}
	if _, err := guild.SignEpoch(ca, protocol.Certificate{GuildID: "guild-1"}); err == nil {
		t.Error("a certificate that names no Citadel was signed")
	}
}

// CA-REPL-07
func TestAMachineKeepsTheHighestCertificateItSawAndRefusesTheRest(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	ctx := t.Context()
	var mu sync.Mutex
	var refused []uint64
	var followed []uint64
	build := func() *guild.Epochs {
		return loadEpochs(t, r.founder, guild.EpochsConfig{
			OnRefused: func(c protocol.Certificate, _ error) { mu.Lock(); refused = append(refused, c.Epoch); mu.Unlock() },
			OnFollow: func(_ context.Context, c protocol.Certificate) error {
				followed = append(followed, c.Epoch)
				return nil
			},
		})
	}
	e := build()
	ca, gid, me := r.founded.CA, r.founded.GuildID, r.founded.MachineID
	if e.Epoch() != 1 || e.Certificate().CitadelMachineID != me || e.Citadel() != me {
		t.Fatalf("starts at %+v", e.Certificate())
	}

	if err := e.Follow(ctx, signedEpoch(t, ca, gid, 2, "m2")); err != nil {
		t.Fatal(err)
	}
	if e.Epoch() != 2 || e.Citadel() != "m2" {
		t.Fatalf("after following: epoch %d, Citadel %s", e.Epoch(), e.Citadel())
	}
	for name, tc := range map[string]struct {
		cert protocol.Certificate
		want error
	}{
		"a lower epoch":         {signedEpoch(t, ca, gid, 1, me), guild.ErrStaleCertificate},
		"the same epoch":        {signedEpoch(t, ca, gid, 2, "m3"), guild.ErrStaleCertificate},
		"a bad signature":       {func() protocol.Certificate { c := signedEpoch(t, ca, gid, 3, "m3"); c.Signature = "AAAA"; return c }(), transport.ErrBadSignature},
		"another Guild's epoch": {signedEpoch(t, ca, "other", 3, "m3"), guild.ErrWrongGuild},
	} {
		if err := e.Follow(ctx, tc.cert); !errors.Is(err, tc.want) {
			t.Errorf("%s: Follow = %v, want %v", name, err, tc.want)
		}
		if err := e.Verify(tc.cert); tc.want != guild.ErrStaleCertificate && err == nil {
			t.Errorf("%s: Verify accepted it", name)
		}
	}
	if e.Epoch() != 2 {
		t.Errorf("a refused certificate moved the epoch to %d", e.Epoch())
	}
	mu.Lock()
	slices.Sort(refused)
	if !slices.Equal(refused, []uint64{1, 2, 3, 3, 3, 3}) {
		t.Errorf("refusals recorded: %v, want one per refused certificate", refused)
	}
	mu.Unlock()
	if !slices.Equal(followed, []uint64{2}) {
		t.Errorf("OnFollow saw %v, want only the epoch that was kept", followed)
	}

	// it survives a restart: the database keeps the highest
	if got := build().Epoch(); got != 2 {
		t.Errorf("after reading the database again: epoch %d, want 2", got)
	}
	if id, err := r.founder.member.Identity(ctx); err != nil || id.Epoch.Epoch != 2 {
		t.Errorf("identity: %+v, %v", id.Epoch, err)
	}

	// a refusal by the hook keeps the old certificate
	veto := loadEpochs(t, r.founder, guild.EpochsConfig{OnFollow: func(context.Context, protocol.Certificate) error { return errors.New("demotion failed") }})
	if err := veto.Follow(ctx, signedEpoch(t, ca, gid, 4, "m4")); err == nil || veto.Epoch() != 2 {
		t.Errorf("a vetoed epoch was followed: %v, epoch %d", err, veto.Epoch())
	}

	// a peer that says our epoch is old reaches the stage that refreshes it
	stale := make(chan string, 1)
	loadEpochs(t, r.founder, guild.EpochsConfig{OnStale: func(_ context.Context, p protocol.Peer) { stale <- p.ID }}).Stale(ctx, protocol.Peer{ID: "citadel-2"})
	if got := <-stale; got != "citadel-2" {
		t.Errorf("Stale peer = %s", got)
	}
}

func TestTheIdentityOfAMachineSurvivesReopeningItsDatabaseAndIsNeverReplacedByALowerEpoch(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	ctx := t.Context()
	n := r.newNode("tower1")
	j := r.enroll(n)
	if err := n.member.SaveEpoch(ctx, signedEpoch(t, r.founded.CA, r.founded.GuildID, 5, "m5")); err != nil {
		t.Fatal(err)
	}
	if err := n.member.SaveEpoch(ctx, signedEpoch(t, r.founded.CA, r.founded.GuildID, 4, "m4")); !errors.Is(err, guild.ErrStaleCertificate) {
		t.Fatalf("saving a lower epoch = %v", err)
	}
	path := n.mach.Path()
	if err := n.mach.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := machine.Open(ctx, path, store.Options{Clock: r.clk, Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id, err := guild.NewMember(db, n.keys, r.clk).Identity(ctx)
	if err != nil || id.MachineID != j.MachineID || id.Epoch.Epoch != 5 || id.Epoch.CitadelMachineID != "m5" || id.IsCitadel() {
		t.Fatalf("identity after reopening = %+v, %v", id, err)
	}
	if string(id.CA.Raw) != string(j.CA.Raw) || string(id.Motor.Raw) != string(j.Motor.Raw) || string(id.Guardian.Raw) != string(j.Guardian.Raw) {
		t.Error("a certificate changed in the database")
	}
}

func TestACitadelOfAHigherEpochBringsAStaleTowerForwardAndAFalseOneDoesNot(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		forged bool
	}{{"a certificate signed by the CA", false}, {"a certificate that is not", true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				r := newRig(t, clock.NewFake(day0))
				n := r.newNode("tower1")
				j := r.enroll(n) // joined at epoch 1
				id := r.founded.MachineID

				// the Guild moved on to epoch 2, still with this Citadel
				w := &pworld{r: r, router: protocol.NewRouter(), closes: &closeCounter{}, nw: protorig.Network{Net: r.net}}
				c2 := signedEpoch(t, r.founded.CA, r.founded.GuildID, 2, id)
				if err := r.founder.member.SaveEpoch(ctx, c2); err != nil {
					t.Fatal(err)
				}
				w.epochs = loadEpochs(t, r.founder, guild.EpochsConfig{})
				cfg, _ := protorig.Config(clock.Real{}, id, 2, id)
				cfg.Epoch, cfg.Certs, cfg.Router = w.epochs, w.epochs, w.router
				if tc.forged {
					cfg.Epoch = protorig.NewEpochs(2, id) // signature "ok": not the CA's
				}
				srv, err := protocol.NewServer(cfg)
				if err != nil {
					t.Fatal(err)
				}
				lis, err := w.nw.Listen(protoAddr, protocol.Peer{Role: protocol.RoleMachine, ID: id})
				if err != nil {
					t.Fatal(err)
				}
				go func() { _ = srv.Serve(ctx, lis) }()

				tw := w.addTower(t, n, j)
				got, err := n.member.Identity(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if tc.forged {
					select {
					case c := <-tw.refused:
						if c.Epoch != 2 {
							t.Errorf("refused epoch %d", c.Epoch)
						}
					default:
						t.Error("the false certificate was not refused and recorded")
					}
					if tw.link.Connected() || tw.epochs.Epoch() != 1 || got.Epoch.Epoch != 1 {
						t.Errorf("the Tower followed a false certificate: connected %v, epoch %d, kept %d", tw.link.Connected(), tw.epochs.Epoch(), got.Epoch.Epoch)
					}
					return
				}
				if !tw.link.Connected() || tw.epochs.Epoch() != 2 || got.Epoch.Epoch != 2 {
					t.Errorf("the Tower did not follow epoch 2: connected %v, epoch %d, kept %d", tw.link.Connected(), tw.epochs.Epoch(), got.Epoch.Epoch)
				}
			})
		})
	}
}
