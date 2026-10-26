package hook

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The hook queue (docs/provedores/despachante-de-hooks.md#modo-degradado):
// with the engine down, the dispatcher leaves each event in a file of its
// own; the engine reads them when it is back. The dispatcher only creates
// files (it cannot list nor read the folder), so the limit of the queue is
// applied by the engine when it reads: of more than MaxQueued files, the
// oldest informative ones are dropped, never a decision already taken.
const (
	// MaxQueued is the most files the engine keeps of the queue.
	MaxQueued = 10_000
	// MaxRecord is the largest queue file; the dispatcher drops the raw
	// input and cuts the texts of a bigger event, and the engine refuses a
	// bigger file.
	MaxRecord = 64 << 10
	// QueueSettle is how old a file must be before the engine reads it: the
	// dispatcher writes it in place.
	QueueSettle = 5 * time.Second
	// QueueVersion is the version of a queue file.
	QueueVersion = 1
)

// Kinds of queued event; the kind is also the last part of the file name,
// so the engine can apply the limit without reading the files.
const (
	QueuedDecision = "d"
	QueuedInfo     = "i"
)

// QueueRecord is one file of the queue.
type QueueRecord struct {
	V     int             `json:"v"`
	Kind  string          `json:"kind"`
	Event providers.Event `json:"event"`
	// Decision is the degraded decision the dispatcher took.
	Decision *QueuedVerdict `json:"decision,omitempty"`
	// Truncated is set when the event was cut to fit MaxRecord.
	Truncated bool `json:"truncated,omitempty"`
}

// QueuedVerdict is the degraded decision of a queued event.
type QueuedVerdict struct {
	Verdict classes.Verdict `json:"verdict"`
	Reason  string          `json:"reason,omitempty"`
	Layer   classes.Layer   `json:"layer,omitempty"`
}

var queueName = regexp.MustCompile(`^[0-9]{20}-[0-9a-f]{16}-([di])\.json$`)

// enqueue writes one event to the queue. It never fails the call: a queue
// that cannot be written loses the event, not the answer.
func enqueue(files Files, dir string, clk clock.Clock, ev providers.Event, d *classes.Decision) error {
	if dir == "" {
		return errors.New("no hook queue")
	}
	ev.Raw = nil // the recorded payload is for the conformance, not the queue
	rec := QueueRecord{V: QueueVersion, Kind: QueuedInfo, Event: ev}
	if d != nil {
		rec.Kind = QueuedDecision
		rec.Decision = &QueuedVerdict{Verdict: d.Verdict, Reason: d.Reason, Layer: d.Layer}
	}
	data, err := fitRecord(rec)
	if err != nil {
		return err
	}
	var rnd [8]byte
	if _, err := io.ReadFull(rand.Reader, rnd[:]); err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s-%s.json", clk.Now().UnixNano(), hex.EncodeToString(rnd[:]), rec.Kind)
	return files.CreateNew(filepath.Join(dir, name), data)
}

// fitRecord encodes rec within MaxRecord: first without the raw input of
// the tool, then with the texts cut.
func fitRecord(rec QueueRecord) ([]byte, error) {
	data, err := json.Marshal(rec)
	if err != nil || len(data) <= MaxRecord {
		return data, err
	}
	rec.Truncated = true
	ev := rec.Event
	if ev.Tool != nil {
		t := *ev.Tool
		t.RawInput = nil
		t.Command = cut(t.Command, 8<<10)
		if len(t.Paths) > 64 {
			t.Paths = t.Paths[:64]
		}
		ev.Tool = &t
	}
	if ev.ToolResult != nil {
		r := *ev.ToolResult
		r.Output, r.Error = cut(r.Output, 1<<10), cut(r.Error, 1<<10)
		ev.ToolResult = &r
	}
	ev.Prompt, ev.LastMessage, ev.Error = cut(ev.Prompt, 1<<10), cut(ev.LastMessage, 1<<10), cut(ev.Error, 1<<10)
	rec.Event = ev
	if rec.Decision != nil {
		v := *rec.Decision
		v.Reason = cut(v.Reason, 4<<10)
		rec.Decision = &v
	}
	data, err = json.Marshal(rec)
	if err == nil && len(data) > MaxRecord {
		return nil, fmt.Errorf("queue record of %d bytes", len(data))
	}
	return data, err
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 { // keep whole UTF-8 sequences
		n--
	}
	return s[:n]
}

// Drain reads the queue (engine side), oldest first, keeping at most max
// files (MaxQueued), and gives each valid record to handle, then removes its
// file. Files younger than QueueSettle
// stay for the next time. A file the dispatcher did not write (another
// name, not a regular file, too big, not a record) is removed and logged:
// every agent of the machine can create files there, so nothing in a record
// is the verified identity of a character, only what it claims.
func Drain(dir string, max int, clk clock.Clock, log *slog.Logger, handle func(QueueRecord)) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("hook queue: %w", err)
	}
	now := clk.Now()
	queued := make([]queuedFile, 0, len(entries))
	for _, e := range entries {
		if !queueName.MatchString(e.Name()) || !e.Type().IsRegular() {
			log.Warn("hook queue: a file the dispatcher did not write", "file", e.Name(), "type", e.Type().String())
			removeEntry(dir, e.Name(), log)
			continue
		}
		queued = append(queued, queuedFile{name: e.Name(), at: arrival(e, now)})
	}
	// Oldest first by when the file reached the queue, as the system of the
	// engine saw it, never by the time in the name: any agent of the machine
	// can create a file there with the name it likes, and one stamped in the
	// future would otherwise sort last (and so outlive the real decisions)
	// when the limit drops the oldest. The name only breaks a tie.
	sort.Slice(queued, func(i, j int) bool {
		if !queued[i].at.Equal(queued[j].at) {
			return queued[i].at.Before(queued[j].at)
		}
		return queued[i].name < queued[j].name
	})
	names := make([]string, len(queued))
	for i, q := range queued {
		names[i] = q.name
	}
	names = dropOldestInfo(dir, names, max, log)
	n := 0
	for _, name := range names {
		path := filepath.Join(dir, name)
		rec, young, err := readRecord(path, now)
		switch {
		case young:
			continue
		case err != nil:
			log.Warn("hook queue: an invalid file was removed", "file", name, "err", err)
		default:
			handle(rec)
			n++
		}
		// One entry that cannot go never stops the others.
		removeEntry(dir, name, log)
	}
	return n, nil
}

