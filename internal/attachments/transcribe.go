package attachments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/lucasndas/mestre/internal/attachments/ffmpeg"
	"github.com/lucasndas/mestre/pkg/clock"
)

// MinTimeout is the shortest time a transcription may take; the limit is the
// greater of it and twice the length of the audio.
const MinTimeout = 60 * time.Second

// ErrOffline says the machine that transcribes cannot be reached: the next one
// takes the audio.
var ErrOffline = errors.New("attachments: the machine that transcribes is offline")

// Converter turns the audio of a voice message (OGG/Opus) into WAV 16 kHz mono,
// the format of the transcriber. The engine of the Citadel converts before it
// sends the audio anywhere, so the machine that transcribes never converts.
type Converter interface {
	ToWAV(ctx context.Context, in []byte) ([]byte, error)
}

// Result is what a transcriber answers.
type Result struct {
	Text string
	Lang string
}

// Engine transcribes WAV audio: the whisper-server of this machine, or the
// engine of another machine of the Guild.
type Engine interface {
	Transcribe(ctx context.Context, wav []byte, lang string) (Result, error)
}

// Machine is a machine that transcribes.
type Machine struct {
	Name   string
	GPU    bool
	Engine Engine
}

// Transcription is the transcription of voice messages, one at a time, on the
// first machine that answers: the ones with a GPU first, the Citadel itself by
// default. The time limit runs on the injected clock.
type Transcription struct {
	Clock     clock.Clock
	Converter Converter
	// Machines are the machines that transcribe now, in the order they are
	// tried (the caller puts the ones with a GPU before the others and leaves
	// out the ones it knows are offline).
	Machines func(ctx context.Context) []Machine
	// Language is fixed (`pt`) or empty for automatic detection.
	Language string

	queue chan struct{}
}

// NewTranscription returns a Transcription that takes one audio at a time.
func NewTranscription(clk clock.Clock, conv Converter, machines func(ctx context.Context) []Machine, language string) *Transcription {
	return &Transcription{Clock: clk, Converter: conv, Machines: machines, Language: language, queue: make(chan struct{}, 1)}
}

// Timeout is the limit of a transcription of an audio of that length.
func Timeout(duration time.Duration) time.Duration {
	if d := 2 * duration; d > MinTimeout {
		return d
	}
	return MinTimeout
}

// Transcribe converts and transcribes. It never fails: when there is no text,
// the Transcript says why, and the audio is delivered anyway.
func (t *Transcription) Transcribe(ctx context.Context, audio []byte, duration time.Duration) Transcript {
	out := Transcript{Duration: duration}
	select {
	case t.queue <- struct{}{}: // one at a time, in the order they came
		defer func() { <-t.queue }()
	case <-ctx.Done():
		out.Err = "a fila de transcrição não andou"
		return out
	}
	ctx, cancel := clock.WithTimeout(ctx, t.Clock, Timeout(duration))
	defer cancel()
	wav, err := t.Converter.ToWAV(ctx, audio)
	if err != nil {
		out.Err = reason("a conversão do áudio falhou", ctx, err)
		return out
	}
	var machines []Machine
	if t.Machines != nil {
		machines = t.Machines(ctx)
	}
	if len(machines) == 0 {
		out.Err = "nenhuma máquina transcreve (sem modelo baixado ou sem máquina capaz)"
		return out
	}
	var last error
	for _, m := range machines {
		res, err := m.Engine.Transcribe(ctx, wav, t.Language)
		if err != nil {
			last = err
			if ctx.Err() != nil {
				break
			}
			continue // the next machine takes it
		}
		if noise(res.Text) {
			out.Err = "texto vazio ou só ruído"
			return out
		}
		out.Text, out.Lang = strings.TrimSpace(res.Text), res.Lang
		return out
	}
	out.Err = reason("o servidor de transcrição não respondeu", ctx, last)
	return out
}

// reason is the sentence the Grão-Mestre reads: the time limit, or the failure.
func reason(what string, ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "tempo esgotado"
	}
	if err == nil {
		return what
	}
	if errors.Is(err, ErrOffline) {
		return "servidor fora"
	}
	return what
}

// noise says the text is empty or only the marks a model writes for silence and
// noise ("[BLANK_AUDIO]", "(música)", "...").
func noise(text string) bool {
	depth := 0
	for _, r := range text {
		switch {
		case r == '[' || r == '(' || r == '*':
			depth++
		case r == ']' || r == ')':
			if depth > 0 {
				depth--
			}
		case depth == 0 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			return false
		}
	}
	return true
}

// FFmpeg converts with the ffmpeg of the machine (a dependency of the package and
// of the installer): WAV 16 kHz mono from the standard input to the standard output.
type FFmpeg struct {
	// Path is the program (default "ffmpeg").
	Path string
	// Run runs it; nil is os/exec. A test gives its own.
	Run func(ctx context.Context, name string, args []string, stdin []byte) ([]byte, error)
}

// Args are the arguments of the conversion.
func (FFmpeg) Args() []string {
	return []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-i", "pipe:0", "-ar", "16000", "-ac", "1", "-f", "wav", "pipe:1"}
}

// ToWAV implements Converter.
func (f FFmpeg) ToWAV(ctx context.Context, in []byte) ([]byte, error) {
	name := f.Path
	if name == "" {
		name = "ffmpeg"
	}
	run := f.Run
	if run == nil {
		run = ffmpeg.Run
	}
	out, err := run(ctx, name, f.Args(), in)
	if err != nil {
		return nil, fmt.Errorf("attachments: ffmpeg: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("attachments: ffmpeg gave no audio")
	}
	return out, nil
}

// Whisper is the whisper-server of this machine (whisper.cpp, POST /inference).
// It only talks to a server on the loopback: the audio never goes anywhere else.
type Whisper struct {
	// BaseURL is http://127.0.0.1:<port> of the instance.
	BaseURL string
	HTTP    *http.Client
	// Prompt is the short prompt in Portuguese the server is asked with.
	Prompt string
}

// ErrNotLocal is a server that is not on the loopback.
var ErrNotLocal = errors.New("attachments: the whisper server must be on 127.0.0.1")

func (w Whisper) local() error {
	u, err := url.Parse(w.BaseURL)
	if err != nil || u.Scheme != "http" {
		return ErrNotLocal
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return ErrNotLocal
	}
	return nil
}

// Transcribe implements Engine.
func (w Whisper) Transcribe(ctx context.Context, wav []byte, lang string) (Result, error) {
	if err := w.local(); err != nil {
		return Result{}, err
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		return Result{}, err
	}
	if _, err := part.Write(wav); err != nil {
		return Result{}, err
	}
	if lang == "" {
		lang = "auto"
	}
	for k, v := range map[string]string{"response_format": "verbose_json", "temperature": "0.0", "language": lang, "prompt": w.Prompt} {
		if v == "" {
			continue
		}
		if err := mw.WriteField(k, v); err != nil {
			return Result{}, err
		}
	}
	if err := mw.Close(); err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(w.BaseURL, "/")+"/inference", &buf)
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	hc := w.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("%w: %v", ErrOffline, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Result{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("attachments: the whisper server answered %d", resp.StatusCode)
	}
	var out struct {
		Text     string `json:"text"`
		Language string `json:"language"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Result{}, fmt.Errorf("attachments: the answer of the whisper server is not readable: %w", err)
	}
	return Result{Text: out.Text, Lang: out.Language}, nil
}
