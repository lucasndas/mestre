package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucasndas/mestre/internal/localchan"
)

// The engine never opens a path that an agent names: when a command takes a
// file (`--file`, `--input-file`, `--attach`) the CLI, which runs as the
// agent, reads it and sends the text (docs/cli/comunicacao.md, rule 5 of
// docs/cli/convencoes.md#regras-de-segurança-da-cli).

// FileAs says where the text of a file goes in the request.
type FileAs int

// Where the text of a file goes.
const (
	// FileToFlag puts the text in the flag Into, which only the CLI sets.
	FileToFlag FileAs = iota
	// FileAsArg is the last positional argument of the command (the content
	// of a scroll); giving it and the argument is a usage error.
	FileAsArg
	// FileAsTree reads a folder (the package of a class): the flag Into gets
	// a map of the relative path to the text of each file. The folder must be
	// inside the MESTRE_TMP of the character (from_outside_tmp otherwise).
	FileAsTree
	// FileJoinArg joins the text to the first positional argument after a
	// blank line, or is that argument when the command has none (the summary
	// of a `done`); Into (when set) is a flag set to "true" to tell the
	// engine the text came from a file, and so may be as long as a file.
	FileJoinArg
)

// FileFlag is a flag of a command whose value is a path.
type FileFlag struct {
	// Flag is the flag the person writes (`file`); Into the flag of the
	// engine that gets the text (see FileAs).
	Flag, Into string
	As         FileAs
	// Max is the largest file, in bytes.
	Max int64
	// Stdin lets the path be `-`, the standard input.
	Stdin bool
}

// MaxAttach is the largest file `reply --attach` sends
// (docs/telegram/anexos-e-transcricao.md#envio-de-arquivos-mestre-reply---attach): the limit of the
// Bot API for a document.
const MaxAttach = 50 << 20

// readInput reads the file of a flag, with the errors of the conventions: a
// file that does not exist is not_found (4), one over the limit too_large (8).
func readInput(env Env, ff FileFlag, path string) ([]byte, *localchan.Response) {
	fail := func(code, msg, hint string) ([]byte, *localchan.Response) {
		r := localchan.Fail(code, msg, hint)
		return nil, &r
	}
	var data []byte
	var err error
	if path == "-" {
		if !ff.Stdin {
			return fail(localchan.CodeUsage, fmt.Sprintf("--%s does not take `-`", ff.Flag), "give a path")
		}
		data, err = io.ReadAll(io.LimitReader(env.Stdin, ff.Max+1))
	} else {
		path = resolvePath(env.Cwd, path)
		data, err = env.ReadFile(path, ff.Max+1)
	}
	switch {
	case err != nil && errors.Is(err, fs.ErrNotExist):
		return fail(localchan.CodeNotFound, fmt.Sprintf("the file %q does not exist", path), "check the path")
	case err != nil:
		return fail(localchan.CodeDenied, fmt.Sprintf("the file could not be read: %v", err), "check the path and its permissions")
	case int64(len(data)) > ff.Max:
		return fail(localchan.CodeTooLarge, fmt.Sprintf("the file is over %d KiB", ff.Max>>10), "send a shorter text")
	case bytes.IndexByte(data, 0) >= 0:
		return fail(localchan.CodeUsage, "the file is not text", "only text goes in the request")
	}
	return data, nil
}

// resolvePath makes a path relative to the folder of the caller; a rooted path
// stays as it is.
func resolvePath(cwd, path string) string {
	if filepath.IsAbs(path) || strings.HasPrefix(path, "/") || cwd == "" {
		return path
	}
	return filepath.Join(cwd, path)
}

// defaultReadFile reads at most max bytes of a file.
func defaultReadFile(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max))
}

func defaultFileSize(path string) (int64, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if !st.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a file", path)
	}
	return st.Size(), nil
}

