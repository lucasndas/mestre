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

// CA-WIN-03 (the spellings of the worktree that must not slip through)
func TestAgentEnvRecognizesTheOtherSpellingsOfTheWorktree(t *testing.T) {
	t.Parallel()
	const wt = `D:\work\mesa-1`
	for _, p := range []string{
		`D:\work\x\..\mesa-1`, `D:\work\.\mesa-1\bin`, `D:\work\mesa-1.`, `D:\work\mesa-1 \bin`,
		`\\?\D:\work\mesa-1\bin`, `d:/WORK/Mesa-1/`, `D:\work\mesa-1\a\..\..\mesa-1\z`,
	} {
		if !under(p, wt) {
			t.Errorf("%q is not seen under %q", p, wt)
		}
	}
	for _, p := range []string{`D:\work\mesa-10`, `D:\work`, `D:\work\mesa-2\mesa-1`, `\\srv\work\mesa-1`, `E:\work\mesa-1`} {
		if under(p, wt) {
			t.Errorf("%q is seen under %q", p, wt)
		}
	}
}

// CA-WIN-03 (the program of the agent comes from the cleaned PATH)
func TestResolveProgramLooksOnlyInTheCleanedPathAndTrimsNothingElse(t *testing.T) {
	t.Parallel()
	have := map[string]bool{`c:\tools\claude.cmd`: true, `d:\wt\claude.exe`: true, `c:\windows\system32\cmd.exe`: true}
	exists := func(p string) bool { return have[strings.ToLower(p)] } // the file system of Windows ignores case
	env := agentEnv("windows", []string{`PATH=D:\wt;C:\tools;C:\Windows\System32`}, `D:\wt`)
	got, err := resolveProgram("windows", "claude", env, exists)
	if err != nil || !strings.EqualFold(got, `C:\tools\claude.cmd`) {
		t.Errorf("claude = %q, %v: want the one of the PATH, not the planted D:\\wt\\claude.exe", got, err)
	}
	if got, err := resolveProgram("windows", "cmd.exe", env, exists); err != nil || got != `C:\Windows\System32\cmd.exe` {
		t.Errorf("cmd.exe = %q, %v", got, err)
	}
	for _, bad := range []string{"", `.\claude`, `sub\claude`, "C:claude", "git"} {
		if got, err := resolveProgram("windows", bad, env, exists); err == nil {
			t.Errorf("%q resolved to %q", bad, got)
		}
	}
	if got, err := resolveProgram("windows", `C:\a\b.exe`, env, exists); err != nil || got != `C:\a\b.exe` {
		t.Errorf("an absolute path changed: %q, %v", got, err)
	}
	if got, err := resolveProgram("linux", "claude", nil, exists); err != nil || got != "claude" {
		t.Errorf("linux changed the program: %q, %v", got, err)
	}
}
