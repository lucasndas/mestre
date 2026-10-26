package classes_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
)

// The PowerShell and Windows forms that hid a command or a move of the home of
// git from the fixed rules (docs/classes/permissoes.md): a call of .NET or a
// cmdlet that sets the environment, and the tools that create a process from a
// command line they are given.

// wantAsk reports the lines whose verdict is not ask: not an allowance, and not
// a fixed denial.
func wantAsk(t *testing.T, name string, env classes.Env, r *classes.Rules, cmds ...string) {
	t.Helper()
	for _, c := range cmds {
		if d := judge(env, r, c); d.Verdict != classes.Ask {
			t.Errorf("%s: %q = %s by %s (%q); want ask", name, c, d.Verdict, d.Layer, d.Reason)
		}
	}
}

// CA-PERM-33
func TestPowerShellEnvironmentFormsMoveTheGlobalConfigOfGit(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	win := evasionEnvs()[1]
	moved := []evasion{
		// the .NET call that sets a variable of the process
		{"[Environment]::SetEnvironmentVariable('HOME','C:\\x'); git status", "HOME"},
		{"[Environment]::SetEnvironmentVariable(\"HOME\", \"x\", \"Process\"); git status", "HOME"},
		{"[System.Environment]::SetEnvironmentVariable('XDG_CONFIG_HOME','x'); git status", "XDG_CONFIG_HOME"},
		{"[environment]::setenvironmentvariable('USERPROFILE','x'); git status", "USERPROFILE"},
		{"[Environment]::SetEnvironmentVariable('HOMEDRIVE','x'); git status", "HOMEDRIVE"},
		{"[Environment]::SetEnvironmentVariable('HOME','x','User'); git status", "HOME"},
		{"$null = [Environment]::SetEnvironmentVariable('HOME','x'); git status", "HOME"},
		{"[void][Environment]::SetEnvironmentVariable('HOME','x'); git status", "HOME"},
		{"& { [Environment]::SetEnvironmentVariable('HOME','x') }; git status", "HOME"},
		{"[Environment]::SetEnvironmentVariable('HOME','x')\ngit status", "HOME"},
		{"[Environment]::SetEnvironmentVariable('HOME','x'); bash -c 'git status'", "HOME"},
		{"[Environment]::SetEnvironmentVariable('GIT_CONFIG_GLOBAL', $null)", "GIT_CONFIG_GLOBAL"},
		{"[Environment]::SetEnvironmentVariable('GIT_CONFIG_GLOBAL','')", "GIT_CONFIG_GLOBAL"},
		{"[System.Environment]::SetEnvironmentVariable(\"GIT_CONFIG_GLOBAL\",\"C:\\x\")", "GIT_CONFIG_GLOBAL"},
		{"[Environment]::SetEnvironmentVariable('GIT_CONFIG_SYSTEM', $null)", "GIT_CONFIG"},
		{"[Environment]::SetEnvironmentVariable('GIT_DIR','x')", "GIT_DIR"},
		{"[System.Environment]::SetEnvironmentVariable('GIT_WORK_TREE','x')", "GIT_WORK_TREE"},
		// a name in braces, and the other assignment operators
		{"${env:HOME} = 'x'; git status", "HOME"}, {"${env:HOME}='x'; git status", "HOME"}, {"${ENV:Home} = 'x'; git status", "Home"},
		{"${env:GIT_DIR} = 'x'", "GIT_DIR"}, {"${env:GIT_CONFIG_GLOBAL} = $null", "GIT_CONFIG_GLOBAL"},
		{"$env:HOME += 'x'; git status", "HOME"}, {"$env:HOME+='x'; git status", "HOME"}, {"$env:XDG_CONFIG_HOME ??= 'x'; git status", "XDG_CONFIG_HOME"},
		{"$env:GIT_CONFIG_GLOBAL += 'x'", "GIT_CONFIG_GLOBAL"},
		// the cmdlets that reach the Env: drive, or name a variable of it
		{"Set-Variable -Name env:HOME -Value x; git status", "HOME"}, {"Set-Variable env:HOME x; git status", "HOME"}, {"sv env:HOME x; git status", "HOME"},
		{"New-Variable env:HOME x; git status", "HOME"}, {"nv env:HOME x; git status", "HOME"}, {"set env:HOME x; git status", "HOME"},
		{"Copy-Item Env:FOO Env:HOME; git status", "HOME"}, {"cpi Env:FOO Env:HOME; git status", "HOME"},
		{"Copy-Item -Path Env:FOO -Destination Env:HOME; git status", "HOME"},
		{"Move-Item Env:FOO Env:HOME; git status", "HOME"}, {"mi Env:FOO Env:HOME; git status", "HOME"},
		{"Rename-Item Env:FOO HOME; git status", "HOME"}, {"Rename-Item -Path Env:FOO -NewName HOME; git status", "HOME"}, {"rni Env:FOO HOME; git status", "HOME"},
		{"Set-Item -Path:Env:HOME x; git status", "HOME"}, {"Set-Item -LiteralPath Env:\\HOME x; git status", "HOME"}, {"Set-Item Env:\\HOME x; git status", "HOME"},
		{"Remove-Item Env:\\GIT_CONFIG_GLOBAL", "GIT_CONFIG_GLOBAL"}, {"Move-Item Env:GIT_CONFIG_GLOBAL Env:X", "GIT_CONFIG_GLOBAL"},
		{"Rename-Item Env:GIT_CONFIG_GLOBAL X", "GIT_CONFIG_GLOBAL"}, {"Clear-Item Env:GIT_CONFIG_GLOBAL", "GIT_CONFIG_GLOBAL"},
		{"Set-Variable env:GIT_DIR x", "GIT_DIR"},
	}
	fixedDenials(t, win.name, win.env, r, moved)
	// Other variables, and the same cmdlets on files and on variables, are what
	// they were.
	allowances(t, win.name, win.env, r,
		"Set-Variable -Name x -Value 1; git status", "sv x 1; git status", "New-Variable x 1; git status", "set x 1; git status",
		"Copy-Item Env:FOO Env:BAR; git status", "Move-Item Env:FOO Env:BAR; git status", "Rename-Item Env:FOO BAR; git status",
		"Copy-Item a.txt b.txt; git status", "Move-Item a.txt b.txt; git status", "Rename-Item a.txt b.txt; git status",
		"Set-Item Env:FOO x; git status", "Remove-Item Env:FOO; git status", "Get-ChildItem Env:")
	// A variable of PowerShell is a request on the line that has it, and so is
	// the .NET call, whatever the variable: the denial is for those that move
	// the configuration of git. Reading one is no assignment.
	wantAsk(t, win.name, win.env, r,
		"${env:FOO} = 'x'; git status", "$env:FOO += 'x'; git status", "$env:FOO+='x'; git status", "$env:FOO ??= 'x'; git status",
		"[Environment]::SetEnvironmentVariable('FOO','1'); git status", "[Environment]::SetEnvironmentVariable($n,'x'); git status",
		"[Environment]::GetEnvironmentVariable('HOME'); git status")
}

