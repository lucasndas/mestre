package hook

import (
	"path/filepath"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/localchan"
)

// Machine are the folders of an instance that the dispatcher reads (the
// snapshots of agentes/) and writes (the hook queue), and the folders of the
// Mestre the fixed rules close.
type Machine struct {
	Instance string
	// Agents is agentes/: policy.json and env.json of each character.
	Agents string
	// Queue is the hook queue, where the dispatcher only creates files.
	Queue  string
	Layout classes.Layout
}

// MachineOf is the Machine of an installed instance
// (docs/arquitetura/persistencia.md#arquivos). programData is %ProgramData%
// on Windows.
func MachineOf(goos, programData, instance string) Machine {
	l := classes.DefaultLayout(goos, programData, instance)
	return Machine{Instance: instance, Agents: l.Agents, Queue: QueueDir(goos, programData, instance), Layout: l}
}

// QueueDir is the hook queue of an installed instance:
// /var/spool/mestre/<inst>/hook/ on Linux,
// %ProgramData%\Mestre\<inst>\maquina\fila-hook\ on Windows.
func QueueDir(goos, programData, instance string) string {
	if goos == "windows" {
		return filepath.Join(programData, "Mestre", instance, "maquina", "fila-hook")
	}
	return filepath.Join("/var/spool/mestre", instance, "hook")
}

// absolute reports whether p is an absolute path of goos: on Windows with a
// drive (C:\…) or a UNC share (\\host\…), whatever the system the code runs
// on.
func absolute(goos, p string) bool {
	if goos != "windows" {
		return strings.HasPrefix(p, "/")
	}
	if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		c := p[0] | 0x20
		return c >= 'a' && c <= 'z'
	}
	return strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//")
}

// InstanceOf is the instance whose socket of the local channel is socket,
// for the dispatcher that knows only MESTRE_ENDPOINT. An endpoint that is
// not the socket of an installed instance (`mestre run --dir`) names none:
// the dispatcher then has no snapshot and no queue, and with the engine
// down only the fixed rules and reading hold.
func InstanceOf(goos, socket string) (string, bool) {
	dir := filepath.Dir(socket)
	if goos == "windows" {
		dir = filepath.Dir(dir) // <inst>\run\cli.sock
	}
	inst := filepath.Base(dir)
	want, err := localchan.SocketPath(inst)
	if err != nil {
		return "", false
	}
	if goos == "windows" {
		return inst, strings.EqualFold(want, socket)
	}
	return inst, want == socket
}
