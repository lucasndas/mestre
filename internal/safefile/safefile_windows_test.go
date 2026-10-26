package safefile_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/mestre-ia/mestre/internal/safefile"
)

// junction makes link a junction (a mount point reparse point) to target, as
// `mklink /J` does. Unlike a symbolic link it takes no privilege, so the
// account of the agents can make one.
func junction(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Mkdir(link, 0o755); err != nil {
		t.Fatal(err)
	}
	subst, err := windows.UTF16FromString(`\??\` + target)
	if err != nil {
		t.Fatal(err)
	}
	printName, err := windows.UTF16FromString(target)
	if err != nil {
		t.Fatal(err)
	}
	// REPARSE_DATA_BUFFER of IO_REPARSE_TAG_MOUNT_POINT: the tag, the data
	// length, a reserved word, then the offsets and lengths of the two names
	// (bytes, without the terminating NUL) and the names with their NULs.
	names := append(subst, printName...)
	buf := make([]byte, 16+2*len(names))
	put16 := func(off int, v int) { buf[off], buf[off+1] = byte(v), byte(v>>8) }
	put32 := func(off int, v uint32) {
		for i := range 4 {
			buf[off+i] = byte(v >> (8 * i))
		}
	}
	put32(0, windows.IO_REPARSE_TAG_MOUNT_POINT)
	put16(4, 8+2*len(names))
	put16(8, 0)
	put16(10, 2*(len(subst)-1))
	put16(12, 2*len(subst))
	put16(14, 2*(len(printName)-1))
	for i, c := range names {
		put16(16+2*i, int(c))
	}
	p, _ := windows.UTF16PtrFromString(link)
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var n uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, (*byte)(unsafe.Pointer(&buf[0])), uint32(len(buf)), nil, 0, &n, nil); err != nil {
		t.Fatalf("set the junction: %v", err)
	}
}

// CA-NATIVO-10
//
// An agent on Windows puts a junction to an engine folder in place of a
// folder of the path (it needs no privilege for that): nothing is read from
// it or written into it.
func TestJunctionInPlaceOfAFolder(t *testing.T) {
	t.Parallel()
	secret, content := secretFile(t)
	wt := t.TempDir()
	junction(t, filepath.Dir(secret), filepath.Join(wt, ".mestre"))
	inside := filepath.Join(wt, ".mestre", filepath.Base(secret))
	if b, err := os.ReadFile(inside); err != nil || string(b) != string(content) {
		t.Fatalf("the junction does not work: %q, %v", b, err)
	}
	if _, _, err := safefile.Read(inside, 0); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("read through a junction = %v, want ErrLink", err)
	}
	if _, err := safefile.Stat(inside); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("stat through a junction = %v, want ErrLink", err)
	}
	if err := safefile.Write(inside, []byte("x"), safefile.Options{}); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("write through a junction = %v, want ErrLink", err)
	}
	if err := safefile.Write(filepath.Join(wt, ".mestre", "new", "f"), []byte("x"), safefile.Options{}); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("write that creates folders through a junction = %v, want ErrLink", err)
	}
	checkSecret(t, secret, content)
	if _, err := os.Stat(filepath.Join(filepath.Dir(secret), "new")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a folder was created in the engine's folder: %v", err)
	}
}

// A junction in place of the file itself is a folder reparse point: it is
// refused, and the write does not go through it.
func TestJunctionInPlaceOfTheFile(t *testing.T) {
	t.Parallel()
	secret, content := secretFile(t)
	wt := t.TempDir()
	link := filepath.Join(wt, "settings.json")
	junction(t, filepath.Dir(secret), link)
	if _, _, err := safefile.Read(link, 0); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("read of a junction = %v, want ErrLink", err)
	}
	if err := safefile.Write(link, []byte("x"), safefile.Options{}); err == nil {
		t.Fatal("a write over a junction succeeded")
	}
	checkSecret(t, secret, content)
}
