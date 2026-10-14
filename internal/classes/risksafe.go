package classes

import (
	"regexp"
	"strings"
)

// The list of known-safe programs (docs/classes/lista-de-seguros.md): a
// part that matches no alto or medio row of the table is baixo only when
// its program and the shape of its arguments are in this list and it shows
// no risk signal; anything else is medio, which the Mestre da mesa decides
// within seconds (ADR 0025). Listing what is safe, instead of recognising
// every program that can run another or write files, is what keeps an
// unknown program from passing as baixo. The shapes that pass an argument
// to be run as code (-c, -e, --eval, -exec, --rcfile…) are never in it, and
// they hold as a floor over the owner's adjustments too (codeShape).
// RiskTableVersion changes when an entry changes.

// safeSpec is the shape a program of the list must have.
type safeSpec struct {
	group string
	// subs are the subcommands it may run (the first operand); nil is any.
	subs map[string]bool
	// code are the options that pass code to run: never safe, and medio
	// over an adjustment too. deny are other options that make the part
	// leave the list (a write the line does not show, an interactive mode).
	// An option matches as written, as --long=value and, for a short one,
	// with the value attached (-ofile); a "+" matches every word that
	// starts with it (less +cmd).
	code, deny []string
	// shape is an extra check of the arguments; nil passes.
	shape func(s *safeCall) bool
	// maxOps bounds the operands (uniq in out writes out); 0 is no bound.
	maxOps int
	// bare also takes the program without a subcommand (yarn); script
	// also takes a script of the worktree as the first operand (bun x.ts).
	bare, script bool
}

// safeCall is a part as the list sees it.
type safeCall struct {
	w    *riskWalker
	c    *riskCmd
	ws   []word
	args []string
	st   *shellState
}

// The groups of the list, which name the entry in the rule.
const (
	safeRead    = "leitura e texto"
	safePwsh    = "leitura e texto do PowerShell"
	safeShell   = "comandos internos do shell"
	safeFiles   = "arquivos dentro do worktree"
	safeGit     = "git na branch da mesa"
	safePkg     = "pacotes do projeto"
	safeTools   = "scripts e ferramentas do projeto"
	safeVersion = "versão ou ajuda"
)

func set(names string) map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields(names) {
		m[n] = true
	}
	return m
}

// safeList is the list, by canonical program name.
var safeList = map[string]safeSpec{}

func addSafe(names string, s safeSpec) {
	for _, n := range strings.Fields(names) {
		if _, dup := safeList[n]; dup {
			panic("classes: program listed twice in the safe list: " + n)
		}
		safeList[n] = s
	}
}

