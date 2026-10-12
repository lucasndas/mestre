# Prepares a fresh Windows Sandbox for the M1 Windows spikes.
# Installs Git for Windows and Go (SHA-256 checked) and Claude Code (official
# installer), then clones the repository. Nothing persists after the Sandbox
# closes, so this runs on every opening. See docs/desenvolvimento/ambiente.md.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Shared  = 'C:\winsandbox'
$Results = Join-Path $Shared 'resultados'
New-Item -ItemType Directory -Force -Path $Results | Out-Null
Start-Transcript -Path (Join-Path $Results 'setup.log') -Append | Out-Null

$Downloads = 'C:\downloads'
New-Item -ItemType Directory -Force -Path $Downloads | Out-Null

# Pinned versions and checksums (update together).
$GitVersion = '2.56.0.windows.2'
$GitFile    = 'Git-2.56.0.2-64-bit.exe'
$GitSha256  = '52188f917b378f00c70ec136bcf090005f30d44fbc4eba0bce759cc6592d60f6'
$GoFile     = 'go1.27.2.windows-amd64.msi'
$GoSha256   = 'dc673c9207a83f95031549fafe72cc0b4c4554a1c16b373d875e90cf8d995ed9'
$Repo       = 'https://github.com/lucasndas/mestre.git'
$Branch     = 'spike/m1-windows'

function Get-Checked([string]$Url, [string]$Path, [string]$Sha256) {
    Write-Host "Baixando $Url"
    Invoke-WebRequest -Uri $Url -OutFile $Path -UseBasicParsing
    $actual = (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLowerInvariant()
    if ($actual -ne $Sha256) { throw "Checksum errado para $Path`: $actual (esperado $Sha256)" }
    Write-Host "  SHA-256 conferido."
}

function Add-ToPath([string]$Dir) {
    $env:Path = "$Dir;$env:Path"
    $user = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not ($user -split ';' | Where-Object { $_ -eq $Dir })) {
        [Environment]::SetEnvironmentVariable('Path', "$Dir;$user", 'User')
    }
}

# Git for Windows (includes Git Credential Manager for the GitHub login on push).
if (-not (Test-Path 'C:\Program Files\Git\cmd\git.exe')) {
    $gitExe = Join-Path $Downloads $GitFile
    Get-Checked "https://github.com/git-for-windows/git/releases/download/v$GitVersion/$GitFile" $gitExe $GitSha256
    Start-Process -FilePath $gitExe -ArgumentList '/VERYSILENT', '/NORESTART', '/NOCANCEL', '/SP-', '/SUPPRESSMSGBOXES' -Wait
}
Add-ToPath 'C:\Program Files\Git\cmd'

# Go.
# Official MSI (archive extraction is unreliable inside the Sandbox).
if (-not (Test-Path 'C:\Program Files\Go\bin\go.exe')) {
    $goMsi = Join-Path $Downloads $GoFile
    Get-Checked "https://go.dev/dl/$GoFile" $goMsi $GoSha256
    $p = Start-Process -FilePath msiexec.exe -ArgumentList '/i', $goMsi, '/qn', '/norestart' -Wait -PassThru
    if ($p.ExitCode -ne 0) { throw "msiexec falhou ao instalar $goMsi (codigo $($p.ExitCode))" }
}
Add-ToPath 'C:\Program Files\Go\bin'

# Claude Code, official installer (it downloads and installs the native binary).
if (-not (Test-Path (Join-Path $env:USERPROFILE '.local\bin\claude.exe'))) {
    Write-Host 'Instalando o Claude Code pelo instalador oficial'
    Invoke-RestMethod https://claude.ai/install.ps1 | Invoke-Expression
}
Add-ToPath (Join-Path $env:USERPROFILE '.local\bin')

# Repository, on the spike branch.
git config --global user.name 'Lucas Novais'
git config --global user.email 'lucasndas@gmail.com'
git config --global core.autocrlf false
if (-not (Test-Path 'C:\mestre\.git')) { git clone $Repo C:\mestre }
Set-Location C:\mestre
git fetch origin
if (git ls-remote --heads origin $Branch) { git checkout -B $Branch "origin/$Branch" } else { git checkout -B $Branch origin/main }

git --version
go version
claude --version

Write-Host ''
Write-Host 'Preparo pronto. Agora:'
Write-Host '  1. claude            (faça o login no navegador e saia com /exit)'
Write-Host '  2. cd C:\mestre; claude --model opus --effort high "Leia e execute o arquivo tools/winsandbox/spikes-prompt.md"'
Stop-Transcript | Out-Null
