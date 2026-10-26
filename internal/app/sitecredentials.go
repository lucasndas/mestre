package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/vault"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The request of a site credential by a Tower (vault.request, docs/plataforma/cofre.md#uso
// and docs/conectividade/mensagens.md): the Citadel decides from the metadata of
// the credential and the rules of the character, audits the request without the
// value, and gives the value in the ack of the request, to be held only in memory.

// AuditVaultRequest is the action of the audit trail of a request of a credential
// by a Tower; the row is made for every request, released or refused, with the
// machine that asked, the character, the credential and the purpose, never the value.
const AuditVaultRequest = "vault.request"

// credentialWait is how long a Tower waits for the answer of a request of a credential.
const credentialWait = 20 * time.Second

// maxPurpose bounds the purpose that goes in the audit.
const maxPurpose = 200

// CredentialBroker is the side of the Citadel: it answers vault.request.
type CredentialBroker struct {
	Vault    *vault.Vault
	Registry characters.Registry
	// Rules are the effective rules of a character: the network.credentials it
	// lists are what its class releases.
	Rules func(ctx context.Context, character string) (*classes.Rules, error)
	Clock clock.Clock
	// Mask hides in the audit anything the Tower wrote in the purpose.
	Mask *logging.Masker
	// Excluded says whether a machine is excluded: it keeps no secrets, so it is given none.
	Excluded func(ctx context.Context, machine string) (bool, error)
}

func (b *CredentialBroker) clk() clock.Clock {
	if b.Clock != nil {
		return b.Clock
	}
	return clock.Real{}
}

// Handle implements protocol.HandlerFunc for vault.request.
func (b *CredentialBroker) Handle(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
	p, ok := m.Payload.(*protocol.VaultRequest)
	if !ok {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "vault.request: %T", m.Payload)
	}
	value, err := b.Release(ctx, m.Peer.ID, *p)
	if err != nil {
		return nil, err
	}
	return &protocol.Reply{Type: protocol.TypeVaultCredential, Payload: protocol.VaultCredential{Value: protocol.Secret(value)}, InAck: true}, nil
}

// Release decides a request of a credential for a character that the machine
// says runs on it: the character must be on that machine by the registry, the
// credential must be a site credential that the class lists and that lists the
// class, and the request is audited either way. The value comes back only when it
// is released; the refusal is a protocol error, with the same words whatever the
// reason (the reason is in the audit).
func (b *CredentialBroker) Release(ctx context.Context, machine string, p protocol.VaultRequest) (string, error) {
	purpose := p.Purpose
	if b.Mask != nil {
		purpose = b.Mask.Mask(purpose)
	}
	if r := []rune(purpose); len(r) > maxPurpose {
		purpose = string(r[:maxPurpose]) + "…"
	}
	refuse := func(reason string) (string, error) {
		if err := b.audit(ctx, machine, p.AgentID, p.Secret, purpose, "refused", reason); err != nil {
			return "", protocol.NewError(protocol.CodeInternal, "vault.request: the audit: %v", err) // the Tower asks again
		}
		return "", protocol.NewError(protocol.CodeForbidden, "vault.request: the credential is not released to this character")
	}
	// an error of the engine is audited too: every request leaves a row
	fail := func(what string, err error) (string, error) {
		_ = b.audit(ctx, machine, p.AgentID, p.Secret, purpose, "error", what)
		return "", protocol.NewError(protocol.CodeInternal, "vault.request: %s: %v", what, err)
	}
	if strings.TrimSpace(p.Purpose) == "" {
		return refuse("a purpose is required")
	}
	// every lookup is made before the decision, so the answer does not tell by its
	// time which of the reasons it was
	c, cerr := b.Registry.Get(ctx, p.AgentID)
	info, ierr := b.Vault.Info(ctx, p.Secret)
	var rules *classes.Rules
	var rerr error
	if cerr == nil {
		rules, rerr = b.Rules(ctx, c.ID)
	}
	excluded, xerr := false, error(nil)
	if b.Excluded != nil {
		excluded, xerr = b.Excluded(ctx, machine)
	}
	switch {
	case cerr != nil && !errors.Is(cerr, characters.ErrNoCharacter):
		return fail("the registry", cerr)
	case ierr != nil && !errors.Is(ierr, vault.ErrNotFound):
		return fail("the vault", ierr)
	case rerr != nil:
		return fail("the rules of the character", rerr)
	case xerr != nil:
		return fail("the state of the machine", xerr)
	case excluded:
		return refuse("the machine is excluded: it keeps no secrets")
	case errors.Is(cerr, characters.ErrNoCharacter):
		return refuse("the character is unknown")
	case c.Machine != machine:
		return refuse("the character does not run on that machine")
	case !c.State.Alive() || c.State == characters.Stopping || c.State == characters.Interrupted:
		return refuse("the character is not alive")
	case errors.Is(ierr, vault.ErrNotFound):
		return refuse("no such credential")
	}
	if err := vault.SiteCredentialAllowed(info, c.Class, rules.Network.Credentials); err != nil {
		return refuse(err.Error())
	}
	if exp := info.Site.ExpiresAt; exp != nil && !b.clk().Now().Before(*exp) {
		return refuse("the credential expired")
	}
	var value string
	if err := b.Vault.Use(ctx, p.Secret, purpose, func(v []byte) error { value = string(v); return nil }); err != nil {
		if errors.Is(err, vault.ErrClosed) {
			return refuse("the vault is closed")
		}
		return fail("the vault", err)
	}
	if err := b.audit(ctx, machine, p.AgentID, p.Secret, purpose, "released", ""); err != nil {
		return "", protocol.NewError(protocol.CodeInternal, "vault.request: the audit: %v", err) // nothing was given: the Tower asks again
	}
	return value, nil
}