// readFiles resolves the files of the flags of a command into the request,
// then checks the number of arguments again, since a file may be one.
func readFiles(env Env, p *parsed) *localchan.Response {
	for _, ff := range p.spec.Files {
		v, ok := p.flags[ff.Flag].(string)
		if !ok {
			continue
		}
		delete(p.flags, ff.Flag)
		if ff.As == FileAsTree {
			tree, refused := readTree(env, ff, v)
			if refused != nil {
				return refused
			}
			p.flags[ff.Into] = tree
			continue
		}
		usage := func(msg string) *localchan.Response {
			r := localchan.Fail(localchan.CodeUsage, msg, "usage: "+p.spec.Usage)
			return &r
		}
		data, refused := readInput(env, ff, v)
		if refused != nil {
			return refused
		}
		text := string(data)
		switch ff.As {
		case FileToFlag:
			if _, has := p.flags[ff.Into]; has {
				return usage(fmt.Sprintf("give --%s or its text, not both", ff.Flag))
			}
			p.flags[ff.Into] = text
		case FileAsArg:
			if len(p.args) >= p.spec.MaxArgs {
				return usage(fmt.Sprintf("give the content or --%s, not both", ff.Flag))
			}
			p.args = append(p.args, text)
		case FileJoinArg:
			if len(p.args) == 0 {
				p.args = append(p.args, text)
			} else {
				p.args[0] += "\n\n" + text
			}
			if ff.Into != "" {
				p.flags[ff.Into] = true
			}
		}
	}
	if n := len(p.args); n < p.spec.MinArgs || (p.spec.MaxArgs >= 0 && n > p.spec.MaxArgs) {
		r := localchan.Fail(localchan.CodeUsage, fmt.Sprintf("wrong number of arguments for %s", p.spec.Name), "usage: "+p.spec.Usage)
		return &r
	}
	return nil
}

