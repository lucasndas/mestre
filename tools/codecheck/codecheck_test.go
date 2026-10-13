package main

import (
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// run checks a tree of Go files and returns the issues as text.
func run(t *testing.T, files map[string]string) []string {
	t.Helper()
	fsys := fstest.MapFS{}
	for p, src := range files {
		fsys[p] = &fstest.MapFile{Data: []byte(src)}
	}
	issues, err := Check(fsys)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(issues))
	for i, is := range issues {
		out[i] = is.String()
	}
	return out
}

func expect(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("issues =\n%s\nwant %d: %q", strings.Join(got, "\n"), len(want), want)
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], w) {
			t.Fatalf("issue %d =\n%s\nwant prefix %q", i, got[i], w)
		}
	}
}

// CA-CODIGO-01
func TestEverySubtestIsParallel(t *testing.T) {
	t.Parallel()
	src := `package x

import "testing"

func TestA(t *testing.T) {
	t.Parallel()
	t.Run("sub", func(t *testing.T) {})
}

func TestB(t *testing.T) {
	t.Parallel()
	t.Run("sub", func(t *testing.T) { t.Parallel() })
}

func runCases(t *testing.T, names []string) {
	for _, n := range names {
		t.Run(n, func(t *testing.T) {})
	}
}

func (s suite) check(name string, t *testing.T) {
	t.Run(name, func(t *testing.T) { t.Parallel() })
}

func noT(names []string) {
	var b *testing.B
	b.Run("bench", func(b *testing.B) {})
}
`
	got := run(t, map[string]string{
		"x/x_test.go":                   src,
		"x/testdata/y_test.go":          "not go",
		"x/x.go":                        "package x\n\nfunc f(t interface{ Run(string, func()) }) { t.Run(\"a\", func() {}) }\n",
		"x/other_test.go":               "package x\n",
		"web/node/x_test.go":            "not go",
		".claude/worktrees/a/x_test.go": "not go",
		"_old/x_test.go":                "not go",
	})
	expect(t, got,
		"x/x_test.go:7: parallel: subtest of TestA does not call t.Parallel()",
		"x/x_test.go:17: parallel: subtest of runCases does not call t.Parallel()")
}

// CA-CODIGO-02
func TestBareSelectIsForbiddenInTestCode(t *testing.T) {
	t.Parallel()
	bare := "package x\n\nfunc block() {\n\tselect {}\n}\n"
	withCase := "package x\n\nfunc wait(c chan int) {\n\tselect {\n\tcase <-c:\n\t}\n}\n"
	cases := map[string]struct {
		path, src string
		want      []string
	}{
		"test file":             {"internal/x/h_test.go", bare, []string{"internal/x/h_test.go:4: bare-select: bare select {} in test code"}},
		"testkit package":       {"internal/testkit/fakepty/p.go", bare, []string{"internal/testkit/fakepty/p.go:4: bare-select:"}},
		"fake agent":            {"cmd/fakeagent/main.go", bare, []string{"cmd/fakeagent/main.go:4: bare-select:"}},
		"select with a case":    {"internal/x/h_test.go", withCase, nil},
		"production code":       {"internal/x/x.go", bare, nil},
		"spike code":            {"tools/spikes/linux/a/main.go", bare, nil},
		"the block() pattern":   {"internal/x/h_test.go", "package x\n\nfunc block() {\n\t<-clock.Real{}.NewTimer(1000 * time.Hour).C()\n}\n", nil},
		"excused with a reason": {"internal/x/h_test.go", "package x\n\nfunc block() {\n\t// mestre:allow bare-select: the helper is a console program that never runs on Windows\n\tselect {}\n}\n", nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(t, map[string]string{c.path: c.src}), c.want...)
		})
	}
}

