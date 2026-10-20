package attachments_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/attachments"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

type conv struct{}

func (conv) ToWAV(_ context.Context, in []byte) ([]byte, error) { return in, nil }

type engine struct {
	mu    sync.Mutex
	calls int
	res   attachments.Result
	err   error
}

func (e *engine) Transcribe(context.Context, []byte, string) (attachments.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	return e.res, e.err
}

// CA-ANEXO-05: with a Torre with GPU and the Cidadela, the Torre goes first; when it is
// offline the audio is transcribed by the Cidadela itself.
func TestAnOfflineTowerLeavesTheAudioToTheCitadel(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	tower := &engine{err: attachments.ErrOffline}
	citadel := &engine{res: attachments.Result{Text: "olá mundo", Lang: "pt"}}
	tr := attachments.NewTranscription(clk, conv{}, func(context.Context) []attachments.Machine {
		return []attachments.Machine{{Name: "torre", GPU: true, Engine: tower}, {Name: "cidadela", Engine: citadel}}
	}, "")
	got := tr.Transcribe(t.Context(), []byte("a"), 5*time.Second)
	if got.Err != "" || got.Text != "olá mundo" || tower.calls != 1 || citadel.calls != 1 {
		t.Fatalf("got %+v (tower %d, citadel %d)", got, tower.calls, citadel.calls)
	}
	// with the Torre back, the Cidadela is not asked
	tower.err, tower.res = nil, attachments.Result{Text: "da torre", Lang: "pt"}
	if got = tr.Transcribe(t.Context(), []byte("a"), 5*time.Second); got.Text != "da torre" || citadel.calls != 1 {
		t.Fatalf("got %+v (citadel %d)", got, citadel.calls)
	}
}

func TestTheFailuresOfATranscriptSayWhy(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	one := func(c attachments.Converter, e attachments.Engine) attachments.Transcript {
		tr := attachments.NewTranscription(clk, c, func(context.Context) []attachments.Machine {
			if e == nil {
				return nil
			}
			return []attachments.Machine{{Engine: e}}
		}, "")
		return tr.Transcribe(t.Context(), []byte("a"), time.Second)
	}
	if got := one(conv{}, &engine{res: attachments.Result{Text: " [BLANK_AUDIO] "}}); got.Err != "texto vazio ou só ruído" {
		t.Errorf("noise: %+v", got)
	}
	if got := one(conv{}, &engine{res: attachments.Result{Text: "(música)..."}}); got.Err != "texto vazio ou só ruído" {
		t.Errorf("music: %+v", got)
	}
	if got := one(conv{}, &engine{err: attachments.ErrOffline}); got.Err != "servidor fora" {
		t.Errorf("offline: %+v", got)
	}
	if got := one(conv{}, nil); !strings.Contains(got.Err, "nenhuma máquina") {
		t.Errorf("no machine: %+v", got)
	}
	if got := one(failingConv{}, &engine{}); !strings.Contains(got.Err, "conversão") {
		t.Errorf("conversion: %+v", got)
	}
	if got := attachments.Timeout(10 * time.Second); got != time.Minute {
		t.Errorf("timeout of a short audio = %v", got)
	}
	if got := attachments.Timeout(90 * time.Second); got != 3*time.Minute {
		t.Errorf("timeout of 90 s = %v", got)
	}
}

type failingConv struct{}

func (failingConv) ToWAV(context.Context, []byte) ([]byte, error) { return nil, errors.New("boom") }

func TestFFmpegConvertsToWAV16kMonoThroughThePipes(t *testing.T) {
	t.Parallel()
	var gotName string
	var gotArgs []string
	var gotIn []byte
	f := attachments.FFmpeg{Path: "/usr/bin/ffmpeg", Run: func(_ context.Context, name string, args []string, stdin []byte) ([]byte, error) {
		gotName, gotArgs, gotIn = name, args, stdin
		return []byte("RIFFwav"), nil
	}}
	out, err := f.ToWAV(t.Context(), []byte("ogg"))
	if err != nil || string(out) != "RIFFwav" || gotName != "/usr/bin/ffmpeg" || string(gotIn) != "ogg" {
		t.Fatalf("out %q, %v", out, err)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"-ar 16000", "-ac 1", "-f wav", "-i pipe:0", "pipe:1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q lack %q", joined, want)
		}
	}
	empty := attachments.FFmpeg{Run: func(context.Context, string, []string, []byte) ([]byte, error) { return nil, nil }}
	if _, err := empty.ToWAV(t.Context(), nil); err == nil {
		t.Fatal("an empty conversion was accepted")
	}
}

