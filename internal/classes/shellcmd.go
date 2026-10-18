package classes

import (
	"encoding/base64"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"
)

// maxDepth bounds the nesting of shells and substitutions analysed; deeper
// is denied.
const maxDepth = 4

// shellState follows the folder a command is in across its parts. After a
// cd, a relative path is checked against the folder before and after it (a
// cd inside a pipeline or after || may not happen), and must pass in all.
type shellState struct {
	cwds    []string
	unknown bool
	pending []string // where the cd of the current part goes
	// facts are what earlier parts of the line wrote, linked and copied: a
	// program made earlier in the line is unanalysable for the rest of it
	// (docs/classes/classificacao-de-risco.md). Nil outside the decision
	// (the risk walker keeps its own).
	facts *lineFacts
	// feed is the legible literal a pipe feeds the current part, which a
	// shell reading its input runs (pipefeed.go); nil otherwise.
	feed *string
}

// shell analyses a command line. On Windows it is analysed as POSIX and as
// PowerShell, and the most restrictive result holds.
func (e *evaluator) shell(cmd, cwd string, depth int) result {
	res := newResult()
	if depth > maxDepth {
		res.fixedDeny("comando aninhado demais para ser analisado")
		return res
	}
	if depth == 0 && !analysable(cmd) {
		res.fixedDeny(reasonTooLong)
		return res
	}
	res.merge(e.shellIn(cmd, cwd, dialectPOSIX, depth))
	if e.windows {
		res.merge(e.shellIn(cmd, cwd, dialectPwsh, depth))
	}
	return res
}

func (e *evaluator) shellIn(cmd, cwd string, d dialect, depth int) result {
	res := newResult()
	if e.spend(len(cmd) + costLine) {
		res.fixedDeny(reasonTooMuch)
		return res
	}
	if hasControlByte(cmd) {
		res.fixedDeny(reasonControl)
		return res
	}
	// What a part does to the home of git holds for the parts after it in this
	// text, and for the texts it runs, not for the one around it.
	set := e.homeSet
	defer func() { e.homeSet = set }()
	parts, opaque, grouped := parseLine(cmd, d)
	for _, bound := range []string{braceTooLarge, reasonTooDeep} {
		if slices.Contains(opaque, bound) || partsHold(parts, bound) {
			res.fixedDeny(bound)
		}
	}
	if len(parts) == 0 {
		if strings.TrimSpace(cmd) != "" {
			res.atLeastAsk("nenhum comando reconhecido")
		}
		return res
	}
	st := &shellState{cwds: []string{cwd}, facts: newLineFacts()}
	for i := range parts {
		if e.spend(costPart) {
			res.fixedDeny(reasonTooMuch)
			return res
		}
		// A prefix assignment of HOME (and the like) is for the command it
		// prefixes, and for what that command runs.
		scope := e.homeScope
		if n := assignedHome(&parts[i]); n != "" && len(parts[i].words) > 0 {
			e.homeScope = n
		}
		res.merge(e.part(&parts[i], d, st, depth))
		res.merge(e.stdinBodies(parts, i, st, depth))
		res.merge(e.pipeDecision(parts, i, d, st, depth))
		e.homeScope = scope
		e.noteHome(&parts[i])
		e.noteEffects(&parts[i], d, st)
		if st.pending != nil {
			// cd x && …, in a line without subshells or pipes: the rest
			// surely runs in x. Otherwise in either folder.
			if parts[i].next == "&&" && !grouped {
				st.cwds = st.pending
			} else {
				st.cwds = append(st.cwds, st.pending...)
			}
			st.pending = nil
		}
	}
	if len(opaque) > 0 {
		res.atLeastAsk(opaque[0])
	}
	return res
}

func (e *evaluator) part(p *simple, d dialect, st *shellState, depth int) result {
	res := newResult()
	for _, s := range p.subs {
		res.merge(e.shell(s, st.cwds[0], depth+1))
		res.atLeastAsk("substituição de comando")
	}
	for _, a := range p.assigns {
		e.gitEnv(&res, a.text[:a.assign])
	}
	if n := pwshEnvAssign(p); n != "" {
		e.gitEnv(&res, n)
	}
	for _, r := range p.redirs {
		if r.op == "<<" || r.op == "<<<" || isDevice(r.target.text) {
			continue
		}
		res.merge(e.argPath(r.target, use{write: r.writes()}, st))
	}
	if len(p.opaque) > 0 {
		res.atLeastAsk(p.opaque[0])
	}
	if len(p.words) > 0 {
		res.merge(e.command(p.words, d, st, depth, nil))
	}
	return res
}

// isDevice reports the redirection targets that are not files.
func isDevice(t string) bool {
	switch strings.ToLower(t) {
	case "/dev/null", "/dev/stdout", "/dev/stderr", "/dev/tty", "nul", "$null":
		return true
	}
	return strings.HasPrefix(t, "/dev/fd/")
}

