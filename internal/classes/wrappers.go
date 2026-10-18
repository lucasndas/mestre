package classes

import "strings"

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
	valued   map[string]bool
	operands int
	sub      string
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
	"firejail":  {},
	"rlwrap":    {valued: set("-b -C -D -f -F -H -l -M -p -P -q -s -S -t -z")},
	"systemd-run": {valued: set("-u --unit --description --slice -p --property -E --setenv --uid --gid --working-directory --service-type --nice " +
		"-M --machine -H --host --on-active --on-boot --on-startup --on-unit-active --on-unit-inactive --on-calendar --timer-property")},
	"env-cmd": {valued: set("-f --file -e --environments -r --rc-file")},
	"dotenv":  {valued: set("-e -v -p")},
	"dotenvx": {valued: set("-f --env-file -fk --env-keys-file -e --env"), sub: "run"},
	"doppler": {valued: set("-p --project -c --config -t --token --api-host --scope --name-transformer --preserve-env --fallback --mount"), sub: "run"},
	"direnv":  {sub: "exec", operands: 1},
}

// corepackManagers are the package managers corepack runs.
var corepackManagers = set("npm npx pnpm pnpx yarn yarnpkg")

// wrapped is the command a wrapper of the table, cross-env or corepack runs,
// as the words of a command, or nil.
func (e *evaluator) wrapped(cn string, args []word, res *result) []word {
	switch cn {
	case "cross-env":
		// cross-env NAME=value command: the assignments are env's.
		inner, _ := e.envInner(args, res)
		return inner
	case "corepack":
		if rest := skipDashOpts(args, nil); len(rest) > 0 && corepackManagers[rest[0].text] {
			return rest
		}
		return nil
	case "sg":
		// sg GROUP command, sg GROUP -c 'command line'
		rest := skipDashOpts(args, nil)
		if len(rest) < 2 {
			return nil
		}
		if rest = rest[1:]; rest[0].text == "-c" && len(rest) > 1 {
			return []word{{text: "sh", assign: -1}, {text: "-c", assign: -1}, rest[1]}
		}
		return rest
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
	rest = skipDashOpts(rest, spec.valued)
	if len(rest) < spec.operands {
		return nil
	}
	return rest[spec.operands:]
}

// winCreate is the command line a Windows program that creates a process
// takes in a string: wmic process call create "cmd" and Invoke-WmiMethod
// -Class Win32_Process -Name Create -ArgumentList "cmd".
func winCreate(cn string, args []word) (string, bool) {
	texts := wordsText(args)
	switch cn {
	case "wmic":
		for i := 0; i+2 < len(texts); i++ {
			if strings.EqualFold(texts[i], "call") && strings.EqualFold(texts[i+1], "create") {
				return strings.Join(texts[i+2:], " "), true
			}
		}
	case "invoke-wmimethod", "iwmi":
		create := false
		for i, t := range texts {
			if strings.EqualFold(pwshDashFix(t), "-name") && i+1 < len(texts) && strings.EqualFold(texts[i+1], "create") {
				create = true
			}
		}
		if !create {
			return "", false
		}
		for i, t := range texts {
			if l := strings.ToLower(pwshDashFix(t)); (l == "-argumentlist" || l == "-args") && i+1 < len(texts) {
				return strings.Join(texts[i+1:], " "), true
			}
		}
	}
	return "", false
}
