package panel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/internal/vault"
)

func init() { RegisterArea("secrets", mountSecrets) }

// SecretVault is what the panel needs of the vault (*vault.Vault). Nothing in it
// gives a value back: the panel can list, replace and delete, never read.
type SecretVault interface {
	List(ctx context.Context) ([]vault.Info, error)
	Info(ctx context.Context, name string) (vault.Info, error)
	Put(ctx context.Context, name string, typ vault.Type, value []byte, site *vault.SiteMeta) error
	Delete(ctx context.Context, name string) error
	RewrapRecovery(ctx context.Context, public []byte) error
}

const (
	pinSecretPut      = "vault.put"
	pinSecretDelete   = "vault.delete"
	pinRecoveryKey    = "vault.recovery_key"
	maxSecretNameRune = 80
)

func mountSecrets(r *Router) {
	p := r.Panel()
	r.Require("GET /api/secrets", p.handleSecrets, users.BasesVaultPolicy)
	r.Sensitive("POST /api/secrets/site", pinSecretPut, users.BasesVaultPolicy, p.handleCreateSite)
	r.Sensitive("PUT /api/secrets/{name}", pinSecretPut, users.BasesVaultPolicy, p.handleReplaceSecret)
	r.Sensitive("DELETE /api/secrets/{name}", pinSecretDelete, users.BasesVaultPolicy, p.handleDeleteSecret)
	r.Sensitive("POST /api/recovery-key", pinRecoveryKey, users.BasesVaultPolicy, p.handleRecoveryKey)
}

// changeable are the types the panel replaces and deletes: the ones a person put in
// the vault. The rest is kept by the Mestre itself (the keys of the Apps, the
// tokens of the users, the CA, the bot, the capture key of the Operator): listed,
// never changed here, so that a slip does not take the engine down.
func changeable(t vault.Type) bool {
	return t == vault.TypeSiteCredential || t == vault.TypeProviderAPIKey
}

func secretRow(i vault.Info, now time.Time) SecretRow {
	row := SecretRow{Name: i.Name, Type: string(i.Type), Fingerprint: i.Fingerprint, CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt,
		LastUsedAt: i.LastUsedAt, Managed: !changeable(i.Type)}
	if i.Site != nil {
		row.Site = &SiteMetaInfo{OwnerUser: i.Site.OwnerUser, Classes: listOf(i.Site.Classes), Domains: listOf(i.Site.Domains),
			Kind: string(i.Site.Kind), ExpiresAt: i.Site.ExpiresAt}
		row.Notice = noticeOf(i, now)
	}
	return row
}

const day = 24 * time.Hour

// noticeOf is the validity notice of a site credential, with the defaults of the
// vault (docs/plataforma/cofre.md#credenciais-de-site).
func noticeOf(i vault.Info, now time.Time) *SecretNotice {
	if exp := i.Site.ExpiresAt; exp != nil {
		switch left := exp.Sub(now); {
		case left <= 0:
			return &SecretNotice{Kind: "expired", Days: int(-left / day)}
		case left <= vault.ExpiryLead:
			return &SecretNotice{Kind: "expiring", Days: int((left + day - 1) / day)}
		}
		return nil
	}
	if age := now.Sub(i.UpdatedAt); age >= vault.StaleAfter {
		return &SecretNotice{Kind: "stale", Days: int(age / day)}
	}
	return nil
}

// secretSnapshot is what the audit keeps of a secret: what the list shows, never a value.
func secretSnapshot(i vault.Info) map[string]any {
	m := map[string]any{"type": string(i.Type), "fingerprint": i.Fingerprint}
	if s := i.Site; s != nil {
		m["owner_user"], m["classes"], m["domains"], m["kind"] = s.OwnerUser, s.Classes, s.Domains, string(s.Kind)
		if s.ExpiresAt != nil {
			m["expires_at"] = s.ExpiresAt
		}
	}
	return m
}

func (p *Panel) needVault(w http.ResponseWriter) bool {
	if p.vault == nil {
		WriteError(w, Err(CodeUnavailable, "o cofre não está disponível agora", nil))
		return false
	}
	return true
}

