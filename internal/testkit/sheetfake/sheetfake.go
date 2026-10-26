// Package sheetfake is the double of the machine for the machine sheet
// (docs/qualidade/dubles.md): one fake that implements every probe of
// internal/machinesheet, so a test says what the machine looks like and
// changes it later (a login that falls, a disk that fills) without reading
// the real system.
package sheetfake

import (
	"context"
	"strings"
	"sync"

	"github.com/mestre-ia/mestre/internal/machinesheet"
)

// The parts a test can make fail.
const (
	PartSystem        = "system"
	PartResources     = "resources"
	PartGUI           = "gui"
	PartIPs           = "ips"
	PartProviders     = "providers"
	PartOperator      = "operator"
	PartBrowser       = "browser"
	PartTools         = "tools"
	PartTranscription = "transcription"
)

// Machine is a fake machine.
type Machine struct {
	mu            sync.Mutex
	info          machinesheet.SystemInfo
	res           machinesheet.Resources
	gui           bool
	ips           []string
	providers     []machinesheet.Provider
	operator      machinesheet.Operator
	browser       *machinesheet.Browser
	tools         map[string]string
	transcription bool
	failing       map[string]error
	reads         map[string]int
}

var (
	_ machinesheet.SystemProbe        = (*Machine)(nil)
	_ machinesheet.ProviderProbe      = (*Machine)(nil)
	_ machinesheet.OperatorProbe      = (*Machine)(nil)
	_ machinesheet.BrowserProbe       = (*Machine)(nil)
	_ machinesheet.ToolsProbe         = (*Machine)(nil)
	_ machinesheet.TranscriptionProbe = (*Machine)(nil)
)

const gib = 1 << 30

// Linux is an Ubuntu machine with a desktop-less session, 16 GiB, 8 cores and
// a GPU, with Claude Code logged in and conformant.
func Linux(host string) *Machine {
	return &Machine{
		info: machinesheet.SystemInfo{Host: host, OS: machinesheet.OSLinux, OSVersion: "Ubuntu 26.04", Arch: "amd64"},
		res: machinesheet.Resources{CPUCores: 8, MemBytes: 16 * gib, DiskFreeBytes: 200 * gib,
			GPUs: []machinesheet.GPU{{Model: "NVIDIA RTX 4070", MemBytes: 12 * gib}}},
		ips: []string{"192.168.1.10"},
		providers: []machinesheet.Provider{{Provider: machinesheet.ProviderClaudeCode, Profiles: []machinesheet.Profile{
			{Name: "claude-trabalho", Version: "2.1.0", LoggedIn: true, Conformant: true, Default: true}}}},
		tools:   map[string]string{"git": "2.50.0"},
		failing: map[string]error{}, reads: map[string]int{},
	}
}

// Windows is a Windows 11 machine with a desktop, 32 GiB, 12 cores and no GPU.
func Windows(host string) *Machine {
	return &Machine{
		info: machinesheet.SystemInfo{Host: host, OS: machinesheet.OSWindows, OSVersion: "Windows 11 24H2", Arch: "amd64"},
		res:  machinesheet.Resources{CPUCores: 12, MemBytes: 32 * gib, DiskFreeBytes: 500 * gib, GPUs: []machinesheet.GPU{}},
		gui:  true,
		ips:  []string{"192.168.1.20"},
		providers: []machinesheet.Provider{{Provider: machinesheet.ProviderCodex, Profiles: []machinesheet.Profile{
			{Name: "codex-pessoal", Version: "0.9.0", LoggedIn: true, Conformant: true, Default: true}}}},
		browser: &machinesheet.Browser{Name: "Chrome", Version: "126.0.1"},
		tools:   map[string]string{"git": "2.50.0", "ffmpeg": "7.1"},
		failing: map[string]error{}, reads: map[string]int{},
	}
}

// Sources are the probes of the machine.
func (m *Machine) Sources() machinesheet.Sources {
	return machinesheet.Sources{System: m, Providers: m, Operator: m, Browser: m, Tools: m, Transcription: m}
}

// With changes the machine under the lock.
func (m *Machine) With(f func(m *Machine)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f(m)
}

