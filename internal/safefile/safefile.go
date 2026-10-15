// Package safefile reads and writes the files the engine keeps in folders
// the account of the agents can write: the worktrees and the account
// profiles (docs/arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes).
// An agent can put a link (on Windows, a junction or another reparse point)
// in place of any folder or file there, so a plain os.ReadFile or a rename
// would read an engine file into a file the agents read, or write over one.
//
// Every operation here walks the path one folder at a time from the volume
// root, holding each folder (a descriptor on Linux, a handle that cannot be
// renamed or deleted on Windows), and refuses a link or a reparse point in
// any component (ErrLink) and a final file that is not a regular one
// (ErrNotRegular). A read takes at most a limit of bytes (ErrTooLarge). A
// write is a temporary file created exclusively in the same held folder,
// with the mode set on the open file (never by path), synced and renamed
// over the old one: a reader sees the old content or the new one.
//
// The whole path must be free of links, also above the folder the agents
// write: the folders of the Mestre are created as real folders by the
// installer (docs/plataforma/instalacao-e-atualizacao.md), and a path the
// engine derives from them has no link.
package safefile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Mode is the mode of every native file the engine writes for the agents:
// read by the group and the others, and written by the group, because the
// CLI of the agents (running as mestre-agente) rewrites some of them in
// place (.claude.json, config.toml, settings.json). The group is the one of
// the folder (docs/classes/reforco-nativo.md#quando-as-regras-mudam). No
// native file is a secret. On Windows the mode is ignored: the file takes
// the ACL of its folder.
const Mode fs.FileMode = 0o664

// DirMode is the mode of the folders Write creates.
const DirMode fs.FileMode = 0o755

// DefaultLimit bounds a read when the caller gives no limit.
const DefaultLimit int64 = 16 << 20

// The refusals. Each is returned wrapped with the path.
var (
	// ErrLink is a link, a junction or another reparse point in a component
	// of the path, or as the file itself.
	ErrLink = errors.New("a link or a reparse point is on the path")
	// ErrNotRegular is a final file that is not a regular one (a folder, a
	// FIFO, a device, a socket).
	ErrNotRegular = errors.New("not a regular file")
	// ErrTooLarge is a file over the limit of the read.
	ErrTooLarge = errors.New("file over the size limit")
	// ErrNotFolder is a path that RemoveDir found to be a file.
	ErrNotFolder = errors.New("not a folder")
	// ErrPath is a path that is not absolute.
	ErrPath = errors.New("the path is not absolute")
)

// Refused reports whether err is one of the refusals of this package (a
// file the engine will not touch), as opposed to an ordinary error.
func Refused(err error) bool {
	return errors.Is(err, ErrLink) || errors.Is(err, ErrNotRegular) || errors.Is(err, ErrNotFolder) || errors.Is(err, ErrTooLarge) || errors.Is(err, ErrPath)
}

// Info is the state of a file at a moment.
type Info struct {
	Exists  bool
	Size    int64
	ModTime time.Time
	Perm    fs.FileMode
}

// Same reports whether two states are the same version of the file (the
// check that it was not rewritten between a read and a write).
func (i Info) Same(o Info) bool {
	return i.Exists == o.Exists && i.Size == o.Size && i.ModTime.Equal(o.ModTime)
}

// Open opens the regular file at path for reading, refusing a link in any
// component and a file that is not regular, checked on the open file itself
// (no window between the check and the use). A missing file (or folder of
// the path) is an error that wraps fs.ErrNotExist.
func Open(path string) (*os.File, Info, error) {
	dir, base, err := split(path)
	if err != nil {
		return nil, Info{}, err
	}
	f, info, err := open(dir, base)
	if err != nil {
		return nil, Info{}, wrap(path, err)
	}
	return f, info, nil
}

