package discovery_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/discovery"
	"github.com/lucasndas/mestre/internal/testkit/discorig"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/pkg/clock"
)

const otherNonce = "00000000000000000000000000000000"

// bubbleNet is a fake network with two hosts that have an IP.
func bubbleNet() *fakenet.Network {
	n := fakenet.New(clock.Real{})
	n.SetIP("citadel", "192.168.1.10")
	n.SetIP("tower", "192.168.1.20")
	return n
}

func search(ctx context.Context, n *fakenet.Network, host, guild string) ([]discovery.Seen, error) {
	return discovery.Search(ctx, discovery.SearchConfig{
		Net: discorig.Packets{Host: n.Host(host)}, Clock: clock.Real{}, GuildID: guild,
	})
}

func startResponder(t *testing.T, n *fakenet.Network, host string, info discovery.Info) *discovery.Responder {
	t.Helper()
	r, err := discovery.StartResponder(t.Context(), discovery.ResponderConfig{
		Net: discorig.Packets{Host: n.Host(host)}, Clock: clock.Real{},
		Info: func() (discovery.Info, bool) { return info, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

func citadelInfo(guild, id string, port int) discovery.Info {
	return discovery.Info{
		GuildID: guild, MachineID: id, Name: id, Role: discovery.RoleCitadel, Epoch: 2, Port: port,
		Citadel:  &discovery.CitadelRef{MachineID: id, Addrs: []string{"192.168.1.10"}, Port: port},
		Machines: 3, CAFp: "sha256:test",
	}
}

// CA-DESC-01
func TestOnlyTheAnswerWithTheNonceOfTheRequestEntersTheList(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		n := bubbleNet()
		sock, err := n.Host("citadel").ListenPacket(discovery.DefaultPort, true)
		if err != nil {
			t.Fatal(err)
		}
		defer sock.Close()
		go func() { // answers every request twice: with a wrong nonce, then with the right one
			buf := make([]byte, 1024)
			for {
				k, from, err := sock.ReadFrom(buf)
				if err != nil {
					return
				}
				req, err := discovery.DecodeRequest(buf[:k])
				if err != nil {
					continue
				}
				for _, nonce := range []string{otherNonce, req.Nonce} {
					id := "forged"
					if nonce == req.Nonce {
						id = "citadel"
					}
					b, err := discovery.Response{Nonce: nonce, GuildID: guildID, MachineID: id, Role: discovery.RoleCitadel, Epoch: 1, Port: 7443}.Encode()
					if err != nil {
						return
					}
					_ = sock.WriteTo(b, from)
				}
			}
		}()
		seen, err := search(t.Context(), n, "tower", guildID)
		if err != nil {
			t.Fatal(err)
		}
		if len(seen) != 1 || seen[0].MachineID != "citadel" || seen[0].Addrs[0] != "192.168.1.10" {
			t.Fatalf("seen = %+v, want only the answer with the right nonce", seen)
		}
	})
}

// CA-DESC-03
func TestAMachineDoesNotAnswerARequestOfAnotherGuild(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		guild     string
		answers   bool
		datagrams int // the requests plus the answers, for the 1 s of the first request
	}{
		"another guild":    {"guild-2", false, 0},
		"the guild itself": {guildID, true, 0},
		"an empty guild":   {"", true, 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				n := bubbleNet()
				startResponder(t, n, "citadel", citadelInfo(guildID, "citadel", 7443))
				before := n.Datagrams()
				// a raw request, to look at the responder and not at the filter of the asker
				sock, err := n.Host("tower").ListenPacket(0, false)
				if err != nil {
					t.Fatal(err)
				}
				defer sock.Close()
				req, err := discovery.Request{Nonce: otherNonce, GuildID: tc.guild}.Encode()
				if err != nil {
					t.Fatal(err)
				}
				if err := sock.WriteTo(req, "255.255.255.255:7445"); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				sent := n.Datagrams() - before
				if tc.answers && sent != 2 || !tc.answers && sent != 1 {
					t.Fatalf("%d datagrams were sent; answers = %v", sent, tc.answers)
				}
			})
		})
	}
}