// SetInfo, SetResources and the others replace one part.
func (m *Machine) SetInfo(v machinesheet.SystemInfo)     { m.With(func(m *Machine) { m.info = v }) }
func (m *Machine) SetResources(v machinesheet.Resources) { m.With(func(m *Machine) { m.res = v }) }
func (m *Machine) SetGUI(v bool)                         { m.With(func(m *Machine) { m.gui = v }) }
func (m *Machine) SetIPs(v ...string)                    { m.With(func(m *Machine) { m.ips = v }) }
func (m *Machine) SetProviders(v ...machinesheet.Provider) {
	m.With(func(m *Machine) { m.providers = v })
}
func (m *Machine) SetOperator(v machinesheet.Operator) { m.With(func(m *Machine) { m.operator = v }) }
func (m *Machine) SetBrowser(v *machinesheet.Browser)  { m.With(func(m *Machine) { m.browser = v }) }
func (m *Machine) SetTools(v map[string]string)        { m.With(func(m *Machine) { m.tools = v }) }
func (m *Machine) SetTranscription(v bool)             { m.With(func(m *Machine) { m.transcription = v }) }

// Fail makes a part fail with err (nil makes it work again).
func (m *Machine) Fail(part string, err error) {
	m.With(func(m *Machine) {
		if err == nil {
			delete(m.failing, part)
			return
		}
		m.failing[part] = err
	})
}

// Reads is how many times a part was read.
func (m *Machine) Reads(part string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reads[part]
}

// read counts the read and returns the failure of the part, if any.
func (m *Machine) read(part string) error {
	m.reads[part]++
	return m.failing[part]
}

// Info implements machinesheet.SystemProbe.
func (m *Machine) Info(context.Context) (machinesheet.SystemInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.info, m.read(PartSystem)
}

// Resources implements machinesheet.SystemProbe.
func (m *Machine) Resources(context.Context) (machinesheet.Resources, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.res
	r.GPUs = append([]machinesheet.GPU(nil), r.GPUs...)
	return r, m.read(PartResources)
}

// GUI implements machinesheet.SystemProbe.
func (m *Machine) GUI(context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gui, m.read(PartGUI)
}

// IPs implements machinesheet.SystemProbe.
func (m *Machine) IPs(context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.ips...), m.read(PartIPs)
}

// Providers implements machinesheet.ProviderProbe.
func (m *Machine) Providers(context.Context) ([]machinesheet.Provider, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]machinesheet.Provider, len(m.providers))
	for i, p := range m.providers {
		out[i] = machinesheet.Provider{Provider: p.Provider, Profiles: append([]machinesheet.Profile(nil), p.Profiles...)}
	}
	return out, m.read(PartProviders)
}

// Operator implements machinesheet.OperatorProbe.
func (m *Machine) Operator(context.Context) (machinesheet.Operator, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.operator, m.read(PartOperator)
}

// Browser implements machinesheet.BrowserProbe.
func (m *Machine) Browser(context.Context) (*machinesheet.Browser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.browser == nil {
		return nil, m.read(PartBrowser)
	}
	b := *m.browser
	return &b, m.read(PartBrowser)
}

// Tools implements machinesheet.ToolsProbe.
func (m *Machine) Tools(context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(m.tools))
	for k, v := range m.tools {
		out[k] = v
	}
	return out, m.read(PartTools)
}

// Transcription implements machinesheet.TranscriptionProbe.
func (m *Machine) Transcription(context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.transcription, m.read(PartTranscription)
}

// Runner is a fake machinesheet.Runner: it answers `--version` from a table.
type Runner struct {
	mu   sync.Mutex
	out  map[string]string
	runs []string
}

var _ machinesheet.Runner = (*Runner)(nil)

// NewRunner answers with out[exe] for the program exe (the whole path).
func NewRunner(out map[string]string) *Runner { return &Runner{out: out} }

// Run implements machinesheet.Runner.
func (r *Runner) Run(_ context.Context, exe string, _ ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs = append(r.runs, exe)
	for k, v := range r.out {
		if strings.EqualFold(k, exe) { // Windows paths do not care about case
			return v, nil
		}
	}
	return "", nil
}

// Runs lists the programs run.
func (r *Runner) Runs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.runs...)
}
