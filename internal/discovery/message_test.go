package discovery_test

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/discovery"
)

func TestTheMessagesRoundTripAndStayUnder512Bytes(t *testing.T) {
	t.Parallel()
	nonce, err := discovery.NewNonce(bytes.NewReader(bytes.Repeat([]byte{0x9f, 0x2c}, 8)))
	if err != nil || nonce != "9f2c9f2c9f2c9f2c9f2c9f2c9f2c9f2c" {
		t.Fatalf("nonce = %q, %v", nonce, err)
	}
	b, err := discovery.Request{Nonce: nonce, GuildID: "01JABCDEFGHJKMNPQRSTVWXYZ0"}.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"mestre":"discover","v":1,"nonce":"` + nonce + `","guild_id":"01JABCDEFGHJKMNPQRSTVWXYZ0"}`; string(b) != want {
		t.Fatalf("request = %s", b)
	}
	req, err := discovery.DecodeRequest(b)
	if err != nil || req.Nonce != nonce {
		t.Fatalf("decode = %+v, %v", req, err)
	}

	in := discovery.Response{
		Nonce: nonce, GuildID: "01JABCDEFGHJKMNPQRSTVWXYZ0", MachineID: "01JABCDEFGHJKMNPQRSTVWXYZ1", Name: "servidor",
		Role: discovery.RoleCitadel, Epoch: 2, Port: 7443,
		Citadel:  &discovery.CitadelRef{MachineID: "01JABCDEFGHJKMNPQRSTVWXYZ1", Addrs: []string{"192.168.1.10"}, Port: 7443},
		Machines: 3, CAFp: "sha256:" + strings.Repeat("ab", 32),
	}
	b, err = in.Encode()
	if err != nil || len(b) >= discovery.MaxMessage {
		t.Fatalf("response of %d bytes, %v", len(b), err)
	}
	out, err := discovery.DecodeResponse(b)
	if err != nil || out.Role != "cidadela" || out.Citadel.Addrs[0] != "192.168.1.10" || out.Epoch != 2 {
		t.Fatalf("decode = %+v, %v", out, err)
	}
}

func TestAnAnswerThatDoesNotFitLosesTheLastAddressesOfTheCitadel(t *testing.T) {
	t.Parallel()
	var addrs []string
	for i := range 30 {
		addrs = append(addrs, "192.168.100."+strings.Repeat("1", 1+i%3))
	}
	in := discovery.Response{
		Nonce: otherNonce, GuildID: "g", MachineID: "m", Role: discovery.RoleTower, Port: 7443,
		Citadel: &discovery.CitadelRef{MachineID: "c", Addrs: addrs, Port: 7443}, CAFp: "sha256:" + strings.Repeat("ab", 32),
	}
	b, err := in.Encode()
	if err != nil || len(b) >= discovery.MaxMessage {
		t.Fatalf("%d bytes, %v", len(b), err)
	}
	out, _ := discovery.DecodeResponse(b)
	if len(out.Citadel.Addrs) == 0 || len(out.Citadel.Addrs) >= len(addrs) || out.Citadel.Addrs[0] != addrs[0] {
		t.Fatalf("addresses = %v", out.Citadel.Addrs)
	}
	if len(in.Citadel.Addrs) != len(addrs) {
		t.Fatal("Encode changed the caller's value")
	}
}

func TestMessagesOfOtherVersionsKindsOrNoncesAreNotTaken(t *testing.T) {
	t.Parallel()
	n := `"nonce":"` + otherNonce + `"`
	for name, raw := range map[string]string{
		"version 2":     `{"mestre":"discover","v":2,` + n + `}`,
		"another kind":  `{"mestre":"here","v":1,` + n + `}`,
		"not json":      `discover`,
		"short nonce":   `{"mestre":"discover","v":1,"nonce":"abc"}`,
		"upper nonce":   `{"mestre":"discover","v":1,"nonce":"` + strings.ToUpper("abcdefabcdefabcdefabcdefabcdefab") + `"}`,
		"too big":       `{"mestre":"discover","v":1,` + n + `,"pad":"` + strings.Repeat("x", 512) + `"}`,
		"another place": `{"hello":"world"}`,
	} {
		if _, err := discovery.DecodeRequest([]byte(raw)); err == nil {
			t.Errorf("%s: the request was taken", name)
		}
	}
	if _, err := discovery.DecodeRequest([]byte(`{"mestre":"discover","v":1,` + n + `,"extra":[1,2]}`)); err != nil {
		t.Errorf("unknown fields must be ignored: %v", err)
	}
	if _, err := (discovery.Request{Nonce: "x"}).Encode(); !errors.Is(err, discovery.ErrBadNonce) {
		t.Errorf("Encode = %v", err)
	}
	var big json.RawMessage = []byte(strings.Repeat("x", 600))
	if _, err := discovery.DecodeResponse(big); !errors.Is(err, discovery.ErrTooBig) {
		t.Errorf("DecodeResponse = %v", err)
	}
}

func TestTheAvahiFileAnnouncesBothServicesOfTheInstance(t *testing.T) {
	t.Parallel()
	a := discovery.Announcement{
		GuildID: "01JGUILD", MachineID: "01JMACHINE", Role: discovery.RoleCitadel, Epoch: 2, EnginePort: 7443, GuardianPort: 7444,
	}
	b, err := discovery.AvahiServiceFile(a)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Name     string `xml:"name"`
		Services []struct {
			Type string   `xml:"type"`
			Port int      `xml:"port"`
			TXT  []string `xml:"txt-record"`
		} `xml:"service"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("not well-formed: %v\n%s", err, b)
	}
	if len(doc.Services) != 2 || doc.Services[0].Type != "_mestre._tcp" || doc.Services[0].Port != 7443 ||
		doc.Services[1].Type != "_mestre-guardiao._tcp" || doc.Services[1].Port != 7444 || len(doc.Services[1].TXT) != 0 {
		t.Fatalf("services = %+v", doc.Services)
	}
	want := []string{"guild_id=01JGUILD", "machine_id=01JMACHINE", "instance=principal", "role=cidadela", "epoch=2", "v=1"}
	if strings.Join(doc.Services[0].TXT, ",") != strings.Join(want, ",") {
		t.Fatalf("TXT = %v, want %v", doc.Services[0].TXT, want)
	}
	if !strings.Contains(string(b), `<!DOCTYPE service-group SYSTEM "avahi-service.dtd">`) || !strings.Contains(doc.Name, "%h") {
		t.Fatalf("not an Avahi service group:\n%s", b)
	}

	for in, want := range map[string]string{"": "mestre.service", "principal": "mestre.service", "alfa": "mestre-alfa.service"} {
		if got := discovery.AvahiServiceFileName(in); got != want {
			t.Errorf("file of %q = %s, want %s", in, got, want)
		}
	}
	for name, mut := range map[string]func(*discovery.Announcement){
		"an instance with a slash": func(a *discovery.Announcement) { a.Instance = "../x" },
		"a bad role":               func(a *discovery.Announcement) { a.Role = "king" },
		"no guild":                 func(a *discovery.Announcement) { a.GuildID = "" },
		"no port":                  func(a *discovery.Announcement) { a.GuardianPort = 0 },
	} {
		bad := a
		mut(&bad)
		if _, err := discovery.AvahiServiceFile(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// text that is XML is escaped, not interpreted
	esc := a
	esc.GuildID = `a<b>&"c`
	if b, err := discovery.AvahiServiceFile(esc); err != nil || strings.Contains(string(b), "<b>") {
		t.Errorf("guild id not escaped: %v\n%s", err, b)
	}

	line, err := discovery.AvahiHostsLine(netip.MustParseAddr("192.168.1.10"))
	if err != nil || line != "192.168.1.10 mestre.local\n" {
		t.Errorf("hosts line = %q, %v", line, err)
	}
	if _, err := discovery.AvahiHostsLine(netip.MustParseAddr("127.0.0.1")); err == nil {
		t.Error("mestre.local at the loopback")
	}
}
