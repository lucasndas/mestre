package attach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The ways a run of the session can end, besides a failure.
var (
	errDetached = errors.New("attach: detached")
	errEnded    = errors.New("attach: the terminal ended")
)

// outcome is how one connection to the panel ended.
type outcome int

const (
	lost         outcome = iota // the connection fell: try again
	sessionEnded                // the Citadel ended the session
)

// session is one `mestre attach` after the login: the terminal followed, the
// picture of it, and the keys.
type session struct {
	env   Env
	c     *panelClient
	opts  options
	info  panel.TerminalInfo
	write bool
	frame protocol.ULID

	scr *screen

	mu       sync.Mutex
	conn     *websocket.Conn // the open socket, nil while it is down
	next     uint64          // the byte of the output that comes next
	status   string          // what the line of state says about the stream
	notice   string
	noticeAt time.Time
	dirty    chan struct{}
	typed    bool // a key was sent: the typing session needs its end

	signal atomic.Bool
	msgSeq atomic.Int64
	cancel context.CancelCauseFunc
	// detaching is set when the person pressed the detach key: the socket is
	// closed in order (the end of the typing first), and the run ends with it.
	detaching atomic.Bool
}

func newSession(env Env, c *panelClient, opts options, info panel.TerminalInfo, write bool) *session {
	if info.Cols <= 0 || info.Rows <= 0 {
		info.Cols, info.Rows = 200, 50
	}
	s := &session{env: env, c: c, opts: opts, info: info, write: write, scr: newScreen(info.Cols, info.Rows), dirty: make(chan struct{}, 1)}
	s.frame, _ = protocol.ParseULID(info.FrameID)
	s.signal.Store(true)
	return s
}

func (s *session) retry() time.Duration {
	if s.env.Retry > 0 {
		return s.env.Retry
	}
	return DefaultRetry
}

// run follows the terminal until the person detaches (nil), the terminal ends
// (nil), or something fails.
func (s *session) run(parent context.Context) error {
	ctx, cancel := context.WithCancelCause(parent)
	s.cancel = cancel
	defer cancel(nil)
	restore, err := s.env.Console.EnterRaw()
	if err != nil {
		return fail(codeUsage, "run it in a terminal", "cannot put the terminal in raw mode: %v", err)
	}
	var ended bool
	defer func() {
		restore()
		if ended {
			fmt.Fprintln(s.env.Stdout, "terminal encerrado")
		}
	}()
	defer s.scr.Close()

	go s.readKeys(ctx)
	go s.watchEnded(ctx)
	go s.draw(ctx)

	var lostSince time.Time
	for {
		out, err := s.once(ctx, &lostSince)
		if s.detaching.Load() {
			<-ctx.Done()
		}
		if cause := context.Cause(ctx); cause != nil {
			s.closeConn(true)
			switch {
			case errors.Is(cause, errEnded):
				ended = true
				return nil
			case errors.Is(cause, errDetached), errors.Is(cause, context.Canceled):
				return nil
			default:
				var f failure
				if errors.As(cause, &f) {
					return f
				}
				return cause
			}
		}
		var f failure
		if errors.As(err, &f) {
			s.closeConn(false)
			return f
		}
		if out == sessionEnded {
			s.closeConn(false)
			return errSessionEnded
		}
		// lost: the Citadel may have changed, the network may be down
		s.closeConn(false)
		now := s.env.Clock.Now()
		if lostSince.IsZero() {
			lostSince = now
		}
		s.setStatus("connection lost, trying again")
		if now.Sub(lostSince) >= s.retry() {
			return fail(codeUnavailable, "run mestre debug on the machine of the Citadel; the agent was not touched", "the connection to the panel was lost for %s", s.retry())
		}
		if err := clock.Sleep(ctx, s.env.Clock, time.Second); err != nil {
			continue // the context ended: the check at the top of the loop decides
		}
	}
}