// canonical is the name of a program as the fixed rules see it: the base
// name, lower case, without the Windows extensions.
func canonical(name string) string {
	n := name
	if i := strings.LastIndexAny(n, `/\`); i >= 0 {
		n = n[i+1:]
	}
	n = strings.ToLower(n)
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com", ".ps1"} {
		n = strings.TrimSuffix(n, ext)
	}
	return n
}

func wordsText(ws []word) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.text
	}
	return out
}

var (
	elevation = map[string]bool{"sudo": true, "sudoedit": true, "su": true, "doas": true, "pkexec": true, "run0": true, "please": true, "pleaser": true, "runuser": true, "runas": true, "gsudo": true, "psexec": true, "psexec64": true, "nsudo": true, "nsudolc": true, "nsudolg": true}
	startProc = map[string]bool{"start-process": true, "saps": true, "start": true}
	agentCLIs = map[string]bool{"claude": true, "codex": true, "agy": true}
	shells    = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "ash": true, "mksh": true, "csh": true, "tcsh": true, "rbash": true, "yash": true, "nu": true, "elvish": true, "xonsh": true, "posh": true, "oksh": true, "loksh": true, "pdksh": true, "lksh": true}
	pwshNames = map[string]bool{"pwsh": true, "powershell": true}
	// interpreters run a file given as an argument.
	interpreters = map[string]bool{
		"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "ash": true, "csh": true, "tcsh": true, "mksh": true, "rbash": true, "yash": true, "nu": true, "elvish": true, "xonsh": true, "posh": true, "oksh": true, "loksh": true, "pdksh": true, "lksh": true,
		"source": true, ".": true, "python": true, "python2": true, "python3": true, "py": true, "node": true, "nodejs": true,
		"deno": true, "bun": true, "ruby": true, "perl": true, "php": true, "lua": true, "rscript": true, "java": true,
		"pwsh": true, "powershell": true, "cmd": true, "wscript": true, "cscript": true, "mshta": true, "rundll32": true,
		"regsvr32": true, "msiexec": true, "start-process": true, "saps": true, "start": true, "invoke-item": true, "ii": true,
		"osascript": true, "tsx": true, "ts-node": true, "xdg-open": true, "open": true, "exec": true, "go": true,
	}
	permissionTools = map[string]bool{"chmod": true, "chown": true, "chgrp": true, "setfacl": true, "icacls": true, "cacls": true, "attrib": true, "takeown": true, "unblock-file": true, "set-acl": true}
	gitRepoEnv      = map[string]bool{"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_COMMON_DIR": true, "GIT_INDEX_FILE": true, "GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true, "GIT_NAMESPACE": true}
	cdNames         = map[string]bool{"cd": true, "pushd": true, "popd": true, "chdir": true, "set-location": true, "sl": true, "push-location": true, "pop-location": true}
)

// programEnv are the variables that change which program runs, or what it
// loads: with one of them set, the name of a command no longer says what it
// is, so the part is at least ask.
var programEnv = map[string]bool{
	"PATH": true, "LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "LD_AUDIT": true, "BASH_ENV": true, "ENV": true,
	"NODE_OPTIONS": true, "PYTHONSTARTUP": true, "PYTHONPATH": true, "PERL5OPT": true, "RUBYOPT": true, "GIT_EXEC_PATH": true,
	"DYLD_INSERT_LIBRARIES": true, "DYLD_LIBRARY_PATH": true, "PSMODULEPATH": true, "PATHEXT": true,
	// programs git and the read-only commands start by themselves
	"GIT_PAGER": true, "PAGER": true, "GIT_EXTERNAL_DIFF": true, "GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_EDITOR": true,
	"EDITOR": true, "VISUAL": true, "GIT_ASKPASS": true, "SSH_ASKPASS": true, "LESSOPEN": true, "LESSCLOSE": true,
	"GIT_SEQUENCE_EDITOR": true, "GIT_PROXY_COMMAND": true, "GIT_TEMPLATE_DIR": true,
	// ripgrep reads its arguments (--pre among them) from the file this names
	"RIPGREP_CONFIG_PATH": true,
}

// gitEnv checks an assignment: one that points git at another repository is
// a fixed denial; one that changes the program that runs is at least ask.
func (e *evaluator) gitEnv(res *result, name string) {
	n := strings.ToUpper(name)
	if gitRepoEnv[n] || strings.HasPrefix(n, "GIT_CONFIG") {
		res.fixedDeny(fmt.Sprintf("%s aponta o git para fora do worktree", name))
	}
	if programEnv[n] {
		res.atLeastAsk(fmt.Sprintf("%s muda o programa que roda", name))
	}
}

// command analyses one simple command; outer are the texts of the wrappers
// around it, which deny and ask patterns also see.
func (e *evaluator) command(ws []word, d dialect, st *shellState, depth int, outer []string) result {
	res := newResult()
	scope := e.homeScope // what an env around the command sets is for it alone
	defer func() { e.homeScope = scope }()
	name := ws[0]
	cn := canonical(name.text)
	args := ws[1:]
	argText := wordsText(args)

	// The program itself, when given by path.
	if strings.ContainsAny(name.text, `/\`) || strings.HasPrefix(name.text, ".") && cn != "." {
		res.merge(e.argPath(name, use{}, st))
		if e.inAnexos(name, st) {
			res.fixedDeny("anexos nunca são executados")
		}
	}

	// A program made earlier in the same line, or a link or copy of one, is
	// not the program the text shows: a link or copy of a program is
	// analysed as that program; a program written earlier in the line is
	// unanalysable for the rest of it
	// (docs/classes/classificacao-de-risco.md).
	if written, targets := e.madeProgram(cn, ws, st); written || len(targets) > 0 {
		for _, k := range targets {
			res.merge(e.command(asProgram(k, ws), d, st, depth+1, append(outer, strings.Join(wordsText(ws), " "))))
		}
		if written {
			res.atLeastAsk("programa criado antes na mesma linha")
		}
		res.rule(e.commandRule(ws, outer, false))
		return res
	}

	switch {
	case elevation[cn]:
		res.fixedDeny(fmt.Sprintf("%s pede privilégio de outra conta", cn))
	case startProc[cn] && hasRunAs(argText):
		res.fixedDeny("Start-Process -Verb RunAs pede privilégio de administrador")
	case agentCLIs[cn]:
		res.fixedDeny(fmt.Sprintf("%s só nasce pelo motor, nunca pelo shell", cn))
	case agentRunner(cn, args):
		res.fixedDeny("um CLI de agente só nasce pelo motor, nunca pelo shell")
	}
	around := append(outer, strings.Join(wordsText(ws), " "))
	for _, inner := range runnerCommands(cn, args) {
		// npx git push: what a package runner runs is a command, and a fixed
		// rule on it denies (the rest is for the class to decide).
		e.nested(&res, inner, d, st, depth, around, true)
	}
	if inner := e.wrapped(cn, args, &res); len(inner) > 0 {
		// setarch x86_64 git push, cross-env HOME=x git status, corepack npx claude
		e.nested(&res, inner, d, st, depth, around, true)
	}
	if call, ok := runnerCall(cn, args); ok {
		// npx -c 'git push': a command line, as sh -c
		res.merge(e.shell(call, st.cwds[0], depth+1))
		res.atLeastAsk("comando dentro de um argumento de " + cn)
	}
	if line, ok := winCreate(cn, args); ok {
		// wmic process call create "git push"
		res.merge(e.shell(line, st.cwds[0], depth+1))
		res.atLeastAsk("comando dentro de um argumento de " + cn)
	}
	if v := e.homeMoved(); v != "" && isGitProgram(cn) {
		res.fixedDeny(v + " alterado antes do git aponta a configuração global do git para fora")
	}
	if startProc[cn] {
		// The program Start-Process launches is a command of its own.
		cmds, unknown := startCommands(cn, args)
		for _, inner := range cmds {
			e.nested(&res, inner, d, st, depth, around, false)
		}
		if unknown {
			res.atLeastAsk("parâmetro do Start-Process que não se analisa")
		}
	}
	if cn == "." && d == dialectPwsh && len(args) > 0 {
		// . git push: the dot operator runs a command, as & does.
		e.nested(&res, args, d, st, depth, around, false)
	}
	if shellVarBuiltins[cn] || cn == "unset" || pwshEnvSet[cn] || pwshEnvRemove[cn] {
		assigned, removed := varNames(cn, argText)
		for _, n := range assigned {
			e.gitEnv(&res, n)
		}
		for _, n := range removed {
			e.envRemoved(&res, n)
		}
	}

	if inner, extra, ok := e.unwrap(cn, args, &res); ok {
		if len(inner) == 0 {
			res.rule(e.commandRule(ws, outer, false))
			if extra != "" {
				res.atLeastAsk(extra)
			}
			return res
		}
		res.merge(e.command(inner, d, st, depth, append(outer, strings.Join(wordsText(ws), " "))))
		if extra != "" {
			res.atLeastAsk(extra)
		}
		if strings.ContainsAny(name.text, `/\`) || strings.HasPrefix(name.text, ".") || name.quoted || name.expand {
			// ./nohup may be any file: the program itself decides too.
			res.rule(e.commandRule(ws, outer, false))
		}
		return res
	}
	if nested, nd, why, ok := nestedShell(cn, args); ok && !pwshScript(name) {
		if nested == "" {
			res.atLeastAsk(why)
		} else {
			res.merge(e.shellIn(nested, st.cwds[0], nd, depth+1))
			res.atLeastAsk(why)
		}
		res.rule(e.commandRule(ws, outer, false))
		return res
	}

	if interpreters[cn] || permissionTools[cn] {
		for _, a := range args {
			if e.inAnexos(a, st) {
				res.fixedDeny("anexos nunca são executados nem ganham permissão de execução")
				break
			}
		}
	}

	// awk and sed programs that execute a command or write a file: a fixed
	// rule in a literal command denies, a write follows the path rules, and
	// anything dynamic is at least ask (awksed.go).
	if pc, ok := e.progCodeOf(cn, args); ok {
		for _, cmd := range pc.cmds {
			res.merge(e.shell(cmd, st.cwds[0], depth+1))
		}
		for _, wd := range pc.writes {
			res.merge(e.argPath(wd, use{write: true}, st))
		}
		if pc.forcesCode() {
			res.atLeastAsk(pc.why)
		}
	}
	// A command given in an argument (tmux new 'cmd', gdb -ex 'shell cmd',
	// tar -I cmd, python -c 'os.system("cmd")'…): the literal is analysed,
	// so a fixed rule in it denies (codeargs.go).
	for _, ac := range argCommands(cn, argText) {
		res.merge(e.shell(ac, st.cwds[0], depth+1))
		res.atLeastAsk("comando dentro de um argumento de " + cn)
	}

	readOnly := false
	switch {
	case cn == "git":
		res.merge(e.git(args, st, depth))
		readOnly = gitReadOnly(args)
	case strings.HasPrefix(cn, "git-") && gitDashedFixed(cn[len("git-"):]):
		// A dashed git form (git-push, git-receive-pack, git-remote-ext):
		// analyse it as "git <sub> <rest>" so the fixed rules deny it.
		inner := append([]word{{text: cn[len("git-"):], assign: -1}}, args...)
		res.merge(e.git(inner, st, depth))
	case cn == "find":
		res.merge(e.find(args, d, st, depth))
		readOnly = !findActs(argText)
	case cdNames[cn]:
		res.merge(e.chdir(cn, args, st))
		readOnly = cn != "popd" && cn != "pop-location"
	default:
		uses, ro := pathUses(cn, args, d)
		readOnly = ro
		if d == dialectPwsh && len(uses) == 0 && pwshKind(cn) != "" {
			res.atLeastAsk("caminhos vindos do pipeline")
		}
		for _, u := range uses {
			res.merge(e.argPath(u.w, u.use, st))
		}
	}
	if cn == "mestre" {
		// The CLI of the Mestre: the engine checks each call against the
		// caller's level and cli.allow (docs/cli/convencoes.md).
		readOnly = true
	}
	if strings.ContainsAny(name.text, `/\`) || name.quoted || name.expand {
		// Only the program the shell finds by its name is the read-only
		// one: tests/ls may be anything.
		readOnly = false
	}
	res.rule(e.commandRule(ws, outer, readOnly))
	return res
}

// nested analyses the command another one runs (a launcher, a package
// runner, the dot operator, a wrapper): it costs work and nests, so a chain
// of them is bounded and cannot grow the analysis exponentially. With
// fixedOnly only what the fixed rules say of it counts; the rest is for the
// class to decide on the wrapper.
func (e *evaluator) nested(res *result, inner []word, d dialect, st *shellState, depth int, around []string, fixedOnly bool) {
	if depth >= maxDepth {
		res.fixedDeny("comando aninhado demais para ser analisado")
		return
	}
	if e.spend(costNested) {
		res.fixedDeny(reasonTooMuch)
		return
	}
	r := e.command(inner, d, st, depth+1, around)
	if fixedOnly {
		res.fixed.worse(r.fixed)
		return
	}
	res.merge(r)
}

// commandRule applies the decrees and the commands block to a simple
// command. Allow patterns see the command as written; deny and ask see it
// also with the program's base name and with the wrappers around it.
func (e *evaluator) commandRule(ws []word, outer []string, readOnly bool) outcome {
	texts := wordsText(ws)
	text := strings.Join(texts, " ")
	forms := []string{text}
	if n := canonical(texts[0]); n != texts[0] {
		forms = append(forms, strings.Join(append([]string{n}, texts[1:]...), " "))
	}
	forms = append(forms, outer...)
	matchAny := func(p string) bool {
		for _, f := range forms {
			if matchText(p, f) {
				return true
			}
		}
		return false
	}
	return e.decreeSplit(func(g *Grant) []string { return g.Commands }, matchAny, func(p string) bool { return matchText(p, text) },
		func() outcome { return e.patterns("commands", e.r.Commands, forms, text, readOnly) })
}

func hasRunAs(args []string) bool {
	for _, a := range args {
		l := strings.ToLower(strings.Trim(a, `"'`))
		if l == "runas" || strings.HasSuffix(l, ":runas") {
			return true
		}
	}
	return false
}

