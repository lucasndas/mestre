package main

import (
	"os"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/winsvc"
)

// allowEngine lets the engine of the instance open this process to identify
// it as the caller (winsvc.AllowEngineToIdentify). The instance comes from
// the socket of MESTRE_ENDPOINT, or is the principal one; an error is not
// reported: the engine refuses a caller it cannot identify, as it did before.
func allowEngine() {
	instance := localchan.DefaultInstance
	if sock, err := localchan.EndpointFromEnv(os.Getenv); err == nil {
		if inst, ok := localchan.InstanceOfSocket(sock); ok {
			instance = inst
		}
	}
	_ = winsvc.AllowEngineToIdentify(instance)
}
