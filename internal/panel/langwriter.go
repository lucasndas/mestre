package panel

import (
	"bufio"
	"errors"
	"net"
	"net/http"

	"github.com/mestre-ia/mestre/internal/i18n"
)

// langWriter is the ResponseWriter of a request of the API that knows the language an
// error is written in: the language the user of the session chose, else the one of the
// Accept-Language of the request, else pt-BR (docs/plataforma/mensagens-por-codigo.md).
type langWriter struct {
	http.ResponseWriter
	accept string // the Accept-Language header
	user   string // the language the user chose, once the session is known
}

func (w *langWriter) lang() string {
	if w.user != "" {
		return i18n.Match(w.user)
	}
	if w.accept != "" {
		return i18n.FromAcceptLanguage(w.accept)
	}
	return i18n.PtBR
}

// Unwrap lets http.ResponseController reach the real writer (flush, deadlines).
func (w *langWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush keeps the streams (server-sent events) working through the wrapper.
func (w *langWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack keeps the opening of the socket working through the wrapper.
func (w *langWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("panel: the writer cannot be hijacked")
}
