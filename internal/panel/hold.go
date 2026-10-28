package panel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
)

// The gate of the handover (docs/guilda/cidadela-e-torres.md#troca-planejada): every write of the
// panel into the Guild database goes through it, so none lands after the last lsn the planned
// switch reads. A request that changes something holds it from the moment it is authenticated
// and its body is read (a slow or unauthenticated client never holds it) until it ends; a
// write made while handling a read or the socket (the touch of a session, an audit line) holds
// it for the length of that write; once the Citadel drains, all of them are refused with
// citadel_in_failover.

// HoldFunc admits one change of the Guild: it returns the function that ends it, or false
// while the Citadel drains.
type HoldFunc func() (release func(), ok bool)

// ErrInFailover is a write refused because the Citadel hands over the command.
var ErrInFailover = errors.New("panel: the Citadel is handing over the command")

// errInFailover is the answer of the API to a request refused by the gate.
func errInFailover() *APIError {
	return Err(CodeCitadelInFailover, "api.citadel_in_failover", nil)
}

type heldKey struct{}

// admit holds the gate for a write. A context that already holds it (the request that changes
// something holds it for its whole length) passes without entering again: a second entry
// would be refused by a gate that began to drain meanwhile. Without a gate every write is
// admitted.
func (p *Panel) admit(ctx context.Context) (release func(), err error) {
	if ctx.Value(heldKey{}) != nil || p.hold == nil {
		return func() {}, nil
	}
	rel, ok := p.hold()
	if !ok {
		return nil, ErrInFailover
	}
	return rel, nil
}

// holdRequest holds the gate for a request that changes something and marks its context as
// holding it. It answers the refusal itself and returns false.
func (p *Panel) holdRequest(w http.ResponseWriter, r *http.Request) (*http.Request, func(), bool) {
	if r.Context().Value(heldKey{}) != nil {
		return r, func() {}, true
	}
	release, err := p.admit(r.Context())
	if err != nil {
		WriteError(w, errInFailover())
		return r, nil, false
	}
	return r.WithContext(context.WithValue(r.Context(), heldKey{}, true)), release, true
}

// bufferBody reads the whole body of the request, so that the gate is held only after a slow
// client is done. A body over the limit or broken is answered as a bad request, as decode does.
func bufferBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, errBadRequest())
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return true
}

// holdSignedIn holds the gate for the length of a page that changes the Guild even on a GET (the
// return of the manifest and of the installation make the App): once the user is signed in and
// the body is read. A request with no session passes untouched; the page refuses it.
func (p *Panel) holdSignedIn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := p.whoIs(r); !ok || !bufferBody(w, r) {
			if !ok {
				next.ServeHTTP(w, r)
			}
			return
		}
		r, release, held := p.holdRequest(w, r)
		if !held {
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}
