package github

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// codeOwnersPaths are the places GitHub looks for the file, in its order.
var codeOwnersPaths = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

// CodeOwners finds who the repository requires to review a PR: the owners of the
// files it changes, when the main branch asks for a code owner review. It
// understands the usual patterns of the file; one it does not understand, a
// team, an e-mail or a missing file is an error and the decision goes to a
// person, never to "nobody is required".
type CodeOwners struct {
	// Client reads the rules, the file of owners and the files of the PR.
	Client interface {
		ReadRules(ctx context.Context, repo, branch string) (ReviewRules, error)
		FileText(ctx context.Context, repo, ref, path string) (string, bool, error)
		PullFiles(ctx context.Context, repo string, n int) ([]PullFile, error)
		PullData(ctx context.Context, repo string, n int) (PullData, error)
	}
}

// Required returns the logins, without the @, of the owners of the changed files
// of the PR; none when the branch does not ask for a code owner review.
func (c CodeOwners) Required(ctx context.Context, repo string, n int) ([]string, error) {
	pr, err := c.Client.PullData(ctx, repo, n)
	if err != nil {
		return nil, err
	}
	rules, err := c.Client.ReadRules(ctx, repo, pr.BaseRef)
	if err != nil {
		return nil, err
	}
	if !rules.CodeOwners {
		return nil, nil
	}
	var text string
	for _, p := range codeOwnersPaths {
		t, ok, err := c.Client.FileText(ctx, repo, pr.BaseRef, p)
		if err != nil {
			return nil, err
		}
		if ok {
			text = t
			break
		}
	}
	if text == "" {
		return nil, fmt.Errorf("the branch asks for a code owner review and the repository has no CODEOWNERS file")
	}
	entries, err := parseCodeOwners(text)
	if err != nil {
		return nil, err
	}
	files, err := c.Client.PullFiles(ctx, repo, n)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	add := func(owners []string) {
		for _, o := range owners {
			if !seen[strings.ToLower(o)] {
				seen[strings.ToLower(o)] = true
				out = append(out, o)
			}
		}
	}
	for _, f := range files {
		paths := []string{f.Path}
		if f.PreviousPath != "" {
			paths = append(paths, f.PreviousPath)
		}
		for _, p := range paths {
			if owners, ok := ownersOf(entries, p); ok {
				add(owners)
			}
		}
	}
	return out, nil
}

type ownerEntry struct {
	re     *regexp.Regexp
	owners []string
}

func ownersOf(entries []ownerEntry, path string) ([]string, bool) {
	for i := len(entries) - 1; i >= 0; i-- { // the last pattern that matches wins
		if entries[i].re.MatchString(path) {
			return entries[i].owners, true
		}
	}
	return nil, false
}

// parseCodeOwners reads the file. A pattern that is negated or has a class of
// characters, and an owner that is not a user, are errors.
func parseCodeOwners(text string) ([]ownerEntry, error) {
	var out []ownerEntry
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if h := strings.Index(line, " #"); h >= 0 {
			line = strings.TrimSpace(line[:h])
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		pattern, owners := fields[0], fields[1:]
		if strings.ContainsAny(pattern, "![]\\") {
			return nil, fmt.Errorf("CODEOWNERS line %d: the pattern %q is not understood", i+1, pattern)
		}
		var logins []string
		for _, o := range owners {
			if !strings.HasPrefix(o, "@") || strings.Contains(o, "/") {
				return nil, fmt.Errorf("CODEOWNERS line %d: the owner %q is not a user", i+1, o)
			}
			logins = append(logins, strings.TrimPrefix(o, "@"))
		}
		re, err := ownerPattern(pattern)
		if err != nil {
			return nil, fmt.Errorf("CODEOWNERS line %d: %w", i+1, err)
		}
		out = append(out, ownerEntry{re: re, owners: logins})
	}
	return out, nil
}

// ownerPattern turns a pattern of the file into a regular expression over the
// path: anchored at the root when it starts with a slash or has a slash inside,
// at any depth otherwise; a trailing slash is a directory and all that is under it.
func ownerPattern(p string) (*regexp.Regexp, error) {
	dir := strings.HasSuffix(p, "/")
	p = strings.TrimSuffix(p, "/")
	anchored := strings.HasPrefix(p, "/") || strings.Contains(strings.TrimPrefix(p, "/"), "/")
	p = strings.TrimPrefix(p, "/")
	var b strings.Builder
	b.WriteString("^")
	if !anchored {
		b.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(p); i++ {
		switch {
		case strings.HasPrefix(p[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(p[i:], "/**"):
			b.WriteString("/.*")
			i += 2
		case p[i] == '*':
			b.WriteString("[^/]*")
		case p[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(p[i])))
		}
	}
	if dir || !strings.ContainsAny(p, "*?") {
		b.WriteString("(?:/.*)?")
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}
