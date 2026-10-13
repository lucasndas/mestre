package guild

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Errors of the joining machine.
var (
	// ErrJoinRefused is the "entrada recusada ou expirada" of the installer:
	// the Owner refused, the hour passed, or the request was not accepted.
	// The machine keeps no CA and nothing is created in the Guild.
	ErrJoinRefused = errors.New("guild: join refused or expired")
	// ErrCitadelUnavailable: the Citadel answered 503 (suspended) or could
	// not be reached.
	ErrCitadelUnavailable = errors.New("guild: the Citadel does not take entries now")
	// ErrChannelChanged: the certificate the Citadel presents changed in the
	// middle of the entry, so the code on the screen no longer vouches for
	// the connection. Start again.
	ErrChannelChanged = errors.New("guild: the certificate of the Citadel changed during the entry")
	// ErrBadBundle: what the Citadel sent does not hold together.
	ErrBadBundle = errors.New("guild: the answer of the Citadel is not valid")
)

// DefaultPollWait is how long each GET /join/<id> waits.
const DefaultPollWait = 30 * time.Second

// JoinConfig is what a machine says about itself to ask for the entry.
type JoinConfig struct {
	// Addr is "host:port" of the engine of the Citadel (from the discovery
	// or from `mestre install --join`).
	Addr    string
	Network transport.Network
	Clock   clock.Clock

	Name string
	// Hostname is the computer name; only its first label is sent. Of IPs,
	// only the addresses a certificate may carry are (transport.CertificateIPs).
	Hostname string
	OS       string
	Instance string
	IPs      []net.IP
	// EnginePort is the engine port of this instance.
	EnginePort int
	Sheet      json.RawMessage

	// OnCode is called with the code to show: "Código de entrada: 123 456".
	OnCode func(code string)
	// PollWait is the wait of each poll (30 s).
	PollWait time.Duration
	// Rand is the source of the nonce and of the keys; nil means crypto/rand.
	Rand io.Reader
}

// Joined is what the machine holds when the Owner accepted it: the identity,
// the certificates and the two private keys, which were made on this machine
// and have never left it. The caller keeps the keys in their protected files
// (Member.Adopt stores the rest, Member.SaveEngineKey the engine key).
type Joined struct {
	Identity
	MotorKey    *ecdsa.PrivateKey
	GuardianKey *ecdsa.PrivateKey
	// Name is the name the Guild gave this machine.
	Name     string
	Excluded bool
	Config   protocol.GuildConfig
	// Code is the code that was compared.
	Code string
}

