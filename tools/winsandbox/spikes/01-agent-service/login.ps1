# Opens, through the MestreSpikeLauncher service (session 0), a terminal as
# mestre-agente running the login of one CLI. Ctrl+] leaves.
# agy-install runs the official Antigravity installer as mestre-agente, so agy
# lands in that account's profile (%LOCALAPPDATA%\agy\bin) and self-updates
# there; agy then runs from that copy.
param([Parameter(Mandatory)][ValidateSet('claude', 'codex', 'agy', 'agy-install')][string]$Cli)
$s = 'C:\ProgramData\MestreSpike\bin\spikesvc.exe'
$C = 'C:\ProgramData\MestreSpike\clis'
$P = 'C:\ProgramData\MestreSpike\work\contas\p1'
$Agy = 'C:\Users\mestre-agente\AppData\Local\agy\bin\agy.exe'
switch ($Cli) {
    'claude' { & $s open -i -svc launcher -env "CLAUDE_CONFIG_DIR=$P\claude" -- "$C\claude\claude.exe" auth login }
    'codex' { & $s open -i -svc launcher -env "CODEX_HOME=$P\codex" -- "$C\codex\bin\codex.exe" login --device-auth }
    'agy' { & $s open -i -svc launcher -env "HOME=$P\home" -env "USERPROFILE=$P\home" -- $Agy }
    'agy-install' { & $s open -i -svc launcher -- powershell.exe -NoProfile -ExecutionPolicy Bypass -Command 'irm https://antigravity.google/cli/install.ps1 | iex' }
}
