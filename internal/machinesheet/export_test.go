package machinesheet

// The parsers and the search of the PATH, for the tests of the package
// machinesheet_test.
var (
	ParseOSRelease     = parseOSRelease
	ParseMemTotal      = parseMemTotal
	ParseLogindSession = parseLogindSession
	WindowsVersion     = windowsVersion
	IsVirtualGPU       = isVirtualGPU
	PCIName            = pciName
	LookPath           = lookPath
	FirstVersion       = firstVersion
)

// NewToolsProbeAt is NewToolsProbe over a PATH given.
func NewToolsProbeAt(r Runner, path string, names ...string) ToolsProbe {
	return toolsProbe{runner: r, names: names, path: &path}
}
