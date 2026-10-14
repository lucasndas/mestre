package classes

import (
	"net"
	"os"
	"strings"
	"sync"
)

func isDriveLetter(c byte) bool { return (c|0x20) >= 'a' && (c|0x20) <= 'z' }

// localNames are the names and addresses of this machine, lower case: its
// host name (with and without the domain) and the addresses of its
// interfaces.
var localNames = sync.OnceValue(func() map[string]bool {
	names := map[string]bool{}
	if h, err := os.Hostname(); err == nil && h != "" {
		h = strings.ToLower(h)
		names[h] = true
		if first, _, ok := strings.Cut(h, "."); ok {
			names[first] = true
		}
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ip, _, err := net.ParseCIDR(a.String()); err == nil {
				names[ip.String()] = true
			}
		}
	}
	return names
})

// localHost reports whether the host of a UNC path is this machine: a name
// of the loopback (localhost, 127.x.x.x, ::1 and its UNC spelling), its
// host name, with or without the domain, or an address of its interfaces.
func localHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasPrefix(h, "localhost.") || h == "::1" || h == "[::1]" || h == "0--1.ipv6-literal.net" {
		return true
	}
	if ip := net.ParseIP(strings.Trim(h, "[]")); ip != nil && ip.IsLoopback() {
		return true
	}
	names := localNames()
	if names[h] {
		return true
	}
	first, _, ok := strings.Cut(h, ".")
	return ok && names[first] && net.ParseIP(h) == nil
}

// windowsShare is the reason the fixed rules deny a UNC key on Windows, or
// "": a device path (\\?\GLOBALROOT\…, \\.\pipe\…, the host "?" of
// windowsKey), a share of this machine (its drives are mapped to C:\…; any
// other share is a folder the motor does not see) and a hidden or
// administrative share of any machine (C$, ADMIN$, IPC$).
func windowsShare(k string) string {
	rest, ok := strings.CutPrefix(k, "//")
	if !ok {
		return ""
	}
	host, after, _ := strings.Cut(rest, "/")
	share, _, _ := strings.Cut(after, "/")
	switch {
	case host == "?" || host == "." || host == "":
		return "caminho de dispositivo do Windows fora de uma unidade (\\\\?\\, \\\\.\\)"
	case localHost(host):
		return "compartilhamento de rede da própria máquina"
	case strings.HasSuffix(share, "$"):
		return "compartilhamento administrativo ou oculto (C$, ADMIN$, IPC$)"
	}
	return ""
}
