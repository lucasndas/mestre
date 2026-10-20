package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/attachments"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The transcription on another machine of the Guild
// (docs/telegram/anexos-e-transcricao.md#transcrição-local): the Citadel converts
// the audio, sends the WAV to the Tower the sheets name in a transfer of kind
// `audio`, asks for the text with transcribe.request and gets it back as
// transcribe.result. The Tower calls the whisper-server of its own machine. Any
// failure leaves the audio to the next machine, and in the end to the sentence
// "[áudio sem transcrição: …]": the WAV goes nowhere the sheets do not point to.

const (
	// wavHeader is the size of the header of a WAV 16 kHz mono 16 bit.
	wavHeader = 44
	// wavBytesPerSecond is what one second of that WAV takes.
	wavBytesPerSecond = 16000 * 2
	// maxWAV is the biggest WAV a Tower takes: the longest audio the engine
	// transcribes, with some room for the header and the chunks of metadata.
	maxWAV = wavHeader + wavBytesPerSecond*int(attachments.MaxAudio/time.Second) + 1<<20
	// heldAudioTTL is how long a Tower keeps a WAV nobody asked to transcribe: the
	// time limit of the longest audio, so a long outage between the end of the
	// transfer and the request does not lose it.
	heldAudioTTL = attachments.MaxAudio * 2
	// maxHeldAudio bounds the WAVs a Tower holds at once (the Citadel sends one
	// at a time).
	maxHeldAudio = 2
	// transcribeSendWait is the time the Tower has to send the end of a transcription.
	transcribeSendWait = 10 * time.Second
	// transcribeWatch is how often the Citadel checks that the Tower is still connected.
	transcribeWatch = 2 * time.Second
)

// ---- the Tower ----

// towerAudio is the half of a Tower that transcribes: it receives the WAVs
// (kind `audio`), keeps them in memory and runs them through its whisper.
type towerAudio struct {
	clk     clock.Clock
	whisper attachments.Engine
	// one transcription at a time on this machine
	turn chan struct{}

	mu   sync.Mutex
	held map[string]heldAudio
}

type heldAudio struct {
	wav []byte
	at  time.Time
}

func newTowerAudio(clk clock.Clock, whisper attachments.Engine) *towerAudio {
	return &towerAudio{clk: clk, whisper: whisper, turn: make(chan struct{}, 1), held: map[string]heldAudio{}}
}

var _ protocol.Finalizer = (*towerAudio)(nil)
var _ protocol.Admitter = (*towerAudio)(nil)

// sweep forgets the WAVs nobody asked for. The caller holds the lock.
func (a *towerAudio) sweep() {
	now := a.clk.Now()
	for id, h := range a.held {
		if now.Sub(h.at) > heldAudioTTL {
			delete(a.held, id)
		}
	}
}

// Admit implements protocol.Admitter: no whisper, or a size that is not a WAV of
// the longest audio, and the transfer is refused before any part is sent.
func (a *towerAudio) Admit(_ context.Context, b protocol.XferBegin) error {
	if a.whisper == nil {
		return protocol.NewError(protocol.CodeUnavailable, "this machine does not transcribe")
	}
	if b.Size < wavHeader || b.Size > int64(maxWAV) {
		return protocol.NewError(protocol.CodeInvalidPayload, "an audio to transcribe is a WAV of %d to %d bytes, not %d", wavHeader, maxWAV, b.Size)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweep()
	return nil
}

// Finalize implements protocol.Finalizer: it keeps the WAV, once its header
// says it is one.
func (a *towerAudio) Finalize(_ context.Context, b protocol.XferBegin, file string) error {
	if a.whisper == nil {
		return protocol.NewError(protocol.CodeUnavailable, "this machine does not transcribe")
	}
	wav, err := os.ReadFile(file)
	if err != nil {
		return protocol.NewError(protocol.CodeUnavailable, "reading the audio: %v", err)
	}
	if len(wav) < wavHeader || len(wav) > maxWAV || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return protocol.NewError(protocol.CodeInvalidPayload, "the audio to transcribe is not a WAV")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweep()
	// the Citadel sends one at a time: when the room is gone, the oldest is one
	// whose request never came, and it makes way
	for len(a.held) >= maxHeldAudio {
		oldest := ""
		for id, h := range a.held {
			if oldest == "" || h.at.Before(a.held[oldest].at) {
				oldest = id
			}
		}
		delete(a.held, oldest)
	}
	a.held[b.XferID] = heldAudio{wav: wav, at: a.clk.Now()}
	return nil
}

// take gives the WAV of a transfer, once.
func (a *towerAudio) take(id string) ([]byte, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sweep()
	h, ok := a.held[id]
	delete(a.held, id)
	return h.wav, ok
}

// seconds is the length of a WAV 16 kHz mono 16 bit.
func seconds(wav []byte) time.Duration {
	return time.Duration(max(len(wav)-wavHeader, 0)) * time.Second / wavBytesPerSecond
}

// transcribe runs the WAV through the whisper of this machine, one at a time,
// inside the time limit of its length.
func (a *towerAudio) transcribe(ctx context.Context, wav []byte, lang string) (attachments.Result, error) {
	ctx, cancel := clock.WithTimeout(ctx, a.clk, attachments.Timeout(seconds(wav)))
	defer cancel()
	res, err := a.whisper.Transcribe(ctx, wav, lang)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return attachments.Result{}, errors.New("time out")
	}
	return res, err
}