// Read reads the regular file at path, as Open, refusing a file over limit
// bytes (limit <= 0 is DefaultLimit).
func Read(path string, limit int64) ([]byte, Info, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	f, info, err := Open(path)
	if err != nil {
		return nil, Info{}, err
	}
	defer f.Close()
	if info.Size > limit {
		return nil, Info{}, wrap(path, fmt.Errorf("%w: %d bytes, the limit is %d", ErrTooLarge, info.Size, limit))
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, Info{}, wrap(path, err)
	}
	if int64(len(data)) > limit {
		return nil, Info{}, wrap(path, fmt.Errorf("%w: it grew past %d bytes", ErrTooLarge, limit))
	}
	return data, info, nil
}

// Stat is the state of the regular file at path: Exists is false when it is
// missing. A link or a file that is not regular is refused.
func Stat(path string) (Info, error) {
	dir, base, err := split(path)
	if err != nil {
		return Info{}, err
	}
	info, err := stat(dir, base)
	if err != nil {
		return Info{}, wrap(path, err)
	}
	return info, nil
}

// Options are the options of Write.
type Options struct {
	// Mode of the file on Linux (the group is the one of the folder); zero
	// is Mode.
	Mode fs.FileMode
	// Guard, if not nil, sees the state of the file right before the rename
	// and stops the write with its error (the file was rewritten since it
	// was read).
	Guard func(Info) error
	// Sync forces the file to disk before the rename: for a file the CLI
	// keeps state in, not for one the engine regenerates at every start.
	Sync bool
}

// Write replaces path with data whole, creating the missing folders with
// DirMode.
func Write(path string, data []byte, o Options) error {
	dir, base, err := split(path)
	if err != nil {
		return err
	}
	if o.Mode == 0 {
		o.Mode = Mode
	}
	o.Mode = o.Mode.Perm()
	if err := write(dir, base, data, o); err != nil {
		return wrap(path, err)
	}
	return nil
}

// Remove deletes the regular file at path, walking the path as Write does
// (a link on the way is ErrLink, a final link or non-regular file is
// refused and left alone). A missing file is not an error. guard, if not
// nil, sees the state of the file right before it is deleted and stops the
// removal with its error (the file is not the one that was checked).
func Remove(path string, guard func(Info) error) error {
	dir, base, err := split(path)
	if err != nil {
		return err
	}
	if err := remove(dir, base, guard); err != nil {
		return wrap(path, err)
	}
	return nil
}

// RemoveDir deletes the empty folder at path, or the link or junction that
// stands in the place of one (the link itself, never what it points at),
// walking the path as Write does. A missing one is not an error; a folder
// with something in it is an error and stays; a file is ErrNotFolder
// (Remove deletes files).
func RemoveDir(path string) error {
	dir, base, err := split(path)
	if err != nil {
		return err
	}
	if err := removeDir(dir, base); err != nil {
		return wrap(path, err)
	}
	return nil
}

// split checks that path is absolute and splits it, clean. With no link on
// the path, the lexical ".." of Clean is the real parent.
func split(path string) (dir, base string, err error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", "", fmt.Errorf("%w: %q", ErrPath, path)
	}
	dir, base = filepath.Split(filepath.Clean(path))
	if base == "" || base == "." || base == ".." {
		return "", "", fmt.Errorf("%w: %q has no file name", ErrPath, path)
	}
	return filepath.Clean(dir), base, nil
}

// components are the names of the folders of dir under its volume root.
func components(dir string) (root string, names []string) {
	vol := filepath.VolumeName(dir)
	rest := strings.TrimLeft(dir[len(vol):], `/\`)
	root = vol + string(filepath.Separator)
	if rest == "" {
		return root, nil
	}
	return root, strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == filepath.Separator })
}

type pathError struct {
	path string
	err  error
}

func (e *pathError) Error() string { return e.path + ": " + e.err.Error() }
func (e *pathError) Unwrap() error { return e.err }

func wrap(path string, err error) error {
	var pe *pathError
	if errors.As(err, &pe) {
		return err
	}
	return &pathError{path: path, err: err}
}

// MkdirAll makes the folder dir and its missing parents with DirMode,
// walking the path as Write does: a link or a reparse point on the way is
// ErrLink, and a final component that is not a folder is an error.
func MkdirAll(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%w: %s", ErrPath, dir)
	}
	if err := mkdirAll(filepath.Clean(dir)); err != nil {
		return wrap(dir, err)
	}
	return nil
}