// unwrap returns the command a transparent wrapper runs (nice, env,
// timeout…) or one whose arguments come from elsewhere (xargs: then extra
// says why it is at least ask). ok is false for anything else.
func (e *evaluator) unwrap(cn string, args []word, res *result) (inner []word, extra string, ok bool) {
	skip := func(withValue map[string]bool) []word {
		i := 0
		for i < len(args) {
			a := args[i].text
			if a == "--" {
				i++
				break
			}
			if !strings.HasPrefix(a, "-") || a == "-" {
				break
			}
			if withValue[a] {
				i++
			}
			i++
		}
		return args[min(i, len(args)):] // an option's value may be missing (xargs -I)
	}
	switch cn {
	case "nohup", "builtin", "setsid":
		return skip(nil), "", true
	case "command":
		for _, a := range args {
			if a.text == "-v" || a.text == "-V" {
				return nil, "", true
			}
		}
		return skip(nil), "", true
	case "exec":
		return skip(map[string]bool{"-a": true}), "", true
	case "nice":
		return skip(map[string]bool{"-n": true, "--adjustment": true}), "", true
	case "time":
		rest := skip(map[string]bool{"-f": true, "-o": true, "--format": true, "--output": true})
		return rest, "", true
	case "ionice":
		return skip(map[string]bool{"-c": true, "-n": true, "-p": true, "-P": true, "-u": true, "--class": true, "--classdata": true}), "", true
	case "stdbuf":
		return skip(map[string]bool{"-i": true, "-o": true, "-e": true, "--input": true, "--output": true, "--error": true}), "", true
	case "timeout":
		rest := skip(map[string]bool{"-s": true, "-k": true, "--signal": true, "--kill-after": true})
		if len(rest) > 0 {
			rest = rest[1:] // the duration
		}
		return rest, "", true
	case "env":
		inner, extra := e.envInner(args, res)
		return inner, extra, true
	case "xargs":
		rest := skip(map[string]bool{"-a": true, "-d": true, "-E": true, "-I": true, "-L": true, "-n": true, "-P": true, "-s": true, "--arg-file": true, "--delimiter": true, "--max-args": true, "--max-procs": true, "--max-chars": true, "--max-lines": true, "--replace": true})
		if len(rest) == 0 {
			return nil, "", false
		}
		return rest, "argumentos vindos do xargs", true
	}
	if inner, ok := runnerUnwrap(cn, args); ok {
		return inner, "", true
	}
	if multiCall(cn) {
		// The applet named by the first argument is the program.
		switch {
		case len(args) == 0:
			return nil, cn + " sem o programa", true
		case strings.HasPrefix(args[0].text, "--coreutils-prog="):
			applet := args[0]
			applet.text = strings.TrimPrefix(applet.text, "--coreutils-prog=")
			return append([]word{applet}, args[1:]...), "", true
		case strings.HasPrefix(args[0].text, "-"):
			return nil, "opção de " + cn, true
		}
		return args, "", true
	}
	return nil, "", false
}

