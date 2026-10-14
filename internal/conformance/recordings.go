package conformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

var (
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	greetRe = regexp.MustCompile(`(?i)(welcome back|olá|hello|hi),? [^!\n│|]+`)
)

// Recorder keeps the hook payloads and the screens of the runs as
// recordings for review (docs/qualidade/dubles.md#gravações): in
// <Dir>/<provider>/<version>/, one file per native event (and one per tool
// of the tool events), the start screen as the raw bytes of the terminal and
// the screen of the end of each step, all masked. Reviewed, they become the
// recordings of the fake agent.
type Recorder struct {
	Dir string
	// Clock dates the recordings; nil is the real clock.
	Clock clock.Clock

	mu   sync.Mutex
	seen map[string]bool
	errs []error
}

// Err is what the recorder could not write.
func (r *Recorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.errs) == 0 {
		return nil
	}
	return fmt.Errorf("recordings: %v", r.errs)
}

func (r *Recorder) now() time.Time {
	if r.Clock == nil {
		return clock.Real{}.Now()
	}
	return r.Clock.Now()
}

// recording is the format of internal/testkit (testkit.Recording).
type recording struct {
	Origin     string `json:"origin"`
	Version    string `json:"version"`
	CapturedAt string `json:"captured_at"`
	Source     string `json:"source"`
	Body       any    `json:"body"`
}

func (r *Recorder) write(req Request, name, source string, body any) {
	dir := filepath.Join(r.Dir, string(req.Provider), versionDir(req.Version))
	b, err := json.MarshalIndent(recording{Origin: string(req.Provider), Version: req.Version,
		CapturedAt: r.now().UTC().Format("2006-01-02"), Source: source, Body: body}, "", "  ")
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, name+".json"), append(b, '\n'), 0o644)
	}
	if err != nil {
		r.mu.Lock()
		r.errs = append(r.errs, err)
		r.mu.Unlock()
	}
}

func versionDir(v string) string {
	if v == "" {
		return "unknown"
	}
	return strings.NewReplacer("/", "_", `\`, "_", " ", "_").Replace(v)
}

// first reports whether name is new for the provider of req.
func (r *Recorder) first(req Request, name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.seen == nil {
		r.seen = map[string]bool{}
	}
	key := string(req.Provider) + "/" + name
	if r.seen[key] {
		return false
	}
	r.seen[key] = true
	return true
}

// Hook keeps the first payload of each native event, and of each tool of
// the tool events.
func (r *Recorder) Hook(req Request, native, tool string, raw json.RawMessage, paths map[string]string) {
	var body any
	if err := json.Unmarshal([]byte(maskText(string(raw), paths)), &body); err != nil {
		return
	}
	source := fmt.Sprintf("capture: %s %s %s stdin, masked, by the conformance test", req.Provider, req.Version, native)
	// The statusLine of Claude Code has the windows of the limits only after
	// the first answer: the last one of the run is kept.
	if r.first(req, native) || native == "StatusLine" {
		r.write(req, native, source, body)
	}
	if tool != "" {
		name := native + "-" + strings.Map(func(c rune) rune {
			if c == '/' || c == '\\' || c == ':' || c == ' ' {
				return '_'
			}
			return c
		}, tool)
		if r.first(req, name) {
			r.write(req, name, source, body)
		}
	}
}

// ScreenBody is the body of a screen recording: the raw bytes the terminal
// printed (with the escape sequences) and the size of the terminal.
type ScreenBody struct {
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
	Raw  string `json:"raw"`
}

// StartScreen keeps the raw output of the start, up to the first ready
// screen: the recording CA-TERM-02 renders.
func (r *Recorder) StartScreen(req Request, raw []byte, paths map[string]string) {
	if len(raw) == 0 || !r.first(req, "screen-start") {
		return
	}
	r.write(req, "screen-start", fmt.Sprintf("capture: %s %s start screen, raw terminal output, masked, by the conformance test", req.Provider, req.Version),
		ScreenBody{Cols: ptywire.DefaultCols, Rows: ptywire.DefaultRows, Raw: maskText(string(raw), paths)})
}

// StepScreen keeps the screen of the end of a step (already masked).
func (r *Recorder) StepScreen(req Request, step int, text string) {
	name := fmt.Sprintf("screen-step%02d", step)
	if !r.first(req, name) {
		return
	}
	r.write(req, name, fmt.Sprintf("capture: %s %s screen at the end of step %d, masked", req.Provider, req.Version, step), text)
}

// DialogBody is the body of the recording of a native permission screen:
// the screen, the calls of the engine it was matched against and the
// answer.
type DialogBody struct {
	Screen string   `json:"screen"`
	Calls  []string `json:"calls"`
	Answer string   `json:"answer"`
}

// maxDialogs bounds the dialog recordings of a run.
const maxDialogs = 8

// Dialog keeps a native permission screen of the run, masked, with what it
// was matched against: the layout the matcher of each CLI must read.
func (r *Recorder) Dialog(req Request, screen string, calls []string, answer string, paths map[string]string) {
	for n := 1; n <= maxDialogs; n++ {
		name := fmt.Sprintf("screen-dialog%02d", n)
		if !r.first(req, name) {
			continue
		}
		masked := make([]string, len(calls))
		for i, c := range calls {
			masked[i] = maskText(c, paths)
		}
		r.write(req, name, fmt.Sprintf("capture: %s %s native permission screen, masked, by the conformance test", req.Provider, req.Version),
			DialogBody{Screen: maskText(screen, paths), Calls: masked, Answer: answer})
		return
	}
}

// Result keeps the result of the run (already masked).
func (r *Recorder) Result(res Result) {
	r.write(Request{Provider: res.Provider, Version: res.Version}, "result", "conformance result", res)
}
