package replication

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/store"
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
	// Allow, when set, says which paths of the folder travel: dir is true for a
	// folder (a folder that is not allowed is not entered; one that only leads
	// to allowed files must be), false for a file. Both machines apply it: the
	// Citadel sends nothing else and the Tower refuses nothing else, so a folder
	// that holds more than the Mestre's own files (the folder of the workspaces
	// holds the mirrors and the worktrees too) is shared only where it is meant.
	Allow func(rel string, dir bool) bool
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

// ErrPathEscapes is wrapped by the errors for a path that leaves its folder,
// by a name or by a link in the middle of it.
var ErrPathEscapes = errors.New("replication: the path leaves the folder")

// checkRef validates a reference as a name: relative, clean, with no "..", no
// names the system reserves.
func (f *Files) checkRef(ref FileRef) (FileRoot, string, error) {
	root, ok := f.roots[ref.Root]
	if !ok {
		return FileRoot{}, "", fmt.Errorf("replication: no folder %q", ref.Root)
	}
	// A backslash, a colon or a NUL would make a different path on another
	// system (on Windows a backslash is a separator and a colon an alternate
	// stream): the folders carry the names the same on both sides, and the
	// filter below sees the path as it is written.
	if strings.ContainsAny(ref.Path, "\\:\x00") || !fs.ValidPath(ref.Path) || ref.Path == "." || !filepath.IsLocal(filepath.FromSlash(ref.Path)) {
		return FileRoot{}, "", fmt.Errorf("%w: %q is not a path inside %s", ErrPathEscapes, ref.Path, ref.Root)
	}
	if root.Allow != nil && !root.Allow(ref.Path, false) {
		return FileRoot{}, "", fmt.Errorf("%w: %s/%s is not a path this folder carries", ErrPathEscapes, ref.Root, ref.Path)
	}
	return root, filepath.FromSlash(ref.Path), nil
}

// openRoot opens the folder as a root: every access through it resolves each
// component inside the folder, so a link in the middle of the path (a folder
// an agent could write to turned into a link, or on Windows a junction) cannot
// take the access elsewhere. It does not create the folder.
func (f *Files) openRoot(ref FileRef) (*os.Root, FileRoot, string, error) {
	fr, rel, err := f.checkRef(ref)
	if err != nil {
		return nil, FileRoot{}, "", err
	}
	root, err := os.OpenRoot(fr.Dir)
	if err != nil {
		return nil, FileRoot{}, "", err
	}
	return root, fr, rel, nil
}

// lstat looks at the file without following a link at the end. A link in the
// middle that leaves the folder is an error, a missing file is
// fs.ErrNotExist.
func lstat(root *os.Root, rel string, ref FileRef) (fs.FileInfo, error) {
	fi, err := root.Lstat(rel)
	switch {
	case err == nil && fi.Mode()&fs.ModeSymlink != 0:
		return nil, fmt.Errorf("%w: %s/%s is a link", ErrPathEscapes, ref.Root, ref.Path)
	case err == nil, errors.Is(err, fs.ErrNotExist):
		return fi, err
	case isEscape(err):
		return nil, fmt.Errorf("%w: %s/%s: %v", ErrPathEscapes, ref.Root, ref.Path, err)
	}
	return nil, fmt.Errorf("replication: %s/%s: %w", ref.Root, ref.Path, err)
}

