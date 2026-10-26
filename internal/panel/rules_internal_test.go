package panel

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/safefile"
)

// The write of a file of the rules goes through the guard of safefile: a file edited by
// hand between the read and the rename is never overwritten, and the answer is the
// conflict of the file that changed.
func TestAFileEditedOnDiskBetweenTheReadAndTheWriteIsNotOverwritten(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "politica-de-aprovacao.yaml")
	if err := os.WriteFile(path, []byte("versao: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, read, err := readRuleInfo(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	// somebody edits the file after it was read (a different size and a later time)
	hand := []byte("versao: 1\n# editado a mao\n")
	if err := os.WriteFile(path, hand, 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	err = safefile.Write(path, []byte("versao: 1\nexpiracao: 12h\n"), unchangedSince(read))
	if !errors.Is(err, errChangedOnDisk) {
		t.Fatalf("write = %v, want the file changed on disk", err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(hand) {
		t.Errorf("the edit made by hand was overwritten: %q", got)
	}
	w := httptest.NewRecorder()
	(&Panel{}).fileError(w, err)
	if w.Code != http.StatusConflict {
		t.Errorf("answer = %d %s, want the conflict", w.Code, w.Body)
	}
	// a file that was not touched is written, and a file that did not exist is created
	_, read, _ = readRuleInfo(path, 1024)
	if err := safefile.Write(path, []byte("novo\n"), unchangedSince(read)); err != nil {
		t.Fatalf("write of an untouched file = %v", err)
	}
	fresh := filepath.Join(t.TempDir(), "a", "teto.yaml")
	_, read, _ = readRuleInfo(fresh, 1024)
	if err := safefile.Write(fresh, []byte("x\n"), unchangedSince(read)); err != nil {
		t.Fatalf("creating a file = %v", err)
	}
}
