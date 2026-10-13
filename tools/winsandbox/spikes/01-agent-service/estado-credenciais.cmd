@echo off
rem Runs as mestre-agente: lists where logins may have been saved.
echo == whoami & whoami
echo == cmdkey /list (Gerenciador de Credenciais)
cmdkey /list
echo == Credentials (Roaming)
dir /a /b "%APPDATA%\Microsoft\Credentials" 2>nul
echo == Credentials (Local)
dir /a /b "%LOCALAPPDATA%\Microsoft\Credentials" 2>nul
echo == Vault (Local)
dir /a /s /b "%LOCALAPPDATA%\Microsoft\Vault" 2>nul
echo == Protect (chaves mestras DPAPI)
dir /a /s /b "%APPDATA%\Microsoft\Protect" 2>nul
echo == contas\p1
dir /a /s /b "C:\ProgramData\MestreSpike\work\contas\p1" 2>nul | findstr /v /i "\\projects\\ \\sessions\\ \\log\\ \\cache\\ \\tmp\\ \\statsig\\ \\shell-snapshots\\ \\todos\\ \\plugins\\ \\.cache\\"
echo == perfil: .claude .codex .gemini
dir /a /s /b "%USERPROFILE%\.claude" "%USERPROFILE%\.claude.json" "%USERPROFILE%\.codex" "%USERPROFILE%\.gemini" 2>nul
