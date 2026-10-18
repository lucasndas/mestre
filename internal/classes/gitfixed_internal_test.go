package classes

import (
	"slices"
	"strings"
	"testing"
)

// gitSubcommands are the git subcommands the probe tries: the porcelain and
// the plumbing that write.
var gitSubcommands = strings.Fields(`add am apply archive bisect blame branch bundle checkout cherry-pick clean clone commit
config describe diff fetch fetch-pack filter-branch filter-repo format-patch fsck gc grep http-push init log ls-files
ls-remote maintenance merge mv notes pack-refs prune prune-packed pull push range-diff read-tree rebase reflog remote
repack replace request-pull reset restore revert rm send-pack shortlog show sparse-checkout stash status submodule
switch symbolic-ref tag update-index update-ref worktree write-tree fast-import commit-graph multi-pack-index index-pack unpack-objects
bundle pack-objects clone init`)

// The reach of a pattern knows every git subcommand the fixed rules deny:
// gitFixed is the same set the git analysis uses.
func TestGitFixedIsTheSetTheFixedRulesDeny(t *testing.T) {
	t.Parallel()
	e := riskEnv()
	probes := []string{"", " x y", " -d x", " -D x", " -b x", " -c x", " -x", " -f x", " set a b", " add x y", " expire", " a b"}
	for _, sub := range gitSubcommands {
		denied := false
		for _, p := range probes {
			if e.shell("git "+sub+p, "/w", 0).fixed.v == Deny {
				denied = true
				break
			}
		}
		if denied != slices.Contains(gitFixed, sub) {
			t.Errorf("git %s: denied by a fixed rule %v, in gitFixed %v", sub, denied, !denied)
		}
	}
	for _, sub := range gitFixed {
		if why := reachesFixed("git " + sub + " *"); why == "" {
			t.Errorf("git %s * does not reach a fixed rule", sub)
		}
	}
	for _, p := range []string{"git n*", "git p*", "git s*"} {
		if reachesFixed(p) == "" {
			t.Errorf("%s does not reach a fixed rule", p)
		}
	}
}
