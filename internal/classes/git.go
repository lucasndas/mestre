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
	"worktree", "tag", "reflog", "notes", "stash", "symbolic-ref", "config", "submodule", "clean", "branch", "checkout", "switch",
}

func (e *evaluator) git(args []word, st *shellState) result {
	res := newResult()
	g, gst := e.parseGit(args, st, &res)
	rest := g.rest
	texts := wordsText(rest)
	deny := func(why string) { res.fixedDeny(why) }
	switch g.sub {
	case "push", "send-pack", "http-push":
		deny("git push é sempre feito pelo motor: peça o envio com mestre pr open ou mestre pr update")
	case "fetch", "pull", "fetch-pack":
		deny("git " + g.sub + " atualiza refs do espelho, que é do motor")
	case "remote":
		if len(texts) > 0 && !slices.Contains([]string{"-v", "--verbose", "show", "get-url"}, texts[0]) {
			deny("mudar remotos é só do motor")
		}
	case "update-ref", "filter-branch", "filter-repo", "gc", "prune", "repack", "pack-refs", "maintenance", "prune-packed", "replace", "fast-import":
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
	default:
		for _, w := range rest {
			if looksLikePath(w.text) && !strings.HasPrefix(w.text, "-") {
				res.merge(e.argPath(w, use{}, gst))
			}
			if v, ok := strings.CutPrefix(w.text, "--output="); ok {
				ow := w
				ow.text = v
				res.merge(e.argPath(ow, use{write: true}, gst))
			}
		}
	}
	return res
}

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
	case hasAny(rest, "-u", "--set-upstream-to", "--unset-upstream", "--edit-description", "-t", "--track", "--no-track"):
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
		for _, w := range rest[i+1:] {
			res.merge(e.argPath(w, use{write: true}, st))
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
			res.merge(e.argPath(word{text: p, assign: -1}, use{write: true}, st))
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
	branch, err := e.env.Git.Branch(e.ctx, e.env.Worktree)
	if err != nil {
		res.rule(e.unverifiable("não foi possível ler a branch: " + err.Error()))
		return
	}
	if !e.ownBranch(branch) {
		res.rule(e.deny(e.r.originField("git.branches"), fmt.Sprintf("commit na branch %s, fora de git.branches %v", branch, e.r.Git.Branches)))
		return
	}
	all := hasShortFlag(rest, 'a') || hasAny(rest, "--all", "-i", "--include", "-o", "--only") ||
		len(positionals(rest, map[string]bool{"-m": true, "--message": true, "-F": true, "--file": true, "-C": true, "-c": true, "--reuse-message": true, "--reedit-message": true, "--fixup": true, "--squash": true, "--author": true, "--date": true, "-t": true, "--template": true, "--cleanup": true, "-u": true, "--trailer": true})) > 0
	files, err := e.env.Git.Committed(e.ctx, e.env.Worktree, all)
	if err != nil {
		res.rule(e.unverifiable("não foi possível ler os arquivos preparados: " + err.Error()))
		return
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

// unverifiable is the outcome of a check that cannot be made: deny, the
// engine down says so.
func (e *evaluator) unverifiable(why string) outcome {
	if e.degraded {
		return outcome{v: Deny, reason: DegradedReason, layer: LayerDegraded}
	}
	return outcome{v: Deny, reason: why, layer: LayerFixed}
}
