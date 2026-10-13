package machinesheet

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"slices"
)

// systemProbe is the real SystemProbe. What differs between the two systems
// is read by the functions of system_linux.go and system_windows.go
// (osVersion, memTotal, diskFree, gpus, desktopSession); the rest is shared.
type systemProbe struct {
	// dataDir is the folder whose free space the sheet reports: the data
	// folder of the machine.
	dataDir string
}

// NewSystemProbe returns the probe that reads this machine. dataDir is the
// folder of the data of the machine (docs/arquitetura/persistencia.md), whose
// volume gives the free disk; empty means the current folder.
func NewSystemProbe(dataDir string) SystemProbe { return systemProbe{dataDir: dataDir} }

// Info implements SystemProbe.
func (p systemProbe) Info(context.Context) (SystemInfo, error) {
	host, err := os.Hostname()
	if err != nil {
		return SystemInfo{}, fmt.Errorf("host name: %w", err)
	}
	version, err := osVersion()
	if err != nil {
		return SystemInfo{}, fmt.Errorf("system version: %w", err)
	}
	return SystemInfo{Host: host, OS: runtime.GOOS, OSVersion: version, Arch: runtime.GOARCH}, nil
}

// Resources implements SystemProbe.
func (p systemProbe) Resources(context.Context) (Resources, error) {
	res := Resources{CPUCores: runtime.NumCPU(), GPUs: []GPU{}}
	mem, err := memTotal()
	if err != nil {
		return Resources{}, fmt.Errorf("memory: %w", err)
	}
	res.MemBytes = mem
	dir := p.dataDir
	if dir == "" {
		dir = "."
	}
	if res.DiskFreeBytes, err = diskFree(dir); err != nil {
		return Resources{}, fmt.Errorf("disk: %w", err)
	}
	// A machine whose adapters cannot be read has none that the sheet knows:
	// the other numbers are still good.
	if g, err := gpus(); err == nil && g != nil {
		res.GPUs = g
	}
	return res, nil
}

// GUI implements SystemProbe.
func (p systemProbe) GUI(context.Context) (bool, error) { return desktopSession() }

// IPs implements SystemProbe.
func (p systemProbe) IPs(context.Context) ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("interfaces: %w", err)
	}
	var out []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				if ip := localIPv4(ipn.IP); ip != "" && !slices.Contains(out, ip) {
					out = append(out, ip)
				}
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// localIPv4 is the address of an interface that other machines of the local
// network can reach: an IPv4 one that is not link-local.
func localIPv4(ip net.IP) string {
	v4 := ip.To4()
	if v4 == nil || v4.IsLinkLocalUnicast() || v4.IsLoopback() || v4.IsUnspecified() {
		return ""
	}
	return v4.String()
}
