//go:build full

package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/attachments"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// fakeWhisper is the whisper-server of a Tower: it keeps what it was sent and
// answers with the text it was given.
type fakeWhisper struct {
	*httptest.Server
	mu    sync.Mutex
	sums  [][32]byte
	langs []string
}

func newFakeWhisper(t *testing.T, status int, text string) *fakeWhisper {
	t.Helper()
	w := &fakeWhisper{}
	w.Server = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		defer f.Close()
		data, _ := io.ReadAll(f)
		w.mu.Lock()
		w.sums = append(w.sums, sha256.Sum256(data))
		w.langs = append(w.langs, r.FormValue("language"))
		w.mu.Unlock()
		if status != http.StatusOK {
			http.Error(rw, "down", status)
			return
		}
		_ = json.NewEncoder(rw).Encode(map[string]string{"text": text, "language": "pt"})
	}))
	t.Cleanup(w.Close)
	return w
}

// A Citadel and a Tower of the rig, the Citadel with no whisper of its own.
func transcriptionRig(t *testing.T, whisper attachments.Engine) (r *rig, a, b *inproc) {
	t.Helper()
	r = newRig(t)
	a = r.node("a")
	b = r.node("b", a)
	b.sheet.SetTranscription(true)
	b.setup = func(e *Engine) { e.deps.Whisper = whisper }
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })
	r.waitFor("the sheet of b to say it transcribes", func() bool { return len(a.e.transcribers(t.Context()).Towers) == 1 })
	return r, a, b
}

// transcription is the transcription of the Citadel a: the audio is converted to
// the WAV given (the conversion has its own tests) and the machines are the ones
// the sheets say.
func transcription(a *inproc, wav []byte) *attachments.Transcription {
	return attachments.NewTranscription(clock.Real{}, fixedConverter{wav: wav}, func(ctx context.Context) []attachments.Machine {
		return orderMachines(nil, a.e.transcribers(ctx))
	}, "pt")
}

// CA-ANEXO-11: a Citadel with no whisper-server and a real Tower whose sheet says
// it transcribes: the WAV (several parts long) crosses the WSS, the whisper of the
// Tower gets exactly it, and the text comes back to the Citadel.
func TestACitadelWithoutWhisperHasTheTowerTranscribeTheVoice(t *testing.T) {
	t.Parallel()
	w := newFakeWhisper(t, http.StatusOK, "olá, mestre, abra o PR do login")
	_, a, _ := transcriptionRig(t, attachments.Whisper{BaseURL: w.URL})
	wav := wavOf(40) // 1.2 MB: five parts of 256 KiB
	for i := wavHeader; i < len(wav); i += 7 {
		wav[i] = byte(i)
	}

	got := transcription(a, wav).Transcribe(t.Context(), []byte("ogg"), 40*time.Second)
	if got.Err != "" || got.Text != "olá, mestre, abra o PR do login" || got.Lang != "pt" {
		t.Fatalf("transcript = %+v", got)
	}
	w.mu.Lock()
	if len(w.sums) != 1 || w.sums[0] != sha256.Sum256(wav) || w.langs[0] != "pt" {
		t.Errorf("the whisper of the Tower got %d audios (language %v), want the WAV as it left the Citadel", len(w.sums), w.langs)
	}
	w.mu.Unlock()

	// the text reaches the Grand Master marked as a transcribed audio: the
	// delivery of the Citadel (the attachments service) builds the lines
	ctx := t.Context()
	svc := voiceService(t, transcription(a, wav))
	st, err := svc.Receive(ctx, attachments.Request{Workspace: "loja", File: attachments.Ref{ID: "v1", Size: 4, Kind: attachments.KindVoice, Duration: 40 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(st.Lines(), "\n")
	if !strings.Contains(lines, "[áudio transcrito · 0:40 · idioma pt] olá, mestre, abra o PR do login") || strings.Contains(lines, "sem transcrição") {
		t.Errorf("the lines for the Grand Master = %q, want the text marked as a transcribed audio", lines)
	}
}

// Part of CA-ANEXO-13: the Tower says it transcribes but its whisper is down, or it has none:
// the voice goes on with the reason, and a Tower that is gone is not asked at all.
func TestATowerThatCannotTranscribeLeavesTheVoiceWithTheReason(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		whisper func(t *testing.T) attachments.Engine
	}{
		{"the whisper of the Tower answers 500", func(t *testing.T) attachments.Engine {
			return attachments.Whisper{BaseURL: newFakeWhisper(t, http.StatusInternalServerError, "").URL}
		}},
		{"the Tower has no whisper", func(*testing.T) attachments.Engine { return nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, a, _ := transcriptionRig(t, c.whisper(t))
			got := transcription(a, wavOf(2)).Transcribe(t.Context(), []byte("ogg"), 2*time.Second)
			if got.Text != "" || got.Err == "" {
				t.Fatalf("transcript = %+v, want the reason and no text", got)
			}
		})
	}
	t.Run("the Tower is gone", func(t *testing.T) {
		t.Parallel()
		r, a, b := transcriptionRig(t, attachments.Whisper{BaseURL: newFakeWhisper(t, http.StatusOK, "x").URL})
		b.stop()
		r.waitFor("the Citadel to see b gone", func() bool { return len(a.e.transcribers(t.Context()).Towers) == 0 })
		got := transcription(a, wavOf(2)).Transcribe(t.Context(), []byte("ogg"), 2*time.Second)
		if got.Text != "" || got.Err == "" {
			t.Fatalf("transcript = %+v, want the reason and no text", got)
		}
	})
}

// Part of CA-ANEXO-13: the Tower drops while it transcribes: the Citadel does not wait
// for the whole time limit, it gives the machine up (the next one takes the audio).
func TestATowerThatDropsWhileItTranscribesIsGivenUpAtOnce(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
	}))
	t.Cleanup(func() { close(release); slow.Close() })
	r, a, b := transcriptionRig(t, attachments.Whisper{BaseURL: slow.URL})
	_ = r
	done := make(chan attachments.Transcript, 1)
	go func() { done <- transcription(a, wavOf(2)).Transcribe(t.Context(), []byte("ogg"), 40*time.Second) }()
	<-started
	b.stop()
	limit := clock.Real{}.NewTimer(30 * time.Second)
	defer limit.Stop()
	select {
	case got := <-done:
		if got.Text != "" || got.Err != "servidor fora" {
			t.Fatalf("transcript = %+v, want the Tower given up as out", got)
		}
	case <-limit.C():
		t.Fatal("the Citadel went on waiting for a Tower that is gone")
	}
}
