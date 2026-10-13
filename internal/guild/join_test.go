package guild_test

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// rawRequest is the request of a machine that talks to the Citadel without the
// Join client: for the tests of the limits and of the commitment.
func rawRequest(t *testing.T, name string) (guild.JoinRequest, []byte) {
	t.Helper()
	var csrs [2][]byte
	for i := range csrs {
		key, err := transport.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		if csrs[i], err = transport.NewCSR(key); err != nil {
			t.Fatal(err)
		}
	}
	nonce := make([]byte, guild.NonceSize)
	copy(nonce, name)
	h := sha256.Sum256(nonce)
	return guild.JoinRequest{
		Name: name, Hostname: name, CSRMotor: csrs[0], CSRGuardian: csrs[1], Commitment: h[:],
	}, nonce
}

// ask runs the two first steps of an entry and returns the question the Owner
// got.
func (r *rig) ask(t *testing.T, name, ip string) (guild.JoinTicket, guild.ApprovalRequest) {
	t.Helper()
	req, nonce := rawRequest(t, name)
	ticket, err := r.citadel.Begin(t.Context(), req, ip)
	if err != nil {
		t.Fatalf("Begin(%s): %v", name, err)
	}
	if err := r.citadel.Reveal(t.Context(), ticket.JoinID, nonce); err != nil {
		t.Fatalf("Reveal(%s): %v", name, err)
	}
	return ticket, <-r.approver.asked
}