// checkAttachments looks at the files of `--attach` before anything is sent:
// each must exist, be inside the area of the workspace (the main clone or a
// worktree of a table; path_not_allowed otherwise), be a file and fit the
// limit (docs/telegram/anexos-e-transcricao.md). What travels is the name; the
// path never does. The refusal is the CLI's and no barrier (whoever builds the
// request by hand does not pass through it): it keeps an agent from sending a
// file of the machine by mistake.
func checkAttachments(env Env, p *parsed) *localchan.Response {
	list, _ := p.flags["attach"].([]string)
	if len(list) == 0 {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, path := range list {
		if foreignAbs(path) {
			r := localchan.Fail("path_not_allowed", fmt.Sprintf("the attachment %q is outside the main clone and the worktrees of the tables", path), "put the file in the workspace first")
			return &r
		}
		full := resolvePath(env.Cwd, path)
		real, err := env.RealPath(full)
		if err != nil && errors.Is(err, fs.ErrNotExist) {
			r := localchan.Fail(localchan.CodeNotFound, fmt.Sprintf("the attachment %q does not exist", path), "check the path")
			return &r
		}
		if err != nil {
			r := localchan.Fail(localchan.CodeDenied, fmt.Sprintf("the attachment %q could not be read: %v", path, err), "check that it is a file you may read")
			return &r
		}
		if !attachAllowed(env, real) {
			r := localchan.Fail("path_not_allowed", fmt.Sprintf("the attachment %q is outside the main clone and the worktrees of the tables", path),
				"put the file in the workspace first; .git and .mestre/agents are never sent")
			return &r
		}
		size, err := env.FileSize(real)
		switch {
		case err != nil && errors.Is(err, fs.ErrNotExist):
			r := localchan.Fail(localchan.CodeNotFound, fmt.Sprintf("the attachment %q does not exist", path), "check the path")
			return &r
		case err != nil:
			r := localchan.Fail(localchan.CodeDenied, fmt.Sprintf("the attachment %q could not be read: %v", path, err), "check that it is a file you may read")
			return &r
		case size > MaxAttach:
			r := localchan.Fail(localchan.CodeTooLarge, fmt.Sprintf("the attachment %q is over %d MB", path, MaxAttach>>20), "send a smaller file")
			return &r
		}
		names = append(names, filepath.Base(path))
	}
	p.flags["attach"] = names
	return nil
}

// foreignAbs says a path is absolute in another system (a drive letter or a UNC
// path): on this one it would be read as a name inside the folder, so it is
// refused whatever system the process runs on.
func foreignAbs(p string) bool {
	if strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') && (p[0]|0x20 >= 'a' && p[0]|0x20 <= 'z')
}

// attachAllowed says a file (its path, links resolved) is in the area of the
// workspace of the caller: the main clone (<workspace>/principal) or a worktree
// of a table (<workspace>/mesas/<table>). The workspace is found from the
// worktree the process is in (worktreeRoot); a process outside any has the
// area of its own folder only. The git folder and the folders the engine
// writes for the character (.mestre/agents) are never in it.
func attachAllowed(env Env, real string) bool {
	root := worktreeRoot(env)
	if root == "" {
		return false
	}
	if r, err := env.RealPath(root); err == nil {
		root = r
	}
	areas := []string{root}
	switch {
	case filepath.Base(root) == "principal":
		areas = append(areas, filepath.Join(filepath.Dir(root), "mesas"))
	case filepath.Base(filepath.Dir(root)) == "mesas":
		areas = append(areas, filepath.Join(filepath.Dir(filepath.Dir(root)), "principal"), filepath.Dir(root))
	}
	for i, area := range areas {
		rel, err := filepath.Rel(area, real)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			continue
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		// the folder of the tables is an area only below a table: mesas/<table>/…
		if i > 0 && filepath.Base(area) == "mesas" {
			if len(parts) < 2 {
				continue
			}
			parts = parts[1:]
		}
		return !mestreOwn(parts)
	}
	return false
}

// mestreOwn says a path below a worktree is the git folder or the folder the
// engine writes for the characters.
func mestreOwn(parts []string) bool {
	for i, p := range parts {
		if p == ".git" || (p == ".mestre" && i+1 < len(parts) && parts[i+1] == "agents") {
			return true
		}
	}
	return false
}

// worktreeRoot is the nearest folder, from the current one up, that has a
// `.git` (a folder in a clone, a file in a worktree); empty when there is none.
func worktreeRoot(env Env) string {
	dir := env.Cwd
	if !filepath.IsAbs(dir) && !strings.HasPrefix(dir, "/") { // "/" is rooted for the tests on Windows too
		return ""
	}
	for {
		if env.FileExists(filepath.Join(dir, ".git")) {
			return dir
		}
		up := filepath.Dir(dir)
		if up == dir {
			return ""
		}
		dir = up
	}
}

// readTree reads the folder of a flag that takes one: it must be inside the
// MESTRE_TMP of the character, links followed (the engine does not open it; a
// package someone else wrote is not sent as if it were the character's own),
// and the whole is text up to the limit.
func readTree(env Env, ff FileFlag, path string) (map[string]any, *localchan.Response) {
	fail := func(code, msg, hint string) (map[string]any, *localchan.Response) {
		r := localchan.Fail(code, msg, hint)
		if code == "from_outside_tmp" {
			r = r.WithExit(localchan.ExitUsage)
		}
		return nil, &r
	}
	path = resolvePath(env.Cwd, path)
	tmp := env.Getenv("MESTRE_TMP")
	if tmp == "" {
		return fail("from_outside_tmp", "--"+ff.Flag+" takes a folder inside $MESTRE_TMP, which this process does not have", "run mestre debug")
	}
	real, err := env.RealPath(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fail(localchan.CodeNotFound, fmt.Sprintf("the folder %q does not exist", path), "check the path")
		}
		return fail(localchan.CodeDenied, fmt.Sprintf("the folder could not be read: %v", err), "check the path and its permissions")
	}
	root, err := env.RealPath(tmp)
	if err != nil {
		return fail("from_outside_tmp", "$MESTRE_TMP could not be resolved: "+err.Error(), "run mestre debug")
	}
	if rel, err := filepath.Rel(root, real); err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fail("from_outside_tmp", fmt.Sprintf("--%s takes a folder inside $MESTRE_TMP (%s)", ff.Flag, tmp),
			"copy the package there: `mestre class show <class> --export` leaves it in $MESTRE_TMP/classes/<class>/")
	}
	files, err := env.ReadTree(real, ff.Max)
	switch {
	case errors.Is(err, errTreeTooLarge):
		return fail(localchan.CodeTooLarge, fmt.Sprintf("the folder is over %d KiB", ff.Max>>10), "send a smaller package")
	case err != nil:
		return fail(localchan.CodeUsage, fmt.Sprintf("the folder %q is not a package: %v", path, err), "only text files, no links, go in a package")
	}
	out := make(map[string]any, len(files))
	for p, text := range files {
		out[p] = text
	}
	return out, nil
}

var errTreeTooLarge = errors.New("the folder is too large")

// defaultReadTree reads the regular files under dir, at most max bytes in all;
// a link or a file of another kind refuses the whole folder.
func defaultReadTree(dir string, max int64) (map[string]string, error) {
	out := map[string]string{}
	var total int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", p)
		}
		data, err := defaultReadFile(p, max-total+1)
		if err != nil {
			return err
		}
		if total += int64(len(data)); total > max {
			return errTreeTooLarge
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return fmt.Errorf("%s is not text", p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	return out, err
}
