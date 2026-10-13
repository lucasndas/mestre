package vault

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Composite values (docs/plataforma/cofre.md#formato): a site credential and
// a GitHub user token are JSON objects; every other type is the secret
// itself (a token, a key, a PEM block).

// SiteCredentialValue is the encrypted part of a site_credential.
type SiteCredentialValue struct {
	URL      string `json:"url"`
	User     string `json:"user"`
	Password string `json:"password"` // or the app password, token or API key
	// Selectors and Success are optional hints for the login browser.
	Selectors map[string]string `json:"selectors,omitempty"`
	Success   string            `json:"success,omitempty"`
}

// GitHubUserTokenValue is the encrypted part of a github_user_token.
type GitHubUserTokenValue struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

// validateValue checks the format of composite values. Errors never carry
// the value.
func validateValue(name string, typ Type, value []byte) error {
	switch typ {
	case TypeSiteCredential:
		var v SiteCredentialValue
		if err := strictJSON(value, &v); err != nil || v.Password == "" || v.URL == "" {
			return fmt.Errorf("vault: secret %q: a site credential is a JSON object with url, password and, optionally, user, selectors and success", name)
		}
	case TypeGitHubUserToken:
		var v GitHubUserTokenValue
		if err := strictJSON(value, &v); err != nil || v.AccessToken == "" {
			return fmt.Errorf("vault: secret %q: a GitHub user token is a JSON object with access_token and, optionally, refresh_token", name)
		}
	}
	return nil
}

func strictJSON(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing data")
	}
	return nil
}

// maskParts returns every string Use registers in the masker for a value of
// typ: the whole value, each sensitive component of a composite value (the
// password of a site credential, the access and refresh tokens of a GitHub
// user token) and each line of a multi-line value (a PEM key logged one line
// at a time), except the PEM armor lines, which are not secret. The masker
// ignores strings shorter than 4 bytes.
func maskParts(typ Type, value []byte) []string {
	parts := []string{string(value)}
	var components []string
	switch typ {
	case TypeSiteCredential:
		var v SiteCredentialValue
		if json.Unmarshal(value, &v) == nil {
			components = append(components, v.Password)
		}
	case TypeGitHubUserToken:
		var v GitHubUserTokenValue
		if json.Unmarshal(value, &v) == nil {
			components = append(components, v.AccessToken, v.RefreshToken)
		}
	}
	for _, c := range append([]string{string(value)}, components...) {
		if c != string(value) {
			parts = append(parts, c)
		}
		if !strings.ContainsAny(c, "\r\n") {
			continue
		}
		for line := range strings.Lines(c) {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "-----") {
				parts = append(parts, line)
			}
		}
	}
	return parts
}
