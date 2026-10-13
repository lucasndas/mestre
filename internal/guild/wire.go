package guild

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
)

// The routes of the entry, on the engine's port, outside mTLS
// (docs/arquitetura/componentes.md#portas).
const (
	RouteJoin       = "/join"
	routeJoinReveal = "/join/{id}/reveal"
	routeJoinStatus = "/join/{id}"
)

// Errors of the entry. The HTTP status of each is in statusOf.
var (
	// ErrRateLimited: too many requests from one address in the last hour.
	ErrRateLimited = errors.New("guild: too many requests from this address")
	// ErrTooManyPending: too many requests waiting for the Owner.
	ErrTooManyPending = errors.New("guild: too many requests waiting for the Owner")
	// ErrSuspended: a suspended Citadel does not take entries.
	ErrSuspended = errors.New("guild: the Citadel is suspended")
	// ErrJoinNotFound: no such request.
	ErrJoinNotFound = errors.New("guild: no such join request")
	// ErrExpired: the request is over its hour.
	ErrExpired = errors.New("guild: the join request expired")
	// ErrNotPending: the request was already decided.
	ErrNotPending = errors.New("guild: the join request was already decided")
	// ErrNotRevealed: the machine has not revealed its nonce yet, so there is
	// no code to compare and the Owner was not asked.
	ErrNotRevealed = errors.New("guild: the join request has no code yet")
	// ErrBadRequest: the request is not well formed.
	ErrBadRequest = errors.New("guild: bad join request")
	// ErrBadReveal: the nonce does not match what the machine committed to.
	ErrBadReveal = errors.New("guild: the nonce does not match the commitment")
)

// JoinRequest is what a new machine sends first (POST /join). The CSRs carry
// only the public keys: the Citadel decides every name in the certificates.
type JoinRequest struct {
	// Name is the name of the computer; the Citadel adds a suffix when it is
	// already taken.
	Name     string `json:"name"`
	OS       string `json:"os,omitempty"`
	Hostname string `json:"hostname"`
	Instance string `json:"instance,omitempty"`
	// Hostname is one label and IPs are private, loopback or link-local
	// addresses, at most transport.MaxIPs: the Citadel refuses any other
	// (docs/arquitetura/seguranca.md#nomes-dos-certificados). The extra names
	// of the panel are the Owner's, for the Guild; a machine never asks for
	// a name.
	IPs []string `json:"ips,omitempty"`
	// Sheet is the machine sheet (docs/guilda/ficha-da-maquina.md).
	Sheet json.RawMessage `json:"sheet,omitempty"`
	// CSRMotor and CSRGuardian are the DER certificate requests of the
	// engine and of the Guardian.
	CSRMotor    []byte `json:"csr_motor"`
	CSRGuardian []byte `json:"csr_guardian"`
	// Commitment is SHA-256(nonce_n): the machine commits to its nonce before
	// it sees the nonce of the Citadel.
	Commitment []byte `json:"commitment"`
}

// JoinTicket answers the request.
type JoinTicket struct {
	JoinID    string    `json:"join_id"`
	NonceC    []byte    `json:"nonce_c"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RevealBody is the second step: the nonce the commitment was made of.
type RevealBody struct {
	NonceN []byte `json:"nonce_n"`
}

// JoinState is where a request stands.
type JoinState string

// The states of a request (docs/aprovacoes/fluxo.md#pedidos-da-guilda).
const (
	JoinPending  JoinState = "pending"
	JoinApproved JoinState = "approved"
	JoinRefused  JoinState = "refused"
	JoinExpired  JoinState = "expired"
)

// JoinStatus answers GET /join/<id>.
type JoinStatus struct {
	State JoinState `json:"state"`
	// Bundle is what the machine receives once the Owner accepted.
	Bundle *Bundle `json:"bundle,omitempty"`
}

// Bundle is what a machine receives when the Owner accepts it: public
// material only. Nothing in it is a secret, so a copy in the wrong hands
// admits nobody; what protects the entry is the code the Owner compared.
type Bundle struct {
	GuildID   string `json:"guild_id"`
	MachineID string `json:"machine_id"`
	// Name is the name the Guild gave the machine.
	Name string `json:"name"`
	// CA, Motor and Guardian are DER certificates.
	CA       []byte `json:"ca"`
	Motor    []byte `json:"motor"`
	Guardian []byte `json:"guardian"`
	// Epoch is the certificate of the epoch the Citadel follows now.
	Epoch protocol.Certificate `json:"epoch"`
	// Excluded says the Owner marked the machine as excluded at the entry.
	Excluded bool `json:"excluded"`
	// Config is the Guild configuration at the moment of the entry.
	Config protocol.GuildConfig `json:"config"`
}

// errorBody is the JSON of a refusal.
type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
