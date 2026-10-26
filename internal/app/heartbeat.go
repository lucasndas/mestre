package app

import (
	"context"
	"sync"

	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/protocol"
)

// towerHeartbeat is the `machine.heartbeat` of a Tower: the disk that is
// free, read now from the probes of the machine sheet, and the lag of the
// replica. The CPU, the memory and the terminals stay 0 until the engine of the
// Tower measures them (milestone 5, with the hosting of the terminals).
func towerHeartbeat(ctx context.Context, sys machinesheet.SystemProbe, lag *float64) protocol.MachineHeartbeat {
	hb := protocol.MachineHeartbeat{ReplicaLagS: lag}
	if sys != nil {
		if res, err := sys.Resources(ctx); err == nil {
			hb.DiskFreeBytes = res.DiskFreeBytes
		}
	}
	return hb
}

// heartbeats is what the Citadel knows of the heartbeats of the Towers: the
// last one of each, and which Towers missed theirs for the time of the
// protocol (Timing.HeartbeatLost, 90 s). It lives in memory: a heartbeat is
// worth nothing after a restart, the Towers send another at once.
type heartbeats struct {
	mu   sync.Mutex
	last map[string]protocol.MachineHeartbeat
	lost map[string]bool
}

func newHeartbeats() *heartbeats {
	return &heartbeats{last: map[string]protocol.MachineHeartbeat{}, lost: map[string]bool{}}
}

// Handle is the handler of `machine.heartbeat`: it records the beat and the
// machine is alive again.
func (h *heartbeats) Handle(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
	hb, ok := m.Payload.(*protocol.MachineHeartbeat)
	if !ok {
		return nil, nil
	}
	h.mu.Lock()
	h.last[m.Peer.ID] = *hb
	delete(h.lost, m.Peer.ID)
	h.mu.Unlock()
	return nil, nil
}

// Lost is the hook of the protocol for a Tower that missed its heartbeat.
func (h *heartbeats) Lost(p protocol.Peer) {
	h.mu.Lock()
	h.lost[p.ID] = true
	h.mu.Unlock()
}

// Last is the last heartbeat of a machine.
func (h *heartbeats) Last(machine string) (protocol.MachineHeartbeat, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	hb, ok := h.last[machine]
	return hb, ok
}

// Online says whether a machine has not missed its heartbeat.
func (h *heartbeats) Online(machine string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.lost[machine]
}

// heartbeatPresence is the presence of the sheets: connected, with the
// heartbeat up to date (docs/guilda/ficha-da-maquina.md: `online`).
type heartbeatPresence struct {
	conn machinesheet.Presence
	hb   *heartbeats
}

func (p heartbeatPresence) Connected(id string) bool { return p.conn.Connected(id) && p.hb.Online(id) }
