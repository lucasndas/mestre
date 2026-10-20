# Round of real Windows checks of M3/M4 (docs/arquitetura/windows.md#rodada-de-verificações-reais).
#
# Runs inside the Windows Sandbox opened by mestre-round.wsb (setup.ps1 -Round
# prepares the machine and calls this script), or by hand in any disposable
# Windows machine with Git, Go and Claude Code installed:
#
#   powershell -ExecutionPolicy Bypass -File run-all.ps1 [-Only 1,8] [-NoPause]
#
# Every check is a function of its own and a failure in one never stops the
# others. The script never logs in by itself: where the Owner's login is needed
# it PAUSES with a short instruction and goes on. It never prints a token, a
# password or the content of a credential (everything that is written to the
# result goes through Protect-Text). The outcome is RESULTADO.txt, with
# PASS/FAIL/SKIP per item and the minimal output to paste back.
#
# Idempotent: installs only what is missing, rebuilds the binaries, overwrites
# the results and removes the temporary accounts, services and tasks it makes.
[CmdletBinding()]
param(
    # Run only these items (1 to 9); the default is all of them. Text, split
    # at the commas: under powershell -File, "-Only 1,8" arrives as one string.
    [string[]]$Only = @(),
    # Never pause: what needs a login of the Owner or a manual step is SKIP.
    [switch]$NoPause,
    [string]$Out = 'C:\winsandbox\resultados\round'
)
$Wanted = [int[]]@($Only | ForEach-Object { $_ -split ',' } | Where-Object { $_.Trim() -ne '' } | ForEach-Object { [int]$_.Trim() })
$ErrorActionPreference = 'Continue'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$Repo     = 'C:\mestre'
$Work     = 'C:\rodada'
$Bin      = Join-Path $Work 'bin'
$Profiles = Join-Path $Work 'perfis'
$Detail   = Join-Path $Out 'detalhes'
$Cache    = 'C:\winsandbox\cache'
$Pwsh7Dir = 'C:\tools\pwsh7'
# PowerShell 7 (zip), pinned like the other tools of setup.ps1.
$Pwsh7Version = '7.6.7'
$Pwsh7File    = "PowerShell-$Pwsh7Version-win-x64.zip"
$Pwsh7Sha256  = 'd2f952ebdbf24386cb8923553aa58e41a846db35a6155ea04c58142a4b16dbb7'

New-Item -ItemType Directory -Force -Path $Out, $Detail, $Work, $Bin, $Profiles, $Cache | Out-Null
Start-Transcript -Path (Join-Path $Out 'run-all.log') -Force | Out-Null

$Titles = @{
    1 = 'Linha do hook: Codex no PowerShell e Claude no Git Bash'
    2 = 'Codex: sandbox alcança o socket local (modo unelevated) e identificação do chamador'
    3 = 'Nomes das ferramentas de shell (PowerShell) do Claude e do Codex'
    4 = 'agy: Gerenciador de Credenciais e --sandbox (AppContainer)'
    5 = 'Hooks dentro de subagentes nativos (etapa 12 da conformidade)'
    6 = 'Rodada de conformidade completa no Windows'
    7 = 'Textos da tela de permissão do Claude e do menu de limite de uso'
    8 = 'Leitura do analisador de comandos para PowerShell 5.1 e 7'
    9 = 'ACL de agentes\<id>\ (sem setgid no Windows), accountSID e contas reais'
}
$script:Subs = @{}
foreach ($k in 1..9) { $script:Subs[$k] = New-Object System.Collections.ArrayList }
$script:ConfCache = @{}
$script:ConfWhy = @{}
$script:Versions = [ordered]@{}
$script:Built = $null
$script:Pwsh7 = $null

# ---------------------------------------------------------------- helpers

function Protect-Text([string]$Text) {
    if (-not $Text) { return '' }
    $t = $Text
    $t = $t -replace '(?i)sk-[A-Za-z0-9_\-]{16,}', '[oculto]'
    $t = $t -replace '(?i)gh[pousr]_[A-Za-z0-9]{20,}', '[oculto]'
    $t = $t -replace 'eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+', '[oculto]'
    $t = $t -replace '(?i)bearer\s+[A-Za-z0-9._\-]{8,}', 'Bearer [oculto]'
    $t = $t -replace '(?i)\b(token|secret|password|senha|api[_-]?key|authorization)(["'']?\s*[=:]\s*["'']?)[^\s"'',;]{6,}', '$1$2[oculto]'
    return $t
}

function Add-Sub([int]$Item, [string]$Status, [string]$Text) {
    [void]$script:Subs[$Item].Add([pscustomobject]@{ Status = $Status; Text = (Protect-Text $Text) })
    Write-Host ("  [{0}] item {1}: {2}" -f $Status, $Item, (Protect-Text $Text).Split("`n")[0])
}

function Tail([string]$Text, [int]$N = 12) {
    $lines = @($Text -split "`r?`n" | Where-Object { $_.Trim() -ne '' })
    if ($lines.Count -gt $N) { $lines = $lines[($lines.Count - $N)..($lines.Count - 1)] }
    return ($lines -join "`n")
}

function Save-Detail([string]$Name, [string]$Text) {
    Set-Content -LiteralPath (Join-Path $Detail $Name) -Value (Protect-Text $Text) -Encoding UTF8
}

function Wait-Owner([string]$Message) {
    # Returns $true to go on, $false when the Owner skips or pauses are off.
    if ($NoPause) { Write-Host "  (sem pausa: $Message)"; return $false }
    Write-Host ''
    Write-Host '============================================================' -ForegroundColor Yellow
    Write-Host ">>> PAUSA - precisa de você: $Message" -ForegroundColor Yellow
    Write-Host '    Enter = continuar     P + Enter = pular este passo' -ForegroundColor Yellow
    Write-Host '============================================================' -ForegroundColor Yellow
    $a = Read-Host
    return ($a.Trim().ToUpperInvariant() -ne 'P')
}

function Update-Path {
    $m = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    $u = [Environment]::GetEnvironmentVariable('Path', 'User')
    $extra = @("$env:USERPROFILE\.local\bin", "$env:LOCALAPPDATA\agy\bin", "$env:USERPROFILE\.codex\packages\standalone\current\bin",
        'C:\Program Files\Git\cmd', 'C:\Program Files\Go\bin')
    $env:Path = ($extra + @($m, $u)) -join ';'
}

function ConvertTo-ArgText([string]$A) {
    if ($A -eq '') { return '""' }
    if ($A -notmatch '[\s"]') { return $A }
    $s = $A -replace '(\\*)"', '$1$1\"'
    $s = $s -replace '(\\+)$', '$1$1'
    return '"' + $s + '"'
}

# Runs a program without a console of its own, with a deadline; the output is
# stdout and stderr together. Standard input is closed at once.
function Invoke-Native {
    param(
        [Parameter(Mandatory)][string]$File,
        [string[]]$Arguments = @(),
        [hashtable]$Env = @{},
        [string]$Dir = '',
        [int]$TimeoutSec = 300
    )
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $File
    $psi.Arguments = (@($Arguments | ForEach-Object { ConvertTo-ArgText ([string]$_) })) -join ' '
    $psi.UseShellExecute = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.RedirectStandardInput = $true
    $psi.CreateNoWindow = $true
    if ($Dir) { $psi.WorkingDirectory = $Dir }
    foreach ($k in $Env.Keys) { $psi.EnvironmentVariables[$k] = [string]$Env[$k] }
    $p = New-Object System.Diagnostics.Process
    $p.StartInfo = $psi
    try { [void]$p.Start() } catch { return [pscustomobject]@{ Exit = -1; Out = "não iniciou: $($_.Exception.Message)"; TimedOut = $false } }
    $p.StandardInput.Close()
    $o = $p.StandardOutput.ReadToEndAsync()
    $e = $p.StandardError.ReadToEndAsync()
    $timedOut = $false
    if (-not $p.WaitForExit($TimeoutSec * 1000)) {
        $timedOut = $true
        & taskkill.exe /T /F /PID $p.Id 2>&1 | Out-Null
        [void]$p.WaitForExit(10000)
    }
    $text = $o.Result + $e.Result
    $code = -1
    if (-not $timedOut) { $code = $p.ExitCode }
    return [pscustomobject]@{ Exit = $code; Out = $text; TimedOut = $timedOut }
}

# Runs one check; an exception is a FAIL of that item, never the end of the run.
function Invoke-Check([int]$Item, [scriptblock]$Body) {
    if ($Wanted.Count -gt 0 -and ($Wanted -notcontains $Item)) { return }
    Write-Host ''
    Write-Host ("=== Item {0}: {1}" -f $Item, $Titles[$Item]) -ForegroundColor Cyan
    try { & $Body } catch { Add-Sub $Item 'FAIL' ("erro inesperado: " + $_.Exception.Message) }
}

function Test-Wanted([int]$Item) { return ($Wanted.Count -eq 0 -or ($Wanted -contains $Item)) }

