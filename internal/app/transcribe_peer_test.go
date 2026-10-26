package app

import (
	"errors"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// An audio to transcribe (transfer kind `audio`) is taken only from the Citadel: a transfer
// from any other machine is refused at xfer.begin, before any part is sent.
func TestATowerTakesTheAudioTransferOnlyFromTheCitadel(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	audio := newTowerAudio(clk, nopEngine{})
	fin := withAudio(map[string]protocol.Finalizer{}, audio, func() string { return "citadel-1" })[protocol.KindAudio]
	pf, ok := fin.(protocol.PeerFinalizer)
	if !ok {
		t.Fatal("the receiver of the audios does not depend on the peer")
	}
	begin := protocol.XferBegin{XferID: "x", Kind: protocol.KindAudio, Size: int64(len(wavOf(1)))}
	for _, machine := range []string{"torre-2", ""} {
		adm, ok := pf.FinalizerFor(machine).(protocol.Admitter)
		if !ok {
			t.Fatalf("the receiver for %q does not refuse at xfer.begin", machine)
		}
		var pe *protocol.Error
		if err := adm.Admit(t.Context(), begin); !errors.As(err, &pe) || pe.Code != protocol.CodeForbidden {
			t.Errorf("an audio from %q: %v, want forbidden", machine, err)
		}
		if err := pf.FinalizerFor(machine).Finalize(t.Context(), begin, "ignored"); err == nil {
			t.Errorf("an audio from %q was finalized", machine)
		}
	}
	for _, machine := range []string{"citadel-1", protocol.CitadelKey} {
		if got, ok := pf.FinalizerFor(machine).(*towerAudio); !ok || got != audio {
			t.Fatalf("the receiver for %q = %T, want the audio of the Tower", machine, pf.FinalizerFor(machine))
		}
	}
}

// The Tower says why a transcription failed with a code: the time limit or any other failure.
func TestATranscriptionThatFailedIsReportedWithItsCode(t *testing.T) {
	t.Parallel()
	if reason, code := failureOf(errTranscribeTimeout); reason == "" || code != protocol.TranscribeTimeout {
		t.Errorf("a time out = %q, %q", reason, code)
	}
	if reason, code := failureOf(errors.New("whisper: 500")); reason != "whisper: 500" || code != protocol.TranscribeFailed {
		t.Errorf("a failure = %q, %q", reason, code)
	}
}

// A failed transcription carries a code (transcribe.result.error_code) with its reason.
func TestATranscribeResultThatFailedCarriesAKnownCodeWithTheReason(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		r    protocol.TranscribeResult
		ok   bool
	}{
		{"text", protocol.TranscribeResult{Text: "oi"}, true},
		{"timeout", protocol.TranscribeResult{Error: "time out", ErrorCode: protocol.TranscribeTimeout}, true},
		{"failed", protocol.TranscribeResult{Error: "whisper 500", ErrorCode: protocol.TranscribeFailed}, true},
		{"unknown code", protocol.TranscribeResult{Error: "x", ErrorCode: "boom"}, false},
		{"code without reason", protocol.TranscribeResult{Text: "oi", ErrorCode: protocol.TranscribeFailed}, false},
	} {
		if err := c.r.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate = %v, want ok = %v", c.name, err, c.ok)
		}
	}
}