// Join asks a Citadel for the entry and waits for the Owner: it makes the
// two keys, sends the request, shows the code, and waits for the answer
// (docs/guilda/guilda.md#entrada-na-guilda). What it receives is checked
// before it is returned: the certificates must chain to the CA that came with
// them, carry the identity the Guild gave this machine and match the keys made
// here, and the certificate of epoch must carry the signature of that CA.
func Join(ctx context.Context, cfg JoinConfig) (*Joined, error) {
	if cfg.Network == nil || cfg.Addr == "" {
		return nil, errors.New("guild: Join needs Addr and Network")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Reader
	}
	if cfg.PollWait <= 0 {
		cfg.PollWait = DefaultPollWait
	}
	motorKey, err := transport.GenerateKey()
	if err != nil {
		return nil, err
	}
	guardKey, err := transport.GenerateKey()
	if err != nil {
		return nil, err
	}
	csrMotor, err := transport.NewCSR(motorKey)
	if err != nil {
		return nil, err
	}
	csrGuard, err := transport.NewCSR(guardKey)
	if err != nil {
		return nil, err
	}
	nonceN := make([]byte, NonceSize)
	if _, err := io.ReadFull(cfg.Rand, nonceN); err != nil {
		return nil, fmt.Errorf("guild: draw a nonce: %w", err)
	}

	ch := newChannel(cfg.Network, cfg.Addr)
	defer ch.close()
	var ticket JoinTicket
	req := JoinRequest{
		Name: cfg.Name, OS: cfg.OS, Hostname: transport.HostLabel(cfg.Hostname), Instance: cfg.Instance,
		EnginePort: cfg.EnginePort, Sheet: cfg.Sheet, CSRMotor: csrMotor, CSRGuardian: csrGuard, Commitment: commitment(nonceN),
	}
	for _, ip := range transport.CertificateIPs(cfg.IPs) {
		req.IPs = append(req.IPs, ip.String())
	}
	if err := ch.do(ctx, http.MethodPost, RouteJoin, req, &ticket); err != nil {
		return nil, err
	}
	if err := ch.do(ctx, http.MethodPost, RouteJoin+"/"+ticket.JoinID+"/reveal", RevealBody{NonceN: nonceN}, nil); err != nil {
		return nil, err
	}
	fp, ok := ch.fingerprint()
	if !ok {
		return nil, ErrChannelChanged
	}
	code := JoinCode(fp, [][]byte{csrMotor, csrGuard}, nonceN, ticket.NonceC)
	if cfg.OnCode != nil {
		cfg.OnCode(code)
	}

	path := RouteJoin + "/" + ticket.JoinID + "?wait=" + strconv.Itoa(int(cfg.PollWait/time.Second))
	for {
		var st JoinStatus
		if err := ch.do(ctx, http.MethodGet, path, nil, &st); err != nil {
			return nil, err
		}
		switch st.State {
		case JoinPending:
			continue
		case JoinApproved:
			j, err := accept(st.Bundle, motorKey, guardKey, cfg.Clock.Now())
			if err != nil {
				return nil, err
			}
			j.Code = code
			return j, nil
		default:
			return nil, ErrJoinRefused
		}
	}
}

// accept checks a bundle against everything this machine knows before it
// trusts it, and builds the Joined.
func accept(b *Bundle, motorKey, guardKey *ecdsa.PrivateKey, now time.Time) (*Joined, error) {
	if b == nil {
		return nil, fmt.Errorf("%w: an approval without the certificates", ErrBadBundle)
	}
	bad := func(format string, args ...any) (*Joined, error) {
		return nil, fmt.Errorf("%w: %s", ErrBadBundle, fmt.Sprintf(format, args...))
	}
	ca, err := x509.ParseCertificate(b.CA)
	if err != nil || !ca.IsCA {
		return bad("the CA certificate")
	}
	motor, err := x509.ParseCertificate(b.Motor)
	if err != nil {
		return bad("the engine certificate")
	}
	guardian, err := x509.ParseCertificate(b.Guardian)
	if err != nil {
		return bad("the Guardian certificate")
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	for _, c := range []struct {
		cert *x509.Certificate
		kind transport.Kind
		key  *ecdsa.PrivateKey
	}{{motor, transport.KindMachine, motorKey}, {guardian, transport.KindGuardian, guardKey}} {
		if _, err := c.cert.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
			return bad("a %s certificate does not chain to the CA: %v", c.kind, err)
		}
		id, err := transport.IdentityOf(c.cert)
		if err != nil || id.Kind != c.kind || id.MachineID != b.MachineID {
			return bad("a %s certificate speaks for %v, not for %s", c.kind, id, b.MachineID)
		}
		if pub, ok := c.cert.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&c.key.PublicKey) {
			return bad("a %s certificate is not for the key made on this machine", c.kind)
		}
	}
	if err := NewVerifier(ca, b.GuildID).Verify(b.Epoch); err != nil {
		return bad("the certificate of epoch: %v", err)
	}
	if b.Epoch.CitadelMachineID == "" {
		return bad("the certificate of epoch names no Citadel")
	}
	return &Joined{
		Identity: Identity{
			MachineID: b.MachineID, GuildID: b.GuildID, CA: ca, Motor: motor, Guardian: guardian,
			Epoch: b.Epoch, JoinedAt: now.UTC(),
		},
		MotorKey: motorKey, GuardianKey: guardKey, Name: b.Name, Excluded: b.Excluded, Config: b.Config,
	}, nil
}

