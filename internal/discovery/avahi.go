package discovery

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// The names of the mDNS services (docs/conectividade/descoberta.md#mdns).
const (
	ServiceEngine   = "_mestre._tcp"
	ServiceGuardian = "_mestre-guardiao._tcp"
	// CitadelHostname is the name only the Guardian of the Citadel
	// publishes: the address of the panel.
	CitadelHostname = "mestre.local"
	// DefaultInstance is the name of the default instance.
	DefaultInstance = "principal"
)

// Announcement is what the Guardian of an instance announces by mDNS.
type Announcement struct {
	GuildID   string
	MachineID string
	Instance  string // DefaultInstance when empty
	Role      string // RoleCitadel or RoleTower
	Epoch     uint64
	// EnginePort and GuardianPort are the ports of the instance.
	EnginePort, GuardianPort int
}

func (a Announcement) instance() string {
	if a.Instance == "" {
		return DefaultInstance
	}
	return a.Instance
}

// TXT is the TXT record of _mestre._tcp.
func (a Announcement) TXT() []string {
	return []string{
		"guild_id=" + a.GuildID,
		"machine_id=" + a.MachineID,
		"instance=" + a.instance(),
		"role=" + a.Role,
		"epoch=" + strconv.FormatUint(a.Epoch, 10),
		"v=" + strconv.Itoa(Version),
	}
}

func (a Announcement) validate() error {
	switch {
	case a.GuildID == "" || a.MachineID == "":
		return fmt.Errorf("discovery: announcement needs a guild_id and a machine_id")
	case a.Role != RoleCitadel && a.Role != RoleTower:
		return fmt.Errorf("discovery: announcement role %q", a.Role)
	case !validInstance(a.instance()):
		return fmt.Errorf("discovery: instance name %q", a.Instance)
	case a.EnginePort < 1 || a.EnginePort > 65535 || a.GuardianPort < 1 || a.GuardianPort > 65535:
		return fmt.Errorf("discovery: announcement ports %d and %d", a.EnginePort, a.GuardianPort)
	}
	return nil
}

// validInstance keeps the instance name safe for a file name and for XML.
func validInstance(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		ok := r == '-' || r == '_' || ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')
		if !ok {
			return false
		}
	}
	return true
}

// AvahiServiceFileName is the file of an instance in /etc/avahi/services:
// mestre.service for the default one, mestre-<instance>.service for the
// others (each instance has its own Guardian that rewrites its own file).
func AvahiServiceFileName(instance string) string {
	if instance == "" || instance == DefaultInstance {
		return "mestre.service"
	}
	return "mestre-" + instance + ".service"
}

// AvahiServiceFile returns the content of the Avahi service file that
// announces the two services of an instance, as the Guardian writes it on
// Linux when the role changes. The host name `<host>.local` is Avahi's own.
func AvahiServiceFile(a Announcement) ([]byte, error) {
	if err := a.validate(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" standalone='no'?>\n")
	b.WriteString("<!DOCTYPE service-group SYSTEM \"avahi-service.dtd\">\n")
	b.WriteString("<service-group>\n")
	fmt.Fprintf(&b, "  <name replace-wildcards=\"yes\">mestre %s on %%h</name>\n", xmlText(a.instance()))
	b.WriteString("  <service>\n")
	fmt.Fprintf(&b, "    <type>%s</type>\n    <port>%d</port>\n", ServiceEngine, a.EnginePort)
	for _, t := range a.TXT() {
		fmt.Fprintf(&b, "    <txt-record>%s</txt-record>\n", xmlText(t))
	}
	b.WriteString("  </service>\n  <service>\n")
	fmt.Fprintf(&b, "    <type>%s</type>\n    <port>%d</port>\n", ServiceGuardian, a.GuardianPort)
	b.WriteString("  </service>\n</service-group>\n")
	return b.Bytes(), nil
}

// AvahiHostsLine is the line of /etc/avahi/hosts that publishes mestre.local
// at the address of the panel; only the Guardian of the Citadel, while it
// holds the lease of the engine, writes it.
func AvahiHostsLine(ip netip.Addr) (string, error) {
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return "", fmt.Errorf("discovery: %v is not an address for %s", ip, CitadelHostname)
	}
	return ip.String() + " " + CitadelHostname + "\n", nil
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
