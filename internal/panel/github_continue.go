package panel

import (
	"html/template"
	"net/http"
	"strconv"
)

// The session cookie is SameSite=Strict, so a browser does not send it on a navigation that
// comes from another site: the redirect of GitHub to the return of the manifest and to the
// return of the installation, and a link opened from the web page of Telegram. The Owner would
// be told to sign in with a session that exists. continueCrossSite answers such a request, when it
// has no cookie, with a page that holds nothing and goes on to the same address by itself: that
// second navigation starts in the panel, is same-site, and carries the cookie. The code is never
// exchanged by the first request: only the pages behind this one do it, with the session.

var continueTmpl = template.Must(template.New("continue").Parse(`<!doctype html>
<html lang="pt-BR"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="0;url={{.URL}}">
<title>Continuando</title></head><body>
<p>Continuando no painel do Mestre…</p>
<p><a href="{{.URL}}">Continuar</a></p>
</body></html>`))

// looksRight says the request has the parameters its page needs; anything else is not
// continued (the page behind says what is wrong).
func looksRight(r *http.Request) bool {
	q := r.URL.Query()
	switch r.URL.Path {
	case "/github/manifest/callback":
		return q.Get("code") != "" && q.Get("state") != ""
	case "/github/apps/instalado":
		n, err := strconv.ParseInt(q.Get("installation_id"), 10, 64)
		return err == nil && n > 0
	}
	return len(r.URL.Path) > len("/github/apps/novo/") && r.URL.Path[:len("/github/apps/novo/")] == "/github/apps/novo/"
}

// continueCrossSite wraps the pages of the creation of Apps.
func continueCrossSite(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(CookieName); err != nil && r.Method == http.MethodGet && looksRight(r) {
			// only the browser says where the navigation came from; one that comes from our own
			// page is same-origin, and does not get the page again (no loop)
			if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-site" {
				h := w.Header()
				h.Set("Content-Type", "text/html; charset=utf-8")
				h.Set("Cache-Control", "no-store")
				h.Set("Referrer-Policy", "no-referrer")
				h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
				_ = continueTmpl.Execute(w, struct{ URL string }{r.URL.RequestURI()})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
