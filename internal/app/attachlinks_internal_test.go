package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// An attachment that has another name (a hard link of a file the engine reads
// and the agents do not) is not copied into the tables.
func TestAHardLinkedAttachmentIsNotCopied(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the number of links is looked at on Linux")
	}
	r := newAttachRig(t)
	const rel = ".mestre/anexos/2026/10/h-1.png"
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("segredo"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(r.root, attWS, "principal", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(secret, p); err != nil {
		t.Skip("no hard links here:", err)
	}
	if err := r.copies.copyInto(t.Context(), attWS, r.citadel, []string{rel}); err == nil {
		t.Error("a hard-linked file was copied")
	}
	if present(filepath.Join(r.citadel, filepath.FromSlash(rel))) {
		t.Error("the content of a hard-linked file reached the table")
	}
}
