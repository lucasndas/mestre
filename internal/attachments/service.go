package attachments

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/safefile"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// DefaultLimit is the biggest attachment: 20 MB, which is also the most the Bot
// API in the cloud lets a bot download.
const DefaultLimit int64 = 20 << 20

// Attempts is how many times a download is tried.
const Attempts = 3

// Kind is what the file is.
type Kind string

// The kinds.
const (
	KindDocument Kind = "document"
	KindPhoto    Kind = "photo"
	KindVoice    Kind = "voice"
)

// Ref is the file as the channel names it.
type Ref struct {
	ID       string
	Name     string
	MIME     string
	Size     int64
	Duration time.Duration
	Kind     Kind
}

// Origin is where the file came from.
type Origin struct {
	Channel    string // telegram or panel
	ChatID     int64
	MessageID  int64
	PanelState string // the session of the panel
}

// Request is a file to receive for a workspace.
type Request struct {
	Workspace string
	UserID    string
	Origin    Origin
	File      Ref
	Caption   string
}

// Fetcher downloads files of the channel (the Bot API: getFile, then the link).
type Fetcher interface {
	GetFile(ctx context.Context, id string) (path string, size int64, err error)
	// Download reads the file and fails if it is bigger than limit bytes.
	Download(ctx context.Context, path string, limit int64) ([]byte, error)
}

// Stored is an attachment kept.
type Stored struct {
	ID string
	// Path is relative to the main clone: .mestre/anexos/AAAA/MM/id-name.
	Path     string
	Name     string
	Original string
	MIME     string
	Declared string
	Mismatch bool
	Size     int64
	SHA256   string
	Caption  string
	Kind     Kind
	// Transcript is set for a voice message: the text, or why there is none.
	Transcript *Transcript
}

// Transcript is the transcription of a voice message.
type Transcript struct {
	Text     string
	Lang     string
	Duration time.Duration
	// Err is why there is no text: the attachment is still delivered.
	Err string
}

// Errors the user is told about.
var (
	// ErrDownload: the file could not be downloaded after every attempt.
	ErrDownload = errors.New("attachments: the file could not be downloaded")
	// ErrWorkspace: the workspace has no folder to keep the file in.
	ErrWorkspace = errors.New("attachments: the workspace has no folder for attachments")
)

// TooLargeError is a file above the limit; nothing was downloaded.
type TooLargeError struct{ Limit int64 }

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("o arquivo passa do limite de %s", HumanSize(e.Limit))
}

// Options are the pieces of the Service; Fetcher, DB, Clock and Root are required.
type Options struct {
	DB      *store.DB
	Clock   clock.Clock
	Fetcher Fetcher
	// Root is the main clone of the workspace; the files go under its
	// .mestre/anexos/.
	Root func(ctx context.Context, workspace string) (string, error)
	// Limit is the biggest file (default 20 MB).
	Limit int64
	// Backoff is the wait between the attempts of a download (default 1s, 2s…),
	// on the injected clock; Wait replaces the wait itself (a test records it).
	Backoff time.Duration
	Wait    func(ctx context.Context, d time.Duration) error
	// Transcriber turns a voice message into text (nil: every voice message has
	// no transcript, with the reason).
	Transcriber *Transcription
	// Mask hides secrets in the text of a caption or a transcript (default: the
	// masker of the logs).
	Mask   func(string) string
	Logger *slog.Logger
}

// Service receives the attachments.
type Service struct {
	o   Options
	clk clock.Clock
	ids *protocol.IDGen
	log *slog.Logger
}

