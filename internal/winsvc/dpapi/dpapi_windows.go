package dpapi

import (
	"errors"
	"fmt"
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Protect encrypts data with machine-scope DPAPI. label is the entropy:
// Unprotect needs the same one, so a blob of one purpose does not open as
// another. The returned blob is not secret on this machine without its ACL
// (see the package comment).
func Protect(data []byte, label string) ([]byte, error) {
	if len(data) == 0 || label == "" {
		return nil, errors.New("dpapi: protect needs data and a label")
	}
	in, err := blob(data)
	if err != nil {
		return nil, err
	}
	entropy, err := blob([]byte(label))
	if err != nil {
		return nil, err
	}
	var out windows.DataBlob
	flags := uint32(windows.CRYPTPROTECT_LOCAL_MACHINE | windows.CRYPTPROTECT_UI_FORBIDDEN)
	if err := windows.CryptProtectData(in, nil, entropy, 0, nil, flags, &out); err != nil {
		return nil, fmt.Errorf("dpapi: protect: %w", err)
	}
	return take(&out), nil
}

// Unprotect decrypts a blob made by Protect with the same label on this
// machine. The caller owns the plaintext and should zero it after use; the
// copy DPAPI allocated is zeroed before it is freed.
func Unprotect(protected []byte, label string) ([]byte, error) {
	if len(protected) == 0 || label == "" {
		return nil, errors.New("dpapi: unprotect needs a blob and a label")
	}
	in, err := blob(protected)
	if err != nil {
		return nil, err
	}
	entropy, err := blob([]byte(label))
	if err != nil {
		return nil, err
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(in, nil, entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, fmt.Errorf("dpapi: unprotect (wrong label, damaged blob or another machine): %w", err)
	}
	return take(&out), nil
}

func blob(b []byte) (*windows.DataBlob, error) {
	if uint64(len(b)) > math.MaxUint32 {
		return nil, errors.New("dpapi: data too large")
	}
	return &windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}, nil
}

// take copies a DPAPI output buffer to the Go heap, zeroes it and frees it.
func take(out *windows.DataBlob) []byte {
	if out.Data == nil {
		return nil
	}
	src := unsafe.Slice(out.Data, out.Size)
	res := make([]byte, len(src))
	copy(res, src)
	clear(src)
	_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return res
}
