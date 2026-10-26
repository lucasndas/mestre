package guild

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// roleCitadel is the role, in machine_credentials, of the certificate of the
// acting Citadel: the engine's, with mestre.local and the extra names, for 7
// days. A renewal by cert.renew never replaces it (that is role motor).
const roleCitadel = "cidadela"

// ErrNotCitadel: only the machine the epoch certificate of the Guild names
// gets the certificate with mestre.local.
var ErrNotCitadel = errors.New("guild: only the Citadel of the current epoch gets the certificate of the panel")

// ExtraNames are the extra names of the panel the Owner set for the Guild
// (docs/arquitetura/seguranca.md#nomes-dos-certificados).
func (c *Citadel) ExtraNames(ctx context.Context) ([]string, error) {
	g, err := readGuild(ctx, c.db.Reader())
	if err != nil {
		return nil, err
	}
	return g.ExtraNames, nil
}

// SetExtraNames replaces the extra names of the panel (the Owner, with the
// PIN; whoever calls checks it). Each must be under home.arpa or .internal,
// without wildcards, at most transport.MaxExtraNames; an error wraps
// transport.ErrBadName. Only the certificate of the acting Citadel carries
// them: CitadelCertificateDue says it must be issued again.
func (c *Citadel) SetExtraNames(ctx context.Context, names []string) error {
	clean, err := transport.CheckExtraNames(names)
	if err != nil {
		return fmt.Errorf("guild: the extra names: %w", err)
	}
	b, _ := json.Marshal(clean) // a []string always encodes
	if _, err := c.db.Writer().ExecContext(ctx, `UPDATE guild SET extra_names = ? WHERE id = 1`, string(b)); err != nil {
		return fmt.Errorf("guild: the extra names: %w", err)
	}
	c.cfg.Logger.Info("guild: extra names of the panel changed", "names", clean)
	return nil
}

// IssueCitadelCertificate issues the engine certificate of the acting
// Citadel, the only one that carries mestre.local (and the extra names of the
// Guild), valid for transport.CitadelValidity, for key, and records it in
// machine_credentials with the role "cidadela". machineID must be the machine
// the epoch certificate in db names, active and not excluded.
//
// The Citadel calls it itself, since it holds the CA: when it takes the
// command (after TakeCommand, in a promotion; at the start, on the founder
// and after a restart) and then whenever CitadelCertificateDue says so. It
// serves the result on port 7443 in place of its machine certificate, which
// it keeps for when it is a Tower again; a machine that stops being the
// Citadel keeps the name for at most transport.CitadelValidity, and its first
// cert.renew as a Tower drops it (transport.NamesChanged).
func IssueCitadelCertificate(ctx context.Context, db *store.DB, ca *transport.CA, machineID string, key crypto.Signer, clk clock.Clock) (*x509.Certificate, error) {
	if clk == nil {
		clk = clock.Real{}
	}
	csr, err := transport.NewCSR(key)
	if err != nil {
		return nil, err
	}
	tx, err := db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("guild: the certificate of the Citadel: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	g, err := readGuild(ctx, tx)
	if err != nil {
		return nil, err
	}
	if g.Epoch.CitadelMachineID != machineID {
		return nil, fmt.Errorf("%w: epoch %d names %s, not %s", ErrNotCitadel, g.Epoch.Epoch, g.Epoch.CitadelMachineID, machineID)
	}
	m, err := loadMachine(ctx, tx, machineID)
	if err != nil {
		return nil, err
	}
	switch {
	case m.State != MachineActive:
		return nil, fmt.Errorf("%w: %s is %s", ErrNotActive, machineID, m.State)
	case m.Excluded:
		return nil, ErrExcludedMachine
	}
	cert, err := ca.IssueCitadel(csr, m.Names(), g.ExtraNames)
	if err != nil {
		return nil, fmt.Errorf("guild: the certificate of the Citadel: %w", err)
	}
	if err := insertCredential(ctx, tx, machineID, roleCitadel, cert.Raw, transport.Serial(cert), cert.NotAfter, clk.Now()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("guild: the certificate of the Citadel: %w", err)
	}
	return cert, nil
}

// CitadelCertificate is IssueCitadelCertificate for the machine this
// Citadel serves as, with its database, CA and clock.
func (c *Citadel) CitadelCertificate(ctx context.Context, machineID string, key crypto.Signer) (*x509.Certificate, error) {
	leave, ok := c.enter()
	if !ok {
		return nil, ErrSuspended
	}
	defer leave()
	return IssueCitadelCertificate(ctx, c.db, c.cfg.CA, machineID, key, c.clk)
}

// CitadelCertificateDue says whether the Citadel must issue its certificate
// again: leaf is not a certificate of the Citadel, is within
// transport.CitadelRenewBefore of its end, or no longer carries the names of
// the registry and the extra names of the Guild.
func (c *Citadel) CitadelCertificateDue(ctx context.Context, leaf *x509.Certificate) (bool, error) {
	id, err := transport.IdentityOf(leaf)
	if err != nil {
		return true, nil
	}
	g, err := readGuild(ctx, c.db.Reader())
	if err != nil {
		return false, err
	}
	m, err := loadMachine(ctx, c.db.Reader(), id.MachineID)
	if err != nil {
		return false, err
	}
	return !transport.IsCitadelCertificate(leaf) || transport.NeedsRenewal(leaf, c.clk.Now()) ||
		transport.CitadelNamesChanged(leaf, m.Names(), g.ExtraNames), nil
}
