package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/usage"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/providers"
)

// The use of the characters of a Tower (docs/plataforma/limites-de-uso.md): the
// transcript of Codex and the command of Antigravity are on the machine where
// the character runs, so the Tower reads them there, with the same reader the
// Citadel uses for its own characters, and tells the result to the Citadel in
// usage.reading. The cursors stay on the Tower; the records and the windows are
// the Citadel's, taken as if it had read them.

// towerUsageSink is the usageTaker of a Tower: what the reader would give the
// limits of use goes to the Citadel instead. A Tower has no limits of use.
type towerUsageSink struct {
	tell func(ctx context.Context, p protocol.UsageReading) error
}

func (towerUsageSink) OnEvent(context.Context, providers.Event) {}

func (s towerUsageSink) send(ctx context.Context, p protocol.UsageReading) error {
	return s.tell(ctx, p)
}

// tellCitadel sends the reading to the Citadel this Tower follows. It asks for
// an ack, so it waits in the outbox if the connection drops meanwhile.
func (e *Engine) tellCitadel(ctx context.Context, p protocol.UsageReading) error {
	link := e.towerLink()
	if link == nil {
		return errors.New("usage: this machine is not a Tower now")
	}
	_, err := link.Send(ctx, protocol.Outgoing{Type: protocol.TypeUsageReading, Payload: p})
	return err
}

func (s towerUsageSink) Reading(ctx context.Context, sub usage.Subject, u providers.Usage, rd usage.Reading) error {
	raw, err := json.Marshal(u)
	if err != nil {
		return err
	}
	return s.send(ctx, protocol.UsageReading{Character: sub.ID, Source: string(sub.Provider), Session: rd.Session, Usage: raw})
}

func (s towerUsageSink) SourceUnavailable(ctx context.Context, character, source, detail string) error {
	return s.send(ctx, protocol.UsageReading{Character: character, Source: source, Unavailable: detail})
}

// towerSubjects names the characters the Runtime of a Tower runs; a Tower knows
// nothing else about them (the Citadel completes the subject).
type towerSubjects struct{ rt *characters.Runtime }

func (t towerSubjects) Character(_ context.Context, id string) (usage.Subject, bool, error) {
	for _, c := range t.rt.Living() {
		if c.ID == id {
			return usage.Subject{ID: c.ID, Name: c.Name, Role: c.Role, Workspace: c.Workspace, Table: c.Table, Class: c.Class, Provider: c.Provider}, true, nil
		}
	}
	return usage.Subject{}, false, nil
}

// newTowerUsage is the reader of the characters of this Tower; nil when the
// machine runs no terminals. It reads only while the Citadel is connected: a
// reading goes on from its cursor, nothing is lost by waiting.
func (e *Engine) newTowerUsage() *usageReader {
	if e.rt == nil {
		return nil
	}
	rt := e.rt.rt
	return newUsageReader(&usageReader{
		svc: towerUsageSink{tell: e.tellCitadel}, dir: towerSubjects{rt},
		alive: func(context.Context) ([]characters.Character, error) {
			var out []characters.Character
			for _, c := range rt.Living() {
				out = append(out, c.Character)
			}
			return out, nil
		},
		adapters: providers.NewRegistry(providers.Options{Exec: e.deps.UsageSources.Exec}), src: e.deps.UsageSources,
		machine: e.id.MachineID, noSheet: true, pause: func() bool { l := e.towerLink(); return l == nil || !l.Connected() }, clk: e.clk, log: e.log})
}

// handleUsageReading is the handler of usage.reading on the Citadel: the reading
// that a Tower made of a character that runs on it.
func (e *Engine) handleUsageReading(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
	p, ok := m.Payload.(*protocol.UsageReading)
	if !ok {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "usage.reading: %T", m.Payload)
	}
	period := e.period.Load()
	if period == nil || period.usage == nil {
		return nil, nil
	}
	if err := e.runsOn(ctx, p.Character, m.Peer.ID); err != nil {
		return nil, protocol.NewError(protocol.CodeForbidden, "usage.reading: %v", err)
	}
	if err := period.usage.tower(ctx, *p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "usage.reading: %v", err)
	}
	return nil, nil
}

// tower takes the reading a Tower sent, as the reader would have taken its own.
func (h *hookUsage) tower(ctx context.Context, p protocol.UsageReading) error {
	if p.Unavailable != "" {
		return h.svc.SourceUnavailable(ctx, p.Character, p.Source, cleanDetail(p.Unavailable))
	}
	var u providers.Usage
	if err := json.Unmarshal(p.Usage, &u); err != nil {
		return fmt.Errorf("the usage is not readable: %w", err)
	}
	sub, ok, err := h.reader.dir.Character(ctx, p.Character)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("the character %q is not known", p.Character)
	}
	return h.svc.Reading(ctx, sub, u, usage.Reading{Session: p.Session})
}
