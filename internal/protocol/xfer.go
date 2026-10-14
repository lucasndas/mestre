package protocol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/privdir"
)

// The window of the transfer in parts
// (docs/conectividade/protocolo.md#transferência-em-partes): at most
// XferWindow parts are in flight; the receiver tells the next part it
// expects every XferProgressEvery parts and after the last one, and that
// frees the window.
const (
	XferWindow        = 16
	XferProgressEvery = 8
)

// Finalizer is what a kind of transfer does with the file once its SHA-256
// matched: verify it (a bundle against the repository), put it where it
// belongs. It runs once per transfer. A *Error keeps its code, so
// `missing_prerequisite` reaches the sender.
type Finalizer interface {
	Finalize(ctx context.Context, begin XferBegin, file string) error
}

// Skipper is an optional part of a Finalizer: asked at xfer.begin, before any
// part is sent, it says whether the receiver already has this very content
// (the SHA-256 of the begin), and then the transfer ends at once with no part
// on the wire. A file that did not change since the last connection costs a
// hash, not a transfer.
type Skipper interface {
	Have(ctx context.Context, begin XferBegin) bool
}

// FinalizerFunc adapts a function to Finalizer.
type FinalizerFunc func(ctx context.Context, begin XferBegin, file string) error

// Finalize calls f.
func (f FinalizerFunc) Finalize(ctx context.Context, b XferBegin, file string) error {
	return f(ctx, b, file)
}

// XferConfig turns on the receiving side of the transfers.
type XferConfig struct {
	// Dir holds the temporary files, one pair per transfer, named by the
	// xfer_id. It is made private. A file nobody touched for 24 hours is
	// removed by Link.PruneTransfers.
	Dir string
	// Finalizers says what to do with a finished transfer of each kind; a
	// kind without one is refused as `unavailable`.
	Finalizers map[string]Finalizer
	// FreeSpace says how many bytes the disk of Dir still has (default: the
	// system's answer). A transfer that would leave less than
	// XferDiskReserve is refused as `unavailable` at xfer.begin.
	FreeSpace func(dir string) (uint64, error)

	// reserved is what the transfers in progress still need on the disk, by
	// xfer_id: two transfers that begin at once cannot both take the same
	// free space.
	rmu      sync.Mutex
	reserved map[string]uint64
}

// reserve takes need bytes for a transfer if avail covers them, the others
// already reserved and the reserve of the disk; it says whether it did.
func (c *XferConfig) reserve(id string, need, avail uint64) bool {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	var others uint64
	for k, v := range c.reserved {
		if k != id {
			others += v
		}
	}
	if avail < need+others+XferDiskReserve {
		return false
	}
	if c.reserved == nil {
		c.reserved = map[string]uint64{}
	}
	c.reserved[id] = need
	return true
}

// unreserve gives the space of a transfer back.
func (c *XferConfig) unreserve(id string) {
	c.rmu.Lock()
	delete(c.reserved, id)
	c.rmu.Unlock()
}

// XferDiskReserve is what a transfer must leave free on the disk.
const XferDiskReserve = 64 << 20

// Source is the content to transfer.
type Source struct {
	io.ReaderAt
	Size int64
}

// Transfer is a request to send a content in parts.
type Transfer struct {
	Kind string
	// Meta is for the receiver: for a bundle the table, the branch, the
	// prerequisite commits and the tip; for a file the destination path.
	Meta   Object
	Source Source
	// ChunkSize defaults to DefaultChunkSize (256 KiB).
	ChunkSize int
	// ID is the xfer_id; a new one is generated if empty. A retry of a
	// transfer that failed uses a new one.
	ID string
}

var (
	errLostConnection = errors.New("protocol: the connection was lost during the transfer")
	// errStalled ends an attempt whose receiver gave no sign for
	// Timing.XferStall: the next attempt sends xfer.begin again.
	errStalled = errors.New("protocol: the receiver of the transfer stopped answering")
)