// CA-GUILDA-02
func TestBothSidesComputeTheSameCodeAndTheOwnerAcceptingIssuesTheCertificates(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(day0)
	r := newRig(t, clk)
	n := r.newNode("desktop-ana")
	j := r.startJoin(n)

	q := <-r.approver.asked
	code := <-j.code
	if q.Code != code || len(code) != 7 || code[3] != ' ' {
		t.Fatalf("the Owner sees %q, the new machine shows %q; want the same six digits", q.Code, code)
	}
	if q.Name != "desktop-ana" || q.OS != "linux" || q.ExpiresAt != day0.Add(time.Hour) {
		t.Fatalf("question = %+v", q)
	}
	r.expectNothingIssued() // nothing is issued before the answer

	if err := r.owner.answer(q.JoinID, "0000", guild.Decision{Accept: true}); !errors.Is(err, errBadPIN) {
		t.Fatalf("answer with a wrong PIN = %v", err)
	}
	r.expectNothingIssued() // the bot refused the PIN: the Guild never heard of it
	if err := r.owner.answer(q.JoinID, "1234", guild.Decision{Accept: true}); err != nil {
		t.Fatal(err)
	}

	got, err := j.result()
	if err != nil {
		t.Fatal(err)
	}
	if got.Code != code || got.GuildID != r.founded.GuildID || got.Name != "desktop-ana" || got.Excluded {
		t.Fatalf("joined = %+v", got.Identity)
	}
	if got.Epoch.Epoch != 1 || got.Epoch.CitadelMachineID != r.founded.MachineID {
		t.Fatalf("certificate of epoch = %+v, want epoch 1 naming the founder", got.Epoch)
	}
	wantURI := map[transport.Kind]string{
		transport.KindMachine:  "mestre://machine/" + got.MachineID,
		transport.KindGuardian: "mestre://guardian/" + got.MachineID,
	}
	for kind, cert := range map[transport.Kind]*x509.Certificate{transport.KindMachine: got.Motor, transport.KindGuardian: got.Guardian} {
		if len(cert.URIs) != 1 || cert.URIs[0].String() != wantURI[kind] {
			t.Errorf("%s certificate URIs = %v, want %s", kind, cert.URIs, wantURI[kind])
		}
		// never mestre.local: only the certificate of the acting Citadel has it
		if !slices.Equal(cert.DNSNames, []string{"desktop-ana.local"}) {
			t.Errorf("%s certificate names = %v, want only desktop-ana.local", kind, cert.DNSNames)
		}
		if len(cert.IPAddresses) != 1 || !cert.IPAddresses[0].Equal(net.ParseIP("192.168.1.20")) {
			t.Errorf("%s certificate IPs = %v", kind, cert.IPAddresses)
		}
		if _, err := cert.Verify(x509.VerifyOptions{Roots: r.founded.CA.Pool(), CurrentTime: clk.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
			t.Errorf("%s certificate does not chain to the CA: %v", kind, err)
		}
		if d := cert.NotAfter.Sub(clk.Now()); d < 364*24*time.Hour || d > 366*24*time.Hour {
			t.Errorf("%s certificate lives %s, want a year", kind, d)
		}
	}
	if len(got.Config.Machines) != 2 || got.Config.Timezone != "America/Sao_Paulo" {
		t.Errorf("configuration = %+v, want the two machines and the Guild time zone", got.Config)
	}

	// what the machine keeps: the identity, the engine key, the way to connect
	id, src, err := n.member.TLS(t.Context())
	if err != nil || id.MachineID != got.MachineID || src.Leaf().Raw == nil || !n.keys.has() {
		t.Fatalf("TLS() = %+v, %v", id, err)
	}
	r.hooks.mu.Lock()
	defer r.hooks.mu.Unlock()
	if len(r.hooks.joined) != 1 || r.hooks.joined[0].ID != got.MachineID {
		t.Errorf("Joined hook = %+v", r.hooks.joined)
	}
	if r.count("machines") != 2 || r.count("machine_credentials") != 4 {
		t.Errorf("registry: %d machines, %d credentials", r.count("machines"), r.count("machine_credentials"))
	}
}

// CA-GUILDA-02
func TestAnOwnerWhoRefusesIssuesNothingAndTheMachineKeepsNoCA(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	n := r.newNode("intruso")
	j := r.startJoin(n)
	q := <-r.approver.asked
	if err := r.owner.answer(q.JoinID, "1234", guild.Decision{Accept: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.result(); !errors.Is(err, guild.ErrJoinRefused) {
		t.Fatalf("join = %v, want refused or expired", err)
	}
	r.expectNothingIssued()
	if _, err := n.member.Identity(t.Context()); !errors.Is(err, guild.ErrNotMember) || n.keys.has() {
		t.Errorf("the refused machine kept something: %v, key %v", err, n.keys.has())
	}
	if err := r.citadel.Decide(t.Context(), q.JoinID, guild.Decision{Accept: true}); !errors.Is(err, guild.ErrNotPending) {
		t.Errorf("accepting after the refusal = %v, want ErrNotPending", err)
	}
	r.expectNothingIssued()
}

// CA-GUILDA-03
func TestAMachineInTheMiddleMakesTheTwoCodesDiffer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		viaEvil  bool
		wantSame bool
	}{{"direct to the Citadel", false, true}, {"through a machine in the middle", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			clk := clock.NewFake(day0)
			r := newRig(t, clk)
			addr := citadelAddr
			if tc.viaEvil {
				// The intermediary relays the entry to the real Citadel but
				// shows the new machine a certificate of its own.
				evilCA, err := transport.NewCA("evil", clk)
				if err != nil {
					t.Fatal(err)
				}
				evil, _, _ := r.serve("mitm", "mitm:7443", evilCA.Certificate(), newCert(t, evilCA, "citadel"))
				r.citadel.Mount(evil)
				addr = "mitm:7443"
			}
			n := r.newNode("laptop")
			j := r.startJoinAt(n, addr)
			q := <-r.approver.asked
			code := <-j.code
			if same := q.Code == code; same != tc.wantSame {
				t.Fatalf("the Owner sees %q and the machine shows %q", q.Code, code)
			}
			// the Owner compares: accepts when equal, refuses when not
			if err := r.owner.answer(q.JoinID, "1234", guild.Decision{Accept: tc.wantSame}); err != nil {
				t.Fatal(err)
			}
			if _, err := j.result(); tc.wantSame != (err == nil) {
				t.Fatalf("join = %v", err)
			}
			if !tc.wantSame {
				r.expectNothingIssued()
			}
		})
	}
}