function New-Terminal([string]$Dir, [string[]]$Lines) {
    # A console window for the Owner (Windows Terminal when setup.ps1 put it there).
    if ($NoPause) { return }
    New-Item -ItemType Directory -Force -Path $Dir | Out-Null
    $script = Join-Path $Dir 'inicio.ps1'
    Set-Content -LiteralPath $script -Value $Lines -Encoding UTF8
    $wt = Get-ChildItem 'C:\tools\wt' -Filter wt.exe -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
    $shellArgs = @('-NoExit', '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $script)
    if ($wt) { Start-Process $wt.FullName -ArgumentList (@('-d', $Dir, 'powershell.exe') + $shellArgs) }
    else { Start-Process powershell.exe -WorkingDirectory $Dir -ArgumentList $shellArgs }
}

# A manual step whose answer is pasted in Notepad. Returns the text pasted
# (comment lines starting with # are the instructions and are dropped).
function Read-PastedText([string]$Name, [string]$Instructions) {
    if ($NoPause) { return '' }
    $file = Join-Path $Detail $Name
    $header = ($Instructions -split "`n" | ForEach-Object { '# ' + $_.TrimEnd() }) -join "`r`n"
    Set-Content -LiteralPath $file -Value ($header + "`r`n# --- cole o texto abaixo desta linha, salve (Ctrl+S) e feche o Bloco de Notas ---`r`n") -Encoding UTF8
    # The Sandbox of Windows 11 has no Notepad (it became an app of the Store):
    # there the text is pasted in this console.
    $notepad = Get-Command notepad.exe -ErrorAction SilentlyContinue
    $where = 'o Bloco de Notas'; if (-not $notepad) { $where = 'este console' }
    if (-not (Wait-Owner "vou pedir o texto ($Name) em $where.")) { return '' }
    if ($notepad) {
        Start-Process $notepad.Source -ArgumentList $file -Wait
        $lines = @(Get-Content -LiteralPath $file | Where-Object { $_ -notmatch '^\s*#' })
    } else {
        Write-Host $Instructions
        Write-Host 'Cole o texto aqui e termine com uma linha só com FIM (vazio = nada a colar):' -ForegroundColor Yellow
        $lines = @()
        while ($true) { $l = Read-Host; if ($l.Trim() -eq 'FIM') { break }; $lines += $l }
        Add-Content -LiteralPath $file -Value $lines -Encoding UTF8
    }
    return (($lines -join "`n").Trim())
}

# ---------------------------------------------------------------- tools

function Get-Pwsh7 {
    if ($script:Pwsh7) { return $script:Pwsh7 }
    $exe = Join-Path $Pwsh7Dir 'pwsh.exe'
    if (-not (Test-Path $exe)) {
        $zip = Join-Path $Cache $Pwsh7File
        $ok = $false
        if (Test-Path $zip) { $ok = ((Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLowerInvariant() -eq $Pwsh7Sha256) }
        if (-not $ok) {
            Write-Host "Baixando PowerShell $Pwsh7Version"
            Invoke-WebRequest -Uri "https://github.com/PowerShell/PowerShell/releases/download/v$Pwsh7Version/$Pwsh7File" -OutFile $zip -UseBasicParsing
            $actual = (Get-FileHash -Algorithm SHA256 -Path $zip).Hash.ToLowerInvariant()
            if ($actual -ne $Pwsh7Sha256) { throw "Checksum errado para $Pwsh7File`: $actual" }
        }
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        if (Test-Path $Pwsh7Dir) { Remove-Item -Recurse -Force $Pwsh7Dir }
        [System.IO.Compression.ZipFile]::ExtractToDirectory($zip, $Pwsh7Dir)
    }
    $script:Pwsh7 = $exe
    return $exe
}

function Get-BuiltBinaries {
    # mestre, mestre-pty and the conformance tool, built once from the clone.
    if ($script:Built -ne $null) { return $script:Built }
    if (-not (Test-Path (Join-Path $Repo '.git'))) { $script:Built = $false; $script:BuildWhy = "sem o clone em $Repo (rode o setup.ps1)"; return $false }
    New-Item -ItemType Directory -Force -Path (Join-Path $Bin 'pty\1.0.0') | Out-Null
    $e = @{ CGO_ENABLED = '0' }
    $steps = @(
        @('build', '-o', (Join-Path $Bin 'mestre.exe'), './cmd/mestre'),
        @('build', '-o', (Join-Path $Bin 'pty\1.0.0\mestre-pty.exe'), '-ldflags', '-X main.version=1.0.0', './cmd/mestre-pty'),
        @('build', '-o', (Join-Path $Bin 'conformance.exe'), './tools/conformance'))
    $script:Built = $true
    foreach ($s in $steps) {
        $r = Invoke-Native -File 'go' -Arguments $s -Env $e -Dir $Repo -TimeoutSec 900
        if ($r.Exit -ne 0) { $script:Built = $false; $script:BuildWhy = "go build falhou: " + (Tail $r.Out 6); break }
    }
    return $script:Built
}

function Get-CliPath([string]$Provider) {
    switch ($Provider) {
        'claude-code' { return (Join-Path $env:USERPROFILE '.local\bin\claude.exe') }
        'codex'       { return (Join-Path $env:USERPROFILE '.codex\packages\standalone\current\bin\codex.exe') }
        'antigravity' { return (Join-Path $env:LOCALAPPDATA 'agy\bin\agy.exe') }
    }
}

function Get-ProfileDir([string]$Provider) {
    switch ($Provider) { 'claude-code' { return "$Profiles\claude" } 'codex' { return "$Profiles\codex" } 'antigravity' { return "$Profiles\agy" } }
}

function Install-Cli([string]$Provider) {
    $exe = Get-CliPath $Provider
    if (Test-Path $exe) { return $true }
    $url = @{ 'claude-code' = 'https://claude.ai/install.ps1'; 'codex' = 'https://chatgpt.com/codex/install.ps1'; 'antigravity' = 'https://antigravity.google/cli/install.ps1' }[$Provider]
    Write-Host "Instalando $Provider pelo instalador oficial ($url)"
    $r = Invoke-Native -File 'powershell.exe' -Arguments @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', "irm $url | iex") -TimeoutSec 900
    Save-Detail "instalador-$Provider.log" $r.Out
    Update-Path
    return (Test-Path $exe)
}

function Get-CliEnv([string]$Provider) {
    $dir = Get-ProfileDir $Provider
    switch ($Provider) {
        'claude-code' { return @{ CLAUDE_CONFIG_DIR = $dir } }
        'codex'       { return @{ CODEX_HOME = $dir } }
        # On Windows the adapter gives agy USERPROFILE = HOME (providers/antigravity.go).
        'antigravity' { return @{ HOME = $dir; USERPROFILE = $dir } }
    }
}

# The theme picker of a new CLAUDE_CONFIG_DIR is answered in the initial setup
# of the account profile (docs/provedores/claude-code.md#telas-que-o-pty-precisa-tratar);
# `claude auth login` never shows it, so the profile keeps what that choice writes.
function Complete-ClaudeFirstUse {
    $file = Join-Path (Get-ProfileDir 'claude-code') '.claude.json'
    if (-not (Test-Path $file)) { return }
    $j = Get-Content -LiteralPath $file -Raw | ConvertFrom-Json
    if ($j.hasCompletedOnboarding -and $j.theme) { return }
    if (-not $j.theme) { $j | Add-Member -NotePropertyName theme -NotePropertyValue 'dark' -Force }
    $j | Add-Member -NotePropertyName hasCompletedOnboarding -NotePropertyValue $true -Force
    [IO.File]::WriteAllText($file, ($j | ConvertTo-Json -Depth 100), (New-Object Text.UTF8Encoding $false))
}

function Test-Login([string]$Provider) {
    $exe = Get-CliPath $Provider
    $e = Get-CliEnv $Provider
    switch ($Provider) {
        'claude-code' {
            $r = Invoke-Native -File $exe -Arguments @('auth', 'status') -Env $e -TimeoutSec 60
            return ($r.Exit -eq 0 -and $r.Out -notmatch '(?i)not logged|loggedIn"?\s*:\s*false')
        }
        'codex' {
            $r = Invoke-Native -File $exe -Arguments @('login', 'status') -Env $e -TimeoutSec 60
            return ($r.Exit -eq 0)
        }
        'antigravity' {
            $r = Invoke-Native -File $exe -Arguments @('-p', '/usage', '--output-format', 'json') -Env $e -TimeoutSec 90
            # The first use (the color scheme) of the interactive agy is in the profile
            # too; a login made with HOME alone left it under the real USERPROFILE.
            $onb = Join-Path (Get-ProfileDir $Provider) '.gemini\antigravity-cli\cache\onboarding.json'
            $done = (Test-Path $onb) -and ((Get-Content -LiteralPath $onb -Raw) -match '"onboardingComplete"\s*:\s*true')
            return ($r.Exit -eq 0 -and $r.Out -match '"buckets"' -and $done)
        }
    }
}

# Installs the CLI, makes the profile and, when it is not logged in, pauses for
# the Owner's login. Returns $true when the CLI is ready.
function Initialize-Provider([string]$Provider) {
    if ($script:ConfWhy.ContainsKey("ready-$Provider")) { return ($script:ConfWhy["ready-$Provider"] -eq 'ok') }
    $why = 'ok'
    try {
        $short = @{ 'claude-code' = 'claude'; 'codex' = 'codex'; 'antigravity' = 'agy' }[$Provider]
        New-Item -ItemType Directory -Force -Path (Get-ProfileDir $Provider) | Out-Null
        if (-not (Install-Cli $Provider)) { $why = "$short não instalou (veja detalhes\instalador-$Provider.log)"; throw $why }
        $exe = Get-CliPath $Provider
        $ver = Invoke-Native -File $exe -Arguments @('--version') -Env (Get-CliEnv $Provider) -TimeoutSec 60
        $script:Versions[$short] = ((Tail $ver.Out 1) -replace '\s+', ' ').Trim()
        if ($Provider -eq 'codex') {
            # The login of Codex goes to a file of the profile (docs/provedores/codex.md).
            $cfg = Join-Path (Get-ProfileDir $Provider) 'config.toml'
            $has = (Test-Path $cfg) -and ((Get-Content -LiteralPath $cfg -Raw) -match 'cli_auth_credentials_store')
            if (-not $has) { Add-Content -LiteralPath $cfg -Value 'cli_auth_credentials_store = "file"' }
        }
        if (-not (Test-Login $Provider)) {
            $cmd = @{ 'claude-code' = 'claude auth login (faça o login no navegador do Sandbox e volte aqui)';
                      'codex' = 'codex login --device-auth (abra o link no navegador do Sandbox, digite o código e volte aqui)';
                      'antigravity' = 'agy (escolha o esquema de cores e faça o login no navegador do Sandbox, se ele pedir; depois saia do agy com /exit ou Ctrl+C duas vezes)' }[$Provider]
            if (-not (Wait-Owner "login do $short. Vou rodar: $cmd")) { $why = "sem o login do $short"; throw $why }
            $saved = @{}
            foreach ($k in (Get-CliEnv $Provider).Keys) { $saved[$k] = [Environment]::GetEnvironmentVariable($k); [Environment]::SetEnvironmentVariable($k, (Get-CliEnv $Provider)[$k]) }
            # The call operator keeps the console of the Owner and waits only for the CLI
            # itself: Start-Process -Wait also waits for the helper processes that the
            # login of Claude and of Codex leave running after "Successfully logged in",
            # which hung the script. Out-Host keeps the output out of the function result.
            switch ($Provider) {
                'claude-code' { & $exe auth login | Out-Host }
                'codex'       { & $exe login --device-auth | Out-Host }
                'antigravity' { & $exe | Out-Host }
            }
            foreach ($k in $saved.Keys) { [Environment]::SetEnvironmentVariable($k, $saved[$k]) }
            if (-not (Test-Login $Provider)) { $why = "o login do $short não ficou pronto"; throw $why }
        }
        if ($Provider -eq 'claude-code') { Complete-ClaudeFirstUse }
        if ($Provider -eq 'codex') {
            # The Owner's trust of the hooks of the Mestre in the profile, once (docs/provedores/codex.md#hooks).
            if (-not (Get-BuiltBinaries)) { $why = $script:BuildWhy; throw $why }
            $r = Invoke-Native -File (Join-Path $Bin 'conformance.exe') -Env @{ MESTRE_REAL_CLI = '1' } -TimeoutSec 600 -Arguments @(
                'setup-codex', '-provider', 'codex', '-profile-dir', (Get-ProfileDir 'codex'), '-cli', $exe, '-mestre', (Join-Path $Bin 'mestre.exe'),
                '-pty', (Join-Path $Bin 'pty\1.0.0\mestre-pty.exe'), '-root', "$Work\w\setup-codex")
            Save-Detail 'setup-codex.log' $r.Out
            if ($r.Exit -ne 0) { $why = 'a confiança dos hooks do Codex (setup-codex) falhou: ' + (Tail $r.Out 3); throw $why }
        }
    } catch {
        if ($why -eq 'ok') { $why = $_.Exception.Message }
    }
    $script:ConfWhy["ready-$Provider"] = $why
    return ($why -eq 'ok')
}

# Runs the conformance of one provider once and keeps the result (items 1 to 7
# read it).
function Get-Conformance([string]$Provider) {
    if ($script:ConfCache.ContainsKey($Provider)) { return $script:ConfCache[$Provider] }
    $script:ConfCache[$Provider] = $null
    if (-not (Initialize-Provider $Provider)) { $script:ConfWhy[$Provider] = $script:ConfWhy["ready-$Provider"]; return $null }
    if (-not (Get-BuiltBinaries)) { $script:ConfWhy[$Provider] = $script:BuildWhy; return $null }
    $short = @{ 'claude-code' = 'claude'; 'codex' = 'codex'; 'antigravity' = 'agy' }[$Provider]
    Write-Host "Rodando a conformidade do $short (alguns minutos; gasta poucos turnos de cota real)..."
    $root = "$Work\w\$short"
    if (Test-Path $root) { Remove-Item -Recurse -Force $root -ErrorAction SilentlyContinue }
    New-Item -ItemType Directory -Force -Path $root | Out-Null
    $rec = Join-Path $Out "gravacoes\$short"
    $r = Invoke-Native -File (Join-Path $Bin 'conformance.exe') -Env @{ MESTRE_REAL_CLI = '1' } -TimeoutSec 2400 -Arguments @(
        'run', '-provider', $Provider, '-profile-dir', (Get-ProfileDir $Provider), '-cli', (Get-CliPath $Provider),
        '-mestre', (Join-Path $Bin 'mestre.exe'), '-pty', (Join-Path $Bin 'pty\1.0.0\mestre-pty.exe'), '-root', $root, '-out', $rec, '-restore')
    Save-Detail "conformidade-$short.log" $r.Out
    $lines = @($r.Out -split "`r?`n")
    $start = -1
    for ($i = $lines.Count - 1; $i -ge 0; $i--) { if ($lines[$i] -eq '{') { $start = $i; break } }
    if ($start -lt 0) {
        $script:ConfWhy[$Provider] = 'a conformidade não devolveu o resultado: ' + (Tail $r.Out 5)
        return $null
    }
    try {
        $obj = ($lines[$start..($lines.Count - 1)] -join "`n") | ConvertFrom-Json
    } catch {
        $script:ConfWhy[$Provider] = 'resultado ilegível: ' + $_.Exception.Message
        return $null
    }
    Save-Detail "conformidade-$short.json" ($lines[$start..($lines.Count - 1)] -join "`n")
    $script:ConfCache[$Provider] = $obj
    return $obj
}

function Get-StepOf($Conf, [int]$N) { return @($Conf.steps | Where-Object { $_.step -eq $N }) | Select-Object -First 1 }

function Format-Step($Conf, [int]$N) {
    $s = Get-StepOf $Conf $N
    if (-not $s) { return "$N`:?" }
    $o = switch ($s.outcome) { 'passed' { 'ok' } 'failed' { 'FALHOU' } 'not_applicable' { 'n/a' } default { [string]$s.outcome } }
    return "$N`:$o"
}

function Add-StepSub([int]$Item, [string]$Name, $Conf, [int[]]$Steps, [switch]$Info) {
    $parts = @(); $fail = $false; $notes = @()
    foreach ($n in $Steps) {
        $s = Get-StepOf $Conf $n
        $parts += (Format-Step $Conf $n)
        if (-not $s -or $s.outcome -eq 'failed') {
            $fail = $true
            if ($s -and $s.reason) { $notes += ("etapa $n`: " + $s.reason) }
        }
    }
    $st = 'PASS'; if ($fail) { $st = 'FAIL' }
    Add-Sub $Item $st ("$Name, etapas " + ($parts -join ' ') + $(if ($notes) { "`n    " + ($notes -join "`n    ") } else { '' }))
}

function Add-NoConf([int]$Item, [string]$Provider, [string]$What) {
    $why = $script:ConfWhy[$Provider]
    if (-not $why) { $why = 'sem resultado' }
    Add-Sub $Item 'SKIP' "$What ($Provider): $why"
}

# ---------------------------------------------------------------- item 1

function Test-Item1 {
    # The lines the adapters write, run by the real shells.
    if (Get-BuiltBinaries) {
        $r = Invoke-Native -File 'go' -Arguments @('test', '-tags', 'full', '-run', 'HookLine', '-count=1', '-v', './internal/providers') -Dir $Repo -TimeoutSec 900
        Save-Detail 'item1-gotest.log' $r.Out
        # This test runs the Windows line in the bash of Linux: on Windows it skips by
        # design, and TestHookLineInTheShellOfTheSystem runs the line here.
        $skipsByDesign = @('TestWindowsHookLineTakesABackslashMestreCLI')
        $skips = @([regex]::Matches($r.Out, '--- SKIP: (\S+)') | ForEach-Object { $_.Groups[1].Value } | Where-Object { $skipsByDesign -notcontains $_ })
        $ok = ($r.Exit -eq 0 -and $r.Out -match '--- PASS: TestCodexHookLineInTheShellOfCodex' -and $skips.Count -eq 0)
        if ($ok) { Add-Sub 1 'PASS' 'go test das linhas reais (Codex no PowerShell 5.1, Claude no shell do sistema) passou, sem SKIP fora dos de Linux' }
        else { Add-Sub 1 'FAIL' ('go test das linhas dos hooks: ' + (Tail $r.Out 8)) }
    } else { Add-Sub 1 'SKIP' ('go test das linhas dos hooks: ' + $script:BuildWhy) }

    # The same line of Codex in Windows PowerShell and in PowerShell 7, and the
    # line of Claude in Git Bash, calling a mestre.exe that blocks (exit 2),
    # from a path with a blank.
    $d = Join-Path $Work 'hookline\with space'
    New-Item -ItemType Directory -Force -Path $d | Out-Null
    Set-Content -LiteralPath (Join-Path $d 'stub.go') -Encoding ASCII -Value @'
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Fprintln(os.Stderr, "stub: "+strings.Join(os.Args[1:], " "))
	os.Exit(2)
}
'@
    $stub = Join-Path $d 'mestre.exe'
    $b = Invoke-Native -File 'go' -Arguments @('build', '-o', $stub, (Join-Path $d 'stub.go')) -Dir $d -TimeoutSec 300
    if ($b.Exit -ne 0) { Add-Sub 1 'SKIP' ('stub do mestre.exe não compilou: ' + (Tail $b.Out 3)); return }
    $psLine = '& "' + $stub + '" hook --provider codex --event PreToolUse; exit $LASTEXITCODE'
    $hosts = @(, @('PowerShell 5.1', 'powershell.exe'))
    $p7 = $null
    try { $p7 = Get-Pwsh7 } catch { Add-Sub 1 'SKIP' ('PowerShell 7 indisponível: ' + $_.Exception.Message) }
    if ($p7) { $hosts += , @('PowerShell 7', $p7) }
    foreach ($h in $hosts) {
        $r = Invoke-Native -File $h[1] -Arguments @('-NoProfile', '-Command', $psLine) -TimeoutSec 60
        if ($r.Exit -eq 2) { Add-Sub 1 'PASS' ("linha do Codex no " + $h[0] + " chama o mestre.exe (caminho com espaço) e repassa o código 2") }
        else { Add-Sub 1 'FAIL' ("linha do Codex no " + $h[0] + ": saiu com " + $r.Exit + " (esperado 2): " + (Tail $r.Out 3)) }
    }
    $bash = 'C:\Program Files\Git\bin\bash.exe'
    if (Test-Path $bash) {
        $slash = $stub -replace '\\', '/'
        $line = '[ -n "$MESTRE_AGENT_ID" ] && command -v "' + $slash + '" >/dev/null 2>&1 && exec "' + $slash + '" hook --provider claude-code --event PreToolUse; exit 0'
        $r = Invoke-Native -File $bash -Arguments @('-c', $line) -Env @{ MESTRE_AGENT_ID = 'rodada' } -TimeoutSec 60
        if ($r.Exit -eq 2) { Add-Sub 1 'PASS' 'linha do Claude no Git Bash (barras normais, caminho com espaço) repassa o código 2' }
        else { Add-Sub 1 'FAIL' ('linha do Claude no Git Bash: saiu com ' + $r.Exit + ' (esperado 2): ' + (Tail $r.Out 3)) }
    } else { Add-Sub 1 'SKIP' 'Git Bash não encontrado em C:\Program Files\Git\bin\bash.exe' }

    # The real CLIs: the hooks must fire (steps 4 to 6 fail when they do not).
    foreach ($p in @('claude-code', 'codex')) {
        $c = Get-Conformance $p
        if ($c) { Add-StepSub 1 "CLI real $p, hooks na conformidade" $c @(4, 5, 6) } else { Add-NoConf 1 $p 'hooks do CLI real' }
    }
}

# ---------------------------------------------------------------- item 2

$SocketServer = @'
param($Path, $Out)
$ErrorActionPreference = 'Stop'
$lines = New-Object System.Collections.ArrayList
# The chain of parents, from one snapshot taken while the caller still lives.
function Get-Chain($procId) {
    $all = @{}
    foreach ($p in @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue)) { $all[[int]$p.ProcessId] = $p }
    $chain = @(); $cur = [int]$procId
    for ($i = 0; $i -lt 8 -and $all.ContainsKey($cur); $i++) { $chain += $all[$cur]; $cur = [int]$all[$cur].ParentProcessId }
    return , $chain
}
function Describe($chain) {
    foreach ($p in $chain) {
        $o = Invoke-CimMethod -InputObject $p -MethodName GetOwner -ErrorAction SilentlyContinue
        [void]$lines.Add(('  {0}({1}) dono={2}\{3}' -f $p.Name, $p.ProcessId, $o.Domain, $o.User))
    }
}
if (Test-Path $Path) { Remove-Item -Force $Path }
$s = [System.Net.Sockets.Socket]::new([System.Net.Sockets.AddressFamily]::Unix, [System.Net.Sockets.SocketType]::Stream, [System.Net.Sockets.ProtocolType]::Unspecified)
$s.Bind([System.Net.Sockets.UnixDomainSocketEndPoint]::new($Path))
$s.Listen(8)
# The answer for "no connection" is there from the start: the script stops the
# server when Codex ends, long before the deadline below.
Set-Content -LiteralPath $Out -Value 'conectou=nao'
Set-Content -LiteralPath "$Out.ready" -Value 'ok'
$deadline = (Get-Date).AddMinutes(9)
$got = $false
while ((Get-Date) -lt $deadline -and -not $got) {
    if (-not $s.Poll(1000000, [System.Net.Sockets.SelectMode]::SelectRead)) { continue }
    $c = $s.Accept()
    $got = $true
    $o = New-Object byte[] 4
    $peer = -1
    try { [void]$c.IOControl(0x58000100, [byte[]]::new(0), $o); $peer = [BitConverter]::ToInt32($o, 0) } catch { [void]$lines.Add('GETPEERPID: ' + $_.Exception.Message) }
    [void]$lines.Add("conectou=sim pid=$peer")
    Set-Content -LiteralPath $Out -Value $lines
    $buf = New-Object byte[] 4096
    try { [void]$c.Receive($buf) } catch { }
    $chain = @(); if ($peer -gt 0) { $chain = Get-Chain $peer }
    # Answer before the owners (one CIM call per process): that can take long
    # enough for the command of the sandbox to give up.
    $resp = [Text.Encoding]::ASCII.GetBytes("HTTP/1.1 200 OK`r`nContent-Length: 2`r`nConnection: close`r`n`r`nok")
    [void]$c.Send($resp)
    $c.Close()
    Describe $chain
}
if (-not $got) { [void]$lines.Add('conectou=nao') }
Set-Content -LiteralPath $Out -Value $lines
'@

