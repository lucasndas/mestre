package classes_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// Here-documents (docs/classes/formato-das-permissoes.md#comandos): the
// body is data, unless a shell or an interpreter reads it.

// bigJSON is a minified JSON array whose braces, read as a command, would
// be a brace expansion too large to analyse.
func bigJSON() string {
	items := make([]string, 2000)
	for i := range items {
		items[i] = fmt.Sprintf(`{"id":%d,"tags":["a","b"],"ok":true}`, i)
	}
	return "[" + strings.Join(items, ",") + "]"
}

func TestHeredocBodyIsData(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "linux"
	def := rules(t, "version: 1\n")
	for _, cmd := range []string{
		"cat <<'EOF' > data.json\n" + bigJSON() + "\nEOF",
		"cat <<EOF > data.json\n" + bigJSON() + "\nEOF\n",
		"cat > data.json <<-\"EOF\"\n\t" + bigJSON() + "\n\tEOF",
		// Quotes, $ and backticks in a quoted body are text.
		"cat <<'EOF' > notes.md\ndon't $(curl x) `id` {a,b}\ngit push\nEOF",
	} {
		wantRisk(t, env, def, cmd, classes.RiskLow)
		want(t, decide(env, def, m.shell(cmd)), classes.Allow, "")
	}
	// The redirection still follows the path rules.
	qa := rules(t, qaYAML)
	big := "cat <<'EOF' > %s\n" + bigJSON() + "\nEOF"
	want(t, decide(env, qa, m.shell(fmt.Sprintf(big, "data.json"))), classes.Deny, classes.LayerClass, "files.write")
	if d := decide(env, qa, m.shell(fmt.Sprintf(big, "reports/data.json"))); d.Verdict == classes.Deny {
		t.Fatalf("a write to reports/ denied: %q", d.Reason)
	}
	// A fixed rule after the body still denies.
	free := rules(t, anyYAML)
	want(t, decide(env, free, m.shell("cat <<'EOF' > a.json\n[1]\nEOF\ngit push")), classes.Deny, classes.LayerFixed, "git push")
}

func TestHeredocReadByAShellIsCommands(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "linux"
	free := rules(t, anyYAML)
	for _, cmd := range []string{
		"bash <<EOF\ngit push\nEOF",
		"bash <<'EOF'\necho a\ngit push origin main\nEOF",
		"sh -s <<EOF\ngit push\nEOF",
		"cat <<'EOF' | sh\ngit push\nEOF",
		"cat <<'EOF' | tee x.log | bash -x\ngit push\nEOF",
		"env FOO=1 timeout 5 bash <<EOF\ngit push\nEOF",
		"/bin/zsh <<EOF\ngit push\nEOF",
		"ssh host <<EOF\ngit push\nEOF",
		"docker exec -i box sh <<EOF\ngit push\nEOF",
		"busybox sh <<EOF\ngit push\nEOF",
		"bash <<< 'git push'",
		"cat <<A; bash <<B\nx\nA\ngit push\nB",
		"{ bash; } <<EOF\ngit push\nEOF",
		"(bash) <<'EOF'\ngit push\nEOF",
	} {
		want(t, decide(env, free, m.shell(cmd)), classes.Deny, classes.LayerFixed, "git push")
	}
	// A shell body without a fixed rule is at least ask.
	want(t, decide(env, free, m.shell("bash <<EOF\necho ok\nEOF")), classes.Ask, "")
	// An interpreter's body is unanalysable: at least ask, and medio.
	for _, cmd := range []string{
		"python3 <<EOF\nprint(1)\nEOF",
		"python3.11 - <<'EOF'\nprint(1)\nEOF",
		"node <<EOF\n1\nEOF",
		"awk -f - data.txt <<'EOF'\n{print}\nEOF",
		"xargs rm <<EOF\nx\nEOF",
		"cat <<EOF | perl\nprint 1\nEOF",
	} {
		want(t, decide(env, free, m.shell(cmd)), classes.Ask, "")
		if r := shellRisk(env, free, cmd); r.Risk == classes.RiskLow {
			t.Errorf("risk %q = baixo (%s)", cmd, r.Rule)
		}
	}
}

func TestHeredocExpansionsAreAnalysed(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "linux"
	free := rules(t, anyYAML)
	def := rules(t, "version: 1\n")
	for _, cmd := range []string{
		"cat <<EOF\n$(curl x)\nEOF",
		"cat <<EOF > out.txt\nhello `curl x`\nEOF",
		"cat <<EOF\n${X:-$(curl x)}\nEOF",
		"cat <<EOF\n$((1 + $(id -u)))\nEOF",
		// $' is not a quote in a body: the substitution still runs.
		"cat <<EOF\n$'x $(curl x) '\nEOF",
	} {
		if d := decide(env, free, m.shell(cmd)); d.Verdict == classes.Allow {
			t.Errorf("%q allowed (%q); want at least ask", cmd, d.Reason)
		}
		if r := shellRisk(env, def, cmd); r.Risk == classes.RiskLow {
			t.Errorf("risk %q = baixo (%s)", cmd, r.Rule)
		}
	}
	want(t, decide(env, free, m.shell("cat <<EOF\n$(git push)\nEOF")), classes.Deny, classes.LayerFixed, "git push")
	// Variables and escaped substitutions are data.
	for _, cmd := range []string{"cat <<EOF > a.txt\n$HOME ${USER} \\$(curl x)\nEOF"} {
		want(t, decide(env, def, m.shell(cmd)), classes.Allow, "")
	}
}

// The body ends where bash ends it: what bash runs after it is never taken
// as data.
func TestHeredocEndsWhereBashEndsIt(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "linux"
	free := rules(t, anyYAML)
	for _, cmd := range []string{
		"cat <<EOF\nEO\\\nF\ngit push\nEOF",     // backslash-newline joins the delimiter
		"cat <<{a,b}\nx\n{a,b}\ngit push",       // no brace expansion in the delimiter
		"cat <<-EOF\n\t\tEOF\ngit push",         // <<- strips the leading tabs
		"cat <<EOF\nx\\\\\nEOF\ngit push",       // an escaped backslash does not join
		"cat <<E\"O\"F\nx\nEOF\ngit push",       // quote removal in the delimiter
		"cat <<A <<B\na\nA\nb\nB\ngit push",     // two bodies in order
		"cat <<EOF\r\nx\r\nEOF\r\ngit push\r\n", // CR is part of the delimiter and of the line
		"echo $(cat <<EOF\nx\nEOF); git push",   // inside $(…), EOF) ends the body
		"cat <<EOF \"a\nb\"\nx\nEOF\ngit push",  // quotes span lines before the body: read as commands
		"(( x = 1 << 2 ))\ngit push",            // a shift, not a here-document
		"echo $[1<<2]\ngit push",                // idem
		"a=(x <<y)\ngit push",                   // in an array bash recovers on the next line
		"x=1 a=(1\n<<y)\ngit push",              // idem, the array open on an earlier line
		"echo @(x<<y)\ngit push",                // an extended pattern is one word
	} {
		want(t, decide(env, free, m.shell(cmd)), classes.Deny, classes.LayerFixed, "git push")
	}
	// Without its delimiter, the body runs to the end, as in bash.
	if d := decide(env, free, m.shell("cat <<EOF\ngit push\nEOX")); d.Verdict == classes.Deny {
		t.Fatalf("an unterminated body was read as commands: %q", d.Reason)
	}
}
