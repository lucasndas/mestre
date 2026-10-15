package safefile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// heldDir is a folder path whose components are each held by a handle
// opened without FILE_SHARE_DELETE: while they are open no component can be
// renamed, deleted or replaced by a junction, so the path resolves to the
// folders that were checked.
type heldDir struct {
	path    string
	handles []windows.Handle
}

func (h *heldDir) close() {
	for _, x := range h.handles {
		_ = windows.CloseHandle(x)
	}
	h.handles = nil
}

// openDir holds every component of dir under its volume root, refusing a
// reparse point (a junction, a symbolic link, a mount point) as ErrLink;
// with create, a missing one is made.
func openDir(dir string, create bool) (*heldDir, error) {
	root, names := components(dir)
	h := &heldDir{path: root}
	cur := root
	for _, name := range names {
		cur = filepath.Join(cur, name)
		handle, err := holdDir(cur, create)
		if err != nil {
			h.close()
			return nil, err
		}
		h.handles = append(h.handles, handle)
	}
	h.path = cur
	return h, nil
}

func holdDir(path string, create bool) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	for range 2 {
		h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if (errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND)) && create {
			if err := windows.CreateDirectory(p, nil); err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
				return 0, &os.PathError{Op: "mkdir", Path: path, Err: err}
			}
			continue
		}
		if err != nil {
			return 0, &os.PathError{Op: "open", Path: path, Err: err}
		}
		var fi windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(h, &fi); err != nil {
			_ = windows.CloseHandle(h)
			return 0, &os.PathError{Op: "stat", Path: path, Err: err}
		}
		switch {
		case fi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0:
			_ = windows.CloseHandle(h)
			return 0, fmt.Errorf("%w: %s", ErrLink, path)
		case fi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0:
			_ = windows.CloseHandle(h)
			return 0, &os.PathError{Op: "open", Path: path, Err: windows.ERROR_DIRECTORY}
		}
		return h, nil
	}
	return 0, &os.PathError{Op: "mkdir", Path: path, Err: windows.ERROR_PATH_NOT_FOUND}
}

func infoOf(fi *windows.ByHandleFileInformation) Info {
	return Info{
		Exists:  true,
		Size:    int64(fi.FileSizeHigh)<<32 | int64(fi.FileSizeLow),
		ModTime: time.Unix(0, fi.LastWriteTime.Nanoseconds()),
		Perm:    0o666,
	}
}

func notFound(err error) bool {
	return errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}

// openFile opens the file itself, never what a reparse point names, and
// refuses one that is not a regular file on disk.
func openFile(path string, access uint32) (windows.Handle, Info, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, Info{}, err
	}
	h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, Info{}, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fi); err != nil {
		_ = windows.CloseHandle(h)
		return 0, Info{}, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	if fi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(h)
		return 0, Info{}, fmt.Errorf("%w: %s", ErrLink, path)
	}
	if t, _ := windows.GetFileType(h); fi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 || t != windows.FILE_TYPE_DISK {
		_ = windows.CloseHandle(h)
		return 0, Info{}, fmt.Errorf("%w: %s", ErrNotRegular, path)
	}
	return h, infoOf(&fi), nil
}

func open(dir, base string) (*os.File, Info, error) {
	d, err := openDir(dir, false)
	if err != nil {
		return nil, Info{}, err
	}
	defer d.close()
	path := filepath.Join(d.path, base)
	h, info, err := openFile(path, windows.GENERIC_READ)
	if err != nil {
		return nil, Info{}, err
	}
	return os.NewFile(uintptr(h), path), info, nil
}

func statIn(d *heldDir, base string) (Info, error) {
	h, info, err := openFile(filepath.Join(d.path, base), windows.FILE_READ_ATTRIBUTES)
	if notFound(err) {
		return Info{}, nil
	}
	if err != nil {
		return Info{}, err
	}
	_ = windows.CloseHandle(h)
	return info, nil
}

func stat(dir, base string) (Info, error) {
	d, err := openDir(dir, false)
	if notFound(err) {
		return Info{}, nil
	}
	if err != nil {
		return Info{}, err
	}
	defer d.close()
	return statIn(d, base)
}

func remove(dir, base string, guard func(Info) error) error {
	d, err := openDir(dir, false)
	if notFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer d.close()
	cur, err := statIn(d, base)
	if err != nil {
		return err
	}
	if !cur.Exists {
		return nil
	}
	if guard != nil {
		if err := guard(cur); err != nil {
			return err
		}
	}
	// The folders of the path are held, so the file is in the checked folder.
	if err := os.Remove(filepath.Join(d.path, base)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func write(dir, base string, data []byte, o Options) error {
	d, err := openDir(dir, true)
	if err != nil {
		return err
	}
	defer d.close()
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := filepath.Join(d.path, "."+base+".mestre-"+hex.EncodeToString(rnd[:]))
	p, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	// CREATE_NEW is the O_EXCL of Windows; the file takes the ACL of the
	// folder, so the mode does not apply.
	// mestre:allow nil-sa: a native file takes the ACL its folder passes on (the ACL of the workspace or profile), as os.Create would give it
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return &os.PathError{Op: "create", Path: tmp, Err: err}
	}
	f := os.NewFile(uintptr(h), tmp)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if o.Sync {
		if err := f.Sync(); err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	target := filepath.Join(d.path, base)
	if o.Guard != nil {
		cur, err := statIn(d, base)
		if err == nil {
			err = o.Guard(cur)
		}
		if err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	// MoveFileEx replaces a reparse point in place of the file itself; the
	// folders of the path are held, so the target is in the checked folder.
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// mkdirAll makes dir and its missing parents, refusing a link on the way.
func mkdirAll(dir string) error {
	d, err := openDir(dir, true)
	if err != nil {
		return err
	}
	d.close()
	return nil
}
