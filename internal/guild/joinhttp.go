package guild

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"
)

// maxJoinBody bounds a body of the entry: two CSRs and a sheet are far below.
const maxJoinBody = 64 << 10

// MaxJoinWait is the longest a GET /join/<id> may wait (the long poll).
const MaxJoinWait = 60 * time.Second

// Mounter is where the routes of the entry go: *transport.Server.
type Mounter interface {
	Handle(pattern string, h http.Handler)
}

// Mount puts the routes of the entry on the engine's HTTPS server, outside
// mTLS (a new machine has no certificate yet):
//
//	POST /join              the request of the new machine
//	POST /join/{id}/reveal  its nonce
//	GET  /join/{id}         the answer, long poll with ?wait=<seconds>
func (c *Citadel) Mount(m Mounter) {
	m.Handle("POST "+RouteJoin, http.HandlerFunc(c.serveBegin))
	m.Handle("POST "+routeJoinReveal, http.HandlerFunc(c.serveReveal))
	m.Handle("GET "+routeJoinStatus, http.HandlerFunc(c.serveStatus))
}

func (c *Citadel) serveBegin(w http.ResponseWriter, r *http.Request) {
	var req JoinRequest
	if !readJSON(w, r, &req) {
		return
	}
	t, err := c.Begin(r.Context(), req, remoteHost(r))
	if err != nil {
		c.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (c *Citadel) serveReveal(w http.ResponseWriter, r *http.Request) {
	var body RevealBody
	if !readJSON(w, r, &body) {
		return
	}
	if err := c.Reveal(r.Context(), r.PathValue("id"), body.NonceN); err != nil {
		c.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *Citadel) serveStatus(w http.ResponseWriter, r *http.Request) {
	var wait time.Duration
	if s := r.URL.Query().Get("wait"); s != "" {
		secs, err := strconv.Atoi(s)
		if err != nil || secs < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "wait is a number of seconds")
			return
		}
		wait = min(time.Duration(secs)*time.Second, MaxJoinWait)
	}
	st, err := c.Status(r.Context(), r.PathValue("id"), wait)
	if err != nil {
		c.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// fail answers with the status the error stands for. A suspended Citadel
// answers 503, like the rest of its public routes.
func (c *Citadel) fail(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "internal"
	switch {
	case errors.Is(err, ErrRateLimited), errors.Is(err, ErrTooManyPending):
		status, code = http.StatusTooManyRequests, "too_many_requests"
	case errors.Is(err, ErrSuspended):
		status, code = http.StatusServiceUnavailable, "suspended"
	case errors.Is(err, ErrJoinNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, ErrExpired):
		status, code = http.StatusGone, "expired"
	case errors.Is(err, ErrNotPending):
		status, code = http.StatusConflict, "decided"
	case errors.Is(err, ErrBadRequest), errors.Is(err, ErrBadReveal):
		status, code = http.StatusBadRequest, "bad_request"
	case errors.Is(err, context.Canceled):
		status, code = 499, "canceled"
	}
	if status == http.StatusInternalServerError {
		c.cfg.Logger.Error("guild: join route", "err", err)
		writeError(w, status, code, "internal error")
		return
	}
	writeError(w, status, code, err.Error())
}

func remoteHost(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

func readJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJoinBody))
	if err := dec.Decode(into); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is not the JSON of this route")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorBody{Error: code, Message: msg})
}
