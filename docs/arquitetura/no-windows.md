# Nó Windows

> **Assunto:** serviço `mestre-node`, processo na sessão do usuário, ConPTY, named pipe e capacidade `gui` · **Componente:** Node · **Atualizado:** 2026-10-12

## Problema

Serviços do Windows rodam na **sessão 0**, sem área de trabalho. Um agente que precisa de interface gráfica (ou o navegador dedicado que cria GitHub Apps) não aparece nem funciona ali. Ao mesmo tempo, o nó precisa subir com o PC, sem janela, e continuar rodando agentes sem GUI mesmo com ninguém logado.

## Desenho

O nó Windows é formado por dois processos do mesmo binário `mestre.exe` ([componentes](componentes.md)):

| Processo | Onde roda | Conta | O que faz |
|---|---|---|---|
| **Serviço `mestre-node`** (`mestre node`) | sessão 0, sobe com o PC | a do usuário ("Fazer logon como", configurado na instalação) | conexão WSS com o Core, banco local, worktrees, terminais **sem GUI** (ConPTY no próprio serviço), encaminhamento da CLI |
| **Processo de sessão** (`mestre node session`) | sessão interativa do usuário | o usuário logado | terminais com `gui: true` (ConPTY), navegador dedicado, redirect local do manifesto de App |

O serviço roda com a conta do usuário para que os CLIs (`claude`, `codex`, `agy`) encontrem o perfil e as credenciais dele, inclusive nas pastas de conta separadas ([bases e fichas](../orquestracao/bases-e-fichas.md)).

| Situação | Agentes sem GUI | Agentes `gui: true` e navegador dedicado |
|---|---|---|
| Usuário logado (ou login automático) | funcionam | funcionam |
| Ninguém logado | funcionam | **não** funcionam; criação falha com explicação ([personagens](../orquestracao/personagens.md)) |

## Como o processo de sessão é iniciado (proposta)

`WTSQueryUserToken` exige o privilégio `SeTcbPrivilege`, que só a conta LocalSystem tem por padrão. Como o serviço roda com a conta do usuário, ele **não** pode usar `WTSQueryUserToken` + `CreateProcessAsUser`. Proposta:

1. A instalação cria a tarefa agendada `\Mestre\Sessao`: gatilho "ao fazer logon" daquele usuário, principal = o usuário, modo "somente quando o usuário estiver conectado" (token interativo), ação `mestre.exe node session`.
2. No logon, o Agendador de Tarefas inicia o processo na sessão do usuário. Nenhum privilégio especial é necessário.
3. O serviço aceita notificações de sessão (`SERVICE_ACCEPT_SESSIONCHANGE`). Se o usuário está logado e o processo de sessão não está conectado (caiu ou o serviço reiniciou), o serviço dispara a tarefa de novo (`schtasks /run /tn \Mestre\Sessao`), com backoff.

