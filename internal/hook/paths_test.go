package hook_test

import (
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/providers"
)

// pathFiles records the paths the dispatcher touches.
type pathFiles struct {
	mu    sync.Mutex
	paths []string
}

func (p *pathFiles) ReadFile(path string) ([]byte, error) {
	p.mu.Lock()
	p.paths = append(p.paths, path)
	p.mu.Unlock()
	return nil, fs.ErrNotExist
}

func (p *pathFiles) CreateNew(path string, _ []byte) error {
	p.mu.Lock()
	p.paths = append(p.paths, path)
	p.mu.Unlock()
	return nil
}

// The folders of the degraded mode come from the socket of the instance,
// never from the environment of the caller: with no %ProgramData% in it (an
// agent's shell can clear it) the snapshot and the queue are still the
// absolute folders of the instance on Windows, not folders relative to the
// worktree.
func TestTheDegradedFoldersComeFromTheSocketNotTheCallersEnvironment(t *testing.T) {
	t.Parallel()
	sock, err := localchan.SocketPath("principal")
	if err != nil {
		t.Fatal(err)
	}
	files := &pathFiles{}
	env := map[string]string{localchan.EnvAgentID: agent, localchan.EnvEndpoint: "unix://" + filepath.ToSlash(sock), "ProgramData": ""}
	call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: claudeShell("npm test"), env: env, dial: down, files: files}.run(t.Context())
	want := classes.DefaultLayout(runtime.GOOS, "", "principal").Agents
	if runtime.GOOS == "windows" {
		root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(sock))))
		want = classes.DefaultLayout("windows", root, "principal").Agents
	}
	if len(files.paths) == 0 {
		t.Fatal("the dispatcher touched no file")
	}
	for _, p := range files.paths {
		if !filepath.IsAbs(p) {
			t.Errorf("a relative path %q", p)
		}
		if !strings.HasSuffix(p, ".json") || (!strings.HasPrefix(p, want) && !strings.Contains(p, "hook")) {
			t.Errorf("the dispatcher touched %q; the snapshots are under %q", p, want)
		}
	}
}
