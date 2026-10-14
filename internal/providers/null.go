package providers

import (
	"fmt"
	"regexp"
	"slices"
)

// null is the adapter of terminals without an agent (bash, powershell
// bases): no hooks, no Mestre layer, readiness only from the screen.
type null struct{ screens screenSet }

// NewNull returns the null adapter.
func NewNull() Adapter {
	// Without the base's ready pattern, a shell prompt ends in $, # or >.
	// No busy sign: a screen without the prompt is a running command.
	return &null{screens: screenSet{prompt: regexp.MustCompile(`[$#>]\s*$`)}}
}

func (n *null) Info() Info { return Info{ID: Null} }

// BuildLaunch is the base command as is; a shell has no session to resume.
func (n *null) BuildLaunch(req LaunchRequest) (Launch, error) {
	l, err := newLaunch(req)
	if err != nil {
		return Launch{}, err
	}
	l.Args = slices.Concat(req.Base.Args, req.ProviderArgs)
	return l, nil
}

// NormalizeHook: no hook calls the null adapter.
func (n *null) NormalizeHook(in HookInput) (Event, error) {
	return Event{}, fmt.Errorf("terminal without agent %q: %w", in.NativeEvent, ErrUnknownEvent)
}

func (n *null) Screens(in ScreenInput) ScreenResult { return n.screens.evaluate(in) }

// TypeMessage: no paste; each line typed and followed by \r.
func (n *null) TypeMessage(text string) ([]byte, error) { return linesAndSend(text, "\r") }

// DeliverMestreLayer: a terminal without an agent has no Mestre layer.
func (n *null) DeliverMestreLayer(LayerRequest) (Layer, error) { return Layer{}, nil }

// HookFiles: no hook ever arrives.
func (n *null) HookFiles(HookFilesRequest) ([]ManagedFile, error) { return nil, nil }

// PreTrust: nothing to trust.
func (n *null) PreTrust(PreTrustRequest) (Trust, error) { return Trust{}, nil }

// ReadUsage: a shell spends nothing the Mestre reads.
func (n *null) ReadUsage(UsageRequest) (Usage, error) { return Usage{}, ErrNoUsage }
