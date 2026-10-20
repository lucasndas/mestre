package telegram_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/attachments"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/testkit/faketelegram"
	"github.com/lucasndas/mestre/internal/users"
)

// fakeConverter makes "wav" of the audio: the content is only passed through.
type fakeConverter struct{ err error }

func (c fakeConverter) ToWAV(_ context.Context, in []byte) ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	return append([]byte("WAV:"), in...), nil
}

// fakeWhisper is the transcriber that answers a text, fails, or never answers.
type fakeWhisper struct {
	mu      sync.Mutex
	text    string
	err     error
	hang    bool
	started chan struct{}
	calls   int
}

func (f *fakeWhisper) Transcribe(ctx context.Context, wav []byte, _ string) (attachments.Result, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.hang {
		close(f.started)
		<-ctx.Done()
		return attachments.Result{}, ctx.Err()
	}
	if f.err != nil {
		return attachments.Result{}, f.err
	}
	return attachments.Result{Text: f.text, Lang: "pt"}, nil
}

type attWorld struct {
	*world
	root    string
	svc     *attachments.Service
	waits   []time.Duration
	whisper *fakeWhisper
}

func attachmentsWorld(t *testing.T) (*attWorld, users.User, faketelegram.User) {
	t.Helper()
	w := newWorld(t, rigOpts{routing: true})
	aw := &attWorld{world: w, root: t.TempDir(), whisper: &fakeWhisper{text: "faça o deploy da loja", started: make(chan struct{})}}
	tr := attachments.NewTranscription(w.clk, fakeConverter{}, func(context.Context) []attachments.Machine {
		return []attachments.Machine{{Name: "cidadela", Engine: aw.whisper}}
	}, "")
	svc, err := attachments.New(attachments.Options{DB: w.db, Clock: w.clk, Fetcher: w.opts.Client, Root: func(context.Context, string) (string, error) { return aw.root, nil },
		Transcriber: tr, Wait: func(_ context.Context, d time.Duration) error { aw.waits = append(aw.waits, d); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	aw.svc = svc
	w.routing.Files = svc
	u, tg := w.user("Ana", users.Administrator, "loja")
	return aw, u, tg
}

func (w *attWorld) send(tg faketelegram.User, kind string, m faketelegram.Media) {
	w.t.Helper()
	if _, err := w.fake.UserSendsFile(tg, kind, m); err != nil {
		w.t.Fatal(err)
	}
	w.poll()
	w.drain()
}

// CA-ANEXO-01: a PDF with a caption is kept under .mestre/anexos/, registered with
// its SHA-256 and delivered with the caption, the path, the type and the size.
func TestAPDFIsKeptRegisteredAndDelivered(t *testing.T) {
	t.Parallel()
	w, u, tg := attachmentsWorld(t)
	pdf := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 5<<20)...)
	w.fake.AddFile("doc-1", pdf)
	w.send(tg, "document", faketelegram.Media{FileID: "doc-1", Name: "relatório do mês.pdf", MIME: "application/pdf", Size: int64(len(pdf)), Caption: "segue o relatório"})
	got := w.delivered.all()
	if len(got) != 1 || got[0].File == nil {
		t.Fatalf("delivered %+v", got)
	}
	f := got[0].File
	if !strings.HasPrefix(f.Path, ".mestre/anexos/2026/10/") || !strings.HasSuffix(f.Path, "-relatório do mês.pdf") || f.MIME != "application/pdf" || f.Size != int64(len(pdf)) || len(f.SHA256) != 64 {
		t.Fatalf("stored %+v", f)
	}
	data, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(f.Path)))
	if err != nil || !bytes.Equal(data, pdf) {
		t.Fatalf("the file on disk: %v", err)
	}
	if info, _ := os.Stat(filepath.Join(w.root, filepath.FromSlash(f.Path))); info.Mode()&0o111 != 0 {
		t.Fatalf("an attachment is executable: %v", info.Mode())
	}
	row, err := w.svc.Get(w.ctx, f.ID)
	if err != nil || row.SHA256 != f.SHA256 || row.UserID != u.ID || row.MessageID == 0 || row.Caption != "segue o relatório" {
		t.Fatalf("row = %+v, %v", row, err)
	}
	item := telegram.NewItem(got[0], "gm")
	if !strings.HasPrefix(item.Text, "segue o relatório\n[anexo] .mestre/anexos/") || !strings.Contains(item.Text, "· application/pdf · 5,0 MB") || len(item.Attachments) != 1 {
		t.Fatalf("the item says %q", item.Text)
	}
}

