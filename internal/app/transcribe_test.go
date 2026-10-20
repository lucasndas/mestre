package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/attachments"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/pkg/clock"
)

type nopEngine struct{}

func (nopEngine) Transcribe(context.Context, []byte, string) (attachments.Result, error) {
	return attachments.Result{Text: "x"}, nil
}

// wavOf is a WAV 16 kHz mono of that many seconds (silence).
func wavOf(seconds int) []byte {
	b := make([]byte, wavHeader+seconds*wavBytesPerSecond)
	copy(b, "RIFF")
	copy(b[8:], "WAVE")
	return b
}

type fixedConverter struct{ wav []byte }

func (c fixedConverter) ToWAV(context.Context, []byte) ([]byte, error) { return c.wav, nil }

func machineNames(ms []attachments.Machine) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

// CA-ANEXO-12: only the machines that are online, not excluded and whose sheet
// says they transcribe are asked, the ones with a GPU first and, between equals,
// the Citadel before the Towers.
func TestOnlyTheMachinesWhoseSheetsSayTheyTranscribeAreChosenGPUFirst(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		sheet machinesheet.Sheet
		want  bool
	}{
		{"transcribes", machinesheet.Sheet{Online: true, Transcricao: true}, true},
		{"offline", machinesheet.Sheet{Transcricao: true}, false},
		{"excluded", machinesheet.Sheet{Online: true, Transcricao: true, Excluded: true}, false},
		{"no whisper in the sheet", machinesheet.Sheet{Online: true}, false},
	} {
		if got := transcribes(c.sheet); got != c.want {
			t.Errorf("%s: transcribes = %v, want %v", c.name, got, c.want)
		}
	}
	local := &attachments.Machine{Name: "cidadela", Engine: nopEngine{}}
	tower := func(name string, gpu bool) attachments.Machine {
		return attachments.Machine{Name: name, GPU: gpu, Engine: nopEngine{}}
	}
	for _, c := range []struct {
		name  string
		local *attachments.Machine
		fleet Fleet
		want  []string
	}{
		{"the Citadel is the default", local, Fleet{Towers: []attachments.Machine{tower("t1", false)}}, []string{"cidadela", "t1"}},
		{"a Tower with a GPU goes first", local, Fleet{Towers: []attachments.Machine{tower("t1", false), tower("t2", true)}}, []string{"t2", "cidadela", "t1"}},
		{"the Citadel with a GPU beats a Tower with a GPU", local, Fleet{CitadelGPU: true, Towers: []attachments.Machine{tower("t1", true)}}, []string{"cidadela", "t1"}},
		{"a Citadel without whisper leaves it to the Tower", nil, Fleet{Towers: []attachments.Machine{tower("t1", false)}}, []string{"t1"}},
		{"nobody", nil, Fleet{}, nil},
	} {
		if got := machineNames(orderMachines(c.local, c.fleet)); !equal(got, c.want) {
			t.Errorf("%s: order = %v, want %v", c.name, got, c.want)
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CA-ANEXO-13: a Tower holds only a WAV, only when it has a whisper, only a few
// at a time and only for a while; each WAV is taken once.
func TestATowerHoldsOnlyAValidWAVForAWhileAndGivesItOnce(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	dir := t.TempDir()
	file := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	begin := func(id string, size int) protocol.XferBegin {
		return protocol.XferBegin{XferID: id, Kind: protocol.KindAudio, Size: int64(size)}
	}
	code := func(err error) string {
		var e *protocol.Error
		if errors.As(err, &e) {
			return e.Code
		}
		return "none"
	}

	deaf := newTowerAudio(clk, nil)
	if got := code(deaf.Admit(t.Context(), begin("a", 100))); got != protocol.CodeUnavailable {
		t.Errorf("a Tower without whisper admits an audio: %s", got)
	}
	if got := code(deaf.Finalize(t.Context(), begin("a", 100), file("a.wav", wavOf(1)))); got != protocol.CodeUnavailable {
		t.Errorf("a Tower without whisper keeps an audio: %s", got)
	}

	a := newTowerAudio(clk, nopEngine{})
	for name, size := range map[string]int{"too small": wavHeader - 1, "too big": maxWAV + 1} {
		if got := code(a.Admit(t.Context(), begin("b", size))); got != protocol.CodeInvalidPayload {
			t.Errorf("%s: admitted with %s", name, got)
		}
	}
	notWAV := wavOf(1)
	copy(notWAV, "OggS")
	if got := code(a.Finalize(t.Context(), begin("c", len(notWAV)), file("c.wav", notWAV))); got != protocol.CodeInvalidPayload {
		t.Errorf("an audio that is not a WAV was kept: %s", got)
	}
	if _, ok := a.take("c"); ok {
		t.Error("an audio that is not a WAV is held")
	}

	for _, id := range []string{"d", "e", "f"} {
		if err := a.Finalize(t.Context(), begin(id, len(wavOf(1))), file(id+".wav", wavOf(1))); err != nil {
			t.Fatalf("keeping %s: %v", id, err)
		}
		clk.Advance(time.Second)
	}
	// only maxHeldAudio are held: the oldest, whose request never came, made way
	if _, ok := a.take("d"); ok {
		t.Errorf("more than %d audios are held", maxHeldAudio)
	}
	if err := a.Finalize(t.Context(), begin("d", len(wavOf(1))), file("d.wav", wavOf(1))); err != nil {
		t.Fatal(err)
	}
	if wav, ok := a.take("d"); !ok || len(wav) != len(wavOf(1)) {
		t.Fatalf("take d = %d bytes, %v", len(wav), ok)
	}
	if _, ok := a.take("d"); ok {
		t.Error("an audio was given twice")
	}
	clk.Advance(heldAudioTTL + time.Second)
	if _, ok := a.take("e"); ok {
		t.Error("an audio nobody asked for was held past its time")
	}
	if got := seconds(wavOf(42)); got != 42*time.Second {
		t.Errorf("seconds = %v", got)
	}
}

// Part of CA-ANEXO-13: the end of a transcription is taken from the Tower that was
// asked, once, and from nobody else.
func TestTheEndOfATranscriptionIsTakenOnlyFromTheTowerAsked(t *testing.T) {
	t.Parallel()
	var r replies
	ch, release := r.expect("req1", "tower-b")
	defer release()
	if r.deliver("req1", "tower-c", protocol.TranscribeResult{Text: "x"}) {
		t.Error("the end was taken from a Tower that was not asked")
	}
	if r.deliver("unknown", "tower-b", protocol.TranscribeResult{Text: "x"}) {
		t.Error("the end of a request nobody made was taken")
	}
	if !r.deliver("req1", "tower-b", protocol.TranscribeResult{Text: "ok"}) {
		t.Fatal("the end from the Tower asked was refused")
	}
	if got := (<-ch).(protocol.TranscribeResult); got.Text != "ok" {
		t.Errorf("end = %+v", got)
	}
	if r.deliver("req1", "tower-b", protocol.TranscribeResult{Text: "again"}) {
		t.Error("the end was taken twice")
	}
}

// CA-ANEXO-13: with no machine that transcribes, or with the Tower out of reach,
// the voice goes on with the reason and nothing is invented.
func TestWithNoMachineThatTranscribesTheVoiceGoesOnWithTheReason(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	conv := fixedConverter{wav: wavOf(1)}

	none := attachments.NewTranscription(clk, conv, func(context.Context) []attachments.Machine { return orderMachines(nil, Fleet{}) }, "")
	if got := none.Transcribe(t.Context(), []byte("ogg"), 3*time.Second); got.Text != "" || got.Err == "" {
		t.Errorf("no machine: %+v", got)
	}

	// a Tower the sheets name, whose connection is not there
	gone := remoteTranscriber{c: towerClient{srv: &atomic.Pointer[protocol.Server]{}, clk: clk, ids: protocol.NewIDGen(clk), rep: &replies{}}, machine: "b"}
	if _, err := gone.Transcribe(t.Context(), conv.wav, "pt"); !errors.Is(err, attachments.ErrOffline) {
		t.Fatalf("a Tower out of reach: %v", err)
	}
	out := attachments.NewTranscription(clk, conv, func(context.Context) []attachments.Machine {
		return orderMachines(nil, Fleet{Towers: []attachments.Machine{{Name: "b", Engine: gone}}})
	}, "")
	if got := out.Transcribe(t.Context(), []byte("ogg"), 3*time.Second); got.Text != "" || got.Err != "servidor fora" {
		t.Errorf("a Tower out of reach: %+v", got)
	}
}
