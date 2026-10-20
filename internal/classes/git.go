package classes

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// The worktrees of the tables share the refs of the mirror, so the fixed
// rules deny what changes refs of other branches, other worktrees or the
// mirror (docs/classes/permissoes.md#regras-fixas-de-segurança); on the
// table's own branch, the git block of the effective rules holds.

// gitArgs is a git command line after the global options.
type gitArgs struct {
	sub  string
	rest []word
}

// parseGit reads the global options; -C and --git-dir that leave the
// worktree are denied.
func (e *evaluator) parseGit(args []word, st *shellState, res *result) (gitArgs, *shellState) {
	cur := st
	i := 0
	for ; i < len(args); i++ {
		a := args[i].text
		if !strings.HasPrefix(a, "-") {
			break
		}
		name, val, hasVal := strings.Cut(a, "=")
		takesValue := func() (word, bool) {
			if hasVal {
				w := args[i]
				w.text = val
				return w, true
			}
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return word{}, false
		}
		switch name {
		case "-C", "--git-dir", "--work-tree":
			w, ok := takesValue()
			if !ok {
				continue
			}
			ks, known := e.resolve(w.text, cur)
			if !known || w.expand || w.tilde {
				res.fixedDeny(fmt.Sprintf("git %s com uma pasta que não dá para conferir", name))
				continue
			}
			for _, k := range ks {
				if !within(k, e.wt) {
					res.fixedDeny(fmt.Sprintf("git %s aponta para fora do worktree", name))
				}
			}
			if name == "-C" {
				next := &shellState{}
				for _, k := range ks {
					next.cwds = append(next.cwds, filepath.FromSlash(k))
				}
				cur = next
			}
		case "--namespace":
			res.fixedDeny("git --namespace escreve refs do espelho")
			takesValue()
		case "-c", "--exec-path", "--config-env", "--super-prefix":
			if !hasVal && name != "--exec-path" {
				i++
			}
		}
	}
	g := gitArgs{}
	if i < len(args) {
		g.sub, g.rest = args[i].text, args[i+1:]
	}
	return g, cur
}

// gitGlobalValue are the git global options that take a value: skipping
// them reaches the subcommand.
var gitGlobalValue = map[string]bool{"-C": true, "--git-dir": true, "--work-tree": true, "--namespace": true, "-c": true, "--config-env": true, "--super-prefix": true}

// gitSubText is "git <subcommand> <rest…>" after the global options, or ""
// when there is no subcommand: so the risk table and the signals match a
// writer whatever the global options before it (git -C . apply, git
// --no-pager am).
func gitSubText(args []word) string {
	i := 0
	for i < len(args) {
		a := args[i].text
		if !strings.HasPrefix(a, "-") {
			break
		}
		if name, _, hasVal := strings.Cut(a, "="); gitGlobalValue[name] && !hasVal {
			i++
		}
		i++
	}
	if i >= len(args) {
		return ""
	}
	return "git " + strings.Join(wordsText(args[i:]), " ")
}

// gitReadOnly reports git status|diff|log|show without a way to run
// something else: -c and --exec-path can name a pager, a hook or a program.
func gitReadOnly(args []word) bool {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i].text, "-") {
		t := args[i].text
		if t == "-c" || strings.HasPrefix(t, "--config-env") || strings.HasPrefix(t, "--exec-path") {
			return false
		}
		if t == "-C" || t == "--git-dir" || t == "--work-tree" {
			i++
		}
		i++
	}
	if i >= len(args) {
		return false
	}
	switch args[i].text {
	case "status", "diff", "log", "show":
		for _, a := range args[i+1:] {
			if a.text == "--output" || strings.HasPrefix(a.text, "--output=") || a.text == "-o" {
				return false
			}
		}
		return true
	}
	return false
}

// positionals are the words that are not options; with value, the options
// that take one consume the next word.
func positionals(ws []word, value map[string]bool) []string {
	var out []string
	for i := 0; i < len(ws); i++ {
		t := ws[i].text
		if t == "--" {
			for _, w := range ws[i+1:] {
				out = append(out, w.text)
			}
			break
		}
		if strings.HasPrefix(t, "-") && t != "-" {
			if value[t] {
				i++
			}
			continue
		}
		out = append(out, t)
	}
	return out
}

func hasAny(ws []word, flags ...string) bool {
	for _, w := range ws {
		if slices.Contains(flags, w.text) {
			return true
		}
		for _, f := range flags {
			if strings.HasPrefix(f, "--") && strings.HasPrefix(w.text, f+"=") {
				return true
			}
		}
	}
	return false
}