// once opens the socket, follows the terminal and reads until it falls. It
// returns why it ended; lostSince is reset when the stream came up.
func (s *session) once(ctx context.Context, lostSince *time.Time) (outcome, error) {
	conn, status, err := s.dial(ctx)
	if err != nil {
		switch {
		case status == http.StatusUnauthorized:
			return sessionEnded, nil
		case status >= 300 && status < 400, status == 0:
			// the Citadel moved or does not answer: find it again before the next try
			rctx, cancel := clock.WithTimeout(ctx, s.env.Clock, 10*time.Second)
			defer cancel()
			if rerr := s.c.resolve(rctx, s.c.base); rerr != nil {
				return lost, nil
			}
		}
		return lost, nil
	}
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	conn.SetReadLimit(4 << 20)
	// a new connection brings the whole buffer again: start the picture over
	s.scr.Reset()
	s.mu.Lock()
	s.next = 0
	s.mu.Unlock()
	if err := s.send(ctx, conn, panel.TypeUISubscribe, panel.UISubscribe{Topics: []string{"terminal:" + s.info.TerminalID}}); err != nil {
		return lost, nil
	}
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == panel.CloseSessionEnded {
				return sessionEnded, nil
			}
			return lost, nil
		}
		switch typ {
		case websocket.MessageBinary:
			s.onFrame(data)
		case websocket.MessageText:
			if err := s.onMessage(ctx, data, lostSince); err != nil {
				return lost, err
			}
		}
	}
}

// dial opens /ws/ui with the cookie and the Origin of the panel.
func (s *session) dial(ctx context.Context) (*websocket.Conn, int, error) {
	u := url.URL{Scheme: "wss", Host: s.c.base.Host, Path: "/ws/ui"}
	h := http.Header{}
	s.c.sign(h)
	dctx, cancel := clock.WithTimeout(ctx, s.env.Clock, 20*time.Second)
	defer cancel()
	conn, res, err := websocket.Dial(dctx, u.String(), &websocket.DialOptions{HTTPClient: s.c.ws, HTTPHeader: h, CompressionMode: websocket.CompressionDisabled})
	status := 0
	if res != nil {
		status = res.StatusCode
	}
	return conn, status, err
}

func (s *session) send(ctx context.Context, conn *websocket.Conn, typ string, payload any) error {
	p, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	b, err := json.Marshal(panel.UIEnvelope{ID: "a" + strconv.FormatInt(s.msgSeq.Add(1), 10), Type: typ,
		TS: s.env.Clock.Now().UTC().Format("2006-01-02T15:04:05.000Z"), Payload: p})
	if err != nil {
		return err
	}
	wctx, cancel := clock.WithTimeout(ctx, s.env.Clock, 10*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, b)
}

// onFrame writes the output of the terminal, in order and without repeating a byte.
func (s *session) onFrame(b []byte) {
	f, err := protocol.DecodeData(b)
	if err != nil || f.Type != protocol.DataTerminal || f.ID != s.frame {
		return
	}
	s.mu.Lock()
	data := f.Data
	if f.Pos < s.next { // bytes already drawn (a resync overlaps)
		skip := s.next - f.Pos
		if skip >= uint64(len(data)) {
			s.mu.Unlock()
			return
		}
		data = data[skip:]
	}
	s.next = max(s.next, f.Pos) + uint64(len(data))
	s.mu.Unlock()
	s.scr.Write(data)
	s.touch()
}

func (s *session) onMessage(ctx context.Context, data []byte, lostSince *time.Time) error {
	var env panel.UIEnvelope
	if json.Unmarshal(data, &env) != nil {
		return nil
	}
	switch env.Type {
	case panel.TypeUISubscribed:
		var sub panel.UISubscribed
		if json.Unmarshal(env.Payload, &sub) == nil && len(sub.Rejected) > 0 {
			return s.rejected(sub.Rejected[0])
		}
	case panel.TypeUIEvent:
		var ev panel.UIEvent
		if json.Unmarshal(env.Payload, &ev) != nil {
			return nil
		}
		s.onEvent(ctx, ev, lostSince)
	case panel.TypeUIError:
		var e panel.APIError
		if json.Unmarshal(env.Payload, &e) == nil {
			s.onRefusal(e)
		}
	}
	return nil
}