// CA-CODIGO-03
func TestChildProcessNeverGetsTheStdioOfTheTest(t *testing.T) {
	t.Parallel()
	head := "package x\n\nimport (\n\t\"os\"\n\t\"os/exec\"\n)\n\nfunc f(cmd *exec.Cmd) {\n"
	cases := map[string]struct {
		body string
		want []string
	}{
		"stdout":      {"\tcmd.Stdout = os.Stdout\n}\n", []string{"internal/x/h_test.go:9: child-stdio:"}},
		"stderr":      {"\tcmd.Stderr = os.Stderr\n}\n", []string{"internal/x/h_test.go:9: child-stdio:"}},
		"both":        {"\tcmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr\n}\n", []string{"internal/x/h_test.go:9: child-stdio:", "internal/x/h_test.go:9: child-stdio:"}},
		"multiwriter": {"\tcmd.Stdout = io.MultiWriter(&buf, os.Stdout)\n}\n", []string{"internal/x/h_test.go:9: child-stdio:"}},
		"literal":     {"\t_ = exec.Cmd{Path: \"x\", Stderr: os.Stderr}\n}\n", []string{"internal/x/h_test.go:9: child-stdio:"}},
		"proc attr":   {"\t_ = os.ProcAttr{Files: []*os.File{os.Stdin, os.Stdout, os.Stderr}}\n}\n", []string{"internal/x/h_test.go:9: child-stdio:"}},
		"a buffer":    {"\tvar out bytes.Buffer\n\tcmd.Stdout, cmd.Stderr = &out, &out\n}\n", nil},
		"discard":     {"\tcmd.Stdout = io.Discard\n}\n", nil},
		"stdin":       {"\tcmd.Stdin = os.Stdin\n}\n", nil},
		"own output":  {"\tfmt.Fprintln(os.Stderr, \"boom\")\n}\n", nil},
		"other type":  {"\t_ = Env{Stdout: os.Stdout}\n}\n", nil},
		"excused":     {"\t// mestre:allow child-stdio: the child is the visible console of a manual run\n\tcmd.Stdout = os.Stdout\n}\n", nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := run(t, map[string]string{"internal/x/h_test.go": head + c.body})
			expect(t, got, c.want...)
		})
	}
	t.Run("production code may", func(t *testing.T) {
		t.Parallel()
		expect(t, run(t, map[string]string{"cmd/mestre/main.go": head + "\tcmd.Stdout = os.Stdout\n}\n"}))
	})
}

const winHead = "package x\n\nimport \"golang.org/x/sys/windows\"\n\nfunc f() {\n"

// CA-CODIGO-04
func TestWindowsObjectsNeedASecurityDescriptor(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		path, body string
		want       []string
	}{
		"job object":           {"a_windows.go", "\t_, _ = windows.CreateJobObject(nil, nil)\n}\n", []string{"a_windows.go:6: nil-sa: windows.CreateJobObject with nil security attributes"}},
		"job with attributes":  {"a_windows.go", "\t_, _ = windows.CreateJobObject(sa, nil)\n}\n", nil},
		"named pipe":           {"a_windows.go", "\t_, _ = windows.CreateNamedPipe(name, 1, 2, 3, 4, 5, 0, nil)\n}\n", []string{"a_windows.go:6: nil-sa: windows.CreateNamedPipe"}},
		"pipe with sa":         {"a_windows.go", "\t_, _ = windows.CreateNamedPipe(name, 1, 2, 3, 4, 5, 0, l.sa)\n}\n", nil},
		"process as user":      {"a_windows.go", "\t_ = windows.CreateProcessAsUser(tok, app, cmd, nil, tsa, true, 0, env, dir, si, pi)\n}\n", []string{"a_windows.go:6: nil-sa: windows.CreateProcessAsUser"}},
		"process thread":       {"a_windows.go", "\t_ = windows.CreateProcessAsUser(tok, app, cmd, psa, nil, true, 0, env, dir, si, pi)\n}\n", []string{"a_windows.go:6: nil-sa: windows.CreateProcessAsUser"}},
		"process":              {"a_windows.go", "\t_ = windows.CreateProcess(app, cmd, nil, nil, true, 0, env, dir, si, pi)\n}\n", []string{"a_windows.go:6: nil-sa: windows.CreateProcess"}},
		"process with both":    {"a_windows.go", "\t_ = windows.CreateProcess(app, cmd, psa, tsa, true, 0, env, dir, si, pi)\n}\n", nil},
		"new file":             {"a_windows.go", "\t_, _ = windows.CreateFile(p, 1, 0, nil, windows.CREATE_NEW, 0, 0)\n}\n", []string{"a_windows.go:6: nil-sa: windows.CreateFile"}},
		"new file, a variable": {"a_windows.go", "\t_, _ = windows.CreateFile(p, 1, 0, sa, windows.CREATE_NEW, 0, 0)\n}\n", nil},
		"open existing file":   {"a_windows.go", "\t_, _ = windows.CreateFile(p, 1, 0, nil, windows.OPEN_EXISTING, 0, 0)\n}\n", nil},
		"truncate existing":    {"a_windows.go", "\t_, _ = windows.CreateFile(p, 1, 0, nil, windows.TRUNCATE_EXISTING, 0, 0)\n}\n", nil},
		"excused":              {"a_windows.go", "\t// mestre:allow nil-sa: the job only counts processes and nobody else opens it\n\t_, _ = windows.CreateJobObject(nil, nil)\n}\n", nil},
		"test file":            {"a_windows_test.go", "\t_, _ = windows.CreateJobObject(nil, nil)\n}\n", nil},
		"not a windows file":   {"a.go", "\t_, _ = windows.CreateJobObject(nil, nil)\n}\n", nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(t, map[string]string{"internal/x/" + c.path: winHead + c.body}), prefixAll("internal/x/", c.want)...)
		})
	}
	t.Run("an aliased import", func(t *testing.T) {
		t.Parallel()
		src := "package x\n\nimport w \"golang.org/x/sys/windows\"\n\nfunc f() {\n\t_, _ = w.CreateJobObject(nil, nil)\n}\n"
		expect(t, run(t, map[string]string{"internal/x/a_windows.go": src}), "internal/x/a_windows.go:6: nil-sa:")
	})
}

