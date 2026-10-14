package providers

// The operations of docs/provedores/adaptador.md#contrato that the Antigravity
// adapter does not have yet. Each stub answers with
// ErrNotImplemented; the change that implements an operation for a provider
// deletes its stub here.

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