# CA-CODEX-WIN-05: the isolation between two Codex characters of the same
# machine in the unelevated mode (docs/provedores/codex-no-windows.md). Two
# Codex run, each in its own logon session (a scheduled task of this user with
# S4U makes a new one per run) and in its own worktree. The command B runs
# holds a process; the command A runs tries to open it, to write in the worktree
# of B and outside any workspace, and to write in its own. What the DACL of the
# command of B and the sandbox of A answer is what the engine will rely on.
# The connect() of the CLI to the socket of the engine and the ACE of the
# terminal are the part of the real accounts (tools/realaccount), which makes
# the launcher and the engine.
function Test-CodexCrossIsolation {
    if (-not (Initialize-Provider 'codex')) { Add-Sub 2 'SKIP' ('isolamento entre dois Codex: ' + $script:ConfWhy['ready-codex']); return }
    $x = Join-Path $Work 'cx'
    $wa = Join-Path $x 'wa'; $wb = Join-Path $x 'wb'; $probe = Join-Path $x 'probe'; $out = Join-Path $x 'out'
    if (Test-Path $x) { Remove-Item -Recurse -Force $x -ErrorAction SilentlyContinue }
    if (Test-Path $x) { Add-Sub 2 'FAIL' 'isolamento entre dois Codex: não consegui limpar C:\rodada\cx de uma rodada anterior'; return }
    New-Item -ItemType Directory -Force -Path $wa, $wb, $probe, $out | Out-Null
    $codex = Get-CliPath 'codex'
    $home7 = (Get-CliEnv 'codex').CODEX_HOME
    $envLines = @("`$env:CODEX_HOME = '$home7'", "`$env:USERPROFILE = '$env:USERPROFILE'", "`$env:LOCALAPPDATA = '$env:LOCALAPPDATA'", "`$env:APPDATA = '$env:APPDATA'", "`$env:TEMP = '$env:TEMP'", "`$env:TMP = '$env:TEMP'")
    Set-Content -LiteralPath (Join-Path $probe 'hold.ps1') -Encoding UTF8 -Value @"
`$PID | Set-Content -LiteralPath '$wb\bpid.txt'
Start-Sleep -Seconds 240
"@
    Set-Content -LiteralPath (Join-Path $probe 'attack.ps1') -Encoding UTF8 -Value @"
param([int]`$Target)
function Probe(`$name, [scriptblock]`$b) {
    try { & `$b | Out-Null; "`$name=ok" }
    catch {
        `$e = `$_.Exception
        while (`$e.InnerException) { `$e = `$e.InnerException }
        if (`$e -is [ArgumentException]) { "`$name=ausente" }
        elseif (`$e -is [ComponentModel.Win32Exception] -and `$e.NativeErrorCode -eq 5) { "`$name=negado" }
        elseif (`$e -is [UnauthorizedAccessException]) { "`$name=negado" }
        else { "`$name=erro" }
    }
}
Probe 'OPEN' { `$p = [Diagnostics.Process]::GetProcessById(`$Target); `$null = `$p.StartTime }
Probe 'HANDLE' { `$p = [Diagnostics.Process]::GetProcessById(`$Target); `$null = `$p.Handle }
Probe 'WB' { Set-Content -LiteralPath '$wb\x.txt' -Value 'x' -ErrorAction Stop }
Probe 'FORA' { Set-Content -LiteralPath '$x\fora.txt' -Value 'x' -ErrorAction Stop }
Probe 'WA' { Set-Content -LiteralPath '$wa\own.txt' -Value 'x' -ErrorAction Stop }
'FIM'
"@
    function New-CodexRun([string]$Name, [string]$Dir, [string]$Command) {
        $prompt = "Use a ferramenta de shell para rodar exatamente este comando e mostre so a saida dele, sem comentar: $Command"
        $body = ($envLines -join "`r`n") + "`r`n" + @"
Set-Location -LiteralPath '$Dir'
((whoami.exe /groups) -join ' ') -match 'S-1-5-5-\d+-\d+' | Out-Null
`$Matches[0] | Set-Content -LiteralPath '$out\$Name.sid'
& '$codex' exec --skip-git-repo-check -s workspace-write -c sandbox_workspace_write.network_access=true -c 'windows.sandbox="unelevated"' '$prompt' *> '$out\$Name.log'
'' | Set-Content -LiteralPath '$out\$Name.done'
"@
        Set-Content -LiteralPath (Join-Path $probe "$Name.ps1") -Value $body -Encoding UTF8
    }
    New-CodexRun 'b' $wb ("powershell.exe -NoProfile -ExecutionPolicy Bypass -File $probe\hold.ps1")
    $tasks = @()
    $user = "$env:USERDOMAIN\$env:USERNAME"
    function Start-AsOwnLogon([string]$Name) {
        $act = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$probe\$Name.ps1`""
        $pri = New-ScheduledTaskPrincipal -UserId $user -LogonType S4U -RunLevel Limited
        $set = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
        Register-ScheduledTask -TaskName "MestreRodadaCx$Name" -Action $act -Principal $pri -Settings $set -Force | Out-Null
        Start-ScheduledTask -TaskName "MestreRodadaCx$Name"
    }
    try {
        $tasks += 'MestreRodadaCxb'; Start-AsOwnLogon 'b'
        $t0 = Get-Date
        $bpid = 0
        while ($bpid -le 0 -and ((Get-Date) - $t0).TotalSeconds -lt 300) {
            Start-Sleep -Seconds 2
            if (Test-Path "$wb\bpid.txt") { $bpid = [int](('0' + ((Get-Content -LiteralPath "$wb\bpid.txt" -Raw) -replace '\D', ''))) }
        }
        if ($bpid -le 0) { Add-Sub 2 'FAIL' ('isolamento entre dois Codex: o comando do Codex B não segurou o processo. Saída:' + "`n" + (Tail (Get-Content -LiteralPath "$out\b.log" -Raw -ErrorAction SilentlyContinue) 8)); return }
        if (-not (Get-Process -Id $bpid -ErrorAction SilentlyContinue)) { Add-Sub 2 'FAIL' 'isolamento entre dois Codex: o processo do comando de B já tinha acabado antes de A começar'; return }
        New-CodexRun 'a' $wa ("powershell.exe -NoProfile -ExecutionPolicy Bypass -File $probe\attack.ps1 -Target $bpid")
        $tasks += 'MestreRodadaCxa'; Start-AsOwnLogon 'a'
        $t0 = Get-Date
        while (-not (Test-Path "$out\a.done") -and ((Get-Date) - $t0).TotalSeconds -lt 420) { Start-Sleep -Seconds 2 }
        $log = if (Test-Path "$out\a.log") { Get-Content -LiteralPath "$out\a.log" -Raw } else { '' }
        Save-Detail 'item2-cx-a.log' $log
        Save-Detail 'item2-cx-b.log' (Get-Content -LiteralPath "$out\b.log" -Raw -ErrorAction SilentlyContinue)
        $want = [ordered]@{ 'OPEN' = 'negado'; 'HANDLE' = 'negado'; 'WB' = 'negado'; 'FORA' = 'negado'; 'WA' = 'ok' }
        $bad = @()
        $sidA = (Get-Content -LiteralPath "$out\a.sid" -ErrorAction SilentlyContinue | Select-Object -First 1)
        $sidB = (Get-Content -LiteralPath "$out\b.sid" -ErrorAction SilentlyContinue | Select-Object -First 1)
        if (-not $sidA -or -not $sidB) { $bad += 'não li o SID de logon de uma das tarefas' }
        elseif ($sidA -eq $sidB) { Add-Sub 2 'SKIP' "isolamento entre dois Codex: as duas tarefas caíram na mesma sessão de logon ($sidA); o cenário não foi medido"; return }
        if (-not (Get-Process -Id $bpid -ErrorAction SilentlyContinue)) { $bad += 'o processo de B acabou durante a sonda (o resultado OPEN não vale)' }
        foreach ($k in $want.Keys) {
            if ($log -match "(?m)^$k=(ok|negado)") {
                if ($Matches[1] -ne $want[$k]) { $bad += "$k`: obtido $($Matches[1]), esperado $($want[$k])" }
            } else { $bad += "$k`: sem resultado" }
        }
        if ($bad.Count -eq 0) { Add-Sub 2 'PASS' "isolamento entre dois Codex (unelevated, logons $sidA e $sidB): o comando de A não abriu o processo do comando de B, não escreveu no worktree de B nem fora de um workspace, e escreveu no próprio" }
        else { Add-Sub 2 'FAIL' ("isolamento entre dois Codex diverge:`n    " + ($bad -join "`n    ") + "`n  fim da saída do Codex A:`n" + (Tail $log 8)) }
    } finally {
        foreach ($t in $tasks) { Stop-ScheduledTask -TaskName $t -ErrorAction SilentlyContinue; Unregister-ScheduledTask -TaskName $t -Confirm:$false -ErrorAction SilentlyContinue }
        if ($bpid -gt 0) { & taskkill.exe /T /F /PID $bpid 2>&1 | Out-Null }
    }
}