func newCert(t *testing.T, ca *transport.CA, id string) *transport.CertSource {
	t.Helper()
	key, err := transport.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	csr, err := transport.NewCSR(key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := ca.IssueMachine(csr, transport.Names{MachineID: id, Hostname: id})
	if err != nil {
		t.Fatal(err)
	}
	return transport.NewCertSource(cert, key)
}

// closing makes every answer of the routes it mounts end its connection, so
// the next request of the client opens a new one.
type closing struct{ m guild.Mounter }

func (c closing) Handle(pattern string, h http.Handler) {
	c.m.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		h.ServeHTTP(w, r)
	}))
}

// flipNet sends the first connection of the client to one address and the
// others to another: a machine in the middle that takes over after the first.
type flipNet struct {
	transport.Network
	first, then string
	calls       atomic.Int32
}

func (f *flipNet) DialContext(ctx context.Context, addr string) (net.Conn, error) {
	if f.calls.Add(1) > 1 {
		addr = f.then
	}
	return f.Network.DialContext(ctx, addr)
}

// A machine that shows another certificate in the middle of the entry would
// make the code on the screen vouch for a connection it did not compute: the
// entry stops.
func TestAnEntryWhoseCertificateChangesInTheMiddleStops(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(day0)
	r := newRig(t, clk)
	real, _, _ := r.serve("real2", "real2:7443", r.founded.CA.Certificate(), r.cert)
	r.citadel.Mount(closing{real})
	evilCA, err := transport.NewCA("evil", clk)
	if err != nil {
		t.Fatal(err)
	}
	evil, _, _ := r.serve("mitm", "mitm:7443", evilCA.Certificate(), newCert(t, evilCA, "citadel"))
	r.citadel.Mount(closing{evil})

	n := r.newNode("laptop")
	cfg, _ := r.joinConfig(n, "real2:7443")
	cfg.Network = &flipNet{Network: r.net.Host("laptop"), first: "real2:7443", then: "mitm:7443"}
	if _, err := guild.Enroll(t.Context(), n.member, cfg); !errors.Is(err, guild.ErrChannelChanged) {
		t.Fatalf("Enroll = %v, want ErrChannelChanged", err)
	}
	if _, err := n.member.Identity(t.Context()); !errors.Is(err, guild.ErrNotMember) {
		t.Errorf("the machine entered: %v", err)
	}
}

// CA-GUILDA-04
func TestARequestOlderThanAnHourIsExpiredAndNothingIsCreated(t *testing.T) {
	t.Parallel()
	t.Run("an hour and a minute", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(day0)
		r := newRig(t, clk)
		n := r.newNode("tarde")
		j := r.startJoin(n)
		q := <-r.approver.asked
		<-j.code
		clk.Advance(time.Hour + time.Minute)
		if err := r.owner.answer(q.JoinID, "1234", guild.Decision{Accept: true}); !errors.Is(err, guild.ErrExpired) {
			t.Fatalf("Decide = %v, want ErrExpired", err)
		}
		if _, err := j.result(); !errors.Is(err, guild.ErrJoinRefused) {
			t.Fatalf("join = %v, want refused or expired", err)
		}
		r.expectNothingIssued()
		if w := r.approver.withdrawals(); len(w) != 1 || w[0] != "approval-1" {
			t.Errorf("the question was withdrawn %v, want once", w)
		}
		if _, err := n.member.Identity(t.Context()); !errors.Is(err, guild.ErrNotMember) {
			t.Errorf("the machine entered: %v", err)
		}
	})
	t.Run("59 minutes still accepts", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(day0)
		r := newRig(t, clk)
		n := r.newNode("pontual")
		j := r.startJoin(n)
		q := <-r.approver.asked
		<-j.code
		clk.Advance(59 * time.Minute)
		if err := r.owner.answer(q.JoinID, "1234", guild.Decision{Accept: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := j.result(); err != nil {
			t.Fatalf("join = %v", err)
		}
	})
	t.Run("the sweep ends it and takes the question down", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(day0)
		r := newRig(t, clk)
		_, q := r.ask(t, "esquecido", "10.0.0.9")
		clk.Advance(time.Hour)
		if n, err := r.citadel.Sweep(t.Context()); err != nil || n != 1 {
			t.Fatalf("Sweep = %d, %v", n, err)
		}
		if w := r.approver.withdrawals(); len(w) != 1 {
			t.Errorf("withdrawn = %v", w)
		}
		if err := r.citadel.Decide(t.Context(), q.JoinID, guild.Decision{Accept: true}); !errors.Is(err, guild.ErrExpired) {
			t.Errorf("Decide = %v", err)
		}
		if n, _ := r.citadel.Sweep(t.Context()); n != 0 {
			t.Errorf("a second sweep ended %d", n)
		}
	})
}

