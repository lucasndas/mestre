package discovery

import (
	"net"
	"sort"
	"strconv"
)

// Known is what this machine already knows about the Citadel: the epoch of
// the highest certificate it has seen and the machine that certificate names.
// Both are zero on a machine that has no certificate yet.
type Known struct {
	Epoch            uint64
	CitadelMachineID string
}

// Candidate is an address that may be the Citadel. Discovery is only a hint:
// the identity check decides.
type Candidate struct {
	// Addr is "host:port" of the WSS port.
	Addr   string
	Source Source
	// MachineID is the machine believed to be there; empty if unknown.
	MachineID string
	// Epoch is the epoch the one who pointed here follows; zero when the
	// address came from the configuration or the cache.
	Epoch uint64
}

// MaxCandidates bounds the addresses one search hands to the identity check:
// forged answers cannot make a pass open TLS without end.
const MaxCandidates = 16

// CandidatesFrom turns what answered into the addresses to check, best
// first: the higher the epoch, the better, then a Citadel that answered for
// itself before a Tower that pointed to one. An address whose epoch is lower
// than the one this machine follows is dropped: that Citadel is a stale one.
// A Tower's own address is never a candidate, only the Citadel it names.
// Every address is an IP literal that can be dialed (never a name, which
// would send the machine to a DNS lookup of the sender's choice), and at
// most MaxCandidates are kept.
func CandidatesFrom(seen []Seen, known Known) []Candidate {
	type scored struct {
		c      Candidate
		direct bool
	}
	var out []scored
	add := func(addrs []string, port int, s Seen, machineID string, direct bool) {
		for _, a := range addrs {
			if usableAddr(a) {
				out = append(out, scored{Candidate{Addr: hostPort(a, port), Source: s.Source, MachineID: machineID, Epoch: s.Epoch}, direct})
			}
		}
	}
	for _, s := range seen {
		switch s.Role {
		case RoleCitadel:
			add(s.Addrs, s.Port, s, s.MachineID, true)
			if s.Citadel != nil && s.Citadel.MachineID == s.MachineID {
				add(s.Citadel.Addrs, s.Citadel.Port, s, s.MachineID, false)
			}
		case RoleTower:
			if s.Citadel == nil || s.Citadel.MachineID == "" {
				continue
			}
			add(s.Citadel.Addrs, s.Citadel.Port, s, s.Citadel.MachineID, false)
		}
	}
	kept := out[:0]
	for _, o := range out {
		if o.c.Epoch < known.Epoch {
			continue
		}
		if _, p, err := splitAddr(o.c.Addr); err != nil || p == 0 {
			continue
		}
		kept = append(kept, o)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].c.Epoch != kept[j].c.Epoch {
			return kept[i].c.Epoch > kept[j].c.Epoch
		}
		return kept[i].direct && !kept[j].direct
	})
	seenAddr := map[string]bool{}
	var res []Candidate
	for _, k := range kept {
		if seenAddr[k.c.Addr] {
			continue
		}
		seenAddr[k.c.Addr] = true
		res = append(res, k.c)
		if len(res) == MaxCandidates {
			break
		}
	}
	return res
}

// withDefaultPort turns "host" into "host:7443" and leaves "host:port"
// alone.
func withDefaultPort(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(trimBrackets(addr), strconv.Itoa(DefaultEnginePort))
}

func trimBrackets(s string) string {
	if len(s) > 1 && s[0] == '[' && s[len(s)-1] == ']' {
		return s[1 : len(s)-1]
	}
	return s
}
