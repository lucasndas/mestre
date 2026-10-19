// Package countfs is a file system that counts what is read from it, so a
// test can bound the work of a check by the reads it makes instead of by the
// clock, which the load of the machine moves (docs/qualidade/testes.md).
package countfs

import (
	"errors"
	"io/fs"
	"sync"
)

// FS counts the opens of each path and the bytes read. It hides the optional
// interfaces of the file system underneath, so every read goes through Open.
type FS struct {
	fsys  fs.FS
	mu    sync.Mutex
	opens map[string]int
	bytes int64
}

// New wraps fsys.
func New(fsys fs.FS) *FS { return &FS{fsys: fsys, opens: map[string]int{}} }

// Open implements fs.FS.
func (c *FS) Open(name string) (fs.File, error) {
	f, err := c.fsys.Open(name)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.opens[name]++
	c.mu.Unlock()
	return &file{File: f, c: c}, nil
}

// Opens is the number of opens and of distinct paths opened.
func (c *FS) Opens() (opens, paths int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range c.opens {
		opens += n
	}
	return opens, len(c.opens)
}

// Bytes is the number of bytes read.
func (c *FS) Bytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// Size is the total size of the files of fsys under dir.
func Size(fsys fs.FS, dir string) int64 {
	var size int64
	_ = fs.WalkDir(fsys, dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return size
}

type file struct {
	fs.File
	c *FS
}

func (f *file) Read(p []byte) (int, error) {
	n, err := f.File.Read(p)
	f.c.mu.Lock()
	f.c.bytes += int64(n)
	f.c.mu.Unlock()
	return n, err
}

func (f *file) ReadDir(n int) ([]fs.DirEntry, error) {
	d, ok := f.File.(fs.ReadDirFile)
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Err: errors.ErrUnsupported}
	}
	return d.ReadDir(n)
}
