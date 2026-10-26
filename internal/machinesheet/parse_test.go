package machinesheet_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mestre-ia/mestre/internal/machinesheet"
	"github.com/mestre-ia/mestre/internal/testkit/sheetfake"
)

func TestTheVersionOfTheSystemIsReadFromTheTextTheSystemGives(t *testing.T) {
	t.Parallel()
	t.Run("os-release", func(t *testing.T) {
		t.Parallel()
		tests := map[string]string{
			"NAME=\"Ubuntu\"\nVERSION_ID=\"26.04\"\nID=ubuntu\n": "Ubuntu 26.04",
			"# comment\nNAME=Arch Linux\nID=arch\n":              "Arch Linux",
			"ID=alpine\nVERSION_ID=3.22.1\n":                     "alpine 3.22.1",
			"":                                                   "",
			"PRETTY_NAME=\"Debian GNU/Linux 13\"\nNAME=\"Debian\"\nVERSION_ID=\"13\"\n": "Debian 13",
		}
		for in, want := range tests {
			if got := machinesheet.ParseOSRelease(in); got != want {
				t.Errorf("ParseOSRelease(%q) = %q, want %q", in, got, want)
			}
		}
	})
	t.Run("meminfo", func(t *testing.T) {
		t.Parallel()
		got, err := machinesheet.ParseMemTotal("MemTotal:       16318412 kB\nMemFree:  1 kB\n")
		if err != nil || got != 16318412*1024 {
			t.Errorf("ParseMemTotal = %d, %v", got, err)
		}
		for _, bad := range []string{"", "MemTotal:\n", "MemTotal: lots kB\n", "MemFree: 1 kB\n"} {
			if _, err := machinesheet.ParseMemTotal(bad); err == nil {
				t.Errorf("ParseMemTotal(%q) did not fail", bad)
			}
		}
	})
	t.Run("windows", func(t *testing.T) {
		t.Parallel()
		tests := []struct{ product, display, build, want string }{
			{"Windows 10 Pro", "24H2", "26100", "Windows 11 24H2"}, // the product name still says 10
			{"Windows 10 Pro", "22H2", "19045", "Windows 10 22H2"},
			{"Windows Server 2025 Datacenter", "24H2", "26100", "Windows Server 2025 Datacenter 24H2"},
			{"", "", "", "Windows"},
			{"Windows 11 Home", "", "22631", "Windows 11"},
		}
		for _, tt := range tests {
			if got := machinesheet.WindowsVersion(tt.product, tt.display, tt.build); got != tt.want {
				t.Errorf("WindowsVersion(%q, %q, %q) = %q, want %q", tt.product, tt.display, tt.build, got, tt.want)
			}
		}
	})
}

func TestAVirtualAdapterIsNotAGPU(t *testing.T) {
	t.Parallel()
	for model, virtual := range map[string]bool{
		"Microsoft Basic Display Adapter": true, "Microsoft Hyper-V Video": true, "VMware SVGA 3D": true,
		"Red Hat QXL paravirtual graphic card": true, "NVIDIA GeForce RTX 4070": false, "AMD Radeon RX 7800 XT": false,
		"Intel(R) UHD Graphics 770": false,
	} {
		if got := machinesheet.IsVirtualGPU(model); got != virtual {
			t.Errorf("IsVirtualGPU(%q) = %v", model, got)
		}
	}
}