function Test-Item2 {
    $c = Get-Conformance 'codex'
    if ($c) {
        $s5 = Get-StepOf $c 5
        $note = @($s5.notes | Where-Object { $_ -match 'mestre debug identificado' }) | Select-Object -First 1
        if ($note -match 'identificado com o terminal: true') { Add-Sub 2 'PASS' "conformidade do Codex (classe sem internet), etapa 5: $note" }
        else { Add-Sub 2 'FAIL' ("conformidade do Codex, etapa 5: " + $(if ($note) { $note } else { 'sem a nota do mestre debug' }) + " (resultado da etapa: " + (Format-Step $c 5) + ")") }
    } else { Add-NoConf 2 'codex' 'conformidade do Codex' }

    # A direct probe: the shell of a Codex in the unelevated mode (the one the engine uses) reaches a
    # Unix socket of ours, and who the server sees at the other end.
    if (-not (Initialize-Provider 'codex')) { Add-Sub 2 'SKIP' ('sonda direta do socket: ' + $script:ConfWhy['ready-codex']); return }
    $p7 = $null
    try { $p7 = Get-Pwsh7 } catch { Add-Sub 2 'SKIP' ('sonda direta: PowerShell 7 indisponível: ' + $_.Exception.Message); return }
    $dir = Join-Path $Work 'sock'
    if (Test-Path $dir) { Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue }
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    # What the engine does for a terminal: the logon SID of this session with read
    # and write on the socket file and read and execute on the folder (icacls does
    # not take that SID, so .NET).
    $logon = ((whoami.exe /groups) -join ' ') -match 'S-1-5-5-\d+-\d+'
    if (-not $logon) { Add-Sub 2 'SKIP' 'sonda direta do socket: não achei o SID de logon desta sessão'; return }
    $logonSid = New-Object Security.Principal.SecurityIdentifier $Matches[0]
    $dacl = Get-Acl -LiteralPath $dir
    $dacl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($logonSid, 'ReadAndExecute', 'None', 'None', 'Allow')))
    $dacl.AddAccessRule((New-Object Security.AccessControl.FileSystemAccessRule($logonSid, 'Read, Write', 'ObjectInherit', 'InheritOnly', 'Allow')))
    Set-Acl -LiteralPath $dir -AclObject $dacl
    $work = Join-Path $Work 'sockwork'
    if (Test-Path $work) { Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue }
    New-Item -ItemType Directory -Force -Path $work | Out-Null
    Set-Content -LiteralPath (Join-Path $dir 'server.ps1') -Value $SocketServer -Encoding UTF8
    # The socket outside the writable roots (where the cli.sock of the engine is),
    # then inside the workdir: the second tells an ACL that keeps the sandbox
    # users out from a sandbox that blocks every AF_UNIX connect.
    $where = @(@{ Dir = $dir; Name = 'fora das pastas graváveis'; Log = 'item2-codex-exec.log' },
               @{ Dir = $work; Name = 'dentro do workdir (pasta gravável do sandbox)'; Log = 'item2-codex-exec-workdir.log' })
    foreach ($w in $where) {
        $sock = Join-Path $w.Dir 'p.sock'
        $res = Join-Path $w.Dir 'peer.txt'
        Remove-Item -Force $sock, $res, "$res.ready" -ErrorAction SilentlyContinue
        $srv = Start-Process -FilePath $p7 -ArgumentList @('-NoProfile', '-File', (Join-Path $dir 'server.ps1'), $sock, $res) -PassThru -WindowStyle Hidden
        $t0 = Get-Date
        while (-not (Test-Path "$res.ready") -and ((Get-Date) - $t0).TotalSeconds -lt 30) { Start-Sleep -Milliseconds 300 }
        if (-not (Test-Path "$res.ready")) { Add-Sub 2 'SKIP' "o servidor do socket ($($w.Name)) não subiu"; if (-not $srv.HasExited) { $srv.Kill() }; continue }
        $prompt = "Use a ferramenta de shell para rodar exatamente este comando e mostre só a saída dele, sem comentar: curl.exe --silent --show-error --unix-socket $sock http://localhost/ping; echo EXIT=`$LASTEXITCODE; whoami"
        $r = Invoke-Native -File (Get-CliPath 'codex') -Env (Get-CliEnv 'codex') -Dir $work -TimeoutSec 420 -Arguments @(
            'exec', '--skip-git-repo-check', '-s', 'workspace-write', '-c', 'sandbox_workspace_write.network_access=true', '-c', 'windows.sandbox="unelevated"', $prompt)
        Save-Detail $w.Log $r.Out
        # A connection ends the server after it describes the caller; without one it waits.
        [void]$srv.WaitForExit($(if ((Get-Content -LiteralPath $res -ErrorAction SilentlyContinue) -match 'conectou=sim') { 30000 } else { 5000 }))
        if (-not $srv.HasExited) { $srv.Kill() }
        $peer = ''
        if (Test-Path $res) { $peer = (Get-Content -LiteralPath $res) -join "`n" }
        $curl = (@($r.Out -split "`r?`n" | Where-Object { $_ -match '^(curl: |EXIT=|\S+\\codexsandbox)' } | Select-Object -Unique) -join "`n    ")
        if ($peer -match 'conectou=sim') {
            $chainsToCodex = ($peer -match 'codex')
            Add-Sub 2 'PASS' ("sandbox do Codex (modo unelevated), socket $($w.Name): ALCANÇOU. Quem o servidor viu (PID, dono e cadeia de pais):`n" + $peer + "`n  cadeia chega a um processo 'codex': " + $(if ($chainsToCodex) { 'sim' } else { 'não (a identificação tem de ser pelo Job Object do terminal)' }))
        } elseif ($peer -match 'conectou=nao') {
            Add-Sub 2 'FAIL' ("sandbox do Codex (modo unelevated), socket $($w.Name): NÃO alcançou. O que o curl disse:`n    " + $(if ($curl) { $curl } else { Tail $r.Out 8 }))
        } else {
            Add-Sub 2 'FAIL' ("sonda do socket ($($w.Name)) sem resultado. Fim da saída do codex:`n" + (Tail $r.Out 8))
        }
    }
    Test-CodexCrossIsolation
}