func (p *Panel) vaultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, vault.ErrClosed):
		WriteError(w, Err(CodeVaultClosed, "o cofre está fechado; veja o alerta grave", nil))
	case errors.Is(err, vault.ErrNotFound):
		WriteError(w, Err(CodeNotFound, "segredo não encontrado", nil))
	default:
		p.log.Error("panel: the vault", "err", err)
		WriteError(w, errInternal())
	}
}

func (p *Panel) handleSecrets(w http.ResponseWriter, r *http.Request) {
	if !p.needVault(w) {
		return
	}
	list, err := p.vault.List(r.Context())
	if err != nil {
		p.vaultError(w, err)
		return
	}
	now := p.clk.Now()
	out := SecretList{Secrets: make([]SecretRow, 0, len(list))}
	for _, i := range list {
		out.Secrets = append(out.Secrets, secretRow(i, now))
	}
	WriteJSON(w, http.StatusOK, out)
}

// siteMeta checks the credential and makes what the vault keeps of it.
func (p *Panel) siteMeta(ctx context.Context, in *SiteCredentialInput) (*vault.SiteMeta, []byte, *APIError) {
	bad := func(field string) (*vault.SiteMeta, []byte, *APIError) {
		return nil, nil, Err(CodeInvalidSecret, "campo inválido: "+field, map[string]any{"field": field})
	}
	if u, err := url.Parse(strings.TrimSpace(in.URL)); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return bad("url")
	}
	if in.Password == "" {
		return bad("password")
	}
	kind := vault.SiteKind(in.Kind)
	if !slices.Contains([]vault.SiteKind{vault.KindMainPassword, vault.KindAppPassword, vault.KindToken, vault.KindAPIKey}, kind) {
		return bad("kind")
	}
	clean := func(l []string) []string {
		var out []string
		for _, s := range l {
			if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
		return out
	}
	classes, domains := clean(in.Classes), clean(in.Domains)
	if len(classes) == 0 {
		return bad("classes")
	}
	if len(domains) == 0 {
		return bad("domains")
	}
	owner := in.OwnerUser
	if owner == "" { // the default is the Owner
		o, err := p.ownerID(ctx)
		if err != nil {
			return nil, nil, errInternal()
		}
		owner = o
	}
	if u, err := p.users.Get(ctx, owner); err != nil || u.State != users.Active {
		return bad("owner_user")
	}
	val, err := json.Marshal(vault.SiteCredentialValue{URL: strings.TrimSpace(in.URL), User: in.User, Password: in.Password, Selectors: in.Selectors, Success: in.Success})
	if err != nil {
		return nil, nil, errInternal()
	}
	return &vault.SiteMeta{OwnerUser: owner, Classes: classes, Domains: domains, Kind: kind, ExpiresAt: in.ExpiresAt}, val, nil
}

func (p *Panel) ownerID(ctx context.Context) (string, error) {
	var id string
	err := p.db.Reader().QueryRowContext(ctx, `SELECT id FROM users WHERE level = 'owner' AND state <> 'removed'`).Scan(&id)
	return id, err
}

func validSecretName(name string) bool {
	n := strings.TrimSpace(name)
	return n != "" && n == name && utf8.RuneCountInString(n) <= maxSecretNameRune && !strings.ContainsAny(n, ":/\\")
}

func (p *Panel) handleCreateSite(w http.ResponseWriter, r *http.Request) {
	if !p.needVault(w) {
		return
	}
	ctx, me := r.Context(), UserOf(r)
	var in SiteCredentialInput
	if !decode(w, r, &in) {
		return
	}
	if !validSecretName(in.Name) {
		WriteError(w, Err(CodeInvalidSecret, "campo inválido: name", map[string]any{"field": "name"}))
		return
	}
	if _, err := p.vault.Info(ctx, in.Name); err == nil {
		WriteError(w, Err(CodeSecretExists, "já existe um segredo com este nome; use Substituir", nil))
		return
	} else if !errors.Is(err, vault.ErrNotFound) {
		p.vaultError(w, err)
		return
	}
	meta, val, apiErr := p.siteMeta(ctx, &in)
	if apiErr != nil {
		WriteError(w, apiErr)
		return
	}
	defer clear(val)
	if err := p.vault.Put(ctx, in.Name, vault.TypeSiteCredential, val, meta); err != nil {
		p.vaultError(w, err)
		return
	}
	info, err := p.vault.Info(ctx, in.Name)
	if err != nil {
		p.vaultError(w, err)
		return
	}
	p.audit(ctx, "panel.secret.created", map[string]any{"user_id": me.ID, "secret": in.Name, "after": secretSnapshot(info), "ip": clientIP(r)})
	WriteJSON(w, http.StatusCreated, secretRow(info, p.clk.Now()))
}

