package classes

import (
	"regexp"
	"strings"
)

// Programs that run another program with another environment, namespace or
// privilege (setarch, unshare, nsenter, chroot, setpriv, gosu, fakeroot,
// systemd-run, cross-env, dotenv, direnv exec, corepack…). The table of the
// transparent runners does not hold them, and they are not unwrapped for the
// risk or for the allow patterns: the wrapper is not a program of the safe
// list, and what it runs is not the program it seems to be. But what they run
// is a command like any other for the fixed rules: git push, the CLI of an
// agent, an elevation command and a write of .git/** deny whatever starts
// them (docs/classes/permissoes.md#regras-fixas-de-segurança).

// wrapper is how a program of the table takes the command it runs: its
// options that take the next word, the operands before the command (setarch
// ARCH, chroot NEWROOT, gosu USER) and the subcommand that comes first
// (doppler run, dotenvx run, direnv exec DIR).
type wrapper struct {
	valued map[string]bool
	// takes are the options that take more than one word (bwrap --bind SRC
	// DEST takes two, --overlay RW WORK DEST three).
	takes map[string]int
	// sets are the options whose first value names a variable of the
	// environment of the command (systemd-run -E NAME=value, bwrap --setenv
	// NAME value), and unsets the ones whose value is a variable taken out of
	// it (bwrap --unsetenv NAME): what moves the global configuration of git
	// for the command moves it for the git it runs.
	sets, unsets map[string]bool
	operands     int
	sub          string
	// line are the options whose value is a command line for a shell
	// (doppler run --command 'git push').
	line map[string]bool
}

var wrappers = map[string]wrapper{
	"setarch": {operands: 1}, "linux32": {}, "linux64": {},
	"setpriv": {valued: set("--reuid --regid --ruid --euid --rgid --egid --groups --inh-caps --ambient-caps --bounding-set --securebits --pdeathsig --selinux-label --apparmor-profile --landlock-access --landlock-rule")},
	"prlimit": {valued: set("-p --pid -o --output")},
	"unshare": {valued: set("--propagation --setgroups -R --root -w --wd -S --setuid -G --setgid --map-user --map-group --map-users --map-groups --monotonic --boottime")},
	"nsenter": {valued: set("-t --target -S --setuid -G --setgid -w --wd -r --root")},
	"chroot":  {valued: set("--userspec --groups"), operands: 1},
	"gosu":    {operands: 1}, "su-exec": {operands: 1},
	"fakeroot":  {valued: set("-l --lib -f --faked -i -s -b")},
	"dumb-init": {valued: set("-r --rewrite")},
	"tini":      {valued: set("-p -e")},
	"ssh-agent": {valued: set("-a -E -O -P -t")},
	"firejail":  {sets: set("--env")},
	"rlwrap":    {valued: set("-b -C -D -f -F -H -l -M -p -P -q -s -S -t -z")},
	"systemd-run": {valued: set("-u --unit --description --slice -p --property -E --setenv --uid --gid --working-directory --service-type --nice " +
		"-M --machine -H --host --on-active --on-boot --on-startup --on-unit-active --on-unit-inactive --on-calendar --timer-property"), sets: set("-E --setenv")},
	"env-cmd": {valued: set("-f --file -e --environments -r --rc-file")},
	"dotenv":  {valued: set("-e -v -p"), sets: set("-v")},
	"dotenvx": {valued: set("-f --env-file -fk --env-keys-file -e --env"), sets: set("-e --env"), sub: "run"},
	"doppler": {valued: set("-p --project -c --config -t --token --api-host --scope --name-transformer --preserve-env --fallback --mount"), sub: "run", line: set("--command")},
	"direnv":  {sub: "exec", operands: 1},
	// bubblewrap: its options take zero to three words each.
	"bwrap": {
		valued: set("--args --argv0 --userns --userns2 --pidns --uid --gid --hostname --chdir --unsetenv --lock-file --sync-fd --remount-ro --exec-label --file-label " +
			"--proc --dev --tmpfs --mqueue --dir --seccomp --add-seccomp-fd --block-fd --userns-block-fd --info-fd --json-status-fd --cap-add --cap-drop --perms --size " +
			"--overlay-src --tmp-overlay --ro-overlay"),
		takes: map[string]int{
			"--setenv": 2, "--bind": 2, "--bind-try": 2, "--dev-bind": 2, "--dev-bind-try": 2, "--ro-bind": 2, "--ro-bind-try": 2, "--bind-fd": 2, "--ro-bind-fd": 2,
			"--file": 2, "--bind-data": 2, "--ro-bind-data": 2, "--symlink": 2, "--chmod": 2, "--overlay": 3,
		},
		sets: set("--setenv"), unsets: set("--unsetenv"),
	},
	"cpulimit": {valued: set("-l --limit -p --pid -e --exe")},
	"sshpass":  {valued: set("-f -d -p -P")},
}

