package providers

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// The trust hash follows hooks/src/engine/discovery.rs (hook_hash) of
// openai/codex: the input below is the canonical JSON the source builds for
// this handler (read at 2f05c0a7); the conformance test checks the real CLI.
func TestCodexHookHashIsTheHashOfTheCanonicalIdentity(t *testing.T) {
	t.Parallel()
	const input = `{"event_name":"pre_tool_use","hooks":[{"async":false,"command":"/usr/bin/mestre hook --provider codex --event PreToolUse","timeout":30,"type":"command"}],"matcher":"*"}`
	sum := sha256.Sum256([]byte(input))
	var pre codexHook
	for _, h := range codexHooks {
		if h.event == "PreToolUse" {
			pre = h
		}
	}
	got, err := codexHookHash(pre, "/usr/bin/mestre")
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:" + hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("hash = %s, want %s", got, want)
	}
	// Stop has no matcher in the identity: Codex ignores it there.
	var stop codexHook
	for _, h := range codexHooks {
		if h.event == "Stop" {
			stop = h
		}
	}
	stopInput := `{"event_name":"stop","hooks":[{"async":false,"command":"/usr/bin/mestre hook --provider codex --event Stop","timeout":60,"type":"command"}]}`
	sum = sha256.Sum256([]byte(stopInput))
	if got, _ := codexHookHash(stop, "/usr/bin/mestre"); got != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("stop hash = %s", got)
	}
	if k := codexHookKey("/p/hooks.json", pre); k != "/p/hooks.json:pre_tool_use:0:0" {
		t.Fatalf("key = %s", k)
	}
}

// What looks like a table inside a string or an array is not one.
func TestSetTOMLKeySeesTablesOnlyOutsideValues(t *testing.T) {
	t.Parallel()
	for _, doc := range []string{
		"a = \"\"\"x \\\"\"\"\n[t]\nk = 1\n\"\"\"\n", // an escaped quote does not close
		"a = \"\"\"x\"\"\"\"\n",                      // a quote right before the closing is content
		"a = '''\n[t]\n'''\n",                        // a literal multi-line string
		"m = [\n  [1, 2],\n  [3],\n]\n",              // an array of arrays across lines
		"i = { x = \"[t]\" } # [t]\n",                // an inline table and a comment
	} {
		out, err := setTOMLKey([]byte(doc), []string{"t"}, "k", "2")
		if err != nil {
			t.Errorf("%q: %v", doc, err)
			continue
		}
		if want := doc + "\n[t]\nk = 2\n"; string(out) != want {
			t.Errorf("%q became %q, want %q", doc, out, want)
		}
	}
	// The root: before the first table.
	out, err := setTOMLKey([]byte("# c\na = 1\n\n[t]\nb = 2\n"), nil, "cli_auth_credentials_store", `"file"`)
	if err != nil || string(out) != "# c\na = 1\n\ncli_auth_credentials_store = \"file\"\n[t]\nb = 2\n" {
		t.Fatalf("root = %q, %v", out, err)
	}
}

func TestTheTrustFallbackWritesOneEntryPerHook(t *testing.T) {
	t.Parallel()
	c := NewCodex(Options{})
	files, err := c.HookFiles(HookFilesRequest{Root: "/p", Binary: "/usr/bin/mestre", Fallbacks: NativeFallbacks{CodexHookTrust: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || !strings.HasSuffix(files[1].Path, "config.toml") {
		t.Fatalf("files = %+v", files)
	}
	cur := []byte("model = \"x\"\n")
	out, err := files[1].Merge(cur)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), string(cur)) || strings.Count(string(out), "trusted_hash = \"sha256:") != len(codexHooks) {
		t.Fatalf("config.toml = %s", out)
	}
	if again, _ := files[1].Merge(out); string(again) != string(out) {
		t.Fatal("a second merge changed config.toml")
	}
}