// audit writes the request in audit_log: who asked (the machine), for which
// character, which credential, why and when, and the outcome. No value.
func (b *CredentialBroker) audit(ctx context.Context, machine, character, secret, purpose, outcome, reason string) error {
	detail, err := json.Marshal(map[string]string{"character": character, "secret": secret, "purpose": purpose, "outcome": outcome, "reason": reason})
	if err != nil {
		return err
	}
	return b.Registry.Audit(ctx, protocol.NewIDGen(b.clk()).NewString(), b.clk().Now(), machine, AuditVaultRequest, detail)
}

// CredentialError is why a Tower did not get a credential: Code is the one the
// CLI shows (citadel_unavailable when the Citadel is away or does not act) or the
// refusal of the protocol (forbidden).
type CredentialError struct {
	Code    string
	Message string
}

func (e *CredentialError) Error() string { return e.Code + ": " + e.Message }

// RequestSiteCredential is the side of the Tower: it asks the Citadel, at the
// moment of use, for the credential `secret` for the character `agentID`, and
// returns the value, which the caller holds only in memory. It is a request that
// needs the answer now: it is never kept in the outbox, so with the Citadel away it
// fails with citadel_unavailable (and a Citadel that does not act, the same).
func RequestSiteCredential(ctx context.Context, clk clock.Clock, link *protocol.Link, agentID, secret, purpose string) (protocol.Secret, error) {
	ctx, cancel := clock.WithTimeout(ctx, clk, credentialWait) // a request that is not kept does not wait for ever
	defer cancel()
	away := &CredentialError{Code: localchan.CodeCitadelUnavailable, Message: "the Citadel is not available, the credential was not requested"}
	if link == nil {
		return "", away
	}
	res, err := link.Call(ctx, protocol.Outgoing{Type: protocol.TypeVaultRequest, Transient: true,
		Payload: protocol.VaultRequest{AgentID: agentID, Secret: secret, Purpose: purpose}})
	switch {
	case errors.Is(err, protocol.ErrNotConnected):
		return "", away
	case err != nil:
		return "", err
	case res.Status == protocol.AckError && res.Error != nil:
		if res.Error.Code == protocol.CodeUnavailable {
			return "", away
		}
		return "", &CredentialError{Code: res.Error.Code, Message: res.Error.Message}
	}
	var out protocol.VaultCredential
	if err := json.Unmarshal(res.Result, &out); err != nil || out.Value == "" {
		return "", fmt.Errorf("app: the Citadel answered the request of a credential with something that is not one")
	}
	return out.Value, nil
}

// handleVaultRequest is vault.request on the Citadel of the period: the broker
// over the registry and the effective rules of the orchestration. A Citadel that
// does not act answers unavailable, which a Tower shows as citadel_unavailable.
func (e *Engine) handleVaultRequest(run *citadelRun) protocol.HandlerFunc {
	return func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		b, err := e.credentialBroker(run)
		if err != nil {
			return nil, err
		}
		return b.Handle(ctx, m)
	}
}

// credentialBroker is the broker of this period, or the refusal that says why
// there is none.
func (e *Engine) credentialBroker(run *citadelRun) (*CredentialBroker, error) {
	orc, p := e.orc.Load(), e.period.Load()
	switch {
	case run.gate.isDraining() || !run.fence.Acting():
		return nil, protocol.NewError(protocol.CodeUnavailable, "vault.request: the Citadel does not act now")
	case orc == nil || p == nil || p.siteRules == nil:
		return nil, protocol.NewError(protocol.CodeUnavailable, "vault.request: this Citadel hosts no orchestration")
	}
	excluded := func(ctx context.Context, machine string) (bool, error) {
		run.mu.Lock()
		c := run.citadel
		run.mu.Unlock()
		if c == nil {
			return false, errors.New("the Citadel has not acted yet")
		}
		m, err := c.Machine(ctx, machine)
		return m.Excluded, err
	}
	return &CredentialBroker{Vault: run.vault, Registry: orc.Registry, Rules: p.siteRules, Clock: e.clk, Mask: e.deps.Masker, Excluded: excluded}, nil
}

// RequestSiteCredential is what a character of this machine asks when it needs a
// credential of a site: on a Tower it goes to the Citadel over the protocol, on the
// Citadel it is decided here by the same rules; the value is never kept.
func (e *Engine) RequestSiteCredential(ctx context.Context, agentID, secret, purpose string) (protocol.Secret, error) {
	e.mu.Lock()
	cit, tow := e.cit, e.tow
	e.mu.Unlock()
	switch {
	case tow != nil:
		v, err := RequestSiteCredential(ctx, e.clk, tow.client.Link(), agentID, secret, purpose)
		if err == nil && e.deps.Masker != nil {
			e.deps.Masker.Register(string(v)) // what the machine holds is masked in what it writes
		}
		return v, err
	case cit != nil:
		b, err := e.credentialBroker(cit)
		if err == nil {
			var v string
			if v, err = b.Release(ctx, e.id.MachineID, protocol.VaultRequest{AgentID: agentID, Secret: secret, Purpose: purpose}); err == nil {
				if e.deps.Masker != nil {
					e.deps.Masker.Register(v)
				}
				return protocol.Secret(v), nil
			}
		}
		var pe *protocol.Error
		if errors.As(err, &pe) {
			if pe.Code == protocol.CodeUnavailable {
				return "", &CredentialError{Code: localchan.CodeCitadelUnavailable, Message: pe.Message}
			}
			return "", &CredentialError{Code: pe.Code, Message: pe.Message}
		}
		return "", err
	}
	return "", &CredentialError{Code: localchan.CodeCitadelUnavailable, Message: "the Citadel is not available, the credential was not requested"}
}