// ownBranch reports whether a branch is one the table may use (git.branches).
func (e *evaluator) ownBranch(b string) bool {
	b = strings.TrimPrefix(b, "refs/heads/")
	for _, p := range e.r.Git.Branches {
		if matchGlob(p, b) {
			return true
		}
	}
	return false
}

// gitFixed are the subcommands git denies by a fixed rule, whole or with
// some options (below): the reach of a command pattern (reachesFixed) is
// derived from them, and a test keeps the two in step.
var gitFixed = []string{
	"push", "send-pack", "http-push", "fetch", "pull", "fetch-pack", "remote",
	"update-ref", "filter-branch", "filter-repo", "gc", "prune", "repack", "pack-refs", "maintenance", "prune-packed", "replace", "fast-import",
	"commit-graph", "multi-pack-index", "index-pack", "unpack-objects",
	"worktree", "tag", "reflog", "notes", "stash", "symbolic-ref", "config", "submodule", "clean", "branch", "checkout", "switch",
}

// gitServer are the git subcommands that serve or transport a repository,
// open a shell, or run as a remote daemon: a fixed deny, since they move
// refs of the mirror (the tables share them) or run programs. Their dashed
// forms (git-receive-pack) and the remote helpers (git remote-ext,
// git-remote-https) are denied too (gitDashedFixed, the remote- prefix).
var gitServer = []string{
	"receive-pack", "upload-pack", "upload-archive", "shell", "daemon", "http-backend", "instaweb",
}

// gitFixedSet is gitFixed and gitServer by name: an alias on the command
// line that runs one of them (git -c alias.x=push) is a fixed deny.
var gitFixedSet = func() map[string]bool {
	m := make(map[string]bool, len(gitFixed)+len(gitServer))
	for _, s := range gitFixed {
		m[s] = true
	}
	for _, s := range gitServer {
		m[s] = true
	}
	return m
}()

// gitDashedFixed reports a dashed git form (git-push, git-receive-pack,
// git-remote-ext) whose subcommand is a fixed deny.
func gitDashedFixed(sub string) bool {
	return gitFixedSet[sub] || strings.HasPrefix(sub, "remote-")
}

// isGitProgram reports a program that is git or a dashed git form.
func isGitProgram(cn string) bool {
	return cn == "git" || strings.HasPrefix(cn, "git-")
}

