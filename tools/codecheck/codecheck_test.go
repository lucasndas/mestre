package main

import (
	"os"
	"testing"
	"testing/fstest"
)

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
	fsys := fstest.MapFS{
		"x/x_test.go":          {Data: []byte(src)},
		"x/testdata/y_test.go": {Data: []byte("not go")},
		"x/x.go":               {Data: []byte("package x\n\nfunc f(t interface{ Run(string, func()) }) { t.Run(\"a\", func() {}) }\n")},
		"x/other_test.go":      {Data: []byte("package x\n")},
		"web/node/x_test.go":   {Data: []byte("not go")},
	}
	issues, err := Check(fsys)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"x/x_test.go:7: parallel: subtest of TestA does not call t.Parallel()",
		"x/x_test.go:17: parallel: subtest of runCases does not call t.Parallel()",
	}
	if len(issues) != len(want) {
		t.Fatalf("issues = %q, want %q", issues, want)
	}
	for i, w := range want {
		if issues[i].String() != w {
			t.Fatalf("issues = %q, want %q", issues, want)
		}
	}
}

// The rule holds for the whole repository.
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