// changeableSecret finds the secret of the path and refuses one the panel does not change.
func (p *Panel) changeableSecret(w http.ResponseWriter, r *http.Request) (vault.Info, bool) {
	if !p.needVault(w) {
		return vault.Info{}, false
	}
	info, err := p.vault.Info(r.Context(), r.PathValue("name"))
	if err != nil {
		p.vaultError(w, err)
		return vault.Info{}, false
	}
	if !changeable(info.Type) {
		WriteError(w, Err(CodeSecretManaged, "este segredo é guardado pelo próprio Mestre e não se muda por aqui", map[string]any{"type": string(info.Type)}))
		return vault.Info{}, false
	}
	return info, true
}

func (p *Panel) handleReplaceSecret(w http.ResponseWriter, r *http.Request) {
	before, ok := p.changeableSecret(w, r)
	if !ok {
		return
	}
	ctx, me := r.Context(), UserOf(r)
	var in ReplaceSecretRequest
	if !decode(w, r, &in) {
		return
	}
	var (
		val  []byte
		meta *vault.SiteMeta
	)
	if before.Type == vault.TypeSiteCredential {
		if in.Site == nil {
			WriteError(w, Err(CodeInvalidSecret, "campo inválido: site", map[string]any{"field": "site"}))
			return
		}
		var apiErr *APIError
		if meta, val, apiErr = p.siteMeta(ctx, in.Site); apiErr != nil {
			WriteError(w, apiErr)
			return
		}
	} else {
		if in.Value == "" {
			WriteError(w, Err(CodeInvalidSecret, "campo inválido: value", map[string]any{"field": "value"}))
			return
		}
		val = []byte(in.Value)
	}
	defer clear(val)
	if err := p.vault.Put(ctx, before.Name, before.Type, val, meta); err != nil {
		p.vaultError(w, err)
		return
	}
	after, err := p.vault.Info(ctx, before.Name)
	if err != nil {
		p.vaultError(w, err)
		return
	}
	p.audit(ctx, "panel.secret.replaced", map[string]any{"user_id": me.ID, "secret": before.Name, "before": secretSnapshot(before), "after": secretSnapshot(after), "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, secretRow(after, p.clk.Now()))
}

func (p *Panel) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	before, ok := p.changeableSecret(w, r)
	if !ok {
		return
	}
	ctx, me := r.Context(), UserOf(r)
	if err := p.vault.Delete(ctx, before.Name); err != nil {
		p.vaultError(w, err)
		return
	}
	p.audit(ctx, "panel.secret.deleted", map[string]any{"user_id": me.ID, "secret": before.Name, "before": secretSnapshot(before), "ip": clientIP(r)})
	w.WriteHeader(http.StatusNoContent)
}

// handleRecoveryKey makes a new recovery key: the Owner read the warning about the
// old backups (Acknowledged), the vault envelopes its key to the new one, and the
// key is in this answer and nowhere else.
func (p *Panel) handleRecoveryKey(w http.ResponseWriter, r *http.Request) {
	if !p.needVault(w) {
		return
	}
	ctx, me := r.Context(), UserOf(r)
	var in RecoveryKeyRequest
	if !decode(w, r, &in) {
		return
	}
	if !in.Acknowledged {
		WriteError(w, Err(CodeAckRequired, "confirme que leu o aviso sobre os backups antigos", nil))
		return
	}
	rk, err := vault.NewRecoveryKey()
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	defer rk.Destroy()
	if err := p.vault.RewrapRecovery(ctx, rk.PublicKey()); err != nil {
		p.vaultError(w, err)
		return
	}
	p.audit(ctx, "panel.recovery_key.renewed", map[string]any{"user_id": me.ID, "ip": clientIP(r)})
	WriteJSON(w, http.StatusOK, RecoveryKeyResponse{Key: rk.Reveal()})
}