// CA-ANEXO-02: a file above the limit is not downloaded and the user is told the limit.
func TestAFileAboveTheLimitIsNotDownloaded(t *testing.T) {
	t.Parallel()
	w, _, tg := attachmentsWorld(t)
	w.send(tg, "document", faketelegram.Media{FileID: "big", Name: "grande.zip", MIME: "application/zip", Size: 30 << 20})
	if n := w.fake.Calls("getFile"); n != 0 {
		t.Fatalf("getFile was called %d times", n)
	}
	if !strings.Contains(lastText(w.world, tg), "limite de 20,0 MB") || len(w.delivered.all()) != 0 {
		t.Fatalf("user told %q, delivered %d", lastText(w.world, tg), len(w.delivered.all()))
	}
}

// CA-ANEXO-03: a voice message with a fake whisper reaches the Grão-Mestre as text,
// marked as transcribed, and the original audio.
func TestAVoiceMessageIsTranscribedAndTheOriginalAudioIsKept(t *testing.T) {
	t.Parallel()
	w, _, tg := attachmentsWorld(t)
	w.fake.AddFile("voice-9", append([]byte("OggS"), make([]byte, 100)...))
	w.send(tg, "voice", faketelegram.Media{FileID: "voice-9", MIME: "audio/ogg", Size: 104, Duration: 42})
	got := w.delivered.all()
	if len(got) != 1 || got[0].File == nil || got[0].File.Transcript == nil {
		t.Fatalf("delivered %+v", got)
	}
	lines := got[0].File.Lines()
	if len(lines) != 2 || lines[0] != "[áudio transcrito · 0:42 · idioma pt] faça o deploy da loja" || !strings.HasPrefix(lines[1], "[anexo] .mestre/anexos/") || !strings.Contains(lines[1], "audio/ogg") {
		t.Fatalf("lines = %q", lines)
	}
}

// CA-ANEXO-04: a whisper that never answers: when the clock passes the time limit the
// Grão-Mestre gets the audio with the reason.
func TestAWhisperThatDoesNotAnswerGivesTheAudioWithTheReason(t *testing.T) {
	t.Parallel()
	w, _, tg := attachmentsWorld(t)
	w.whisper.hang = true
	w.fake.AddFile("voice-3", append([]byte("OggS"), make([]byte, 50)...))
	if _, err := w.fake.UserSendsFile(tg, "voice", faketelegram.Media{FileID: "voice-3", MIME: "audio/ogg", Size: 54, Duration: 10}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.poll()
	}()
	<-w.whisper.started // the transcription is under way, waiting on the clock
	w.clk.Advance(attachments.Timeout(10 * time.Second))
	<-done
	w.drain()
	got := w.delivered.all()
	if len(got) != 1 || got[0].File.Transcript == nil || got[0].File.Transcript.Err != "tempo esgotado" {
		t.Fatalf("delivered %+v", got)
	}
	if lines := got[0].File.Lines(); lines[0] != "[áudio sem transcrição: tempo esgotado]" || !strings.HasPrefix(lines[1], "[anexo]") {
		t.Fatalf("lines = %q", lines)
	}
}

// CA-ANEXO-09: a getFile that fails three times: three attempts with the waits of
// the clock, the user is told and the Grão-Mestre gets nothing.
func TestADownloadThatFailsIsTriedThreeTimesAndTheUserIsTold(t *testing.T) {
	t.Parallel()
	w, _, tg := attachmentsWorld(t)
	w.fake.AddFile("doc-2", []byte("%PDF-1.4 x"))
	for range 3 {
		w.fake.FailNext("getFile", faketelegram.Failure{Status: 500})
	}
	w.send(tg, "document", faketelegram.Media{FileID: "doc-2", Name: "a.pdf", MIME: "application/pdf", Size: 10})
	if n := w.fake.Calls("getFile"); n != 3 {
		t.Fatalf("getFile was called %d times", n)
	}
	if len(w.waits) != 2 || w.waits[0] != time.Second || w.waits[1] != 2*time.Second {
		t.Fatalf("waits = %v", w.waits)
	}
	if len(w.delivered.all()) != 0 || !strings.Contains(lastText(w.world, tg), "3 tentativas") {
		t.Fatalf("delivered %d, user told %q", len(w.delivered.all()), lastText(w.world, tg))
	}
}

