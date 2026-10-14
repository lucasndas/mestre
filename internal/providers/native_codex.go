package providers

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
