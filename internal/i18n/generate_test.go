package i18n_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/i18n"
)

// CA-MSG-05: the panel says what the engine says from web/src/gen/messages.ts, so the
// committed file has to be what the generator makes of the catalogs today.
func TestGeneratedMessagesAreNotStale(t *testing.T) {
	t.Parallel()
	want, err := i18n.GenerateTS()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join("..", "..", i18n.GeneratedTS)
	have, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v: run `go generate ./internal/i18n`", err)
	}
	// A Windows checkout with autocrlf has CRLF; the content is the same.
	have = bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(have, want) {
		t.Fatalf("%s is stale: run `go generate ./internal/i18n` and commit it", i18n.GeneratedTS)
	}
}
