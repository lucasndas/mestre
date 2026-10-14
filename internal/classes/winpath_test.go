package classes_test

import (
	"os"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// windowsEnv is a character of an instance on Windows, on any system.
func windowsEnv() classes.Env {
	return classes.Env{
		AgentID: agentID, TableID: "42-login", OS: "windows", Exceptions: newLedger(),
		Layout:   classes.DefaultLayout("windows", `C:\ProgramData`, "principal"),
		Worktree: `C:\ProgramData\Mestre\principal\workspaces\loja\mesas\42-login`,
		Tmp:      `C:\ProgramData\Mestre\principal\maquina\agentes\` + agentID + `\tmp`,
	}
}

// The UNC spellings of a local drive are that drive; the other shares of
// this machine, the hidden shares and the device paths are denied.
func TestWindowsUNCSpellingsOfThisMachine(t *testing.T) {
	t.Parallel()
	env := windowsEnv()
	free := rules(t, anyYAML)
	host, _ := os.Hostname()
	auth := `\ProgramData\Mestre\principal\contas\p\auth.json`
	at := func(kind providers.ToolKind, p string) providers.Event {
		return event(&providers.Tool{NativeName: "Read", Kind: kind, Paths: []string{p}}, env.Worktree)
	}
	for _, p := range []string{
		`\\localhost\C$` + auth, `\\127.0.0.1\c$` + auth, `\\127.1.2.3\C$` + auth, `\\?\UNC\localhost\C$` + auth,
		`//localhost/c$` + auth, `\\0--1.ipv6-literal.net\C$` + auth, `\\` + host + `\C$` + auth, `\\?\C:` + auth,
	} {
		want(t, decide(env, free, at(providers.KindRead, p)), classes.Deny, classes.LayerFixed, "contas")
	}
	for _, p := range []string{
		`\\localhost\share\x.txt`, `\\` + strings.ToUpper(host) + `\docs\x`, `\\fileserver\ADMIN$\x`, `\\fileserver\d$\x`, `\\fileserver\IPC$`,
		`\\?\GLOBALROOT\Device\HarddiskVolume3` + auth, `\\?\Volume{12345678-1234-1234-1234-123456789abc}` + auth, `\\.\pipe\mestre`, `\\.\PhysicalDrive0`,
	} {
		want(t, decide(env, free, at(providers.KindRead, p)), classes.Deny, classes.LayerFixed)
		want(t, decide(env, free, at(providers.KindWrite, p)), classes.Deny, classes.LayerFixed)
	}
	// The worktree through the administrative share is the worktree.
	in := `\\localhost\C$\ProgramData\Mestre\principal\workspaces\loja\mesas\42-login\tests\a.ts`
	want(t, decide(env, free, at(providers.KindWrite, in)), classes.Allow, "")
	want(t, decide(env, free, at(providers.KindWrite, `\\localhost\C$\ProgramData\Mestre\principal\workspaces\loja\mesas\42-login\.git\config`)), classes.Deny, classes.LayerFixed, ".git")
	// A share of another machine is outside the worktree, not a fixed rule.
	want(t, decide(env, free, at(providers.KindRead, `\\fileserver\docs\x.txt`)), classes.Deny, classes.LayerClass, "fora do worktree")
	// In a command too.
	want(t, decide(env, free, event(&providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: `type \\localhost\c$` + auth}, env.Worktree)), classes.Deny, classes.LayerFixed)
}
