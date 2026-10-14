package classes

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// riskRow is one row of the default table of shell commands
// (docs/classes/classificacao-de-risco.md#comandos-de-shell): a command
// pattern, matched like a deny of commands, or a condition on the command.
type riskRow struct {
	risk    Risk
	group   string
	pattern string
	// cond is a condition row (pattern ""), or an extra condition the
	// pattern needs; label names it in the rule.
	cond  func(c *riskCmd) bool
	label string
	// lower turns the row into baixo (the network commands to the domains
	// of the effective rules).
	lower func(c *riskCmd) bool
	// program is the program a condition row is about, for the reach of a
	// pattern.
	program string
}

func (r riskRow) rule() string {
	if r.pattern == "" {
		return r.group + ": " + r.label
	}
	if r.label != "" {
		return r.group + ": " + r.pattern + " " + r.label
	}
	return r.group + ": " + r.pattern
}

func rows(risk Risk, group string, patterns ...string) []riskRow {
	out := make([]riskRow, len(patterns))
	for i, p := range patterns {
		out[i] = riskRow{risk: risk, group: group, pattern: p}
	}
	return out
}

func join(parts ...[]riskRow) []riskRow {
	var out []riskRow
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// riskTable is the default table, in the order it is matched: alto, medio,
// baixo; the first row that matches decides the part. RiskTableVersion
// changes when a row changes.
var riskTable = join(
	rows(RiskHigh, "GitHub e publicação", "gh *", "hub *", "npm publish*", "pnpm publish*", "yarn publish*", "yarn npm publish*",
		"cargo publish*", "twine upload*", "gem push*", "docker push*", "podman push*"),
	[]riskRow{{risk: RiskHigh, group: "GitHub e publicação", label: "URL do GitHub", cond: func(c *riskCmd) bool { return c.github }}},
	rows(RiskHigh, "segredos e acesso remoto", "printenv*"),
	[]riskRow{
		{risk: RiskHigh, group: "segredos e acesso remoto", label: "env sem comando", program: "env", cond: func(c *riskCmd) bool { return c.bareEnv }},
		{risk: RiskHigh, group: "segredos e acesso remoto", label: "set sem argumentos", program: "set", cond: func(c *riskCmd) bool { return c.name == "set" && len(c.args) == 0 }},
	},
	rows(RiskHigh, "segredos e acesso remoto", "ssh*", "scp*", "sftp*", "rsync *:*", "gpg*", "aws *", "gcloud *", "az *",
		"kubectl *", "helm *", "terraform *", "pulumi *", "git credential*", "npm login*", "npm adduser*", "npm token*",
		"secret-tool *", "cmdkey*", "vault *"),
	rows(RiskHigh, "apagar dados", "*DROP *", "*TRUNCATE*", "*DELETE FROM*", "dropdb*", "redis-cli flushall*",
		"docker volume rm*", "docker system prune*"),
	[]riskRow{{risk: RiskHigh, group: "apagar dados", label: "rm com caminho fora do worktree", program: "rm", cond: func(c *riskCmd) bool { return c.removes && c.rmOutside }}},
	rows(RiskHigh, "novos agentes", "aider*", "gemini*", "cursor-agent*", "opencode*", "goose*", "amp *", "ollama run*"),

	networkRows("curl*", "wget*", "http *", "Invoke-WebRequest*", "iwr *", "nc *", "ncat *", "telnet *"),
	[]riskRow{{risk: RiskMedium, group: "apagar em massa", label: "rm com -r, curinga ou mais de 20 caminhos", program: "rm", cond: func(c *riskCmd) bool { return c.removes && c.rmMass }}},
	rows(RiskMedium, "apagar em massa", "git clean*", "git reset --hard*", "git checkout -- *", "git restore *", "find * -delete*"),
	rows(RiskMedium, "fora do worktree e configuração", "npm install -g*", "npm i -g*", "* --global*", "pip install*", "pip3 install*",
		"go install*", "cargo install*", "npx *", "pnpm dlx *", "bunx *", "brew *", "winget *", "choco *", "scoop *",
		"npm config *", "npm pkg set*", "npm exec*", "npm x *", "npm link*", "yarn dlx *", "yarn global *", "bun x *",
		"deno install*"),
	rows(RiskMedium, "envio da branch da mesa", "mestre pr open*", "mestre pr update*"),
	rows(RiskMedium, "contêineres e serviços", "docker *", "podman *", "docker-compose *", "systemctl *", "service *",
		"launchctl *", "sc *", "Stop-Service*", "Start-Service*"),

	rows(RiskLow, "pacotes do projeto", "npm install*", "npm i *", "npm ci*", "npm uninstall*", "npm update*", "pnpm install*",
		"pnpm add*", "pnpm remove*", "yarn", "yarn install*", "yarn add*", "yarn remove*", "bun install*", "bun add*",
		"bun remove*", "uv pip install*", "poetry install*", "poetry add*", "uv sync*", "uv add*", "go mod *", "go get *",
		"cargo add*", "cargo fetch*", "bundle install*", "composer install*", "composer require*"),
	rows(RiskLow, "scripts e ferramentas do projeto", "npm run *", "npm test*", "pnpm run *", "pnpm test*", "yarn run *",
		"yarn test*", "bun run *", "make *", "just *", "go build*", "go test*", "go vet*", "go run *", "go generate*",
		"cargo build*", "cargo test*", "cargo check*", "cargo run*", "cargo clippy*", "cargo fmt*", "pytest*", "tsc*",
		"eslint*", "prettier*", "./gradlew *", "gradle *", "mvn *", "dotnet build*", "dotnet test*"),
	[]riskRow{{risk: RiskLow, group: "scripts e ferramentas do projeto", label: "interpretador com um arquivo do worktree", cond: func(c *riskCmd) bool { return c.scriptInWorktree }}},
	rows(RiskLow, "arquivos dentro do worktree", "mkdir *", "touch *", "cp *", "mv *", "ln -s *", "chmod *"),
	[]riskRow{{risk: RiskLow, group: "arquivos dentro do worktree", label: "rm sem -r, sem curinga e com até 20 caminhos", program: "rm", cond: func(c *riskCmd) bool { return c.name == "rm" }}},
)

func networkRows(patterns ...string) []riskRow {
	out := rows(RiskMedium, "rede", patterns...)
	for i := range out {
		out[i].lower = func(c *riskCmd) bool { return c.urls > 0 && c.allInDomains }
	}
	return out
}

// maxRmPaths is how many paths rm takes before it is a removal in mass.
const maxRmPaths = 20

// rmFamily are rm and the removal commands of PowerShell: the alto and
// medio rows of rm hold for them as well (only rm itself has the baixo).
var rmFamily = map[string]bool{"rm": true, "del": true, "erase": true, "remove-item": true, "ri": true, "rd": true, "rmdir": true}

// riskCmd is one simple command as the table sees it.
type riskCmd struct {
	name  string   // canonical program name
	args  []string // the arguments
	forms []string // the texts deny and ask patterns see
	text  string   // as written, without the wrappers

	bareEnv          bool
	github           bool
	urls             int
	allInDomains     bool
	removes          bool
	rmOutside        bool
	rmMass           bool
	scriptInWorktree bool
}

// match is the first row of the table the command matches.
func (c *riskCmd) match() (riskRow, bool) {
	for _, r := range riskTable {
		if r.pattern == "" {
			if r.cond(c) {
				return r, true
			}
			continue
		}
		if !c.matches(r.pattern, r.risk != RiskLow) {
			continue
		}
		if r.cond != nil && !r.cond(c) {
			continue
		}
		if r.lower != nil && r.lower(c) {
			r.risk, r.label = RiskLow, "para domínios das regras efetivas"
		}
		return r, true
	}
	return riskRow{}, false
}

// matches is the deny matching of commands: every form of the command; the
// alto and medio rows without a difference of case (the SQL ones, and so
// that only a higher risk comes of it), the baixo ones as written and by
// the program's base name.
func (c *riskCmd) matches(pattern string, fold bool) bool {
	forms := c.forms
	if !fold {
		forms = forms[:min(2, len(forms))]
	} else {
		pattern = strings.ToLower(pattern)
	}
	for _, f := range forms {
		if fold {
			f = strings.ToLower(f)
		}
		if matchText(pattern, f) {
			return true
		}
	}
	return false
}

// line classifies a command line: the highest risk of its parts, and at
// least medio for what cannot be analysed.
func (w *riskWalker) line(cmd, cwd string, d dialect, depth int) riskOut {
	if depth > maxDepth {
		return riskOut{risk: RiskHigh, rule: "comando aninhado demais para ser analisado"}
	}
	parts, opaque, grouped := parseLine(cmd, d)
	if len(parts) == 0 {
		return riskOut{risk: RiskMedium, rule: "nenhum comando reconhecido"}
	}
	var out riskOut
	st := &shellState{cwds: []string{cwd}, facts: newLineFacts()}
	for i := range parts {
		out.raise(w.part(&parts[i], d, st, depth))
		w.e.noteEffects(&parts[i], d, st)
		if st.pending != nil {
			if parts[i].next == "&&" && !grouped {
				st.cwds = st.pending
			} else {
				st.cwds = append(st.cwds, st.pending...)
			}
			st.pending = nil
		}
	}
	if len(opaque) > 0 {
		out.raise(riskOut{risk: RiskMedium, rule: "trecho que o motor não analisa: " + opaque[0]})
	}
	return out
}

func (w *riskWalker) part(p *simple, d dialect, st *shellState, depth int) riskOut {
	var out riskOut
	if len(p.words) > 0 {
		out = w.command(p.words, d, st, depth, nil)
	}
	for _, s := range p.subs {
		out.raise(w.line(s, st.cwds[0], dialectPOSIX, depth+1))
		if w.e.windows {
			out.raise(w.line(s, st.cwds[0], dialectPwsh, depth+1))
		}
		out.raise(riskOut{risk: RiskMedium, rule: "trecho que o motor não analisa: substituição de comando"})
	}
	for _, a := range p.assigns {
		if programEnv[strings.ToUpper(a.text[:a.assign])] {
			out.raise(riskOut{risk: RiskMedium, rule: fmt.Sprintf("%s muda o programa que roda", a.text[:a.assign])})
		}
	}
	for _, r := range p.redirs {
		if r.op == "<<" || r.op == "<<<" || isDevice(r.target.text) {
			continue
		}
		out.raise(w.wordRisk(r.target, st))
	}
	if len(p.opaque) > 0 {
		out.raise(riskOut{risk: RiskMedium, rule: "trecho que o motor não analisa: " + p.opaque[0]})
	}
	if !out.set() {
		out = riskOut{risk: RiskLow, rule: "parte sem comando e sem sinal de risco"}
	}
	return out
}

// command classifies one simple command; outer are the texts of the
// wrappers around it.
func (w *riskWalker) command(ws []word, d dialect, st *shellState, depth int, outer []string) riskOut {
	e := w.e
	cn := canonical(ws[0].text)
	args := ws[1:]
	texts := wordsText(ws)
	c := &riskCmd{name: cn, args: texts[1:], text: strings.Join(texts, " ")}
	c.forms = []string{c.text, strings.Join(append([]string{cn}, texts[1:]...), " ")}
	if cn == "git" {
		// git clean, git apply, git am… after global options (git -C .
		// apply, git -c k=v am): the table and the signals see git <sub>.
		if sub := gitSubText(args); sub != "" {
			c.forms = append(c.forms, sub)
		}
	}
	c.forms = append(c.forms, outer...)

	// A program made earlier in the same line is unanalysable; a link or
	// copy of one is classified as that program.
	if written, targets := e.madeProgram(cn, ws, st); written || len(targets) > 0 {
		var out riskOut
		for _, k := range targets {
			out.raise(w.command(asProgram(k, ws), d, st, depth+1, append(outer, c.text)))
		}
		if written {
			out.raise(riskOut{risk: RiskMedium, rule: "programa criado antes na mesma linha: " + c.text})
		}
		return out
	}

	unread := ""
	if inner, extra, ok := e.unwrap(cn, args, &result{}); ok {
		if len(inner) > 0 {
			out := w.command(inner, d, st, depth, append(outer, c.text))
			if extra != "" {
				out.raise(riskOut{risk: RiskMedium, rule: "trecho que o motor não analisa: " + extra})
			}
			return out
		}
		c.bareEnv = cn == "env"
		unread = extra
	}
	if cn == "eval" || cn == "invoke-expression" || cn == "iex" {
		return riskOut{risk: RiskHigh, rule: "shell aninhado: " + cn}
	}
	w.scan(c, ws, st)
	if !c.scriptInWorktree {
		if nested, nd, why, ok := nestedShell(cn, args); ok {
			if nested == "" || illegible(nested, nd) {
				return riskOut{risk: RiskHigh, rule: "shell aninhado de texto ilegível: " + why}
			}
			out := w.line(nested, st.cwds[0], nd, depth+1)
			out.raise(riskOut{risk: RiskMedium, rule: "trecho que o motor não analisa: " + why})
			return out
		}
	}

	uses, readOnly := pathUses(cn, args, d)
	switch {
	case cn == "git":
		readOnly = gitReadOnly(args)
	case cn == "find":
		readOnly = !findActs(c.args)
	case cdNames[cn]:
		readOnly, uses = true, nil
	}
	if strings.ContainsAny(ws[0].text, `/\`) || ws[0].quoted || ws[0].expand {
		readOnly = false // only the program the shell finds by its name is the read-only one
	}

	var out riskOut
	if a, ok := w.adjusted(c); ok {
		out = a
	} else if r, ok := c.match(); ok {
		out = riskOut{risk: r.risk, rule: r.rule()}
		out.raise(w.floors(c))
	} else if readOnly {
		// cd, and the commands the format frees as read-only: they act on
		// nothing but the paths they read, which the rule below sees.
		out = riskOut{risk: RiskLow, rule: "comando só de leitura: " + cn}
	} else if s, ok := w.signal(c, ws, st); ok {
		out = s
	} else {
		out = riskOut{risk: RiskLow, rule: fmt.Sprintf("%q sem linha na tabela e sem sinal de risco", c.text)}
	}
	if unread != "" {
		out.raise(riskOut{risk: RiskMedium, rule: "trecho que o motor não analisa: " + unread})
	}

	// The literal paths the command touches, and the program itself when
	// given by a path.
	if strings.ContainsAny(ws[0].text, `/\`) || strings.HasPrefix(ws[0].text, ".") && cn != "." {
		out.raise(w.wordRisk(ws[0], st))
	}
	if cdNames[cn] {
		for _, a := range args {
			if !strings.HasPrefix(a.text, "-") {
				out.raise(w.wordRisk(a, st))
				break
			}
		}
		e.chdir(cn, args, st)
		return out
	}
	for _, u := range uses {
		out.raise(w.wordRisk(u.w, st))
	}
	for _, v := range valuePaths(args) {
		out.raise(w.wordRisk(v, st))
	}
	// A script of the worktree is the project's only if it came from the
	// repository; over the rows and the adjustments, like the paths.
	if out.risk.rank() < RiskMedium.rank() {
		if s, ok := w.script(cn, ws, st); ok {
			out.raise(w.scriptRisk(s, st))
		}
	}
	return out
}

// adjusted is the first of the owner's command adjustments that matches.
func (w *riskWalker) adjusted(c *riskCmd) (riskOut, bool) {
	if w.adj == nil {
		return riskOut{}, false
	}
	for _, a := range w.adj.Items {
		if a.Command == "" {
			continue
		}
		for _, f := range c.forms {
			if matchText(a.Command, f) {
				return riskOut{risk: a.Risk, rule: a.rule(), adj: a.Index}, true
			}
		}
	}
	return riskOut{}, false
}

// scan fills what the condition rows of the table ask about the command.
func (w *riskWalker) scan(c *riskCmd, ws []word, st *shellState) {
	c.allInDomains = true
	for _, a := range c.args {
		l := strings.ToLower(a)
		if strings.Contains(l, "://") {
			c.urls++
			host := ""
			if u, err := url.Parse(a); err == nil {
				host = u.Hostname()
			}
			if githubHost(host) {
				c.github = true
			}
			if host == "" || githubHost(host) || !w.inDomains(host) {
				c.allInDomains = false
			}
		}
		if strings.HasPrefix(l, "github.com/") || strings.HasPrefix(l, "api.github.com/") || strings.Contains(l, "@github.com:") {
			c.github = true
		}
	}
	args := ws[1:]
	if rmFamily[c.name] {
		c.removes = true
		var ops []word
		for i, a := range args {
			if a.text == "--" {
				ops = append(ops, args[i+1:]...)
				break
			}
			if !strings.HasPrefix(a.text, "-") || a.text == "-" {
				ops = append(ops, a)
			}
		}
		c.rmMass = hasShortFlag(args, 'r', 'R') || hasFlag(args, "--recursive") || hasFlagFold(args, "-recurse") || len(ops) > maxRmPaths
		for _, o := range ops {
			if o.glob {
				c.rmMass = true
			}
			if !w.inWorktree(o, st) {
				c.rmOutside = true
			}
		}
	}
	if a, ok := interpreterScript(c.name, args); ok && !a.glob && w.inWorktree(a, st) {
		c.scriptInWorktree = true
	}
}

// groupChangedScript is the signal of a script of the worktree that is not
// the repository's: created or changed at the table, it is not a "script of
// the project" and is medio (ADR 0025).
const groupChangedScript = "script alterado na mesa"

// script is the file of the worktree a command runs as a script: the
// program given by a path, or the first argument of an interpreter.
func (w *riskWalker) script(cn string, ws []word, st *shellState) (word, bool) {
	if p := ws[0]; !p.expand && (strings.ContainsAny(p.text, `/\`) || strings.HasPrefix(p.text, ".") && cn != ".") {
		return p, w.inWorktree(p, st)
	}
	if p := ws[0]; !p.expand && looksLikeScriptFile(p.text) && w.inWorktree(p, st) {
		// A bare script file run as a program (uv run x.py unwrapped to
		// x.py): the launcher does not launder a changed script.
		return p, true
	}
	if a, ok := interpreterScript(cn, ws[1:]); ok && !a.glob && w.inWorktree(a, st) {
		return a, true
	}
	return word{}, false
}

// scriptRisk is medio for a script that differs from the base of the table
// branch, or that the motor cannot check (no git state that tells it).
func (w *riskWalker) scriptRisk(a word, st *shellState) riskOut {
	e := w.e
	changed := riskOut{risk: RiskMedium, rule: "sinal de " + groupChangedScript + ": " + a.text}
	ts, ok := e.env.Git.(TableScripts)
	if !ok {
		return changed
	}
	ks, ok := w.resolve(a, st)
	if !ok {
		return changed
	}
	for _, k := range ks {
		if c, err := ts.Changed(e.ctx, e.env.Worktree, filepath.FromSlash(k)); err != nil || c {
			return changed
		}
	}
	return riskOut{}
}

// inWorktree reports whether a literal path argument surely lies in the
// worktree: an expansion, or a path after a cd to an unknown folder, does
// not.
func (w *riskWalker) inWorktree(a word, st *shellState) bool {
	ks, ok := w.resolve(a, st)
	if !ok {
		return false
	}
	for _, k := range ks {
		if _, inside := w.e.rel(k); !inside {
			return false
		}
	}
	return true
}

func (w *riskWalker) resolve(a word, st *shellState) ([]string, bool) {
	if a.expand || a.text == "" {
		return nil, false
	}
	text := a.text
	if a.tilde {
		if w.e.env.Home == "" {
			return nil, false
		}
		text = w.e.env.Home + text[1:]
	}
	return w.e.resolve(text, st)
}

// wordRisk is the floor a path argument puts on its part.
func (w *riskWalker) wordRisk(a word, st *shellState) riskOut {
	if a.expand || a.text == "" || a.text == "{}" || strings.Contains(a.text, "://") {
		return riskOut{} // an expansion makes the part opaque; a URL is not a path
	}
	ks, ok := w.resolve(a, st)
	if !ok {
		return riskOut{risk: RiskMedium, rule: "caminho que o motor não resolve: " + a.text}
	}
	var out riskOut
	for _, k := range ks {
		out.raise(w.pathRisk(k))
	}
	return out
}

// illegible reports a nested script whose program the motor cannot read:
// its first command is an expansion ("$CMD").
func illegible(nested string, d dialect) bool {
	parts, _, _ := parseLine(nested, d)
	if len(parts) == 0 {
		return true
	}
	for _, p := range parts {
		if len(p.words) > 0 && p.words[0].expand {
			return true
		}
	}
	return false
}