func (e *evaluator) git(args []word, st *shellState, depth int) result {
	res := newResult()
	g, gst := e.parseGit(args, st, &res)
	e.gitExec(args, gst, depth, &res)
	rest := g.rest
	texts := wordsText(rest)
	deny := func(why string) { res.fixedDeny(why) }
	if strings.HasPrefix(g.sub, "remote-") && g.sub != "remote" {
		deny("git " + g.sub + " é um ajudante de transporte do git e pode rodar um programa")
		return res
	}
	switch g.sub {
	case "receive-pack", "upload-pack", "upload-archive", "shell", "daemon", "http-backend", "instaweb":
		deny("git " + g.sub + " serve ou transporta o repositório: pode mover refs do espelho (compartilhadas) ou rodar um programa")
	case "push", "send-pack", "http-push":
		deny("git push é sempre feito pelo motor: peça o envio com mestre pr open ou mestre pr update")
	case "fetch", "pull", "fetch-pack":
		deny("git " + g.sub + " atualiza refs do espelho, que é do motor")
	case "remote":
		if len(texts) > 0 && !slices.Contains([]string{"-v", "--verbose", "show", "get-url"}, texts[0]) {
			deny("mudar remotos é só do motor")
		}
	case "update-ref", "filter-branch", "filter-repo", "gc", "prune", "repack", "pack-refs", "maintenance", "prune-packed", "replace", "fast-import",
		"commit-graph", "multi-pack-index", "index-pack", "unpack-objects":
		if g.sub == "replace" && (len(texts) == 0 || texts[0] == "-l" || texts[0] == "--list") {
			break
		}
		deny("git " + g.sub + " altera refs ou objetos do espelho, compartilhados por todas as mesas")
	case "worktree":
		if len(texts) == 0 || texts[0] != "list" {
			deny("git worktree mexe nos worktrees de todas as mesas")
		}
	case "tag":
		if len(positionals(rest, nil)) > 0 && !hasAny(rest, "-l", "--list") || hasAny(rest, "-d", "--delete", "-a", "-s", "-f", "--force", "-m", "-F", "-u", "--annotate", "--sign") {
			deny("git tag cria ou apaga refs do espelho")
		}
	case "reflog":
		if len(texts) > 0 && slices.Contains([]string{"expire", "delete", "drop"}, texts[0]) {
			deny("git reflog " + texts[0] + " altera o espelho")
		}
	case "notes":
		if len(texts) > 0 && !slices.Contains([]string{"list", "show"}, texts[0]) {
			deny("git notes escreve refs do espelho")
		}
	case "stash":
		if len(texts) == 0 || !slices.Contains([]string{"list", "show"}, texts[0]) {
			deny("git stash grava refs/stash, compartilhada por todas as mesas")
		}
	case "symbolic-ref":
		if len(positionals(rest, nil)) >= 2 || hasAny(rest, "-d", "--delete") {
			deny("git symbolic-ref altera refs")
		}
	case "config":
		if gitConfigWrites(rest) {
			deny("a configuração do repositório é do espelho, compartilhada por todas as mesas")
		}
	case "submodule":
		if len(texts) > 0 && !slices.Contains([]string{"status", "summary"}, texts[0]) {
			deny("git submodule altera a configuração e .git/modules do espelho")
		}
	case "clean":
		if hasShortFlag(rest, 'x', 'X') {
			deny("git clean -x apaga também .mestre/ e os arquivos ignorados")
		}
		for _, p := range positionals(rest, map[string]bool{"-e": true, "--exclude": true}) {
			res.merge(e.argPath(word{text: p, assign: -1}, use{write: true, recursive: true}, gst))
		}
	case "branch":
		e.gitBranch(&res, rest)
	case "checkout", "switch":
		e.gitSwitch(&res, g.sub, rest, gst)
	case "commit":
		e.gitCommit(&res, rest, gst)
	case "merge", "rebase", "cherry-pick", "revert", "am":
		if hasAny(rest, "--abort", "--quit") {
			break
		}
		switch e.r.Git.Commit {
		case Allow:
		case Ask:
			res.rule(e.commitAsk())
		default:
			res.rule(e.deny(e.r.originField("git.commit"), fmt.Sprintf("git %s cria commits e a classe não commita (git.commit)", g.sub)))
		}
		if g.sub == "rebase" {
			if ps := positionals(rest, map[string]bool{"--onto": true, "-s": true, "-X": true, "--strategy": true, "--exec": true, "-x": true}); len(ps) >= 2 && !e.ownBranch(ps[1]) {
				res.rule(e.deny(e.r.originField("git.branches"), fmt.Sprintf("git rebase troca para %s, fora de git.branches %v", ps[1], e.r.Git.Branches)))
			}
		}
	case "rm", "mv", "restore":
		for _, p := range positionals(rest, map[string]bool{"-s": true, "--source": true, "--pathspec-from-file": true}) {
			res.merge(e.argPath(word{text: p, assign: -1}, use{write: true, recursive: g.sub != "restore"}, gst))
		}
		if g.sub == "restore" && (flagValue(rest, "-s", "--source") != nil || hasAny(rest, "-s")) {
			// restore --source <ref> -- <paths>: brings paths from another
			// tree; a pathspec that includes a protected path is a fixed deny.
			for _, p := range positionals(rest, map[string]bool{"-s": true, "--source": true, "--pathspec-from-file": true}) {
				res.merge(e.argPath(word{text: p, assign: -1}, use{write: true, recursive: true}, gst))
			}
		}
	case "checkout-index":
		// Plumbing that writes tracked paths to the worktree; -a all, or the
		// pathspecs after -- / as positionals. A protected path is denied, and
		// so is --prefix=<dir>, which moves every write under <dir> (checked
		// like read-tree --prefix). --stdin names the paths in an input the
		// line does not show: they may be protected, so it is a fixed deny.
		for _, p := range gitPathspecs(rest) {
			res.merge(e.argPath(word{text: p, assign: -1}, use{write: true, recursive: true}, gst))
		}
		if w := flagValue(rest, "--prefix"); w != nil {
			res.merge(e.argPath(*w, use{write: true, recursive: true}, gst))
		}
		if hasAny(rest, "--stdin") {
			deny("git checkout-index --stdin escreve caminhos que a linha não mostra e que podem ser protegidos")
		}
		if len(gitPathspecs(rest)) == 0 && hasAny(rest, "-a", "--all") {
			res.atLeastAsk("git checkout-index -a escreve todos os arquivos preparados na árvore")
		}
	case "read-tree":
		// read-tree --prefix=<dir> -u writes <dir> from a tree; a prefix
		// into a protected path is a fixed deny.
		if w := flagValue(rest, "--prefix"); w != nil {
			res.merge(e.argPath(*w, use{write: true, recursive: true}, gst))
		}
	case "update-index":
		// --cacheinfo <mode,sha,path> stages a path into the shared index; a
		// protected path is a fixed deny. --index-info and --stdin read the
		// entries or paths from an input the line does not show, so the paths
		// are unknown and may be protected: also a fixed deny.
		if hasAny(rest, "--index-info", "--stdin") {
			deny("git update-index --index-info/--stdin grava no índice compartilhado caminhos que a linha não mostra e que podem ser protegidos")
		}
		for _, p := range cacheInfoPaths(rest) {
			res.merge(e.argPath(word{text: p, assign: -1}, use{write: true}, gst))
		}
	default:
		writes := gitWrittenPaths(g.sub, rest)
		written := make(map[int]bool, len(writes))
		for _, wr := range writes {
			written[wr.at] = true
			if isDevice(wr.w.text) {
				continue
			}
			u := use{write: true, recursive: wr.deep}
			if ks, known := e.resolve(wr.w.text, gst); known && u.recursive && slices.Contains(ks, e.wt) {
				// the root of the worktree: the patch names the paths below it,
				// which the line does not show, as it does without --directory
				continue
			}
			res.merge(e.argPath(wr.w, u, gst))
		}
		for i, w := range rest {
			if !written[i] && looksLikePath(w.text) && !strings.HasPrefix(w.text, "-") {
				res.merge(e.argPath(w, use{}, gst))
			}
		}
	}
	return res
}

