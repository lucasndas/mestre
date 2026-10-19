package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// LairRepoName is the name of the private repository of the lock of the epoch:
// `mestre-guilda-<guild_id curto>` (the first characters of the guild_id, in lower
// case).
func LairRepoName(guildID string) string {
	short := strings.ToLower(guildID)
	if len(short) > 8 {
		short = short[len(guildID)-8:]
	}
	return "mestre-guilda-" + short
}

// ErrCreateByHand: GitHub refused the creation of the repository with the token of
// the App "Grão-Mestre" (403); the Owner gets the link to make it in the browser
// and no promotion is possible until the engine confirms the access.
type ErrCreateByHand struct {
	// Link is the page of GitHub to create a private repository with the name
	// already filled in.
	Link string
	Name string
}

func (e *ErrCreateByHand) Error() string {
	return fmt.Sprintf("github: GitHub did not let the App create %s: create it in the browser: %s", e.Name, e.Link)
}

// Lair makes and finds the repository of the lock.
type Lair struct {
	Apps    *Registry
	BaseURL string
	HTTP    *http.Client
}

func (l *Lair) do(ctx context.Context, method, path, token string, body any) (int, []byte, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(l.BaseURL, "/")+path, r)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	hc := l.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, raw, nil
}

// Ensure creates the repository of the lock after the App "Grão-Mestre" was
// installed (`POST /user/repos`, `private: true` and `auto_init: true`, because a
// ref can not be made in an empty repository), then checks with
// `GET /installation/repositories` that the App has access, and records the name.
// A 403 on the creation is *ErrCreateByHand. The token is the one of the
// installation of the App, which has Repository creation: it is asked for the
// repository after it is made, so the first call uses a token of the installation
// without a repository.
func (l *Lair) Ensure(ctx context.Context, ownerLogin, guildID, installationToken string) (string, error) {
	name := LairRepoName(guildID)
	status, _, err := l.do(ctx, http.MethodPost, "/user/repos", installationToken, map[string]any{"name": name, "private": true, "auto_init": true})
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusCreated:
	case http.StatusUnprocessableEntity:
		// it exists already: a repeated call must not fail, the access is checked below
	case http.StatusForbidden, http.StatusNotFound:
		return "", &ErrCreateByHand{Name: name, Link: "https://github.com/new?name=" + name + "&visibility=private"}
	default:
		return "", fmt.Errorf("github: creating %s: status %d", name, status)
	}
	status, raw, err := l.do(ctx, http.MethodGet, "/installation/repositories", installationToken, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("github: reading the repositories of the installation: status %d", status)
	}
	var list struct {
		Repositories []struct {
			FullName string `json:"full_name"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return "", errors.New("github: the list of repositories of the installation is not readable")
	}
	full := ownerLogin + "/" + name
	for _, r := range list.Repositories {
		if strings.EqualFold(r.FullName, full) {
			if err := l.Apps.SetLair(ctx, full); err != nil {
				return "", err
			}
			return full, nil
		}
	}
	return "", &ErrCreateByHand{Name: name, Link: "https://github.com/new?name=" + name + "&visibility=private"}
}