// registerTowerTranscription puts in the router of a Tower the handler of
// transcribe.request. The ack says the audio is accepted; the text goes back as
// transcribe.result, because a transcription takes minutes and the messages of
// the connection are handled one after the other.
func (e *Engine) registerTowerTranscription(ctx context.Context, router *protocol.Router, audio *towerAudio) {
	router.HandleFunc(protocol.TypeTranscribeRequest, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
		if !e.fromCitadel(m.Peer) {
			return nil, protocol.NewError(protocol.CodeForbidden, "transcribe.request: only the Citadel gives this command")
		}
		p, ok := m.Payload.(*protocol.TranscribeRequest)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "transcribe.request: %T", m.Payload)
		}
		if audio.whisper == nil {
			return nil, protocol.NewError(protocol.CodeUnavailable, "transcribe.request: this machine does not transcribe")
		}
		// one at a time on this machine: a busy Tower refuses (the next machine
		// takes the audio), so no queue of WAVs grows in memory
		select {
		case audio.turn <- struct{}{}:
		default:
			return nil, protocol.NewError(protocol.CodeUnavailable, "transcribe.request: this machine is transcribing another audio")
		}
		wav, ok := audio.take(p.AudioXferID)
		if !ok {
			<-audio.turn
			return nil, protocol.NewError(protocol.CodeNotFound, "transcribe.request: this machine holds no audio %s", p.AudioXferID)
		}
		link, id, lang := m.Link, m.Env.ID, p.Language
		go func() {
			defer func() { <-audio.turn }()
			out := protocol.TranscribeResult{DurationS: seconds(wav).Seconds()}
			res, err := audio.transcribe(ctx, wav, lang)
			if err != nil {
				out.Error = err.Error()
			} else {
				out.Text, out.Language = res.Text, res.Lang
			}
			sctx, cancel := clock.WithTimeout(ctx, e.clk, transcribeSendWait)
			defer cancel()
			// transient: a text that the Citadel no longer waits for is of no use
			if _, err := link.Send(sctx, protocol.Outgoing{Type: protocol.TypeTranscribeResult, Payload: out, ReplyTo: id, Transient: true}); err != nil && ctx.Err() == nil {
				e.log.Warn("app: the end of a transcribe.request was not sent", "err", err)
			}
		}()
		return nil, nil
	})
}

// ---- the Citadel ----

// registerCitadelTranscription puts in the router of the Citadel the handler of
// transcribe.result: it goes to whoever asked, once, if it came from the Tower
// that was asked.
func (e *Engine) registerCitadelTranscription(router *protocol.Router, run *citadelRun) {
	router.HandleFunc(protocol.TypeTranscribeResult, run.gated(protocol.HandlerFunc(func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
		if p, ok := m.Payload.(*protocol.TranscribeResult); ok && e.sent.deliver(m.Env.ReplyTo, m.Peer.ID, *p) {
			return nil, nil
		}
		return nil, protocol.NewError(protocol.CodeForbidden, "transcribe.result: nobody asked for it from %s", m.Peer.ID)
	})))
}

// remoteTranscriber is the engine of transcription of a Tower, seen from the
// Citadel (attachments.Engine).
type remoteTranscriber struct {
	c       towerClient
	machine string
}

var _ attachments.Engine = remoteTranscriber{}