func TestARequestOfAnotherVersionOrTooBigIsIgnoredAndUnknownFieldsAreNot(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		n := bubbleNet()
		startResponder(t, n, "citadel", citadelInfo(guildID, "citadel", 7443))
		sock, err := n.Host("tower").ListenPacket(0, false)
		if err != nil {
			t.Fatal(err)
		}
		defer sock.Close()
		ask := func(msg string) int {
			before := n.Datagrams()
			if err := sock.WriteTo([]byte(msg), "255.255.255.255:7445"); err != nil {
				t.Fatal(err)
			}
			synctest.Wait()
			return n.Datagrams() - before - 1
		}
		nonce := `"nonce":"` + otherNonce + `"`
		if got := ask(`{"mestre":"discover","v":2,` + nonce + `,"guild_id":""}`); got != 0 {
			t.Errorf("a request of version 2 got %d answers", got)
		}
		if got := ask(`{"mestre":"discover","v":1,` + nonce + `,"guild_id":"","pad":"` + strings.Repeat("x", 600) + `"}`); got != 0 {
			t.Errorf("a request of over 512 bytes got %d answers", got)
		}
		if got := ask(`{"mestre":"discover","v":1,"nonce":"short","guild_id":""}`); got != 0 {
			t.Errorf("a request with a bad nonce got %d answers", got)
		}
		if got := ask(`{"mestre":"discover","v":1,` + nonce + `,"guild_id":"","future":{"a":1}}`); got != 1 {
			t.Errorf("a request with an unknown field got %d answers, want 1", got)
		}
	})
}

func TestAMachineAnswersAtMostTenRequestsPerSecond(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		n := bubbleNet()
		r := startResponder(t, n, "citadel", citadelInfo(guildID, "citadel", 7443))
		sock, err := n.Host("tower").ListenPacket(0, false)
		if err != nil {
			t.Fatal(err)
		}
		defer sock.Close()
		req, _ := discovery.Request{Nonce: otherNonce, GuildID: guildID}.Encode()
		before := n.Datagrams()
		for range 25 {
			_ = sock.WriteTo(req, "192.168.1.10:7445")
		}
		synctest.Wait()
		if got := n.Datagrams() - before - 25; got != 10 || r.Dropped() != 15 {
			t.Fatalf("%d answers, %d dropped; want 10 and 15", got, r.Dropped())
		}
		_ = clock.Sleep(t.Context(), clock.Real{}, time.Second)
		before = n.Datagrams()
		_ = sock.WriteTo(req, "192.168.1.10:7445")
		synctest.Wait()
		if got := n.Datagrams() - before - 1; got != 1 {
			t.Fatalf("%d answers one second later, want 1", got)
		}
	})
}

// The instances of a machine share the port: each one hears the broadcast and
// answers as a machine of its own. (CA-DESC-09 runs this on real sockets in
// the full suite.)
func TestInstancesOfOneMachineAllAnswerOnTheSharedPort(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		n := fakenet.New(clock.Real{})
		n.SetIP("pc", "192.168.1.30")
		for i, id := range []string{"principal", "alfa", "beta"} {
			info := citadelInfo(guildID, id, 7443+10*i)
			info.Citadel = &discovery.CitadelRef{MachineID: "principal", Addrs: []string{"192.168.1.30"}, Port: 7443}
			if i > 0 {
				info.Role = discovery.RoleTower
			}
			startResponder(t, n, "pc", info)
		}
		// the new instance asks from the same host
		seen, err := search(t.Context(), n, "pc", guildID)
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for _, s := range seen {
			ids[s.MachineID] = true
		}
		if len(seen) != 3 || len(ids) != 3 {
			t.Fatalf("seen = %+v, want three machines", seen)
		}
		cands := discovery.CandidatesFrom(seen, discovery.Known{})
		if len(cands) != 1 || cands[0].Addr != "192.168.1.30:7443" || cands[0].MachineID != "principal" {
			t.Fatalf("candidates = %+v, want the Citadel of the first instance", cands)
		}
	})
}

func TestTheAskerGroupsTheAnswersByGuildToCountThem(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		n := bubbleNet()
		n.SetIP("other", "192.168.1.40")
		startResponder(t, n, "citadel", citadelInfo("guild-a", "a1", 7443))
		startResponder(t, n, "other", citadelInfo("guild-b", "b1", 7443))
		seen, err := search(t.Context(), n, "tower", "")
		if err != nil {
			t.Fatal(err)
		}
		by := discovery.ByGuild(seen)
		if len(by) != 2 || len(by["guild-a"]) != 1 || len(by["guild-b"]) != 1 || !discovery.CitadelAnswered(by["guild-a"]) {
			t.Fatalf("by guild = %+v", by)
		}
	})
}

func TestALostRequestIsSentAgainAndTheAnswerArrives(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		n := bubbleNet()
		startResponder(t, n, "citadel", citadelInfo(guildID, "citadel", 7443))
		n.LoseNextDatagrams(2) // the first request goes to two broadcast addresses
		seen, err := search(t.Context(), n, "tower", guildID)
		if err != nil || len(seen) != 1 {
			t.Fatalf("seen = %+v, %v", seen, err)
		}
	})
}

