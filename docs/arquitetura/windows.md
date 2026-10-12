# Windows

> **Assunto:** o Windows como Core ou como nó, sem interface gráfica: serviços, contas virtuais, a conta dos agentes `mestre-agente`, ConPTY, o Operador do PC e o que verificar · **Componente:** Todos · **Atualizado:** 2026-10-12

## Princípios

- **Só serviços, sem interface gráfica.** Tudo roda como serviço do Windows na sessão 0 e sobe com o PC, com ou sem usuário logado. Não há processo na sessão do usuário, tarefa agendada nem janela na área de trabalho, salvo o [Operador do PC](#operador-do-pc), quando habilitado. O acesso é pelo painel no navegador; os agentes são de terminal e navegam na web pelo MCP de navegador próprio de cada personagem ([permissões](../classes/permissoes.md)). O porquê: [ADR 0020](../decisoes/0020-quatro-usos-core-em-linux-ou-windows.md).
- **Agente nunca roda com a conta dona do cofre nem com a conta do usuário** ([segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)); a única exceção é o Operador do PC ([ADR 0021](../decisoes/0021-operador-do-pc.md)).
- **Mesmo modelo de contas do Linux:** o motor do nó (`mestre-node`) roda numa conta de serviço (a conta virtual `NT SERVICE\MestreNode`, o papel de `mestre` no Linux) e inicia cada personagem com a conta local `mestre-agente` (abaixo). Fora do Operador do PC, nenhum processo do Mestre usa a conta de quem instalou.
- Nomes, contas e portas de cada serviço: [componentes](componentes.md#serviços-e-contas-padrão). Pastas e ACLs: [persistência](persistencia.md#arquivos).

## Windows como nó

| Serviço | Conta | O que faz |
|---|---|---|
| `mestre-node` | conta virtual `NT SERVICE\MestreNode` | conexão WSS com o Core, banco local, worktrees, terminais dos agentes (ConPTY, processos como `mestre-agente`), canal da CLI |
| `mestre-guardiao` | LocalSystem | vigia e conserta o `mestre-node`; página reserva; diagnóstico com IA ([Guardião](../autocura/guardiao.md)) |

Não há cofre no nó. Motor e agentes usam contas diferentes: os agentes não leem o banco, a configuração nem a chave de cliente do nó ([persistência](persistencia.md#arquivos)).

## Windows como Core

A máquina do Core Windows tem **dois serviços do motor**, do mesmo `mestre.exe`:

| Serviço | Conta | O que faz |
|---|---|---|
| `mestre-core` | conta virtual `NT SERVICE\MestreCore` | fonte da verdade: cofre, banco do Core, espelhos, painel e API, GitHub, Telegram, WSS dos nós, `whisper-server` e `ffmpeg` |
| `mestre-node` | conta virtual `NT SERVICE\MestreNode` | **nó local do Core**: roda o Grão-Mestre e os agentes desta máquina, como `mestre-agente` |
| `mestre-guardiao` | LocalSystem | vigia os dois serviços do motor; anúncio mDNS ([descoberta](../conectividade/descoberta.md)) |

- **Por que dois:** em toda máquina Core o motor do Core nunca roda agentes; eles rodam no nó local, o `mestre-node` ([componentes](componentes.md)). Assim o cofre fica numa conta que nem o motor do nó nem os agentes usam.
- **Contas virtuais:** `NT SERVICE\MestreCore` e `NT SERVICE\MestreNode` nascem com o registro de cada serviço, sem senha. `MestreCore` é dona de `%ProgramData%\Mestre\` no Core; a chave do cofre é protegida por DPAPI com escopo de máquina e o arquivo tem ACL só para ela ([cofre](../plataforma/cofre.md#chave-do-cofre)).
- **Nó local:** o `mestre-node` fala com o `mestre-core` pela mesma WSS com mTLS de um nó remoto, em `127.0.0.1:7443`, e é pareado sozinho pela instalação (sem convite manual). Ele trabalha direto no clone principal e nos worktrees das mesas do Core, sem bundle, como o nó local do Core Linux ([mesas](../orquestracao/mesas.md)).
- **CLI:** os agentes chamam o `mestre-node` pelo canal local dele, e o nó encaminha ao Core como qualquer nó ([canal local](canal-local.md)).
- **Autocura:** o Guardião vigia os dois serviços. Com o `mestre-core` no ar, a queda do `mestre-node` é tratada como a de qualquer nó; o resto segue as [camadas](../autocura/camadas.md), inclusive o modo máquina única do uso "só Windows".

## Conta dos agentes (`mestre-agente`)

Criada pela instalação em toda máquina Windows, Core ou nó ([instalação](../plataforma/instalacao-e-atualizacao.md)):
- **Conta local comum:** só no grupo `Users`, fora de `Administradores`; senha que nunca expira e que a própria conta não troca.
- **Senha:** 32 caracteres aleatórios gerados pela instalação; ninguém a digita nem a vê. Fica cifrada com DPAPI de escopo de máquina em `%ProgramData%\Mestre\no\mestre-agente.senha`, com ACL só para `NT SERVICE\MestreNode` e SYSTEM, sem herança ([persistência](persistencia.md#arquivos)).
- **Sem logon interativo:** direitos "Negar logon local" (`SeDenyInteractiveLogonRight`) e "Negar logon pelos Serviços de Área de Trabalho Remota" (`SeDenyRemoteInteractiveLogonRight`); oculta da tela de login (valor `mestre-agente` = 0 em `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon\SpecialAccounts\UserList`). Para o Mestre iniciar processos com ela, recebe "Fazer logon como trabalho em lotes" (`SeBatchLogonRight`).
- **Privilégios do motor:** a instalação concede a `NT SERVICE\MestreNode` `SeAssignPrimaryTokenPrivilege` e `SeIncreaseQuotaPrivilege` (pela LSA) e os declara nos privilégios exigidos do serviço. É o equivalente ao `CAP_SETUID`/`CAP_SETGID` do Linux.
- **Como um personagem nasce:** o `mestre-node` faz `LogonUser` em lote com a senha guardada, `LoadUserProfile` (perfil em `C:\Users\mestre-agente`), dá à SID de logon do token acesso à estação de janela e à área de trabalho do serviço, e cria o processo com `CreateProcessAsUser` e o ConPTY (abaixo). O token e o perfil ficam abertos enquanto houver terminal daquela conta e são liberados depois.
- **O que ela alcança:** dentro de `%ProgramData%\Mestre\`, só o que a ACL de [persistência](persistencia.md#arquivos) dá. O perfil do usuário (`C:\Users\<usuário>`) não é acessível a ela pelas ACLs padrão do Windows. Pastas fora do perfil que dão leitura a `Users` (outras pastas na raiz de um disco, por exemplo) continuam legíveis: **o Mestre não altera ACLs fora das pastas dele**.
- **CLIs:** `claude`, `codex` e `agy` precisam ser executáveis por ela: instalados fora do perfil do usuário ou para a própria conta, e com o login feito nela, na configuração inicial do perfil de conta ([bases e fichas](../orquestracao/bases-e-fichas.md#perfis-de-conta)). Os logins pessoais do usuário nos CLIs não são usados. O checklist confere (WIN-20, [checklist](../plataforma/checklist.md)).
- **Entre personagens:** todos usam `mestre-agente`; a separação entre eles é do despachante ([segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)).
- **Remoção:** `mestre uninstall` apaga a conta e o perfil dela ([desinstalar](../operacao/desinstalar.md)).

## Diagnóstico com IA

O agente de diagnóstico ([diagnóstico com IA](../autocura/diagnostico-com-ia.md)) precisa rodar justamente quando o `mestre-node` pode estar fora. Quem o inicia é o Guardião (LocalSystem), **direto como `mestre-agente`**, pelo mesmo mecanismo do `mestre-node` (lê a senha guardada, cuja ACL inclui SYSTEM; logon em lote; perfil carregado; `CreateProcessAsUser`), sem ConPTY: entrada e saída por pipes. Não há tarefa agendada.

## Operador do PC

O que é, ativação e fim: [bases e fichas](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc). Existe só num `mestre-node` Windows (nó, ou o nó local de um Core Windows) com o Operador habilitado na instalação para um usuário ([instalação](../plataforma/instalacao-e-atualizacao.md)).

- **Tarefa `\Mestre\Operador`:** gatilho "ao fazer logon de <usuário configurado>", executa `mestre node operator-session` com a conta dele, privilégios normais (sem "executar com privilégios mais altos"), sem limite de tempo, reiniciando em falha (3 vezes, a cada 1 min, padrão).
- **Processo da sessão:** liga-se ao `mestre-node` pelo named pipe `\\.\pipe\mestre-operador`, com ACL só da conta do usuário configurado e de `NT SERVICE\MestreNode`; o `mestre-node` aceita só um cliente com essa conta, na sessão do console. Pelo mesmo pipe, recebe a ordem de criar ou fechar um terminal do Operador, cria o processo com ConPTY e o próprio token (o do usuário) num Job Object, e repassa entrada, saída, tamanho e PIDs; para o `mestre-node`, é um terminal como os outros ([terminal](terminal.md)). A CLI e o `mestre hook` dos terminais do Operador usam o canal local em `\\.\pipe\mestre-operador-cli` (mesma ACL, mesmo contrato do [canal local](canal-local.md), com a identificação do chamador pela árvore de processos).
- **Capacidade `operador`:** o processo da sessão acompanha bloqueio, desbloqueio e logoff (`WTSRegisterSessionNotification`). O nó anuncia `operador: true` só com o processo ligado, a sessão desbloqueada e os itens de UAC do [checklist](../plataforma/checklist.md) (WIN-21, WIN-22) em dia. Tela bloqueada durante uma sessão: o nó para de entregar entrada ao terminal e avisa o Core, que encerra a sessão com o motivo "tela bloqueada".
- **Aviso na área de trabalho:** enquanto há sessão ativa, o processo da sessão mostra uma faixa no topo da tela, sempre visível e sem roubar o foco, "Mestre operando — até HH:MM", com o botão **Parar** (encerra na hora), e um ícone na área de notificação. É a única janela do Mestre.
- **Capturas:** o processo da sessão captura todos os monitores (PNG) a cada 30 s (padrão) e quando o `mestre-node` pede (a cada ação do MCP de controle do computador, vista no `post_tool`), e as entrega pelo pipe; o `mestre-node` as guarda em `no\operador\` até o Core confirmar o recebimento ([persistência](persistencia.md#operador-do-pc)).
- **Contenção:** com UAC ligado, o processo e seus filhos rodam com o token filtrado (Administradores só para negar); `%ProgramData%\Mestre` não tem entrada para a conta do usuário, e a chave do cofre só para `NT SERVICE\MestreCore`. O pedido de elevação aparece na área de trabalho segura, que entrada sintética não alcança (UIPI) ([segurança](seguranca.md#operador-do-pc)).

## ConPTY

- Wrapper próprio sobre `golang.org/x/sys/windows` (`CreatePseudoConsole`, `ResizePseudoConsole`, lista de atributos com `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`), dentro do serviço `mestre-node` ([ADR 0001](../decisoes/0001-stack-go.md)).
- Sequência: criar os pipes, `CreatePseudoConsole`, `STARTUPINFOEX` com o atributo de pseudo-console, `CreateProcessAsUser` com o token do `mestre-agente` e `EXTENDED_STARTUPINFO_PRESENT`. O filho **não** herda os handles padrão do pai, senão a saída escapa do pseudo-console.
- Ambiente do filho: `CreateEnvironmentBlock` do `mestre-agente` (`USERPROFILE`, `APPDATA` e `LOCALAPPDATA` do perfil dele) + variáveis da base, do perfil de conta e do Mestre (`MESTRE_AGENT_ID`, `MESTRE_CLI` etc.; [sessão zero do personagem](../sessao-zero/personagem.md)).
- Tamanho fixo, emulador de terminal, buffer, log e Job Object por terminal: iguais aos do [terminal](terminal.md).

## Verificar na implementação

Cada item é conferido no laboratório da [suíte pesada](../qualidade/testes.md#laboratório) (VM Windows, como Core e como nó; os spikes do M1, antes, no Windows Sandbox) e, nas máquinas de uso, pelo [checklist](../plataforma/checklist.md). Os três primeiros são spikes do M1 ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md#spikes-do-m1)). **Plano B** dos dois primeiros: um serviço auxiliar `mestre-launcher`, como LocalSystem, que só inicia processos como `mestre-agente` a pedido do `mestre-node`, por um named pipe com ACL só para `NT SERVICE\MestreNode`; ele cria o pseudo-console e o processo e devolve ao `mestre-node` as pontas dos pipes (`DuplicateHandle`); redimensionar e encerrar passam pelo mesmo pipe.

- **Verificar na implementação:** ConPTY com o processo iniciado como `mestre-agente` por `CreateProcessAsUser`, com o perfil carregado, a partir do serviço na sessão 0, roda `claude`, `codex` e `agy` com tela, entrada e redimensionamento corretos. **Padrão:** a sequência acima, com acesso da SID de logon à estação de janela e à área de trabalho do serviço. **Se falhar:** o `mestre-launcher` acima; se a falha for do ConPTY do sistema na sessão 0, o Mestre distribui o `OpenConsole.exe` (o ConPTY do Windows Terminal, licença MIT) e o usa pela mesma API. **Onde:** spike do M1, no [Windows Sandbox](../desenvolvimento/ambiente.md#windows-sandbox-para-os-spikes-do-m1) (um serviço de teste com conta virtual inicia o agente falso e os três CLIs); depois, na [conformidade](../provedores/conformidade.md) de cada máquina Windows.
- **Verificar na implementação:** a conta virtual `NT SERVICE\MestreNode` aceita `SeAssignPrimaryTokenPrivilege` e `SeIncreaseQuotaPrivilege` e os tem no token do serviço. **Padrão:** concedidos pela LSA e declarados nos privilégios exigidos do serviço. **Se falhar:** o `mestre-launcher` acima. **Onde:** spike do M1, no [Windows Sandbox](../desenvolvimento/ambiente.md#windows-sandbox-para-os-spikes-do-m1) (o serviço de teste lista os privilégios do próprio token e inicia um processo como `mestre-agente`).
- **Verificar na implementação:** `claude`, `codex` e `agy` gravam o login no perfil do `mestre-agente` (pasta do perfil de conta e, onde o CLI usa, DPAPI e Gerenciador de Credenciais do perfil carregado), e um personagem iniciado depois, noutra sessão de logon da mesma conta, encontra esse login. **Padrão:** funciona com o perfil carregado por `LoadUserProfile`. **Se falhar:** o perfil passa a guardar o login em arquivo onde o provedor permite (Codex já usa `cli_auth_credentials_store = "file"`); se ainda falhar, aquele provedor não é oferecido naquela máquina Windows, e o checklist explica (WIN-20). **Onde:** spike do M1 no [Windows Sandbox](../desenvolvimento/ambiente.md#windows-sandbox-para-os-spikes-do-m1) e conformidade no Windows (login feito no terminal de configuração do perfil, depois um personagem novo).
- **Verificar na implementação:** git aceita, como `mestre-agente`, worktrees cujo dono é `NT SERVICE\MestreCore` ou `NT SERVICE\MestreNode` (proteção "dubious ownership"); o mesmo vale no Linux para `mestre-agente` sobre pastas de `mestre`. **Padrão:** o `gitconfig` do personagem ([identidades e Apps](../github/identidades-e-apps.md#autoria-dos-commits)) inclui `safe.directory` para o clone principal e os worktrees daquele personagem. **Se falhar:** a instalação torna `mestre-agente` dona de `workspaces\` (no Linux, de `workspaces/`), o motor mantém modificação por ACL ou grupo e grava `safe.directory` na configuração git dele. **Onde:** suíte completa no Windows e no Linux (commit do agente falso num worktree do Core).

- **Verificar na implementação:** requisitos do `--chrome` a partir do terminal do Operador: mesma conta Claude na CLI e na extensão Claude in Chrome, Chrome aberto na mesma sessão do Windows, extensão instalada. **Padrão:** o processo da sessão confere os três antes de o nó anunciar `operador`, e a configuração inicial do Operador no painel guia o que faltar. **Se falhar:** o Operador inicia sem `--chrome` e usa só o MCP de controle do computador, e o painel mostra o motivo. **Onde:** spike no começo do Operador (M7), na VM Windows do laboratório, e no `TestMVP14_PCOperator`.
- **Verificar na implementação:** se o `--chrome` funciona com um perfil Claude próprio do Operador (`CLAUDE_CONFIG_DIR` em `%LOCALAPPDATA%\Mestre\Operador\claude\`) logado na mesma conta da extensão, ou só com o perfil Claude da conta do usuário. **Padrão:** perfil próprio. **Se falhar:** o Operador usa o perfil Claude do usuário, e a camada do Mestre e os hooks entram só por argumentos (`--settings` e `--append-system-prompt-file` apontando para a pasta da sessão), sem alterar os arquivos do usuário. **Onde:** o mesmo spike do M7.
- **Verificar na implementação:** o MCP de controle do computador instalado pelo usuário consegue capturar a tela e enviar mouse e teclado quando iniciado como filho do processo da sessão (ConPTY, mesma sessão e área de trabalho do usuário, integridade média). **Padrão:** consegue. **Se falhar:** o processo da sessão passa a criar o terminal do Operador com `CreateProcess` comum anexado à área de trabalho `winsta0\default` e o ConPTY só para entrada e saída; se ainda falhar, o Operador fica só com o Chrome (`--chrome`) e o painel explica. **Onde:** o mesmo spike do M7.

## Fontes

- Sessão ConPTY (Microsoft): https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session
- CreateProcessAsUser: https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessasuserw
- Contas de serviço (contas virtuais): https://learn.microsoft.com/en-us/windows-server/identity/ad-ds/manage/understand-service-accounts
- x/sys/windows: https://pkg.go.dev/golang.org/x/sys/windows
- Wrappers de referência: https://github.com/aymanbagabas/go-pty , https://pkg.go.dev/github.com/UserExistsError/conpty
- Notificações de sessão (`WTSRegisterSessionNotification`): https://learn.microsoft.com/en-us/windows/win32/api/wtsapi32/nf-wtsapi32-wtsregistersessionnotification