func TestTheWhisperServerOnlyTalksToTheLoopback(t *testing.T) {
	t.Parallel()
	var gotPath, gotType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotType = r.URL.Path, r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"text":" bom dia ","language":"pt"}`))
	}))
	defer srv.Close()
	w := attachments.Whisper{BaseURL: srv.URL, Prompt: "Conversa em português."} // httptest listens on 127.0.0.1
	res, err := w.Transcribe(t.Context(), []byte("RIFF"), "")
	if err != nil || res.Text != " bom dia " || res.Lang != "pt" || gotPath != "/inference" || !strings.HasPrefix(gotType, "multipart/form-data") {
		t.Fatalf("res %+v, %v (%s %s)", res, err, gotPath, gotType)
	}
	for _, want := range []string{"RIFF", `name="language"`, "auto", "Conversa em português."} {
		if !strings.Contains(string(gotBody), want) {
			t.Fatalf("the request lacks %q", want)
		}
	}
	for _, base := range []string{"http://example.com", "http://192.168.1.5:7447", "https://127.0.0.1:7447", "http://localhost:7447", "127.0.0.1:7447"} {
		if _, err := (attachments.Whisper{BaseURL: base}).Transcribe(t.Context(), []byte("a"), "pt"); !errors.Is(err, attachments.ErrNotLocal) {
			t.Errorf("%s: %v", base, err)
		}
	}
	down := httptest.NewServer(http.NotFoundHandler())
	url := down.URL
	down.Close()
	if _, err := (attachments.Whisper{BaseURL: url}).Transcribe(t.Context(), []byte("a"), "pt"); !errors.Is(err, attachments.ErrOffline) {
		t.Fatalf("a server that is down: %v", err)
	}
}

func TestSafeNameKeepsNoPathNoControlAndAtMostAHundredCharacters(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"../../etc/passwd":   "passwd",
		`C:\Users\x\a.pdf`:   "a.pdf",
		"a\x00b\nc.txt":      "a_b_c.txt",
		"  ..hidden.. ":      "hidden",
		"relatório final.md": "relatório final.md",
	} {
		if got := attachments.SafeName(in, attachments.KindDocument, "abcd1234", "text/plain"); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("a", 300) + ".pdf"
	if got := attachments.SafeName(long, attachments.KindDocument, "id", "application/pdf"); len([]rune(got)) != 100 || !strings.HasSuffix(got, ".pdf") {
		t.Errorf("long name = %q (%d)", got, len(got))
	}
	if got := attachments.SafeName("", attachments.KindPhoto, "id1", "image/jpeg"); got != "foto-id1.jpg" {
		t.Errorf("photo = %q", got)
	}
	if got := attachments.SafeName("..", attachments.KindDocument, "id1", "application/pdf"); got != "arquivo-id1.pdf" {
		t.Errorf("only dots = %q", got)
	}
}

func TestTheTypeComesFromTheContent(t *testing.T) {
	t.Parallel()
	for want, data := range map[string][]byte{
		"application/pdf": []byte("%PDF-1.4"),
		"audio/ogg":       []byte("OggS\x00\x02"),
		"image/png":       []byte("\x89PNG\r\n\x1a\n...."),
		"image/jpeg":      []byte("\xff\xd8\xff\xe0abc"),
		"text/plain":      []byte("só texto"),
	} {
		if got := attachments.Detect(data); got != want {
			t.Errorf("Detect = %q, want %q", got, want)
		}
	}
}

// A file bigger than the limit whatever the size declared: the download is cut and refused.
type fetcher struct {
	size  int64
	data  []byte
	calls int
}

func (f *fetcher) GetFile(context.Context, string) (string, int64, error) {
	f.calls++
	return "files/x", f.size, nil
}

func (f *fetcher) Download(_ context.Context, _ string, limit int64) ([]byte, error) {
	if int64(len(f.data)) > limit {
		return nil, &attachments.TooLargeError{Limit: limit}
	}
	return f.data, nil
}

func TestTheLimitHoldsWhateverTheSizeDeclared(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &fetcher{size: 10, data: make([]byte, 200)}
	svc, err := attachments.New(attachments.Options{DB: db, Clock: clk, Fetcher: f, Limit: 100, Root: func(context.Context, string) (string, error) { return t.TempDir(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	var big *attachments.TooLargeError
	// declared 10 bytes, really 200: refused while downloading, not retried
	if _, err := svc.Receive(ctx, attachments.Request{Workspace: "loja", File: attachments.Ref{ID: "a", Size: 10, Kind: attachments.KindDocument}}); !errors.As(err, &big) || f.calls != 1 {
		t.Fatalf("lying size: %v (calls %d)", err, f.calls)
	}
	// declared above the limit: nothing asked of the channel
	f.calls = 0
	if _, err := svc.Receive(ctx, attachments.Request{Workspace: "loja", File: attachments.Ref{ID: "a", Size: 101, Kind: attachments.KindDocument}}); !errors.As(err, &big) || f.calls != 0 {
		t.Fatalf("declared size: %v (calls %d)", err, f.calls)
	}
	// a workspace with no folder keeps nothing
	nf, _ := attachments.New(attachments.Options{DB: db, Clock: clk, Fetcher: f, Root: func(context.Context, string) (string, error) { return "", errors.New("no") }})
	if _, err := nf.Receive(ctx, attachments.Request{Workspace: "x", File: attachments.Ref{ID: "a", Size: 1}}); !errors.Is(err, attachments.ErrWorkspace) {
		t.Fatalf("no folder: %v", err)
	}
}

func TestACaptionAndATranscriptAreMasked(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('loja', 'lucas/loja', 'lucas/loja', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	const token = "ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD"
	tr := attachments.NewTranscription(clk, conv{}, func(context.Context) []attachments.Machine {
		return []attachments.Machine{{Engine: &engine{res: attachments.Result{Text: "o token é " + token, Lang: "pt"}}}}
	}, "")
	f := &fetcher{size: 4, data: []byte("OggS")}
	svc, err := attachments.New(attachments.Options{DB: db, Clock: clk, Fetcher: f, Transcriber: tr, Root: func(context.Context, string) (string, error) { return t.TempDir(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	st, err := svc.Receive(ctx, attachments.Request{Workspace: "loja", Caption: "use " + token, File: attachments.Ref{ID: "a", Size: 4, Kind: attachments.KindVoice}})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(st.Lines(), "\n")
	if strings.Contains(all, token) {
		t.Fatalf("a secret reached the delivery: %q", all)
	}
	row, _ := svc.Get(ctx, st.ID)
	if strings.Contains(row.Caption+row.Transcript.Text, token) {
		t.Fatal("a secret reached the database")
	}
}
