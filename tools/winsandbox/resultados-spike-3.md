# Spikes de Windows do M1, rodada 3 — spike 5 (`mestre-pty`)

- **Data:** 2026-10-13 (relógio do Sandbox, -03:00), branch `spike/m1-windows-3` (a partir da `main`, `a37cdd5`).
- **Windows:** Windows 11 Enterprise 24H2, build 10.0.26100.9457 (Windows Sandbox, usuário WDAGUtilityAccount, administrador). Mesma sessão do Sandbox da rodada 2 (conta `mestre-agente` e login do `agy` reaproveitados; `spikesvc install` **não** foi rodado de novo, porque redefine a senha do `mestre-agente` e faria a DPAPI perder o Gerenciador de Credenciais).
- **Ferramentas:** Go 1.27.2; `golang.org/x/sys` v0.47.0 (sem mudar); `github.com/aymanbagabas/go-pty` v0.2.3 e `github.com/charmbracelet/x/vt` v0.1.0 no `go.mod` só desta branch (diretiva `go 1.25.0` mantida). CLIs como `mestre-agente`: Claude Code 2.1.296, Codex 0.162.1, agy 1.3.3.
- **Código:** `tools/winsandbox/spikes/03-mestre-pty/` (`mestrepty.exe`, também copiado como `bin\ptyhost.exe`: papéis `ptyhost`, `service` = motor, `run`/`ctl` = condutor, `helper`); `01-agent-service/startpty.go` (pedido `start-pty` do launcher) e ajustes em `service.go` (campos do pedido; o log do launcher não grava mais o corpo de pedidos com segredo) e `login.ps1` (caminhos reais dos três CLIs e modos `*-install`).
- **Evidências:** `C:\winsandbox\resultados\rodada-3\` (`clis.md`, `clis-claude.md`, `survive.md`, `hkcu.md`, `isolate.md`, `kill.md`, `ident.md`, `wmi.md`, `logs\`). Nenhum segredo nem token nos arquivos (conferido; o segredo dos terminais fica só em `C:\ProgramData\MestreSpike\no\terminals.json`, ACL do serviço e SYSTEM).

## Resumo

| Item | Resultado | Em uma linha |
|---|---|---|
| 1. Launcher `start-pty` | **Passou** | `LogonUser` em lote por terminal + `LoadUserProfile` + `CreateEnvironmentBlock`, `CREATE_BREAKAWAY_FROM_JOB`, descritor do processo, segredo no stdin herdado (lista de handles); devolve PID e horário de criação |
| 2. ptyhost (go-pty + Job + vt + socket + HMAC) | **Passou** (2 achados) | `go-pty` fecha o handle da thread: retomar com `NtResumeProcess`; o `vt` exige ler as respostas dele de volta ao ConPTY |
| 3. Motor: `start-pty`, PID do par, aperto de mão; `cli.sock` | **Passou** | `SIO_AF_UNIX_GETPEERPID` deu o PID certo nos dois sentidos (motor→ptyhost e 11/11 chamadores do `cli.sock`) |
| 4. Agente falso, `claude`, `codex`, `agy` pelo ptyhost | **Passou** | entrada, saída e modo cru (`\x03` como byte) nos quatro |
| 5. Sobrevivência (motor e launcher reiniciados) | **Passou** | mesmos PIDs, mesma tela, digitação depois da volta; `agy -p /usage` num ptyhost novo achou o login; HKCU e Gerenciador de Credenciais em ordem |
| 6. Isolamento | **Parcial: socket e segredo passaram; acesso ao processo do ptyhost não executado** | conectar, mudar a ACL e apagar o socket recusados (fora e dentro de outro terminal); segredo errado recusado |
| 7. Encerramento | **Passou** | `TerminateJobObject` e `TerminateProcess` do ptyhost deixam zero processos da árvore |
| 8. Identificação pelo Job do ptyhost | **Passou** | todos os casos de dentro acertados, os de fora e o WMI recusados; nenhum atribuído ao terminal errado |
| 9. WMI | **Passou no padrão** | o processo do WMI leva a sessão de logon do terminal; a varredura dela no fim do terminal o encerra; plano B desnecessário |

## 1. Launcher (`start-pty`) — PASSOU

- Aceita só o processo do serviço `MestreSpike` (PID do SCM) com token de `NT SERVICE\MestreSpike` (`GetNamedPipeClientProcessId` → `OpenProcessToken`).
- Por terminal: `LogonUser` em lote (sessão de logon nova a cada um: ex. `00892dca`, `008ba68b`…), `LoadUserProfile`, `CreateEnvironmentBlock`, ACE do motor (`PROCESS_QUERY_LIMITED_INFORMATION|SYNCHRONIZE`) na DACL padrão do token, SID de logon na estação de janela e área de trabalho do serviço.
- `CreateProcessAsUser` de `C:\ProgramData\MestreSpike\bin\ptyhost.exe` (linha de comando fixa `ptyhost -id <id>`), `CREATE_BREAKAWAY_FROM_JOB | DETACHED_PROCESS`, descritor do processo `D:P(A;;GA;;;SY)(A;;0x101001;;;<motor>)(A;;0x101000;;;OW)` e da thread inicial equivalente; o segredo (32 bytes) vai por um pipe anônimo cujo lado de leitura é o stdin do filho, único handle herdado (`PROC_THREAD_ATTRIBUTE_HANDLE_LIST`). Nada em argumento nem em ambiente.
- Devolve PID e horário de criação (`GetProcessTimes`); `start-pty` leva 30–260 ms. O ptyhost roda como `mestre-agente`, sessão 0, **fora de qualquer Job** (`IsProcessInJob(self, NULL)` = falso). Quando o ptyhost sai, o launcher descarrega o perfil e fecha a sessão de logon (log do launcher).
- **Achado:** o handler genérico do launcher gravava o corpo de cada pedido no log; com `start-pty`, isso gravaria o segredo. Corrigido no spike. Na implementação: o launcher nunca registra o corpo do pedido.

## 2. ptyhost — PASSOU (com achados)

- `go-pty` v0.2.3 com ConPTY na sessão 0, 200×50; Job anônimo com `KILL_ON_JOB_CLOSE` (sem `BREAKAWAY_OK`), DACL só de SYSTEM; buffer circular de 1 MiB com deslocamentos; tela no `x/vt`; socket `AF_UNIX` em `C:\ProgramData\MestreSpike\pty\<id>.sock`.
- ACL da pasta `pty` (aplicada pela instalação): `D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;<motor>)(A;OICI;RC;;;OW)(A;;0x100002;;;<mestre-agente>)`. O `mestre-agente` **consegue** criar o socket só com `FILE_ADD_FILE`; o arquivo nasce com dono `mestre-agente` e DACL herdada `SY:FA, motor:FA, OW:RC` (conferido em todos os terminais).
- Aperto de mão HMAC-SHA256 nos dois sentidos com nonces de 32 bytes (`proto.go`); mensagens `start`, `screen`, `input`, `replay`, `status`, `injob`, `stop`.
- **Achado A (go-pty):** `Cmd.Start` fecha o handle da thread logo depois do `CreateProcess`, então não dá para `ResumeThread`. Funciona assim: `CreationFlags: CREATE_SUSPENDED` → `OpenProcess` pelo PID → `AssignProcessToJobObject` → `NtResumeProcess`. Sem janela: o processo só roda dentro do Job.
- **Achado B (x/vt):** o emulador escreve as respostas a consultas do terminal (posição do cursor, atributos) num `io.Pipe` síncrono; sem alguém lendo `Emulator.Read`, o `Write` trava. O ptyhost lê e devolve ao ConPTY, como um terminal faz.
- **Achado C:** o `conhost.exe` do ConPTY é filho do ptyhost, **fora do Job**, na sessão de logon do terminal. Some com o `ClosePseudoConsole` ou com o fim do ptyhost, e a varredura da sessão de logon (item 9) também o pega.

## 3. Motor — PASSOU

- Ordem de `terminal.md#criação`: linha gravada → `start-pty` → PID e horário gravados → conexão (o socket aparece em 50–250 ms) → `SIO_AF_UNIX_GETPEERPID` do lado do **cliente** devolve o PID do ptyhost, conferido com o horário de criação → aperto de mão → `start` do agente.
- `cli.sock` do motor: em 11/11 chamadas, o PID lido por `SIO_AF_UNIX_GETPEERPID` foi o PID do próprio processo chamador (`ident.md`, coluna "PID pelo socket").

