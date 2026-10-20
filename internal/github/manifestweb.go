package github

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lucasndas/mestre/internal/users"
)

// Web serves the three pages of the creation of an App by link
// (docs/github/criacao-de-apps.md): the page of the link, the return of the
// manifest and the return of the installation. The panel mounts Handler and says
// who is logged in; nothing is done for who is not the Owner.
type Web struct {
	Creation *Creation
	// Who is the user logged in the panel for the request.
	Who func(r *http.Request) (users.User, bool)
}

// Handler gives the routes: GET /github/apps/novo/{token},
// GET /github/manifest/callback and GET /github/apps/instalado.
func (w *Web) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /github/apps/novo/{token}", w.link)
	mux.HandleFunc("GET /github/manifest/callback", w.callback)
	mux.HandleFunc("GET /github/apps/instalado", w.installed)
	return secureHeaders(mux)
}

// secureHeaders keep the link (a secret in the address) from leaking and the
// page from loading anything: the form goes only to GitHub.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		h := rw.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(rw, r)
	})
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="pt-BR"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title></head><body>
<h1>{{.Title}}</h1>
{{if .Message}}<p>{{.Message}}</p>{{end}}
{{if .Form}}
<p>App: <strong>{{.Form.App.Name}}</strong>{{if .Form.App.Class}} · classe {{.Form.App.Class}}{{end}}</p>
<p>Permissões pedidas ao GitHub:</p>
<ul>{{range $k, $v := .Form.Permissions}}<li>{{$k}}: {{$v}}</li>{{end}}</ul>
{{if .Form.DeviceFlow}}<p>Depois de criar, abra a configuração do App e marque <strong>Enable Device Flow</strong>.</p>{{end}}
<form method="post" action="{{.Form.FormAction}}">
<input type="hidden" name="manifest" value="{{.Form.ManifestJSON}}">
<button type="submit">Criar no GitHub</button>
</form>
{{end}}
</body></html>`))

type pageData struct {
	Title, Message string
	Form           *Page
}

func (w *Web) render(rw http.ResponseWriter, status int, d pageData) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	if d.Form != nil {
		// the form goes to GitHub and nowhere else
		u, err := url.Parse(d.Form.FormAction)
		if err == nil {
			rw.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; form-action "+u.Scheme+"://"+u.Host)
		}
	} else {
		rw.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	}
	rw.WriteHeader(status)
	_ = pageTmpl.Execute(rw, d)
}

// owner answers who is logged in; when nobody is, or he is not the Owner, it
// writes the refusal and says false.
func (w *Web) owner(rw http.ResponseWriter, r *http.Request) (users.User, bool) {
	u, ok := w.Who(r)
	switch {
	case !ok:
		w.render(rw, http.StatusUnauthorized, pageData{Title: "Entre no painel", Message: "Abra o painel como Dono e use o link de novo."})
		return users.User{}, false
	case users.Require(u.Level, users.GitHubApps) != nil || u.State != users.Active:
		w.render(rw, http.StatusForbidden, pageData{Title: "Exige Dono", Message: "Só o Dono cria os Apps do GitHub: eles nascem na conta pessoal dele."})
		return users.User{}, false
	}
	return u, true
}

func (w *Web) link(rw http.ResponseWriter, r *http.Request) {
	u, ok := w.owner(rw, r)
	if !ok {
		return
	}
	page, err := w.Creation.Open(r.Context(), r.PathValue("token"), u)
	if err != nil {
		w.fail(rw, err)
		return
	}
	w.render(rw, http.StatusOK, pageData{Title: "Criar o App " + page.App.Name, Form: &page})
}

func (w *Web) callback(rw http.ResponseWriter, r *http.Request) {
	u, ok := w.owner(rw, r)
	if !ok {
		return
	}
	next, err := w.Creation.Callback(r.Context(), r.URL.Query().Get("code"), r.URL.Query().Get("state"), u)
	if err != nil {
		w.fail(rw, err)
		return
	}
	http.Redirect(rw, r, next, http.StatusFound)
}

func (w *Web) installed(rw http.ResponseWriter, r *http.Request) {
	u, ok := w.owner(rw, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.URL.Query().Get("installation_id"), 10, 64)
	if err != nil {
		w.fail(rw, ErrWrongInstallation)
		return
	}
	app, err := w.Creation.Installed(r.Context(), id, u)
	if err != nil {
		w.fail(rw, err)
		return
	}
	w.render(rw, http.StatusOK, pageData{Title: "App pronto", Message: "O App " + app.Name + " está instalado e pronto. Pode fechar esta página."})
}

// fail writes the page of an error: what happened, never a secret.
func (w *Web) fail(rw http.ResponseWriter, err error) {
	var lim *LimitedError
	switch {
	case errors.Is(err, ErrLinkGone):
		w.render(rw, http.StatusGone, pageData{Title: "Link vencido ou já usado", Message: "Peça outro link no painel (Apps do GitHub) ou ao Grão-Mestre."})
	case errors.Is(err, ErrOwnerOnly):
		w.render(rw, http.StatusForbidden, pageData{Title: "Exige Dono", Message: "Só o Dono cria os Apps do GitHub."})
	case errors.Is(err, ErrBadState):
		w.render(rw, http.StatusBadRequest, pageData{Title: "Retorno não reconhecido", Message: "Este retorno do GitHub não pertence a um pedido aberto do Mestre."})
	case errors.Is(err, ErrCodeLate):
		w.render(rw, http.StatusGone, pageData{Title: "Tempo esgotado", Message: "O código não foi entregue em 1 hora. Exclua o App no GitHub se ele foi criado e peça outro link."})
	case errors.Is(err, ErrWrongOwner):
		w.render(rw, http.StatusConflict, pageData{Title: "Conta diferente", Message: "O App foi criado em outra conta do GitHub e não será usado. Exclua-o naquela conta e peça outro link, abrindo-o com a sua."})
	case errors.Is(err, ErrWrongInstallation):
		w.render(rw, http.StatusBadRequest, pageData{Title: "Instalação não reconhecida", Message: "Esta instalação não é de um App do Mestre que espera instalação, na sua conta."})
	case errors.Is(err, ErrDeviceFlowOff):
		w.render(rw, http.StatusConflict, pageData{Title: "Falta um passo", Message: "Marque \"Enable Device Flow\" na configuração do App; o Mestre confere sozinho em seguida."})
	case errors.As(err, &lim):
		w.render(rw, http.StatusServiceUnavailable, pageData{Title: "GitHub pediu espera", Message: "Tente de novo em " + lim.RetryAfter.String() + "."})
	default:
		w.render(rw, http.StatusBadGateway, pageData{Title: "Não foi possível agora", Message: "O Mestre não conseguiu falar com o GitHub. Tente de novo."})
	}
}
