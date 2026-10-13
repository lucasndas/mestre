// Package guild is the Guild of the machines: how it is formed, how a new
// machine joins it with a code and the approval of the Owner, the credentials
// of each machine, the exclusion of a machine from the secrets, the exit and
// the removal, and the certificate of epoch (docs/guilda/guilda.md,
// docs/guilda/replicacao.md#certidão-de-época).
//
// # Pieces
//
//   - Create founds a Guild on the first machine: guild_id, the CA (its key
//     in the vault), the certificates of the machine and the certificate of
//     epoch 1, signed by the CA.
//   - Citadel is the side that admits and removes: the /join routes (HTTPS,
//     outside mTLS), the Approver of the Owner, the issue of the certificates
//     from the CSRs, the registry of the machines and of the credentials, the
//     renewal (cert.renew), the exclusion and the removal. Only the Citadel of
//     the current epoch runs it.
//   - Join is the new machine: it asks to enter, shows the code, waits for
//     the Owner and checks everything it receives before it keeps it. Member
//     is what a machine keeps of its place in the Guild (machine_identity);
//     Epochs and Verifier are the EpochState and the CertVerifier that the
//     protocol asks for; Credentials is the transport's revocation hook over
//     machine_credentials; Renewer renews the certificate of a machine over
//     the protocol.
//   - PlanInstall is the decision of the installer after the discovery:
//     create the Guild, join the one that answered, choose, or give up.
//   - The change of Citadel (docs/guilda/cidadela-e-torres.md): Lock is the
//     witness of the epoch outside the Guild (the GitHub, milestone 5);
//     Fence keeps a Citadel from acting outside unless the lock confirms its
//     epoch (suspended at the start, the lease, the wait of the promoted
//     machine, the demotion); ClaimEpoch, Promoter and TakeCommand are the
//     promotion; ListDiscarded and RecordDiscarded the transactions the
//     change lost; DownWatch the notice to the Owner. internal/app runs them.
//
// # What stays out
//
// The Owner's question, with the button and the PIN, belongs to the bot and
// the panel (milestone 5): here it is the Approver interface, and the answer
// comes back as Citadel.Decide. The erasure of the files, the replica and the
// vault copy on the machine that leaves is a Wiper that the installer and the
// replication provide; the vault.drop of an excluded machine and the
// guild.config that follows an exclusion are sent by the replication from the
// Hooks that fire here. The Guardian's own keys and connection are the
// Guardian's.
//
// The join code is a short comparison that binds the request to the TLS
// connection that carried it, with no secret exchanged before: see JoinCode.
// Time comes only from pkg/clock.
package guild
