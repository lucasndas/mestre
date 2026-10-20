package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/attachments"
)

// DefaultBaseURL is the Telegram Bot API.
const DefaultBaseURL = "https://api.telegram.org"

// Client calls the Bot API. The token is part of the URL, so it is taken out of
// every error.
type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
}

var defaultHTTP = &http.Client{Timeout: 2 * time.Minute}

// APIError is an answer of the Bot API with ok=false.
type APIError struct {
	Method      string
	Code        int
	Description string
	// RetryAfter is the seconds of a 429.
	RetryAfter int
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// Retryable says the call may be made again later: a 429, a 5xx.
func (e *APIError) Retryable() bool { return e.Code == http.StatusTooManyRequests || e.Code >= 500 }

// Conflict says the 409 of a second reader of the same bot.
func (e *APIError) Conflict() bool { return e.Code == http.StatusConflict }

func (c *Client) url(method string) string {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	return base + "/bot" + c.Token + "/" + method
}

func (c *Client) do(ctx context.Context, method string, req *http.Request) (json.RawMessage, error) {
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP // a hung call must not hold the loop past a change of the Gate
	}
	resp, err := hc.Do(req.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("telegram %s: %s", method, c.scrub(err.Error()))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("telegram %s: %s", method, c.scrub(err.Error()))
	}
	var out struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, &APIError{Method: method, Code: resp.StatusCode, Description: "unreadable answer"}
	}
	if !out.OK {
		return nil, &APIError{Method: method, Code: out.ErrorCode, Description: c.scrub(out.Description), RetryAfter: out.Parameters.RetryAfter}
	}
	return out.Result, nil
}

// scrub takes the token out of a text.
func (c *Client) scrub(s string) string {
	if c.Token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.Token, "***")
}

// Call makes a method call with JSON parameters.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	b, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.url(method), bytes.NewReader(b))
	if err != nil {
		return nil, errors.New("telegram " + method + ": " + c.scrub(err.Error()))
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(ctx, method, req)
}

// Upload makes a method call that sends a file (sendDocument).
func (c *Client) Upload(ctx context.Context, method string, fields map[string]string, fileField, fileName string, content []byte) (json.RawMessage, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return nil, err
		}
	}
	part, err := w.CreateFormFile(fileField, fileName)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(content); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.url(method), &buf)
	if err != nil {
		return nil, errors.New("telegram " + method + ": " + c.scrub(err.Error()))
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.do(ctx, method, req)
}

// The parts of an update the bot reads.
type (
	// Update is one update of getUpdates.
	Update struct {
		UpdateID int64 `json:"update_id"`
		// Redacted marks the copy the bot keeps of an answer it captured: its text is gone.
		Redacted      bool           `json:"redacted,omitempty"`
		Message       *Message       `json:"message,omitempty"`
		CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
	}
	// Message is a message; the kinds the bot does not read are kept raw only to tell the kind.
	Message struct {
		MessageID int64  `json:"message_id"`
		From      *User  `json:"from,omitempty"`
		Chat      Chat   `json:"chat"`
		Text      string `json:"text,omitempty"`
		Caption   string `json:"caption,omitempty"`
		// ReplyTo is the message this one answers.
		ReplyTo *Message `json:"reply_to_message,omitempty"`

		Photo     json.RawMessage `json:"photo,omitempty"`
		Document  json.RawMessage `json:"document,omitempty"`
		Voice     json.RawMessage `json:"voice,omitempty"`
		Audio     json.RawMessage `json:"audio,omitempty"`
		Sticker   json.RawMessage `json:"sticker,omitempty"`
		Video     json.RawMessage `json:"video,omitempty"`
		VideoNote json.RawMessage `json:"video_note,omitempty"`
		Animation json.RawMessage `json:"animation,omitempty"`
		Location  json.RawMessage `json:"location,omitempty"`
		Contact   json.RawMessage `json:"contact,omitempty"`
		Poll      json.RawMessage `json:"poll,omitempty"`
	}
	// User is a Telegram user.
	User struct {
		ID        int64  `json:"id"`
		IsBot     bool   `json:"is_bot"`
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	}
	// Chat is a chat.
	Chat struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	}
	// CallbackQuery is the click on a button.
	CallbackQuery struct {
		ID      string   `json:"id"`
		From    User     `json:"from"`
		Message *Message `json:"message,omitempty"`
		Data    string   `json:"data"`
	}
)

