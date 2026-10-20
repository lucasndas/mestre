package fakegithub

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"strconv"
	"strings"
)

// Conversion is what GitHub gives for the code of a manifest
// (POST /app-manifests/{code}/conversions): the App that was made.
type Conversion struct {
	Name, Slug string
	AppID      int64
	ClientID   string
	// Owner is the login of the account that made the App.
	Owner string
	// Key is the private key GitHub gives; the double checks the JWTs against it.
	Key *rsa.PrivateKey
}

// AddManifestCode makes a code of a manifest that GitHub would give after the
// "Create GitHub App" click. The first conversion registers the App (without an
// installation) and spends the code, as the real one does.
func (s *Server) AddManifestCode(code string, c Conversion) {
	s.mu.Lock()
	if s.manifests == nil {
		s.manifests = map[string]Conversion{}
	}
	s.manifests[code] = c
	s.mu.Unlock()
}

// FailConversion makes the next conversion answer with the status and the
// headers (a 429 with Retry-After is the limit of use of the endpoint); the
// code is not spent.
func (s *Server) FailConversion(status int, headers map[string]string) {
	s.mu.Lock()
	s.convFail, s.convFailHdr = status, headers
	s.mu.Unlock()
}

// Conversions is how many conversions of a code were asked for, the refused ones too.
func (s *Server) Conversions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conversions
}

// Install installs the App (by slug) in the account, in the repositories
// (`owner/name`), with the id given: what the click on "Install" does.
func (s *Server) Install(slug string, installationID int64, account string, repos ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.apps {
		if a.slug == slug {
			a.installationID, a.account, a.repos = installationID, account, repos
			s.installs[installationID] = a
		}
	}
}

// InstallationLookups is how many times GET /users/{login}/installation was asked.
func (s *Server) InstallationLookups() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookups
}

// SetDeviceFlow says whether the App (by slug) has "Enable Device Flow" marked.
func (s *Server) SetDeviceFlow(slug string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range s.apps {
		if a.slug == slug {
			a.deviceFlow = on
		}
	}
}

// serveManifest serves the routes of the creation of an App by manifest.
func (s *Server) serveManifest(w http.ResponseWriter, r *http.Request, parts []string) bool {
	switch {
	case r.Method == http.MethodPost && len(parts) == 3 && parts[0] == "app-manifests" && parts[2] == "conversions":
		s.convert(w, parts[1])
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "app" && parts[1] == "installations":
		s.appInstallation(w, r, parts[2])
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "users" && parts[2] == "installation":
		s.userInstallation(w, r, parts[1])
	case r.Method == http.MethodPost && r.URL.Path == "/login/device/code":
		s.deviceCode(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) convert(w http.ResponseWriter, code string) {
	s.mu.Lock()
	s.conversions++
	if st := s.convFail; st != 0 {
		hdr := s.convFailHdr
		s.convFail, s.convFailHdr = 0, nil
		s.mu.Unlock()
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		writeJSON(w, st, map[string]any{"message": "injected failure"})
		return
	}
	c, ok := s.manifests[code]
	delete(s.manifests, code) // the code is spent
	if ok {
		a := &appInfo{slug: c.Slug, appID: c.AppID, key: &c.Key.PublicKey, clientID: c.ClientID}
		s.apps[c.AppID] = a
		s.bots[c.Slug+"[bot]"] = c.AppID + 1000
	}
	s.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(c.Key)})
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": c.AppID, "slug": c.Slug, "name": c.Name, "client_id": c.ClientID, "client_secret": "fake-client-secret",
		"webhook_secret": "fake-webhook-secret", "pem": string(pemBytes), "owner": map[string]any{"login": c.Owner},
	})
}

// appOf is the App a JWT says it is of (the issuer), when the signature holds.
func (s *Server) appOf(r *http.Request) *appInfo {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var c struct {
		Iss int64 `json:"iss"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return nil
	}
	s.mu.Lock()
	app := s.apps[c.Iss]
	s.mu.Unlock()
	if app == nil || !s.verifyJWT(tok, app) {
		return nil
	}
	return app
}

func (s *Server) installationView(app *appInfo) map[string]any {
	return map[string]any{"id": app.installationID, "app_id": app.appID, "app_slug": app.slug,
		"account": map[string]any{"login": app.account}, "repository_selection": "selected"}
}

// appInstallation is GET /app/installations/{id}, with the JWT of an App: an
// installation of another App is not found.
func (s *Server) appInstallation(w http.ResponseWriter, r *http.Request, id string) {
	app := s.appOf(r)
	n, err := strconv.ParseInt(id, 10, 64)
	if app == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "A JSON web token could not be decoded"})
		return
	}
	s.mu.Lock()
	inst := s.installs[n]
	s.mu.Unlock()
	if err != nil || inst == nil || inst != app {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	writeJSON(w, http.StatusOK, s.installationView(app))
}

// userInstallation is GET /users/{login}/installation, with the JWT of an App.
func (s *Server) userInstallation(w http.ResponseWriter, r *http.Request, login string) {
	s.mu.Lock()
	s.lookups++
	s.mu.Unlock()
	app := s.appOf(r)
	if app == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "A JSON web token could not be decoded"})
		return
	}
	s.mu.Lock()
	has := app.installationID != 0 && app.account == login
	s.mu.Unlock()
	if !has {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	writeJSON(w, http.StatusOK, s.installationView(app))
}

// deviceCode is POST /login/device/code: the device flow of an App that has it enabled.
func (s *Server) deviceCode(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad_request"})
		return
	}
	id := r.PostForm.Get("client_id")
	s.mu.Lock()
	var on, found bool
	for _, a := range s.apps {
		if a.clientID == id && id != "" {
			on, found = a.deviceFlow, true
		}
	}
	s.mu.Unlock()
	switch {
	case !found:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Not Found"})
	case !on:
		writeJSON(w, http.StatusOK, map[string]any{"error": "device_flow_disabled", "error_description": "Device flow must be explicitly enabled for this App"})
	default:
		writeJSON(w, http.StatusOK, s.registerDevice(id))
	}
}
