package github

import (
	"context"
	"fmt"
	"net/http"
)

// lockPermissions are what the token of the lock asks for: the contents of the one
// repository of the lock (refs and commits), nothing else.
var lockPermissions = map[string]string{"contents": "write", "metadata": "read"}

// NewLairLock is the lock of the epoch over the repository of the lock the Guild
// recorded (Registry.SetLair), with the token of the App "Grão-Mestre" for that
// one repository. It returns nil, and no error, while no repository was recorded:
// the Guild has no lock, so no promotion (docs/guilda/trava-no-github.md#repositório-e-refs).
// The repository and the token are asked for at each request, so a repository
// recorded later, or an App that went to error, is followed.
func NewLairLock(ctx context.Context, tokens *Tokens, guildID, baseURL string, hc *http.Client) (*LairLock, error) {
	if _, ok, err := tokens.Apps.LairRepo(ctx); err != nil || !ok {
		return nil, err
	}
	return &LairLock{
		Repo: func(ctx context.Context) (string, error) {
			repo, ok, err := tokens.Apps.LairRepo(ctx)
			if err != nil || !ok {
				return "", fmt.Errorf("%w: no repository of the lock", ErrLockUnreachable)
			}
			return repo, nil
		},
		Token: func(ctx context.Context) (string, error) {
			repo, ok, err := tokens.Apps.LairRepo(ctx)
			if err != nil || !ok {
				return "", fmt.Errorf("%w: no repository of the lock", ErrLockUnreachable)
			}
			app, err := tokens.Apps.Fixed(ctx, RoleGrandMaster)
			if err != nil {
				return "", err
			}
			tok, err := tokens.Installation(ctx, Request{App: app.Name, Repo: repo, Permissions: lockPermissions})
			if err != nil {
				return "", err
			}
			return tok.Secret(), nil
		},
		GuildID: guildID, BaseURL: baseURL, HTTP: hc, Clock: tokens.Clock,
	}, nil
}
