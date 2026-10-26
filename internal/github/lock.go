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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// epochPrefix is the prefix the lock lists (docs/guilda/trava-no-github.md#repositório-e-refs).
const epochPrefix = "mestre/epoca-"

var epochRef = regexp.MustCompile(`^refs/mestre/epoca-([0-9]{1,18})$`)

// LairLock is the lock of the epoch over the private repository of the Guild
// (guild.Lock): the refs `refs/mestre/epoca-N`, the highest N is the epoch. Its
// requests go one at a time, never in parallel (the secondary limit of GitHub).
// The token is asked for at each request and never kept here or in an error.
type LairLock struct {
	// Repo is `owner/name` of the repository of the lock.
	Repo func(ctx context.Context) (string, error)
	// Token is an installation token of the App "Grão-Mestre" for that
	// repository, with the permission to write contents.
	Token   func(ctx context.Context) (string, error)
	GuildID string
	BaseURL string
	HTTP    *http.Client
	Clock   clock.Clock

	mu    sync.Mutex
	etag  string
	known uint64 // the highest epoch of the listing the ETag belongs to
	multi bool   // the last listing had more than one page: no conditional request
}

var _ guild.Lock = (*LairLock)(nil)

// ErrLockUnreachable wraps every failure to read or write the lock: GitHub out
// (network, time out, 5xx, a limit, the repository gone) is not a higher epoch.
var ErrLockUnreachable = errors.New("github: the lock cannot be reached")

func (l *LairLock) do(ctx context.Context, method, path string, body any, header map[string]string) (int, http.Header, []byte, error) {
	return l.doAt(ctx, method, "/git/"+path, body, header)
}

// doAt makes a request to a path under the repository (`/git/...`, or “ for the repository).
func (l *LairLock) doAt(ctx context.Context, method, path string, body any, header map[string]string) (int, http.Header, []byte, error) {
	repo, err := l.Repo(ctx)
	if err != nil || repo == "" {
		return 0, nil, nil, fmt.Errorf("%w: no repository of the lock", ErrLockUnreachable)
	}
	tok, err := l.Token(ctx)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%w: no token for the lock", ErrLockUnreachable)
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(l.BaseURL, "/")+"/repos/"+repo+path, rd)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%w: %v", ErrLockUnreachable, scrubToken(err.Error(), tok))
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	hc := l.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%w: %v", ErrLockUnreachable, scrubToken(err.Error(), tok))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%w: %v", ErrLockUnreachable, scrubToken(err.Error(), tok))
	}
	return resp.StatusCode, resp.Header, raw, nil
}

func scrubToken(s, tok string) string {
	if tok == "" {
		return s
	}
	return strings.ReplaceAll(s, tok, "***")
}

func statusError(what string, status int, h http.Header) error {
	if ra := h.Get("Retry-After"); ra != "" {
		return fmt.Errorf("%w: %s: status %d, retry after %ss", ErrLockUnreachable, what, status, ra)
	}
	return fmt.Errorf("%w: %s: status %d", ErrLockUnreachable, what, status)
}

// list reads the refs of the lock and returns the highest epoch. The first page
// is conditional: a 304 repeats the listing that confirmed before.
func (l *LairLock) list(ctx context.Context, conditional bool) (uint64, error) {
	hdr := map[string]string{}
	if conditional && l.etag != "" && !l.multi {
		hdr["If-None-Match"] = l.etag
	}
	status, h, raw, err := l.do(ctx, http.MethodGet, "matching-refs/"+epochPrefix+"?per_page=100", nil, hdr)
	if err != nil {
		return 0, err
	}
	switch status {
	case http.StatusNotModified:
		if l.etag == "" {
			return 0, statusError("an unexpected 304", status, h)
		}
		return l.known, nil
	case http.StatusOK:
	default:
		return 0, statusError("listing the refs", status, h)
	}
	highest, err := highestEpoch(raw)
	if err != nil {
		return 0, err
	}
	pages := 1
	for next := nextLink(h); next != ""; {
		if pages++; pages > maxPages {
			return 0, fmt.Errorf("%w: the list of refs has more than %d pages", ErrLockUnreachable, maxPages)
		}
		st, h2, raw2, err := l.doURL(ctx, next)
		if err != nil {
			return 0, err
		}
		if st != http.StatusOK {
			return 0, statusError("listing the refs", st, h2)
		}
		n, err := highestEpoch(raw2)
		if err != nil {
			return 0, err
		}
		highest = max(highest, n)
		next = nextLink(h2)
	}
	l.multi = pages > 1 // the ETag is of the first page only: with more pages every check lists them all
	l.etag, l.known = h.Get("ETag"), highest
	return highest, nil
}

func highestEpoch(raw []byte) (uint64, error) {
	var refs []struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(raw, &refs); err != nil {
		return 0, fmt.Errorf("%w: the list of refs is not readable", ErrLockUnreachable)
	}
	var highest uint64
	for _, r := range refs {
		if m := epochRef.FindStringSubmatch(r.Ref); m != nil {
			n, _ := strconv.ParseUint(m[1], 10, 64)
			highest = max(highest, n)
		}
	}
	return highest, nil
}

// maxPages bounds the pages of the listing (100 refs each): a Link that never ends is an error.
const maxPages = 20

var linkNext = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func nextLink(h http.Header) string {
	if m := linkNext.FindStringSubmatch(h.Get("Link")); m != nil {
		return m[1]
	}
	return ""
}