// finishedXfer is the outcome the receiver remembers, so a sender that lost
// the connection before it heard `xfer.done` finds it again.
type finishedXfer struct {
	done XferDone
	at   time.Time
}

type recvXfer struct {
	begin      XferBegin
	parts      uint64
	path, meta string

	mu         sync.Mutex
	f          *os.File
	next       uint64
	since      int
	asked      bool // the sender was asked to go back to next; parts after it are dropped quietly
	finalizing bool
	failed     bool
}

type sendXfer struct {
	ready    chan uint64
	progress chan XferProgress
	done     chan XferDone
}

// xferHub is the machinery of the transfers of one Link, both as sender and
// as receiver. It is the Handler of the xfer.* messages.
type xferHub struct {
	l *Link

	mu       sync.Mutex
	recv     map[string]*recvXfer
	send     map[string]*sendXfer
	finished map[string]finishedXfer

	// bg are the goroutines that finish a received transfer (the check of the
	// content, the Finalizer, the end with an error): they outlive the session
	// that started them, so the Client and the Server wait for them in drain,
	// after the last session ended and before the owner closes what they write.
	bg      sync.WaitGroup
	cancels map[uint64]context.CancelFunc // of the goroutines in bg, by number
	nbg     uint64
}

func newXferHub(l *Link) *xferHub {
	return &xferHub{l: l, recv: map[string]*recvXfer{}, send: map[string]*sendXfer{}, finished: map[string]finishedXfer{}, cancels: map[uint64]context.CancelFunc{}}
}

// background runs f in a goroutine that drain waits for. Its context outlives
// the session of ctx (a transfer whose end arrived is finished even if the
// connection drops) but ends at drain.
func (h *xferHub) background(ctx context.Context, f func(ctx context.Context)) {
	bctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	h.mu.Lock()
	h.nbg++
	n := h.nbg
	h.cancels[n] = cancel
	h.bg.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.bg.Done()
		defer cancel()
		defer func() {
			h.mu.Lock()
			delete(h.cancels, n)
			h.mu.Unlock()
		}()
		f(bctx)
	}()
}

// drain cancels the goroutines that finish transfers and waits for them. The
// Client and the Server call it when they stop, once no session is left to
// start another.
func (h *xferHub) drain() {
	h.mu.Lock()
	for _, cancel := range h.cancels {
		cancel()
	}
	h.mu.Unlock()
	h.bg.Wait()
}

// Handle runs the five messages of the transfer.
func (h *xferHub) Handle(ctx context.Context, m *Message) (*Reply, error) {
	switch p := m.Payload.(type) {
	case *XferBegin:
		return h.onBegin(ctx, p)
	case *XferEnd:
		return h.onEnd(ctx, m, p)
	case *XferReady:
		h.push(p.XferID, func(s *sendXfer) { offer(s.ready, p.NextIndex) })
	case *XferProgress:
		h.push(p.XferID, func(s *sendXfer) { offer(s.progress, *p) })
	case *XferDone:
		h.push(p.XferID, func(s *sendXfer) { offer(s.done, *p) })
	}
	return nil, nil
}

func offer[T any](ch chan T, v T) {
	select {
	case ch <- v:
	default:
	}
}

func (h *xferHub) push(id string, f func(*sendXfer)) {
	h.mu.Lock()
	s := h.send[id]
	h.mu.Unlock()
	if s != nil {
		f(s)
	}
}

// ---- receiver ----