// rejected: the panel did not give the topic.
func (s *session) rejected(r panel.UIRejected) error {
	switch r.Code {
	case panel.CodeTooManyStreams:
		return fail(codeTooMany, "close a terminal you have open, in the panel or in another attach", "too many terminals open for this user")
	case panel.CodeForbiddenLevel:
		return fail(codeNotVisible, "check the name on the canvas, and the workspace", "there is no character %q that you see", s.opts.character)
	}
	return fail(codeUnavailable, "try again in a moment", "the panel did not open the terminal (%s)", r.Code)
}

func (s *session) onEvent(ctx context.Context, ev panel.UIEvent, lostSince *time.Time) {
	switch ev.Event {
	case panel.EventSnapshot:
		*lostSince = time.Time{} // the stream is up
		var info panel.TerminalInfo
		if json.Unmarshal(ev.Data, &info) == nil && info.TerminalID == s.info.TerminalID {
			s.setStatus("")
		}
	case panel.EventReplayed:
		var r panel.TerminalReplayed
		if json.Unmarshal(ev.Data, &r) == nil && r.Lost {
			s.setNotice("part of the output was lost (it left the buffer of the machine)")
		}
	case panel.EventScreen:
		// a slow connection got the picture of the screen instead of the output it lost
		var sc panel.TerminalScreenEvent
		if json.Unmarshal(ev.Data, &sc) == nil {
			s.scr.Reset()
			s.scr.Write([]byte("\x1b[H\x1b[2J" + crlf(sc.Text)))
			s.mu.Lock()
			s.next = sc.Offset
			s.mu.Unlock()
			s.touch()
		}
	case panel.EventSignal:
		var sg panel.TerminalSignal
		if json.Unmarshal(ev.Data, &sg) != nil {
			return
		}
		s.signal.Store(sg.Signal)
		if sg.Signal {
			s.setStatus("")
		} else {
			s.setStatus("no signal from the machine")
		}
	}
}

// onRefusal shows why the panel refused a key. The agent got none of them.
func (s *session) onRefusal(e panel.APIError) {
	switch e.Code {
	case panel.CodePermissionPrompt:
		s.setNotice("the agent waits on a permission prompt: keys are refused, decide it by the approval")
	case panel.CodeForbiddenLevel:
		s.mu.Lock()
		s.write = false
		s.mu.Unlock()
		s.setNotice("typing refused: your level does not allow it (read-only now)")
	case panel.CodeUnavailable:
		s.setNotice("typing refused: the machine of the terminal has no signal")
	default:
		s.setNotice("typing refused (" + string(e.Code) + ")")
	}
}

// endedEvery is how often the panel is asked whether the terminal still exists.
const endedEvery = 2 * time.Second

// watchEnded asks the panel from time to time whether the terminal is gone for
// good: the stream of a terminal that ends just stops (the machine is still
// there), and a machine that went away is no terminal that ended. It also learns
// of a session that was ended while nothing was said on the socket.
func (s *session) watchEnded(ctx context.Context) {
	for {
		if clock.Sleep(ctx, s.env.Clock, endedEvery) != nil {
			return
		}
		_, found, err := s.c.info(ctx, s.info.Character)
		var f failure
		switch {
		case errors.As(err, &f) && f.code == codeSessionEnded:
			s.cancel(f)
			return
		case err == nil && !found:
			s.cancel(errEnded)
			return
		}
	}
}

func crlf(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, '\r')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func (s *session) setStatus(v string) {
	s.mu.Lock()
	s.status = v
	s.mu.Unlock()
	s.touch()
}

func (s *session) setNotice(v string) {
	s.mu.Lock()
	s.notice, s.noticeAt = v, s.env.Clock.Now()
	s.mu.Unlock()
	s.touch()
}

