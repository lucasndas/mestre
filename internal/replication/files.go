package replication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
)

// The files of the Citadel (docs/guilda/replicacao.md#arquivos-e-espelhos):
// folders of the data directory that every Tower keeps a copy of (global
// classes, templates, attachments, scrolls, the Operator's captures, the
// .mestre/ folder of the main clone of each workspace). Each file travels as a
// transfer of kind file, and one that a record of the database cites is on the
// Tower before the record is. The set of folders is configuration: this
// package knows none by name.

// FileRoot is one folder that is copied to the Towers.
type FileRoot struct {
	// Name identifies the folder on the wire; the Tower has the same names.
	Name string
	// Dir is where it lives on this machine.
	Dir string
	// NotForExcluded keeps the folder away from an excluded machine (the
	// Operator's captures).
	NotForExcluded bool
	// Walk makes the Citadel send every file of the folder when a Tower
	// connects, not only the ones a record cites or a notification names.
	Walk bool
}

// Files is the set of folders of one machine.
type Files struct {
	roots map[string]FileRoot
	order []string
}

// NewFiles checks the folders: names are unique and folders are absolute.
func NewFiles(roots ...FileRoot) (*Files, error) {
	f := &Files{roots: map[string]FileRoot{}}
	for _, r := range roots {
		if r.Name == "" || strings.ContainsAny(r.Name, `/\`) {
			return nil, fmt.Errorf("replication: folder name %q is not valid", r.Name)
		}
		if !filepath.IsAbs(r.Dir) {
			return nil, fmt.Errorf("replication: folder %s: %q is not an absolute path", r.Name, r.Dir)
		}
		if _, dup := f.roots[r.Name]; dup {
			return nil, fmt.Errorf("replication: folder %s is repeated", r.Name)
		}
		f.roots[r.Name] = r
		f.order = append(f.order, r.Name)
	}
	return f, nil
}

// FileRef is a file a change of the database cites: the folder and the path
// in it, with slashes.
type FileRef struct {
	Root string
	Path string
}

// Cites says which files a change of the database cites (the path column of
// a class, an attachment, a scroll). It returns nothing for a change that
// cites none.
type Cites func(c store.Change) []FileRef

// resolve turns a reference into a path on this machine, refusing anything
// that leaves the folder: absolute paths, "..", names the system reserves, and
// a link at the end (an agent that can write there could aim it elsewhere).
func (f *Files) resolve(ref FileRef) (FileRoot, string, error) {
	root, ok := f.roots[ref.Root]
	if !ok {
		return FileRoot{}, "", fmt.Errorf("replication: no folder %q", ref.Root)
	}
	if !fs.ValidPath(ref.Path) || ref.Path == "." || !filepath.IsLocal(filepath.FromSlash(ref.Path)) {
		return FileRoot{}, "", fmt.Errorf("replication: %q is not a path inside %s", ref.Path, ref.Root)
	}
	full := filepath.Join(root.Dir, filepath.FromSlash(ref.Path))
	if fi, err := os.Lstat(full); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return FileRoot{}, "", fmt.Errorf("replication: %s/%s is a link", ref.Root, ref.Path)
	}
	return root, full, nil
}

// walk lists the files of a folder, in order.
func (f *Files) walk(name string) ([]FileRef, error) {
	root := f.roots[name]
	var out []FileRef
	err := filepath.WalkDir(root.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == root.Dir {
				return fs.SkipAll
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root.Dir, p)
		if err != nil {
			return err
		}
		out = append(out, FileRef{Root: name, Path: filepath.ToSlash(rel)})
		return nil
	})
	return out, err
}

// fileMeta is the meta of a transfer of kind file.
type fileMeta struct {
	Root string `json:"root"`
	Path string `json:"path"`
	Mode uint32 `json:"mode,omitempty"`
}

func (m fileMeta) object() protocol.Object {
	raw, _ := json.Marshal(m)
	var o protocol.Object
	_ = json.Unmarshal(raw, &o)
	return o
}

func metaOf(o protocol.Object) (fileMeta, error) {
	raw, err := json.Marshal(o)
	if err != nil {
		return fileMeta{}, err
	}
	var m fileMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return fileMeta{}, err
	}
	return m, nil
}

// stamp is what tells a file did not change since it was sent.
type stamp struct {
	size int64
	mod  time.Time
}

// fileSender is the memory of what one Tower was sent on one connection.
type fileSender struct {
	files *Files
	mu    sync.Mutex
	sent  map[string]stamp
}

func newFileSender(f *Files) *fileSender { return &fileSender{files: f, sent: map[string]stamp{}} }

// ensure makes sure the Tower has the file: it sends it unless this
// connection already did with the same size and time. A file that is not on the
// Citadel is not an error here (the record that cites it may be a deletion).
func (s *fileSender) ensure(ctx context.Context, link *protocol.Link, excluded bool, ref FileRef) error {
	root, full, err := s.files.resolve(ref)
	if err != nil {
		return err
	}
	if excluded && root.NotForExcluded {
		return nil
	}
	fi, err := os.Stat(full)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !fi.Mode().IsRegular()) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("replication: %s/%s: %w", ref.Root, ref.Path, err)
	}
	key := ref.Root + "/" + ref.Path
	st := stamp{size: fi.Size(), mod: fi.ModTime()}
	s.mu.Lock()
	same := s.sent[key] == st
	s.mu.Unlock()
	if same {
		return nil
	}
	var lastErr error
	for range 3 { // the file may change under the transfer: hash mismatch, try again
		f, err := os.Open(full)
		if err != nil {
			return fmt.Errorf("replication: %s/%s: %w", ref.Root, ref.Path, err)
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return err
		}
		st = stamp{size: info.Size(), mod: info.ModTime()}
		lastErr = link.Transfer(ctx, protocol.Transfer{
			Kind:   protocol.KindFile,
			Meta:   fileMeta{Root: ref.Root, Path: ref.Path, Mode: uint32(info.Mode().Perm())}.object(),
			Source: protocol.Source{ReaderAt: f, Size: info.Size()},
		})
		_ = f.Close()
		if lastErr == nil {
			s.mu.Lock()
			s.sent[key] = st
			s.mu.Unlock()
			return nil
		}
		var pe *protocol.Error
		if !errors.As(lastErr, &pe) || pe.Code != protocol.CodeHashMismatch {
			break
		}
	}
	return fmt.Errorf("replication: sending %s/%s: %w", ref.Root, ref.Path, lastErr)
}

// fileReceiver is the Finalizer of kind file on a Tower.
type fileReceiver struct{ files *Files }

var (
	_ protocol.Finalizer = (*fileReceiver)(nil)
	_ protocol.Skipper   = (*fileReceiver)(nil)
)

// Have says whether the file is already here with the content announced.
func (r *fileReceiver) Have(_ context.Context, b protocol.XferBegin) bool {
	m, err := metaOf(b.Meta)
	if err != nil {
		return false
	}
	_, full, err := r.files.resolve(FileRef{Root: m.Root, Path: m.Path})
	if err != nil {
		return false
	}
	fi, err := os.Stat(full)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != b.Size {
		return false
	}
	sum, err := sha256File(full)
	return err == nil && sum == b.SHA256
}

// Finalize puts the file in place: written next to its destination and moved
// over it, so a reader sees the old file or the new one.
func (r *fileReceiver) Finalize(_ context.Context, b protocol.XferBegin, tmp string) error {
	m, err := metaOf(b.Meta)
	if err != nil {
		return protocol.NewError(protocol.CodeInvalidPayload, "file meta: %v", err)
	}
	_, full, err := r.files.resolve(FileRef{Root: m.Root, Path: m.Path})
	if err != nil {
		return protocol.NewError(protocol.CodeForbidden, "%v", err)
	}
	// The folders and their modes are the installer's (some are shared with the
	// agents' group); only what is missing is made, never more open than 0750.
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return protocol.NewError(protocol.CodeUnavailable, "%v", err)
	}
	in, err := os.Open(tmp)
	if err != nil {
		return protocol.NewError(protocol.CodeUnavailable, "%v", err)
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(full), "."+path.Base(m.Path)+".incoming-*")
	if err != nil {
		return protocol.NewError(protocol.CodeUnavailable, "%v", err)
	}
	name := out.Name()
	fail := func(err error) error {
		_ = out.Close()
		_ = os.Remove(name)
		return protocol.NewError(protocol.CodeUnavailable, "%v", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		return fail(err)
	}
	mode := os.FileMode(m.Mode) & 0o770 // never open to others
	if mode == 0 {
		mode = 0o640
	}
	if err := out.Chmod(mode); err != nil && !errors.Is(err, os.ErrPermission) {
		return fail(err)
	}
	if err := out.Sync(); err != nil {
		return fail(err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(name)
		return protocol.NewError(protocol.CodeUnavailable, "%v", err)
	}
	if err := os.Rename(name, full); err != nil {
		_ = os.Remove(name)
		return protocol.NewError(protocol.CodeUnavailable, "%v", err)
	}
	return nil
}

func sha256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