func (h *xferHub) onBegin(ctx context.Context, b *XferBegin) (*Reply, error) {
	cfg := h.l.cfg.Xfer
	if cfg == nil {
		return nil, NewError(CodeUnavailable, "this machine does not receive transfers")
	}
	finalizer, known := cfg.Finalizers[b.Kind]
	if !known {
		return nil, NewError(CodeUnavailable, "this machine has no receiver for transfers of kind %s", b.Kind)
	}
	h.mu.Lock()
	fin, ok := h.finished[b.XferID]
	if ok && fin.done.OK {
		h.mu.Unlock()
		return &Reply{Type: TypeXferReady, Payload: XferReady{XferID: b.XferID, NextIndex: b.Parts()}, Transient: true}, nil
	}
	delete(h.finished, b.XferID) // a transfer that failed starts again
	prev := h.recv[b.XferID]
	h.mu.Unlock()
	if prev != nil {
		prev.mu.Lock()
		finalizing := prev.finalizing
		prev.mu.Unlock()
		if finalizing {
			// Every part is here and the end is being checked: xfer.done
			// goes to the sender when it finishes.
			return &Reply{Type: TypeXferReady, Payload: XferReady{XferID: b.XferID, NextIndex: b.Parts()}, Transient: true}, nil
		}
	}
	if s, ok := finalizer.(Skipper); ok && s.Have(ctx, *b) {
		// The content is already here: say every part arrived, and the end
		// finds the transfer done.
		h.mu.Lock()
		h.finished[b.XferID] = finishedXfer{done: XferDone{XferID: b.XferID, OK: true}, at: h.l.cfg.Clock.Now()}
		h.mu.Unlock()
		return &Reply{Type: TypeXferReady, Payload: XferReady{XferID: b.XferID, NextIndex: b.Parts()}, Transient: true}, nil
	}
	if prev != nil {
		prev.mu.Lock()
		if prev.f != nil {
			_ = prev.f.Close()
			prev.f = nil
		}
		prev.mu.Unlock()
	}
	if err := h.checkSpace(cfg, *b); err != nil {
		return nil, err
	}
	r, err := h.openRecv(cfg, *b)
	if err != nil {
		cfg.unreserve(b.XferID)
		return nil, err
	}
	h.mu.Lock()
	h.recv[b.XferID] = r
	h.mu.Unlock()
	return &Reply{Type: TypeXferReady, Payload: XferReady{XferID: b.XferID, NextIndex: r.next}, Transient: true}, nil
}

// checkSpace refuses, as `unavailable`, a transfer that the disk cannot take
// with XferDiskReserve to spare; what an earlier attempt already wrote counts
// as taken. A disk that cannot be asked does not stop the transfer.
func (h *xferHub) checkSpace(cfg *XferConfig, b XferBegin) error {
	if err := privdir.Ensure(cfg.Dir); err != nil {
		return NewError(CodeUnavailable, "transfer folder: %v", err)
	}
	free := cfg.FreeSpace
	if free == nil {
		free = diskFree
	}
	avail, err := free(cfg.Dir)
	if err != nil {
		h.l.cfg.Logger.Warn("protocol: free disk space unknown", "dir", cfg.Dir, "err", err)
		return nil
	}
	need := uint64(b.Size)
	if st, err := os.Stat(filepath.Join(cfg.Dir, b.XferID+".part")); err == nil {
		need -= min(need, uint64(st.Size()))
	}
	if !cfg.reserve(b.XferID, need, avail) {
		return NewError(CodeUnavailable, "the disk has %d bytes free; transfer %s needs %d more and %d to spare", avail, b.XferID, need, XferDiskReserve)
	}
	return nil
}