// B1 of the audit: the CA is a root the browsers trust, so a request whose
// names a certificate may not carry is refused before anything is recorded
// or asked.
func TestARequestWithNamesACertificateMayNotCarryIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	many := make([]string, transport.MaxIPs+1)
	for i := range many {
		many[i] = fmt.Sprintf("10.0.0.%d", i+1)
	}
	for name, mod := range map[string]func(*guild.JoinRequest){
		"the name of the panel":   func(q *guild.JoinRequest) { q.Hostname = "mestre" },
		"a host name with dots":   func(q *guild.JoinRequest) { q.Hostname = "accounts.google.com" },
		"a wildcard host name":    func(q *guild.JoinRequest) { q.Hostname = "*" },
		"a public address":        func(q *guild.JoinRequest) { q.IPs = []string{"8.8.8.8"} },
		"a global IPv6 address":   func(q *guild.JoinRequest) { q.IPs = []string{"2001:db8::1"} },
		"more than 16 addresses":  func(q *guild.JoinRequest) { q.IPs = many },
		"something that is no IP": func(q *guild.JoinRequest) { q.IPs = []string{"servidor"} },
	} {
		req, _ := rawRequest(t, "m1")
		mod(&req)
		if _, err := r.citadel.Begin(t.Context(), req, "new1"); !errors.Is(err, guild.ErrBadRequest) {
			t.Errorf("%s: Begin = %v, want ErrBadRequest", name, err)
		}
	}
	if r.count("join_requests") != 0 || r.approver.questions() != 0 {
		t.Fatal("a refused request was recorded or reached the Owner")
	}
	req, _ := rawRequest(t, "m1")
	req.Hostname, req.IPs = "Desk-01", []string{"192.168.1.5", "fd00::5", "fe80::5", "127.0.0.1"}
	if _, err := r.citadel.Begin(t.Context(), req, "new1"); err != nil {
		t.Fatalf("names inside the rules: %v", err)
	}
}

