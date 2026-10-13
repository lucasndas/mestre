# Opens, through the MestreSpikeLauncher service (session 0), a terminal as
# mestre-agente running the login of one CLI. Ctrl+] leaves.
# The *-install modes run the official installer of each CLI as mestre-agente,
# so the CLI lands in that account's profile and self-updates there:
# claude in %USERPROFILE%\.local\bin, codex in %USERPROFILE%\.codex\packages
# (CODEX_HOME left at its default, %USERPROFILE%\.codex), agy in
# %LOCALAPPDATA%\agy\bin.
param([Parameter(Mandatory)][ValidateSet('claude', 'codex', 'agy', 'claude-install', 'codex-install', 'agy-install')][string]$Cli)
$s = 'C:\ProgramData\MestreSpike\bin\spikesvc.exe'
$P = 'C:\ProgramData\MestreSpike\work\contas\p1'
$U = 'C:\Users\mestre-agente'
$Claude = "$U\.local\bin\claude.exe"
$Codex = "$U\.codex\packages\standalone\current\bin\codex.exe"
$Agy = "$U\AppData\Local\agy\bin\agy.exe"
function Install($url) {
    & $s open -i -svc launcher -- powershell.exe -NoProfile -ExecutionPolicy Bypass -Command "irm $url | iex"
}
switch ($Cli) {
    'claude' { & $s open -i -svc launcher -env "CLAUDE_CONFIG_DIR=$P\claude" -- $Claude auth login }
    'codex' { & $s open -i -svc launcher -- $Codex login --device-auth }
    'agy' { & $s open -i -svc launcher -env "HOME=$P\home" -env "USERPROFILE=$P\home" -- $Agy }
    'claude-install' { Install 'https://claude.ai/install.ps1' }
    'codex-install' { Install 'https://chatgpt.com/codex/install.ps1' }
    'agy-install' { Install 'https://antigravity.google/cli/install.ps1' }
}
