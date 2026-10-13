//go:build linux

package machinesheet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func osVersion() (string, error) {
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if v := parseOSRelease(string(b)); v != "" {
			return v, nil
		}
	}
	return "Linux", nil
}

func memTotal() (uint64, error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	return parseMemTotal(string(b))
}

func diskFree(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", dir, err)
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// gpus lists the display controllers on the PCI bus (class 03xx), leaving out
// virtual adapters. The name comes from pci.ids when the system has it; the
// memory is known for the amdgpu driver.
func gpus() ([]GPU, error) {
	devs, err := filepath.Glob("/sys/bus/pci/devices/*")
	if err != nil {
		return nil, err
	}
	var ids string
	var out []GPU
	for _, d := range devs {
		if class := readTrim(filepath.Join(d, "class")); !strings.HasPrefix(class, "0x03") {
			continue
		}
		vendor := strings.TrimPrefix(readTrim(filepath.Join(d, "vendor")), "0x")
		device := strings.TrimPrefix(readTrim(filepath.Join(d, "device")), "0x")
		if virtualVendors[vendor] {
			continue
		}
		if ids == "" {
			ids = readPCIIDs()
		}
		name, ok := pciName(ids, vendor, device)
		switch {
		case ok:
		case vendorShort[vendor] != "":
			name = vendorShort[vendor] + " " + device
		default:
			name = vendor + ":" + device
		}
		g := GPU{Model: name}
		if v, err := strconv.ParseUint(readTrim(filepath.Join(d, "mem_info_vram_total")), 10, 64); err == nil {
			g.MemBytes = v
		}
		out = append(out, g)
	}
	return out, nil
}

func readPCIIDs() string {
	for _, p := range []string{"/usr/share/hwdata/pci.ids", "/usr/share/misc/pci.ids", "/usr/share/pci.ids"} {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
	}
	return " "
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// desktopSession is true when a user has a graphical session. The service
// runs as its own user, in a private /tmp and with no access to the runtime
// folders of other users (/run/user/<uid> is 0700), so neither the X11 socket
// nor the Wayland one can be counted on to be visible: the sessions that
// systemd-logind keeps, which everyone can read, come first; the sockets are
// for a machine where the service sees them.
func desktopSession() (bool, error) {
	sessions, _ := filepath.Glob("/run/systemd/sessions/*")
	for _, p := range sessions {
		if strings.HasSuffix(p, ".ref") {
			continue
		}
		if b, err := os.ReadFile(p); err == nil && parseLogindSession(string(b)) {
			return true, nil
		}
	}
	if x, _ := filepath.Glob("/tmp/.X11-unix/X*"); len(x) > 0 {
		return true, nil
	}
	w, _ := filepath.Glob("/run/user/*/wayland-[0-9]*")
	for _, p := range w {
		if !strings.HasSuffix(p, ".lock") {
			return true, nil
		}
	}
	return false, nil
}

// browsers are the programs that can serve the browser MCPs, in order.
var browsers = []struct{ exe, name string }{
	{"google-chrome-stable", "Chrome"}, {"google-chrome", "Chrome"}, {"chromium", "Chromium"}, {"chromium-browser", "Chromium"},
}

func detectBrowser(ctx context.Context, r Runner) (*Browser, error) {
	path := os.Getenv("PATH") + string(os.PathListSeparator) + "/opt/google/chrome"
	for _, b := range browsers {
		exe := lookPath(b.exe, path, OSLinux, "")
		if exe == "" {
			continue
		}
		out := &Browser{Name: b.name}
		if r != nil {
			text, err := r.Run(ctx, exe, "--version")
			if err != nil {
				return nil, fmt.Errorf("%s --version: %w", b.exe, err)
			}
			out.Version = firstVersion(text)
		}
		return out, nil
	}
	return nil, nil
}