func init() {
	// Read-only and text tools.
	addSafe("ls dir cat head tail wc grep egrep fgrep stat diff cmp du df which whereis type pwd echo test [ true false : "+
		"cut tr column basename dirname realpath readlink sha1sum sha224sum sha256sum sha384sum sha512sum md5sum b2sum "+
		"cksum sum hexdump od nl paste join comm expand unexpand fold fmt rev tac seq sleep whoami uname nproc arch jq "+
		"locale tty clear", safeSpec{group: safeRead})
	addSafe("printf", safeSpec{group: safeRead, deny: []string{"-v"}})
	addSafe("date", safeSpec{group: safeRead, deny: []string{"-s", "--set"}})
	addSafe("hostname", safeSpec{group: safeRead, deny: []string{"-F", "--file", "-b", "--boot"}, shape: noOperands})
	addSafe("file", safeSpec{group: safeRead, deny: []string{"-C", "--compile"}})
	addSafe("tree", safeSpec{group: safeRead, deny: []string{"-o", "-R"}})
	addSafe("rg", safeSpec{group: safeRead, code: []string{"--pre"}})
	addSafe("find", safeSpec{group: safeRead, code: []string{"-exec", "-execdir", "-ok", "-okdir"},
		deny: []string{"-delete", "-fprint", "-fprint0", "-fprintf", "-fls"}})
	addSafe("sort", safeSpec{group: safeRead, code: []string{"--compress-program"}, deny: []string{"-o", "--output"}})
	addSafe("uniq xxd base64", safeSpec{group: safeRead, deny: []string{"-o", "--output"}, maxOps: 1})
	addSafe("yq", safeSpec{group: safeRead, deny: []string{"-i", "--inplace"}})
	addSafe("less more", safeSpec{group: safeRead, code: []string{"+"},
		deny: []string{"-k", "--lesskey-file", "--lesskey-src", "--lesskey-content", "-o", "-O", "--log-file", "--LOG-FILE"}})
	// awk and sed: an inline program that runs a command is the code
	// signal (awksed.go); a program file, an include or an extension never.
	addSafe("awk gawk mawk nawk goawk", safeSpec{group: safeRead,
		code: []string{"-f", "--file", "-i", "--include", "-l", "--load", "-E", "--exec", "-W"}})
	addSafe("sed gsed", safeSpec{group: safeRead, code: []string{"-f", "--file"}})
	// Archives that are only listed or tested (an extraction is the signal
	// of a write the line does not show).
	addSafe("tar bsdtar gtar", safeSpec{group: safeRead, shape: tarLists,
		code: []string{"-I", "--use-compress-program", "--to-command", "--checkpoint-action", "--rsh-command", "-F",
			"--info-script", "--new-volume-script", "--rmt-command"}})
	addSafe("unzip", safeSpec{group: safeRead, shape: func(s *safeCall) bool { return hasText(s.args, "-l", "-v", "-t", "-Z") }})
	addSafe("7z 7za 7zr 7zz", safeSpec{group: safeRead, subs: set("l t")})
	addSafe("ar", safeSpec{group: safeRead, shape: func(s *safeCall) bool {
		return len(s.args) > 0 && strings.Trim(s.args[0], "-tvO") == ""
	}})
	addSafe("cpio", safeSpec{group: safeRead, deny: []string{"-F", "-O", "--file", "-E", "--pattern-file"},
		shape: func(s *safeCall) bool { return hasText(s.args, "-o", "--create", "-t", "--list", "-ot", "-tv", "-ov") }})

	// PowerShell: the reading cmdlets and their aliases (the script blocks
	// in braces are parts of their own), and the writes of files, which the
	// path rules see.
	addSafe("get-childitem gci get-content gc select-string sls measure-object measure get-location gl write-output "+
		"write write-host test-path get-item gi get-command gcm resolve-path rvpa split-path join-path convertfrom-json "+
		"convertto-json select-object sort-object format-table ft format-list fl out-string out-null get-date "+
		"where-object where foreach-object get-filehash get-member gm group-object compare-object get-help", safeSpec{group: safePwsh})
	addSafe("new-item ni copy-item cpi copy move-item mi move rename-item ren rni set-content add-content ac out-file",
		safeSpec{group: safeFiles})

	// Shell builtins without an effect outside the shell, and the ones that
	// set variables when none changes the program that runs.
	addSafe("exit return shift break continue wait dirs umask unset", safeSpec{group: safeShell})
	addSafe("export declare typeset local readonly", safeSpec{group: safeShell, shape: noProgramEnv})
	addSafe("set", safeSpec{group: safeShell, shape: onlyOptions})
	addSafe("command", safeSpec{group: safeShell, shape: func(s *safeCall) bool { return hasText(s.args, "-v", "-V") }})
	for n := range cdNames {
		safeList[n] = safeSpec{group: safeShell}
	}

	// Files inside the worktree, by the write rules (rm in mass and outside
	// the worktree are rows above).
	addSafe("mkdir touch cp mv chmod tee rm", safeSpec{group: safeFiles})
	addSafe("ln", safeSpec{group: safeFiles, shape: func(s *safeCall) bool {
		return hasShortFlag(s.ws[1:], 's') || hasText(s.args, "--symbolic")
	}})

	// git on the table's own branch: the fixed rules and the branch checks
	// (git.go) decide the rest.
	addSafe("git", safeSpec{group: safeGit, shape: safeGitShape})

	// The project's packages, scripts and tools.
	npmCode := []string{"-c", "--call", "--script-shell", "--shell-mode", "--node-options"}
	addSafe("npm", safeSpec{group: safePkg, code: npmCode,
		subs: set("install i ci uninstall un remove rm r update up test t tst run run-script start stop restart ls list ll la outdated")})
	jsScripts := " build lint dev format fmt typecheck check preview serve watch"
	addSafe("pnpm", safeSpec{group: safePkg, code: npmCode,
		subs: set("install i add remove rm uninstall un update up test t run start ls list ll outdated" + jsScripts)})
	addSafe("yarn", safeSpec{group: safePkg, code: npmCode, bare: true,
		subs: set("install add remove upgrade up test run start list outdated why" + jsScripts)})
	addSafe("bun", safeSpec{group: safePkg, code: []string{"-e", "--eval", "-p", "--print", "-r", "--preload", "--require"}, script: true,
		subs: set("install i add a remove rm update outdated test build run")})
	addSafe("deno", safeSpec{group: safeTools, code: []string{"--eval"}, script: true, subs: set("test fmt lint check task bench doc info")})
	addSafe("go", safeSpec{group: safeTools, code: []string{"-toolexec", "-exec", "-vettool"},
		subs: set("build test vet run mod get fmt generate version env list doc"), shape: goShape})
	addSafe("cargo", safeSpec{group: safeTools, code: []string{"--config"},
		subs: set("build b test t check c clippy fmt run r add fetch doc d bench tree metadata")})
	addSafe("uv", safeSpec{group: safePkg, subs: set("run sync add remove lock tree venv pip version")})
	addSafe("poetry", safeSpec{group: safePkg, subs: set("install add remove lock show check run")})
	addSafe("bundle", safeSpec{group: safePkg, subs: set("install")})
	addSafe("composer", safeSpec{group: safePkg, subs: set("install require")})
	addSafe("make gmake", safeSpec{group: safeTools, code: []string{"--eval", "-E"}, shape: makeShape})
	addSafe("just", safeSpec{group: safeTools, code: []string{"--shell", "--shell-arg", "--shell-command", "--command", "--evaluate"}})
	addSafe("mvn mvnw", safeSpec{group: safeTools, shape: mavenShape})
	addSafe("gradle gradlew", safeSpec{group: safeTools,
		code: []string{"-I", "--init-script", "-c", "--settings-file", "-b", "--build-file"}, shape: gradleShape})
	addSafe("dotnet", safeSpec{group: safeTools, subs: set("build test restore format run clean")})
	addSafe("pytest py.test tsc eslint prettier ruff black isort flake8 mypy pylint gofmt goimports golangci-lint biome "+
		"stylelint rustfmt clang-format shellcheck shfmt jest vitest mocha", safeSpec{group: safeTools, code: []string{"--rulesdir"}})

	// Interpreters run a script of the worktree (the script signal holds on
	// top), or a module of the list on python; their code options never.
	interp := func(code ...string) safeSpec {
		return safeSpec{group: safeTools, code: code, shape: safeInterpreter}
	}
	addSafe("python python2 python3 py", interp("-c", "-i"))
	addSafe("node nodejs", interp("-e", "--eval", "-p", "--print", "-r", "--require", "--import", "--loader",
		"--experimental-loader", "-i", "--interactive"))
	addSafe("ruby", interp("-e", "-r", "-I", "-x"))
	addSafe("perl", interp("-e", "-E", "-M", "-m", "-I", "-x"))
	addSafe("php", interp("-r", "-B", "-R", "-E", "-F", "-d", "-c", "-a"))
	addSafe("lua", interp("-e", "-l", "-i"))
	addSafe("sh bash zsh dash ksh mksh ash", interp("--rcfile", "--init-file", "-i"))
	addSafe("pwsh powershell", interp("-command", "-c", "-encodedcommand", "-e", "-ec", "-enc"))
	addSafe("source .", interp())
	addSafe("tsx ts-node", interp("-e", "--eval", "-p", "--print", "-r", "--require", "--import"))
}