// gitWrite is a path a git command writes: w is the path (the value of the
// option, or the operand) and at the index in the arguments of the word that
// holds it, whole or with its option attached.
type gitWrite struct {
	w    word
	at   int
	deep bool // the write is a tree under the path (git apply --directory)
}

// gitWrittenPaths are the paths a git subcommand writes because it is told
// to: the output of --output (any command that takes the diff options),
// archive -o, format-patch -o and --output-directory, bundle create <file>,
// the base name of pack-objects, and the folder clone and init make. They
// are paths like any other write: .git/**, .mestre/** and the files block
// judge them. The forms with a space and the ones with the value attached
// (--output=f, -of) are both read.
func gitWrittenPaths(sub string, rest []word) []gitWrite {
	var out []gitWrite
	long := func(i int, name string) {
		w := rest[i]
		if v, ok := strings.CutPrefix(w.text, name+"="); ok {
			w.text = v
			out = append(out, gitWrite{w: w, at: i})
		} else if w.text == name && i+1 < len(rest) {
			out = append(out, gitWrite{w: rest[i+1], at: i + 1})
		}
	}
	short := func(i int, letter string) {
		w := rest[i]
		if !strings.HasPrefix(w.text, "-"+letter) || strings.HasPrefix(w.text, "--") {
			return
		}
		if v := w.text[2:]; v != "" {
			w.text = v
			out = append(out, gitWrite{w: w, at: i})
		} else if i+1 < len(rest) {
			out = append(out, gitWrite{w: rest[i+1], at: i + 1})
		}
	}
	for i := range rest {
		if rest[i].text == "--" {
			break
		}
		long(i, "--output")
		switch sub {
		case "archive":
			short(i, "o")
		case "format-patch":
			short(i, "o")
			long(i, "--output-directory")
		case "clone", "init":
			long(i, "--separate-git-dir")
		case "apply":
			// git apply --directory=D roots every path of the patch under D.
			n := len(out)
			long(i, "--directory")
			for k := n; k < len(out); k++ {
				out[k].deep = true
			}
		}
	}
	taken := map[int]bool{}
	for _, wr := range out {
		taken[wr.at] = true
	}
	var ops []int // the operands, without the value of an option that takes one
	value := map[string]bool(nil)
	if sub == "clone" {
		value = gitCloneValue
	}
	for i := 0; i < len(rest); i++ {
		t := rest[i].text
		if t == "--" {
			for j := i + 1; j < len(rest); j++ {
				ops = append(ops, j)
			}
			break
		}
		if strings.HasPrefix(t, "-") && t != "-" {
			if value[t] {
				i++
			}
			continue
		}
		if !taken[i] {
			ops = append(ops, i)
		}
	}
	switch sub {
	case "bundle":
		// bundle create [options] <file> <rev-list-args>: - is the output.
		if len(ops) >= 2 && rest[ops[0]].text == "create" && rest[ops[1]].text != "-" {
			out = append(out, gitWrite{w: rest[ops[1]], at: ops[1]})
		}
	case "pack-objects", "init":
		// pack-objects <base-name> and init [<directory>]: the operands (an
		// option's value among them, git init -b main dir) are judged as
		// writes, which costs nothing inside the worktree.
		for _, at := range ops {
			out = append(out, gitWrite{w: rest[at], at: at})
		}
	case "clone":
		// clone [options] <repository> [<directory>]
		if len(ops) >= 2 {
			at := ops[len(ops)-1]
			out = append(out, gitWrite{w: rest[at], at: at})
		}
	}
	return out
}