func prefixAll(prefix string, in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = prefix + s
	}
	return out
}

// CA-CODIGO-05
func TestPrivateFoldersGoThroughTheHelper(t *testing.T) {
	t.Parallel()
	mk := func(call string) string {
		return "package x\n\nimport \"os\"\n\nfunc f(d string) error {\n\t" + call + "\n\treturn nil\n}\n"
	}
	cases := map[string]struct {
		path, src string
		want      []string
	}{
		"MkdirAll":     {"internal/store/s.go", mk("_ = os.MkdirAll(d, 0o700)"), []string{"internal/store/s.go:6: private-dir:"}},
		"Mkdir":        {"internal/store/s.go", mk("_ = os.Mkdir(d, 0o700)"), []string{"internal/store/s.go:6: private-dir:"}},
		"old octal":    {"internal/store/s.go", mk("_ = os.MkdirAll(d, 0700)"), []string{"internal/store/s.go:6: private-dir:"}},
		"FileMode":     {"internal/store/s.go", mk("_ = os.MkdirAll(d, os.FileMode(0o700))"), []string{"internal/store/s.go:6: private-dir:"}},
		"the helper":   {"internal/privdir/privdir.go", mk("_ = os.MkdirAll(d, 0o700)"), nil},
		"another mode": {"internal/store/s.go", mk("_ = os.MkdirAll(d, 0o755)"), nil},
		"test code":    {"internal/store/s_test.go", mk("_ = os.MkdirAll(d, 0o700)"), nil},
		"windows file": {"internal/winsvc/a_windows.go", mk("_ = os.MkdirAll(d, 0o700)"), nil},
		"excused":      {"internal/store/s.go", mk("// mestre:allow private-dir: the folder belongs to a temporary build of the tool\n\t_ = os.MkdirAll(d, 0o700)"), nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(t, map[string]string{c.path: c.src}), c.want...)
		})
	}
}

// CA-CODIGO-06
func TestSecretsPrintThroughValueReceivers(t *testing.T) {
	t.Parallel()
	hdr := "package v\n\nimport \"fmt\"\n\n"
	cases := map[string]struct {
		files map[string]string
		want  []string
	}{
		"value receivers": {map[string]string{"v/k.go": hdr + "type K struct{ b []byte }\n\nfunc (k *K) Reveal() string { return string(k.b) }\nfunc (K) String() string { return \"[segredo]\" }\nfunc (K) GoString() string { return \"[segredo]\" }\n"}, nil},
		"String on a pointer, with Reveal": {map[string]string{"v/k.go": hdr + "type K struct{ b []byte }\n\nfunc (k *K) Reveal() string { return string(k.b) }\nfunc (k *K) String() string { return \"x\" }\n"},
			[]string{"v/k.go:8: secret-receiver: K has a Reveal method, so String must have a value receiver"}},
		"Reveal in another file of the package": {map[string]string{
			"v/a.go": hdr + "type K struct{}\n\nfunc (k *K) String() string { return \"x\" }\n",
			"v/b.go": "package v\n\nfunc (k K) Reveal() string { return \"\" }\n"},
			[]string{"v/a.go:7: secret-receiver: K has a Reveal method"}},
		"Reveal in another package": {map[string]string{
			"v/a.go": hdr + "type K struct{}\n\nfunc (k *K) String() string { return \"x\" }\n",
			"w/b.go": "package w\n\ntype K struct{}\n\nfunc (k K) Reveal() string { return \"\" }\n"}, nil},
		"redaction marker on a pointer": {map[string]string{"v/k.go": hdr + "type S string\n\nfunc (s *S) String() string { return logging.Redacted }\n"},
			[]string{"v/k.go:7: secret-receiver: String of *S returns the redaction marker"}},
		"redaction text on a pointer": {map[string]string{"v/k.go": hdr + "type S string\n\nfunc (s *S) LogValue() string { return \"[segredo]\" }\n"},
			[]string{"v/k.go:7: secret-receiver: LogValue of *S"}},
		"Format that writes the marker": {map[string]string{"v/k.go": hdr + "type S string\n\nfunc (s *S) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(logging.Redacted)) }\n"},
			[]string{"v/k.go:7: secret-receiver: Format of *S"}},
		"MarshalJSON that redacts": {map[string]string{"v/k.go": hdr + "type S string\n\nfunc (s *S) MarshalJSON() ([]byte, error) { return []byte(`\"[segredo]\"`), nil }\n"},
			[]string{"v/k.go:7: secret-receiver: MarshalJSON of *S"}},
		"MarshalJSON that reveals, with Reveal": {map[string]string{"v/k.go": hdr + "type S string\n\nfunc (s S) Reveal() string { return string(s) }\nfunc (s *S) MarshalJSON() ([]byte, error) { return []byte(s.Reveal()), nil }\n"}, nil},
		"pointer String of a plain type":        {map[string]string{"v/k.go": hdr + "type P struct{ n int }\n\nfunc (p *P) String() string { return fmt.Sprint(p.n) }\n"}, nil},
		"generic receiver": {map[string]string{"v/k.go": hdr + "type G[T any] struct{ v T }\n\nfunc (g G[T]) Reveal() T { return g.v }\nfunc (g *G[T]) String() string { return \"\" }\n"},
			[]string{"v/k.go:8: secret-receiver: G has a Reveal method"}},
		"test code": {map[string]string{"v/k_test.go": hdr + "type S string\n\nfunc (s *S) String() string { return logging.Redacted }\n"}, nil},
		"excused":   {map[string]string{"v/k.go": hdr + "type S string\n\n// mestre:allow secret-receiver: S is only ever used through its pointer\nfunc (s *S) String() string { return logging.Redacted }\n"}, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			expect(t, run(t, c.files), c.want...)
		})
	}
}

