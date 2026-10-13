package machinesheet

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// SystemInfo is what the operating system says about the machine.
type SystemInfo struct {
	// Host is the name of the computer.
	Host string
	// OS is OSLinux or OSWindows; OSVersion is "Ubuntu 26.04" or
	// "Windows 11 24H2".
	OS, OSVersion string
	// Arch is the architecture in Go's names ("amd64").
	Arch string
}

// SystemProbe reads the machine itself. The real one is per operating system
// (NewSystemProbe); the tests use a fake.
type SystemProbe interface {
	Info(ctx context.Context) (SystemInfo, error)
	// Resources reads the cores, the memory, the free disk and the GPUs.
	Resources(ctx context.Context) (Resources, error)
	// GUI says whether a user session with a desktop exists.
	GUI(ctx context.Context) (bool, error)
	// IPs are the addresses of the machine on the local network.
	IPs(ctx context.Context) ([]string, error)
}

// ProviderProbe reads the provider CLIs installed and the state of each
// account profile. Providers belong to milestone 3 and the profiles to
// milestone 4: they implement this interface, with the result of the
// checklist and of the conformance (docs/provedores/conformidade.md).
type ProviderProbe interface {
	Providers(ctx context.Context) ([]Provider, error)
}

// OperatorProbe reads the state of the Operator of the PC from the process of
// the session (milestone of the Operator).
type OperatorProbe interface {
	Operator(ctx context.Context) (Operator, error)
}

// BrowserProbe finds the browser the browser MCPs use; nil means none.
type BrowserProbe interface {
	Browser(ctx context.Context) (*Browser, error)
}

// ToolsProbe reads the tools with their versions (git, ffmpeg).
type ToolsProbe interface {
	Tools(ctx context.Context) (map[string]string, error)
}

// TranscriptionProbe says whether the whisper-server was verified
// (docs/telegram/anexos-e-transcricao.md).
type TranscriptionProbe interface {
	Transcription(ctx context.Context) (bool, error)
}

// Sources are the probes of a machine. System is required; a nil probe leaves
// its part of the sheet empty (no providers, no browser, no Operator).
type Sources struct {
	System        SystemProbe
	Providers     ProviderProbe
	Operator      OperatorProbe
	Browser       BrowserProbe
	Tools         ToolsProbe
	Transcription TranscriptionProbe
}

// Builder composes the sheet of this machine.
type Builder struct {
	src      Sources
	machine  string
	instance string
}

// ErrNoSystem: a Builder needs a SystemProbe.
var ErrNoSystem = errors.New("machinesheet: a system probe is required")

// NewBuilder returns the builder of the machine machineID, which runs the
// instance instance ("" for the default one).
func NewBuilder(src Sources, machineID, instance string) (*Builder, error) {
	if src.System == nil {
		return nil, ErrNoSystem
	}
	return &Builder{src: src, machine: machineID, instance: instance}, nil
}

// Build reads every probe. A probe that fails does not lose its part: the
// part keeps the value of prev (so a login monitor that fails for a moment
// does not tell the Guild the login was lost), and the failure comes back
// joined in err together with a complete sheet. Without a usable system
// probe the sheet cannot be valid and Build returns the zero Sheet.
func (b *Builder) Build(ctx context.Context, prev Sheet) (Sheet, error) {
	var errs []error
	keep := func(part string, err error) bool {
		if err != nil {
			errs = append(errs, fmt.Errorf("machinesheet: %s: %w", part, err))
			return true
		}
		return false
	}
	s := prev.Clone()
	s.MachineID, s.Instance = b.machine, b.instance

	info, err := b.src.System.Info(ctx)
	if keep("system", err) && s.OS == "" {
		return Sheet{}, errors.Join(errs...)
	}
	if err == nil {
		s.Host, s.OS, s.OSVersion, s.Arch = info.Host, info.OS, info.OSVersion, info.Arch
	}
	if res, err := b.src.System.Resources(ctx); !keep("resources", err) {
		s.Resources = res
	}
	if gui, err := b.src.System.GUI(ctx); !keep("gui", err) {
		s.GUI = gui
	}
	if ips, err := b.src.System.IPs(ctx); !keep("ips", err) {
		s.IPs = ips
	}
	if p := b.src.Providers; p != nil {
		if v, err := p.Providers(ctx); !keep("providers", err) {
			s.Providers = v
		}
	}
	if p := b.src.Operator; p != nil {
		if v, err := p.Operator(ctx); !keep("operator", err) {
			s.Operador = v
		}
	}
	if p := b.src.Browser; p != nil {
		if v, err := p.Browser(ctx); !keep("browser", err) {
			s.Browser = v
		}
	}
	if p := b.src.Tools; p != nil {
		if v, err := p.Tools(ctx); !keep("tools", err) {
			s.Tools = v
		}
	}
	if p := b.src.Transcription; p != nil {
		if v, err := p.Transcription(ctx); !keep("transcription", err) {
			s.Transcricao = v
		}
	}
	s = s.normalized()
	if !slices.Contains([]string{OSLinux, OSWindows}, s.OS) {
		errs = append(errs, fmt.Errorf("%w: os %q", ErrInvalid, s.OS))
	}
	return s, errors.Join(errs...)
}