// gitCloneValue are the options of git clone that take the next word.
var gitCloneValue = set("-b --branch --depth -o --origin -j --jobs --reference --reference-if-able --template --separate-git-dir -c --config -u --upload-pack --filter --shallow-since --shallow-exclude --bundle-uri --server-option --revision")

// gitConfigWrites reports whether a git config command changes a value.
func gitConfigWrites(rest []word) bool {
	texts := wordsText(rest)
	if len(texts) > 0 {
		switch texts[0] {
		case "get", "list":
			return false
		case "set", "unset", "rename-section", "remove-section", "edit":
			return true
		}
	}
	if hasAny(rest, "--unset", "--unset-all", "--add", "--replace-all", "--rename-section", "--remove-section", "-e", "--edit") {
		return true
	}
	if hasAny(rest, "--get", "--get-all", "--get-regexp", "--get-urlmatch", "-l", "--list", "--get-color", "--get-colorbool") {
		return false
	}
	return len(positionals(rest, map[string]bool{"-f": true, "--file": true, "--blob": true, "--type": true, "--default": true})) >= 2
}

// gitBranch: listing is free; creating, moving, copying, deleting or
// setting the upstream of a branch other than the table's is denied.
func (e *evaluator) gitBranch(res *result, rest []word) {
	ps := positionals(rest, map[string]bool{"--contains": true, "--no-contains": true, "--merged": true, "--no-merged": true, "--points-at": true, "--sort": true, "--format": true, "-u": true, "--set-upstream-to": true})
	listing := hasAny(rest, "-l", "--list", "-a", "--all", "-r", "--remotes", "--show-current", "--contains", "--no-contains", "--merged", "--no-merged", "--points-at", "-v", "-vv", "--verbose")
	var targets []string
	switch {
	case hasAny(rest, "-d", "-D", "--delete"):
		targets = ps
	case hasAny(rest, "-m", "-M", "--move", "-c", "-C", "--copy"):
		targets = ps
		if len(ps) == 0 {
			targets = []string{""}
		}
	case hasShortFlag(rest, 'u') || hasAny(rest, "--set-upstream-to", "--unset-upstream", "--edit-description"):
		// The upstream and the description of a branch are lines of the
		// configuration of the repository (branch.<name>.*), which is the
		// mirror's: the same write as git config, whichever branch it names.
		res.fixedDeny("git branch -u|--set-upstream-to, --unset-upstream e --edit-description gravam a configuração do repositório, que é do espelho e compartilhada por todas as mesas")
		return
	case hasAny(rest, "-t", "--track", "--no-track"):
		targets = ps
		if len(targets) == 0 {
			return // the current branch, which is the table's
		}
	case hasAny(rest, "-f", "--force") || (!listing && len(ps) > 0):
		if len(ps) > 0 {
			targets = ps[:1] // the rest is the start point
		}
	default:
		return
	}
	for _, b := range targets {
		if b == "" || !e.ownBranch(b) {
			name := b
			if name == "" {
				name = "a branch atual"
			}
			res.fixedDeny(fmt.Sprintf("git branch altera %s, que não é a branch desta mesa (os worktrees compartilham as refs do espelho)", name))
			return
		}
	}
}

// gitSwitch: creating a branch other than the table's is a fixed denial;
// switching goes by git.branches; checkout -- writes the paths after it.
func (e *evaluator) gitSwitch(res *result, sub string, rest []word, st *shellState) {
	if i := slices.IndexFunc(rest, func(w word) bool { return w.text == "--" }); i >= 0 {
		// git checkout [<ref>] -- <paths>: a pathspec restores a tree, so it
		// is a recursive write; one that includes a protected path (-- .,
		// -- .claude) is a fixed deny (holdsProtected, protectedWrite).
		for _, w := range rest[i+1:] {
			res.merge(e.argPath(w, use{write: true, recursive: true}, st))
		}
		rest = rest[:i]
		if len(positionals(rest, nil)) <= 1 {
			return // git checkout [<ref>] -- <paths>: files, not a switch
		}
	}
	create := map[string]bool{"-b": true, "-B": true, "--orphan": true, "-c": true, "-C": true, "--create": true, "--force-create": true}
	for i, w := range rest {
		if create[w.text] {
			if i+1 >= len(rest) || !e.ownBranch(rest[i+1].text) {
				name := ""
				if i+1 < len(rest) {
					name = rest[i+1].text
				}
				res.fixedDeny(fmt.Sprintf("git %s %s cria a branch %s, que não é desta mesa", sub, w.text, name))
			}
			return
		}
	}
	if hasAny(rest, "--detach", "-d") && sub == "switch" {
		res.rule(e.deny(e.r.originField("git.branches"), "git switch --detach sai das branches de git.branches"))
		return
	}
	ps := positionals(rest, nil)
	if len(ps) == 0 {
		return
	}
	target := ps[0]
	if sub == "checkout" && (target == "." || strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../")) {
		for _, p := range ps {
			res.merge(e.argPath(word{text: p, assign: -1}, use{write: true, recursive: true}, st))
		}
		return
	}
	if !e.ownBranch(target) {
		res.rule(e.deny(e.r.originField("git.branches"), fmt.Sprintf("trocar para %s: fora de git.branches %v (para restaurar arquivos, git checkout -- <caminho>)", target, e.r.Git.Branches)))
	}
}