# ---------------------------------------------------------------- item 3

function Get-ToolNames($Conf) {
    $names = @()
    foreach ($n in 4, 5, 6) {
        $s = Get-StepOf $Conf $n
        foreach ($note in @($s.notes)) { if ($note -match '^pre_tool (\S+) \(') { $names += [pscustomobject]@{ Step = $n; Name = $Matches[1] } } }
    }
    return $names
}

function Test-Item3 {
    $known = @{ 'claude-code' = @('Bash', 'PowerShell'); 'codex' = @('Bash'); 'antigravity' = @('run_command') }
    foreach ($p in @('claude-code', 'codex', 'antigravity')) {
        $c = Get-Conformance $p
        if (-not $c) { Add-NoConf 3 $p 'nome da ferramenta de shell'; continue }
        $shellNames = @(Get-ToolNames $c | Where-Object { $_.Step -in 5, 6 } | ForEach-Object { $_.Name } | Select-Object -Unique)
        $all = @(Get-ToolNames $c | ForEach-Object { $_.Name } | Select-Object -Unique)
        if ($shellNames.Count -eq 0) { Add-Sub 3 'FAIL' "$p`: nenhuma pre_tool nas etapas 5 e 6 (ferramentas vistas: $($all -join ', '))"; continue }
        $unknown = @($shellNames | Where-Object { $known[$p] -notcontains $_ })
        if ($unknown.Count -eq 0) { Add-Sub 3 'PASS' ("$p`: ferramenta de shell no Windows = " + ($shellNames -join ', ') + " (já mapeada). Todas as vistas: " + ($all -join ', ')) }
        else { Add-Sub 3 'FAIL' ("$p`: nome(s) NOVO(S) de ferramenta de shell no Windows = " + ($unknown -join ', ') + " (mapeadas hoje: " + ($known[$p] -join ', ') + "). Todas as vistas: " + ($all -join ', ')) }
    }
}

# ---------------------------------------------------------------- item 4

function Test-Item4 {
    if (-not (Initialize-Provider 'antigravity')) { Add-Sub 4 'SKIP' ('agy: ' + $script:ConfWhy['ready-antigravity']); return }
    $agy = Get-CliPath 'antigravity'
    $e = Get-CliEnv 'antigravity'
    # Only the names of the credentials are listed, never the user or the secret.
    $targets = @(& cmdkey.exe /list 2>&1 | Where-Object { $_ -match '(?i)^\s*(Target|Destino|Alvo)\s*:' -and $_ -match '(?i)antigravity|gemini' } | ForEach-Object { ($_ -replace '\s+', ' ').Trim() })
    if ($targets.Count -gt 0) { Add-Sub 4 'PASS' ('Gerenciador de Credenciais tem a credencial do agy: ' + ($targets -join '; ')) }
    else { Add-Sub 4 'FAIL' 'Gerenciador de Credenciais sem credencial gemini:antigravity depois do login do agy' }
    $u = Invoke-Native -File $agy -Arguments @('-p', '/usage', '--output-format', 'json') -Env $e -TimeoutSec 90
    if ($u.Exit -eq 0 -and $u.Out -match '"buckets"') { Add-Sub 4 'PASS' 'agy -p /usage acha o login (sem sandbox)' } else { Add-Sub 4 'FAIL' ('agy -p /usage: ' + (Tail $u.Out 3)) }
    # --sandbox (AppContainer): runs a command of the worktree and keeps the login.
    $w = Join-Path $Work 'agywork'
    New-Item -ItemType Directory -Force -Path $w | Out-Null
    & git -C $w init -q 2>&1 | Out-Null
    # -p cannot ask for the "command" permission and denies it; an allow rule
    # for this command alone stands in for the answer, only for this probe
    # (--dangerously-skip-permissions would undo --sandbox, docs/provedores/antigravity.md).
    $settings = Join-Path (Get-ProfileDir 'antigravity') '.gemini\antigravity-cli\settings.json'
    $before = $null
    if (Test-Path $settings) { $before = [IO.File]::ReadAllBytes($settings) }
    New-Item -ItemType Directory -Force -Path (Split-Path $settings) | Out-Null
    Set-Content -LiteralPath $settings -Encoding ASCII -Value '{"permissions":{"allow":["command(git --version)"]}}'
    try {
        $r = Invoke-Native -File $agy -Env $e -Dir $w -TimeoutSec 240 -Arguments @('--sandbox', '-p', 'Rode o comando git --version no shell e responda só com a saída dele.')
    } finally {
        if ($before) { [IO.File]::WriteAllBytes($settings, $before) } else { Remove-Item -LiteralPath $settings -Force -ErrorAction SilentlyContinue }
    }
    Save-Detail 'item4-agy-sandbox.log' $r.Out
    if ($r.Out -match 'git version') { Add-Sub 4 'PASS' 'agy --sandbox rodou git --version no worktree (AppContainer deixou rodar comando)' }
    else { Add-Sub 4 'FAIL' ('agy --sandbox não rodou o comando (código ' + $r.Exit + $(if ($r.TimedOut) { ', estourou o prazo' } else { '' }) + '). Fim da saída:' + "`n" + (Tail $r.Out 6)) }
    $u2 = Invoke-Native -File $agy -Arguments @('-p', '/usage', '--output-format', 'json') -Env $e -TimeoutSec 90
    if ($u2.Exit -eq 0 -and $u2.Out -match '"buckets"') { Add-Sub 4 'PASS' 'depois do --sandbox o agy ainda acha o login' } else { Add-Sub 4 'FAIL' ('depois do --sandbox o agy perdeu o login: ' + (Tail $u2.Out 3)) }
    $c = Get-Conformance 'antigravity'
    if ($c) { Add-StepSub 4 'conformidade do agy (classe sem internet: --sandbox na base)' $c @(1, 4, 5, 6) } else { Add-NoConf 4 'antigravity' 'conformidade do agy' }
}

