package classes_test

import "testing"

// Part of CA-PERM-32: what a debugger or a systemd unit sets in the
// environment of the command it runs moves the global configuration of git.
func TestWhatADebuggerOrASystemdUnitSetsInTheEnvironmentMovesTheConfigOfGit(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	cases := []evasion{
		{"systemd-run -p Environment=HOME=/tmp git status", "HOME"}, {"systemd-run --property=Environment=HOME=/tmp git status", "HOME"},
		{"systemd-run --property Environment=HOME=/tmp git status", "HOME"}, {"systemd-run -pEnvironment=HOME=/tmp git status", "HOME"},
		{"systemd-run -p 'Environment=A=1 HOME=/tmp' git status", "HOME"}, {"systemd-run -p EnvironmentFile=/tmp/e git status", "EnvironmentFile"},
		{"systemd-run --property=EnvironmentFile=/tmp/e git status", "EnvironmentFile"},
		{"rr record --env HOME=/tmp git status", "HOME"}, {"rr record --env=HOME=/tmp git status", "HOME"},
		{"gdb -ex 'set exec-wrapper env HOME=/tmp' --args git status", "HOME"}, {"gdb -iex 'set exec-wrapper env HOME=/tmp' --args git status", "HOME"},
		{"gdb -ex 'set environment HOME /tmp' --args git status", "HOME"}, {"gdb -ex 'set env HOME=/tmp' --args git status", "HOME"},
		{"gdb -ex 'unset environment GIT_CONFIG_GLOBAL' --args git status", "GIT_CONFIG_GLOBAL"},
	}
	for _, ee := range evasionEnvs() {
		fixedDenials(t, ee.name, ee.env, r, cases)
	}
}

// Part of CA-PERM-33: the programs of Windows that start a process later or
// elsewhere run the command line they are given.
func TestTheWindowsLaunchersRunTheCommandLineTheyAreGiven(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	cases := []evasion{
		{"New-ScheduledTaskAction -Execute git -Argument push", "git push"}, {`New-ScheduledTaskAction -Execute "git" -Argument "push"`, "git push"},
		{`New-ScheduledTaskAction -Execute 'git push'`, "git push"}, {"New-ScheduledTaskAction -Argument push -Execute git", "git push"},
		{`sc.exe create x binPath= "git push"`, "git push"}, {`sc create x binpath= "git push" start= auto`, "git push"}, {`sc.exe config x binPath= "git push"`, "git push"},
		{`New-Service -Name x -BinaryPathName "git push"`, "git push"}, {`New-Service x "git push"`, "git push"}, {`new-service -binarypathname 'git push' -name x`, "git push"},
		{`rundll32 shell32.dll,ShellExec_RunDLL git push`, "git push"}, {`rundll32.exe shell32,ShellExec_RunDLL "git push"`, "git push"},
		{`pcalua -a git -c push`, "git push"}, {`pcalua.exe -a "git" -c "push"`, "git push"},
		{`at 12:00 git push`, "git push"}, {`at 12:00 /every:M "git push"`, "git push"},
	}
	for _, ee := range evasionEnvs() {
		if ee.name != "windows" {
			continue
		}
		fixedDenials(t, ee.name, ee.env, r, cases)
	}
}

// Part of CA-PERM-33: what the launchers run, when it is not a fixed rule, is
// at least a request (as schtasks /tr), and mshta never runs on its own.
func TestTheWindowsLaunchersAskForWhatIsNotAFixedRuleAndMshtaAlways(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	for _, ee := range evasionEnvs() {
		if ee.name != "windows" {
			continue
		}
		neverAllowed(t, ee.name, ee.env, r,
			"New-ScheduledTaskAction -Execute notepad.exe", `sc.exe create x binPath= "notepad.exe"`, `New-Service -Name x -BinaryPathName "notepad.exe"`,
			"rundll32 shell32.dll,ShellExec_RunDLL notepad.exe", "pcalua -a notepad.exe", "at 12:00 notepad.exe",
			"mshta x.hta", "mshta https://example.com/x.hta", `mshta "javascript:eval(a)"`, `mshta vbscript:Execute(x)`)
		allowances(t, ee.name, ee.env, r, "sc.exe query x", "sc start x", "rundll32 user32.dll,LockWorkStation", "at", "New-ScheduledTaskTrigger -Daily -At 3am")
	}
}