// gitCommit: commit only with git.commit, on a branch of git.branches and
// with every file it takes inside files.write.
func (e *evaluator) gitCommit(res *result, rest []word, st *shellState) {
	if e.r.Git.Commit != Allow && e.r.Git.Commit != Ask {
		res.rule(e.deny(e.r.originField("git.commit"), "a classe não commita (git.commit)"))
		return
	}
	if e.r.Git.Commit == Ask {
		res.rule(e.commitAsk())
	}
	for _, f := range []string{"-F", "--file"} {
		if w := flagValue(rest, f); w != nil && w.text != "-" {
			res.merge(e.argPath(*w, use{}, st))
		}
	}
	if e.env.Git == nil {
		res.rule(e.unverifiable("sem acesso ao git para conferir a branch e os arquivos preparados"))
		return
	}
	all := hasShortFlag(rest, 'a') || hasAny(rest, "--all", "-i", "--include", "-o", "--only") ||
		len(positionals(rest, map[string]bool{"-m": true, "--message": true, "-F": true, "--file": true, "-C": true, "-c": true, "--reuse-message": true, "--reedit-message": true, "--fixup": true, "--squash": true, "--author": true, "--date": true, "-t": true, "--template": true, "--cleanup": true, "-u": true, "--trailer": true})) > 0
	var files []string
	if cs, ok := e.env.Git.(CommitStater); ok {
		// One reading of the real state (one git process); the branch is
		// judged first, as below.
		state, err := e.commitState(cs, all)
		if err != nil {
			res.rule(e.unverifiable("não foi possível ler o estado do git: " + err.Error()))
			return
		}
		if state.BranchErr != nil {
			res.rule(e.unverifiable("não foi possível ler a branch: " + state.BranchErr.Error()))
			return
		}
		if !e.ownBranch(state.Branch) {
			res.rule(e.deny(e.r.originField("git.branches"), fmt.Sprintf("commit na branch %s, fora de git.branches %v", state.Branch, e.r.Git.Branches)))
			return
		}
		files = state.Files
	} else {
		branch, err := e.env.Git.Branch(e.ctx, e.env.Worktree)
		if err != nil {
			res.rule(e.unverifiable("não foi possível ler a branch: " + err.Error()))
			return
		}
		if !e.ownBranch(branch) {
			res.rule(e.deny(e.r.originField("git.branches"), fmt.Sprintf("commit na branch %s, fora de git.branches %v", branch, e.r.Git.Branches)))
			return
		}
		files, err = e.env.Git.Committed(e.ctx, e.env.Worktree, all)
		if err != nil {
			res.rule(e.unverifiable("não foi possível ler os arquivos preparados: " + err.Error()))
			return
		}
	}
	var outside []string
	for _, f := range files {
		k := e.key(filepath.Join(e.env.Worktree, filepath.FromSlash(f)))
		if why := e.fixedPath(k, use{write: true}); why != "" {
			res.fixedDeny("o commit leva " + f + ": " + why)
			return
		}
		if o := e.filePerm(k, true); o.v != Allow {
			outside = append(outside, f)
		}
	}
	if len(outside) > 0 {
		res.rule(e.deny(e.r.originField("files.write"), fmt.Sprintf("o commit leva arquivos fora de files.write %v: %s", e.r.Files.Write, strings.Join(outside, ", "))))
	}
}

// commitAsk is the request of a commit when the class does not say
// git.commit: the default, so a baixo one runs directly (ADR 0025); the
// branch and the files are still checked.
func (e *evaluator) commitAsk() outcome {
	return outcome{v: Ask, reason: "sem git.commit na classe, padrão ask", layer: e.r.originField("git.commit"), byDefault: true}
}

