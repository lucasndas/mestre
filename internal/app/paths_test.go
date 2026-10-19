package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/pkg/clock"
)

type recordingAccess struct {
	mu    sync.Mutex
	paths []string
}

func (r *recordingAccess) Apply(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
	return nil
}

// The socket of the local channel of an installed instance gets the access
// of docs/arquitetura/canal-local.md#endpoint (0660 and the group of the
// agents of the instance on Linux, the ACL on Windows); only an instance of `--dir`
// leaves it as the system created it. The engine applies the access of its
// paths when it opens the channel.
// CA-LOCAL-15
func TestTheSocketOfAnInstalledInstanceGetsTheAccessOfTheInstaller(t *testing.T) {
	t.Parallel()
	for _, instance := range []string{"principal", "x"} {
		p, err := DefaultPaths(instance)
		if err != nil {
			t.Fatal(err)
		}
		// the access of the instance: another instance has the account of its own
		if !reflect.DeepEqual(p.CLIAccess, localchan.DefaultAccess(instance)) {
			t.Fatalf("installed instance %s: access %#v, want %#v", instance, p.CLIAccess, localchan.DefaultAccess(instance))
		}
	}
	main, _ := DefaultPaths("principal")
	other, _ := DefaultPaths("x")
	if reflect.DeepEqual(main.CLIAccess, other.CLIAccess) {
		t.Fatalf("the instance x has the access of the principal one: %#v", other.CLIAccess)
	}
	if _, ok := DirPaths(t.TempDir()).cliAccess().(localchan.NoAccess); !ok {
		t.Fatal("an instance of --dir changes the access of its socket")
	}

	dir, err := os.MkdirTemp("", "mp") // short: the path of a socket is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	paths := DirPaths(dir)
	rec := &recordingAccess{}
	paths.CLIAccess = rec
	e, err := New(Config{Instance: "a", Name: "a", Hostname: "a", EnginePort: 7443, NoDiscovery: true}, paths, Deps{Clock: clock.Real{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	if err := e.serveCLI(ctx, &wg); err != nil {
		t.Fatal(err)
	}
	cancel()
	wg.Wait()
	if len(rec.paths) != 1 || rec.paths[0] != paths.CLISocket {
		t.Fatalf("the access was applied to %v; want %s", rec.paths, paths.CLISocket)
	}
}

// With no %ProgramData% the folders of an installed instance on Windows are
// the ones of the socket (C:\ProgramData), never folders relative to where
// the engine started. Not parallel: it changes the environment.
func TestTheFoldersOfAnInstalledInstanceAreAbsoluteWithoutProgramData(t *testing.T) { //nolint:paralleltest // it changes the environment of the process
	if runtime.GOOS == "windows" {
		t.Setenv("ProgramData", "")
	}
	p, err := DefaultPaths("principal")
	if err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"config": p.Config, "banco": p.Banco, "maquina": p.Maquina, "agentes": p.Agentes,
		"socket": p.CLISocket, "fila": p.HookQueue} {
		if !filepath.IsAbs(dir) {
			t.Errorf("%s = %q, not absolute", name, dir)
		}
	}
}
