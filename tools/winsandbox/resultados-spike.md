# Relatório dos spikes de Windows do M1

> Branch `spike/m1-windows` · Windows Sandbox · 2026-10-12 · para o coordenador atualizar os documentos

## Ambiente

- Windows 11 Enterprise 24H2, build **26100.9457** (`ver`: 10.0.26100.9457; o registro ainda diz "Windows 10 Enterprise" em `ProductName`), em português. Windows Sandbox, usuário `WDAGUtilityAccount` (administrador).
- Go 1.27.2; `golang.org/x/sys` **v0.47.0** (a v0.48+ e o `go-winio` exigem `go 1.26`, por isso não foram usados; o `go.mod` passou de `go 1.25` para `go 1.25.0`, mesma versão).
- CLIs: Claude Code 2.1.296 (instalador nativo), Codex 0.162.1 (instalador oficial `irm https://chatgpt.com/codex/install.ps1 | iex`), agy 1.3.3 (`irm https://antigravity.google/cli/install.ps1 | iex`).
- Código: `tools/winsandbox/spikes/01-agent-service/` (um binário, `spikesvc`, cobre os três spikes; o comentário do pacote explica o uso). Serviços de teste: `MestreSpikeNode` (conta virtual `NT SERVICE\MestreSpikeNode`) e `MestreSpikeLauncher` (LocalSystem, plano B). Canal de controle por named pipe com ACL só SYSTEM, Administradores (e as contas dos dois serviços).
- Toda espera nos programas tem prazo e o tempo esperado aparece na saída (ex.: "exit after 64ms (deadline 30s)"); não há `sleep` em teste do repositório.

## Resumo

| Spike | Resultado | Em uma linha |
|---|---|---|
| 2. Privilégios da conta virtual | **Passou** | LSA concede, o token do serviço tem os dois; declarar como exigidos tira o resto do token |
| 1. ConPTY como `mestre-agente` a partir da conta virtual | **Falhou no padrão; passou com o plano B** | a conta virtual faz tudo menos `LoadUserProfile` (1314 / acesso negado, sem saída) |
| 3. Login dos CLIs no perfil do `mestre-agente` | **Passou** (claude, codex, agy) com o perfil carregado pelo LocalSystem | agy guarda no Gerenciador de Credenciais, não em arquivo; `HOME` não o isola |

## Spike 2 — privilégios da conta virtual: PASSOU

Logs: `logs/spike2-*`. Três instalações do mesmo serviço:

| Estado | Direitos LSA da conta virtual | `sc qprivs` | Privilégios no token do serviço (`spikesvc info`) | `CreateProcessAsUser` |
|---|---|---|---|---|
| a) nada concedido | `[]` | vazio | Shutdown, ChangeNotify, Undock, Impersonate, CreateGlobal, IncreaseWorkingSet, TimeZone | falha **1314** ("O cliente não tem o privilégio necessário") |
| b) LSA, sem declarar | `[SeAssignPrimaryTokenPrivilege SeIncreaseQuotaPrivilege]` | vazio | os de (a) **+** AssignPrimaryToken e IncreaseQuota (desabilitados; o programa habilita) | funciona |
| c) LSA e declarados | idem | ChangeNotify, AssignPrimaryToken, IncreaseQuota | **só** AssignPrimaryToken, IncreaseQuota e ChangeNotify | funciona |