// corepackManagers are the package managers corepack runs.
var corepackManagers = set("npm npx pnpm pnpx yarn yarnpkg")

// wrapped are the commands a wrapper of the table, cross-env, corepack, ssh, a
// debugger, a profiler or a meter, or python -m runs, as the words of a
// command each, or nil.
func (e *evaluator) wrapped(cn string, args []word, res *result) [][]word {
	one := func(inner []word) [][]word {
		if len(inner) == 0 {
			return nil
		}
		return [][]word{inner}
	}
	switch cn {
	case "cross-env":
		// cross-env NAME=value command: the assignments are env's.
		inner, _ := e.envInner(args, res)
		return one(inner)
	case "corepack":
		if rest := skipDashOpts(args, nil); len(rest) > 0 && corepackManagers[rest[0].text] {
			return one(rest)
		}
		return nil
	case "sg":
		// sg GROUP command, sg GROUP -c 'command line'
		for i, a := range args {
			if a.text == "-c" && i+1 < len(args) {
				return one(shellOf(args[i+1]))
			}
		}
		rest := skipDashOpts(args, nil)
		if len(rest) < 2 {
			return nil
		}
		return one(rest[1:])
	case "runcon":
		return one(runconInner(args))
	case "ssh", "slogin":
		return one(sshInner(args))
	}
	if inner := pythonRunner(cn, args); inner != nil {
		return one(inner)
	}
	if cmds := debugged(cn, args); cmds != nil {
		return cmds
	}
	spec, ok := wrappers[cn]
	if !ok {
		return nil
	}
	rest := args
	if spec.sub != "" {
		i := 0
		for i < len(rest) && strings.HasPrefix(rest[i].text, "-") {
			i++
		}
		if i >= len(rest) || rest[i].text != spec.sub {
			return nil
		}
		rest = rest[i+1:]
	}
	for i, a := range rest {
		opt, val, hasVal := strings.Cut(a.text, "=")
		if !spec.line[opt] {
			continue
		}
		if hasVal {
			a.text = val
			return one(shellOf(a))
		}
		if i+1 < len(rest) {
			return one(shellOf(rest[i+1]))
		}
	}
	rest = spec.skipOpts(rest, func(name string, set bool) { e.wrapperEnv(res, name, set) })
	if len(rest) < spec.operands {
		return nil
	}
	return one(rest[spec.operands:])
}

// wrapperEnv is what an option of a wrapper does to the environment of the
// command it runs: setting or taking out the variable is judged as env does.
func (e *evaluator) wrapperEnv(res *result, name string, set bool) {
	if set {
		e.gitEnv(res, name)
	} else {
		e.envRemoved(res, name)
	}
	if homeLike(name) {
		e.homeScope = name
	}
}

// shellOf is the command sh -c <w>: a command line that a program hands to a
// shell, as the words of a command; shellWords is the same for a line of text.
func shellOf(w word) []word {
	return []word{{text: "sh", assign: -1}, {text: "-c", assign: -1}, w}
}

func shellWords(line string) []word { return shellOf(word{text: line, assign: -1}) }

// option reads a word that starts with a dash: the option it stands for, its
// value when the word carries it (--name=value, -nvalue) and how many of the
// words that follow it are its values. A cluster of short options (-zl 50)
// ends at the first that takes a value, which is the rest of the word or, when
// it is the last letter, the next word.
func (w wrapper) option(a string) (opt, attached string, n int) {
	if n := w.takes[a]; n > 0 {
		return a, "", n
	}
	if w.valued[a] {
		return a, "", 1
	}
	if strings.HasPrefix(a, "--") {
		if name, val, ok := strings.Cut(a, "="); ok {
			return name, val, 0
		}
		return a, "", 0
	}
	for j := 1; j < len(a); j++ {
		o := "-" + a[j:j+1]
		if w.valued[o] || w.takes[o] > 0 {
			if j+1 < len(a) {
				return o, a[j+1:], 0
			}
			return o, "", max(1, w.takes[o])
		}
	}
	return a, "", 0
}