func (s *session) touch() {
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

// closeConn closes the socket; a person who typed ends the typing session first
// (the Citadel ends it anyway when the socket closes, and audits it).
func (s *session) closeConn(graceful bool) {
	s.mu.Lock()
	conn, typed := s.conn, s.typed
	s.conn = nil
	s.mu.Unlock()
	if conn == nil {
		return
	}
	if graceful && typed {
		ctx, cancel := clock.WithTimeout(context.Background(), s.env.Clock, 2*time.Second)
		_ = s.send(ctx, conn, panel.TypeTerminalTypingEnd, panel.TerminalTypingEndMessage{TerminalID: s.info.TerminalID})
		cancel()
	}
	_ = conn.Close(websocket.StatusNormalClosure, "detached")
}

// draw puts the picture on the console when something changed, or the window
// did, at most every redrawEvery.
func (s *session) draw(ctx context.Context) {
	tk := s.env.Clock.NewTicker(redrawEvery)
	defer tk.Stop()
	var last string
	var lastW, lastH int
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C():
		}
		w, h := s.env.Console.Size()
		select {
		case <-s.dirty:
		default:
			if w == lastW && h == lastH {
				continue
			}
		}
		s.mu.Lock()
		status, notice, at, write := s.status, s.notice, s.noticeAt, s.write
		s.mu.Unlock()
		line := s.modeText(write)
		if notice != "" && s.env.Clock.Now().Sub(at) < noticeFor {
			line = notice + " · " + line
		}
		if status != "" {
			line = status + " · " + line
		}
		out := s.scr.Frame(w, h, line, write)
		if w != lastW || h != lastH {
			out = "\x1b[2J" + out
		}
		if out != last || w != lastW || h != lastH {
			_, _ = io.WriteString(s.env.Console.Output(), out)
			last = out
		}
		lastW, lastH = w, h
	}
}

func (s *session) modeText(write bool) string {
	m := "read-only"
	if write {
		m = "typing"
	}
	return fmt.Sprintf("%s · %s · %s detaches", s.info.Name, m, s.opts.detachName)
}

// readKeys reads the keys of the person. The detach key ends the attach; every
// other key goes to the agent only with --write, and never any other way: a
// read-only attach drops them.
func (s *session) readKeys(ctx context.Context) {
	in := s.env.Console.Input()
	buf := make([]byte, 4096)
	var held []byte // the start of a character that the next read finishes
	for {
		n, err := in.Read(buf)
		if n > 0 {
			chunk := append(held, buf[:n]...)
			held = nil
			for i, b := range chunk {
				if b == s.opts.detach {
					s.sendKeys(ctx, chunk[:i])
					s.detaching.Store(true)
					s.closeConn(true)
					s.cancel(errDetached)
					return
				}
			}
			if k := incompleteTail(chunk); k > 0 {
				held = append([]byte(nil), chunk[len(chunk)-k:]...)
				chunk = chunk[:len(chunk)-k]
			}
			s.sendKeys(ctx, chunk)
		}
		if err != nil {
			if ctx.Err() == nil {
				s.cancel(errDetached) // the input is over: nobody can detach any more
			}
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// incompleteTail is how many bytes at the end of b start a UTF-8 character that
// is not whole yet.
func incompleteTail(b []byte) int {
	for k := 1; k <= utf8.UTFMax-1 && k <= len(b); k++ {
		if r := b[len(b)-k]; r&0xC0 != 0x80 { // a start byte
			if r >= 0x80 && !utf8.FullRune(b[len(b)-k:]) {
				return k
			}
			return 0
		}
	}
	return 0
}

// sendKeys types into the terminal, if this attach may. The panel checks the
// level at each message, so this is only the convenience of not asking.
func (s *session) sendKeys(ctx context.Context, keys []byte) {
	if len(keys) == 0 {
		return
	}
	s.mu.Lock()
	conn, write := s.conn, s.write
	s.mu.Unlock()
	if !write || conn == nil {
		return
	}
	if err := s.send(ctx, conn, panel.TypeTerminalInput, panel.TerminalInputMessage{TerminalID: s.info.TerminalID, Data: string(keys)}); err == nil {
		s.mu.Lock()
		s.typed = true
		s.mu.Unlock()
	}
}