// openRecv opens the temporary file of the transfer, and resumes it if the
// file is there from an earlier attempt of the same content.
func (h *xferHub) openRecv(cfg *XferConfig, b XferBegin) (*recvXfer, error) {
	if err := privdir.Ensure(cfg.Dir); err != nil {
		return nil, NewError(CodeUnavailable, "transfer folder: %v", err)
	}
	r := &recvXfer{begin: b, parts: b.Parts(), path: filepath.Join(cfg.Dir, b.XferID+".part"), meta: filepath.Join(cfg.Dir, b.XferID+".json")}
	f, err := os.OpenFile(r.path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, NewError(CodeUnavailable, "transfer file: %v", err)
	}
	var prev XferBegin
	same := false
	if raw, err := os.ReadFile(r.meta); err == nil && json.Unmarshal(raw, &prev) == nil {
		same = prev.Kind == b.Kind && prev.Size == b.Size && prev.SHA256 == b.SHA256 && prev.Chunk() == b.Chunk()
	}
	var next uint64
	if same {
		if st, err := f.Stat(); err == nil {
			next = min(uint64(st.Size())/uint64(b.Chunk()), r.parts)
		}
	}
	if err := f.Truncate(int64(next) * int64(b.Chunk())); err != nil {
		_ = f.Close()
		return nil, NewError(CodeUnavailable, "transfer file: %v", err)
	}
	if !same {
		if err := os.WriteFile(r.meta, jsonOf(b), 0o600); err != nil {
			_ = f.Close()
			return nil, NewError(CodeUnavailable, "transfer file: %v", err)
		}
	}
	h.touch(r)
	r.f, r.next = f, next
	return r, nil
}

func (h *xferHub) touch(r *recvXfer) {
	now := h.l.cfg.Clock.Now()
	_ = os.Chtimes(r.path, now, now)
	_ = os.Chtimes(r.meta, now, now)
}

// onPart writes a part. It runs on the goroutine of the parts of the
// connection, never on its read loop: a slow disk does not stop the pongs.
func (h *xferHub) onPart(ctx context.Context, s *Session, f DataFrame) {
	id := f.ID.String()
	h.mu.Lock()
	r := h.recv[id]
	h.mu.Unlock()
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil || r.finalizing || r.failed || f.Pos < r.next {
		return
	}
	progress := func(resend bool) {
		r.since = 0
		h.touch(r)
		_ = s.sendTyped(TypeXferProgress, XferProgress{XferID: id, NextIndex: r.next, Resend: resend})
	}
	if f.Pos > r.next {
		// A part is missing: ask once to go back to it; what is already on
		// the way after it is dropped quietly.
		if !r.asked {
			r.asked = true
			progress(true)
		}
		return
	}
	if f.Pos >= r.parts {
		return
	}
	chunk := int64(r.begin.Chunk())
	want := min(chunk, r.begin.Size-int64(f.Pos)*chunk)
	if int64(len(f.Data)) != want {
		// The sender cuts the parts by the size and the chunk it announced:
		// a part of another size is not a loss on the way, and sending it
		// again would not mend it. The transfer ends with the error.
		r.failed = true
		h.background(ctx, func(ctx context.Context) {
			h.fail(ctx, r, "", NewError(CodeInvalidPayload, "part %d of transfer %s has %d bytes, not %d", f.Pos, id, len(f.Data), want))
		})
		return
	}
	if _, err := r.f.WriteAt(f.Data, int64(f.Pos)*chunk); err != nil {
		r.failed = true
		h.background(ctx, func(ctx context.Context) {
			h.fail(ctx, r, "", NewError(CodeUnavailable, "writing the transfer: %v", err))
		})
		return
	}
	r.asked = false
	r.next++
	r.since++
	if r.since >= XferProgressEvery || r.next == r.parts {
		progress(false)
	}
}

func (h *xferHub) onEnd(ctx context.Context, m *Message, e *XferEnd) (*Reply, error) {
	h.mu.Lock()
	fin, cached := h.finished[e.XferID]
	r := h.recv[e.XferID]
	h.mu.Unlock()
	if cached {
		return &Reply{Type: TypeXferDone, Payload: fin.done, Transient: true}, nil
	}
	if r == nil {
		return &Reply{Type: TypeXferDone, Payload: XferDone{XferID: e.XferID, Error: NewError(CodeNotFound, "no transfer %s", e.XferID)}, Transient: true}, nil
	}
	r.mu.Lock()
	if r.finalizing {
		r.mu.Unlock()
		return nil, nil
	}
	r.finalizing = true
	r.mu.Unlock()
	endID := m.Env.ID
	h.background(ctx, func(ctx context.Context) { h.finalize(ctx, r, endID) })
	return nil, nil
}