// walk lists the files of a folder, in order. Links are not followed and not
// listed.
func (f *Files) walk(name string) ([]FileRef, error) {
	allow := f.roots[name].Allow
	dir := f.roots[name].Dir
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var out []FileRef
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if d.IsDir() {
			if allow != nil && !allow(p, true) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && (allow == nil || allow(p, false)) {
			out = append(out, FileRef{Root: name, Path: p})
		}
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
	log   *slog.Logger
	mu    sync.Mutex
	sent  map[string]stamp
}

func newFileSender(f *Files, log *slog.Logger) *fileSender {
	return &fileSender{files: f, log: log, sent: map[string]stamp{}}
}

// ensure makes sure the Tower has the file: it sends it unless this
// connection already did with the same size and time. A file that is not on the
// Citadel is not an error here (the record that cites it may be a deletion).
//
// A file that leaves its folder (a link in the middle of the path) is not sent,
// and one that the Tower refuses for the same reason is not sent again: both
// are logged and skipped, because a path an agent could plant must not stop the
// replication of everything else.
func (s *fileSender) ensure(ctx context.Context, link *protocol.Link, excluded bool, ref FileRef) error {
	err := s.send(ctx, link, excluded, ref)
	var pe *protocol.Error
	if errors.Is(err, ErrPathEscapes) || (errors.As(err, &pe) && pe.Code == protocol.CodeForbidden) {
		s.log.Warn("replication: a file was not sent, its path leaves the folder", "file", ref.Root+"/"+ref.Path, "err", err)
		return nil
	}
	return err
}

func (s *fileSender) send(ctx context.Context, link *protocol.Link, excluded bool, ref FileRef) error {
	root, fr, rel, err := s.files.openRoot(ref)
	if errors.Is(err, fs.ErrNotExist) {
		return nil // the folder is not there: neither is the file
	}
	if err != nil {
		return err
	}
	defer root.Close()
	if excluded && fr.NotForExcluded {
		return nil
	}
	fi, err := lstat(root, rel, ref)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !fi.Mode().IsRegular()) {
		return nil
	}
	if err != nil {
		return err
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
		f, err := root.Open(rel)
		if err != nil {
			return fmt.Errorf("replication: %s/%s: %w", ref.Root, ref.Path, err)
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return err
		}
		if !info.Mode().IsRegular() {
			_ = f.Close()
			return nil
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
	ref := FileRef{Root: m.Root, Path: m.Path}
	root, _, rel, err := r.files.openRoot(ref)
	if err != nil {
		return false
	}
	defer root.Close()
	fi, err := lstat(root, rel, ref)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != b.Size {
		return false
	}
	f, err := root.Open(rel)
	if err != nil {
		return false
	}
	defer f.Close()
	sum, err := sha256Reader(f)
	return err == nil && sum == b.SHA256
}

// Finalize puts the file in place: written next to its destination and moved
// over it, so a reader sees the old file or the new one. Every step goes
// through the root of the folder, so no link on the way can take the file out
// of it.
func (r *fileReceiver) Finalize(_ context.Context, b protocol.XferBegin, tmp string) error {
	m, err := metaOf(b.Meta)
	if err != nil {
		return protocol.NewError(protocol.CodeInvalidPayload, "file meta: %v", err)
	}
	ref := FileRef{Root: m.Root, Path: m.Path}
	fr, rel, err := r.files.checkRef(ref)
	if err != nil {
		return protocol.NewError(protocol.CodeForbidden, "%v", err)
	}
	// The folders and their modes are the installer's (some are shared with the
	// agents' group); only what is missing is made, never more open than 0750.
	if err := os.MkdirAll(fr.Dir, 0o750); err != nil {
		return protocol.NewError(protocol.CodeUnavailable, "%v", err)
	}
	root, err := os.OpenRoot(fr.Dir)
	if err != nil {
		return protocol.NewError(protocol.CodeUnavailable, "%v", err)
	}
	defer root.Close()
	dir := filepath.Dir(rel)
	if dir != "." {
		if err := root.MkdirAll(dir, 0o750); err != nil {
			return forbiddenOrUnavailable(err)
		}
	}
	if _, err := lstat(root, rel, ref); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return protocol.NewError(protocol.CodeForbidden, "%v", err)
	}
	in, err := os.Open(tmp)
	if err != nil {
		return protocol.NewError(protocol.CodeUnavailable, "%v", err)
	}
	defer in.Close()
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	name := filepath.Join(dir, "."+filepath.Base(rel)+".incoming-"+hex.EncodeToString(nonce[:]))
	out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return forbiddenOrUnavailable(err)
	}
	fail := func(err error) error {
		_ = out.Close()
		_ = root.Remove(name)
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
		_ = root.Remove(name)
		return protocol.NewError(protocol.CodeUnavailable, "%v", err)
	}
	if err := root.Rename(name, rel); err != nil {
		_ = root.Remove(name)
		return forbiddenOrUnavailable(err)
	}
	return nil
}

// isEscape says whether os.Root refused the path because it leaves the folder
// (a ".." or a link that points out; Go does not export that error).
func isEscape(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "escapes from parent") || strings.Contains(err.Error(), "symlink"))
}

// forbiddenOrUnavailable tells a refusal of the root (a path that leaves the
// folder) from a failure of the disk.
func forbiddenOrUnavailable(err error) error {
	if isEscape(err) {
		return protocol.NewError(protocol.CodeForbidden, "%v: %v", ErrPathEscapes, err)
	}
	return protocol.NewError(protocol.CodeUnavailable, "%v", err)
}

func sha256Reader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
