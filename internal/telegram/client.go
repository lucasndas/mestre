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
	"strings"
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
		hc = http.DefaultClient
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
		UpdateID      int64          `json:"update_id"`
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