// gitExec scans a git command line for the features that run a program:
// aliases on the command line, config keys that run a program (-c,
// --config-env) and the subcommand options that run an external command
// (rebase -x, bisect run, difftool -x, grep -O). Each is at least alto
// (atLeastAsk, so it never runs on its own), and a literal command with a
// fixed rule inside (git -c core.pager='git push') is a fixed deny.
func (e *evaluator) gitExec(args []word, st *shellState, depth int, res *result) {
	i := 0
	for i < len(args) {
		a := args[i].text
		if !strings.HasPrefix(a, "-") || a == "-" {
			break
		}
		switch {
		case a == "-c":
			if i+1 < len(args) {
				i++
				e.gitConfig(args[i].text, args[i], st, depth, res)
			}
		case strings.HasPrefix(a, "-c") && !strings.HasPrefix(a, "--"):
			e.gitConfig(a[2:], args[i], st, depth, res)
		case a == "--config-env":
			if i+1 < len(args) {
				i++
				e.gitConfigEnv(args[i].text, res)
			}
		case strings.HasPrefix(a, "--config-env="):
			e.gitConfigEnv(strings.TrimPrefix(a, "--config-env="), res)
		default:
			if name, _, hasVal := strings.Cut(a, "="); gitGlobalValue[name] && !hasVal {
				i++
			}
		}
		i++
	}
	if i >= len(args) {
		return
	}
	e.gitSubExec(args[i].text, args[i+1:], st, depth, res)
}

// gitConfig analyses one -c key=value: an alias (alias.*) on the command
// line, with ! or naming a fixed subcommand, is a fixed deny; any alias is
// at least alto. A key that runs a program is at least alto, and its
// literal value is analysed as a command line (a fixed rule inside denies).
func (e *evaluator) gitConfig(raw string, w word, st *shellState, depth int, res *result) {
	key, val, _ := strings.Cut(raw, "=")
	if a, ok := strings.CutPrefix(strings.ToLower(key), "alias."); ok {
		_ = a
		res.atLeastAsk("git -c alias.* define um alias na linha de comando")
		cv := strings.TrimSpace(val)
		switch {
		case strings.HasPrefix(cv, "!"):
			res.fixedDeny("um alias com ! roda um comando de shell quando o alias é usado")
			if !w.expand {
				res.merge(e.shell(strings.TrimPrefix(cv, "!"), st.cwds[0], depth+1))
			}
		default:
			if f := firstField(cv); gitDashedFixed(f) {
				res.fixedDeny("o alias roda git " + f + ", uma regra fixa")
			}
		}
		return
	}
	if configRunsProgram(key) {
		res.atLeastAsk("git -c " + key + " define uma configuração que roda um programa")
		if val != "" && !w.expand && configValueIsCommand(key) {
			res.merge(e.shell(val, st.cwds[0], depth+1))
		}
	}
}

// configValueIsCommand reports a config key whose value is a command line
// (so a fixed rule inside it is analysed), as opposed to a directory
// (core.hooksPath) or a mode (protocol.*, uploadpack.* booleans).
func configValueIsCommand(key string) bool {
	k := strings.ToLower(key)
	switch k {
	case "core.pager", "core.fsmonitor", "core.editor", "core.sshcommand", "core.askpass",
		"core.alternaterefscommand", "sequence.editor", "diff.external", "credential.helper",
		"gpg.program", "gpg.ssh.program":
		return true
	}
	switch {
	case strings.HasPrefix(k, "pager."):
		return true
	case strings.HasPrefix(k, "filter.") && (strings.HasSuffix(k, ".clean") || strings.HasSuffix(k, ".smudge") || strings.HasSuffix(k, ".process")):
		return true
	case strings.HasPrefix(k, "diff.") && (strings.HasSuffix(k, ".textconv") || strings.HasSuffix(k, ".command")):
		return true
	case strings.HasPrefix(k, "merge.") && strings.HasSuffix(k, ".driver"):
		return true
	case strings.HasPrefix(k, "credential.") && strings.HasSuffix(k, ".helper"):
		return true
	case strings.HasPrefix(k, "uploadpack.") && strings.HasSuffix(k, "hook"):
		return true
	}
	return false
}

// gitConfigEnv analyses --config-env=key=ENVVAR: the value comes from the
// environment, so it cannot be read, but a key that runs a program (or an
// alias) is at least alto.
func (e *evaluator) gitConfigEnv(raw string, res *result) {
	key, _, _ := strings.Cut(raw, "=")
	if strings.HasPrefix(strings.ToLower(key), "alias.") {
		res.atLeastAsk("git --config-env define um alias na linha de comando")
		return
	}
	if configRunsProgram(key) {
		res.atLeastAsk("git --config-env " + key + " define uma configuração que roda um programa")
	}
}