func TestAPCIDeviceIsNamedFromPciIds(t *testing.T) {
	t.Parallel()
	ids := "# list\n10de  NVIDIA Corporation\n\t2204  GA102 [GeForce RTX 3090]\n\t\t10de 1454  subsystem\n\t2684  AD102 [GeForce RTX 4090]\n1002  Advanced Micro Devices, Inc. [AMD/ATI]\n\t744c  Navi 31 [Radeon RX 7900 XT]\n"
	if got, ok := machinesheet.PCIName(ids, "10de", "2684"); !ok || got != "NVIDIA Corporation AD102 [GeForce RTX 4090]" {
		t.Errorf("PCIName = %q, %v", got, ok)
	}
	if got, ok := machinesheet.PCIName(ids, "1002", "744c"); !ok || got != "Advanced Micro Devices, Inc. [AMD/ATI] Navi 31 [Radeon RX 7900 XT]" {
		t.Errorf("PCIName = %q, %v", got, ok)
	}
	if _, ok := machinesheet.PCIName(ids, "10de", "ffff"); ok {
		t.Error("an unknown device was named")
	}
	if _, ok := machinesheet.PCIName(ids, "ffff", "0001"); ok {
		t.Error("an unknown vendor was named")
	}
	if _, ok := machinesheet.PCIName(" ", "10de", "2684"); ok {
		t.Error("an empty database named a device")
	}
}

func writeFile(t *testing.T, dir, name string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), mode); err != nil {
		t.Fatal(err)
	}
}

func TestProgramsAreFoundOnThePathWithoutStartingAnything(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, dir, "git", 0o755)
	writeFile(t, dir, "notexec", 0o644)
	writeFile(t, dir, "ffmpeg.EXE", 0o644)
	other := t.TempDir()
	path := other + string(os.PathListSeparator) + dir
	if runtime.GOOS != "windows" { // a file has no execute bit on Windows
		if got := machinesheet.LookPath("git", path, machinesheet.OSLinux, ""); got != filepath.Join(dir, "git") {
			t.Errorf("git = %q", got)
		}
		if got := machinesheet.LookPath("notexec", path, machinesheet.OSLinux, ""); got != "" {
			t.Errorf("a file without the execute bit was found: %q", got)
		}
	}
	if got := machinesheet.LookPath("missing", path, machinesheet.OSLinux, ""); got != "" {
		t.Errorf("missing = %q", got)
	}
	// Windows: the suffixes of PATHEXT, and a file with no suffix is no program
	if got := machinesheet.LookPath("ffmpeg", path, machinesheet.OSWindows, ".COM;.EXE"); filepath.Base(got) != "ffmpeg.EXE" {
		t.Errorf("ffmpeg = %q", got)
	}
	if got := machinesheet.LookPath("git", path, machinesheet.OSWindows, ""); got != "" {
		t.Errorf("a program with no suffix was found on Windows: %q", got)
	}
}

func TestTheVersionOfAToolIsTheFirstDottedNumberItPrints(t *testing.T) {
	t.Parallel()
	for out, want := range map[string]string{
		"git version 2.50.1.windows.1\n":   "2.50.1",
		"ffmpeg version 7.1.1-static Copy": "7.1.1",
		"Google Chrome 126.0.6478.126":     "126.0.6478.126",
		"nothing here":                     "",
	} {
		if got := machinesheet.FirstVersion(out); got != want {
			t.Errorf("FirstVersion(%q) = %q, want %q", out, got, want)
		}
	}
}

func TestTheToolsProbeListsOnlyWhatIsInstalledAndAsksTheRunnerForTheVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exe := "git"
	if filepath.Separator == '\\' {
		exe = "git.exe"
	}
	writeFile(t, dir, exe, 0o755)
	run := sheetfake.NewRunner(map[string]string{filepath.Join(dir, exe): "git version 2.50.1\n"})
	got, err := machinesheet.NewToolsProbeAt(run, dir, "git", "ffmpeg").Tools(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["git"] != "2.50.1" {
		t.Errorf("tools = %v", got)
	}
	if len(run.Runs()) != 1 {
		t.Errorf("runs = %v", run.Runs())
	}
	// without a runner the tool is listed, with no version
	got, err = machinesheet.NewToolsProbeAt(nil, dir, "git", "ffmpeg").Tools(t.Context())
	if err != nil || len(got) != 1 || got["git"] != "" {
		t.Errorf("tools without a runner = %v, %v", got, err)
	}
}
