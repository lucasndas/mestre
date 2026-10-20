package ptyd

import (
	"slices"
	"strings"
	"testing"
)

// CA-WIN-03
//
// On Windows the process of an agent gets NoDefaultCurrentDirectoryInExePath=1
// and a PATH without the folders it can plant a program in: empty and
// relative entries, the worktree and what is under it. Elsewhere nothing
// changes.
func TestAgentEnvOnWindowsStopsTheCurrentFolderAndTheWorktreeFromLeadingThePath(t *testing.T) {
	t.Parallel()
	const wt = `D:\work\mesa-1`
	env := []string{
		`Path=C:\Windows\System32;;.;D:\work\mesa-1;d:/WORK/mesa-1/bin;"D:\work\mesa-1\x";bin;C:rel;\rooted;C:\Program Files\Git\cmd;\\srv\share\tools`,
		"NoDefaultCurrentDirectoryInExePath=0",
		"MESTRE_AGENT_ID=a1",
	}
	got := agentEnv("windows", slices.Clone(env), wt)
	var path string
	var flag []string
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		switch strings.ToLower(k) {
		case "path":
			path = v
		case "nodefaultcurrentdirectoryinexepath":
			flag = append(flag, v)
		}
	}
	if want := `C:\Windows\System32;C:\Program Files\Git\cmd;\\srv\share\tools`; path != want {
		t.Errorf("PATH = %q, want %q", path, want)
	}
	if !slices.Equal(flag, []string{"1"}) {
		t.Errorf("NoDefaultCurrentDirectoryInExePath = %v, want exactly 1", flag)
	}
	if !slices.Contains(got, "MESTRE_AGENT_ID=a1") {
		t.Errorf("the rest of the environment is lost: %v", got)
	}
	if got := agentEnv("windows", []string{`PATH=.;D:\work\mesa-1`}, wt); got[0] != "PATH=" {
		t.Errorf("a PATH with nothing safe left must be empty, got %v", got)
	}
	if got := agentEnv("linux", slices.Clone(env), wt); !slices.Equal(got, env) {
		t.Errorf("linux changed the environment: %v", got)
	}
}