// finalize checks the SHA-256 of the whole content, hands it to the
// Finalizer of the kind and tells the sender how it went.
func (h *xferHub) finalize(ctx context.Context, r *recvXfer, endID string) {
	r.mu.Lock()
	next := r.next
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}
	r.mu.Unlock()
	if next != r.parts {
		h.fail(ctx, r, endID, NewError(CodeNotFound, "transfer %s has %d of %d parts", r.begin.XferID, next, r.parts))
		return
	}
	sum, err := fileSHA256(r.path)
	if err != nil {
		h.fail(ctx, r, endID, NewError(CodeUnavailable, "reading the transfer: %v", err))
		return
	}
	if sum != r.begin.SHA256 {
		h.fail(ctx, r, endID, NewError(CodeHashMismatch, "SHA-256 of transfer %s is %s, expected %s", r.begin.XferID, sum, r.begin.SHA256))
		return
	}
	if err := h.l.cfg.Xfer.Finalizers[r.begin.Kind].Finalize(ctx, r.begin, r.path); err != nil {
		h.fail(ctx, r, endID, asError(err))
		return
	}
	h.conclude(ctx, r, endID, XferDone{XferID: r.begin.XferID, OK: true})
}

func (h *xferHub) fail(ctx context.Context, r *recvXfer, endID string, e *Error) {
	r.mu.Lock()
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}
	r.mu.Unlock()
	h.conclude(ctx, r, endID, XferDone{XferID: r.begin.XferID, Error: e})
}

func (h *xferHub) conclude(ctx context.Context, r *recvXfer, endID string, d XferDone) {
	_ = os.Remove(r.path)
	_ = os.Remove(r.meta)
	if cfg := h.l.cfg.Xfer; cfg != nil {
		cfg.unreserve(d.XferID)
	}
	h.mu.Lock()
	delete(h.recv, d.XferID)
	h.finished[d.XferID] = finishedXfer{done: d, at: h.l.cfg.Clock.Now()}
	h.mu.Unlock()
	if _, err := h.l.Send(ctx, Outgoing{Type: TypeXferDone, Payload: d, ReplyTo: endID, Transient: true}); err != nil && !errors.Is(err, ErrNotConnected) && ctx.Err() == nil {
		h.l.cfg.Logger.Warn("protocol: xfer.done", "xfer", d.XferID, "err", err)
	}
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// PruneTransfers removes the temporary files that nobody touched for ttl
// (24 hours in the protocol) and forgets the old outcomes; it returns how
// many files it removed. The Client and the Server run it by themselves
// (pruneLoop), over every link that shares the folder.
func (l *Link) PruneTransfers(ttl time.Duration) int {
	return pruneTransfers(l.cfg, []*Link{l}, ttl)
}

func pruneTransfers(c *Config, links []*Link, ttl time.Duration) int {
	cfg := c.Xfer
	if cfg == nil {
		return 0
	}
	now := c.Clock.Now()
	for _, l := range links {
		h := l.xfer
		h.mu.Lock()
		for id, f := range h.finished {
			if now.Sub(f.at) > ttl {
				delete(h.finished, id)
			}
		}
		h.mu.Unlock()
	}
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		return 0
	}
	// The description file is touched when the transfer begins and at every
	// report of progress; the data file's own modification time is not used.
	n := 0
	for _, e := range entries {
		name := e.Name()
		id, isMeta := strings.CutSuffix(name, ".json")
		if !isMeta {
			// a data file whose description is gone (a crash between the
			// two) will never be resumed: it goes when it is as old as ttl
			if part, ok := strings.CutSuffix(name, ".part"); ok {
				if _, err := os.Stat(filepath.Join(cfg.Dir, part+".json")); errors.Is(err, os.ErrNotExist) {
					if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > ttl {
						forgetRecv(cfg, links, part)
						if os.Remove(filepath.Join(cfg.Dir, name)) == nil {
							n++
						}
					}
				}
			}
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) <= ttl {
			continue
		}
		forgetRecv(cfg, links, id)
		for _, f := range []string{id + ".part", name} {
			if os.Remove(filepath.Join(cfg.Dir, f)) == nil {
				n++
			}
		}
	}
	return n
}