// safeVersions are the programs whose single --version or --help is safe.
var safeVersions = set("python python2 python3 py node nodejs deno bun ruby perl php lua go cargo rustc rustup npm npx pnpm yarn " +
	"uv poetry pip pip3 pipx java javac dotnet mvn gradle make cmake gcc g++ clang clang++ git tsc bash sh zsh pwsh powershell")

var versionWords = set("--version -v -V --help -h version help")

// safe is the entry of the list that makes the part baixo, if any.
func (w *riskWalker) safe(c *riskCmd, ws []word, st *shellState) (string, bool) {
	p := ws[0]
	s := &safeCall{w: w, c: c, ws: ws, args: c.args, st: st}
	spec, listed := safeList[normalizeInterpreter(c.name)]
	byPath := strings.ContainsAny(p.text, `/\`) || strings.HasPrefix(p.text, ".") && c.name != "."
	if byPath || !p.quoted && !p.expand && looksLikeScriptFile(p.text) {
		// A program of the worktree given by its path (./scripts/build.sh,
		// .\build.ps1), or a script file a runner runs: the script signal on
		// top makes the one the table made medio. A listed name keeps the
		// shape of its entry (./gradlew build).
		if p.expand || !w.inWorktree(p, st) {
			return "", false
		}
		if !listed || spec.subs == nil && spec.shape == nil {
			if listed && s.hits(spec.code) {
				return "", false
			}
			return "seguro (" + safeTools + "): programa do worktree " + p.text, true
		}
	} else if p.quoted || p.expand {
		return "", false // only the program the shell finds by its name
	}
	if len(c.args) == 1 && versionWords[c.args[0]] && safeVersions[normalizeInterpreter(c.name)] {
		return "seguro (" + safeVersion + "): " + c.text, true
	}
	if !listed || s.hits(spec.code) || s.hits(spec.deny) {
		return "", false
	}
	label := c.name
	if spec.subs != nil {
		sub := firstOperand(c.args)
		if c.name == "cargo" && strings.HasPrefix(sub, "+") {
			sub = firstOperand(c.args[1:]) // cargo +nightly build
		}
		switch {
		case spec.subs[sub]:
			label += " " + sub
		case sub == "" && spec.bare:
		case spec.script && c.scriptInWorktree:
			label += " " + sub
		default:
			return "", false
		}
	}
	if spec.maxOps > 0 && len(operands(c.args)) > spec.maxOps {
		return "", false
	}
	if spec.shape != nil && !spec.shape(s) {
		return "", false
	}
	return "seguro (" + spec.group + "): " + label, true
}

// codeShape is why a part passes code to run by an option of the list
// (make --eval, find -exec, node -r): a floor of medio that holds over the
// owner's adjustments as well, so no adjustment makes code baixo.
func (w *riskWalker) codeShape(c *riskCmd) (string, bool) {
	spec, ok := safeList[normalizeInterpreter(c.name)]
	if !ok {
		return "", false
	}
	s := &safeCall{c: c, args: c.args}
	for _, o := range spec.code {
		if s.hits([]string{o}) {
			return c.name + " " + o, true
		}
	}
	return "", false
}

// hits reports an argument that is one of the options.
func (s *safeCall) hits(opts []string) bool {
	for _, a := range s.args {
		if a == "--" && s.c.name != "find" {
			return false // operands from here on
		}
		for _, o := range opts {
			if optionIs(a, o, s.c.name) {
				return true
			}
		}
	}
	return false
}

// optionIs reports whether an argument is the option o.
func optionIs(a, o, prog string) bool {
	switch {
	case o == "+":
		return strings.HasPrefix(a, "+")
	case pwshNames[prog]:
		return strings.EqualFold(a, o) || len(o) > 2 && strings.HasPrefix(strings.ToLower(a), o+":")
	case a == o:
		return true
	case strings.HasPrefix(o, "--"):
		return strings.HasPrefix(a, o+"=")
	case len(o) > 2 && o[0] == '-':
		// a long option with one dash (go -toolexec, find -exec): also
		// with =value and with two dashes, as the flag package takes it.
		return strings.HasPrefix(a, o+"=") || a == "-"+o || strings.HasPrefix(a, "-"+o+"=")
	case len(o) == 2 && o[0] == '-':
		// -ofile: a short option with its value attached.
		return strings.HasPrefix(a, o) && !strings.HasPrefix(a, "--")
	}
	return false
}

// firstOperand is the first argument that is not an option.
func firstOperand(args []string) string {
	for _, a := range args {
		if a == "--" {
			return ""
		}
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func noOperands(s *safeCall) bool { return len(operands(s.args)) == 0 }

// tarLists is a tar that only lists (t, --list): creating names its file as
// an operand the write rules do not see, and extracting is the signal.
func tarLists(s *safeCall) bool {
	if len(s.args) == 0 {
		return false
	}
	modes := ""
	for i, a := range s.args {
		switch {
		case a == "--list":
			modes += "t"
		case strings.HasPrefix(a, "--"):
			if set("--create --extract --get --append --update --concatenate --catenate --delete --diff --compare")[strings.SplitN(a, "=", 2)[0]] {
				return false
			}
		case strings.HasPrefix(a, "-") || i == 0:
			// the old style (tar tzf) and the clusters (-tvf)
			for _, m := range "cxrudAt" {
				if strings.ContainsRune(strings.TrimPrefix(a, "-"), m) {
					modes += string(m)
				}
			}
		}
	}
	return modes != "" && strings.Trim(modes, "t") == ""
}

// goShape keeps go env from writing the configuration (-w, -u).
func goShape(s *safeCall) bool {
	return firstOperand(s.args) != "env" || !hasText(s.args, "-w", "-u")
}

// onlyOptions is set -e, set -euo pipefail: shell options, not variables.
func onlyOptions(s *safeCall) bool {
	for i, a := range s.args {
		if strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+") {
			continue
		}
		if p := ""; i > 0 {
			p = s.args[i-1]
			if (strings.HasPrefix(p, "-") || strings.HasPrefix(p, "+")) && strings.HasSuffix(p, "o") && !strings.HasPrefix(p, "--") {
				continue // the name of a set -o option (set -euo pipefail)
			}
		}
		return false
	}
	return len(s.args) > 0
}

// noProgramEnv is an export or declare of variables that do not change the
// program that runs (PATH, LD_PRELOAD… are a signal).
func noProgramEnv(s *safeCall) bool {
	for _, a := range s.args {
		n, _, _ := strings.Cut(a, "=")
		if programEnv[strings.ToUpper(n)] {
			return false
		}
	}
	return true
}

// makeVars are the variables of make that change how it runs its recipes.
var makeVars = set("SHELL .SHELLFLAGS MAKEFLAGS MFLAGS GNUMAKEFLAGS MAKE MAKESHELL MAKEFILES")

// shellMeta are the characters that turn a value into shell code.
const shellMeta = "'\"`$;|&()<>\n\\{}*?!#"

// makeShape is make with targets and simple variables: an assignment of a
// variable that changes how make runs its recipes, or a value with shell
// code or a path to a program, is not in the list.
func makeShape(s *safeCall) bool {
	for _, a := range s.args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		n, v, ok := strings.Cut(a, "=")
		if !ok {
			continue // a target
		}
		n = strings.TrimRight(n, ":+?!")
		if makeVars[n] || strings.ContainsAny(v, shellMeta) || looksLikePath(v) {
			return false
		}
	}
	return true
}

var mavenPhases = set("validate compile test-compile test package verify install clean dependency:tree dependency:resolve dependency:go-offline")

// mavenShape is mvn with the phases of the build only (exec:exec and the
// like run a program).
func mavenShape(s *safeCall) bool {
	for i, a := range s.args {
		switch {
		case strings.HasPrefix(a, "-D") && strings.Contains(strings.ToLower(a), "exec"):
			return false
		case strings.HasPrefix(a, "-"):
		case i > 0 && (s.args[i-1] == "-pl" || s.args[i-1] == "-T" || s.args[i-1] == "-f" || s.args[i-1] == "-P" || s.args[i-1] == "-D"):
		case !mavenPhases[a]:
			return false
		}
	}
	return true
}

var gradleTask = regexp.MustCompile(`^(:[\w.-]+)*:?(build|test|check|assemble|clean|lint|jar|classes|testClasses|compile\w*|dependencies|tasks|help|bootJar|run|spotlessApply|spotlessCheck|ktlintCheck|detekt)$`)

// gradleShape is gradle with the build's tasks (-x excludes one).
func gradleShape(s *safeCall) bool {
	for i, a := range s.args {
		switch {
		case strings.HasPrefix(a, "-"):
		case i > 0 && (s.args[i-1] == "-x" || s.args[i-1] == "--exclude-task" || s.args[i-1] == "-P" || s.args[i-1] == "-D"):
		case !gradleTask.MatchString(a):
			return false
		}
	}
	return true
}

// safeGitShape is git on the table's own branch: reading, staging,
// committing, restoring files and listing branches and tags, without the
// global options that name a program, a config or another repository.
func safeGitShape(s *safeCall) bool {
	args := s.ws[1:]
	i := 0
	for i < len(args) && strings.HasPrefix(args[i].text, "-") {
		switch args[i].text {
		case "-C":
			i++
		case "--no-pager", "-P", "--no-optional-locks", "--literal-pathspecs", "--no-replace-objects":
		default:
			return false // -c, --config-env, --exec-path, --git-dir, --work-tree, --namespace…
		}
		i++
	}
	if i >= len(args) {
		return false
	}
	sub, rest := args[i].text, wordsText(args[i+1:])
	has := func(opts ...string) bool {
		for _, a := range rest {
			for _, o := range opts {
				if optionIs(a, o, "git") {
					return true
				}
			}
		}
		return false
	}
	switch sub {
	case "status", "diff", "log", "show", "rev-parse", "ls-files", "blame", "shortlog", "describe", "merge-base",
		"diff-tree", "diff-files", "diff-index", "ls-tree", "show-ref", "for-each-ref", "cat-file", "version", "help":
		return !has("--output", "-o")
	case "grep":
		return !has("-O", "--open-files-in-pager")
	case "add":
		return !has("-i", "--interactive", "-p", "--patch", "-e", "--edit")
	case "commit", "restore", "checkout", "switch":
		return !has("-p", "--patch", "-i", "--interactive")
	case "branch":
		listing := false
		for _, a := range rest {
			switch {
			case a == "-l" || a == "--list":
				listing = true
			case strings.HasPrefix(a, "-"):
				if !set("-a -r -v -vv --all --remotes --verbose --show-current --merged --no-merged --contains --no-contains --color --no-color --column --no-column")[strings.SplitN(a, "=", 2)[0]] && !strings.HasPrefix(a, "--sort") && !strings.HasPrefix(a, "--format") {
					return false
				}
			case !listing:
				return false
			}
		}
		return true
	case "tag":
		listing := len(rest) == 0
		for _, a := range rest {
			switch {
			case a == "-l" || a == "--list":
				listing = true
			case a == "-n" || strings.HasPrefix(a, "--sort") || strings.HasPrefix(a, "--contains") || strings.HasPrefix(a, "--format"):
			case strings.HasPrefix(a, "-"):
				return false
			case !listing:
				return false
			}
		}
		return listing
	}
	return false
}

// safeModules are the python modules of the list (python -m json.tool):
// the ones that test, check and format, and the pip and venv commands that
// only read or make the project's environment.
var safeModules = set("pytest unittest json.tool venv mypy black ruff flake8 pylint isort coverage pip")

var safePip = set("list show freeze check --version")

// pythonOpts are the options of python that do not change what it runs.
var pythonOpts = set("-u -B -I -E -s -S -O -OO -q -b -bb -P")

// safeInterpreter is an interpreter that runs a script of the worktree
// (python x.py, bash -x x.sh, pwsh -File x.ps1, source x.sh), or python -m
// with a module of the list.
func safeInterpreter(s *safeCall) bool {
	c := s.c
	if c.scriptInWorktree {
		return true
	}
	return pythonSafeModule(normalizeInterpreter(c.name), s.ws[1:])
}

func isPython(base string) bool {
	return base == "python" || base == "python2" || base == "python3" || base == "py"
}

// pythonSafeModule is python -m with a module of the list (pip only to
// list, show, freeze or check).
func pythonSafeModule(base string, ws []word) bool {
	if !isPython(base) {
		return false
	}
	args := wordsText(ws)
	for len(args) > 0 && pythonOpts[args[0]] {
		args = args[1:]
	}
	if len(args) < 2 || args[0] != "-m" || !safeModules[args[1]] {
		return false
	}
	if args[1] == "pip" {
		return len(args) > 2 && safePip[args[2]]
	}
	return true
}

// dataModules are the modules of the list that read their standard input
// as data (json.tool), never as code.
var dataModules = set("json.tool pytest unittest mypy black ruff flake8 pylint isort coverage")

// pythonDataModule is python -m with a module that reads data from its
// input: fed by a pipe or a here-document, it is not code from the input.
func pythonDataModule(base string, ws []word) bool {
	if !pythonSafeModule(base, ws) {
		return false
	}
	for i, w := range ws {
		if w.text == "-m" && i+1 < len(ws) {
			return dataModules[ws[i+1].text]
		}
	}
	return false
}