# ---------------------------------------------------------------- item 5

function Test-Item5 {
    foreach ($p in @('claude-code', 'codex', 'antigravity')) {
        $c = Get-Conformance $p
        if (-not $c) { Add-NoConf 5 $p 'hooks em subagente nativo'; continue }
        $s = Get-StepOf $c 12
        $deny = ''; if ($c.flags -and $c.flags.deny_subagent) { $deny = ' (flag deny_subagent LIGADA: a classe passa a negar o subagente nessa máquina)' }
        if ($s -and $s.outcome -eq 'passed') { Add-Sub 5 'PASS' "$p`: etapa 12 ok, o hook disparou dentro do subagente com o MESTRE_AGENT_ID do personagem$deny" }
        elseif ($s) { Add-Sub 5 'FAIL' ("$p`: etapa 12 " + $s.outcome + ': ' + $s.reason + $deny + "`n    " + ((@($s.notes) | Select-Object -First 4) -join "`n    ")) }
        else { Add-Sub 5 'FAIL' "$p`: sem a etapa 12 no resultado" }
    }
}

# ---------------------------------------------------------------- item 6

function Test-Item6 {
    foreach ($p in @('claude-code', 'codex', 'antigravity')) {
        $c = Get-Conformance $p
        if (-not $c) { Add-NoConf 6 $p 'conformidade'; continue }
        $steps = (1..12 | ForEach-Object { Format-Step $c $_ }) -join ' '
        $missing = ''; if ($c.missing_events) { $missing = ' eventos ausentes: ' + ($c.missing_events -join ',') }
        $st = 'PASS'; if (-not $c.conformant) { $st = 'FAIL' }
        $why = ''; if ($c.reason) { $why = ' motivo: ' + $c.reason }
        Add-Sub 6 $st ("$p $($c.version): conformante=$($c.conformant). $steps.$missing$why")
        foreach ($n in 1..12) {
            $s = Get-StepOf $c $n
            if ($s -and $s.outcome -eq 'failed') { Add-Sub 6 'FAIL' ("  $p etapa $n falhou: " + $s.reason) }
        }
    }
}

# ---------------------------------------------------------------- item 7

function Test-Item7 {
    # Native permission screens the conformance runs met are recorded by it.
    foreach ($short in 'claude', 'codex', 'agy') {
        $files = @(Get-ChildItem (Join-Path $Out "gravacoes\$short") -Recurse -Filter 'screen-dialog*' -ErrorAction SilentlyContinue)
        if ($files.Count -gt 0) {
            $first = (Get-Content -LiteralPath $files[0].FullName -Raw)
            Add-Sub 7 'PASS' ("$short`: a conformidade capturou " + $files.Count + " tela(s) de permissão nativa (arquivos em gravacoes\$short). A primeira:`n" + (Tail $first 18))
        }
    }
    New-Terminal (Join-Path $Work 'prova') @(
        "`$env:CLAUDE_CONFIG_DIR = '" + (Get-ProfileDir 'claude-code') + "'",
        "`$env:Path = `"`$env:USERPROFILE\.local\bin;`$env:Path`"",
        "Write-Host 'Pasta de prova do Claude. Rode: claude'")
    $perm = Read-PastedText 'item7-permissao-claude.txt' @"
Tela de permissão do Claude (a que pede licença para uma ferramenta).
1. Uma janela de terminal abriu em C:\rodada\prova; nela rode:  claude
   (se pedir para confiar na pasta, responda que sim).
2. Peça: crie o arquivo oi.txt com a palavra oi
3. Quando aparecer a tela de permissão, copie TODO o texto dela
   (selecione com o mouse e Ctrl+C) e cole aqui embaixo.
4. Na janela do Claude, responda Esc (nega) e saia com /exit.
"@
    if ($perm) { Add-Sub 7 'PASS' ("texto da tela de permissão do Claude (comparar com 'pedido de permissão nativo' de claude-code.md):`n" + (Tail $perm 25)) }
    else { Add-Sub 7 'SKIP' 'tela de permissão do Claude: não colada' }
    $limit = Read-PastedText 'item7-limite-claude.txt' @"
Menu do limite de uso do Claude ("Stop and wait for limit to reset" e as outras opções).
Só aparece quando o plano chega no limite; não dá para provocar. Se aparecer durante
a rodada (ou se você o vir hoje), copie o texto dele e cole aqui. Se não, deixe vazio,
salve e feche.
"@
    if ($limit) { Add-Sub 7 'PASS' ("texto do menu do limite de uso (comparar com a tabela de telas de claude-code.md):`n" + (Tail $limit 20)) }
    else { Add-Sub 7 'SKIP' 'menu do limite de uso: não apareceu (nunca visto; só no primeiro limite real)' }
}

# ---------------------------------------------------------------- item 8

function Test-Item8 {
    $p7 = $null
    try { $p7 = Get-Pwsh7 } catch { Add-Sub 8 'SKIP' ('PowerShell 7 indisponível: ' + $_.Exception.Message) }
    $LQ = [string][char]0x2018; $RQ = [string][char]0x2019; $LDQ = [string][char]0x201C; $RDQ = [string][char]0x201D
    $LOW9 = [string][char]0x201A; $REV9 = [string][char]0x201B
    $EN = [string][char]0x2013; $EM = [string][char]0x2014; $BAR = [string][char]0x2015
    $NBSP = [string][char]0x00A0; $EMSP = [string][char]0x2003; $IDSP = [string][char]0x3000; $LSEP = [string][char]0x2028; $NEL = [string][char]0x0085
    $CR = [string][char]13; $LF = [string][char]10
    $stubDir = Join-Path $Work 'gitstub'
    New-Item -ItemType Directory -Force -Path $stubDir | Out-Null
    # A git that only records its arguments: what PowerShell runs shows in the log.
    Set-Content -LiteralPath (Join-Path $stubDir 'git.cmd') -Encoding ASCII -Value "@echo off`r`necho ARGS[%*]>>`"%PROBE_LOG%`"`r`nexit /b 0`r`n"
    $SP = 'Start-Process -Wait -NoNewWindow'
    # Expect = the lines the stub must log (what the analyzer assumes runs); a
    # case with ExpectOut checks the text the code prints instead.
    $cases = @(
        @{ Id = 'aspas-simples-tipograficas'; Code = "git ${LQ}push${RQ}"; Expect = @('push') },
        @{ Id = 'aspas-duplas-tipograficas'; Code = "git ${LDQ}push${RDQ}"; Expect = @('push') },
        @{ Id = 'aspas-baixas'; Code = "git ${LOW9}push${REV9}"; Expect = @('push') },
        @{ Id = 'comando-entre-aspas-tipograficas'; Code = "${LQ}git${RQ} push"; Expect = @('push') },
        @{ Id = 'chamada-com-aspas-tipograficas'; Code = "& ${LQ}git${RQ} push"; Expect = @('push') },
        @{ Id = 'aspas-no-meio-da-palavra'; Code = "git p${LDQ}us${RDQ}h"; Expect = @('push') },
        @{ Id = 'espaco-sem-quebra'; Code = "git${NBSP}push"; Expect = @('push') },
        @{ Id = 'espaco-em'; Code = "git${EMSP}push"; Expect = @('push') },
        @{ Id = 'espaco-ideografico'; Code = "git${IDSP}push"; Expect = @('push') },
        @{ Id = 'separador-de-linha-2028'; Code = "git${LSEP}push"; Expect = @('push') },
        @{ Id = 'espaco-nel-0085'; Code = "git${NEL}push"; Expect = @('push') },
        @{ Id = 'travessao-en-parametro'; Code = "$SP git ${EN}ArgumentList push"; Expect = @('push') },
        @{ Id = 'travessao-em-parametro'; Code = "$SP git ${EM}ArgumentList push"; Expect = @('push') },
        @{ Id = 'barra-horizontal-parametro'; Code = "$SP git ${BAR}ArgumentList push"; Expect = @('push') },
        @{ Id = 'ponto-executa-como-e-comercial'; Code = '. git push'; Expect = @('push') },
        @{ Id = 'parentese-depois-de-comando-e-argumento'; Code = 'git (echo push)'; Expect = @('push') },
        @{ Id = 'parentese-com-concatenacao'; Code = "git ('pu'+'sh')"; Expect = @('push') },
        @{ Id = 'chamada-com-parentese'; Code = '& git (Write-Output push)'; Expect = @('push') },
        @{ Id = 'nome-do-comando-em-parentese'; Code = "&('git') push"; Expect = @('push') },
        @{ Id = 'ponto-com-nome-em-parentese'; Code = ".('git') push"; Expect = @('push') },
        @{ Id = 'start-process-argumentlist'; Code = "$SP git -ArgumentList push"; Expect = @('push') },
        @{ Id = 'start-process-itens-juntados-por-espaco'; Code = "$SP git -ArgumentList 'remote','add','o','https://x'"; Expect = @('remote add o https://x') },
        @{ Id = 'start-process-item-com-espaco-nao-e-citado'; Code = "$SP git -ArgumentList 'a b','c'"; Expect = @('a b c') },
        @{ Id = 'start-process-cmd-barra-c'; Code = "$SP cmd -ArgumentList '/c','git push'"; Expect = @('push') },
        @{ Id = 'cr-solto-comeca-comando'; Code = "echo hi${CR}git push --force"; Expect = @('push --force') },
        @{ Id = 'crlf-comeca-comando'; Code = "echo hi${CR}${LF}git push --force"; Expect = @('push --force') },
        @{ Id = 'lf-comeca-comando'; Code = "echo hi${LF}git push --force"; Expect = @('push --force') },
        @{ Id = 'cr-depois-de-comentario'; Code = "# nota${CR}git push --force"; Expect = @('push --force') },
        @{ Id = 'scheduledtaskaction-execute-argument'; Code = '$a = New-ScheduledTaskAction -Execute git -Argument push; "ACTION[$($a.Execute)|$($a.Arguments)]"'; ExpectOut = 'ACTION\[git\|push\]' },
        @{ Id = 'scheduledtaskaction-linha-inteira-no-execute'; Code = '$a = New-ScheduledTaskAction -Execute ''git push''; "ACTION[$($a.Execute)|$($a.Arguments)]"'; ExpectOut = 'ACTION\[git push\|\]' },
        # The binary path is read from the registry: the labels of sc.exe qc are translated (pt-BR: NOME_DO_CAMINHO_BINÁRIO).
        @{ Id = 'sc-exe-create-binpath'; Code = 'sc.exe delete rodadasvc 2>&1 | Out-Null; sc.exe create rodadasvc binPath= "git push" | Out-Null; "BIN[" + (Get-ItemProperty HKLM:\SYSTEM\CurrentControlSet\Services\rodadasvc).ImagePath + "]"; sc.exe delete rodadasvc | Out-Null'; ExpectOut = 'BIN\[git push\]' },
        @{ Id = 'sc-exe-config-binpath'; Code = 'sc.exe delete rodadasvc 2>&1 | Out-Null; sc.exe create rodadasvc binPath= "x" | Out-Null; sc.exe config rodadasvc binPath= "git push" | Out-Null; "BIN[" + (Get-ItemProperty HKLM:\SYSTEM\CurrentControlSet\Services\rodadasvc).ImagePath + "]"; sc.exe delete rodadasvc | Out-Null'; ExpectOut = 'BIN\[git push\]' },
        @{ Id = 'schtasks-create-tr'; Code = 'schtasks /delete /tn rodadatask /f 2>&1 | Out-Null; schtasks /create /tn rodadatask /tr "git push" /sc once /st 23:59 /f | Out-Null; schtasks /query /tn rodadatask /xml | Select-String ''<Command>|<Arguments>''; schtasks /delete /tn rodadatask /f | Out-Null'; ExpectOut = '(?s)git.*push' }
    )
    $hosts = @(, @('PowerShell 5.1', 'powershell.exe'))
    if ($p7) { $hosts += , @('PowerShell 7', $p7) }
    $table = New-Object System.Collections.ArrayList
    foreach ($h in $hosts) {
        $bad = @(); $stricter = @()
        foreach ($case in $cases) {
            $log = Join-Path $Work 'probe.log'
            if (Test-Path $log) { Remove-Item -Force $log }
            $enc = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($case.Code))
            $r = Invoke-Native -File $h[1] -Arguments @('-NoProfile', '-NonInteractive', '-EncodedCommand', $enc) -TimeoutSec 90 -Env @{ PATH = "$stubDir;$env:Path"; PROBE_LOG = $log }
            $logged = @()
            if (Test-Path $log) { $logged = @(Get-Content -LiteralPath $log | ForEach-Object { if ($_ -match '^ARGS\[(.*)\]\s*$') { $Matches[1] } else { $_ } }) }
            $ok = $false
            if ($case.ContainsKey('ExpectOut')) { $ok = ($r.Out -match $case.ExpectOut); $seen = (Tail $r.Out 2) -replace "`r?`n", ' / ' }
            else { $ok = (($logged -join '|') -eq ($case.Expect -join '|')); $seen = $(if ($logged.Count) { 'git recebeu: ' + ($logged -join ' | ') } else { 'git NÃO rodou' }) }
            # git not run at all where the analyzer supposes it runs: the analyzer is
            # stricter than PowerShell (safe); git run with other arguments is the divergence.
            $safe = (-not $ok -and -not $case.ContainsKey('ExpectOut') -and $logged.Count -eq 0)
            [void]$table.Add(("{0} | {1} | {2} | {3}" -f $h[0], $case.Id, $(if ($ok) { 'ok' } elseif ($safe) { 'MAIS-RIGIDO' } else { 'DIVERGE' }), $seen))
            if ($safe) { $stricter += ($case.Id + ' -> ' + $seen) }
            elseif (-not $ok) { $bad += ($case.Id + ' -> ' + $seen) }
        }
        if ($bad.Count -eq 0) { Add-Sub 8 'PASS' ($h[0] + ': os ' + $cases.Count + ' casos leem como o analisador supõe' + $(if ($stricter) { ", ou ele é mais rígido que o PowerShell (seguro):`n    " + ($stricter -join "`n    ") } else { '' })) }
        else { Add-Sub 8 'FAIL' ($h[0] + ': ' + $bad.Count + ' de ' + $cases.Count + " casos DIVERGEM do que o analisador supõe:`n    " + ($bad -join "`n    ") + $(if ($stricter) { "`n  mais rígido que o PowerShell (seguro):`n    " + ($stricter -join "`n    ") } else { '' })) }
    }
    Save-Detail 'item8-casos.txt' ($table -join "`r`n")
}