// Transcribe sends the WAV to the Tower and waits for the text. The Citadel
// gives the limit of time in ctx.
func (r remoteTranscriber) Transcribe(ctx context.Context, wav []byte, lang string) (attachments.Result, error) {
	link, err := r.c.link(r.machine)
	if err != nil {
		return attachments.Result{}, fmt.Errorf("%w: %v", attachments.ErrOffline, err)
	}
	// everything below lives on this session: when it is gone the WAV, the request
	// and the end that travels only on it are lost, and the next machine takes the audio
	ctx, lost := watchSession(ctx, r.c.clk, link)
	defer lost()
	offline := func(err error) error {
		if ctx.Err() != nil && errors.Is(context.Cause(ctx), errSessionLost) {
			return fmt.Errorf("%w: %s lost the connection", attachments.ErrOffline, r.machine)
		}
		return err
	}
	xfer := r.c.ids.NewString()
	err = link.Transfer(ctx, protocol.Transfer{Kind: protocol.KindAudio, ID: xfer, Source: protocol.Source{ReaderAt: bytes.NewReader(wav), Size: int64(len(wav))}})
	if err != nil {
		return attachments.Result{}, offline(fmt.Errorf("sending the audio to %s: %w", r.machine, err))
	}
	id := r.c.ids.NewString()
	ch, release := r.c.rep.expect(id, r.machine)
	defer release()
	if _, err := r.c.call(ctx, r.machine, towerCallWait, protocol.Outgoing{ID: id, Type: protocol.TypeTranscribeRequest,
		Payload: protocol.TranscribeRequest{AudioXferID: xfer, Language: lang}}); err != nil {
		return attachments.Result{}, offline(err)
	}
	select {
	case v := <-ch:
		res, _ := v.(protocol.TranscribeResult)
		if res.Error != "" {
			return attachments.Result{}, fmt.Errorf("%s: %s", r.machine, res.Error)
		}
		return attachments.Result{Text: res.Text, Lang: res.Language}, nil
	case <-ctx.Done():
		return attachments.Result{}, offline(ctx.Err())
	}
}

var errSessionLost = errors.New("the session with the Tower ended")

// watchSession returns a context that ends, with errSessionLost as its cause,
// when the session the link has now is not the one it has any more (the
// connection dropped, even if a new one is already back).
func watchSession(ctx context.Context, clk clock.Clock, link *protocol.Link) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	first := link.Session()
	go func() {
		tick := clk.NewTicker(transcribeWatch)
		defer tick.Stop()
		for {
			select {
			case <-tick.C():
				if s := link.Session(); s == nil || s != first {
					cancel(errSessionLost)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return ctx, func() { cancel(nil) }
}

// Fleet is who transcribes besides the Citadel itself, as the sheets tell it.
type Fleet struct {
	// CitadelGPU says the sheet of the Citadel has a GPU.
	CitadelGPU bool
	// Towers are the Towers that transcribe now, in the order of the Guild.
	Towers []attachments.Machine
}

// transcribers reads the sheets of the Guild: the Towers that are online, not
// excluded, whose sheet says they transcribe. A machine whose connection is not
// open is not offered either (the Citadel knows it is out, and the next takes it).
func (e *Engine) transcribers(ctx context.Context) Fleet {
	reg := e.sheets.Load()
	if reg == nil {
		return Fleet{}
	}
	sheets, err := reg.Sheets(ctx)
	if err != nil {
		e.log.Warn("app: the sheets could not be read to choose who transcribes", "err", err)
		return Fleet{}
	}
	client := e.towerClient()
	var f Fleet
	for _, s := range sheets {
		if s.MachineID == e.id.MachineID {
			f.CitadelGPU = len(s.Resources.GPUs) > 0
			continue
		}
		if m, ok := towerTranscriber(s, client); ok {
			f.Towers = append(f.Towers, m)
		}
	}
	return f
}

// transcribes says the sheet is of a machine that may be asked to transcribe:
// online, not excluded, and the sheet says the whisper-server was verified.
func transcribes(s machinesheet.Sheet) bool { return !s.Excluded && s.Online && s.Transcricao }

// towerTranscriber is the sheet of a Tower as a machine that transcribes, or not.
func towerTranscriber(s machinesheet.Sheet, c towerClient) (attachments.Machine, bool) {
	if !transcribes(s) {
		return attachments.Machine{}, false
	}
	if _, err := c.link(s.MachineID); err != nil {
		return attachments.Machine{}, false
	}
	name := s.Name
	if name == "" {
		name = s.MachineID
	}
	return attachments.Machine{Name: name, GPU: len(s.Resources.GPUs) > 0, Engine: remoteTranscriber{c: c, machine: s.MachineID}}, true
}

// orderMachines is the order the machines are tried in: the ones with a GPU
// first; among equals the Citadel (the default, by CPU) before the Towers.
func orderMachines(local *attachments.Machine, fleet Fleet) []attachments.Machine {
	var gpu, cpu []attachments.Machine
	add := func(m attachments.Machine) {
		if m.GPU {
			gpu = append(gpu, m)
		} else {
			cpu = append(cpu, m)
		}
	}
	if local != nil {
		l := *local
		l.GPU = fleet.CitadelGPU
		add(l)
	}
	for _, m := range fleet.Towers {
		add(m)
	}
	return append(gpu, cpu...)
}
