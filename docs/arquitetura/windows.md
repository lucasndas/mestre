# Windows

> **Assunto:** o Windows como Core ou como nó, sem interface gráfica: serviços, contas virtuais, a conta dos agentes `mestre-agente`, ConPTY e o que verificar · **Componente:** Todos · **Atualizado:** 2026-10-12

## Princípios

- **Só serviços, sem interface gráfica.** Tudo roda como serviço do Windows na sessão 0 e sobe com o PC, com ou sem usuário logado. Não há processo na sessão do usuário, tarefa agendada nem notificação na área de trabalho. O acesso é pelo painel no navegador; os agentes são de terminal e navegam na web pelo MCP de navegador próprio de cada personagem ([permissões](../classes/permissoes.md)). O porquê: [ADR 0020](../decisoes/0020-quatro-usos-core-em-linux-ou-windows.md).
- **Agente nunca roda com a conta dona do cofre nem com a conta do usuário** ([segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)).
- **Mesmo modelo de contas do Linux:** o motor do nó (`mestre-node`) roda numa conta de serviço (a conta virtual `NT SERVICE\MestreNode`, o papel de `mestre` no Linux) e inicia cada personagem com a conta local `mestre-agente` (abaixo). Nenhum processo do Mestre usa a conta de quem instalou.
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

## ConPTY

- Wrapper próprio sobre `golang.org/x/sys/windows` (`CreatePseudoConsole`, `ResizePseudoConsole`, lista de atributos com `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`), dentro do serviço `mestre-node` ([ADR 0001](../decisoes/0001-stack-go.md)).
- Sequência: criar os pipes, `CreatePseudoConsole`, `STARTUPINFOEX` com o atributo de pseudo-console, `CreateProcessAsUser` com o token do `mestre-agente` e `EXTENDED_STARTUPINFO_PRESENT`. O filho **não** herda os handles padrão do pai, senão a saída escapa do pseudo-console.
- Ambiente do filho: `CreateEnvironmentBlock` do `mestre-agente` (`USERPROFILE`, `APPDATA` e `LOCALAPPDATA` do perfil dele) + variáveis da base, do perfil de conta e do Mestre (`MESTRE_AGENT_ID`, `MESTRE_CLI` etc.; [sessão zero do personagem](../sessao-zero/personagem.md)).
- Tamanho fixo, emulador de terminal, buffer, log e Job Object por terminal: iguais aos do [terminal](terminal.md).

## Verificar na implementação

Cada item é conferido no laboratório da [suíte pesada](../qualidade/testes.md#laboratório) (VM Windows, como Core e como nó) e, nas máquinas de uso, pelo [checklist](../plataforma/checklist.md). Os três primeiros são spikes do M1 ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md#spikes-do-m1)). **Plano B** dos dois primeiros: um serviço auxiliar `mestre-launcher`, como LocalSystem, que só inicia processos como `mestre-agente` a pedido do `mestre-node`, por um named pipe com ACL só para `NT SERVICE\MestreNode`; ele cria o pseudo-console e o processo e devolve ao `mestre-node` as pontas dos pipes (`DuplicateHandle`); redimensionar e encerrar passam pelo mesmo pipe.

- **Verificar na implementação:** ConPTY com o processo iniciado como `mestre-agente` por `CreateProcessAsUser`, com o perfil carregado, a partir do serviço na sessão 0, roda `claude`, `codex` e `agy` com tela, entrada e redimensionamento corretos. **Padrão:** a sequência acima, com acesso da SID de logon à estação de janela e à área de trabalho do serviço. **Se falhar:** o `mestre-launcher` acima; se a falha for do ConPTY do sistema na sessão 0, o Mestre distribui o `OpenConsole.exe` (o ConPTY do Windows Terminal, licença MIT) e o usa pela mesma API. **Onde:** spike do M1, na VM Windows do laboratório (um serviço de teste com conta virtual inicia o agente falso e os três CLIs); depois, na [conformidade](../provedores/conformidade.md) de cada máquina Windows.
- **Verificar na implementação:** a conta virtual `NT SERVICE\MestreNode` aceita `SeAssignPrimaryTokenPrivilege` e `SeIncreaseQuotaPrivilege` e os tem no token do serviço. **Padrão:** concedidos pela LSA e declarados nos privilégios exigidos do serviço. **Se falhar:** o `mestre-launcher` acima. **Onde:** spike do M1, na VM Windows (o serviço de teste lista os privilégios do próprio token e inicia um processo como `mestre-agente`).
- **Verificar na implementação:** `claude`, `codex` e `agy` gravam o login no perfil do `mestre-agente` (pasta do perfil de conta e, onde o CLI usa, DPAPI e Gerenciador de Credenciais do perfil carregado), e um personagem iniciado depois, noutra sessão de logon da mesma conta, encontra esse login. **Padrão:** funciona com o perfil carregado por `LoadUserProfile`. **Se falhar:** o perfil passa a guardar o login em arquivo onde o provedor permite (Codex já usa `cli_auth_credentials_store = "file"`); se ainda falhar, aquele provedor não é oferecido naquela máquina Windows, e o checklist explica (WIN-20). **Onde:** spike do M1 e conformidade no Windows (login feito no terminal de configuração do perfil, depois um personagem novo).
- **Verificar na implementação:** git aceita, como `mestre-agente`, worktrees cujo dono é `NT SERVICE\MestreCore` ou `NT SERVICE\MestreNode` (proteção "dubious ownership"); o mesmo vale no Linux para `mestre-agente` sobre pastas de `mestre`. **Padrão:** o `gitconfig` do personagem ([identidades e Apps](../github/identidades-e-apps.md#autoria-dos-commits)) inclui `safe.directory` para o clone principal e os worktrees daquele personagem. **Se falhar:** a instalação torna `mestre-agente` dona de `workspaces\` (no Linux, de `workspaces/`), o motor mantém modificação por ACL ou grupo e grava `safe.directory` na configuração git dele. **Onde:** suíte completa no Windows e no Linux (commit do agente falso num worktree do Core).

## Fontes

- Sessão ConPTY (Microsoft): https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session
- CreateProcessAsUser: https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessasuserw
- Contas de serviço (contas virtuais): https://learn.microsoft.com/en-us/windows-server/identity/ad-ds/manage/understand-service-accounts
- x/sys/windows: https://pkg.go.dev/golang.org/x/sys/windows
- Wrappers de referência: https://github.com/aymanbagabas/go-pty , https://pkg.go.dev/github.com/UserExistsError/conpty