// gitSubExec handles the subcommand options that run an external command.
func (e *evaluator) gitSubExec(sub string, rest []word, st *shellState, depth int, res *result) {
	analyse := func(cmd string) {
		res.atLeastAsk("git " + sub + " roda um comando externo")
		if strings.TrimSpace(cmd) != "" {
			res.merge(e.shell(cmd, st.cwds[0], depth+1))
		}
	}
	switch sub {
	case "rebase", "am", "sequence":
		if w := flagValue(rest, "-x", "--exec"); w != nil {
			analyse(w.text)
		}
	case "bisect":
		if ps := positionals(rest, nil); len(ps) > 0 && ps[0] == "run" {
			analyse(afterWord(rest, "run"))
		}
	case "difftool":
		if w := flagValue(rest, "-x", "--extcmd"); w != nil {
			analyse(w.text)
		}
	case "grep":
		if v, ok := grepOpenPager(rest); ok {
			analyse(v)
		}
	}
}

// configRunsProgram reports a git config key whose value runs a program.
func configRunsProgram(key string) bool {
	k := strings.ToLower(key)
	switch k {
	case "core.pager", "core.fsmonitor", "core.editor", "core.sshcommand", "core.hookspath",
		"core.askpass", "core.alternaterefscommand", "sequence.editor", "diff.external",
		"credential.helper", "gpg.program", "gpg.ssh.program":
		return true
	}
	switch {
	case strings.HasPrefix(k, "pager."), strings.HasPrefix(k, "filter."),
		strings.HasPrefix(k, "uploadpack."), strings.HasPrefix(k, "protocol."):
		return true
	case k == "include.path", strings.HasPrefix(k, "includeif.") && strings.HasSuffix(k, ".path"):
		// A file of configuration the line does not show: what it sets (a
		// core.fsmonitor, an alias with !) runs a program all the same.
		return true
	case strings.HasPrefix(k, "diff.") && (strings.HasSuffix(k, ".textconv") || strings.HasSuffix(k, ".command")):
		return true
	case strings.HasPrefix(k, "merge.") && strings.HasSuffix(k, ".driver"):
		return true
	case strings.HasPrefix(k, "credential.") && strings.HasSuffix(k, ".helper"):
		return true
	}
	return false
}

// firstField is the first whitespace-separated token of s.
func firstField(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

// afterWord joins the words after the first one equal to kw.
func afterWord(rest []word, kw string) string {
	for i, w := range rest {
		if w.text == kw {
			return strings.Join(wordsText(rest[i+1:]), " ")
		}
	}
	return ""
}

// grepOpenPager is the command of git grep -O/--open-files-in-pager.
func grepOpenPager(rest []word) (string, bool) {
	for i, w := range rest {
		t := w.text
		if v, ok := strings.CutPrefix(t, "--open-files-in-pager="); ok {
			return v, true
		}
		if t == "--open-files-in-pager" {
			if i+1 < len(rest) {
				return rest[i+1].text, true
			}
			return "", true
		}
		if v, ok := strings.CutPrefix(t, "-O"); ok {
			if v != "" {
				return v, true
			}
			if i+1 < len(rest) && !strings.HasPrefix(rest[i+1].text, "-") {
				return rest[i+1].text, true
			}
			return "", true
		}
	}
	return "", false
}

// gitPathspecs are the pathspecs of a plumbing command: the words after --,
// or the positionals that are not a flag's value when there is no --.
func gitPathspecs(rest []word) []string {
	if i := slices.IndexFunc(rest, func(w word) bool { return w.text == "--" }); i >= 0 {
		return wordsText(rest[i+1:])
	}
	var out []string
	for _, w := range rest {
		if !strings.HasPrefix(w.text, "-") {
			out = append(out, w.text)
		}
	}
	return out
}

// cacheInfoPaths are the paths of git update-index --cacheinfo: the form
// --cacheinfo <mode>,<sha>,<path> (one argument) or --cacheinfo <mode>
// <sha> <path> (three).
func cacheInfoPaths(rest []word) []string {
	var out []string
	for i := 0; i < len(rest); i++ {
		t := rest[i].text
		val, has := "", false
		if v, ok := strings.CutPrefix(t, "--cacheinfo="); ok {
			val, has = v, true
		} else if t == "--cacheinfo" {
			if i+1 < len(rest) && strings.Count(rest[i+1].text, ",") == 2 {
				val, has = rest[i+1].text, true
			} else if i+3 < len(rest) {
				out = append(out, rest[i+3].text)
				i += 3
				continue
			}
		}
		if has {
			if parts := strings.SplitN(val, ",", 3); len(parts) == 3 {
				out = append(out, parts[2])
			}
		}
	}
	return out
}

// unverifiable is the outcome of a check that cannot be made: deny, the
// engine down says so.
func (e *evaluator) unverifiable(why string) outcome {
	if e.degraded {
		return outcome{v: Deny, reason: DegradedReason, layer: LayerDegraded}
	}
	return outcome{v: Deny, reason: why, layer: LayerFixed}
}
