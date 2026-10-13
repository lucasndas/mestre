package transport

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
)

// The names a Guild certificate may carry (docs/arquitetura/seguranca.md#nomes-dos-certificados).
// The CA is a root the browsers trust, so no name comes unchecked from a
// machine: every issuance and every renewal validates them first.
const (
	// MaxIPs bounds the addresses of one certificate.
	MaxIPs = 16
	// MaxExtraNames bounds the extra names of the panel the Owner configures.
	MaxExtraNames = 8
	// maxLabel and maxDNSName are the limits of RFC 1035.
	maxLabel   = 63
	maxDNSName = 253
	// reservedHost would give mestre.local, the name of the panel.
	reservedHost = "mestre"
)

// ErrBadName is wrapped by every refusal of a name.
var ErrBadName = errors.New("name not allowed in a Guild certificate")

// PermittedDomains are the DNS subtrees the Name Constraints of a new Guild
// CA allow: .local (the names of the machines and mestre.local) and the two
// domains reserved for private networks, home.arpa (RFC 8375) and .internal.
// A certificate the CA signs for any other name fails the verification of a
// browser or of Go, even if the key of the CA leaks.
var PermittedDomains = []string{"local", "home.arpa", "internal"}

// extraDomains are where the extra names may be: the reserved private
// domains, never .local, whose names are the machines' and the panel's.
var extraDomains = []string{"home.arpa", "internal"}

// PermittedNets are the IP ranges the Name Constraints of a new Guild CA
// allow, and the only ones a certificate carries: private (RFC 1918 and the
// IPv6 unique local addresses), loopback and link-local.
var PermittedNets = mustCIDRs(
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8", "169.254.0.0/16",
	"fc00::/7", "::1/128", "fe80::/10",
)

func mustCIDRs(ss ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(ss))
	for _, s := range ss {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

// HostLabel is the form of a computer name a machine reports: lower case and
// only the first label (a host name "box.lan" gives "box"); a label that
// CheckHostname refuses ("mestre", "desk_top") gives "", and the certificate
// then has no .local name, only the addresses. The Citadel still checks what
// it receives with CheckHostname.
func HostLabel(name string) string {
	label, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(name)), ".")
	if _, err := CheckHostname(label); err != nil {
		return ""
	}
	return label
}

// CheckHostname validates the computer name of a machine and returns it in
// lower case: one RFC 1123 label (letters, digits and '-', at most 63, not
// starting or ending with '-'), without dots, and not "mestre". Empty is
// allowed: the certificate then has no DNS name.
func CheckHostname(name string) (string, error) {
	h := strings.ToLower(name)
	switch {
	case h == "":
		return "", nil
	case strings.Contains(h, "."):
		return "", fmt.Errorf("%w: the host name %q has dots; it is one label, and the certificate gives it .local", ErrBadName, name)
	case h == reservedHost:
		return "", fmt.Errorf("%w: the host name %q would give mestre.local, the name of the panel", ErrBadName, name)
	}
	if err := checkLabel(h); err != nil {
		return "", fmt.Errorf("%w: the host name %q: %v", ErrBadName, name, err)
	}
	return h, nil
}

func checkLabel(l string) error {
	if l == "" || len(l) > maxLabel {
		return fmt.Errorf("a label has 1 to %d characters", maxLabel)
	}
	if l[0] == '-' || l[len(l)-1] == '-' {
		return errors.New("a label does not start or end with '-'")
	}
	for i := range len(l) {
		c := l[i]
		if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return fmt.Errorf("%q is not a letter, a digit or '-'", c)
		}
	}
	return nil
}

// CertificateIP says whether ip may go in a certificate: a private, loopback
// or link-local unicast address, inside PermittedNets.
func CertificateIP(ip net.IP) bool {
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast())
}

// CertificateIPs keeps the addresses that may go in a certificate, at most
// MaxIPs: what a machine reports of itself before the Citadel checks it.
func CertificateIPs(ips []net.IP) []net.IP {
	var out []net.IP
	for _, ip := range ips {
		if CertificateIP(ip) && len(out) < MaxIPs && !slices.ContainsFunc(out, ip.Equal) {
			out = append(out, ip)
		}
	}
	return out
}

// CheckIPs validates the addresses of a machine: at most MaxIPs, each one
// private, loopback or link-local.
func CheckIPs(ips []net.IP) error {
	if len(ips) > MaxIPs {
		return fmt.Errorf("%w: %d addresses, at most %d", ErrBadName, len(ips), MaxIPs)
	}
	for _, ip := range ips {
		if !CertificateIP(ip) {
			return fmt.Errorf("%w: %v is not a private, loopback or link-local address", ErrBadName, ip)
		}
	}
	return nil
}

// CheckExtraName validates an extra name of the panel and returns it in lower
// case: a DNS name under home.arpa or .internal, without wildcards. Names
// under .local are the machines' and the panel's own and never extras; a
// public domain is outside the Name Constraints of the CA.
func CheckExtraName(name string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || len(n) > maxDNSName {
		return "", fmt.Errorf("%w: the extra name %q has 1 to %d characters", ErrBadName, name, maxDNSName)
	}
	if strings.Contains(n, "*") {
		return "", fmt.Errorf("%w: the extra name %q is a wildcard", ErrBadName, name)
	}
	under := ""
	for _, d := range extraDomains {
		if strings.HasSuffix(n, "."+d) {
			under = d
		}
	}
	if under == "" {
		return "", fmt.Errorf("%w: the extra name %q is not under %s", ErrBadName, name, strings.Join(extraDomains, " or "))
	}
	for _, l := range strings.Split(strings.TrimSuffix(n, "."+under), ".") {
		if err := checkLabel(l); err != nil {
			return "", fmt.Errorf("%w: the extra name %q: %v", ErrBadName, name, err)
		}
	}
	return n, nil
}

// CheckExtraNames validates the extra names of the panel: at most
// MaxExtraNames, each by CheckExtraName. It returns them in lower case,
// sorted and without repetitions.
func CheckExtraNames(names []string) ([]string, error) {
	if len(names) > MaxExtraNames {
		return nil, fmt.Errorf("%w: %d extra names, at most %d", ErrBadName, len(names), MaxExtraNames)
	}
	out := make([]string, 0, len(names))
	for _, s := range names {
		n, err := CheckExtraName(s)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// Check validates every name: the host name by CheckHostname and the
// addresses by CheckIPs. The CA calls it before it signs.
func (n Names) Check() error {
	if _, err := CheckHostname(n.Hostname); err != nil {
		return err
	}
	return CheckIPs(n.IPs)
}
