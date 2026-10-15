package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/testkit/localrig"
)

// `--from` takes a folder of $MESTRE_TMP: the CLI reads it, as the character,
// and sends the text of its files; a folder outside $MESTRE_TMP, one that
// reaches it through a link, one with a link inside or one that is not text is
// refused before anything is sent (docs/cli/coordenacao-classes-e-rituais.md).
func TestFromTakesAFolderOfTheTmpAndSendsItsFilesAsText(t *testing.T) {
	t.Parallel()
	tmp, outside := t.TempDir(), t.TempDir()
	write := func(root, rel, text string) string {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return filepath.Dir(p)
	}
	pkg := write(tmp, "qa/CLASS.md", "---\nname: qa\n---\ncorpo")
	write(tmp, "qa/hooks/hooks.yaml", "version: 1\n")
	write(outside, "qa/CLASS.md", "x")
	write(tmp, "binary/CLASS.md", "a\x00b")
	write(tmp, "linked/CLASS.md", "x")
	linkOK := os.Symlink(outside, filepath.Join(tmp, "toutside")) == nil
	if linkOK {
		if err := os.Symlink(filepath.Join(outside, "qa", "CLASS.md"), filepath.Join(tmp, "linked", "link.md")); err != nil {
			t.Fatal(err)
		}
	}
	bubble(t, func(t *testing.T, r *localrig.Rig) {
		r.Character("tA", "alice")
		p := newProc(r, "alice-proc", "tA", "alice")
		p.env["MESTRE_TMP"] = tmp
		got := capture(r, "class create")

		res := p.run(t.Context(), "class", "create", "qa", "--app", "novo", "--from", pkg)
		if res.code != 0 {
			t.Fatalf("class create from the tmp = %+v", res)
		}
		files, _ := got().Flags["package"].(map[string]any)
		if len(files) != 2 || files["CLASS.md"] != "---\nname: qa\n---\ncorpo" || files["hooks/hooks.yaml"] != "version: 1\n" {
			t.Errorf("package = %v", got().Flags["package"])
		}
		if _, has := got().Flags["from"]; has {
			t.Error("the path of --from went to the engine")
		}

		refuse := func(name, from string, code int, errCode string) {
			t.Helper()
			res := p.run(t.Context(), "class", "create", "qa", "--app", "novo", "--from", from, "--json")
			if res.code != code || !strings.Contains(res.stdout, `"code":"`+errCode+`"`) {
				t.Errorf("%s: %+v, want exit %d %s", name, res, code, errCode)
			}
		}
		refuse("a folder outside $MESTRE_TMP", filepath.Join(outside, "qa"), 2, "from_outside_tmp")
		refuse("the $MESTRE_TMP itself", tmp, 2, "from_outside_tmp")
		refuse("a relative path that leaves it", filepath.Join(pkg, "..", "..", filepath.Base(outside)), 2, "from_outside_tmp")
		refuse("a folder that does not exist", filepath.Join(tmp, "nada"), 4, "not_found")
		refuse("a file that is not text", filepath.Join(tmp, "binary"), 2, "usage")
		if linkOK {
			refuse("a link in the tmp to a folder outside", filepath.Join(tmp, "toutside", "qa"), 2, "from_outside_tmp")
			refuse("a link inside the folder", filepath.Join(tmp, "linked"), 2, "usage")
		}
		p.env["MESTRE_TMP"] = ""
		refuse("a process with no $MESTRE_TMP", pkg, 2, "from_outside_tmp")
	})
}