// CA-PERM-33
func TestWindowsToolsThatCreateAProcessRunTheCommandTheyAreGiven(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	created := []evasion{
		// a scheduled task runs its /tr later, in the account that created it
		{`schtasks /create /tn t /tr "git push" /sc once /st 00:00`, "git push"},
		{`schtasks /create /tn t /sc once /st 00:00 /tr "git push"`, "git push"},
		{`schtasks /Create /TN t /TR "git push" /SC ONCE`, "git push"},
		{`schtasks -create -tn t -tr "git push" -sc once`, "git push"},
		{`schtasks /change /tn t /tr "git push"`, "git push"},
		{`schtasks.exe /create /tn t /tr "cmd /c git push" /sc daily`, "git push"},
		{`schtasks /create /tn t /tr 'git push' /sc once`, "git push"},
		{`schtasks /create /tn t /tr "sudo id" /sc daily`, "privilégio"},
		{`schtasks /create /tn t /tr claude /sc daily`, "claude"},
		{`schtasks /create /tn t /tr "powershell -c 'git push'" /sc once`, "git push"},
		{`schtasks /create /tn t /tr "\"C:\Program Files\Git\bin\git.exe\" push" /sc once`, "git push"},
		{`schtasks /create /tn t /tr "cp x .git/hooks/pre-commit" /sc once`, ".git"},
		// mshta runs the script of a URL given in the line
		{`mshta "javascript:a=new ActiveXObject('WScript.Shell');a.Run('git push',0,true);close()"`, "git push"},
		{`mshta "javascript:new ActiveXObject('WScript.Shell').Exec('git push');close()"`, "git push"},
		{`mshta "javascript:new ActiveXObject('Shell.Application').ShellExecute('git','push');close()"`, "git push"},
		{`mshta 'vbscript:CreateObject("WScript.Shell").Run("git push",0)(window.close)'`, "git push"},
		{`mshta "javascript:a=new ActiveXObject('WScript.Shell');a.Run('sudo id');close()"`, "privilégio"},
		{`mshta "javascript:a=new ActiveXObject('WScript.Shell');a.run('claude');close()"`, "claude"},
		// Win32_Process.Create takes the command line first, whatever way the
		// arguments are given
		{`Invoke-WmiMethod -Class Win32_Process -Name Create -ArgumentList "git push"`, "git push"},
		{`Invoke-WmiMethod -Class Win32_Process -Name Create "git push"`, "git push"},
		{`Invoke-WmiMethod Win32_Process Create "git push"`, "git push"},
		{`Invoke-WmiMethod -Name Create -Class Win32_Process "git push"`, "git push"},
		{`Invoke-WmiMethod -Path Win32_Process -Name Create "git push"`, "git push"},
		{`Invoke-WmiMethod -Class Win32_Process -Name Create -Arg "git push"`, "git push"},
		{`Invoke-WmiMethod -Class Win32_Process -Name Create -Argument "git push"`, "git push"},
		{`Invoke-WmiMethod -Class Win32_Process -Name Create -ArgumentList:"git push"`, "git push"},
		{`Invoke-WmiMethod -Class Win32_Process -Name:Create -ArgumentList "git push"`, "git push"},
		{`Invoke-WmiMethod -Class Win32_Process -Name Create -ArgumentList 'git push', 'C:\Temp'`, "git push"},
		{`Invoke-WmiMethod -Class Win32_Process -Name Create -ArgumentList 'git push','C:\Temp'`, "git push"},
		{`iwmi Win32_Process Create 'git push'`, "git push"},
		{`Invoke-WmiMethod Win32_Process Create "sudo id"`, "privilégio"},
		{`Invoke-WmiMethod Win32_Process Create claude`, "claude"},
		{`Invoke-WmiMethod -Class Win32_Process -Name Create -ArgumentList "cp x .git/hooks/pre-commit"`, ".git"},
		{`wmic process call create "git push", "C:\Temp"`, "git push"},
		{`wmic process call create 'git push','C:\Temp'`, "git push"},
		{`wmic process call create "git push"`, "git push"},
	}
	for _, ee := range evasionEnvs() {
		for _, operator := range []bool{false, true} {
			env := ee.env
			env.OperatorBase = operator
			fixedDenials(t, ee.name, env, r, created)
		}
		// What they run, when no fixed rule is in it, is a request: the tool
		// that creates a process from a line is never run on its own.
		wantAsk(t, ee.name, ee.env, r, `schtasks -create -tn t -tr notepad.exe -sc once`, `wmic process call create notepad.exe`, `Invoke-WmiMethod Win32_Process Create notepad.exe`)
	}
	// A query, or a task that is not given a command, starts nothing.
	win := evasionEnvs()[1]
	allowances(t, win.name, win.env, r, `schtasks -query`, `schtasks -delete -tn t -f`, `Invoke-WmiMethod -Class Win32_OperatingSystem -Name GetOwner`)
}