func TestMDNSRecordsAreReadFromTheTXTAndFilteredByGuild(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		n := bubbleNet()
		good := discovery.Announcement{GuildID: guildID, MachineID: "citadel", Role: discovery.RoleCitadel, Epoch: 3, EnginePort: 7443, GuardianPort: 7444}
		discorig.Announce(n, "citadel", good)
		other := good
		other.GuildID, other.MachineID = "guild-2", "elsewhere"
		discorig.Announce(n, "citadel", other)
		n.AnnounceMDNS("citadel", fakenet.MDNSEntry{Instance: "x", Service: discovery.ServiceEngine, Port: 7443,
			TXT: map[string]string{"guild_id": guildID, "machine_id": "v2", "role": "cidadela", "epoch": "1", "v": "2"}})
		n.AnnounceMDNS("citadel", fakenet.MDNSEntry{Instance: "y", Service: discovery.ServiceEngine, Port: 7443,
			Addrs: []string{"fe80::1"}, TXT: map[string]string{"guild_id": guildID, "machine_id": "ll", "role": "cidadela", "epoch": "1", "v": "1"}})
		seen, err := discovery.Search(t.Context(), discovery.SearchConfig{
			Browser: discorig.Browser{Host: n.Host("tower")}, Clock: clock.Real{}, GuildID: guildID,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(seen) != 1 || seen[0].MachineID != "citadel" || seen[0].Epoch != 3 || seen[0].Role != discovery.RoleCitadel ||
			seen[0].Source != discovery.SourceMDNS || seen[0].Addrs[0] != "192.168.1.10" || seen[0].Port != 7443 {
			t.Fatalf("seen = %+v", seen)
		}
	})
}

func TestCandidatesAreOrderedByEpochThenByWhoAnsweredForItself(t *testing.T) {
	t.Parallel()
	tower := func(epoch uint64, citadel, ip string) discovery.Seen {
		return discovery.Seen{Source: discovery.SourceBroadcast, Addrs: []string{"192.168.1.200"}, Port: 7443, MachineID: "t-" + citadel,
			Role: discovery.RoleTower, Epoch: epoch, Citadel: &discovery.CitadelRef{MachineID: citadel, Addrs: []string{ip}, Port: 7443}}
	}
	citadel := func(epoch uint64, id, ip string) discovery.Seen {
		return discovery.Seen{Source: discovery.SourceBroadcast, Addrs: []string{ip}, Port: 7443, MachineID: id, Role: discovery.RoleCitadel, Epoch: epoch}
	}
	seen := []discovery.Seen{
		tower(2, "c2", "192.168.1.2"), citadel(1, "c1", "192.168.1.1"), citadel(2, "c2", "192.168.1.2"),
		tower(3, "c3", "192.168.1.3"), tower(2, "c2", "192.168.1.2"),
	}
	var got []string
	for _, c := range discovery.CandidatesFrom(seen, discovery.Known{}) {
		got = append(got, fmt.Sprintf("%s/%d", c.Addr, c.Epoch))
	}
	want := "192.168.1.3:7443/3 192.168.1.2:7443/2 192.168.1.1:7443/1"
	if strings.Join(got, " ") != want {
		t.Fatalf("candidates = %v, want %s", got, want)
	}
	// a machine that follows epoch 2 does not look at the Citadel of epoch 1
	for _, c := range discovery.CandidatesFrom(seen, discovery.Known{Epoch: 2}) {
		if c.Epoch < 2 {
			t.Fatalf("stale candidate %+v", c)
		}
	}
	// the address of a Tower is never a candidate
	for _, c := range discovery.CandidatesFrom(seen, discovery.Known{}) {
		if strings.HasPrefix(c.Addr, "192.168.1.200") {
			t.Fatalf("the address of a Tower is a candidate: %+v", c)
		}
	}
}

func TestTheSystemNetworkRefusesTheFastSuite(t *testing.T) {
	t.Parallel()
	if _, err := (discovery.SystemPacketNetwork{Suite: "fast"}).ListenShared(0); !errors.Is(err, discovery.ErrNetworkInFastSuite) {
		t.Fatalf("ListenShared = %v", err)
	}
	if _, err := (discovery.SystemPacketNetwork{Suite: "fast"}).Open(); !errors.Is(err, discovery.ErrNetworkInFastSuite) {
		t.Fatalf("Open = %v", err)
	}
	if err := (discovery.SystemBrowser{Suite: "fast"}).Browse(t.Context(), discovery.MDNSService, func(discovery.MDNSEntry) {}); !errors.Is(err, discovery.ErrNetworkInFastSuite) {
		t.Fatalf("Browse = %v", err)
	}
}
