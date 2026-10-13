package protocol

import "context"

// EpochState is what the protocol needs to know about the epoch of the Guild
// (docs/guilda/cidadela-e-torres.md#época-e-fencing). The Guild and the
// Citadel and Towers (stages 3 and 5 of the milestone) implement it on top
// of the banco and the certificate; here it is the contract the envelope
// check relies on.
type EpochState interface {
	// Epoch is the epoch this machine follows now. Every message it sends
	// carries it.
	Epoch() uint64
	// Certificate is the certificate of that epoch, which the opening
	// carries and guild.epoch answers.
	Certificate() *Certificate
	// Follow is called, before anything else happens, with a certificate
	// whose signature the CertVerifier accepted and whose epoch is higher
	// than Epoch: this machine is stale. The implementation persists the
	// certificate and from then on Epoch returns it; a Citadel demotes
	// itself to a Tower before any external action. An error refuses the
	// message that revealed the higher epoch.
	Follow(ctx context.Context, cert Certificate) error
	// Stale is called when a peer refused a message of this machine for its
	// epoch (an error stale_epoch or a close with 4006): the implementation
	// refreshes the certificate and finds the right Citadel. The message
	// that was refused is not sent again to that peer.
	Stale(ctx context.Context, peer Peer)
}

// CertVerifier checks the signature of the CA of the Guild on a certificate
// of epoch. The certificates themselves are born with the Guild (stage 3).
type CertVerifier interface {
	Verify(cert Certificate) error
}
