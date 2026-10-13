package vault

import (
	"errors"
	"fmt"
	"slices"
)

// ErrNotReleased is wrapped when a class may not use a site credential.
var ErrNotReleased = errors.New("credential not released to this class")

// SiteCredentialAllowed decides, from metadata only, whether a character of
// class may use the site credential described by cred: the class lists the
// credential in network.credentials (classCredentials) and the credential
// lists the class (docs/plataforma/cofre.md#credenciais-de-site). The
// Citadel checks it before every release, local or requested by a Tower.
func SiteCredentialAllowed(cred Info, class string, classCredentials []string) error {
	if cred.Type != TypeSiteCredential || cred.Site == nil {
		return fmt.Errorf("vault: secret %q is not a site credential: %w", cred.Name, ErrNotReleased)
	}
	if !slices.Contains(classCredentials, cred.Name) {
		return fmt.Errorf("vault: class %q does not list %q in network.credentials: %w", class, cred.Name, ErrNotReleased)
	}
	if !slices.Contains(cred.Site.Classes, class) {
		return fmt.Errorf("vault: credential %q does not list class %q: %w", cred.Name, class, ErrNotReleased)
	}
	return nil
}
