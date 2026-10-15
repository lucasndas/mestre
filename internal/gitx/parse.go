package gitx

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// ParseVersion reads the output of `git version` ("git version 2.43.0",
// "git version 2.45.1.windows.1", "git version 2.39.3 (Apple Git-145)").
func ParseVersion(out string) (Version, error) {
	out = strings.TrimSpace(out)
	m := versionRe.FindStringSubmatch(out)
	if m == nil || !strings.HasPrefix(out, "git version") {
		return Version{}, fmt.Errorf("gitx: cannot read git version from %q", out)
	}
	v := Version{Raw: out}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		v.Patch, _ = strconv.Atoi(m[3])
	}
	return v, nil
}

// CheckVersion returns ErrGitTooOld, with the versions in the message, when
// v is older than least.
func CheckVersion(v, least Version) error {
	if v.Less(least) {
		return fmt.Errorf("%w: found %s, need %s or newer (docs/decisoes/0001-stack-go.md)", ErrGitTooOld, v, least)
	}
	return nil
}

// ParseStatus parses `git status --porcelain=v1 -z`.
func ParseStatus(out []byte) []StatusEntry {
	var entries []StatusEntry
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		e := StatusEntry{Index: f[0], Worktree: f[1], Path: f[3:]}
		if e.Index == 'R' || e.Index == 'C' || e.Worktree == 'R' || e.Worktree == 'C' {
			if i+1 < len(fields) {
				i++
				e.OrigPath = fields[i]
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// ParseNameStatus parses the name-status listing of diff-tree -z without
// renames: pairs of a status and a path, sorted by path.
func ParseNameStatus(out []byte) []FileChange {
	var changes []FileChange
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == "" {
			continue
		}
		changes = append(changes, FileChange{Status: fields[i][0], Path: fields[i+1]})
	}
	slices.SortFunc(changes, func(a, b FileChange) int { return strings.Compare(a.Path, b.Path) })
	return changes
}

// ParseWorktrees parses `git worktree list --porcelain`.
func ParseWorktrees(out []byte) []Worktree {
	var list []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil {
			list = append(list, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			flush()
			cur = &Worktree{Path: filepath.FromSlash(val)}
		case "HEAD":
			if cur != nil && strings.Trim(val, "0") != "" {
				cur.Head = Hash(val)
			}
		case "branch":
			if cur != nil {
				cur.Branch = strings.TrimPrefix(val, "refs/heads/")
			}
		case "bare":
			if cur != nil {
				cur.Bare = true
			}
		case "detached":
			if cur != nil {
				cur.Detached = true
			}
		}
	}
	flush()
	return list
}

// ParseRefs parses lines "<full ref name> <hash>".
func ParseRefs(out []byte) []Ref {
	var refs []Ref
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		name, hash, ok := strings.Cut(line, " ")
		if !ok || name == "" {
			continue
		}
		refs = append(refs, Ref{Name: name, Hash: Hash(hash)})
	}
	return refs
}

// logFormat is the `git log -z --format` of Repo.Log: fields separated by
// 0x1f, records by NUL.
const logFormat = "%H%x1f%P%x1f%an%x1f%ae%x1f%at%x1f%cn%x1f%ce%x1f%ct%x1f%B"

// ParseLog parses the output of `git log -z --format=logFormat`.
func ParseLog(out []byte) ([]CommitInfo, error) {
	var list []CommitInfo
	for rec := range strings.SplitSeq(string(out), "\x00") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, "\x1f", 9)
		if len(f) != 9 {
			return nil, fmt.Errorf("gitx: unexpected git log record %q", rec)
		}
		at, err1 := strconv.ParseInt(f[4], 10, 64)
		ct, err2 := strconv.ParseInt(f[7], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("gitx: unexpected git log dates in %q", rec)
		}
		c := CommitInfo{
			Hash:      Hash(f[0]),
			Author:    Signature{Name: f[2], Email: f[3], When: time.Unix(at, 0).UTC()},
			Committer: Signature{Name: f[5], Email: f[6], When: time.Unix(ct, 0).UTC()},
			Message:   strings.TrimRight(f[8], "\n"),
		}
		for p := range strings.FieldsSeq(f[1]) {
			c.Parents = append(c.Parents, Hash(p))
		}
		list = append(list, c)
	}
	return list, nil
}

// ReadBundle reads the header of a bundle file (format v2 or v3) without
// starting git. The fake git writes the same header.
func ReadBundle(file string) (BundleInfo, error) {
	f, err := os.Open(file)
	if err != nil {
		return BundleInfo{}, fmt.Errorf("open bundle: %w", err)
	}
	defer f.Close()
	return ParseBundleHeader(f)
}