// forgetRecv closes the temporary file of a transfer that is going away (a
// file open cannot be removed on Windows), forgets it and gives back its
// space.
func forgetRecv(cfg *XferConfig, links []*Link, id string) {
	cfg.unreserve(id)
	for _, l := range links {
		h := l.xfer
		h.mu.Lock()
		if r := h.recv[id]; r != nil {
			r.mu.Lock()
			if r.f != nil {
				_ = r.f.Close()
				r.f = nil
			}
			r.mu.Unlock()
			delete(h.recv, id)
		}
		h.mu.Unlock()
	}
}

// releaseFiles closes the temporary files of the transfers in progress when
// the connection ends: the content stays on disk and the next xfer.begin of
// the sender opens it again and goes on.
func (h *xferHub) releaseFiles() {
	h.mu.Lock()
	rs := make([]*recvXfer, 0, len(h.recv))
	for _, r := range h.recv {
		rs = append(rs, r)
	}
	h.mu.Unlock()
	for _, r := range rs {
		r.mu.Lock()
		release := r.f != nil && !r.finalizing
		if release {
			_ = r.f.Close()
			r.f = nil
		}
		r.mu.Unlock()
		// the space comes back; the next xfer.begin reserves it again
		if cfg := h.l.cfg.Xfer; release && cfg != nil {
			cfg.unreserve(r.begin.XferID)
		}
	}
}

// ---- sender ----

// Transfer sends the content in parts and returns when the receiver reported
// the outcome: nil if it checked the SHA-256 and finished, the *Error it
// gave otherwise (`missing_prerequisite`, `hash_mismatch`…). When the
// connection drops it waits for the next one, repeats xfer.begin with the
// same xfer_id and goes on from the part the receiver already has; it does
// the same, on the same connection, when the receiver gives no sign for
// Timing.XferStall.
func (l *Link) Transfer(ctx context.Context, t Transfer) error {
	if t.Source.ReaderAt == nil {
		return errors.New("protocol: transfer without a source")
	}
	begin := XferBegin{XferID: t.ID, Kind: t.Kind, Size: t.Source.Size, ChunkSize: t.ChunkSize, Meta: t.Meta}
	if begin.XferID == "" {
		begin.XferID = l.gen.NewString()
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(t.Source, 0, t.Source.Size)); err != nil {
		return fmt.Errorf("protocol: hashing the content to transfer: %w", err)
	}
	begin.SHA256 = hex.EncodeToString(h.Sum(nil))
	if err := begin.Validate(); err != nil {
		return NewError(CodeInvalidPayload, "transfer: %v", err)
	}

	hub := l.xfer
	snd := &sendXfer{ready: make(chan uint64, 4), progress: make(chan XferProgress, XferWindow), done: make(chan XferDone, 4)}
	hub.mu.Lock()
	hub.send[begin.XferID] = snd
	hub.mu.Unlock()
	defer func() {
		hub.mu.Lock()
		delete(hub.send, begin.XferID)
		hub.mu.Unlock()
	}()

	for {
		sess, err := l.waitConnected(ctx)
		if err != nil {
			return err
		}
		done, err := hub.attempt(ctx, snd, sess, begin, t.Source)
		switch {
		case errors.Is(err, errLostConnection), errors.Is(err, errStalled):
			continue
		case err != nil:
			return err
		case done.OK:
			return nil
		case done.Error != nil:
			return done.Error
		}
		return NewError(CodeInternal, "transfer %s failed with no reason", begin.XferID)
	}
}