// CA-CODIGO-07
func TestExceptionsNeedAReasonAndExcuseSomething(t *testing.T) {
	t.Parallel()
	body := "package x\n\nfunc block() {\n%s\n\tselect {}\n}\n"
	cases := map[string]struct {
		comment string
		want    []string
	}{
		"with a reason":     {"\t// mestre:allow bare-select: the helper is never built for Windows", nil},
		"no reason":         {"\t// mestre:allow bare-select", []string{"internal/x/h_test.go:4: allow: mestre:allow bare-select needs a reason", "internal/x/h_test.go:5: bare-select:"}},
		"empty reason":      {"\t// mestre:allow bare-select:   ", []string{"internal/x/h_test.go:4: allow: mestre:allow bare-select needs a reason", "internal/x/h_test.go:5: bare-select:"}},
		"unknown rule":      {"\t// mestre:allow bare-selec: typo", []string{"internal/x/h_test.go:4: allow: unknown rule bare-selec", "internal/x/h_test.go:5: bare-select:"}},
		"another rule":      {"\t// mestre:allow nil-sa: not this one", []string{"internal/x/h_test.go:4: allow: mestre:allow nil-sa excuses nothing", "internal/x/h_test.go:5: bare-select:"}},
		"two lines away":    {"\t// mestre:allow bare-select: too far\n\t_ = 1", []string{"internal/x/h_test.go:4: allow: mestre:allow bare-select excuses nothing", "internal/x/h_test.go:6: bare-select:"}},
		"nothing to excuse": {"\t// mestre:allow nil-sa: there is no call here", []string{"internal/x/h_test.go:4: allow: mestre:allow nil-sa excuses nothing", "internal/x/h_test.go:5: bare-select:"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src := strings.Replace(body, "%s", c.comment, 1)
			expect(t, run(t, map[string]string{"internal/x/h_test.go": src}), c.want...)
		})
	}
	t.Run("on the same line", func(t *testing.T) {
		t.Parallel()
		src := "package x\n\nfunc block() {\n\tselect {} // mestre:allow bare-select: never built for Windows\n}\n"
		expect(t, run(t, map[string]string{"internal/x/h_test.go": src}))
	})
}

// CA-CODIGO-08
// The rules hold for the whole repository, and the check is fast: the best
// of up to three runs takes under a second (the first within the budget ends
// the measuring).
func TestCodeRules(t *testing.T) {
	t.Parallel()
	root := os.DirFS("../..")
	var best time.Duration
	for i := 0; i < 3; i++ {
		clk := clock.Real{}
		start := clk.Now()
		issues, err := Check(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, is := range issues {
			t.Error(is)
		}
		if d := clock.Since(clk, start); i == 0 || d < best {
			best = d
		}
		if best <= time.Second {
			break // within the budget: no need to measure again
		}
	}
	if best > time.Second {
		t.Errorf("the code rules took %v, budget 1s", best)
	}
}