// CA-ANEXO-10: a file that declares image/png and is a PDF: the detected type wins
// and the difference is recorded.
func TestTheDetectedTypeWinsOverTheDeclaredOne(t *testing.T) {
	t.Parallel()
	w, _, tg := attachmentsWorld(t)
	w.fake.AddFile("doc-3", []byte("%PDF-1.4 conteúdo"))
	w.send(tg, "document", faketelegram.Media{FileID: "doc-3", Name: "foto.png", MIME: "image/png", Size: 20})
	got := w.delivered.all()
	if len(got) != 1 || got[0].File.MIME != "application/pdf" || !got[0].File.Mismatch || got[0].File.Declared != "image/png" {
		t.Fatalf("delivered %+v", got)
	}
	row, _ := w.svc.Get(w.ctx, got[0].File.ID)
	if row.MIME != "application/pdf" || !row.Mismatch || row.Declared != "image/png" {
		t.Fatalf("row = %+v", row)
	}
}

// A photo with no name is foto-<id>.jpg, the biggest size is the one downloaded,
// and a name with separators and control characters is made safe.
func TestAPhotoGetsAnAutomaticNameAndTheBiggestSize(t *testing.T) {
	t.Parallel()
	w, _, tg := attachmentsWorld(t)
	w.fake.AddFile("ph-1", append([]byte("\xff\xd8\xff\xe0"), make([]byte, 64)...))
	w.send(tg, "photo", faketelegram.Media{FileID: "ph-1", Size: 68})
	got := w.delivered.all()
	if len(got) != 1 || !strings.HasSuffix(got[0].File.Path, got[0].File.ID+"-foto-"+got[0].File.ID+".jpg") || got[0].File.MIME != "image/jpeg" {
		t.Fatalf("delivered %+v", got)
	}
	w.fake.AddFile("doc-4", []byte("%PDF-1.4 x"))
	w.send(tg, "document", faketelegram.Media{FileID: "doc-4", Name: "../../etc/pass\x00wd\n.pdf", MIME: "application/pdf", Size: 10})
	got = w.delivered.all()
	if len(got) != 2 || strings.Contains(strings.TrimPrefix(got[1].File.Name, got[1].File.ID), "/") || strings.ContainsAny(got[1].File.Name, "\x00\n") {
		t.Fatalf("name = %q", got[1].File.Name)
	}
	if _, err := os.Stat(filepath.Join(w.root, ".mestre", "anexos", "2026", "10", got[1].File.Name)); err != nil {
		t.Fatalf("the file is not where the path says: %v", err)
	}
}

// Without a Files, nothing with a file goes to the Grão-Mestre (fail-closed).
func TestWithoutAttachmentsNoFileIsDelivered(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{routing: true})
	_, tg := w.user("Ana", users.Administrator, "loja")
	w.fake.AddFile("doc-5", []byte("%PDF-1.4 x"))
	if _, err := w.fake.UserSendsFile(tg, "document", faketelegram.Media{FileID: "doc-5", Name: "a.pdf", MIME: "application/pdf", Size: 10}); err != nil {
		t.Fatal(err)
	}
	w.poll()
	w.drain()
	if len(w.delivered.all()) != 0 || !strings.Contains(lastText(w, tg), "Não consigo receber arquivos") {
		t.Fatalf("delivered %d, user told %q", len(w.delivered.all()), lastText(w, tg))
	}
}

// The file of a message that waits (the Mestre locked, a workspace not chosen yet)
// is kept with it, and received only when the message is delivered.
func TestTheFileOfAMessageThatWaitsIsKeptWithIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{routing: true})
	u, _ := w.user("Ana", users.Administrator, "loja", "blog")
	ref := &attachments.Ref{ID: "f1", Name: "a.pdf", MIME: "application/pdf", Size: 10, Kind: attachments.KindDocument}
	in := telegram.Inbound{User: u, Kind: telegram.KindDocument, Text: "legenda", ChatID: 5, MessageID: 9, File: ref}
	st := w.bot.Store()
	if err := st.Hold(w.ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := st.Park(w.ctx, in); err != nil {
		t.Fatal(err)
	}
	held, err := st.Held(w.ctx)
	if err != nil || len(held) != 1 || held[0].File == nil || *held[0].File != *ref {
		t.Fatalf("held = %+v, %v", held, err)
	}
	parked, err := st.Parked(w.ctx, u.ID)
	if err != nil || len(parked) != 1 || parked[0].File == nil || parked[0].File.ID != "f1" {
		t.Fatalf("parked = %+v, %v", parked, err)
	}
}