Outra tarefa agendada, `\Mestre\Diagnostico`, também criada pela instalação e conferida pelo [checklist](../plataforma/checklist.md), roda o agente de diagnóstico como o usuário, conectado ou não; quem a dispara é o Guardião ([diagnóstico com IA](../autocura/diagnostico-com-ia.md), [instalação](../plataforma/instalacao-e-atualizacao.md#windows)).

Alternativa registrada: o Guardião (LocalSystem) iniciar o processo com `WTSQueryUserToken` + `CreateProcessAsUser`. Não é a preferida porque aumenta o código do Guardião, que deve ser mínimo ([Guardião](../autocura/guardiao.md)).

```mermaid
sequenceDiagram
  participant SO as Windows
  participant S as Serviço mestre-node (sessão 0)
  participant T as Agendador de Tarefas
  participant P as Processo de sessão
  participant C as Core
  SO->>S: inicia no boot (conta do usuário)
  S->>C: WSS, capacidades com gui=false
  SO->>T: usuário fez logon
  T->>P: inicia mestre node session (token interativo)
  P->>S: conecta no named pipe
  S->>S: valida o cliente do pipe
  S->>C: atualiza capacidades: gui=true
  Note over P,S: terminais gui e navegador rodam em P
  SO-->>P: logoff encerra P
  S->>C: gui=false; terminais gui marcados interrompidos
```

## Named pipe entre serviço e sessão

- Nome `\\.\pipe\mestre-node-session` (padrão). O serviço é o servidor; o processo de sessão é o cliente.
- ACL: só o SID do usuário do serviço; `PIPE_REJECT_REMOTE_CLIENTS` (sem acesso pela rede).
- Validação do cliente: o serviço obtém o PID com `GetNamedPipeClientProcessId` e confere que o executável é o `mestre.exe` instalado, que a sessão do processo não é a 0 e que o processo pai é o serviço do Agendador de Tarefas. Só uma conexão de sessão por vez; uma segunda é recusada e gera alerta.
- Mensagens no mesmo envelope do [protocolo](../conectividade/protocolo.md): abrir e fechar terminal, entrada, redimensionar, saída do terminal, fim de processo, abrir o navegador dedicado, heartbeat.
- O processo de sessão não fala com o Core nem guarda estado: tudo passa pelo serviço.

## Capacidade `gui`

O nó anuncia `gui: true` **só enquanto** o processo de sessão estiver conectado e saudável; ao perder a conexão, anuncia `gui: false` na hora. O formato do anúncio está no [protocolo](../conectividade/protocolo.md); a escolha de nó por capacidade, em [personagens](../orquestracao/personagens.md).

Quando o processo de sessão termina (logoff, queda):
- os terminais `gui: true` são marcados **interrompidos** e o Mestre da mesa é avisado; nenhum some em silêncio;
- no próximo logon, os personagens persistentes interrompidos são retomados conforme [personagens](../orquestracao/personagens.md);
- a criação de Apps cai para o link pelo Telegram ([criação de Apps](../github/criacao-de-apps.md)).

## ConPTY

- Wrapper próprio sobre `golang.org/x/sys/windows` (`CreatePseudoConsole`, `ResizePseudoConsole`, lista de atributos com `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`), usado pelos dois processos ([ADR 0001](../decisoes/0001-stack-go.md)).
- Sequência da Microsoft: criar os pipes, `CreatePseudoConsole`, `STARTUPINFOEX` com o atributo, `CreateProcess`. O filho **não** herda os handles padrão do pai, senão a saída escapa do pseudo-console.
- Ambiente do filho: `CreateEnvironmentBlock` do usuário + variáveis da base e as do Mestre (`MESTRE_AGENT_ID`, `MESTRE_CLI` etc.; [sessão zero do personagem](../sessao-zero/personagem.md)).
- Saída: buffer circular (para reenviar ao reconectar o painel) → WSS → xterm.js ([protocolo](../conectividade/protocolo.md)).

## Conta do serviço

- A instalação (elevada) pede a senha da conta do usuário, concede o direito "Fazer logon como serviço" (`SeServiceLogonRight`) e configura o serviço com conta e senha. Quem guarda a senha é o próprio Windows (gerenciador de serviços); o Mestre não a grava. Passo a passo: [instalar](../operacao/instalar.md); visão técnica: [instalação e atualização](../plataforma/instalacao-e-atualizacao.md).
- Se a senha mudar, o serviço deixa de subir (erro de logon). O Guardião (LocalSystem) detecta e alerta; o [checklist](../plataforma/checklist.md) explica como refazer a configuração.
- O Guardião do nó roda como LocalSystem, para poder reiniciar e trocar a versão do motor ([Guardião](../autocura/guardiao.md)).
- Dados do nó em `%ProgramData%\Mestre\` ([persistência](persistencia.md)), com ACL para a conta do serviço, SYSTEM e Administradores.

## Verificar na implementação

Cada item é conferido no laboratório da [suíte pesada](../qualidade/testes.md#laboratório) (VM Windows) e, nas máquinas de uso, pelo [checklist](../plataforma/checklist.md).

- **Verificar na implementação:** ConPTY criado dentro do serviço com conta de usuário, na sessão 0, roda `claude`, `codex` e `agy` sem GUI. **Padrão:** como descrito acima (indício a favor: o OpenSSH do Windows usa ConPTY a partir de serviço na sessão 0). **Se falhar:** os terminais sem GUI também passam para o processo de sessão; agentes passam a exigir usuário logado, e o checklist recomenda o login automático. **Onde:** testes de ConPTY da suíte completa no Windows e [conformidade](../provedores/conformidade.md) no nó Windows.
- **Verificar na implementação:** no serviço com conta de usuário, `USERPROFILE`, `APPDATA` e `LOCALAPPDATA` apontam para o perfil do usuário, e DPAPI e o Gerenciador de Credenciais funcionam para os CLIs. **Padrão:** o ambiente vem de `CreateEnvironmentBlock` do usuário, sem ajuste extra. **Se falhar:** o serviço carrega o perfil com `LoadUserProfile` antes de montar o ambiente; o CLI cuja credencial ainda falhar roda só no processo de sessão. **Onde:** conformidade no nó Windows (login de cada provedor válido dentro do serviço).
- **Senha do "Fazer logon como" (decidido):** o instalador pede a senha numa janela própria e a valida com `LogonUser` antes de configurar o serviço e a tarefa `\Mestre\Diagnostico`; senha errada é pedida de novo. Em conta Microsoft, é a senha da conta Microsoft. **Verificar na implementação:** conta Microsoft e conta que entra só por PIN/Windows Hello. **Padrão:** a senha da conta funciona. **Se falhar:** o instalador explica como reativar o login por senha da conta; sem isso, o nó instala no modo em que todos os terminais rodam no processo de sessão (como no item do ConPTY). **Onde:** instalação na VM Windows do laboratório, com os dois tipos de conta.
- **Verificar na implementação:** a tarefa `\Mestre\Sessao` inicia na sessão certa também com login automático, e `schtasks /run` pela conta do serviço funciona sem elevação. **Padrão:** como descrito acima. **Se falhar:** vale a alternativa registrada: o Guardião (LocalSystem) inicia o processo de sessão com `WTSQueryUserToken` + `CreateProcessAsUser`. **Onde:** suíte pesada (MVP-1 e MVP-12 na VM Windows com login automático) e checklist WIN-07/WIN-08.

## Fontes

- Sessão ConPTY (Microsoft): https://learn.microsoft.com/en-us/windows/console/creating-a-pseudoconsole-session
- x/sys/windows: https://pkg.go.dev/golang.org/x/sys/windows
- `WTSQueryUserToken` (exige LocalSystem e `SE_TCB_NAME`): https://learn.microsoft.com/en-us/windows/win32/api/wtsapi32/nf-wtsapi32-wtsqueryusertoken
- Wrappers de referência: https://github.com/aymanbagabas/go-pty , https://pkg.go.dev/github.com/UserExistsError/conpty
