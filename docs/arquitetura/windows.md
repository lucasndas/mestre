# Windows

> **Assunto:** o Windows como Cidadela ou Torre, sem interface gráfica: serviços, conta virtual, a conta dos agentes `mestre-agente`, o iniciador `mestre-launcher`, ConPTY, o Operador do PC, o resultado dos spikes e o que verificar · **Componente:** Todos · **Atualizado:** 2026-10-13

## Princípios

- **Só serviços, sem interface gráfica.** Tudo roda como serviço do Windows na sessão 0 e sobe com o PC, com ou sem usuário logado. Não há processo na sessão do usuário, tarefa agendada nem janela na área de trabalho, salvo o [Operador do PC](#operador-do-pc), quando habilitado. O acesso é pelo painel no navegador; os agentes são de terminal e navegam na web pelo MCP de navegador próprio de cada personagem ([permissões](../classes/permissoes.md)). O porquê: [ADR 0020](../decisoes/0020-linux-e-windows-em-qualquer-papel.md).
- **Agente nunca roda com a conta dona do cofre nem com a conta do usuário** ([segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)); a única exceção é o Operador do PC ([ADR 0021](../decisoes/0021-operador-do-pc.md)).
- **Mesmo modelo de contas do Linux:** o motor (`mestre`) roda numa conta de serviço (a conta virtual `NT SERVICE\Mestre`, o papel de `mestre` no Linux) e inicia cada personagem com a conta local `mestre-agente` (abaixo), cujo logon e perfil vêm do [`mestre-launcher`](#iniciador-mestre-launcher). Fora do Operador do PC, nenhum processo do Mestre usa a conta de quem instalou.
- **Cidadela ou Torre, a mesma instalação:** qualquer máquina Windows pode virar Cidadela ([Cidadela e Torres](../guilda/cidadela-e-torres.md)), então tudo o que a Cidadela faz roda aqui também: painel, bot do Telegram, `whisper-server` e `ffmpeg`, banco principal, GitHub e cofre.
- Nomes, contas e portas de cada serviço, e as instâncias: [componentes](componentes.md#serviços-e-contas-padrão). Pastas e ACLs: [persistência](persistencia.md#arquivos).

## Serviços no Windows

| Serviço | Conta | O que faz |
|---|---|---|
| `mestre` | conta virtual `NT SERVICE\Mestre` | o motor: conexão com a Guilda, banco local de máquina e réplica (ou, na Cidadela, banco principal), worktrees, terminais dos agentes (ConPTY, processos com o token do `mestre-agente`), canal da CLI; na Cidadela, também cofre, espelhos, painel e API, GitHub, Telegram e o Grão-Mestre |
| `mestre-launcher` | LocalSystem | o [iniciador](#iniciador-mestre-launcher): faz o logon do `mestre-agente` e entrega o token ao `mestre`; nada mais |
| `mestre-guardiao` | LocalSystem | vigia e conserta o `mestre`; página reserva; anúncio mDNS ([descoberta](../conectividade/descoberta.md)); diagnóstico com IA ([Guardião](../autocura/guardiao.md)) |
| `mestre-update` | LocalSystem | ajudante de troca de binário, iniciado só por `NT SERVICE\Mestre` ([autoatualização](../plataforma/autoatualizacao.md)) |

- **Conta virtual:** `NT SERVICE\Mestre` nasce com o registro do serviço, sem senha, e é dona de `%ProgramData%\Mestre\<inst>\` ([instâncias](componentes.md#instâncias)). A chave do cofre, em toda máquina não excluída, é protegida por DPAPI com escopo de máquina e o arquivo tem ACL só para ela ([cofre](../plataforma/cofre.md#chave-do-cofre), [replicação](../guilda/replicacao.md#cofre-em-toda-máquina)).
- **Privilégios do motor:** a instalação concede a `NT SERVICE\Mestre` `SeAssignPrimaryTokenPrivilege` e `SeIncreaseQuotaPrivilege` (pela LSA, depois de criar o serviço, porque o nome da conta virtual só resolve com o serviço existente) e os declara como **privilégios exigidos** do serviço, junto de `SeChangeNotifyPrivilege`; o motor os habilita (`AdjustTokenPrivileges`) antes de `CreateProcessAsUser`. É o equivalente ao `CAP_SETUID`/`CAP_SETGID` do Linux. Declarar a lista **tira do token** `SeImpersonatePrivilege` e `SeCreateGlobalPrivilege`, que o motor não usa (o canal local identifica o chamador por `GetNamedPipeClientProcessId`, sem personificação); se uma função passar a precisar deles, eles entram na lista.
- **Um serviço só para o motor:** a conta que guarda o cofre é a mesma que cria os processos dos agentes, mas nenhum agente roda com ela: cada personagem nasce com o token do `mestre-agente`, que não alcança `config\`, `banco\` nem o cofre ([persistência](persistencia.md#arquivos)).
- **CLI:** os agentes chamam o `mestre` pelo canal local dele; numa Torre, o motor encaminha à Cidadela ([canal local](canal-local.md)).
- **Autocura:** o Guardião vigia o `mestre`; o resto segue as [camadas](../autocura/camadas.md), inclusive o modo máquina única da composição "só Windows".

## Conta dos agentes (`mestre-agente`)

Criada pela instalação em toda máquina Windows, Cidadela ou Torre ([instalação](../plataforma/instalacao-e-atualizacao.md)):
- **Conta local comum:** só no grupo `Users`, fora de `Administradores`; senha que nunca expira e que a própria conta não troca.
- **Senha:** 32 caracteres aleatórios gerados pela instalação; ninguém a digita nem a vê. Fica cifrada com DPAPI de escopo de máquina em `%ProgramData%\Mestre\<inst>\maquina\mestre-agente.senha`, com ACL só para SYSTEM (o `mestre-launcher` e o Guardião), sem herança ([persistência](persistencia.md#arquivos)).
- **Sem logon interativo:** direitos "Negar logon local" (`SeDenyInteractiveLogonRight`) e "Negar logon pelos Serviços de Área de Trabalho Remota" (`SeDenyRemoteInteractiveLogonRight`); oculta da tela de login (valor `mestre-agente` = 0 em `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon\SpecialAccounts\UserList`). Para o logon em lote, recebe "Fazer logon como trabalho em lotes" (`SeBatchLogonRight`).
- **Como um personagem nasce:** o `mestre` pede ao `mestre-launcher` o token do `mestre-agente` (abaixo); com ele, dá à SID de logon acesso à estação de janela e à área de trabalho do serviço e cria o processo com `CreateProcessAsUser`, o ConPTY e o Job Object ([ConPTY](#conpty)). Ao fim do último terminal daquela sessão de logon, o `mestre` devolve o token e o launcher descarrega o perfil.
- **O que ela alcança:** dentro de `%ProgramData%\Mestre\`, só o que a ACL de [persistência](persistencia.md#arquivos) dá. O perfil do usuário (`C:\Users\<usuário>`) não é acessível a ela pelas ACLs padrão do Windows. Pastas fora do perfil que dão leitura a `Users` (outras pastas na raiz de um disco, por exemplo) continuam legíveis: **o Mestre não altera ACLs fora das pastas dele**.
- **CLIs:** `claude`, `codex` e `agy` são instalados pelo instalador oficial de cada provedor rodando como `mestre-agente` (os instaladores do Codex e do Antigravity instalam no perfil de quem instala e se atualizam sozinhos lá; cópias em `%ProgramData%` não se atualizam), e o login é feito nela, na configuração inicial do perfil de conta ([bases e fichas](../orquestracao/bases-e-fichas.md#perfis-de-conta), [atualização dos CLIs](../provedores/atualizacao-dos-clis.md)). `claude` e `codex` guardam o login em arquivo dentro do perfil de conta; o `agy` guarda no **Gerenciador de Credenciais** da conta `mestre-agente`, comum a todos os perfis de conta (`HOME` não o isola), por isso há **um perfil de conta Antigravity por máquina Windows** ([Antigravity](../provedores/antigravity.md#contas-múltiplas)). O Gerenciador de Credenciais só funciona numa sessão de logon cujo token passou por `LoadUserProfile`, o que o launcher garante. O checklist confere (WIN-20, [checklist](../plataforma/checklist.md)).
- **Entre personagens:** todos usam `mestre-agente`; a separação entre eles é do despachante ([segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)).
- **Remoção:** `mestre uninstall` apaga a conta e o perfil dela ([desinstalar](../operacao/desinstalar.md)).

## Iniciador (`mestre-launcher`)

A conta virtual do `mestre` não consegue carregar o perfil de outra conta (`LoadUserProfile` é só de administrador ou LocalSystem), e sem o perfil o agente fica sem `USERPROFILE`, `HKCU` e Gerenciador de Credenciais próprios. Por isso existe um serviço LocalSystem **mínimo**:

- **Só inicia a sessão do `mestre-agente`:** a pedido, faz `LogonUser` em lote com a senha guardada, `LoadUserProfile` e `CreateEnvironmentBlock` e entrega ao `mestre` o token (por `DuplicateHandle` para o processo dele) e o bloco de ambiente; mantém o perfil carregado até o `mestre` devolver o token (ou até o `mestre` cair, quando libera tudo o que entregou a ele). Não cria processos, não lê outros segredos e não aceita outra conta, comando ou caminho.
- **Só aceita o serviço `mestre` da mesma instância:** o pipe `\\.\pipe\mestre-<inst>-launcher` tem ACL só para `NT SERVICE\Mestre` e SYSTEM (que precisa de `FILE_CREATE_PIPE_INSTANCE` para abrir mais de uma instância do pipe); cada conexão é conferida pelo processo cliente (`GetNamedPipeClientProcessId`): o token dele tem de ser o da conta virtual do `mestre` e o PID, o do serviço registrado no SCM. Fora disso, recusa e registra na auditoria local.
- **Pedidos:** `logon` (devolve o token, o ambiente e um identificador da sessão de logon) e `release` (descarrega o perfil e fecha o token). Mais nada.
- **Coberto por** CA-TERM-07 ([terminal](terminal.md#critérios-de-aceite)); o resto do terminal (ConPTY, processo, Job, redimensionar, encerrar) fica no `mestre`.

## Diagnóstico com IA

O agente de diagnóstico ([diagnóstico com IA](../autocura/diagnostico-com-ia.md)) precisa rodar justamente quando o `mestre` pode estar fora. Quem o inicia é o Guardião (LocalSystem), **direto como `mestre-agente`**, pelo mesmo mecanismo do launcher (lê a senha guardada; logon em lote; perfil carregado; `CreateProcessAsUser`), sem ConPTY: entrada e saída por pipes. Não há tarefa agendada.

## Operador do PC

O que é, ativação e fim: [bases e fichas](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc). Existe só numa máquina Windows (Cidadela ou Torre) com o Operador habilitado na instalação para um usuário ([instalação](../plataforma/instalacao-e-atualizacao.md)).

- **Tarefa `\Mestre\Operador`:** gatilho "ao fazer logon de <usuário configurado>", executa `mestre operator-session` com a conta dele, privilégios normais (sem "executar com privilégios mais altos"), sem limite de tempo, reiniciando em falha (3 vezes, a cada 1 min, padrão).
- **Processo da sessão:** liga-se ao `mestre` pelo named pipe `\\.\pipe\mestre-<inst>-operador`, com ACL só da conta do usuário configurado e de `NT SERVICE\Mestre`; o `mestre` aceita só um cliente com essa conta, na sessão do console. Pelo mesmo pipe, recebe a ordem de criar ou fechar um terminal do Operador, cria o processo com ConPTY e o próprio token (o do usuário) num Job Object, e repassa entrada, saída, tamanho e PIDs; para o `mestre`, é um terminal como os outros ([terminal](terminal.md)). A CLI e o `mestre hook` dos terminais do Operador usam o canal local em `\\.\pipe\mestre-<inst>-operador-cli` (mesma ACL, mesmo contrato do [canal local](canal-local.md), com a identificação do chamador pela árvore de processos).
- **Campo `operador` da ficha da máquina:** o processo da sessão acompanha bloqueio, desbloqueio e logoff (`WTSRegisterSessionNotification`). A máquina anuncia `operador: true` na [ficha](../guilda/ficha-da-maquina.md) só com o processo ligado, a sessão desbloqueada e os itens de UAC do [checklist](../plataforma/checklist.md) (WIN-21, WIN-22) em dia. Tela bloqueada durante uma sessão: o motor para de entregar entrada ao terminal e avisa a Cidadela, que encerra a sessão com o motivo "tela bloqueada".
- **Aviso na área de trabalho:** enquanto há sessão ativa, o processo da sessão mostra uma faixa no topo da tela, sempre visível e sem roubar o foco, "Mestre operando — até HH:MM", com o botão **Parar** (encerra na hora), e um ícone na área de notificação. É a única janela do Mestre.
- **Capturas:** o processo da sessão captura todos os monitores (PNG) a cada 30 s (padrão) e quando o `mestre` pede (a cada ação do MCP de controle do computador, vista no `post_tool`), e as entrega pelo pipe; o `mestre` as guarda em `maquina\operador\` até a Cidadela confirmar o recebimento ([persistência](persistencia.md#operador-do-pc)).
- **Contenção:** com UAC ligado, o processo e seus filhos rodam com o token filtrado (Administradores só para negar); `%ProgramData%\Mestre` não tem entrada para a conta do usuário, e a chave do cofre só para `NT SERVICE\Mestre`. O pedido de elevação aparece na área de trabalho segura, que entrada sintética não alcança (UIPI) ([segurança](seguranca.md#operador-do-pc)).

## ConPTY

- Wrapper próprio sobre `golang.org/x/sys/windows` (`CreatePseudoConsole`, `ResizePseudoConsole`, lista de atributos com `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`), dentro do serviço `mestre` ([ADR 0001](../decisoes/0001-stack-go.md)). O ConPTY do sistema funciona na sessão 0; o `OpenConsole.exe` não é usado.
- Sequência, toda no `mestre`: token e ambiente do [launcher](#iniciador-mestre-launcher); criar os pipes; `CreatePseudoConsole`; `STARTUPINFOEX` com o atributo de pseudo-console; `CreateProcessAsUser` com o token, `EXTENDED_STARTUPINFO_PRESENT` e o processo suspenso; pôr o processo no Job Object do terminal; retomar. O filho **não** herda os handles padrão do pai, senão a saída escapa do pseudo-console.
- Ambiente do filho: o bloco de `CreateEnvironmentBlock` do `mestre-agente` (`USERPROFILE`, `APPDATA` e `LOCALAPPDATA` do perfil dele) + variáveis da base, do perfil de conta e do Mestre (`MESTRE_AGENT_ID`, `MESTRE_CLI` etc.; [sessão zero do personagem](../sessao-zero/personagem.md)).
- **Job Object é obrigatório:** um processo com console próprio sobrevive ao `ClosePseudoConsole`; só o `TerminateJobObject` encerra a árvore, inclusive o `conhost.exe` desse console ([encerramento](terminal.md#encerramento)).
- **`ResizePseudoConsole` redesenha a tela inteira** (o texto antigo volta no fluxo): o emulador e quem procura texto na saída contam com isso ([terminal](terminal.md#emulador-de-terminal-na-máquina)).
- `Ctrl-C` (`\x03`) vira `CTRL_C_EVENT` num processo que não desliga `ENABLE_PROCESSED_INPUT`; os CLIs reais põem o console em modo cru e recebem o byte, e o agente falso faz o mesmo ([dublês](../qualidade/dubles.md#agente-falso-fakeagent)).
- Tamanho fixo, emulador de terminal, buffer e log: iguais aos do [terminal](terminal.md).

## Resultado dos spikes do M1

Rodados no Windows Sandbox em 2026-10-12 ([spikes do M1](../desenvolvimento/ordem-de-implementacao.md#spikes-do-m1); código em `tools/winsandbox/spikes/`):
- **Privilégios da conta virtual:** passou. A LSA concede os dois à conta virtual, e o token do serviço os tem; declarar os exigidos remove o resto (acima).
- **ConPTY como `mestre-agente` a partir da conta virtual:** falhou no padrão (`LoadUserProfile` recusado à conta virtual, com ou sem `SeBackupPrivilege` e `SeRestorePrivilege`) e passou com a **entrega do token** pelo launcher, adotada acima. A variante em que o LocalSystem só segura o perfil e a conta virtual faz o próprio logon falha no `CreateEnvironmentBlock` e no Gerenciador de Credenciais.
- **Login dos CLIs no perfil do `mestre-agente`:** passou com o perfil carregado pelo LocalSystem (claude e codex em arquivo; agy no Gerenciador de Credenciais). Ficou o item do `agy` abaixo.

## Verificar na implementação

Conferidos no laboratório da [suíte pesada](../qualidade/testes.md#laboratório) (VM Windows, como Cidadela e como Torre) e, nas máquinas de uso, pelo [checklist](../plataforma/checklist.md). A identificação do chamador pela árvore de processos, quarto spike de Windows do M1, está em [segurança](seguranca.md#verificar-na-implementação).

- **Verificar na implementação:** o `agy` mantém o login quando roda com o token entregue pelo launcher (no spike, a primeira execução por esse caminho disse "not logged in" e a credencial `gemini:antigravity` sumiu do Gerenciador de Credenciais). **Padrão:** mantém; o sumiço foi uma renovação que falhou. **Se falhar:** para o `agy`, o `mestre-launcher` passa a criar ele mesmo o pseudo-console e o processo na sessão de logon dele e devolve ao `mestre` as pontas dos pipes (`DuplicateHandle`), com redimensionar e encerrar pelo mesmo pipe; o `claude` e o `codex` continuam pela entrega do token. **Onde:** rodada curta no [Windows Sandbox](../desenvolvimento/ambiente.md#windows-sandbox-para-os-spikes-do-m1) com login novo do `agy` e a sequência `cmdkey /list` → `agy -p` pelo token entregue → `cmdkey /list`; depois, a [conformidade](../provedores/conformidade.md) do Antigravity no Windows.
- **Verificar na implementação:** git aceita, como `mestre-agente`, worktrees cujo dono é `NT SERVICE\Mestre` (proteção "dubious ownership"); o mesmo vale no Linux para `mestre-agente` sobre pastas de `mestre`. **Padrão:** o `gitconfig` do personagem ([identidades e Apps](../github/identidades-e-apps.md#autoria-dos-commits)) inclui `safe.directory` para o clone principal e os worktrees daquele personagem. **Se falhar:** a instalação torna `mestre-agente` dona de `workspaces\` (no Linux, de `workspaces/`), o motor mantém modificação por ACL ou grupo e grava `safe.directory` na configuração git dele. **Onde:** suíte completa no Windows e no Linux (commit do agente falso num worktree da Cidadela).
- **Verificar na implementação:** requisitos do `--chrome` a partir do terminal do Operador: mesma conta Claude na CLI e na extensão Claude in Chrome, Chrome aberto na mesma sessão do Windows, extensão instalada. **Padrão:** o processo da sessão confere os três antes de a máquina anunciar `operador`, e a configuração inicial do Operador no painel guia o que faltar. **Se falhar:** o Operador inicia sem `--chrome` e usa só o MCP de controle do computador, e o painel mostra o motivo. **Onde:** spike no começo do Operador (M7), na VM Windows do laboratório, e no `TestMVP14_PCOperator`.
- **Verificar na implementação:** se o `--chrome` funciona com um perfil Claude próprio do Operador (`CLAUDE_CONFIG_DIR` em `%LOCALAPPDATA%\Mestre\Operador\claude\`) logado na mesma conta da extensão, ou só com o perfil Claude da conta do usuário. **Padrão:** perfil próprio. **Se falhar:** o Operador usa o perfil Claude do usuário, e a camada do Mestre e os hooks entram só por argumentos (`--settings` e `--append-system-prompt-file` apontando para a pasta da sessão), sem alterar os arquivos do usuário. **Onde:** o mesmo spike do M7.
- **Verificar na implementação:** o MCP de controle do computador instalado pelo usuário consegue capturar a tela e enviar mouse e teclado quando iniciado como filho do processo da sessão (ConPTY, mesma sessão e área de trabalho do usuário, integridade média). **Padrão:** consegue. **Se falhar:** o processo da sessão passa a criar o terminal do Operador com `CreateProcess` comum anexado à área de trabalho `winsta0\default` e o ConPTY só para entrada e saída; se ainda falhar, o Operador fica só com o Chrome (`--chrome`) e o painel explica. **Onde:** o mesmo spike do M7.

## Fontes

- Sessão ConPTY (Microsoft): https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session
- CreateProcessAsUser: https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessasuserw
- Contas de serviço (contas virtuais): https://learn.microsoft.com/en-us/windows-server/identity/ad-ds/manage/understand-service-accounts
- x/sys/windows: https://pkg.go.dev/golang.org/x/sys/windows
- Wrappers de referência: https://github.com/aymanbagabas/go-pty , https://pkg.go.dev/github.com/UserExistsError/conpty
- Notificações de sessão (`WTSRegisterSessionNotification`): https://learn.microsoft.com/en-us/windows/win32/api/wtsapi32/nf-wtsapi32-wtsregistersessionnotification
- `LoadUserProfile` (restrito a administrador e LocalSystem): https://learn.microsoft.com/en-us/windows/win32/api/userenv/nf-userenv-loaduserprofilew
- Privilégios exigidos de serviço: https://learn.microsoft.com/en-us/windows/win32/api/winsvc/ns-winsvc-service_required_privileges_infow
- `GetNamedPipeClientProcessId`: https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-getnamedpipeclientprocessid
