package classes

import (
	"slices"
	"strings"
	"testing"
)

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
	for _, line := range []string{"npm test $(curl x)", "echo `id`", "echo $HOME", "cat <<EOF\nx\nEOF", "diff <(ls) b", "for f in a; do rm $f; done", "echo ${X}", "echo $((1+2))", "echo 'a"} {
		_, opaque, _ := parseLine(line, dialectPOSIX)
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
