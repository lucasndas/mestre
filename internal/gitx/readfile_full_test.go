//go:build full

package gitx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mestre-ia/mestre/internal/gitx"
)

// A folder (a tree) at the path is not a file: ReadFile answers
// ErrFileNotFound, as for a missing path, instead of the failure of the tool.
func TestRealReadFileOfAFolderIsNotFound(t *testing.T) {
	t.Parallel()
	r := initRepo(t, newReal(gitx.Options{}))
	writeFile(t, r, "a/f", "x\n")
	commitAll(t, r, "chore: a")
	for _, p := range []string{"a", "./a", "a/"} {
		if _, err := r.ReadFile(context.Background(), "HEAD", p); !errors.Is(err, gitx.ErrFileNotFound) {
			t.Errorf("ReadFile(%q) = %v; want ErrFileNotFound", p, err)
		}
	}
	if b, err := r.ReadFile(context.Background(), "HEAD", "./a//f"); err != nil || string(b) != "x\n" {
		t.Errorf("ReadFile of ./a//f = %q, %v; want the file", b, err)
	}
}
