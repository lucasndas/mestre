package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

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
// (docs/telegram/anexos-e-transcricao.md#envio-de-arquivos): the limit of the
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
		if !filepath.IsAbs(path) && env.Cwd != "" {
			path = filepath.Join(env.Cwd, path)
		}
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
// each must exist, be a file and fit the limit
// (docs/telegram/anexos-e-transcricao.md). What travels is their names; the
// bytes follow with the attachments of the channel (milestone 5).
func checkAttachments(env Env, p *parsed) *localchan.Response {
	list, _ := p.flags["attach"].([]string)
	if len(list) == 0 {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, path := range list {
		full := path
		if !filepath.IsAbs(full) && env.Cwd != "" {
			full = filepath.Join(env.Cwd, full)
		}
		size, err := env.FileSize(full)
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