// multiCallBases are the binaries that hold many programs and run the one
// their first argument names (busybox rm, toybox sh), by exact name.
var multiCallBases = map[string]bool{"busybox": true, "toybox": true, "coreutils": true, "sbase-box": true, "ubase-box": true}

// multiCall reports a multi-call binary, by exact name or by the name
// variants a packager gives busybox and toybox: a suffix after a separator
// or a version (busybox.static, busybox-static, busybox_1_36, toybox-x).
func multiCall(cn string) bool {
	if multiCallBases[cn] {
		return true
	}
	for _, base := range []string{"busybox", "toybox"} {
		if rest, ok := strings.CutPrefix(cn, base); ok && rest != "" {
			if c := rest[0]; c == '.' || c == '-' || c == '_' {
				return true
			}
			if c := rest[0]; c >= '0' && c <= '9' {
				return true // a version suffix (busybox1, busybox136)
			}
		}
	}
	return false
}

// nestedShell returns the command text a shell, eval or cmd runs, its
// dialect and why it is at least ask; nested is "" when the text is not
// known (a script read from the input).
func nestedShell(cn string, args []word) (nested string, d dialect, why string, ok bool) {
	texts := wordsText(args)
	switch {
	case shells[cn]:
		return shellText(cn, texts)
	case cn == "eval":
		return strings.Join(texts, " "), dialectPOSIX, "eval", true
	case cn == "invoke-expression" || cn == "iex":
		cmd := strings.Join(texts, " ")
		for i, a := range texts {
			if strings.EqualFold(a, "-command") && i+1 < len(texts) {
				cmd = strings.Join(texts[i+1:], " ")
			}
		}
		return cmd, dialectPwsh, "Invoke-Expression", true
	case cn == "invoke-command" || cn == "icm":
		return "", dialectPwsh, "Invoke-Command", true
	case pwshNames[cn]:
		for i := 0; i < len(texts); i++ {
			l := strings.ToLower(pwshDashFix(texts[i])) // pwsh –Command: the en dash is a dash
			switch {
			case l == "-encodedcommand" || l == "-enc" || l == "-e" || l == "-ec":
				if i+1 < len(texts) {
					return decodePwsh(texts[i+1]), dialectPwsh, cn + " -EncodedCommand", true
				}
				return "", dialectPwsh, cn + " -EncodedCommand", true
			case l == "-command" || l == "-c" || l == "-com" || l == "-comm" || l == "-comma" || l == "-comman":
				if i+1 < len(texts) && texts[i+1] != "-" {
					return strings.Join(texts[i+1:], " "), dialectPwsh, cn + " -Command", true
				}
				return "", dialectPwsh, cn + " lendo o script da entrada", true
			case l == "-file" || l == "-f":
				return "", 0, "", false
			case l == "-executionpolicy" || l == "-ex" || l == "-ep" || l == "-windowstyle" || l == "-w" || l == "-outputformat" || l == "-of" || l == "-inputformat" || l == "-if" || l == "-configurationname" || l == "-version" || l == "-v" || l == "-workingdirectory" || l == "-wd" || l == "-settingsfile":
				i++
			case strings.HasPrefix(l, "-"):
			default:
				return strings.Join(texts[i:], " "), dialectPwsh, cn + " com comando", true
			}
		}
		return "", dialectPwsh, cn + " lendo o script da entrada", true
	case cn == "cmd":
		for i, a := range texts {
			l := strings.ToLower(a)
			if l == "/c" || l == "/k" || l == "/r" {
				return strings.Join(texts[i+1:], " "), dialectPwsh, "cmd " + l, true
			}
		}
		return "", dialectPwsh, "cmd lendo da entrada", true
	case cn == "wsl" || cn == "watch" || cn == "script" || cn == "parallel":
		// Their options with a value; the rest is the command they run.
		valued := map[string]bool{"-n": true, "--interval": true, "-d": true, "--distribution": true, "-u": true, "--user": true, "--cd": true, "-c": true, "--command": true, "-j": true, "--jobs": true}
		var rest []string
		for i := 0; i < len(texts); i++ {
			if t := texts[i]; strings.HasPrefix(t, "-") && len(rest) == 0 {
				if valued[t] && t != "-c" && t != "--command" {
					i++
				}
				continue
			}
			rest = append(rest, texts[i])
		}
		if cn == "parallel" {
			return parallelText(rest), dialectPOSIX, cn, true
		}
		return strings.Join(rest, " "), dialectPOSIX, cn, true
	}
	return "", 0, "", false
}

