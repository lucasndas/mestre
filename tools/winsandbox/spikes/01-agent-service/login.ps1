# Opens, through the MestreSpikeLauncher service (session 0), a terminal as
# mestre-agente running the login of one CLI. Ctrl+] leaves.
param([Parameter(Mandatory)][ValidateSet('claude', 'codex', 'agy')][string]$Cli)
$s = 'C:\ProgramData\MestreSpike\bin\spikesvc.exe'
$C = 'C:\ProgramData\MestreSpike\clis'
$P = 'C:\ProgramData\MestreSpike\work\contas\p1'
switch ($Cli) {
    'claude' { & $s open -i -svc launcher -env "CLAUDE_CONFIG_DIR=$P\claude" -- "$C\claude\claude.exe" auth login }
    'codex' { & $s open -i -svc launcher -env "CODEX_HOME=$P\codex" -- "$C\codex\bin\codex.exe" login --device-auth }
    'agy' { & $s open -i -svc launcher -env "HOME=$P\home" -env "USERPROFILE=$P\home" -- "$C\agy\agy.exe" }
}
