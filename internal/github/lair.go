package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// LairRepoName is the name of the private repository of the lock of the epoch:
// `mestre-guilda-<guild_id curto>`, the last 8 characters of the guild_id (the
// random part of the ULID; the first ones are the time), in lower case and only
// letters and digits.
func LairRepoName(guildID string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(guildID) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	short := b.String()
	if len(short) > 8 {
		short = short[len(short)-8:]
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
// `GET /installation/repositories` that the App has access and that the
// repository is private, and records the name. A 403 on the creation is
// *ErrCreateByHand; one that exists and is not private is ErrLairNotPrivate. The
// token is the one of the installation of the App, which has Repository creation,
// asked for without a repository because the repository does not exist yet.
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
		return "", &ErrCreateByHand{Name: name, Link: byHandLink(name)}
	default:
		return "", fmt.Errorf("github: creating %s: status %d", name, status)
	}
	full := ownerLogin + "/" + name
	found, private, err := l.find(ctx, full, installationToken)
	if err != nil {
		return "", err
	}
	if !found {
		return "", &ErrCreateByHand{Name: name, Link: byHandLink(name)}
	}
	if !private {
		// a repository of that name exists and anybody can read it: the lock is
		// not kept there
		return "", ErrLairNotPrivate
	}
	if err := l.Apps.SetLair(ctx, full); err != nil {
		return "", err
	}
	return full, nil
}

// ErrLairNotPrivate: the repository of the lock exists and is not private.
var ErrLairNotPrivate = errors.New("github: the repository of the lock is not private")

func byHandLink(name string) string {
	return "https://github.com/new?name=" + url.QueryEscape(name) + "&visibility=private"
}

// find looks for the repository among the ones of the installation, page by page
// (100 on each), and says if it is private.
func (l *Lair) find(ctx context.Context, full, token string) (found, private bool, err error) {
	for page := 1; page <= 50; page++ {
		status, raw, err := l.do(ctx, http.MethodGet, fmt.Sprintf("/installation/repositories?per_page=100&page=%d", page), token, nil)
		if err != nil {
			return false, false, err
		}
		if status != http.StatusOK {
			return false, false, fmt.Errorf("github: reading the repositories of the installation: status %d", status)
		}
		var list struct {
			Repositories []struct {
				FullName string `json:"full_name"`
				Private  bool   `json:"private"`
			} `json:"repositories"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return false, false, errors.New("github: the list of repositories of the installation is not readable")
		}
		for _, r := range list.Repositories {
			if strings.EqualFold(r.FullName, full) {
				return true, r.Private, nil
			}
		}
		if len(list.Repositories) < 100 {
			return false, false, nil
		}
	}
	return false, false, nil
}
