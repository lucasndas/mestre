package fakenet

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
)

// HandlerTransport is an in-memory http.RoundTripper that serves every
// request with h, for the Telegram, GitHub and whisper-server clients. A
// handler that panics with http.ErrAbortHandler simulates a cut connection.
func HandlerTransport(h http.Handler) http.RoundTripper { return handlerTransport{h} }

type handlerTransport struct{ h http.Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				if r == http.ErrAbortHandler {
					done <- fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, ErrConnectionReset)
					return
				}
				done <- fmt.Errorf("%s %s: handler panic: %v", req.Method, req.URL.Path, r)
			}
		}()
		t.h.ServeHTTP(rec, req)
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
		resp := rec.Result()
		resp.Request = req
		return resp, nil
	case <-req.Context().Done():
		return nil, errors.Join(ErrConnectionReset, req.Context().Err())
	}
}
