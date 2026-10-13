// Package discovery is how a machine of the Guild finds the Citadel on the
// local network without configuration (docs/conectividade/descoberta.md):
// the UDP broadcast on 7445/udp (Responder answers, Search asks), the mDNS
// query of _mestre._tcp (Browser), the cache of addresses (Cache) and the
// Locator that applies the order of the documentation and hands the address
// of the Citadel to the protocol.
//
// Discovery is only a hint. Every address goes through the identity check
// before anyone talks to it: TLS 1.3 with the Guild CA and the mestre://
// identity (transport.Client.Probe through TLSVerifier), and the machine has
// to be the one the certificate of the highest epoch names; the welcome of
// the protocol proves a higher epoch. An address that fails is discarded and
// the next one is tried. Nothing here makes a machine trust anything new.
//
// # Wiring with the protocol
//
// A Tower runs Locator.Run before its protocol.Client, builds the dialer
// with protocol.TransportDialer(client, Locator.Target), and sets
// Hooks.Rediscover to Locator.Rediscover and Hooks.OnConnect to call
// Locator.Connected, so the cache learns every address that worked and a
// Citadel that changed address is found again after three failures.
//
// # Layers
//
// This package is one of the layers that open sockets (SystemPacketNetwork,
// SystemBrowser), each with the guard of the fast suite; the fast tests use
// the fake network (internal/testkit/fakenet through
// internal/testkit/discorig). The announcement of the services is not done
// here: Avahi on Linux and the Guardian on Windows publish it, and the
// installer writes the Avahi files this package renders (AvahiServiceFile).
package discovery
