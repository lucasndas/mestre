package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/attachments"
	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/pkg/clock"
)

type countingEngine struct{ calls atomic.Int32 }

func (c *countingEngine) Transcribe(context.Context, []byte, string) (attachments.Result, error) {
	c.calls.Add(1)
	return attachments.Result{Text: "x"}, nil
}

// Part of CA-ANEXO-13: a transcribe.request that does not come from the Citadel is
// refused by the Tower, nothing is transcribed and the audio it holds stays.
func TestATowerRefusesATranscribeRequestThatIsNotFromTheCitadel(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	eng := &countingEngine{}
	audio := newTowerAudio(clk, eng)
	file := filepath.Join(t.TempDir(), "a.wav")
	if err := os.WriteFile(file, wavOf(1), 0o600); err != nil {
		t.Fatal(err)
	}
	id := "01J9Z0000000000000000000A1"
	if err := audio.Finalize(t.Context(), protocol.XferBegin{XferID: id, Kind: protocol.KindAudio}, file); err != nil {
		t.Fatal(err)
	}
	e := &Engine{epochs: &guild.Epochs{}, clk: clk, log: slog.New(slog.DiscardHandler)}
	router := protocol.NewRouter()
	e.registerTowerTranscription(t.Context(), router, audio)
	h, ok := router.Lookup(protocol.TypeTranscribeRequest)
	if !ok {
		t.Fatal("no handler")
	}
	for _, peer := range []protocol.Peer{{Role: protocol.RoleMachine, ID: "intruder"}, {Role: protocol.RoleMachine, ID: ""}, {Role: protocol.RoleBrowser, ID: "x"}} {
		_, err := h.Handle(t.Context(), &protocol.Message{Peer: peer, Payload: &protocol.TranscribeRequest{AudioXferID: id}})
		var pe *protocol.Error
		if !errors.As(err, &pe) || pe.Code != protocol.CodeForbidden {
			t.Errorf("peer %+v: err = %v, want forbidden", peer, err)
		}
	}
	if eng.calls.Load() != 0 {
		t.Error("a request that was refused transcribed")
	}
	if _, ok := audio.take(id); !ok {
		t.Error("a refused request consumed the audio")
	}
}