// doURL follows a Link of a listing, only to the host of BaseURL: the path and
// the query are taken from it, and the request is made as any other (GitHub
// writes these links with /repositories/<id> instead of the name).
func (l *LairLock) doURL(ctx context.Context, link string) (int, http.Header, []byte, error) {
	base, err := url.Parse(l.BaseURL)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%w: bad base URL", ErrLockUnreachable)
	}
	u, err := url.Parse(link)
	if err != nil || !strings.EqualFold(u.Host, base.Host) || u.Scheme != base.Scheme {
		return 0, nil, nil, fmt.Errorf("%w: a link to somewhere else", ErrLockUnreachable)
	}
	repo, _ := l.Repo(ctx)
	_, after, ok := strings.Cut(u.Path, "/git/")
	if !ok || (!strings.Contains(u.Path, "/repos/"+repo+"/") && !strings.Contains(u.Path, "/repositories/")) {
		return 0, nil, nil, fmt.Errorf("%w: a link to somewhere else", ErrLockUnreachable)
	}
	if u.RawQuery != "" {
		after += "?" + u.RawQuery
	}
	return l.do(ctx, http.MethodGet, after, nil, nil)
}

// Check reads the highest epoch the lock shows: one conditional listing. An
// error is "GitHub fora".
func (l *LairLock) Check(ctx context.Context) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.list(ctx, true)
}

// Claim tries to create the epoch n for the machine: its own commit, the
// creation of the ref and the reading of the ref whatever the answer. It wins
// only when the ref names that commit. A ref of an epoch not above the highest
// is never created.
func (l *LairLock) Claim(ctx context.Context, n uint64, machineID string) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	highest, err := l.list(ctx, false)
	if err != nil {
		return false, err
	}
	if n != highest+1 {
		return false, nil // not the next epoch: another machine has already moved the lock
	}
	// The base is the first commit of the default branch of the repository.
	status, h, raw, err := l.doAt(ctx, http.MethodGet, "", nil, nil)
	if err != nil {
		return false, err
	}
	var info struct {
		DefaultBranch string `json:"default_branch"`
	}
	if status != http.StatusOK || json.Unmarshal(raw, &info) != nil || info.DefaultBranch == "" {
		return false, statusError("reading the repository", status, h)
	}
	status, h, raw, err = l.do(ctx, http.MethodGet, "ref/heads/"+info.DefaultBranch, nil, nil)
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		return false, statusError("reading the first commit", status, h)
	}
	var head struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if json.Unmarshal(raw, &head) != nil || head.Object.SHA == "" {
		return false, fmt.Errorf("%w: the first commit is not readable", ErrLockUnreachable)
	}
	status, h, raw, err = l.do(ctx, http.MethodGet, "commits/"+head.Object.SHA, nil, nil)
	if err != nil {
		return false, err
	}
	var base struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if status != http.StatusOK || json.Unmarshal(raw, &base) != nil || base.Tree.SHA == "" {
		return false, statusError("reading the tree of the first commit", status, h)
	}
	msg := fmt.Sprintf("guild_id=%s machine_id=%s epoch=%d at=%s", l.GuildID, machineID, n, l.Clock.Now().UTC().Format(time.RFC3339))
	status, h, raw, err = l.do(ctx, http.MethodPost, "commits", map[string]any{"message": msg, "tree": base.Tree.SHA, "parents": []string{head.Object.SHA}}, nil)
	if err != nil {
		return false, err
	}
	var mine struct {
		SHA string `json:"sha"`
	}
	if status != http.StatusCreated || json.Unmarshal(raw, &mine) != nil || mine.SHA == "" {
		return false, statusError("making the commit of the claim", status, h)
	}
	ref := "refs/" + epochPrefix + strconv.FormatUint(n, 10)
	// Whatever the creation answered (201, 422 that also covers "spammed", a 5xx or a
	// cut connection after the server did it), the ref says who won: it is read.
	createStatus, ch, _, createErr := l.do(ctx, http.MethodPost, "refs", map[string]any{"ref": ref, "sha": mine.SHA}, nil)
	var readErr error
	for try := 0; try < 3; try++ {
		if try > 0 {
			if err := clock.Sleep(ctx, l.Clock, time.Second<<try); err != nil {
				return false, err
			}
		}
		status, h, raw, err = l.do(ctx, http.MethodGet, "ref/"+strings.TrimPrefix(ref, "refs/"), nil, nil)
		if err != nil {
			readErr = err
			continue
		}
		if status != http.StatusOK {
			readErr = statusError("reading the ref", status, h)
			if status != http.StatusNotFound {
				break
			}
			continue // a 404 right after the creation may be lag
		}
		var got struct {
			Object struct {
				SHA string `json:"sha"`
			} `json:"object"`
		}
		if json.Unmarshal(raw, &got) != nil {
			return false, fmt.Errorf("%w: the ref is not readable", ErrLockUnreachable)
		}
		l.etag = "" // the listing changed
		return got.Object.SHA == mine.SHA, nil
	}
	// Neither the creation nor the reading told: the machine did not win, and the
	// next check shows what became of the epoch.
	if createErr != nil {
		return false, createErr
	}
	if createStatus != http.StatusCreated && createStatus != http.StatusUnprocessableEntity {
		return false, statusError("creating the ref", createStatus, ch)
	}
	return false, readErr
}
