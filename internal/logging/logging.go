package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// Options configures New.
type Options struct {
	Level slog.Leveler
	// Clock stamps each record; nil means clock.Real.
	Clock clock.Clock
}

// sensitiveKeys have their value masked entirely, whatever it looks like.
var sensitiveKeys = map[string]bool{
	"password": true, "token": true, "secret": true, "authorization": true,
}

// New returns a logger that writes JSON lines to w and masks the message and
// every attribute with m before writing.
func New(w io.Writer, m *Masker, opts Options) *slog.Logger {
	c := opts.Clock
	if c == nil {
		c = clock.Real{}
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: opts.Level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			return maskAttr(m, a)
		},
	})
	return slog.New(clockHandler{Handler: h, clock: c})
}

func maskAttr(m *Masker, a slog.Attr) slog.Attr {
	if a.Key == slog.TimeKey || a.Key == slog.LevelKey {
		return a
	}
	v := a.Value.Resolve()
	if sensitiveKeys[strings.ToLower(a.Key)] && v.Kind() != slog.KindGroup {
		return slog.String(a.Key, Redacted)
	}
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, m.Mask(v.String()))
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			return slog.String(a.Key, m.Mask(x.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, m.Mask(x.String()))
		case []byte:
			return slog.String(a.Key, m.Mask(string(x)))
		default:
			return slog.String(a.Key, m.Mask(fmt.Sprintf("%+v", x)))
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}

// clockHandler stamps records with the injected clock.
type clockHandler struct {
	slog.Handler
	clock clock.Clock
}

func (h clockHandler) Handle(ctx context.Context, r slog.Record) error {
	r.Time = h.clock.Now()
	return h.Handler.Handle(ctx, r)
}

func (h clockHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return clockHandler{Handler: h.Handler.WithAttrs(as), clock: h.clock}
}

func (h clockHandler) WithGroup(name string) slog.Handler {
	return clockHandler{Handler: h.Handler.WithGroup(name), clock: h.clock}
}
