# Windows

> **Assunto:** o Windows como Core ou como nó, sem interface gráfica: serviços, contas, ConPTY, tarefa de diagnóstico e o que verificar · **Componente:** Todos · **Atualizado:** 2026-10-12

## Princípios

- **Só serviços, sem interface gráfica.** Tudo roda como serviço do Windows na sessão 0 e sobe com o PC, com ou sem usuário logado. Não há processo na sessão do usuário, tarefa de logon nem notificação na área de trabalho. O acesso é pelo painel no navegador; os agentes são de terminal e navegam na web pelas ferramentas MCP do próprio provedor ([permissões](../classes/permissoes.md)). O porquê: [ADR 0020](../decisoes/0020-quatro-usos-core-em-linux-ou-windows.md).
- **Agente nunca roda com a conta dona do cofre** ([segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)).
- **CLIs na conta do usuário.** Quem roda agentes é o serviço `mestre-node`, com a conta do usuário, para `claude`, `codex` e `agy` acharem o perfil e os logins dele, inclusive nas pastas de conta separadas ([bases e fichas](../orquestracao/bases-e-fichas.md)).
- Nomes, contas e portas de cada serviço: [componentes](componentes.md#serviços-e-contas-padrão). Pastas: [persistência](persistencia.md#arquivos).

## Windows como nó

| Serviço | Conta | O que faz |
|---|---|---|
| `mestre-node` | a do usuário ("Fazer logon como") | conexão WSS com o Core, banco local, worktrees, terminais dos agentes (ConPTY no próprio serviço), canal da CLI |
| `mestre-guardiao` | LocalSystem | vigia e conserta o `mestre-node`; página reserva ([Guardião](../autocura/guardiao.md)) |

Não há cofre no nó. Motor e agentes dividem a conta do usuário; o risco residual está em [segurança](seguranca.md#contas-do-sistema-e-isolamento-dos-agentes).

## Windows como Core

A máquina do Core Windows tem **dois serviços do motor**, do mesmo `mestre.exe`:

| Serviço | Conta | O que faz |
|---|---|---|
| `mestre-core` | conta virtual `NT SERVICE\MestreCore` | fonte da verdade: cofre, banco do Core, espelhos, painel e API, GitHub, Telegram, WSS dos nós, `whisper-server` e `ffmpeg` |
| `mestre-node` | a do usuário ("Fazer logon como") | **nó local do Core**: roda o Grão-Mestre e os agentes desta máquina |
| `mestre-guardiao` | LocalSystem | vigia os dois serviços do motor; anúncio mDNS ([descoberta](../conectividade/descoberta.md)) |

- **Por que dois:** no Linux, um processo só, como `mestre`, inicia os terminais como `mestre-agente` (`CAP_SETUID`). No Windows, iniciar um processo com outra conta exige o token ou a senha dela; o lado dos agentes vira então um serviço próprio com a conta do usuário, e o cofre fica numa conta que nenhum agente usa.
- **Conta virtual:** `NT SERVICE\MestreCore` nasce com o registro do serviço, sem senha. Ela é dona de `%ProgramData%\Mestre\` (configuração, banco, backups, logs do Core e workspaces). A chave do cofre é protegida por DPAPI com escopo de máquina e o arquivo tem ACL só para essa conta ([cofre](../plataforma/cofre.md#chave-do-cofre)).
- **Nó local:** o `mestre-node` fala com o `mestre-core` pela mesma WSS com mTLS de um nó remoto, em `127.0.0.1:7443`, e é pareado sozinho pela instalação (sem convite manual). Ele trabalha direto no clone principal e nos worktrees das mesas do Core, sem bundle, como o nó local do Linux ([mesas](../orquestracao/mesas.md)).
- **ACLs:** a conta do usuário recebe modificação em `%ProgramData%\Mestre\workspaces\` (espelho, clone principal e worktrees) e em `contas\`, o papel do grupo `mestre-agente` no Linux; nunca em `config\`, `banco\` ou `backups\`. O banco e os dados do nó local ficam em `%ProgramData%\Mestre\no\`, com dono a conta do usuário ([persistência](persistencia.md#arquivos)).
- **CLI:** os agentes chamam o `mestre-node` pelo canal local dele, e o nó encaminha ao Core como qualquer nó ([convenções da CLI](../cli/convencoes.md)).
- **Autocura:** o Guardião vigia os dois serviços. Com o `mestre-core` no ar, a queda do `mestre-node` é tratada como a de qualquer nó; o resto segue as [camadas](../autocura/camadas.md), inclusive o modo máquina única do uso "só Windows".

## Conta do usuário ("Fazer logon como")

- Vale para o `mestre-node` nos dois papéis. A instalação (elevada) pede a senha da conta do usuário, valida com `LogonUser`, concede "Fazer logon como serviço" (`SeServiceLogonRight`) e configura o serviço com conta e senha. Quem guarda a senha é o gerenciador de serviços do Windows; o Mestre não a grava. Passo a passo: [instalar](../operacao/instalar.md); visão técnica: [instalação e versões](../plataforma/instalacao-e-atualizacao.md).
- Em conta Microsoft, é a senha da conta Microsoft, não o PIN do Windows Hello.
- Se a senha mudar, o serviço deixa de subir (erro de logon). O Guardião (LocalSystem) detecta e alerta; o [checklist](../plataforma/checklist.md) explica como refazer a configuração.

## Tarefa `\Mestre\Diagnostico`

**Continua**, em toda máquina Windows (Core ou nó). O agente de diagnóstico ([diagnóstico com IA](../autocura/diagnostico-com-ia.md)) precisa rodar com a conta do usuário, onde estão os logins dos CLIs, justamente quando o `mestre-node` pode estar fora; quem o inicia é o Guardião (LocalSystem). Sem a tarefa, o Guardião precisaria da senha do usuário (que o Mestre não guarda) ou de um usuário logado (`WTSQueryUserToken`), o que contraria "sem sessão". A tarefa roda como o usuário, conectado ou não, sem gatilho; a senha é a mesma da instalação, guardada pelo Agendador de Tarefas.

## ConPTY

- Wrapper próprio sobre `golang.org/x/sys/windows` (`CreatePseudoConsole`, `ResizePseudoConsole`, lista de atributos com `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`), dentro do serviço `mestre-node` ([ADR 0001](../decisoes/0001-stack-go.md)).
- Sequência da Microsoft: criar os pipes, `CreatePseudoConsole`, `STARTUPINFOEX` com o atributo, `CreateProcess`. O filho **não** herda os handles padrão do pai, senão a saída escapa do pseudo-console.
- Ambiente do filho: `CreateEnvironmentBlock` do usuário + variáveis da base e as do Mestre (`MESTRE_AGENT_ID`, `MESTRE_CLI` etc.; [sessão zero do personagem](../sessao-zero/personagem.md)).
- Saída: buffer circular (para reenviar ao reconectar o painel) → WSS → xterm.js ([protocolo](../conectividade/protocolo.md)).

## Verificar na implementação

Cada item é conferido no laboratório da [suíte pesada](../qualidade/testes.md#laboratório) (VM Windows, como Core e como nó) e, nas máquinas de uso, pelo [checklist](../plataforma/checklist.md).

- **Verificar na implementação:** ConPTY criado dentro do serviço com conta de usuário, na sessão 0, roda `claude`, `codex` e `agy`. **Padrão:** como descrito acima (indício a favor: o OpenSSH do Windows usa ConPTY a partir de serviço na sessão 0). **Se falhar:** o Mestre passa a distribuir o `OpenConsole.exe` (a implementação de ConPTY do Windows Terminal, licença MIT) e o usa no lugar da do sistema, pela mesma API. **Onde:** testes de ConPTY da suíte completa no Windows e [conformidade](../provedores/conformidade.md) no Windows.
- **Verificar na implementação:** no serviço com conta de usuário, `USERPROFILE`, `APPDATA` e `LOCALAPPDATA` apontam para o perfil do usuário, e DPAPI e o Gerenciador de Credenciais funcionam para os CLIs. **Padrão:** o ambiente vem de `CreateEnvironmentBlock` do usuário, sem ajuste extra. **Se falhar:** o serviço carrega o perfil com `LoadUserProfile` antes de montar o ambiente; o CLI cuja credencial ainda falhar não é oferecido naquela máquina, e o checklist explica (ALL-10). **Onde:** conformidade no Windows (login de cada provedor válido dentro do serviço).
- **Verificar na implementação:** senha do "Fazer logon como" com conta Microsoft e com conta que entra só por PIN/Windows Hello. **Padrão:** a senha da conta funciona. **Se falhar:** o instalador para e explica como definir ou reativar a senha da conta; sem senha, o serviço não roda com a conta do usuário. **Onde:** instalação na VM Windows do laboratório, com os dois tipos de conta.
- **Verificar na implementação:** git aceita, na conta do usuário, os worktrees do Core Windows cujo dono é `NT SERVICE\MestreCore` (proteção "dubious ownership"); o mesmo vale no Linux para `mestre-agente` sobre pastas de `mestre`. **Padrão:** o `gitconfig` do personagem ([identidades e Apps](../github/identidades-e-apps.md#autoria-dos-commits)) inclui `safe.directory` para o clone principal e os worktrees daquele personagem. **Se falhar:** a instalação torna a conta dos agentes dona de `workspaces\` (no Linux, `mestre-agente`), o motor do Core mantém modificação por ACL ou grupo e grava `safe.directory` na configuração git dele. **Onde:** suíte completa no Windows e no Linux (commit do agente falso num worktree do Core).

## Fontes

- Sessão ConPTY (Microsoft): https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session
- x/sys/windows: https://pkg.go.dev/golang.org/x/sys/windows
- Wrappers de referência: https://github.com/aymanbagabas/go-pty , https://pkg.go.dev/github.com/UserExistsError/conpty
