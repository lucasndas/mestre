package providers

// Native translates the policy into the start flags of Codex: the sandbox
// (read-only with no write, workspace-write otherwise, with no rule per
// path, and .git out of its write reach by the sandbox itself), the network
// of the sandbox, which is only on or off, and the hosted search only with
// internet and no list of domains (it does not go through a hook). The
// commands are contained by the sandbox; nothing else is native in Codex.
func (c *codex) Native(p NativePolicy) (NativeControls, error) {
	if err := p.check(); err != nil {
		return NativeControls{}, err
	}
	sandbox, network := "workspace-write", "true"
	if p.NoWrite {
		sandbox = "read-only"
	}
	if p.Offline {
		network = "false"
	}
	args := []string{"-s", sandbox, "-c", "sandbox_workspace_write.network_access=" + network}
	if !p.Offline && p.AnyDomain {
		args = append(args, "--search")
	}
	return NativeControls{Args: args}, nil
}
