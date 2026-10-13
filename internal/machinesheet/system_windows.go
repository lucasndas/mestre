//go:build windows

package machinesheet

import (
	"context"
	"fmt"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func osVersion() (string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	product, _, _ := k.GetStringValue("ProductName")
	display, _, err := k.GetStringValue("DisplayVersion")
	if err != nil {
		display, _, _ = k.GetStringValue("ReleaseId") // before 20H2
	}
	build, _, _ := k.GetStringValue("CurrentBuildNumber")
	return windowsVersion(product, display, build), nil
}

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

func memTotal() (uint64, error) {
	st := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if r, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st))); r == 0 {
		return 0, fmt.Errorf("GlobalMemoryStatusEx: %w", err)
	}
	return st.TotalPhys, nil
}

func diskFree(dir string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var avail, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &total, &free); err != nil {
		return 0, fmt.Errorf("GetDiskFreeSpaceEx %s: %w", dir, err)
	}
	return avail, nil
}

// displayClass is the device setup class of the display adapters; each
// adapter is a numbered subkey (0000, 0001, ...) with the name the driver
// gives and, for most, the video memory.
const displayClass = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`

func gpus() ([]GPU, error) {
	class, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClass, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil, err
	}
	defer class.Close()
	names, err := class.ReadSubKeyNames(-1)
	if err != nil {
		return nil, err
	}
	out := []GPU{}
	for _, n := range names {
		if _, err := strconv.Atoi(n); err != nil {
			continue // "Properties"
		}
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClass+`\`+n, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		model, _, err := k.GetStringValue("DriverDesc")
		if err == nil && model != "" && !isVirtualGPU(model) {
			g := GPU{Model: model}
			if v, _, err := k.GetIntegerValue("HardwareInformation.qwMemorySize"); err == nil {
				g.MemBytes = v
			}
			out = append(out, g)
		}
		k.Close()
	}
	return out, nil
}

var (
	wtsapi32                 = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSQuerySessionInfoW = wtsapi32.NewProc("WTSQuerySessionInformationW")
	wtsUserName              = 5 // WTS_INFO_CLASS WTSUserName
	wtsCurrentServer         = windows.Handle(0)
)

// desktopSession is true when a user is signed in on a session with a
// desktop: an active or disconnected session other than session 0 that has a
// user name (the sign-in screen has none).
func desktopSession() (bool, error) {
	var sessions *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(wtsCurrentServer, 0, 1, &sessions, &count); err != nil {
		return false, fmt.Errorf("WTSEnumerateSessions: %w", err)
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(sessions)))
	for _, s := range unsafe.Slice(sessions, count) {
		if s.SessionID == 0 || (s.State != windows.WTSActive && s.State != windows.WTSDisconnected) {
			continue
		}
		if sessionUser(s.SessionID) != "" {
			return true, nil
		}
	}
	return false, nil
}

func sessionUser(id uint32) string {
	var buf *uint16
	var n uint32
	r, _, _ := procWTSQuerySessionInfoW.Call(uintptr(wtsCurrentServer), uintptr(id), uintptr(wtsUserName),
		uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&n)))
	if r == 0 || buf == nil {
		return ""
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buf)))
	return windows.UTF16PtrToString(buf)
}

// browsers are the browsers that can serve the browser MCPs, in order: the
// program that is registered (App Paths) and where the browser keeps its
// version.
var browsers = []struct{ name, exe, versionKey string }{
	{"Chrome", "chrome.exe", `Software\Google\Chrome\BLBeacon`},
	{"Edge", "msedge.exe", `Software\Microsoft\Edge\BLBeacon`},
}

func detectBrowser(context.Context, Runner) (*Browser, error) {
	for _, b := range browsers {
		if !registeredProgram(b.exe) {
			continue
		}
		out := &Browser{Name: b.name}
		if k, err := registry.OpenKey(registry.CURRENT_USER, b.versionKey, registry.QUERY_VALUE); err == nil {
			out.Version, _, _ = k.GetStringValue("version")
			k.Close()
		}
		return out, nil
	}
	return nil, nil
}

// registeredProgram says whether the program is registered under App Paths,
// for the machine or for the user.
func registeredProgram(exe string) bool {
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		k, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+exe, registry.QUERY_VALUE)
		if err == nil {
			k.Close()
			return true
		}
	}
	return false
}