// CA-PERM-34: PowerShell reads CR, LF and CRLF as new-line characters, so a
// lone CR ends a command as a LF does; the POSIX reading keeps it a blank (stricter, never looser).
func TestPowerShellReadsALoneCarriageReturnAsANewLine(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	envs := evasionEnvs()
	native, win := envs[0], envs[1]
	denied := []evasion{
		{"echo hi\rgit push --force", "push"},
		{"echo hi\r\ngit push --force", "push"},
		{"echo hi\ngit push --force", "push"},
		{"# note\rgit push --force", "push"},
		{"echo hi `\rgit push --force", "push"},
	}
	fixedDenials(t, win.name, win.env, r, denied)
	// The POSIX reading is unchanged: the CR stays a blank, so the line is
	// read as the arguments of echo and no new command appears.
	posix := native.env
	posix.OS = "linux"
	c := "echo hi\rgit push --force"
	if d := judge(posix, r, c); d.Verdict == classes.Deny && d.Layer == classes.LayerFixed {
		t.Errorf("%s: %q = fixed denial (%q)", native.name, c, d.Reason)
	}
}

// Windows PowerShell 5.1 hands an argument to a program that is not a cmdlet between
// double quotes it does not escape inside: a quote in the argument reaches the program
// as several arguments, so what the text shows is not what is run.
// CA-PERM-33
func TestAQuoteInAnArgumentOfANativeProgramInPowerShellAsks(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	win := evasionEnvs()[1]
	wantAsk(t, "quote in the argument of a program", win.env, r,
		`rg 'a" --pre=calc.exe "b c' .`,
		`rg "a`+"`"+`" --pre=calc.exe `+"`"+`"b c" .`,
		`git log --format='%H" --output=x "'`,
		"rg 'a” --pre=calc.exe “b' .",
	)
	for _, c := range []string{`rg 'abc' .`, `Select-String -Pattern 'a"b' x.txt`, `echo 'say "hi"'`, `git status`} {
		if d := judge(win.env, r, c); d.Verdict != classes.Allow {
			t.Errorf("%q = %s (%q); want allow", c, d.Verdict, d.Reason)
		}
	}
}