// queuedFile is a file of the queue and when it arrived.
type queuedFile struct {
	name string
	at   time.Time
}

// arrival is when the entry reached the queue (arrivalOf), never later than
// now: a time in the future is the read's own.
func arrival(e fs.DirEntry, now time.Time) time.Time {
	info, err := e.Info()
	if err != nil {
		return now
	}
	if at := arrivalOf(info); at.Before(now) {
		return at
	}
	return now
}

// removeEntry removes an entry of the queue without following it: a link
// goes as the link, a folder an agent made goes with what it holds (the
// removal does not follow the links inside it). A failure is logged.
func removeEntry(dir, name string, log *slog.Logger) {
	path := filepath.Join(dir, name)
	st, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return
	case err == nil && st.IsDir():
		err = os.RemoveAll(path)
	default:
		err = os.Remove(path)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Warn("hook queue: an entry could not be removed", "file", name, "err", err)
	}
}

// dropOldestInfo applies the limit: the oldest informative files go first;
// decisions stay while they fit, and only a queue of more decisions than the
// limit loses its oldest decisions too (logged, with the count).
func dropOldestInfo(dir string, names []string, max int, log *slog.Logger) []string {
	over := len(names) - max
	if over <= 0 {
		return names
	}
	kept := names[:0:0]
	dropped := 0
	for _, name := range names {
		if dropped < over && queueName.FindStringSubmatch(name)[1] == QueuedInfo {
			removeEntry(dir, name, log)
			dropped++
			continue
		}
		kept = append(kept, name)
	}
	log.Warn("hook queue: over the limit, the oldest informative events were dropped", "dropped", dropped, "limit", max)
	if extra := len(kept) - max; extra > 0 {
		for _, name := range kept[:extra] {
			removeEntry(dir, name, log)
		}
		kept = kept[extra:]
		log.Warn("hook queue: more decisions than the limit, the oldest decisions were dropped", "dropped", extra, "limit", max)
	}
	return kept
}

func readRecord(path string, now time.Time) (rec QueueRecord, young bool, err error) {
	f, err := openQueued(path)
	if err != nil {
		return rec, false, err
	}
	defer f.Close()
	st, err := f.Stat()
	var age time.Duration
	if err == nil {
		age = now.Sub(st.ModTime())
	}
	switch {
	case err != nil:
		return rec, false, err
	case !st.Mode().IsRegular():
		return rec, false, errors.New("not a regular file")
	case age >= 0 && age < QueueSettle:
		return rec, true, nil // a time in the future does not keep a file for ever
	case st.Size() > MaxRecord:
		return rec, false, fmt.Errorf("%d bytes", st.Size())
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxRecord+1))
	if err != nil {
		return rec, false, err
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return rec, false, err
	}
	ev := rec.Event
	switch {
	case rec.V != QueueVersion:
		return rec, false, fmt.Errorf("version %d", rec.V)
	case rec.Kind != QueuedDecision && rec.Kind != QueuedInfo:
		return rec, false, fmt.Errorf("kind %q", rec.Kind)
	case rec.Kind == QueuedDecision && (rec.Decision == nil || !ev.Event.ToolEvent()):
		return rec, false, errors.New("a decision without its verdict or its tool event")
	case ev.Version != providers.EventVersion || !classes.ValidAgentID(ev.AgentID):
		return rec, false, errors.New("not an event of a character")
	}
	if want := queueName.FindStringSubmatch(filepath.Base(path))[1]; want != rec.Kind {
		return rec, false, errors.New("the kind of the name and of the record differ")
	}
	return rec, false, nil
}