// skipOpts returns the words after the options of the wrapper: the first
// word that is not an option, or what follows --. note is told the variables
// its options set or take out of the environment of the command.
func (w wrapper) skipOpts(args []word, note func(name string, set bool)) []word {
	i := 0
	for i < len(args) {
		a := args[i].text
		if a == "--" {
			return args[i+1:]
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			break
		}
		opt, attached, n := w.option(a)
		i++
		var vals []string
		if attached != "" {
			vals = append(vals, attached)
		}
		for ; n > 0 && i < len(args); n-- {
			vals = append(vals, args[i].text)
			i++
		}
		if note != nil && len(vals) > 0 && (w.sets[opt] || w.unsets[opt]) {
			name, _, _ := strings.Cut(vals[0], "=")
			note(name, w.sets[opt])
		}
	}
	return args[min(i, len(args)):]
}

// runconInner is the command runcon runs: runcon CONTEXT command, or, with
// one of -c -u -r -t -l, runcon [options] command (the context is then not an
// operand).
func runconInner(args []word) []word {
	rest := wrapper{valued: set("-u -r -t -l --user --role --type --range")}.skipOpts(args, nil)
	if len(rest) == len(args) || len(args)-len(rest) == 1 && args[0].text == "--" {
		if len(rest) == 0 {
			return nil
		}
		return rest[1:]
	}
	return rest
}

// sshOptions are the options of ssh that take the next word.
var sshOptions = wrapper{valued: set("-B -b -c -D -E -e -F -I -i -J -L -l -m -O -o -p -Q -R -S -W -w")}

// sshInner is the command ssh hands to the shell of the other end: the words
// after the host, joined by spaces, and ssh reads options after the host as
// well. The other end may be this machine (ssh localhost), so the line is a
// command like any other for the fixed rules.
func sshInner(args []word) []word {
	rest := sshOptions.skipOpts(args, nil)
	if len(rest) < 2 {
		return nil
	}
	rest = sshOptions.skipOpts(rest[1:], nil)
	if len(rest) == 0 {
		return nil
	}
	return shellWords(strings.Join(wordsText(rest), " "))
}

// pythonRunners are the modules python -m starts that run another program:
// python -m pipx run claude is pipx run claude.
var pythonRunners = set("pipx uv poetry pipenv pdm rye hatch")

// pythonRunner is the runner python -m starts, as a command: the module and
// the words after it. The options of python that take a value (-W, -X) and the
// letters of a cluster (-Bm pipx, -mpipx) are read as python reads them.
func pythonRunner(cn string, args []word) []word {
	switch normalizeInterpreter(cn) {
	case "python", "python2", "python3", "py":
	default:
		return nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i].text
		switch {
		case a == "--check-hash-based-pycs":
			i++
			continue
		case strings.HasPrefix(a, "--"):
			continue
		case !strings.HasPrefix(a, "-") || a == "-":
			return nil // a script
		}
		for j := 1; j < len(a); j++ {
			switch a[j] {
			case 'c':
				return nil // inline code
			case 'W', 'X', 'Q':
				if j+1 == len(a) {
					i++
				}
				j = len(a)
			case 'm':
				module, rest := a[j+1:], args[i+1:]
				if module == "" {
					if len(rest) == 0 {
						return nil
					}
					module, rest = rest[0].text, rest[1:]
				}
				module = strings.ToLower(strings.SplitN(module, ".", 2)[0])
				if !pythonRunners[module] {
					return nil
				}
				return append([]word{{text: module, assign: -1}}, rest...)
			}
		}
	}
	return nil
}

// winCreate are the command lines that a Windows program which creates a
// process takes as text: wmic process call create "cmd", Invoke-WmiMethod
// -Class Win32_Process -Name Create "cmd", schtasks /create /tr "cmd" (the task
// runs it later, in the account that made it) and the calls that start a
// process in the script a line of mshta carries.
func winCreate(cn string, args []word) []string {
	texts := wordsText(args)
	switch cn {
	case "wmic":
		for i := 0; i+2 < len(texts); i++ {
			if strings.EqualFold(texts[i], "call") && strings.EqualFold(texts[i+1], "create") {
				return commandLines(texts[i+2:])
			}
		}
	case "invoke-wmimethod", "iwmi":
		return wmiCreate(args)
	case "schtasks":
		for i, t := range texts {
			if l := strings.ToLower(t); (l == "/tr" || l == "-tr") && i+1 < len(texts) {
				return []string{texts[i+1]}
			}
		}
	case "mshta":
		return scriptRuns(texts)
	}
	return nil
}

// commandLines are the command lines that the words of an argument list stand
// for: the words joined by spaces, and each item of a list with commas
// ('git push', 'C:\Temp'), whose first is the command line of Win32_Process.Create.
func commandLines(texts []string) []string {
	if len(texts) == 0 {
		return nil
	}
	joined := strings.Join(texts, " ")
	lines := []string{joined}
	if strings.Contains(joined, ",") {
		for _, item := range strings.Split(joined, ",") {
			if item = strings.TrimSpace(item); item != "" {
				lines = append(lines, item)
			}
		}
	}
	return lines
}