func drain[T any](ch chan T) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// attempt is one run over one connection: begin, parts, end.
func (h *xferHub) attempt(ctx context.Context, snd *sendXfer, sess *Session, begin XferBegin, src Source) (XferDone, error) {
	drain(snd.ready)
	drain(snd.progress)
	drain(snd.done)
	l := h.l
	// send puts a control message on the connection and returns the channel
	// of its ack: a refusal (the kind has no receiver, the disk is full)
	// comes there, and ends the transfer with that error.
	send := func(o Outgoing) (<-chan AckResult, func(), error) {
		o.Transient = true
		o.ID = l.gen.NewString()
		ch, release := l.track(o.ID)
		if _, err := l.Send(ctx, o); err != nil {
			release()
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			return nil, nil, errLostConnection
		}
		return ch, release, nil
	}
	refused := func(a AckResult) XferDone {
		return XferDone{XferID: begin.XferID, Error: a.Error}
	}
	// Every sign of the receiver restarts the stall timer; without one for
	// Timing.XferStall the attempt ends and the next one begins again.
	stall := l.cfg.Clock.NewTimer(l.cfg.Timing.XferStall)
	defer stall.Stop()
	alive := func() { stall.Reset(l.cfg.Timing.XferStall) }
	beginAck, release, err := send(Outgoing{Type: TypeXferBegin, Payload: begin})
	if err != nil {
		return XferDone{}, err
	}
	defer release()
	var acked uint64
waitReady:
	for {
		select {
		case acked = <-snd.ready:
			alive()
			break waitReady
		case a := <-beginAck:
			if a.Status == AckError {
				return refused(a), nil
			}
			alive()
		case d := <-snd.done:
			return d, nil
		case <-stall.C():
			return XferDone{}, errStalled
		case <-sess.Done():
			return XferDone{}, errLostConnection
		case <-ctx.Done():
			return XferDone{}, ctx.Err()
		}
	}

	id, _ := ParseULID(begin.XferID) // validated by Transfer
	total, chunk := begin.Parts(), int64(begin.Chunk())
	sent := acked
	for acked < total {
		for sent < total && sent-acked < XferWindow {
			off := int64(sent) * chunk
			buf := make([]byte, min(chunk, begin.Size-off))
			if _, err := src.ReadAt(buf, off); err != nil && (!errors.Is(err, io.EOF) || len(buf) == 0) {
				return XferDone{}, fmt.Errorf("protocol: reading the content to transfer: %w", err)
			}
			if err := l.SendData(ctx, DataFrame{Type: DataXfer, ID: id, Pos: sent, Data: buf}); err != nil {
				if ctx.Err() != nil {
					return XferDone{}, ctx.Err()
				}
				return XferDone{}, errLostConnection
			}
			sent++
		}
		select {
		case p := <-snd.progress:
			alive()
			if p.Resend || p.NextIndex < acked { // the receiver lost ground: send again from there
				sent = p.NextIndex
			}
			acked = p.NextIndex
		case d := <-snd.done: // the receiver gave up
			return d, nil
		case <-stall.C():
			return XferDone{}, errStalled
		case <-sess.Done():
			return XferDone{}, errLostConnection
		case <-ctx.Done():
			return XferDone{}, ctx.Err()
		}
	}
	// The end waits for every part to be on the receiver: the control lane
	// has priority over the data lane and would overtake the last parts.
	endAck, releaseEnd, err := send(Outgoing{Type: TypeXferEnd, Payload: XferEnd{XferID: begin.XferID}})
	if err != nil {
		return XferDone{}, err
	}
	defer releaseEnd()
	alive()
	for {
		select {
		case d := <-snd.done:
			return d, nil
		case a := <-endAck:
			if a.Status == AckError {
				return refused(a), nil
			}
			alive()
		case <-stall.C():
			return XferDone{}, errStalled
		case <-sess.Done():
			return XferDone{}, errLostConnection
		case <-ctx.Done():
			return XferDone{}, ctx.Err()
		}
	}
}