// ParseBundleHeader parses the text header of a bundle: a signature line,
// optional "@capability" lines, "-<hash> <comment>" prerequisites and
// "<hash> <ref>" refs, ended by an empty line.
func ParseBundleHeader(r io.Reader) (BundleInfo, error) {
	br := bufio.NewReader(r)
	sig, err := br.ReadString('\n')
	sig = strings.TrimRight(sig, "\r\n")
	if err != nil || (sig != "# v2 git bundle" && sig != "# v3 git bundle") {
		return BundleInfo{}, fmt.Errorf("%w: missing bundle signature", ErrInvalidBundle)
	}
	var info BundleInfo
	for {
		line, err := br.ReadString('\n')
		if err != nil && line == "" {
			return BundleInfo{}, fmt.Errorf("%w: truncated header", ErrInvalidBundle)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			return info, nil
		case line[0] == '@':
			// capability (object-format, filter): not needed here
		case line[0] == '-':
			h, _, _ := strings.Cut(line[1:], " ")
			info.Prerequisites = append(info.Prerequisites, Hash(h))
		default:
			h, name, ok := strings.Cut(line, " ")
			if !ok {
				return BundleInfo{}, fmt.Errorf("%w: bad ref line %q", ErrInvalidBundle, line)
			}
			info.Refs = append(info.Refs, Ref{Name: name, Hash: Hash(h)})
		}
	}
}

// Exclude blocks: the engine owns the lines between its markers in
// info/exclude and never touches the rest.
const (
	excludeBegin = "# >>> mestre:"
	excludeEnd   = "# <<< mestre:"
)

// ParseExcludeBlock returns the patterns of the named block and whether the
// block exists.
func ParseExcludeBlock(content, name string) ([]string, bool) {
	begin, end := excludeBegin+name+" >>>", excludeEnd+name+" <<<"
	var out []string
	in, found := false, false
	for line := range strings.Lines(content) {
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == begin:
			in, found = true, true
		case line == end:
			in = false
		case in:
			out = append(out, line)
		}
	}
	return out, found
}

// ApplyExcludeBlock returns content with the named block replaced by
// patterns (appended when new, removed when patterns is empty). Everything
// outside the block is kept byte for byte.
func ApplyExcludeBlock(content, name string, patterns []string) string {
	begin, end := excludeBegin+name+" >>>", excludeEnd+name+" <<<"
	var kept strings.Builder
	in := false
	for line := range strings.Lines(content) {
		trimmed := strings.TrimRight(line, "\r\n")
		switch {
		case trimmed == begin:
			in = true
		case trimmed == end && in:
			in = false
		case !in:
			kept.WriteString(line)
		}
	}
	out := kept.String()
	if len(patterns) == 0 {
		return out
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	var b strings.Builder
	b.WriteString(out)
	b.WriteString(begin + "\n")
	for _, p := range patterns {
		b.WriteString(p + "\n")
	}
	b.WriteString(end + "\n")
	return b.String()
}

// ValidateExcludeBlock rejects a block name or patterns that would break the
// markers.
func ValidateExcludeBlock(name string, patterns []string) error {
	if name == "" || strings.ContainsAny(name, " \t\r\n<>") {
		return fmt.Errorf("%w: exclude block name %q", ErrInvalidArgument, name)
	}
	for _, p := range patterns {
		if strings.ContainsAny(p, "\r\n") || strings.HasPrefix(p, "# >>> mestre:") || strings.HasPrefix(p, "# <<< mestre:") {
			return fmt.Errorf("%w: exclude pattern %q", ErrInvalidArgument, p)
		}
	}
	return nil
}

// CleanRepoPath checks and cleans a path given to Repo.ReadFile: it is
// relative to the root of the repository and written with slashes. "./a",
// "a//b" and "a/./b" are "a" and "a/b"; an absolute path, a ".." component,
// the root itself, an empty path or a line break are ErrInvalidArgument (git
// would read them relative to the folder it runs in, or fail oddly). A path
// with a trailing slash names a folder, hence ErrFileNotFound.
func CleanRepoPath(p string) (string, error) {
	if p == "" || strings.ContainsAny(p, "\x00\n") || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%w: path %q", ErrInvalidArgument, p)
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("%w: path %q", ErrInvalidArgument, p)
	}
	if strings.HasSuffix(p, "/") {
		return "", fmt.Errorf("%w: %q is a folder", ErrFileNotFound, p)
	}
	return c, nil
}

// ValidateRef rejects values that git could take for an option.
func ValidateRef(ref string) error {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, "\x00\n") {
		return fmt.Errorf("%w: ref %q", ErrInvalidArgument, ref)
	}
	return nil
}

// ParseRevs splits revs such as "main" or "<base>..main" into the base ("" if
// none) and the tip.
func ParseRevs(rev string) (base, tip string, err error) {
	if err := ValidateRef(rev); err != nil {
		return "", "", err
	}
	if b, t, ok := strings.Cut(rev, ".."); ok {
		if t == "" || strings.Contains(t, "..") || strings.HasPrefix(t, ".") {
			return "", "", fmt.Errorf("%w: revision range %q", ErrInvalidArgument, rev)
		}
		return b, t, nil
	}
	return "", rev, nil
}
