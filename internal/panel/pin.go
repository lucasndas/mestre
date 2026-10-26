package panel

import (
	"errors"
	"net/http"

	"github.com/mestre-ia/mestre/internal/users"
)

// withPIN wraps the handler of a sensitive action. The PIN comes in the request of
// the action; it is checked with the rules of the PIN of the users (a request of
// PIN opened for this action alone, 3 errors cancel it, 5 in 15 minutes lock the
// PIN) and spent by it: it is worth no other action. The lock of the PIN also ends
// the session the wrong PINs came in. Nothing is audited with the PIN.
func (p *Panel) withPIN(action string, h http.HandlerFunc) http.HandlerFunc {
	return p.withPINRequest(action, func(w http.ResponseWriter, r *http.Request, req string) {
		u := UserOf(r)
		if err := p.users.Consume(r.Context(), req, u.ID, action); err != nil {
			p.log.Error("panel: spending the request of PIN", "err", err)
			WriteError(w, errInternal())
			return
		}
		p.audit(r.Context(), "panel.pin.ok", map[string]any{"user_id": u.ID, "action": action, "session": SessionOf(r).ID, "ip": clientIP(r)})
		h(w, r)
	})
}

// withPINRequest is withPIN up to the answer being right: h gets the id of the
// request of PIN, which is still open, for a service that spends it itself in the
// transaction of the action (the transfer of the post of Owner).
func (p *Panel) withPINRequest(action string, h func(w http.ResponseWriter, r *http.Request, request string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		u, ss := UserOf(r), SessionOf(r)
		detail := map[string]any{"user_id": u.ID, "action": action, "session": ss.ID, "ip": clientIP(r)}
		pin := r.Header.Get(PINHeader)
		if pin == "" {
			p.audit(ctx, "panel.pin.required", detail)
			WriteError(w, Err(CodePINRequired, "PIN obrigatório", nil))
			return
		}
		if !users.ValidPIN(pin) {
			p.audit(ctx, "panel.pin.failed", detail)
			WriteError(w, Err(CodePINInvalid, "PIN incorreto", nil))
			return
		}
		req, err := p.users.StartPIN(ctx, u.ID, action)
		switch {
		case errors.Is(err, users.ErrNoPIN):
			p.audit(ctx, "panel.pin.none", detail)
			WriteError(w, Err(CodeNoPIN, "defina um PIN antes de fazer esta ação", nil))
			return
		case errors.Is(err, users.ErrPINLocked):
			p.audit(ctx, "panel.pin.locked", detail)
			WriteError(w, Err(CodePINLocked, "PIN bloqueado por excesso de erros", map[string]any{"until": u.PINLockedUntil}))
			return
		case err != nil:
			p.log.Error("panel: opening the request of PIN", "err", err)
			WriteError(w, errInternal())
			return
		}
		err = p.users.CheckPIN(ctx, req.ID, pin)
		switch {
		case errors.Is(err, users.ErrBadPIN) && errors.Is(err, users.ErrPINLocked):
			p.audit(ctx, "panel.pin.locked", detail)
			_ = p.sessions.End(ctx, ss.ID) // the lock ends the session the errors came in
			WriteError(w, Err(CodePINLocked, "PIN bloqueado por excesso de erros", nil))
			return
		case errors.Is(err, users.ErrBadPIN):
			p.audit(ctx, "panel.pin.failed", detail)
			WriteError(w, Err(CodePINInvalid, "PIN incorreto", nil))
			return
		case errors.Is(err, users.ErrPINLocked):
			p.audit(ctx, "panel.pin.locked", detail)
			WriteError(w, Err(CodePINLocked, "PIN bloqueado por excesso de erros", nil))
			return
		case err != nil:
			p.log.Error("panel: checking the PIN", "err", err)
			WriteError(w, errInternal())
			return
		}
		h(w, r, req.ID)
	}
}