// CA-GUILDA-05
func TestTheEleventhRequestOfAnAddressInAnHourIsRefusedWithoutAskingTheOwner(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(day0)
	r := newRig(t, clk)
	for i := range 10 {
		_, q := r.ask(t, fmt.Sprintf("m%d", i), "new1")
		if err := r.owner.answer(q.JoinID, "1234", guild.Decision{Accept: false}); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.approver.questions(); got != 10 {
		t.Fatalf("the Owner was asked %d times", got)
	}
	req, _ := rawRequest(t, "m11")
	if _, err := r.citadel.Begin(t.Context(), req, "new1"); !errors.Is(err, guild.ErrRateLimited) {
		t.Fatalf("11th Begin = %v, want ErrRateLimited", err)
	}
	// the same over the wire: the client sees the refusal, the Owner nothing
	n := r.newNode("new1")
	if _, err := r.startJoin(n).result(); !errors.Is(err, guild.ErrRateLimited) {
		t.Fatalf("11th join over the wire = %v, want ErrRateLimited", err)
	}
	if got := r.approver.questions(); got != 10 {
		t.Fatalf("the Owner was asked %d times, want still 10", got)
	}
	// another address is not limited, and the hour passing frees this one
	if _, err := r.citadel.Begin(t.Context(), req, "other"); err != nil {
		t.Errorf("another address: %v", err)
	}
	clk.Advance(time.Hour + time.Second)
	req, _ = rawRequest(t, "m12")
	if _, err := r.citadel.Begin(t.Context(), req, "new1"); err != nil {
		t.Errorf("after an hour: %v", err)
	}
}

func TestOnlyFiveRequestsWaitForTheOwnerAtOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	var first guild.ApprovalRequest
	for i := range 5 {
		_, q := r.ask(t, fmt.Sprintf("p%d", i), fmt.Sprintf("10.0.0.%d", i))
		if i == 0 {
			first = q
		}
	}
	req, _ := rawRequest(t, "p5")
	if _, err := r.citadel.Begin(t.Context(), req, "10.0.0.50"); !errors.Is(err, guild.ErrTooManyPending) {
		t.Fatalf("6th pending = %v, want ErrTooManyPending", err)
	}
	if err := r.owner.answer(first.JoinID, "1234", guild.Decision{Accept: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.citadel.Begin(t.Context(), req, "10.0.0.50"); err != nil {
		t.Errorf("after one was answered: %v", err)
	}
}

func TestTwentyRefusalsInADayAlertTheOwner(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	for i := range 21 {
		_, q := r.ask(t, fmt.Sprintf("r%d", i), fmt.Sprintf("10.0.1.%d", i))
		if err := r.owner.answer(q.JoinID, "1234", guild.Decision{Accept: false}); err != nil {
			t.Fatal(err)
		}
		r.hooks.mu.Lock()
		got := slices.Clone(r.hooks.alerts)
		r.hooks.mu.Unlock()
		if want := i >= 19; want != (len(got) == 1) || (want && got[0] != 20) {
			t.Fatalf("after %d refusals the alerts are %v", i+1, got)
		}
	}
}

func TestTheCommitmentBindsTheNonceAndARepeatedRevealAsksOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	ctx := t.Context()

	req, nonce := rawRequest(t, "honest")
	tk, err := r.citadel.Begin(ctx, req, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.citadel.Code(ctx, tk.JoinID); !errors.Is(err, guild.ErrNotRevealed) {
		t.Errorf("Code before the reveal = %v", err)
	}
	if err := r.citadel.Decide(ctx, tk.JoinID, guild.Decision{Accept: true}); !errors.Is(err, guild.ErrNotRevealed) {
		t.Errorf("Decide before the reveal = %v", err)
	}
	for range 2 {
		if err := r.citadel.Reveal(ctx, tk.JoinID, nonce); err != nil {
			t.Fatal(err)
		}
	}
	if r.approver.questions() != 1 {
		t.Errorf("the Owner was asked %d times for one request", r.approver.questions())
	}
	if c, err := r.citadel.Code(ctx, tk.JoinID); err != nil || c != (<-r.approver.asked).Code {
		t.Errorf("Code = %q, %v", c, err)
	}

	// another nonce than the committed one ends the request
	req2, _ := rawRequest(t, "liar")
	tk2, err := r.citadel.Begin(ctx, req2, "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.citadel.Reveal(ctx, tk2.JoinID, make([]byte, guild.NonceSize)); !errors.Is(err, guild.ErrBadReveal) {
		t.Fatalf("Reveal with another nonce = %v", err)
	}
	if st, _ := r.citadel.Status(ctx, tk2.JoinID, 0); st.State != guild.JoinRefused {
		t.Errorf("state = %s, want refused", st.State)
	}
	if r.approver.questions() != 1 {
		t.Errorf("the Owner was asked about a bad reveal")
	}

	// malformed requests are refused before anything is stored
	bad := req
	bad.Commitment = []byte("short")
	if _, err := r.citadel.Begin(ctx, bad, "10.0.0.3"); !errors.Is(err, guild.ErrBadRequest) {
		t.Errorf("short commitment = %v", err)
	}
	bad = req
	bad.CSRGuardian = bad.CSRMotor
	if _, err := r.citadel.Begin(ctx, bad, "10.0.0.3"); !errors.Is(err, guild.ErrBadRequest) {
		t.Errorf("one key for both = %v", err)
	}
	if r.countWhere("join_requests", "remote_ip = ?", "10.0.0.3") != 0 {
		t.Error("a malformed request was stored")
	}
}

func TestAQuestionThatFailedIsAskedAgainOnTheNextReveal(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	r.approver.askErr = errors.New("telegram is down")
	req, nonce := rawRequest(t, "m")
	tk, err := r.citadel.Begin(t.Context(), req, "10.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.citadel.Reveal(t.Context(), tk.JoinID, nonce); err == nil {
		t.Fatal("Reveal hid the failure of the question")
	}
	r.approver.mu.Lock()
	r.approver.askErr = nil
	r.approver.mu.Unlock()
	if err := r.citadel.Reveal(t.Context(), tk.JoinID, nonce); err != nil {
		t.Fatal(err)
	}
	if q := <-r.approver.asked; q.JoinID != tk.JoinID {
		t.Errorf("asked about %s", q.JoinID)
	}
}

func TestTwoMachinesWithTheSameNameGetASuffixAndTheOwnerCanExcludeAtTheEntry(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	a, b := r.newNode("notebook"), r.newNode("notebook")
	r.enroll(a)
	r.approver.mu.Lock()
	r.approver.auto = func(req guild.ApprovalRequest) {
		_ = r.owner.answer(req.JoinID, "1234", guild.Decision{Accept: true, Excluded: true})
	}
	r.approver.mu.Unlock()
	got, err := r.startJoin(b).result()
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "notebook-2" || !got.Excluded {
		t.Fatalf("second machine = %q, excluded %v; want notebook-2 and excluded", got.Name, got.Excluded)
	}
	if slices.Contains(got.Motor.DNSNames, "mestre.local") {
		t.Errorf("an excluded machine's certificate carries mestre.local: %v", got.Motor.DNSNames)
	}
	ms, err := r.citadel.Machines(t.Context())
	excluded := map[string]bool{}
	for _, m := range ms {
		excluded[m.Name] = m.Excluded
	}
	if err != nil || len(ms) != 3 || !excluded["notebook-2"] || excluded["notebook"] || excluded["servidor"] {
		t.Errorf("machines = %+v, %v", ms, err)
	}
}

func TestASuspendedCitadelTakesNoEntries(t *testing.T) {
	t.Parallel()
	var suspended bool
	r := newRig(t, clock.NewFake(day0), func(c *guild.CitadelConfig) { c.Suspended = func() bool { return suspended } })
	suspended = true
	req, _ := rawRequest(t, "m")
	if _, err := r.citadel.Begin(t.Context(), req, "10.0.0.1"); !errors.Is(err, guild.ErrSuspended) {
		t.Fatalf("Begin = %v", err)
	}
	if _, err := r.startJoin(r.newNode("m")).result(); !errors.Is(err, guild.ErrCitadelUnavailable) {
		t.Fatalf("join over the wire = %v, want ErrCitadelUnavailable (503)", err)
	}
	suspended = false
	if _, err := r.citadel.Begin(t.Context(), req, "10.0.0.1"); err != nil {
		t.Errorf("after the suspension: %v", err)
	}
}

// The new machine trusts nothing it is not sure of: an approval that does not
// fit the keys it made, or that is not signed by the CA that came with it,
// keeps nothing.
func TestAMachineKeepsNothingFromAnApprovalThatDoesNotHoldTogether(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(day0)
	r := newRig(t, clk)
	ctx := t.Context()

	// A false Citadel that approves at once; mutate decides what is wrong.
	for i, tc := range []struct {
		name   string
		mutate func(b *guild.Bundle, ca, other *transport.CA)
	}{
		{"a certificate for another key", func(b *guild.Bundle, ca, _ *transport.CA) {
			b.Motor = issueFor(t, ca, transport.KindMachine, b.MachineID).Raw
		}},
		{"a certificate of another CA", func(b *guild.Bundle, _, other *transport.CA) {
			b.Guardian = issueFor(t, other, transport.KindGuardian, b.MachineID).Raw
		}},
		{"the identity of another machine", func(b *guild.Bundle, _, _ *transport.CA) { b.MachineID = "someone-else" }},
		{"an unsigned certificate of epoch", func(b *guild.Bundle, _, _ *transport.CA) { b.Epoch.Signature = "AAAA" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := r.newNode("victim")
			ca, err := transport.NewCA("g", clk)
			if err != nil {
				t.Fatal(err)
			}
			other, err := transport.NewCA("g2", clk)
			if err != nil {
				t.Fatal(err)
			}
			addr := r.falseCitadel(t, "false-"+strconv.Itoa(i), ca, other, tc.mutate)
			cfg, _ := r.joinConfig(n, addr)
			if _, err := guild.Enroll(ctx, n.member, cfg); !errors.Is(err, guild.ErrBadBundle) {
				t.Fatalf("Enroll = %v, want ErrBadBundle", err)
			}
			if _, err := n.member.Identity(ctx); !errors.Is(err, guild.ErrNotMember) || n.keys.has() {
				t.Errorf("the machine kept something: %v, key %v", err, n.keys.has())
			}
		})
	}
}

// issueFor issues a certificate of kind for machineID, for a key made here.
func issueFor(t *testing.T, ca *transport.CA, k transport.Kind, machineID string) *x509.Certificate {
	t.Helper()
	key, err := transport.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	csr, err := transport.NewCSR(key)
	if err != nil {
		t.Fatal(err)
	}
	n := transport.Names{MachineID: machineID, Hostname: "x"}
	if k == transport.KindGuardian {
		c, err := ca.IssueGuardian(csr, n)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c, err := ca.IssueMachine(csr, n)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// falseCitadel serves /join like a Citadel of the Guild of ca that accepts
// everything and signs a perfect bundle, then lets mutate spoil it. It returns
// its address.
func (r *rig) falseCitadel(t *testing.T, host string, ca, other *transport.CA, mutate func(*guild.Bundle, *transport.CA, *transport.CA)) string {
	t.Helper()
	clk := r.clk
	name := host
	cert := newCert(t, ca, "false")
	s, _, _ := r.serve(name, name+":7443", ca.Certificate(), cert)
	var csrs struct {
		motor, guardian []byte
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /join", func(w http.ResponseWriter, req *http.Request) {
		var body guild.JoinRequest
		if !decode(w, req, &body) {
			return
		}
		csrs.motor, csrs.guardian = body.CSRMotor, body.CSRGuardian
		reply(w, 201, guild.JoinTicket{JoinID: "j1", NonceC: make([]byte, guild.NonceSize), ExpiresAt: clk.Now().Add(time.Hour)})
	})
	mux.HandleFunc("POST /join/{id}/reveal", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("GET /join/{id}", func(w http.ResponseWriter, _ *http.Request) {
		motor, err := ca.IssueMachine(csrs.motor, transport.Names{MachineID: "m-new", Hostname: "victim"})
		if err != nil {
			t.Error(err)
			return
		}
		guardian, err := ca.IssueGuardian(csrs.guardian, transport.Names{MachineID: "m-new", Hostname: "victim"})
		if err != nil {
			t.Error(err)
			return
		}
		epoch, err := guild.SignEpoch(ca, certOfEpoch("g", 1, "false", clk.Now()))
		if err != nil {
			t.Error(err)
			return
		}
		b := guild.Bundle{GuildID: "g", MachineID: "m-new", Name: "victim", CA: ca.DER(), Motor: motor.Raw, Guardian: guardian.Raw, Epoch: epoch}
		mutate(&b, ca, other)
		reply(w, 200, guild.JoinStatus{State: guild.JoinApproved, Bundle: &b})
	})
	s.Handle("/join", mux)
	s.Handle("/join/", mux)
	return name + ":7443"
}