// New returns the Service.
func New(o Options) (*Service, error) {
	if o.DB == nil || o.Clock == nil || o.Fetcher == nil || o.Root == nil {
		return nil, errors.New("attachments: the database, the clock, the fetcher and the root of the workspaces are required")
	}
	if o.Limit <= 0 {
		o.Limit = DefaultLimit
	}
	if o.Backoff <= 0 {
		o.Backoff = time.Second
	}
	if o.Mask == nil {
		o.Mask = logging.NewMasker().Mask
	}
	if o.Wait == nil {
		clk := o.Clock
		o.Wait = func(ctx context.Context, d time.Duration) error { return clock.Sleep(ctx, clk, d) }
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{o: o, clk: o.Clock, ids: protocol.NewIDGen(o.Clock), log: log}, nil
}

// Limit is the biggest file.
func (s *Service) Limit() int64 { return s.o.Limit }

// Receive keeps the file of a request: it checks the size before downloading,
// downloads it (three attempts), detects its type, keeps it, registers it and, for
// a voice message, transcribes it. A voice message that cannot be transcribed is
// still received: the Transcript says why.
func (s *Service) Receive(ctx context.Context, r Request) (Stored, error) {
	if r.File.Size > s.o.Limit {
		return Stored{}, &TooLargeError{Limit: s.o.Limit}
	}
	if root, err := s.o.Root(ctx, r.Workspace); err != nil || root == "" {
		return Stored{}, ErrWorkspace
	}
	data, err := s.Fetch(ctx, r.File)
	if err != nil {
		return Stored{}, err
	}
	return s.keep(ctx, r, data)
}

// Fetch downloads the file of a channel and nothing else: the size is checked before the
// download, with three attempts. It needs no workspace and changes nothing, so a channel does it
// before it takes the lock of the handover of the Citadel, and gives the bytes to ReceiveData.
func (s *Service) Fetch(ctx context.Context, f Ref) ([]byte, error) {
	if f.Size > s.o.Limit {
		return nil, &TooLargeError{Limit: s.o.Limit}
	}
	return s.fetch(ctx, f)
}

// ReceiveData keeps a file whose bytes the channel already has: the upload of the
// panel (docs/painel/conversa-e-aprovacoes.md#conversa-com-o-grão-mestre), which has no
// download to make. The limit is the same as the one of Receive, and the bytes are
// never trusted: the type is the one the content says, the name is made safe and the
// file is kept without permission of execution.
func (s *Service) ReceiveData(ctx context.Context, r Request, data []byte) (Stored, error) {
	if int64(len(data)) > s.o.Limit || r.File.Size > s.o.Limit {
		return Stored{}, &TooLargeError{Limit: s.o.Limit}
	}
	return s.keep(ctx, r, data)
}

// keep detects the type of the bytes, writes the file under the main clone of the
// workspace and registers it.
func (s *Service) keep(ctx context.Context, r Request, data []byte) (Stored, error) {
	root, err := s.o.Root(ctx, r.Workspace)
	if err != nil || root == "" {
		return Stored{}, ErrWorkspace
	}
	now := s.clk.Now().UTC()
	id := s.newID()
	detected := Detect(data)
	st := Stored{ID: id, Original: r.File.Name, Declared: r.File.MIME, MIME: detected, Mismatch: r.File.MIME != "" && !sameType(r.File.MIME, detected),
		Size: int64(len(data)), Caption: s.o.Mask(strings.TrimSpace(r.Caption)), Kind: r.File.Kind}
	sum := sha256.Sum256(data)
	st.SHA256 = hex.EncodeToString(sum[:])
	st.Name = id + "-" + SafeName(r.File.Name, r.File.Kind, id, detected)
	st.Path = path.Join(".mestre", "anexos", now.Format("2006"), now.Format("01"), st.Name)
	if err := safefile.Write(filepath.Join(root, filepath.FromSlash(st.Path)), data, safefile.Options{Mode: 0o664}); err != nil {
		return Stored{}, fmt.Errorf("attachments: the file was not kept: %w", err)
	}
	if st.Mismatch {
		s.log.Info("attachments: the type detected is not the declared one", "id", id, "declared", r.File.MIME, "detected", detected)
	}
	if r.File.Kind == KindVoice {
		st.Transcript = s.transcribe(ctx, data, r.File)
	}
	if err := s.register(ctx, r, st, now); err != nil {
		return Stored{}, err
	}
	return st, nil
}

// newID is a short id: the random part of a ULID, in lower case.
func (s *Service) newID() string {
	id := strings.ToLower(s.ids.New().String())
	if len(id) > 8 {
		id = id[len(id)-8:]
	}
	return id
}

// fetch downloads the file: getFile then the link, three attempts with a wait
// on the clock between them. Nothing is downloaded above the limit, whatever the
// channel said of the size.
func (s *Service) fetch(ctx context.Context, f Ref) ([]byte, error) {
	var last error
	for attempt := 1; attempt <= Attempts; attempt++ {
		p, size, err := s.o.Fetcher.GetFile(ctx, f.ID)
		if err == nil && size > s.o.Limit {
			return nil, &TooLargeError{Limit: s.o.Limit}
		}
		if err == nil {
			var data []byte
			if data, err = s.o.Fetcher.Download(ctx, p, s.o.Limit); err == nil {
				return data, nil
			}
		}
		var big *TooLargeError
		if errors.As(err, &big) {
			return nil, err
		}
		last = err
		s.log.Warn("attachments: a download failed", "attempt", attempt, "err", sanitizeErr(err))
		if attempt < Attempts {
			if err := s.o.Wait(ctx, time.Duration(attempt)*s.o.Backoff); err != nil {
				return nil, err
			}
		}
	}
	_ = last
	return nil, ErrDownload
}

// sanitizeErr keeps the log from carrying anything but the text of the error.
func sanitizeErr(err error) string {
	return strings.Join(strings.Fields(err.Error()), " ")
}

func (s *Service) transcribe(ctx context.Context, data []byte, f Ref) *Transcript {
	if s.o.Transcriber == nil {
		return &Transcript{Duration: f.Duration, Err: "o motor não tem como transcrever"}
	}
	if f.Duration > MaxAudio {
		return &Transcript{Duration: f.Duration, Err: fmt.Sprintf("áudio longo demais para transcrever (até %d min)", int(MaxAudio/time.Minute))}
	}
	t := s.o.Transcriber.Transcribe(ctx, data, f.Duration)
	t.Text = s.o.Mask(t.Text)
	return &t
}

func (s *Service) register(ctx context.Context, r Request, st Stored, now time.Time) error {
	mismatch := 0
	if st.Mismatch {
		mismatch = 1
	}
	var text, lang, terr string
	if st.Transcript != nil {
		text, lang, terr = st.Transcript.Text, st.Transcript.Lang, st.Transcript.Err
	}
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO attachments (id, workspace, user_id, channel, chat_id, message_id, panel_session, name_original, mime_declared, mime_detected, mime_mismatch,
			size, sha256, caption, path, transcript, transcript_lang, transcript_error, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			st.ID, r.Workspace, r.UserID, r.Origin.Channel, r.Origin.ChatID, r.Origin.MessageID, r.Origin.PanelState, st.Original, st.Declared, st.MIME, mismatch,
			st.Size, st.SHA256, st.Caption, st.Path, text, lang, terr, now.UnixMilli())
		return err
	})
}

