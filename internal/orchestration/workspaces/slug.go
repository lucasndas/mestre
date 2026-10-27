package workspaces

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Slug limits (docs/orquestracao/workspaces.md#identificador-slug).
const (
	MinSlug = 2
	MaxSlug = 32
)

var (
	// ErrInvalidRepo is a repository that is not "owner/name".
	ErrInvalidRepo = errors.New("workspaces: repository must be owner/name")
	// ErrInvalidSlug is a slug outside the format.
	ErrInvalidSlug = errors.New("workspaces: invalid slug")
	// ErrSlugTaken is a slug that another workspace has.
	ErrSlugTaken = errors.New("workspaces: slug already used")
	// ErrRepoTaken is a repository that is already a workspace.
	ErrRepoTaken = errors.New("workspaces: repository already is a workspace")
)

var (
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	nameRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	// slugRe: [a-z0-9-], 2 to 32 characters. A hyphen never starts the slug
	// (it would read as an option in the CLI and in git arguments) nor ends
	// it.
	slugRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])$`)
)

// Repo is a GitHub repository, "owner/name".
type Repo struct{ Owner, Name string }

// ParseRepo reads "owner/name".
func ParseRepo(s string) (Repo, error) {
	owner, name, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || !ownerRe.MatchString(owner) || !nameRe.MatchString(name) || name == "." || name == ".." {
		return Repo{}, fmt.Errorf("%w: %q", ErrInvalidRepo, s)
	}
	return Repo{Owner: owner, Name: name}, nil
}

// String is "owner/name".
func (r Repo) String() string { return r.Owner + "/" + r.Name }

// Key is the lower case that identifies the repository: GitHub does not tell
// "Lucas/App" from "lucas/app".
func (r Repo) Key() string { return strings.ToLower(r.String()) }

// ValidSlug reports whether s fits the format of a slug.
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// Slugify makes a slug from the name of a repository: lower case, every run
// of characters outside [a-z0-9] becomes one hyphen, no hyphen at the ends,
// at most MaxSlug characters. "Meu_App" gives "meu-app". It returns "" when
// nothing fit the format (a name of one character): the user must then
// choose the slug.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
			continue
		}
		dash = true
	}
	s := b.String()
	if len(s) > MaxSlug {
		s = strings.TrimRight(s[:MaxSlug], "-")
	}
	if !ValidSlug(s) {
		return ""
	}
	return s
}

// SuggestSlug is a free slug near base: base itself, then base-2, base-3…
// (the base is cut so the whole fits MaxSlug). taken says which are used.
func SuggestSlug(base string, taken func(string) bool) string {
	if base == "" {
		base = "ws"
	}
	if ValidSlug(base) && !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		suffix := "-" + strconv.Itoa(n)
		stem := base
		if len(stem)+len(suffix) > MaxSlug {
			stem = strings.TrimRight(stem[:MaxSlug-len(suffix)], "-")
		}
		if cand := stem + suffix; ValidSlug(cand) && !taken(cand) {
			return cand
		}
	}
}

// RepoTakenError says which workspace already has the repository.
type RepoTakenError struct{ Repo, Slug string }

func (e *RepoTakenError) Error() string { return "repositório já é a campanha " + e.Slug }

// Is makes errors.Is(err, ErrRepoTaken) true.
func (e *RepoTakenError) Is(target error) bool { return target == ErrRepoTaken }

// SlugError is a slug the creation refused, with the reason and a free slug
// to try.
type SlugError struct {
	Slug       string
	Reason     string
	Suggestion string
	kind       error // ErrInvalidSlug or ErrSlugTaken
}

func (e *SlugError) Error() string {
	msg := fmt.Sprintf("slug %q %s", e.Slug, e.Reason)
	if e.Suggestion != "" {
		msg += "; sugestão: " + e.Suggestion
	}
	return msg
}

// Is makes errors.Is(err, ErrInvalidSlug) or ErrSlugTaken true.
func (e *SlugError) Is(target error) bool { return target == e.kind }