## 4. CLIs pelo ptyhost — PASSOU

`clis.md` e `clis-claude.md`. Em cada um: tela de início, digitar `hello ptyhost` (apareceu em 0,2–0,6 s), 1º `\x03` (processo vivo e a linha limpa ou a dica "again to exit"), 2º `\x03` (saída com código 0 em 0,2–1,1 s), `stop kill`.

| | entrada/saída | 1º `\x03` | 2º `\x03` |
|---|---|---|---|
| agente falso (`--provider claude`) | ok | vivo, "Press Ctrl-C again to exit" | saiu, 0 |
| `claude` 2.1.296 (`CLAUDE_CONFIG_DIR=p1\claude`) | ok | vivo, linha limpa e dica | saiu, 0 |
| `codex` 0.162.1 (`CODEX_HOME` padrão) | ok | vivo, linha limpa | saiu, 0 |
| `agy` 1.3.3 (`HOME`/`USERPROFILE` = `p1\home`) | ok, logado (e-mail na tela) | vivo, "press ctrl+c again to exit" | saiu, 0 |

- **Telas de primeira execução:** o `claude` num `CLAUDE_CONFIG_DIR` novo mostra o seletor de tema e depois a confiança da pasta, com o cursor em "No, exit": Enter sai; seta para baixo + Enter confia. O `codex` mostrou uma vez "Set up the Codex agent sandbox" (na instalação; já previsto em `codex.md`).
- Instalação: o instalador oficial do Codex pôs o pacote em `%USERPROFILE%\.codex\packages\standalone\current\` (junção em `%LOCALAPPDATA%\Programs\OpenAI\Codex\bin`, no `Path` do usuário) e fez o login no `CODEX_HOME` padrão; o do Claude, em `%USERPROFILE%\.local\bin\claude.exe`.

## 5. Sobrevivência — PASSOU

`survive.md` e `hkcu.md`. Com agente falso, `agy` interativo e um `cmd` vivos:
- **`Stop-Service MestreSpike`:** agentes e ptyhosts vivos. **`Start-Service`:** o motor leu `terminals.json` e reconectou os 3 (PID do par + horário + aperto de mão) em 1–4 ms cada; mesmos PIDs; **tela do agente falso e do `agy` iguais** às de antes; digitação depois da volta chegou ao agente.
- **`MestreSpikeLauncher` parado e iniciado:** agentes vivos. No `cmd` antigo, `reg query HKCU\Environment` e `cmdkey /list` (com `gemini:antigravity`) funcionaram antes e depois. Num ptyhost novo, iniciado depois do reinício, `agy -p "/usage" --output-format json` saiu com 0 e `"status":"SUCCESS"` (cotas na saída).
- **Achado D:** o launcher reiniciado perde o registro dos ptyhosts que criou (o handle do perfil e a espera pelo fim de cada um morrem com o processo). O perfil continua carregado (bom para os terminais vivos), mas ninguém mais o descarrega nem fecha aquelas sessões de logon quando esses ptyhosts terminam. Plano (e) não foi preciso para o terminal funcionar; a implementação precisa que o launcher, ao subir, readote os `mestre-pty` vivos (ex.: lista persistida ou enumeração por sessão de logon) para a limpeza.

## 6. Isolamento — PARCIAL

`isolate.md`, `logs\sockcheck-*.json`. Dois processos `mestre-agente`: um **fora do terminal** (iniciado pelo launcher) e um **dentro de outro terminal**:

| Tentativa | Fora | Dentro de outro terminal |
|---|---|---|
| conectar no socket | recusado (WSAEACCES) | recusado |
| mudar a DACL do arquivo do socket | recusado (acesso negado) | recusado |
| apagar o arquivo do socket | recusado | recusado |
| ler o descritor do socket | permitido (`OW:RC`, esperado) | permitido |

- **Segredo errado:** um motor falso com outro segredo não recebeu `ready`; o ptyhost registrou "proof does not match the secret" e fechou a conexão; o pedido seguinte não teve resposta. O motor real seguiu usando o terminal.
- **Não executado nesta rodada:** `OpenProcess` no ptyhost com `PROCESS_VM_READ` e `PROCESS_DUP_HANDLE` (item (d)). Fica para a próxima rodada, a confirmar pelo Dono. O ptyhost tem a opção `-harden` (plano (d): restringe a DACL das threads e a DACL padrão do token, e dá ao agente um token filho com a DACL original), escrita mas não exercitada.

## 7. Encerramento — PASSOU

`kill.md`. Num `cmd` no terminal: `start "" /b … helper stay -ignore-ctrlc` (neto que ignora Ctrl-C) e `start "" … helper stay` (neto com console próprio, e seu `conhost.exe`). Depois do `\x03`, o Job tinha `cmd`, os dois netos e o `conhost` do console próprio.
- **`TerminateJobObject` pelo ptyhost:** 4 processos antes, 0 ativos logo depois; **0 processos da árvore vivos** após 0,1 s; sessão de logon do terminal vazia.
- **`TerminateProcess` do ptyhost pelo motor** (permitido pelo descritor do processo): saiu em 4 ms; o fechamento do Job (`KILL_ON_JOB_CLOSE`) levou a árvore: **0 processos** vivos após 0,1 s, sessão de logon vazia.

## 8. Identificação — PASSOU

`ident.md`. O motor lê o PID pelo `SIO_AF_UNIX_GETPEERPID`, recusa processo criado depois de aceitar a conexão e pergunta a cada ptyhost `injob` (o ptyhost confere o horário de criação e faz `IsProcessInJob` com o Job que só ele tem).

| Caso | Esperado | Resultado |
|---|---|---|
| direto, `cmd /c`, `powershell -c`, Job aninhado | T1 | T1 |
| órfão `cmd /c start "" /b` (pai já saiu) e órfão `Start-Process` | T1 | T1 |
| `CREATE_BREAKAWAY_FROM_JOB` | T1 (filho negado) | T1; `CreateProcess` com breakaway: acesso negado |
| WMI (`Win32_Process.Create`) | recusa | recusa (em nenhum Job) |
| direto em T2 | T2 | T2 |
| fora: console de Administrador; fora: `mestre-agente` pelo launcher | recusa | recusa (`OpenProcess` negado ao motor) |

Custo por identificação, de ponta a ponta no motor, com 2 terminais: 0,5–1,6 ms (uma ida e volta no socket por ptyhost consultado), contra ~4 µs do `IsProcessInJob` direto da rodada 2.

## 9. WMI — PASSOU (padrão)

`wmi.md`. De dentro do terminal, `Invoke-CimMethod Win32_Process Create` criou o processo (filho do `WmiPrvSE.exe`, fora do Job). **Sessão de logon (`AuthenticationId`) do processo do WMI = a do terminal** (`00980836`), com o launcher fazendo um `LogonUser` por terminal. O ptyhost o listou como "na sessão de logon, fora do Job"; o `stop` (`TerminateJobObject` + varredura da sessão de logon) o encerrou, junto com o `conhost.exe` dele; **nada sobrou** na sessão. O plano B (negar métodos WMI em `root\cimv2`) não foi necessário.

## `go vet`, `go test` e suíte rápida (Windows)

- `go vet ./...`: sem achados (exit 0). `go test ./...`: todos `ok` (exit 0). `rodada-3\go-vet.txt`, `go-test.txt`.
- `go run ./tools/testrun fast`: passou (exit 0, `testrun-fast.txt`) depois de instalar o `golangci-lint` 2.14.0 (`go install …@v2.14.0`); antes falhava só por não haver o linter no Sandbox.

## O que precisa mudar nos documentos

1. `docs/desenvolvimento/ordem-de-implementacao.md`, tabela "Spikes do M1", linha do spike 5: **feito (2026-10-13)** para (a), (b), (c), (e), (f) e o WMI; (d) pendente (acesso ao processo do ptyhost).
2. `docs/arquitetura/windows.md`, "Verificar na implementação", 1.º item (spike 5): (a), (b), (c), (e), (f) passaram (§1–§5, §7); (d) continua aberto. 2.º item (WMI): o padrão se confirmou (§9); pode fechar, mantendo a conferência do checklist.
3. `docs/arquitetura/windows.md`, "Resultado dos spikes do M1": acrescentar a rodada 3 (resumo acima) e os achados A–D.
4. `docs/arquitetura/windows.md`, "ConPTY" (sequência): com o `go-pty`, o processo é retomado por `NtResumeProcess` depois de entrar no Job (achado A); e o `conhost.exe` do ConPTY fica fora do Job, na sessão de logon do terminal (achado C).
5. `docs/arquitetura/windows.md`, "Iniciador": (i) o launcher nunca registra o corpo dos pedidos (o segredo vai neles); (ii) ao subir, ele readota os `mestre-pty` vivos, para descarregar o perfil e fechar a sessão de logon de cada um quando terminar (achado D).
6. `docs/arquitetura/terminal.md`, "Emulador de terminal": as respostas do emulador a consultas do programa voltam ao PTY (achado B). "Verificar na implementação" do `go-pty`: passou no Windows com os três CLIs e o agente falso (com o achado A).
7. `docs/arquitetura/terminal.md`, "Encerramento" passo 3 (Windows): a varredura da sessão de logon funciona e também pega o `conhost.exe` do ConPTY e o de processos do WMI (§9).
8. `docs/arquitetura/seguranca.md`, "Identidade de quem chama a CLI", Windows: resultado do spike 5 (§8) e o custo de 0,5–1,6 ms com a pergunta ao `mestre-pty`. Perguntar só ao `mestre-pty` do terminal do `agent_id` (como o texto já diz) mantém uma ida e volta por chamada.
9. `docs/provedores/codex.md`, "Instalação": no Windows, o instalador oficial põe o pacote em `%USERPROFILE%\.codex\packages\standalone\current\` (junção em `%LOCALAPPDATA%\Programs\OpenAI\Codex\bin`) e faz o login no `CODEX_HOME` padrão; o perfil de conta precisa apontar o `CODEX_HOME` antes do login.
10. `docs/provedores/claude-code.md` (telas de início / adaptador): num `CLAUDE_CONFIG_DIR` novo, seletor de tema e confiança com o cursor em "No, exit" (Enter sai): a resposta do adaptador é seta para baixo + Enter, ou a pré-confiança do motor.
11. `docs/desenvolvimento/ambiente.md`, Windows Sandbox: o `setup.ps1` deve instalar o `golangci-lint` 2.14.0 (sem ele a suíte rápida falha no Sandbox).
