package machinesheet

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// The parsers below are shared by the two operating systems so both are
// tested on both: each system's file only reads the raw text.

// parseOSRelease reads /etc/os-release into "Ubuntu 26.04": the NAME and the
// VERSION_ID. A rolling distribution without a version is just its name.
func parseOSRelease(text string) string {
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		kv[k] = strings.Trim(v, `"'`)
	}
	name := kv["NAME"]
	if name == "" {
		name = kv["ID"]
	}
	if name == "" {
		return ""
	}
	if v := kv["VERSION_ID"]; v != "" {
		return name + " " + v
	}
	return name
}

// parseLogindSession says whether a session file of systemd-logind
// (/run/systemd/sessions/<id>, readable by everyone) describes a user's
// session with a graphical desktop: class user, type x11, wayland or mir, and
// not closing. A console (tty) or an ssh login is not one.
func parseLogindSession(text string) bool {
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "="); ok {
			kv[k] = v
		}
	}
	switch kv["TYPE"] {
	case "x11", "wayland", "mir":
	default:
		return false
	}
	return kv["CLASS"] == "user" && kv["STATE"] != "closing"
}

// parseMemTotal reads the MemTotal line of /proc/meminfo, in kB, as bytes.
func parseMemTotal(meminfo string) (uint64, error) {
	for _, line := range strings.Split(meminfo, "\n") {
		rest, ok := strings.CutPrefix(line, "MemTotal:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			break
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("MemTotal %q: %w", fields[0], err)
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("no MemTotal in meminfo")
}

// windowsVersion is "Windows 11 24H2" from the registry values: the product
// name still says "Windows 10" on Windows 11, so the build number decides
// (22000 is the first build of Windows 11). Servers keep their product name.
func windowsVersion(productName, displayVersion, build string) string {
	name := productName
	if n, err := strconv.Atoi(build); err == nil && !strings.Contains(productName, "Server") {
		switch {
		case n >= 22000:
			name = "Windows 11"
		case strings.HasPrefix(productName, "Windows 10"):
			name = "Windows 10"
		}
	}
	if name == "" {
		name = "Windows"
	}
	if displayVersion != "" {
		return name + " " + displayVersion
	}
	return name
}

// virtualVendors are the PCI vendors of virtual display adapters (Hyper-V,
// VMware, QEMU and VirtIO, VirtualBox, Red Hat QXL): none is a GPU for the
// work of a character.
var virtualVendors = map[string]bool{"1414": true, "15ad": true, "1234": true, "1af4": true, "80ee": true, "1b36": true}

// virtualWords mark a virtual adapter by the name the driver gives.
var virtualWords = []string{"basic display", "basic render", "hyper-v", "remote display", "virtual", "vmware", "virtualbox", "qxl", "parallels", "citrix", "indirect display"}

// isVirtualGPU says whether the name of an adapter is a virtual one.
func isVirtualGPU(model string) bool {
	m := strings.ToLower(model)
	for _, w := range virtualWords {
		if strings.Contains(m, w) {
			return true
		}
	}
	return false
}

// pciName looks a vendor and a device up in the text of pci.ids and returns
// "Vendor Device"; ok is false when either is unknown. ids are four hex
// digits in lower case.
func pciName(idsText, vendor, device string) (string, bool) {
	var vname string
	inVendor := false
	sc := bufio.NewScanner(strings.NewReader(idsText))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] != '\t' {
			id, name, ok := strings.Cut(line, "  ")
			if !ok {
				inVendor = false
				continue
			}
			if inVendor {
				return vname, false // the vendor's block ended without the device
			}
			if id == vendor {
				inVendor, vname = true, name
			}
			continue
		}
		if !inVendor || strings.HasPrefix(line, "\t\t") {
			continue
		}
		id, name, ok := strings.Cut(strings.TrimPrefix(line, "\t"), "  ")
		if ok && id == device {
			return vname + " " + name, true
		}
	}
	return vname, false
}

// vendorShort names the vendors a GPU usually comes from when pci.ids is not
// installed.
var vendorShort = map[string]string{"10de": "NVIDIA", "1002": "AMD", "8086": "Intel"}