// channel is the HTTPS connection to a Citadel that the machine does not
// trust yet: the chain is not checked (there is no CA to check it with), the
// fingerprint of the certificate it presents is recorded, and every
// connection of the entry must present the same one. The code the Owner
// compares is what ties the entry to that certificate.
type channel struct {
	addr string
	hc   *http.Client
	tr   *http.Transport

	mu      sync.Mutex
	fp      [32]byte
	set     bool
	changed bool
}

func newChannel(n transport.Network, addr string) *channel {
	ch := &channel{addr: addr}
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		// There is no CA yet: the check is the code, not the chain.
		InsecureSkipVerify: true,
		NextProtos:         []string{"http/1.1"},
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("guild: the Citadel sent no certificate")
			}
			return ch.see(Fingerprint(cs.PeerCertificates[0].Raw))
		},
	}
	ch.tr = &http.Transport{
		DialTLSContext: func(ctx context.Context, _, a string) (net.Conn, error) {
			raw, err := n.DialContext(ctx, a)
			if err != nil {
				return nil, err
			}
			tc := tls.Client(raw, cfg)
			if err := tc.HandshakeContext(ctx); err != nil {
				_ = raw.Close()
				return nil, err
			}
			return tc, nil
		},
	}
	ch.hc = &http.Client{Transport: ch.tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return ch
}

// see records the fingerprint of the first connection and refuses any that
// differs.
func (ch *channel) see(fp [32]byte) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.set && ch.fp != fp {
		ch.changed = true
		return ErrChannelChanged
	}
	ch.fp, ch.set = fp, true
	return nil
}

func (ch *channel) fingerprint() ([32]byte, bool) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return ch.fp, ch.set && !ch.changed
}

func (ch *channel) close() { ch.tr.CloseIdleConnections() }

// do sends one request of the entry and decodes the answer.
func (ch *channel) do(ctx context.Context, method, path string, body, into any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("guild: encode a join request: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+ch.addr+path, rd)
	if err != nil {
		return fmt.Errorf("guild: join request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := ch.hc.Do(req)
	if err != nil {
		if errors.Is(err, ErrChannelChanged) {
			return ErrChannelChanged
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrCitadelUnavailable, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("guild: read the answer of the Citadel: %w", err)
	}
	if resp.StatusCode >= 300 {
		return statusError(resp.StatusCode, data)
	}
	if into != nil {
		if err := json.Unmarshal(data, into); err != nil {
			return fmt.Errorf("%w: %v", ErrBadBundle, err)
		}
	}
	return nil
}

// statusError turns a refusal of the Citadel into the error it stands for.
func statusError(status int, data []byte) error {
	var e errorBody
	_ = json.Unmarshal(data, &e)
	switch status {
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", ErrRateLimited, e.Message)
	case http.StatusServiceUnavailable:
		return fmt.Errorf("%w: %s", ErrCitadelUnavailable, e.Message)
	case http.StatusGone, http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrJoinRefused, e.Message)
	case http.StatusConflict, http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrJoinRefused, e.Message)
	}
	return fmt.Errorf("guild: the Citadel answered %d: %s", status, e.Message)
}

// Enroll is the entry of a machine already installed that is outside any
// Guild (`mestre join <endereço>`, and the joining part of the installer):
// it refuses a machine that already belongs to one, asks for the entry and
// records the identity and the engine key. The Guardian key and certificate
// are returned in the Joined for the Guardian's own folder.
func Enroll(ctx context.Context, m *Member, cfg JoinConfig) (*Joined, error) {
	if _, err := m.Identity(ctx); err == nil {
		return nil, ErrAlreadyMember
	} else if !errors.Is(err, ErrNotMember) {
		return nil, err
	}
	j, err := Join(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := m.SaveEngineKey(ctx, j.MotorKey); err != nil {
		return nil, err
	}
	j.Instance = cfg.Instance
	if err := m.Adopt(ctx, j.Identity); err != nil {
		return nil, err
	}
	return j, nil
}