// wmiParams are the parameters of Invoke-WmiMethod, true for those that take a
// value, and wmiAliases the other names PowerShell gives them.
var wmiParams = map[string]bool{
	"class": true, "path": true, "name": true, "argumentlist": true, "inputobject": true, "namespace": true, "computername": true,
	"credential": true, "authority": true, "impersonation": true, "authentication": true, "locale": true, "throttlelimit": true,
	"asjob": false, "enableallprivileges": false,
	"erroraction": true, "warningaction": true, "informationaction": true, "errorvariable": true, "warningvariable": true,
	"informationvariable": true, "outvariable": true, "outbuffer": true, "pipelinevariable": true,
	"verbose": false, "debug": false, "whatif": false, "confirm": false,
}

var wmiAliases = map[string]string{
	"args": "argumentlist", "cn": "computername", "ea": "erroraction", "wa": "warningaction", "infa": "informationaction",
	"ev": "errorvariable", "wv": "warningvariable", "iv": "informationvariable", "ov": "outvariable", "ob": "outbuffer",
	"pv": "pipelinevariable", "vb": "verbose", "db": "debug", "wi": "whatif", "cf": "confirm",
}

// wmiParam reads a word as a parameter of Invoke-WmiMethod: -Name, or
// -Name:value, whose value comes after a colon (quoted or not).
func wmiParam(a word) (name, attached string, hasAttached, ok bool) {
	t := a.text
	if a.expand || len(t) < 2 || t[0] != '-' || t[1]|0x20 < 'a' || t[1]|0x20 > 'z' {
		return "", "", false, false
	}
	if a.quoted && !strings.Contains(t, ":") {
		return "", "", false, false // a quoted word that only looks like a parameter
	}
	name, attached, hasAttached = strings.Cut(t[1:], ":")
	return name, attached, hasAttached, true
}

// wmiCreate are the command lines of Invoke-WmiMethod when the method is
// Create (Win32_Process.Create takes the command line first): the
// -ArgumentList, under any abbreviation of its name, attached or not, and the
// positional words, which are the class, the method and the argument list when
// they are not given by name.
func wmiCreate(args []word) []string {
	var positional, list []string
	create := false
	for i := 0; i < len(args); i++ {
		name, val, haveVal, isParam := wmiParam(args[i])
		if !isParam {
			positional = append(positional, args[i].text)
			continue
		}
		canon, ok := pwshParam(name, wmiParams, wmiAliases)
		if !ok {
			continue
		}
		if !haveVal && wmiParams[canon] && i+1 < len(args) {
			i++
			val, haveVal = args[i].text, true
		}
		switch canon {
		case "name":
			create = create || haveVal && strings.EqualFold(val, "create")
		case "argumentlist":
			for haveVal {
				list = append(list, val)
				// 'a', 'b': a word that ends with the comma goes on in the next.
				if !strings.HasSuffix(val, ",") || i+1 >= len(args) {
					break
				}
				i++
				val = args[i].text
			}
		}
	}
	var lines []string
	for _, p := range positional {
		if strings.EqualFold(p, "create") {
			create = true
		} else {
			lines = append(lines, p)
		}
	}
	if !create {
		return nil
	}
	return append(commandLines(list), lines...)
}

var (
	// scriptRun finds the command line of the calls that start a process in
	// the script of a line of mshta: WScript.Shell Run and Exec, in JScript
	// with parentheses and in VBScript without them; scriptShellExecute those
	// of Shell.Application, whose program and arguments come apart.
	scriptRun          = regexp.MustCompile(`(?i)\b(?:run|exec)\s*\(?\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)')`)
	scriptShellExecute = regexp.MustCompile(`(?i)\bshellexecute\s*\(\s*(?:"([^"]*)"|'([^']*)')\s*(?:,\s*(?:"([^"]*)"|'([^']*)'))?`)
)

// scriptRuns are the command lines that the literal calls of a script start.
func scriptRuns(texts []string) []string {
	var out []string
	for _, t := range texts {
		for _, m := range scriptRun.FindAllStringSubmatch(t, -1) {
			out = append(out, m[1]+m[2])
		}
		for _, m := range scriptShellExecute.FindAllStringSubmatch(t, -1) {
			out = append(out, strings.TrimSpace(m[1]+m[2]+" "+m[3]+m[4]))
		}
	}
	return out
}
