package classes

import (
	"slices"
	"strings"
	"testing"
)

func TestBraceExpansionMakesTheWordsOfBash(t *testing.T) {
	t.Parallel()
	for line, want := range map[string]string{
		"echo a{b,c}d":         "echo abd acd",
		"echo {a,b{c,d}}e":     "echo ae bce bde",
		"echo {a}{b,c}":        "echo {a}b {a}c",
		"echo {1..3} {3..1}":   "echo 1 2 3 3 2 1",
		"echo {a..c} {01..03}": "echo a b c 01 02 03",
		"echo {1..10..4}":      "echo 1 5 9",
		"echo {-2..2..2}":      "echo -2 0 2",
		"{gi,}t pu{,}sh":       "git t push push",
		`echo {a,"b,c"}`:       "echo a b,c",
		"echo {a,b":            "echo {a,b",
		"FOO={a,b} x":          "x",
		// An empty word of the expansion is dropped unless a quoted part
		// went into it; a quoted part of a sequence makes it literal.
		`{"git",} push`:          "git push",
		`{'git',} push x`:        "git push x",
		`echo {"",x}y`:           "echo y xy",
		`echo {"",x}`:            "echo  x",
		`echo {a,""}`:            "echo a ",
		`echo {"a".."c"} {a..c}`: "echo {a..c} a b c",
	} {
		parts, _, _ := parseLine(line, dialectPOSIX)
		var got []string
		for _, p := range parts {
			got = append(got, strings.Join(p.texts(), " "))
		}
		if g := strings.Join(got, "; "); g != want {
			t.Errorf("%q → %q; want %q", line, g, want)
		}
	}
	if _, ok := braceExpand("{1..999}", []int{0, 7}, nil); ok {
		t.Error("a sequence of 999 words was expanded")
	}
}

func TestSplitIntoSimpleCommands(t *testing.T) {
	t.Parallel()
	for line, want := range map[string][]string{
		"npm test && git push":            {"npm test", "git push"},
		"a; b || c | d & e\nf":            {"a", "b", "c", "d", "e", "f"},
		"(cd x && rm y); { z; }":          {"cd x", "rm y", "z"},
		`echo "a && b" 'c; d' e\;f`:       {"echo a && b c; d e;f"},
		"FOO=1 BAR=2 npm test # git push": {"npm test"},
		"if true; then rm x; fi":          {"true", "rm x"},
		"g\\it push":                      {"git push"},
	} {
		parts, _, _ := parseLine(line, dialectPOSIX)
		var got []string
		for _, p := range parts {
			got = append(got, strings.Join(p.texts(), " "))
		}
		if !slices.Equal(got, want) {
			t.Errorf("%q → %q; want %q", line, got, want)
		}
	}
}

func TestOpaqueConstructsAndSubstitutions(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"npm test $(curl x)", "echo `id`", "echo $HOME", "cat <<EOF\n$(id)\nEOF", "cat <<$(x)\ny\n$(x)", "diff <(ls) b", "for f in a; do rm $f; done", "echo ${X}", "echo $((1+2))", "echo 'a"} {
		parts, opaque, _ := parseLine(line, dialectPOSIX)
		for _, p := range parts {
			opaque = append(opaque, p.opaque...)
		}
		if len(opaque) == 0 {
			t.Errorf("%q is not opaque", line)
		}
	}
	parts, _, _ := parseLine("npm test $(curl x) > `date`.log", dialectPOSIX)
	if len(parts) != 1 || !slices.Equal(parts[0].subs, []string{"curl x", "date"}) {
		t.Fatalf("subs = %+v", parts)
	}
	_, opaque, _ := parseLine("npm test 2>&1 >/dev/null", dialectPOSIX)
	if len(opaque) != 0 {
		t.Fatalf("plain redirections are opaque: %v", opaque)
	}
}

// The body of a here-document is data of its part; the lines after it are
// commands again.
func TestHeredocBodyIsReadAsData(t *testing.T) {
	t.Parallel()
	parts, opaque, _ := parseLine("cat <<'EOF' > f.json\n{\"a\":[1,2]}\n$(x)\nEOF\necho done", dialectPOSIX)
	if len(opaque) != 0 || len(parts) != 2 {
		t.Fatalf("parts = %+v, opaque = %q", parts, opaque)
	}
	if got := parts[0].stdin; !slices.Equal(got, []string{"{\"a\":[1,2]}\n$(x)\n"}) || len(parts[0].subs) != 0 {
		t.Fatalf("stdin = %q, subs = %q", got, parts[0].subs)
	}
	if got := strings.Join(parts[1].texts(), " "); got != "echo done" {
		t.Fatalf("after the body: %q", got)
	}
	parts, _, _ = parseLine("cat <<EOF\na $(b) `c`\nEOF", dialectPOSIX)
	if !slices.Equal(parts[0].subs, []string{"b", "c"}) {
		t.Fatalf("subs of an unquoted body = %q", parts[0].subs)
	}
}

func TestRedirectionsAndAssignments(t *testing.T) {
	t.Parallel()
	parts, _, _ := parseLine("GIT_DIR=/x A=b cmd arg 2>err.txt >> out.txt < in.txt &> all.txt", dialectPOSIX)
	p := parts[0]
	if len(p.assigns) != 2 || p.assigns[0].text[:p.assigns[0].assign] != "GIT_DIR" {
		t.Fatalf("assigns = %+v", p.assigns)
	}
	var got []string
	for _, r := range p.redirs {
		got = append(got, r.op+" "+r.target.text)
	}
	if !slices.Equal(got, []string{"> err.txt", ">> out.txt", "< in.txt", "&> all.txt"}) {
		t.Fatalf("redirs = %q", got)
	}
}

func TestPowerShellLine(t *testing.T) {
	t.Parallel()
	parts, opaque, _ := parseLine(`Remove-Item -Path 'C:\a b\x' -Recurse; Get-ChildItem | Out-File "o.txt" > $null # c`, dialectPwsh)
	var got []string
	for _, p := range parts {
		got = append(got, strings.Join(p.texts(), " "))
	}
	if !slices.Equal(got, []string{`Remove-Item -Path C:\a b\x -Recurse`, "Get-ChildItem", "Out-File o.txt"}) || len(opaque) != 0 {
		t.Fatalf("parts %q opaque %v", got, opaque)
	}
	if _, opaque, _ := parseLine("echo $env:PATH", dialectPwsh); len(opaque) == 0 {
		t.Fatal("a PowerShell variable is not opaque")
	}
	if parts, _, _ := parseLine("icm { git push }", dialectPwsh); len(parts) != 2 || parts[1].texts()[0] != "git" {
		t.Fatalf("script block = %+v", parts)
	}
}
