package classes

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Limits of a class package. A package travels whole in the Guild database,
// in the replication log, whose frames are 1 MiB at most
// (docs/arquitetura/persistencia.md), so it is small.
const (
	MaxPackageFiles = 64
	MaxPackageBytes = 512 << 10
	MaxFileBytes    = 256 << 10
)

// ClassScope is where a class lives (docs/classes/classes.md#escopos).
type ClassScope string

// The scopes. A name is unique among the scopes visible from a table.
const (
	ClassGlobal    ClassScope = "global"
	ClassWorkspace ClassScope = "workspace"
	ClassTable     ClassScope = "table"
)

// Valid reports whether s is one of the scopes.
func (s ClassScope) Valid() bool { return s == ClassGlobal || s == ClassWorkspace || s == ClassTable }

// File is one file of a class package. Path is relative to the class
// folder, in slash form.
type File struct {
	Path string
	Data []byte
}

// Files is the content of a class package, sorted by path. The hash of the
// package is the hash of its files (not of the folder's metadata), so the
// same content has the same hash on both systems.
type Files []File

// ErrPackage is a class folder the engine refuses to read: a link, a file
// of another kind, a name that does not travel between systems, or a
// package over the limits.
var ErrPackage = errors.New("class package refused")

// Get returns the content of a file.
func (f Files) Get(path string) ([]byte, bool) {
	if i, ok := slices.BinarySearchFunc(f, path, func(x File, p string) int { return strings.Compare(x.Path, p) }); ok {
		return f[i].Data, true
	}
	return nil, false
}

// Under lists the files under a folder of the package ("hooks").
func (f Files) Under(dir string) []File {
	var out []File
	for _, x := range f {
		if strings.HasPrefix(x.Path, dir+"/") {
			out = append(out, x)
		}
	}
	return out
}

// Hash is the hash of the package: sha256 over the paths and contents,
// each with its length so no two packages share a stream.
func (f Files) Hash() string {
	h := sha256.New()
	var n [8]byte
	put := func(b []byte) {
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
	}
	for _, x := range f {
		put([]byte(x.Path))
		put(x.Data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// NewFiles checks and sorts a set of files.
func NewFiles(files []File) (Files, error) {
	out := slices.Clone(files)
	slices.SortFunc(out, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	total := 0
	for i, x := range out {
		if err := checkPackagePath(x.Path); err != nil {
			return nil, err
		}
		if i > 0 && out[i-1].Path == x.Path {
			return nil, fmt.Errorf("%w: %q twice", ErrPackage, x.Path)
		}
		if len(x.Data) > MaxFileBytes {
			return nil, fmt.Errorf("%w: %s has %d bytes; the limit is %d", ErrPackage, x.Path, len(x.Data), MaxFileBytes)
		}
		total += len(x.Data)
	}
	switch {
	case len(out) > MaxPackageFiles:
		return nil, fmt.Errorf("%w: %d files; the limit is %d", ErrPackage, len(out), MaxPackageFiles)
	case total > MaxPackageBytes:
		return nil, fmt.Errorf("%w: %d bytes; the limit is %d", ErrPackage, total, MaxPackageBytes)
	}
	return out, nil
}

// checkPackagePath refuses a path that is not the same file on both
// systems: empty, dot or dotdot segments, backslashes, colons (alternate
// streams, drive letters), control characters, and a trailing dot or space,
// which Windows ignores.
func checkPackagePath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: invalid path %q", ErrPackage, p)
	}
	for _, seg := range strings.Split(p, "/") {
		switch {
		case seg == "" || seg == "." || seg == "..":
			return fmt.Errorf("%w: invalid path %q", ErrPackage, p)
		case strings.ContainsAny(seg, `\:*?"<>|`) || strings.IndexFunc(seg, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0:
			return fmt.Errorf("%w: %q has a character that does not travel between systems", ErrPackage, p)
		case strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " "):
			return fmt.Errorf("%w: %q ends in a dot or a space", ErrPackage, p)
		}
	}
	return nil
}

// ReadDir reads the folder of a class from disk. Links and files that are
// not regular are refused (a link in a package could carry the reading
// outside it), and so are the limits of a package. A folder that does not
// exist is fs.ErrNotExist.
func ReadDir(dir string) (Files, error) {
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a folder", ErrPackage, dir)
	}
	var files []File
	total := 0
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !d.Type().IsRegular() {
			return fmt.Errorf("%w: %s is not a regular file (links and special files are refused)", ErrPackage, rel)
		}
		if len(files) >= MaxPackageFiles {
			return fmt.Errorf("%w: more than %d files", ErrPackage, MaxPackageFiles)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > MaxFileBytes || int64(total)+info.Size() > MaxPackageBytes {
			return fmt.Errorf("%w: %s passes the size limit of a package (%d bytes in all, %d per file)", ErrPackage, rel, MaxPackageBytes, MaxFileBytes)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		total += len(data)
		files = append(files, File{Path: rel, Data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return NewFiles(files)
}

// WriteDir materializes the package as the folder dir, replacing what is
// there: it is written next to it and renamed, so a reader sees the old
// folder or the new one. Files are 0644 and folders 0755; the scripts of
// hooks/ are made executable by whoever materializes them on a machine.
func WriteDir(dir string, f Files) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+"-new-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, x := range f {
		p := filepath.Join(tmp, filepath.FromSlash(x.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, x.Data, 0o644); err != nil {
			return err
		}
	}
	old := ""
	if _, err := os.Lstat(dir); err == nil {
		old = dir + ".old"
		_ = os.RemoveAll(old)
		if err := os.Rename(dir, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		if old != "" {
			_ = os.Rename(old, dir) // put the old one back
		}
		return err
	}
	if old != "" {
		_ = os.RemoveAll(old)
	}
	return nil
}
