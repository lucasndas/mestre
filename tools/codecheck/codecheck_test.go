package main

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

const mod = "module github.com/lucasndas/mestre\n\ngo 1.25\n"

func check(t *testing.T, files map[string]string) []string {
	t.Helper()
	fsys := fstest.MapFS{"go.mod": {Data: []byte(mod)}}
	for p, s := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(s)}
	}
	issues, err := Check(fsys)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, i := range issues {
		out = append(out, i.String())
	}
	return out
}

func TestTimeOnlyInClock(t *testing.T) {
	t.Parallel()
	src := "package x\n\nimport tm \"time\"\n\nfunc f() { tm.Sleep(1); _ = tm.Now(); _ = tm.Duration(1) }\n"
	got := check(t, map[string]string{"internal/x/x.go": src, "pkg/clock/c.go": src})
	if len(got) != 2 || got[0] != "internal/x/x.go:5: time: time.Sleep outside pkg/clock: use the injectable clock" {
		t.Fatalf("issues = %q", got)
	}
}

func TestTestkitOnlyInTests(t *testing.T) {
	t.Parallel()
	imp := "package x\n\nimport _ \"github.com/lucasndas/mestre/internal/testkit/fakenet\"\n"
	got := check(t, map[string]string{
		"internal/core/a.go":      imp,
		"internal/core/a_test.go": imp,
		"cmd/fakeagent/main.go":   imp,
		"internal/testkit/x/x.go": imp,
		"guardian/g.go":           "package g\n\nimport (\n\t_ \"os\"\n\t_ \"github.com/lucasndas/mestre/pkg/clock\"\n\t_ \"github.com/lucasndas/mestre/internal/logging\"\n)\n",
	})
	if len(got) != 2 || !strings.HasPrefix(got[0], "guardian/g.go:6: guardian:") || !strings.HasPrefix(got[1], "internal/core/a.go:3: testkit:") {
		t.Fatalf("issues = %q", got)
	}
}

func TestEveryTestIsParallel(t *testing.T) {
	t.Parallel()
	src := `package x

import "testing"

func TestA(t *testing.T) {
	t.Run("sub", func(t *testing.T) {})
}

func TestB(t *testing.T) {
	t.Parallel()
	t.Run("sub", func(t *testing.T) { t.Parallel() })
}

func TestMain(m *testing.M) {}
`
	got := check(t, map[string]string{"x/x_test.go": src})
	if len(got) != 2 || !strings.Contains(got[0], "TestA does not call t.Parallel()") || !strings.Contains(got[1], "subtest of TestA") {
		t.Fatalf("issues = %q", got)
	}
}

// The rules hold for the whole repository.
func TestCodeRules(t *testing.T) {
	t.Parallel()
	issues, err := Check(os.DirFS("../.."))
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range issues {
		t.Error(i)
	}
}
