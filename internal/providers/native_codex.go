package providers

import (
	"os"
	"path/filepath"
	"strings"
)

// Native translates the policy into the start flags of Codex: the sandbox
// (read-only with no write, workspace-write otherwise, with no rule per
// path, and .git out of its write reach by the sandbox itself), the network
// of the sandbox, which is only on or off, and the hosted search only with
// internet and no list of domains (it does not go through a hook). The
// commands are contained by the sandbox; nothing else is native in Codex.
//
// The local channel (docs/provedores/codex.md#canal-local-dentro-do-sandbox):
// the socket and its folder never go in sandbox_workspace_write.writable_roots,
// which would let an agent unlink or replace the socket every character
// shares. With the network off, Codex on Linux denies every connect() by
// seccomp, Unix sockets included, and has no rule that frees one socket. So,
// on Linux, a class without internet runs with the network of the sandbox
// on, for the `mestre` CLI to reach the engine, and its internet is blocked
// only by the rules of the dispatcher, without the native barrier
// (CodexNoNativeNetwork, which the panel shows). On Windows the sandbox
// blocks only IP traffic, so the network stays off; the conformance test
// checks that the socket is reached, and its fallback (CodexOfflineNetwork)
// turns the network on there too.
func (c *codex) Native(p NativePolicy) (NativeControls, error) {
	if err := p.check(); err != nil {
		return NativeControls{}, err
	}
	sandbox, network := "workspace-write", "true"
	if p.NoWrite {
		sandbox = "read-only"
	}
	if p.Offline && !CodexNoNativeNetwork(p) {
		network = "false"
	}
	args := []string{"-s", sandbox, "-c", "sandbox_workspace_write.network_access=" + network}
	if roots := codexGitRoots(p.Worktree); sandbox == "workspace-write" && len(roots) > 0 {
		vals := make([]string, 0, len(roots))
		for _, r := range roots {
			v, err := tomlString(r)
			if err != nil {
				return NativeControls{}, err
			}
			vals = append(vals, v)
		}
		args = append(args, "-c", "sandbox_workspace_write.writable_roots=["+strings.Join(vals, ",")+"]")
	}
	if !p.Offline && p.AnyDomain {
		args = append(args, "--search")
	}
	return NativeControls{Args: args}, nil
}

// CodexNoNativeNetwork reports whether a Codex character of this policy
// runs without the native network barrier its class asks for: no internet,
// and the network of the sandbox on for the local channel (always on Linux;
// on Windows, with the fallback). The panel shows it on the sheet and the
// class check warns (docs/classes/reforco-nativo.md).
func CodexNoNativeNetwork(p NativePolicy) bool {
	return p.Offline && (machineOS(p.OS) != "windows" || p.Fallbacks.CodexOfflineNetwork)
}

// codexGitRoots are the folders of the mirror a commit in a linked worktree
// writes: the git folder of the worktree (index, HEAD) and the objects, refs
// and logs of the mirror. Codex 0.162.1 keeps them read-only in
// workspace-write and does not ask to escalate a git commit that fails
// there (conformance test, 2026-10-14), so they go in writable_roots; the
// other branches stay protected by the fixed rules of the dispatcher
// (docs/provedores/codex.md#verificar-na-implementação). A worktree whose
// .git is a folder (the main clone) needs nothing.
func codexGitRoots(worktree string) []string {
	raw, err := os.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil {
		return nil
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
	if !ok {
		return nil
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(worktree, gitdir)
	}
	common := filepath.Dir(filepath.Dir(gitdir)) // <mirror>/worktrees/<name>
	if c, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		common = strings.TrimSpace(string(c))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitdir, common)
		}
	}
	out := []string{filepath.Clean(gitdir)}
	for _, d := range []string{"objects", "refs", "logs"} {
		if st, err := os.Stat(filepath.Join(common, d)); err == nil && st.IsDir() {
			out = append(out, filepath.Clean(filepath.Join(common, d)))
		}
	}
	return out
}