# ---------------------------------------------------------------- item 9

function Clear-RealAccountLeftovers {
    foreach ($svc in 'MestreLauncher$ci', 'Mestre$ci') { & sc.exe stop $svc 2>&1 | Out-Null; & sc.exe delete $svc 2>&1 | Out-Null }
    if (Get-LocalUser -Name 'mestre-agente-ci' -ErrorAction SilentlyContinue) { Remove-LocalUser -Name 'mestre-agente-ci' -ErrorAction SilentlyContinue }
    # The profile outlives the account: a new one would be <name>.<computer>,
    # which the tests refuse.
    Get-CimInstance Win32_UserProfile -ErrorAction SilentlyContinue | Where-Object { $_.LocalPath -like '*\mestre-agente-ci*' } | Remove-CimInstance -ErrorAction SilentlyContinue
    if (Test-Path 'C:\ProgramData\MestreCI') { Remove-Item -Recurse -Force 'C:\ProgramData\MestreCI' -ErrorAction SilentlyContinue }
}

function New-RandomPassword {
    $chars = 'abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'.ToCharArray()
    $rng = New-Object System.Security.Cryptography.RNGCryptoServiceProvider
    $b = New-Object byte[] 28
    $rng.GetBytes($b)
    return (-join ($b | ForEach-Object { $chars[$_ % $chars.Length] })) + 'a1!Z'
}