// Row is an attachment as the database keeps it.
type Row struct {
	Stored
	Workspace string
	UserID    string
	ChatID    int64
	MessageID int64
	CreatedAt time.Time
}

// Get reads an attachment.
func (s *Service) Get(ctx context.Context, id string) (Row, error) {
	var r Row
	var mismatch int
	var at int64
	var text, lang, terr string
	err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT id, workspace, user_id, chat_id, message_id, name_original, mime_declared, mime_detected, mime_mismatch, size, sha256, caption, path,
		transcript, transcript_lang, transcript_error, created_at FROM attachments WHERE id = ?`, id).
		Scan(&r.ID, &r.Workspace, &r.UserID, &r.ChatID, &r.MessageID, &r.Original, &r.Declared, &r.MIME, &mismatch, &r.Size, &r.SHA256, &r.Caption, &r.Path, &text, &lang, &terr, &at)
	if err != nil {
		return Row{}, err
	}
	r.Mismatch, r.CreatedAt = mismatch == 1, time.UnixMilli(at).UTC()
	r.Name = path.Base(r.Path)
	if text != "" || terr != "" {
		r.Transcript = &Transcript{Text: text, Lang: lang, Err: terr}
	}
	return r, nil
}

// Detect is the type of a file read from its content (the first bytes), never
// from its name.
func Detect(data []byte) string {
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	t := http.DetectContentType(head)
	if i := strings.Index(t, ";"); i >= 0 {
		t = t[:i]
	}
	// The sniffing of the standard library does not know these: the markers are
	// enough for what a user sends from a phone.
	switch {
	case len(head) >= 4 && string(head[:4]) == "OggS":
		return "audio/ogg"
	case len(head) >= 4 && string(head[:4]) == "%PDF":
		return "application/pdf"
	case len(head) >= 4 && string(head[:4]) == "\x1aE\xdf\xa3":
		return "video/webm"
	}
	return t
}

// sameType says the declared type is the detected one, or a family the sniffing
// does not tell apart (text types, the container of the audio of a voice message).
func sameType(declared, detected string) bool {
	declared = strings.ToLower(strings.TrimSpace(declared))
	if i := strings.Index(declared, ";"); i >= 0 {
		declared = declared[:i]
	}
	switch {
	case declared == detected:
		return true
	case strings.HasPrefix(declared, "text/") && strings.HasPrefix(detected, "text/"):
		return true
	case declared == "application/octet-stream":
		return true // the sender did not say
	case (declared == "audio/opus" || declared == "audio/ogg" || declared == "application/ogg") && detected == "audio/ogg":
		return true
	case (declared == "application/json" || strings.HasSuffix(declared, "+json") || strings.HasSuffix(declared, "+xml") || declared == "text/csv") && strings.HasPrefix(detected, "text/"):
		return true
	}
	return false
}

// maxNameBytes is the most bytes of a name, with room for the id and the dash in front of it
// (255 on Linux and NTFS).
const maxNameBytes = 200

// SafeName is the name a file is kept under: no path separators, no control
// characters, at most 100 characters (the extension is kept); a photo without a
// name is foto-<id>.jpg and a voice message without one is audio-<id>.ogg.
func SafeName(name string, kind Kind, id, detected string) string {
	name = strings.NewReplacer("\\", "/").Replace(name)
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == utf8.RuneError, unicode.IsControl(r), r == '/', r == '\\', r == ':', r == '*', r == '?', r == '"', r == '<', r == '>', r == '|', r == 0:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.Trim(strings.TrimSpace(b.String()), ".")
	if name == "" {
		switch kind {
		case KindPhoto:
			return "foto-" + id + ".jpg"
		case KindVoice:
			return "audio-" + id + ".ogg"
		}
		return "arquivo-" + id + extOf(detected)
	}
	if len([]rune(name)) > 100 || len(name) > maxNameBytes {
		ext := path.Ext(name)
		if len([]rune(ext)) > 20 || len(ext) > 40 {
			ext = ""
		}
		stem := []rune(strings.TrimSuffix(name, ext))
		if len(stem) > 100-len([]rune(ext)) {
			stem = stem[:100-len([]rune(ext))]
		}
		// the bytes too: a name of 100 characters of a script of three bytes each
		// would pass the 255 bytes a file system allows
		for len(string(stem))+len(ext) > maxNameBytes && len(stem) > 0 {
			stem = stem[:len(stem)-1]
		}
		name = string(stem) + ext
	}
	return name
}

func extOf(mime string) string {
	switch mime {
	case "application/pdf":
		return ".pdf"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "audio/ogg":
		return ".ogg"
	}
	return ""
}

// HumanSize is a size as the user reads it ("1,2 MB").
func HumanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return strings.Replace(fmt.Sprintf("%.1f MB", float64(n)/(1<<20)), ".", ",", 1)
	case n >= 1<<10:
		return strings.Replace(fmt.Sprintf("%.1f KB", float64(n)/(1<<10)), ".", ",", 1)
	}
	return fmt.Sprintf("%d B", n)
}

// Line is the line `[anexo]` of the delivery to the Grão-Mestre.
func (s Stored) Line() string {
	return fmt.Sprintf("[anexo] %s · %s · %s", s.Path, s.MIME, HumanSize(s.Size))
}

// Lines is what goes to the Grão-Mestre after the line of origin: the caption
// (or, for a voice message, the transcript line) and one line per file.
func (s Stored) Lines() []string {
	var out []string
	if t := s.Transcript; t != nil {
		if t.Err != "" {
			out = append(out, "[áudio sem transcrição: "+oneLine(t.Err)+"]")
		} else {
			out = append(out, fmt.Sprintf("[áudio transcrito · %s · idioma %s] %s", clockOf(t.Duration), orAuto(t.Lang), t.Text))
		}
	}
	if s.Caption != "" {
		out = append([]string{s.Caption}, out...)
	}
	return append(out, s.Line())
}

func clockOf(d time.Duration) string {
	sec := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

func orAuto(l string) string {
	if l == "" {
		return "?"
	}
	return l
}

func oneLine(v string) string { return strings.Join(strings.Fields(v), " ") }

var _ = io.EOF

// Discard takes back a file that was kept for a message that was never delivered (the
// message failed, or was already in the inbox): the file and its row go. Only a file this
// service made, under the main clone of the workspace, is taken.
func (s *Service) Discard(ctx context.Context, workspace string, st Stored) error {
	root, err := s.o.Root(ctx, workspace)
	if err != nil || root == "" {
		return ErrWorkspace
	}
	if !strings.HasPrefix(st.Path, ".mestre/anexos/") || strings.Contains(st.Path, "..") {
		return fmt.Errorf("attachments: %q is not a kept file", st.Path)
	}
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(st.Path))); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM attachments WHERE id = ? AND workspace = ?`, st.ID, workspace)
		return err
	})
}