- `LsaAddAccountRights` sobre o SID `S-1-5-80-…` da conta virtual funciona depois de o serviço existir (`LookupAccountName("NT SERVICE\<nome>")` só resolve com o serviço criado).
- Declarar privilégios exigidos (`SERVICE_CONFIG_REQUIRED_PRIVILEGES_INFO`) **remove** do token `SeImpersonatePrivilege` e `SeCreateGlobalPrivilege`. Se o `mestre-node` precisar deles (ex.: `ImpersonateNamedPipeClient` no canal local, objetos `Global\`), têm de entrar na lista.
- Os privilégios vêm desabilitados; o serviço habilita com `AdjustTokenPrivileges` antes de `CreateProcessAsUser`.

## Spike 1 — ConPTY como `mestre-agente` a partir do serviço: FALHOU no padrão, PASSOU com o plano B

Conta `mestre-agente` criada como no documento: só `Users`, senha aleatória de 32 caracteres (nunca expira, a conta não troca), guardada com DPAPI de máquina em `%ProgramData%\MestreSpike\no\mestre-agente.senha` com ACL só SYSTEM e a conta virtual; direitos `SeBatchLogonRight`, `SeDenyInteractiveLogonRight`, `SeDenyRemoteInteractiveLogonRight`; oculta em `SpecialAccounts\UserList`.

Verificações automáticas (`spikesvc test`), cada uma num logon novo:
- **identity**: `whoami` como `mestre-agente`, grupo BATCH (S-1-5-3), integridade média (S-1-16-8192), fora de Administradores, `USERPROFILE=C:\Users\mestre-agente`, `HKCU` é o hive do usuário (escrita em `HKCU\Software` funciona), sessão 0, sessão de logon diferente da do serviço.
- **fakeagent** (o agente falso chamado `agy.exe`): título na saída, menu de confiança respondido com Enter, `/fake-dump` digitado e respondido, variável extra (`MESTRE_SPIKE=1`) recebida, saída com código 0.
- **resize**: PowerShell vê 200×50; depois de `ResizePseudoConsole` vê 120×40.
- **tree**: `cmd` → neto `ping` no mesmo console (`start /b`) e neto `ping` em console próprio oculto (`Start-Process`). Passo 2 (`ClosePseudoConsole`): o `cmd` sai (0xC000013A) e o `ping` anexado morre; **sobrevivem** o `ping` de console próprio e o `conhost.exe` dele, ainda no Job. Passo 3 (`TerminateJobObject`): nenhum processo vivo em 201 ms.

| Variante | Quem faz `LoadUserProfile` | Quem cria ConPTY e processo | Resultado | Log |
|---|---|---|---|---|
| Padrão | conta virtual | conta virtual | **falha**: `LoadUserProfile` 1314 | `spike1-a-*` |
| Padrão + `SeBackup`/`SeRestore` concedidos, declarados e habilitados | conta virtual | conta virtual | **falha**: `LoadUserProfile` "Acesso negado", com ou sem perfil já criado | `spike1-c-*`, `spike1-f-*` |
| Sem `LoadUserProfile` | ninguém | conta virtual | inicia, mas `USERPROFILE=C:\Users\Default`, `HKCU` não é do usuário; o agente falso falha ao gravar em `C:\Users\Default\.gemini` | `spike1-b-*`, `spike1-g-*` |
| **Plano B completo** (`mestre-launcher` LocalSystem faz tudo) | LocalSystem | LocalSystem | **passa tudo** | `spike1-final-launcher.txt` |
| Plano B "estreito": LocalSystem só mantém o perfil carregado | LocalSystem (outra sessão de logon) | conta virtual, com logon próprio | ConPTY passa tudo, mas `CreateEnvironmentBlock` dá "Acesso negado" (o motor monta o ambiente pelo `ProfileList`) e o **Gerenciador de Credenciais não carrega** (`CredRead` 1168) | `spike1-final-node-hibrido.txt`, `spike3-credprobe.txt` (H) |
| **Plano B com entrega do token**: o launcher faz `LogonUser` + `LoadUserProfile` + `CreateEnvironmentBlock` e passa o token por `DuplicateHandle` ao nó (pid pelo `GetNamedPipeClientProcessId`); o nó cria ConPTY, processo e Job | LocalSystem, na mesma sessão de logon do token | conta virtual | **passa tudo**; Gerenciador de Credenciais lê e grava nos dois sentidos | `spike1-final-node-token-do-launcher.txt`, `spike3-credprobe.txt` (A–G) |

- O **ConPTY do sistema funciona na sessão 0** com as duas contas (conta virtual e LocalSystem): o plano do `OpenConsole.exe` **não foi necessário** e não foi testado.
- A ACE da SID de logon na estação de janela (`Service-0x0-<luid>$`) e na área de trabalho `Default` do serviço é aceita pela conta virtual e pelo LocalSystem (`SetSecurityInfo` sem erro). A variante sem as ACEs não foi testada.
- O motivo do 1314 não é falta de privilégio concedível: mesmo com `SeBackupPrivilege` e `SeRestorePrivilege` habilitados o serviço de perfis recusa ("Acesso negado"). `LoadUserProfile` fica restrito a administrador/LocalSystem, como diz a documentação da API.
- `Ctrl-C` (`\x03`) chega como `CTRL_C_EVENT` a processo que não desliga `ENABLE_PROCESSED_INPUT`; o agente falso, no Windows, sai pelo sinal em vez de mostrar "Press Ctrl-C again". Os CLIs reais põem o console em modo cru e recebem o byte.
- Depois de `ResizePseudoConsole`, o ConPTY redesenha a tela inteira (texto antigo volta no fluxo): o emulador e quem procura texto na saída precisam contar com isso.
- Named pipe com mais de uma instância: a DACL tem de incluir a própria conta do serviço (`FILE_CREATE_PIPE_INSTANCE`), senão a 2.ª instância dá "Acesso negado".

## Spike 3 — login dos CLIs no perfil do `mestre-agente`: PASSOU (com o perfil carregado pelo LocalSystem)

O Dono fez o login num terminal aberto pelo `MestreSpikeLauncher` como `mestre-agente` (`login.ps1`, relay do console local para o ConPTY da sessão 0), com perfil de conta em `%ProgramData%\MestreSpike\work\contas\p1\`. Depois, cada verificação rodou num **processo novo, noutra sessão de logon** da mesma conta (`logs/spike3-*`).

| CLI | Login (sessão 0, sem navegador) | Onde ficou | Achado num logon novo pelo launcher | Pela conta virtual com token entregue | Pela conta virtual, perfil segurado (estreito) |
|---|---|---|---|---|---|
| claude | `claude auth login`: mostra a URL e pede o código colado | arquivo `CLAUDE_CONFIG_DIR\.credentials.json` | sim (`loggedIn: true`) | sim | sim |
| codex | `codex login --device-auth` | arquivo `CODEX_HOME\auth.json` (padrão, **sem** `config.toml`) | sim ("Logged in using ChatGPT") | sim | sim |
| agy | `agy` interativo, menu → login Google | **Gerenciador de Credenciais** do `mestre-agente`: genérica `LegacyGeneric:target=gemini:antigravity`, usuário `antigravity`, persistência máquina local; blob DPAPI em `C:\Users\mestre-agente\AppData\Local\Microsoft\Credentials\`; **nenhum arquivo** sob `HOME` | sim (`agy models` e `agy -p` respondem) | **inconclusivo** (abaixo) | **não**: `CredRead` dá 1168 e o agy pede login de novo |

- `HOME` e `USERPROFILE` apontando para o perfil de conta **não isolam** o login do agy: ele vai para o Gerenciador de Credenciais da conta `mestre-agente`, comum a todos os perfis de conta. Com `USERPROFILE` trocado, o agy cria `AppData\Local\Microsoft\Windows\INetCache` dentro do perfil de conta.
- O Gerenciador de Credenciais só funciona para uma sessão de logon cujo token passou por `LoadUserProfile` (prova com `credprobe.ps1`: falha só na variante estreita).
- **agy com token entregue:** o `CredRead` do probe funciona nessa variante, mas a primeira execução do `agy -p` ali falhou com "not logged in" e, depois dela, a credencial `gemini:antigravity` tinha sumido (`cmdkey /list` vazio também pelo launcher). Hipótese: o agy apagou a credencial ao falhar uma renovação. Para fechar, falta um novo login do agy e a sequência `cmdkey` → `agy -p` pela entrega do token → `cmdkey`.
- O instalador do agy e o do codex instalam no perfil de quem instala (ACL só do usuário, SYSTEM e Administradores) e se atualizam sozinhos lá; as cópias em `%ProgramData%` não se atualizam (o agy registra "Directory … not fully accessible …, skipping update"). O codex precisa da pasta inteira da versão (~450 MB: `bin\`, `codex-path\rg.exe`, `codex-resources\`), não só do `codex.exe`.

## O que mudar nos documentos

1. `docs/arquitetura/windows.md`, "Verificar na implementação", 1.º item (ConPTY a partir da conta virtual): resultado **falhou no padrão**; adotar o plano B. Recomendo a forma **com entrega do token**, que deixa o `mestre-launcher` mínimo: ele faz `LogonUser` em lote, `LoadUserProfile` e `CreateEnvironmentBlock`, passa o token ao `mestre-node` por `DuplicateHandle` (cliente identificado por `GetNamedPipeClientProcessId`) e mantém o perfil carregado até o nó liberar. O `mestre-node` continua criando ConPTY, processo e Job, então resize e encerramento ficam nele. O `OpenConsole.exe` não é necessário. Se a pendência do agy (acima) mostrar que só o plano B completo serve, vale o texto atual do plano B.
2. `docs/arquitetura/windows.md`, 2.º item (privilégios): **passou**; acrescentar que declarar os privilégios exigidos remove `SeImpersonatePrivilege` e `SeCreateGlobalPrivilege`, a serem incluídos se o nó precisar.
3. `docs/arquitetura/windows.md`, "Conta dos agentes" → "Como um personagem nasce", e a seção "ConPTY": trocar a sequência pela do plano B escolhido (quem faz `LoadUserProfile`, `CreateEnvironmentBlock` e quem segura o perfil). "Diagnóstico com IA" (Guardião como LocalSystem) não muda.
4. `docs/arquitetura/windows.md`, 3.º item (login dos CLIs): claude e codex em arquivo no perfil de conta (funciona); agy no Gerenciador de Credenciais da conta, que exige `LoadUserProfile` na própria sessão de logon.
5. `docs/provedores/antigravity.md`, item "`HOME` separado isola o chaveiro" (linha "Verificar na implementação"): **no Windows não isola** (sem arquivo de reserva sob `HOME`); vale o "Se falhar" (um perfil de conta Antigravity por nó Windows), e o checklist (WIN-20) precisa explicar.
6. `docs/provedores/codex.md`: no Windows, sem `config.toml`, o login já foi para `auth.json` sob `CODEX_HOME`; manter `cli_auth_credentials_store = "file"` explícito.
7. `docs/arquitetura/componentes.md` (serviços e contas padrão) e `docs/plataforma/instalacao-e-atualizacao.md`: acrescentar o `mestre-launcher` (LocalSystem). A instalação precisa pôr os CLIs fora do perfil do usuário com ACL protegida (`Users` só leitura e execução; `%ProgramData%` herda escrita para `Users`), e a atualização dos CLIs fica com o Mestre (`docs/provedores/atualizacao-dos-clis.md`).
8. `docs/arquitetura/terminal.md`, "Encerramento": confirmado que o passo 3 é necessário (processo em console próprio sobrevive ao `ClosePseudoConsole`) e que o Job pega até o `conhost.exe` desse console. Anotar o redesenho da tela depois de `ResizePseudoConsole`.
9. `docs/qualidade/dubles.md`, agente falso: no Windows, pôr o console em modo cru (sem `ENABLE_PROCESSED_INPUT`) para receber `\x03` como os CLIs reais.
10. `docs/desenvolvimento/ambiente.md`, Windows Sandbox: o `setup.ps1` pode instalar codex e agy, e o x/sys fica em v0.47.0 enquanto o `go.mod` pedir `go 1.25`.

## Suítes no Windows

- `go vet ./...`: sem achados (`logs/go-vet.txt`).
- `go test ./...`: todos os pacotes `ok` em ~4 s (`logs/go-test.txt`). `tools/testrun` não rodou (sem golangci-lint no Sandbox).

## Arquivos em `resultados/`

`relatorio.md` (este) e `logs/`: saídas de cada variante (`spike1-*`, `spike2-*`, `spike3-*`), transcrições cruas e de texto por cenário (subpastas), logs dos dois serviços (`MestreSpikeNode.log`, `MestreSpikeLauncher.log`), `go-vet.txt`, `go-test.txt`. E-mail e ids da conta foram omitidos; nenhuma senha ou token foi gravado.
