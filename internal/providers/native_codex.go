package providers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Native translates the policy into the start flags of Codex: the sandbox
// (always workspace-write, with no rule per path, and .git out of its write
// reach by the sandbox itself but for the git folders of the engine's record
// of the worktree), the network
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
	// Never -s read-only, also for a class without write: read-only has no
	// network, and the `mestre` CLI would not reach the local channel. The
	// class without write runs in workspace-write with no extra writable
	// root, and the dispatcher denies its writes (CodexNoNativeWrite).
	network := "true"
	if p.Offline && !CodexNoNativeNetwork(p) {
		network = "false"
	}
	args := []string{"-s", "workspace-write", "-c", "sandbox_workspace_write.network_access=" + network}
	roots, err := codexGitRoots(p)
	if err != nil {
		return NativeControls{}, err
	}
	// The list is always given, empty included: a -c is above every
	// config.toml, so a .codex/config.toml of the repository (which the
	// agent writes) cannot add a root such as "/".
	vals := []string{}
	if !p.NoWrite {
		for _, r := range roots {
			v, err := tomlString(r)
			if err != nil {
				return NativeControls{}, err
			}
			vals = append(vals, v)
		}
	}
	args = append(args, "-c", "sandbox_workspace_write.writable_roots=["+strings.Join(vals, ",")+"]")
	if !p.Offline && p.AnyDomain {
		args = append(args, "--search")
	}
	return NativeControls{Args: args}, nil
}

// CodexNoNativeWrite reports whether a Codex character of this policy runs
// without the native write barrier its class asks for: a class that writes
// nothing runs in workspace-write like the others (read-only would cut the
// local channel), so only the dispatcher, which fails open in Codex, stops
// its writes. The panel shows it on the sheet and the class check warns, as
// with the network (docs/classes/reforco-nativo.md).
func CodexNoNativeWrite(p NativePolicy) bool { return p.NoWrite }

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
// (docs/provedores/codex.md#verificar-na-implementação).
//
// They come from the engine's record of the worktree (GitDir, GitCommon),
// never from the .git file or the commondir of the worktree: the agent
// writes both, and could point them at any folder it wants opened. The git
// folder must be a folder of <mirror>/worktrees/, and no root may be a link.
// No record (the main clone, whose .git is a folder) opens nothing.
func codexGitRoots(p NativePolicy) ([]string, error) {
	if p.GitDir == "" && p.GitCommon == "" {
		return nil, nil
	}
	gitdir, common := filepath.Clean(p.GitDir), filepath.Clean(p.GitCommon)
	if !filepath.IsAbs(gitdir) || !filepath.IsAbs(common) || filepath.Dir(gitdir) != filepath.Join(common, "worktrees") {
		return nil, fmt.Errorf("native controls of %s: the git folder %q is not a worktree of the mirror %q", p.AgentID, p.GitDir, p.GitCommon)
	}
	out := []string{gitdir}
	if st, err := os.Lstat(gitdir); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("native controls of %s: the git folder %s of the worktree is not a folder", p.AgentID, gitdir)
	}
	for _, d := range []string{"objects", "refs", "logs"} {
		if st, err := os.Lstat(filepath.Join(common, d)); err == nil && st.IsDir() {
			out = append(out, filepath.Join(common, d))
		}
	}
	return out, nil
}
