package providers

// The operations of docs/provedores/adaptador.md#contrato that the Codex and
// Antigravity adapters do not have yet. Each stub answers with
// ErrNotImplemented; the change that implements an operation for a provider
// deletes its stub here.

func (c *codex) DeliverMestreLayer(LayerRequest) (Layer, error) {
	return Layer{}, notImplemented(Codex, "DeliverMestreLayer")
}

func (c *codex) HookFiles(HookFilesRequest) ([]ManagedFile, error) {
	return nil, notImplemented(Codex, "HookFiles")
}

func (c *codex) PreTrust(PreTrustRequest) (Trust, error) {
	return Trust{}, notImplemented(Codex, "PreTrust")
}

func (c *codex) ReadUsage(UsageRequest) (Usage, error) {
	return Usage{}, notImplemented(Codex, "ReadUsage")
}

func (a *antigravity) DeliverMestreLayer(LayerRequest) (Layer, error) {
	return Layer{}, notImplemented(Antigravity, "DeliverMestreLayer")
}

func (a *antigravity) HookFiles(HookFilesRequest) ([]ManagedFile, error) {
	return nil, notImplemented(Antigravity, "HookFiles")
}

func (a *antigravity) PreTrust(PreTrustRequest) (Trust, error) {
	return Trust{}, notImplemented(Antigravity, "PreTrust")
}

func (a *antigravity) ReadUsage(UsageRequest) (Usage, error) {
	return Usage{}, notImplemented(Antigravity, "ReadUsage")
}