function Test-AgentFolderAcl {
    $user = 'mestre-rodada-ag'
    $root = 'C:\ProgramData\MestreRodada'
    $outDir = Join-Path $Work 'acl-out'
    $probeDir = Join-Path $Work 'acl'
    foreach ($d in $root, $outDir, $probeDir) { if (Test-Path $d) { Remove-Item -Recurse -Force $d -ErrorAction SilentlyContinue } }
    if (Get-LocalUser -Name $user -ErrorAction SilentlyContinue) { Remove-LocalUser -Name $user }
    $pw = New-RandomPassword
    New-LocalUser -Name $user -Password (ConvertTo-SecureString $pw -AsPlainText -Force) -PasswordNeverExpires -AccountNeverExpires -UserMayNotChangePassword | Out-Null
    # By SID: the name of the group is translated (pt-BR: Usuários).
    Add-LocalGroupMember -SID 'S-1-5-32-545' -Member $user
    try {
        # The tree of docs/arquitetura/persistencia.md#arquivos, with the ACL the installer will apply.
        $x = "$root\maquina\agentes\x"
        foreach ($d in "$x\tmp", "$root\maquina\fila-hook", "$root\config") { New-Item -ItemType Directory -Force -Path $d | Out-Null }
        Set-Content -LiteralPath "$x\policy.json" -Value '{}'
        Set-Content -LiteralPath "$x\env.json" -Value '{}'
        Set-Content -LiteralPath "$root\maquina\segredo.txt" -Value 'x'
        Set-Content -LiteralPath "$root\config\cofre.key" -Value 'x'
        & icacls.exe $root /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
        & icacls.exe "$x\policy.json" /grant "${user}:(R)" | Out-Null
        & icacls.exe "$x\env.json" /grant "${user}:(R)" | Out-Null
        & icacls.exe "$x\tmp" /grant "${user}:(OI)(CI)M" | Out-Null
        & icacls.exe "$root\maquina\fila-hook" /grant "${user}:(WD)" | Out-Null
        New-Item -ItemType Directory -Force -Path $outDir, $probeDir | Out-Null
        & icacls.exe $outDir /grant "${user}:(OI)(CI)M" | Out-Null
        Set-Content -LiteralPath (Join-Path $probeDir 'probe.ps1') -Encoding UTF8 -Value @"
`$Root = '$root'
# Not `$r/`$R: the names of PowerShell variables ignore case.
`$res = [ordered]@{}
function T(`$n, [scriptblock]`$b) { try { & `$b | Out-Null; `$res[`$n] = 'ok' } catch { `$res[`$n] = 'negado' } }
T 'ler policy.json' { Get-Content -LiteralPath "`$Root\maquina\agentes\x\policy.json" -ErrorAction Stop }
T 'gravar policy.json' { Set-Content -LiteralPath "`$Root\maquina\agentes\x\policy.json" -Value 'x' -ErrorAction Stop }
T 'criar arquivo em agentes\x' { New-Item -ItemType File -Path "`$Root\maquina\agentes\x\novo.txt" -ErrorAction Stop }
T 'gravar em agentes\x\tmp' { Set-Content -LiteralPath "`$Root\maquina\agentes\x\tmp\a.txt" -Value 'y' -ErrorAction Stop }
T 'listar maquina' { Get-ChildItem -LiteralPath "`$Root\maquina" -ErrorAction Stop }
T 'ler maquina\segredo.txt' { Get-Content -LiteralPath "`$Root\maquina\segredo.txt" -ErrorAction Stop }
T 'ler config\cofre.key' { Get-Content -LiteralPath "`$Root\config\cofre.key" -ErrorAction Stop }
T 'criar em fila-hook' { Set-Content -LiteralPath "`$Root\maquina\fila-hook\f1" -Value 'x' -ErrorAction Stop }
T 'listar fila-hook' { Get-ChildItem -LiteralPath "`$Root\maquina\fila-hook" -ErrorAction Stop }
T 'apagar policy.json' { Remove-Item -LiteralPath "`$Root\maquina\agentes\x\policy.json" -ErrorAction Stop }
`$res | ConvertTo-Json | Set-Content -LiteralPath '$outDir\r.json'
"@
        & icacls.exe $probeDir /grant "${user}:(OI)(CI)RX" | Out-Null
        $cred = New-Object System.Management.Automation.PSCredential("$env:COMPUTERNAME\$user", (ConvertTo-SecureString $pw -AsPlainText -Force))
        Start-Process -FilePath powershell.exe -Credential $cred -LoadUserProfile -WorkingDirectory 'C:\Windows\System32' -WindowStyle Hidden -Wait `
            -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', (Join-Path $probeDir 'probe.ps1'))
        if (-not (Test-Path "$outDir\r.json")) { Add-Sub 9 'FAIL' 'o teste de ACL como a conta de agente não devolveu resultado'; return }
        $got = Get-Content -LiteralPath "$outDir\r.json" -Raw | ConvertFrom-Json
        $want = [ordered]@{
            'ler policy.json' = 'ok'; 'gravar policy.json' = 'negado'; 'criar arquivo em agentes\x' = 'negado'; 'gravar em agentes\x\tmp' = 'ok'
            'listar maquina' = 'negado'; 'ler maquina\segredo.txt' = 'negado'; 'ler config\cofre.key' = 'negado'
            'criar em fila-hook' = 'ok'; 'listar fila-hook' = 'negado'; 'apagar policy.json' = 'negado' }
        $bad = @()
        foreach ($k in $want.Keys) { if ($got.$k -ne $want[$k]) { $bad += ("$k`: obtido " + $got.$k + ', esperado ' + $want[$k]) } }
        if ($bad.Count -eq 0) { Add-Sub 9 'PASS' 'ACL de maquina\agentes\<id>\ (a que o instalador aplicará): a conta de agente lê policy.json/env.json, escreve só em tmp, cria (sem listar) em fila-hook e não alcança o resto; sem herança de Users' }
        else { Add-Sub 9 'FAIL' ("ACL de maquina\agentes\<id>\ diverge do documento:`n    " + ($bad -join "`n    ")) }
        # accountSID: the account is the local one of this computer.
        $sid = (Get-LocalUser -Name $user).SID.Value
        $full = ([Security.Principal.NTAccount]"$env:COMPUTERNAME\$user").Translate([Security.Principal.SecurityIdentifier]).Value
        if ($full -eq $sid) { Add-Sub 9 'PASS' "<computador>\<nome> resolve para a SID da conta local ($($sid.Substring(0, [Math]::Min(18, $sid.Length)))...)" }
        else { Add-Sub 9 'FAIL' "<computador>\<nome> resolveu para outra SID que a da conta local" }
    } finally {
        Remove-LocalUser -Name $user -ErrorAction SilentlyContinue
        $prof = Get-CimInstance Win32_UserProfile -ErrorAction SilentlyContinue | Where-Object { $_.LocalPath -like "*\$user" }
        if ($prof) { $prof | Remove-CimInstance -ErrorAction SilentlyContinue }
        foreach ($d in $root, $outDir, $probeDir) { Remove-Item -Recurse -Force $d -ErrorAction SilentlyContinue }
    }
}

function Test-Item9 {
    Add-Sub 9 'PASS' 'setgid: n/a no Windows (não existe; o motor não aplica modo nenhum lá e o instalador aplica a ACL, testada abaixo)'
    try { Test-AgentFolderAcl } catch { Add-Sub 9 'FAIL' ('ACL de agentes\<id>\: ' + $_.Exception.Message) }
    if (Get-BuiltBinaries) {
        $r = Invoke-Native -File 'go' -Arguments @('test', '-count=1', './internal/winsvc') -Dir $Repo -TimeoutSec 900
        Save-Detail 'item9-winsvc.log' $r.Out
        if ($r.Exit -eq 0) { Add-Sub 9 'PASS' 'go test ./internal/winsvc (ACLs do socket e da pasta pty, accountSID) passou no Windows real' }
        else { Add-Sub 9 'FAIL' ('go test ./internal/winsvc: ' + (Tail $r.Out 8)) }
        Clear-RealAccountLeftovers
        Write-Host 'Rodando tools/realaccount (cria a conta mestre-agente-ci e os serviços, e remove tudo)...'
        $r = Invoke-Native -File 'go' -Arguments @('run', './tools/realaccount', 'run', '-budget', '20m', '-timeout', '40m') -Dir $Repo -Env @{ MESTRE_REAL_ACCOUNT = '1'; CGO_ENABLED = '0' } -TimeoutSec 3000
        Save-Detail 'item9-realaccount.log' $r.Out
        if ($r.Out -match '(?m)^PASS realaccount') { Add-Sub 9 'PASS' ('contas reais: ' + ((Tail $r.Out 1) -replace '\s+', ' ')) }
        else { Add-Sub 9 'FAIL' ("contas reais:`n" + (Tail $r.Out 10)) }
        Clear-RealAccountLeftovers
    } else { Add-Sub 9 'SKIP' ('go test e contas reais: ' + $script:BuildWhy) }
}

# ---------------------------------------------------------------- run

Update-Path
Write-Host ''
Write-Host 'Rodada de verificações reais no Windows (M3/M4). Isto leva de 30 a 90 minutos e gasta poucos turnos de cota real.' -ForegroundColor Green
Write-Host "Resultado em: $Out\RESULTADO.txt" -ForegroundColor Green

# The logins of the Owner come first, so that what follows can run unattended
# (the only pause left is the paste of item 7, at the end).
if (@(1..7 | Where-Object { Test-Wanted $_ }).Count -gt 0) {
    foreach ($p in 'claude-code', 'codex', 'antigravity') {
        Write-Host ("--- preparando " + $p)
        if (-not (Initialize-Provider $p)) { Write-Host ("    " + $script:ConfWhy["ready-$p"]) }
    }
}

# Items run in the order that lets the conformance (the slowest) be asked once.
Invoke-Check 8 { Test-Item8 }
Invoke-Check 1 { Test-Item1 }
Invoke-Check 6 { Test-Item6 }
Invoke-Check 2 { Test-Item2 }
Invoke-Check 3 { Test-Item3 }
Invoke-Check 4 { Test-Item4 }
Invoke-Check 5 { Test-Item5 }
Invoke-Check 9 { Test-Item9 }
Invoke-Check 7 { Test-Item7 }

# ---------------------------------------------------------------- result

function Get-ItemStatus([int]$Item) {
    $subs = @($script:Subs[$Item])
    if ($subs.Count -eq 0) { return 'SKIP' }
    if (@($subs | Where-Object { $_.Status -eq 'FAIL' }).Count -gt 0) { return 'FAIL' }
    if (@($subs | Where-Object { $_.Status -eq 'PASS' }).Count -gt 0) { return 'PASS' }
    return 'SKIP'
}

$os = Get-CimInstance Win32_OperatingSystem -ErrorAction SilentlyContinue
$ps5 = $PSVersionTable.PSVersion.ToString()
$ps7v = ''
if ($script:Pwsh7) { $ps7v = $Pwsh7Version }
$goV = (Invoke-Native -File 'go' -Arguments @('version') -TimeoutSec 30).Out.Trim()
$gitV = (Invoke-Native -File 'git' -Arguments @('--version') -TimeoutSec 30).Out.Trim()
$branch = (Invoke-Native -File 'git' -Arguments @('rev-parse', '--abbrev-ref', 'HEAD') -Dir $Repo -TimeoutSec 30).Out.Trim()
$commit = (Invoke-Native -File 'git' -Arguments @('rev-parse', '--short', 'HEAD') -Dir $Repo -TimeoutSec 30).Out.Trim()

$sb = New-Object System.Text.StringBuilder
[void]$sb.AppendLine('RODADA WINDOWS (M3/M4) - ' + (Get-Date -Format 'yyyy-MM-dd HH:mm'))
if ($os) { [void]$sb.AppendLine('Windows: ' + $os.Caption + ' ' + $os.Version + ' (build ' + $os.BuildNumber + ')') }
[void]$sb.AppendLine("Windows PowerShell $ps5; PowerShell 7 $ps7v")
[void]$sb.AppendLine("$goV; $gitV; repositório $branch@$commit")
if ($script:Versions.Count) { [void]$sb.AppendLine('CLIs: ' + (($script:Versions.Keys | ForEach-Object { "$_ " + $script:Versions[$_] }) -join '; ')) }
[void]$sb.AppendLine('')
[void]$sb.AppendLine('RESUMO')
foreach ($k in 1..9) {
    $st = Get-ItemStatus $k
    if (-not (Test-Wanted $k)) { $st = 'SKIP' }
    [void]$sb.AppendLine(('  {0}  item {1}: {2}' -f $st.PadRight(4), $k, $Titles[$k]))
}
[void]$sb.AppendLine('')
[void]$sb.AppendLine('DETALHES')
foreach ($k in 1..9) {
    [void]$sb.AppendLine('')
    [void]$sb.AppendLine(('== Item {0}: {1} -> {2}' -f $k, $Titles[$k], $(if (Test-Wanted $k) { Get-ItemStatus $k } else { 'SKIP (fora do -Only)' })))
    foreach ($s in $script:Subs[$k]) { [void]$sb.AppendLine(('  [{0}] {1}' -f $s.Status, $s.Text)) }
}
[void]$sb.AppendLine('')
[void]$sb.AppendLine('Passos manuais que o script não faz: veja o README da rodada (docs/arquitetura/windows.md).')
Set-Content -LiteralPath (Join-Path $Out 'RESULTADO.txt') -Value (Protect-Text $sb.ToString()) -Encoding UTF8

Write-Host ''
Write-Host '============================================================' -ForegroundColor Green
Write-Host (Protect-Text $sb.ToString())
Write-Host '============================================================' -ForegroundColor Green
Write-Host "Cole de volta o conteúdo de $Out\RESULTADO.txt (no Windows do Dono: C:\mestre\tools\winsandbox\resultados\round\RESULTADO.txt)." -ForegroundColor Green
Stop-Transcript | Out-Null