// Kind is what a message carries, as the bot sorts it.
type Kind string

// The kinds of message.
const (
	KindText     Kind = "text"
	KindVoice    Kind = "voice"
	KindPhoto    Kind = "photo"
	KindDocument Kind = "document"
	KindOther    Kind = "other"
)

// Kind sorts the message: text, voice and audio, photo and document are
// accepted; everything else (video, sticker, location, contact, poll…) is not.
func (m *Message) Kind() Kind {
	switch {
	case len(m.Sticker)+len(m.Video)+len(m.VideoNote)+len(m.Animation)+len(m.Location)+len(m.Contact)+len(m.Poll) > 0:
		return KindOther
	case len(m.Voice) > 0 || len(m.Audio) > 0:
		return KindVoice
	case len(m.Photo) > 0:
		return KindPhoto
	case len(m.Document) > 0:
		return KindDocument
	case m.Text != "":
		return KindText
	}
	return KindOther
}

// GetFile asks the Bot API where a file is (getFile): the path to download it
// from and the size the Bot API knows.
func (c *Client) GetFile(ctx context.Context, id string) (string, int64, error) {
	raw, err := c.Call(ctx, "getFile", map[string]any{"file_id": id})
	if err != nil {
		return "", 0, err
	}
	var f struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := json.Unmarshal(raw, &f); err != nil || f.FilePath == "" {
		return "", 0, errors.New("telegram getFile: unreadable answer")
	}
	return f.FilePath, f.FileSize, nil
}

// Download reads a file from the link of the Bot API; one bigger than limit is
// cut and refused.
func (c *Client) Download(ctx context.Context, filePath string, limit int64) ([]byte, error) {
	if filePath == "" || strings.HasPrefix(filePath, "/") || strings.Contains(filePath, "..") || strings.ContainsAny(filePath, "?#\\") {
		return nil, errors.New("telegram download: the path of the file is not valid")
	}
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	segs := strings.Split(filePath, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/file/bot"+c.Token+"/"+strings.Join(segs, "/"), nil)
	if err != nil {
		return nil, errors.New("telegram download: " + c.scrub(err.Error()))
	}
	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, errors.New("telegram download: " + c.scrub(err.Error()))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram download: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("telegram download: " + c.scrub(err.Error()))
	}
	if int64(len(data)) > limit {
		return nil, &attachments.TooLargeError{Limit: limit}
	}
	return data, nil
}

var _ attachments.Fetcher = (*Client)(nil)

// FileOf is the file a message carries: a voice message or an audio, a photo (the
// biggest size) or a document. It is nil for a message with no file.
func (m *Message) FileOf() *attachments.Ref {
	type media struct {
		FileID   string `json:"file_id"`
		Size     int64  `json:"file_size"`
		Name     string `json:"file_name"`
		MIME     string `json:"mime_type"`
		Duration int    `json:"duration"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
	}
	one := func(raw json.RawMessage) (media, bool) {
		var x media
		return x, len(raw) > 0 && json.Unmarshal(raw, &x) == nil && x.FileID != ""
	}
	if x, ok := one(m.Voice); ok {
		return &attachments.Ref{ID: x.FileID, Name: x.Name, MIME: x.MIME, Size: x.Size, Duration: time.Duration(x.Duration) * time.Second, Kind: attachments.KindVoice}
	}
	if x, ok := one(m.Audio); ok {
		return &attachments.Ref{ID: x.FileID, Name: x.Name, MIME: x.MIME, Size: x.Size, Duration: time.Duration(x.Duration) * time.Second, Kind: attachments.KindVoice}
	}
	if x, ok := one(m.Document); ok {
		return &attachments.Ref{ID: x.FileID, Name: x.Name, MIME: x.MIME, Size: x.Size, Kind: attachments.KindDocument}
	}
	var sizes []media
	if len(m.Photo) > 0 && json.Unmarshal(m.Photo, &sizes) == nil && len(sizes) > 0 {
		best := sizes[0]
		for _, x := range sizes {
			if x.Width*x.Height > best.Width*best.Height || (x.Width*x.Height == best.Width*best.Height && x.Size > best.Size) {
				best = x
			}
		}
		if best.FileID != "" {
			return &attachments.Ref{ID: best.FileID, MIME: "image/jpeg", Size: best.Size, Kind: attachments.KindPhoto}
		}
	}
	return nil
}