func operands(texts []string) []string {
	var out []string
	for _, t := range texts {
		if !strings.HasPrefix(t, "-") {
			out = append(out, t)
		}
	}
	return out
}

// decodePwsh decodes the base64 UTF-16LE of -EncodedCommand; what does not
// decode is analysed as is.
func decodePwsh(s string) string {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b)%2 != 0 {
		return s
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

// inAnexos reports whether an argument is a file of .mestre/anexos/.
func (e *evaluator) inAnexos(w word, st *shellState) bool {
	t := strings.ToLower(strings.ReplaceAll(w.text, `\`, "/"))
	if strings.Contains("/"+t, "/.mestre/anexos/") {
		return true
	}
	ks, _ := e.resolve(w.text, st)
	for _, k := range ks {
		if strings.Contains(strings.ToLower(k)+"/", "/.mestre/anexos/") {
			return true
		}
	}
	return false
}

// resolve makes a command argument absolute against the candidate folders.
func (e *evaluator) resolve(text string, st *shellState) ([]string, bool) {
	if text == "" {
		return nil, false
	}
	norm := func(p string) string {
		if e.env.Normalize != nil {
			p = e.env.Normalize(p, "")
		}
		return e.key(p)
	}
	if filepath.IsAbs(text) || isAbsSlash(strings.ReplaceAll(text, `\`, "/")) {
		return []string{norm(text)}, true
	}
	if e.windows && (strings.HasPrefix(text, `\`) || strings.HasPrefix(text, "/")) {
		return []string{norm(filepath.VolumeName(e.env.Worktree) + text)}, true
	}
	if st.unknown {
		return nil, false
	}
	out := make([]string, 0, len(st.cwds))
	for _, c := range st.cwds {
		// Not filepath.Join: it would clean "link/.." away before Normalize
		// resolves the link.
		out = append(out, norm(strings.TrimRight(c, `/\`)+string(filepath.Separator)+text))
	}
	return out, true
}

// argPath checks a path argument of a command or a redirection.
func (e *evaluator) argPath(w word, u use, st *shellState) result {
	res := newResult()
	if w.expand || w.text == "" || w.text == "{}" {
		return res // the part is already at least ask
	}
	text := w.text
	if w.tilde {
		if e.env.Home == "" {
			res.rule(e.deny(LayerClass, fmt.Sprintf("caminho fora do worktree: %s", text)))
			return res
		}
		text = e.env.Home + text[1:]
	}
	if w.glob {
		e.globPath(&res, text, u, st)
		return res
	}
	ks, known := e.resolve(text, st)
	if !known {
		res.atLeastAsk("caminho relativo depois de mudar para uma pasta desconhecida")
		return res
	}
	for _, k := range ks {
		res.merge(e.path(k, u))
	}
	return res
}

// globNames are the protected names a wildcard in the worktree can reach.
var globNames = []string{".git", ".mestre", ".claude", ".codex", ".agents", ".gemini", ".mcp.json", "settings.json", "settings.local.json", "hooks.json", "agents", "commands", "skills"}

// globPath checks a path with wildcards. What the wildcard reaches lies in
// the folder before it, so a file there (named *) stands for it before the
// fixed rules and the files block; a write is also denied when the wildcard
// can name a protected file, and a read or write when it can reach a folder
// of the Mestre. The match is conservative: the files block judges the
// stand-in, not every name the wildcard could take.
func (e *evaluator) globPath(res *result, text string, u use, st *shellState) {
	slash := strings.ReplaceAll(text, `\`, "/")
	first := strings.IndexAny(slash, "*?[")
	dir, rest := ".", slash
	if i := strings.LastIndex(slash[:first], "/"); i >= 0 {
		dir, rest = slash[:i], slash[i+1:]
		if dir == "" {
			dir = "/"
		}
	}
	segs := strings.Split(rest, "/")
	ks, known := e.resolve(dir, st)
	if !known {
		res.atLeastAsk("caminho relativo depois de mudar para uma pasta desconhecida")
		return
	}
	for _, k := range ks {
		child := strings.TrimSuffix(k, "/") + "/*"
		if why := e.fixedPath(child, u); why != "" {
			res.fixedDeny(why)
			return
		}
		for _, d := range append(append([]string{}, e.env.Layout.Data...), e.env.Layout.Accounts, e.env.Layout.Agents) {
			if dk := e.key(d); dk != "" && dk != k && within(dk, k) {
				res.fixedDeny(fmt.Sprintf("o padrão %s pode alcançar as pastas de dados do Mestre", text))
				return
			}
		}
		if relDir, inside := e.rel(k); inside && u.write {
			for _, name := range globNames {
				if !e.segMatch(segs[0], name) {
					continue
				}
				p := strings.TrimPrefix(relDir+"/"+name, "/")
				if len(segs) > 1 {
					p += "/*"
				}
				for _, pp := range protectedWrite {
					if matchGlob(pp, p) || (u.recursive && holdsProtected[p]) {
						res.fixedDeny(fmt.Sprintf("o padrão %s pode alcançar %s", text, p))
						return
					}
				}
			}
		}
		res.rule(e.filePerm(child, u.write))
	}
}

// segMatch reports whether a path segment with wildcards can name name: in
// POSIX shells a wildcard does not take a leading dot unless the pattern
// has it; PowerShell has no such rule.
func (e *evaluator) segMatch(seg, name string) bool {
	if seg == name {
		return true
	}
	if !strings.ContainsAny(seg, "*?[") {
		return false
	}
	ok, _ := path.Match(strings.ToLower(seg), name)
	return ok && (!strings.HasPrefix(name, ".") || strings.HasPrefix(seg, ".") || e.windows)
}

// chdir follows cd and its equivalents.
func (e *evaluator) chdir(cn string, args []word, st *shellState) result {
	res := newResult()
	var target *word
	for i := range args {
		if strings.HasPrefix(args[i].text, "-") && args[i].text != "-" {
			continue // options, and -Path or -LiteralPath before the folder
		}
		target = &args[i]
		break
	}
	if cn == "popd" || cn == "pop-location" || target == nil || target.text == "-" || target.expand || target.glob {
		st.unknown = true
		return res
	}
	res.merge(e.argPath(*target, use{}, st))
	text := target.text
	if target.tilde {
		if e.env.Home == "" {
			st.unknown = true
			return res
		}
		text = e.env.Home + text[1:]
	}
	ks, known := e.resolve(text, st)
	if !known {
		st.unknown = true
		return res
	}
	for _, k := range ks {
		st.pending = append(st.pending, filepath.FromSlash(k))
	}
	return res
}

// pathUse is one path argument and how the command touches it.
type pathUse struct {
	w word
	use
}

// readOnlyNames are the commands allowed by default
// (docs/classes/formato-das-permissoes.md): POSIX and the PowerShell
// equivalents. git status|diff|log|show are in gitReadOnly; cd in cdNames.
var readOnlyNames = map[string]bool{
	"pwd": true, "ls": true, "cat": true, "head": true, "tail": true, "grep": true, "rg": true, "wc": true,
	"get-location": true, "gl": true, "get-childitem": true, "gci": true, "dir": true, "get-content": true, "gc": true,
	"type": true, "select-string": true, "sls": true, "measure-object": true, "measure": true,
}

// patternFirst are the readers whose first operand is a pattern.
var patternFirst = map[string]bool{"grep": true, "rg": true, "select-string": true, "sls": true}

// writeValued are, for the commands that write, the options that take the
// next word as a value that is not a path to write (a mode, a suffix, a
// timestamp). It is per command: touch -m and rsync -o are switches, and one
// that is taken for an option with a value swallows the file after it.
var writeValued = map[string]map[string]bool{
	"touch":    set("-d -t -r --date --reference --time"),
	"mkdir":    set("-m --mode"),
	"truncate": set("-s -r --size --reference"),
	"cp":       set("-t -S --target-directory --suffix"),
	"mv":       set("-t -S --target-directory --suffix"),
	"install":  set("-m -o -g -t -S --mode --owner --group --target-directory --suffix"),
}

// pwshValueParams are PowerShell parameters whose value is not a path.
var pwshValueParams = map[string]bool{
	"-value": true, "-encoding": true, "-itemtype": true, "-type": true, "-stream": true, "-width": true, "-filter": true,
	"-include": true, "-exclude": true, "-pattern": true, "-totalcount": true, "-head": true, "-tail": true, "-first": true,
	"-last": true, "-depth": true, "-delimiter": true, "-readcount": true, "-property": true, "-inputobject": true, "-name": true,
	"-credential": true, "-newname": true, "-context": true,
}

// pwshPathParams are PowerShell parameters whose value is a path.
var pwshPathParams = map[string]bool{"-path": true, "-literalpath": true, "-filepath": true, "-lp": true, "-pspath": true}

// pathUses lists the path arguments of a command and whether it is one of
// the read-only commands.
func pathUses(cn string, args []word, d dialect) (uses []pathUse, readOnly bool) {
	ops := func(withValue map[string]bool) []word {
		var out []word
		for i := 0; i < len(args); i++ {
			a := args[i].text
			if a == "--" {
				out = append(out, args[i+1:]...)
				break
			}
			if strings.HasPrefix(a, "-") && a != "-" {
				if withValue[a] {
					i++
				}
				continue
			}
			out = append(out, args[i])
		}
		return out
	}
	all := func(ws []word, u use) {
		for _, w := range ws {
			uses = append(uses, pathUse{w, u})
		}
	}
	recursive := hasFlag(args, "-r", "-R", "--recursive", "-rf", "-fr", "-Rf", "-fR", "-a", "--archive") || hasShortFlag(args, 'r', 'R', 'a')
	if d == dialectPwsh {
		if u, ok := pwshUses(cn, args); ok {
			return u, false
		}
	}
	switch cn {
	case "rm", "unlink", "shred":
		all(ops(nil), use{write: true, recursive: recursive || cn == "rm" && hasShortFlag(args, 'r', 'R')})
	case "rmdir":
		all(ops(nil), use{write: true, recursive: true})
	case "touch", "mkdir", "truncate":
		all(ops(writeValued[cn]), use{write: true})
	case "tee":
		all(ops(nil), use{write: true})
	case "ln", "link", "mklink":
		// Making a link writes the link and, through it, its target: both
		// are writes, the target read from the link's folder.
		for _, lp := range linkPairs(cn, args) {
			uses = append(uses, pathUse{lp.link, use{write: true}}, pathUse{lp.targetWord(), use{write: true}})
		}
	case "cp", "mv", "install", "rsync":
		o := ops(writeValued[cn])
		var dest []word
		if t := targetDirWord(args); t != nil {
			dest = []word{*t}
		} else if len(o) > 0 {
			dest, o = o[len(o)-1:], o[:len(o)-1]
		}
		move := cn == "mv"
		// cp -s and cp -l make links to the sources instead of copies.
		linked := cn == "cp" && (hasShortFlag(args, 's', 'l') || hasFlag(args, "--symbolic-link", "--link"))
		for _, s := range o {
			uses = append(uses, pathUse{s, use{write: move || linked, recursive: move}})
		}
		for _, dw := range dest {
			uses = append(uses, pathUse{dw, use{write: true, recursive: move || recursive}})
			for _, s := range o {
				// a folder destination takes the source's name inside it
				nw := dw
				nw.text = strings.TrimRight(dw.text, `/\`) + "/" + filepath.Base(filepath.FromSlash(strings.ReplaceAll(s.text, `\`, "/")))
				nw.glob = dw.glob || s.glob
				uses = append(uses, pathUse{nw, use{write: true, recursive: move || recursive}})
			}
		}
	case "chmod", "chown", "chgrp":
		o := ops(nil)
		if flagValue(args, "--reference") == nil && len(o) > 0 {
			o = o[1:]
		}
		all(o, use{write: true, recursive: hasFlag(args, "-R", "--recursive")})
	case "awk", "gawk", "mawk", "nawk", "goawk":
		all(awkFiles(args), use{})
	case "sed":
		inPlace := false
		script := true
		for _, a := range args {
			if strings.HasPrefix(a.text, "-i") || strings.HasPrefix(a.text, "--in-place") || a.text == "-I" {
				inPlace = true
			}
			if a.text == "-e" || a.text == "-f" || strings.HasPrefix(a.text, "--expression") || strings.HasPrefix(a.text, "--file") {
				script = false
			}
		}
		o := ops(map[string]bool{"-e": true, "-f": true, "-l": true})
		if script && len(o) > 0 {
			o = o[1:]
		}
		all(o, use{write: inPlace})
		// The file of a script (-f F) is read, not written by -i.
		if w := flagValue(args, "-f", "--file"); w != nil {
			all([]word{*w}, use{})
		}
	case "dd":
		for _, a := range args {
			if v, ok := strings.CutPrefix(a.text, "of="); ok {
				w := a
				w.text = v
				uses = append(uses, pathUse{w, use{write: true}})
			}
			if v, ok := strings.CutPrefix(a.text, "if="); ok {
				w := a
				w.text = v
				uses = append(uses, pathUse{w, use{}})
			}
		}
	case "gunzip", "gzip", "zcat", "bunzip2", "bzip2", "bzcat", "unxz", "xz", "xzcat",
		"unlzma", "lzma", "unzstd", "zstd", "zstdcat", "unlz4", "lz4", "lzop", "brotli", "uncompress", "compress":
		// A decompressor writes an implicit output: gunzip X.gz writes X. The
		// output is checked against the fixed rules (a protected output is
		// denied). -c/stdout and the *cat forms write nothing.
		for _, w := range decompressorOutputs(cn, args) {
			uses = append(uses, pathUse{w, use{write: true}})
		}
	case "start-process", "saps", "start":
		// The program and its arguments are a command of their own (startCommands).
		uses = startPathUses(args)
	case "tar", "bsdtar", "gtar", "star", "unzip", "7z", "7za", "7zr", "7zz",
		"dpkg-deb", "unsquashfs", "cabextract", "xcopy", "robocopy", "fsutil":
		// Archive extraction and Windows copy tools write a tree under a
		// destination directory (or a link); a destination that includes a
		// protected path is denied.
		uses = append(uses, extractDest(cn, args)...)
	default:
		if readOnlyNames[cn] {
			// rg, grep -r, ls -R and Get-ChildItem -Recurse read the whole tree.
			deep := cn == "rg" || (cn == "grep" && hasShortFlag(args, 'r', 'R')) || hasFlag(args, "-R", "--recursive") || hasFlagFold(args, "-recurse")
			return readOnlyUses(cn, args, deep)
		}
		// Any other command: the arguments that look like paths are read.
		for _, a := range args {
			t := a.text
			if strings.HasPrefix(t, "-") {
				if _, v, ok := strings.Cut(t, "="); ok && looksLikePath(v) {
					w := a
					w.text = v
					uses = append(uses, pathUse{w, use{}})
				}
				continue
			}
			if looksLikePath(t) {
				uses = append(uses, pathUse{a, use{}})
			}
		}
	}
	return uses, false
}

// pwshKind is the kind of a PowerShell cmdlet that writes files, or "".
func pwshKind(cn string) string {
	switch cn {
	case "set-content", "sc", "add-content", "ac", "out-file", "clear-content", "clc", "new-item", "ni", "set-item", "si":
		return "write"
	case "remove-item", "ri", "rm", "del", "erase", "rd", "rmdir":
		return "remove"
	case "copy-item", "cpi", "cp", "copy":
		return "copy"
	case "move-item", "mi", "mv", "move", "rename-item", "ren", "rni":
		return "move"
	case "expand-archive":
		return "expand"
	}
	return ""
}

// pwshUses covers the PowerShell cmdlets that write files.
func pwshUses(cn string, args []word) ([]pathUse, bool) {
	kind := pwshKind(cn)
	if kind == "" {
		return nil, false
	}
	var named, dest, values, names []word
	var pos []word
	itemType := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		l := strings.ToLower(a.text)
		if strings.HasPrefix(l, "-") && len(l) > 1 {
			name, val, hasVal := strings.Cut(l, ":")
			next := func() (word, bool) {
				if hasVal {
					w := a
					w.text = a.text[len(name)+1:]
					_ = val
					return w, true
				}
				if i+1 < len(args) {
					i++
					return args[i], true
				}
				return word{}, false
			}
			switch {
			case pwshPathParams[name]:
				if w, ok := next(); ok {
					named = append(named, w)
				}
			case name == "-destination" || name == "-destinationpath" || name == "-target":
				if w, ok := next(); ok {
					dest = append(dest, w)
				}
			case name == "-itemtype" || name == "-type":
				if w, ok := next(); ok {
					itemType = strings.ToLower(w.text)
				}
			case name == "-value":
				if w, ok := next(); ok {
					values = append(values, w)
				}
			case name == "-name":
				if w, ok := next(); ok {
					names = append(names, w)
				}
			case pwshValueParams[name]:
				_, _ = next()
			}
			continue
		}
		pos = append(pos, a)
	}
	var uses []pathUse
	add := func(ws []word, u use) {
		for _, w := range ws {
			uses = append(uses, pathUse{w, u})
		}
	}
	recurse := hasFlagFold(args, "-recurse", "-r")
	switch kind {
	case "write":
		if len(names) > 0 {
			// New-Item -Path dir -Name x makes dir\x (the current folder
			// without -Path).
			dirs := append(append([]word{}, named...), pos...)
			if len(dirs) == 0 {
				dirs = []word{{text: ".", assign: -1}}
			}
			for _, dw := range dirs {
				for _, nm := range names {
					nw := dw
					nw.text = strings.TrimRight(dw.text, `/\`) + "/" + nm.text
					nw.glob = dw.glob || nm.glob
					nw.expand = dw.expand || nm.expand
					named = append(named, nw)
				}
			}
		}
		add(named, use{write: true})
		add(pos, use{write: true})
		if strings.Contains(itemType, "link") || itemType == "junction" || strings.HasPrefix(itemType, "sym") || strings.HasPrefix(itemType, "hard") || strings.HasPrefix(itemType, "junc") {
			// A link is a write of its target too, read both from the
			// current folder and from the link's.
			for _, t := range append(dest, values...) {
				add([]word{t}, use{write: true})
				for _, l := range append(append([]word{}, named...), pos...) {
					add([]word{linkPair{link: l, target: t, fromLink: true}.targetWord()}, use{write: true})
				}
			}
		}
	case "remove":
		add(named, use{write: true, recursive: true})
		add(pos, use{write: true, recursive: true})
	case "copy", "expand":
		if len(named) == 0 && len(pos) > 0 {
			named, pos = pos[:1], pos[1:]
		}
		add(named, use{})
		add(dest, use{write: true, recursive: recurse || kind == "expand"})
		add(pos, use{write: true, recursive: recurse || kind == "expand"})
	case "move":
		add(named, use{write: true, recursive: true})
		add(dest, use{write: true, recursive: true})
		add(pos, use{write: true, recursive: true})
	}
	return uses, true
}

func looksLikePath(t string) bool {
	return strings.ContainsAny(t, `/\`) || strings.HasPrefix(t, ".") || strings.HasPrefix(t, "~")
}

func hasFlag(args []word, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a.text == f {
				return true
			}
		}
	}
	return false
}

func hasFlagFold(args []word, flags ...string) bool {
	for _, a := range args {
		for _, f := range flags {
			if strings.EqualFold(a.text, f) {
				return true
			}
		}
	}
	return false
}

// hasShortFlag reports a single-letter flag inside a group such as -rf.
func hasShortFlag(args []word, letters ...byte) bool {
	for _, a := range args {
		t := a.text
		if len(t) < 2 || t[0] != '-' || t[1] == '-' {
			continue
		}
		for _, l := range letters {
			if strings.IndexByte(t[1:], l) >= 0 {
				return true
			}
		}
	}
	return false
}

// flagValue is the value of an option given as -x v, --long v or --long=v.
func flagValue(args []word, names ...string) *word {
	for i, a := range args {
		for _, n := range names {
			if a.text == n && i+1 < len(args) {
				return &args[i+1]
			}
			if strings.HasPrefix(n, "--") {
				if v, ok := strings.CutPrefix(a.text, n+"="); ok {
					w := a
					w.text = v
					return &w
				}
			}
		}
	}
	return nil
}

// find: the paths before the expression are read; -exec runs a command
// (analysed, at least ask); -delete and -fprint write.
func (e *evaluator) find(args []word, d dialect, st *shellState, depth int) result {
	res := newResult()
	i := 0
	var paths []word
	for ; i < len(args); i++ {
		t := args[i].text
		if strings.HasPrefix(t, "-") || t == "(" || t == "!" || t == ")" {
			break
		}
		paths = append(paths, args[i])
	}
	del := false
	for ; i < len(args); i++ {
		switch args[i].text {
		case "-exec", "-execdir", "-ok", "-okdir":
			j := i + 1
			for j < len(args) && args[j].text != ";" && args[j].text != "+" {
				j++
			}
			if j > i+1 && j < len(args) {
				// Without its ; or +, find refuses the whole expression
				// and runs nothing (the PowerShell reading of \;).
				res.merge(e.command(args[i+1:j], d, st, depth+1, nil))
			}
			res.atLeastAsk("find " + args[i].text)
			i = j
		case "-delete":
			del = true
		case "-fprint", "-fprint0", "-fprintf", "-fls":
			if i+1 < len(args) {
				res.merge(e.argPath(args[i+1], use{write: true}, st))
			}
		}
	}
	if len(paths) == 0 {
		paths = []word{{text: ".", assign: -1}}
	}
	for _, p := range paths {
		res.merge(e.argPath(p, use{write: del, recursive: true}, st))
	}
	return res
}

func findActs(args []string) bool {
	for _, a := range args {
		switch a {
		case "-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprint0", "-fprintf", "-fls":
			return true
		}
	}
	return false
}
