# Claude Code

> **Assunto:** como o Mestre inicia, instrui, vigia e mede agentes Claude Code · **Componente:** Todos · **Atualizado:** 2026-10-12

## Objetivo

Registrar o que o [adaptador](adaptador.md) do Claude Code precisa saber: arquivos lidos, canal da camada do Mestre, hooks, confiança, flags, contas, uso e telas. Pesquisado na versão **2.1.296** (instalada) e na documentação oficial de 2026-10-12. **Versão mínima: 2.1.295** (por causa de `onFailure`).

## Comportamento

### Instruções do projeto

- Lê `AGENTS.md` nativamente desde a v2.1.277 (plugin embutido `cc-plugin-agents-md@builtin`), da pasta atual para cima.
- Se existir `CLAUDE.md`, `.claude/CLAUDE.md` ou `CLAUDE.local.md` na pasta atual ou acima, lê **só** esses e ignora o `AGENTS.md`. Um `CLAUDE.md` com `@AGENTS.md` carrega os dois. `~/.claude/CLAUDE.md` e `.claude/rules/` não contam e carregam junto.
- O modo pode mudar (`instructionFiles`), mas só em configuração de usuário, `--settings` ou gerenciada. O Mestre não muda: o projeto decide. O aviso de "Claude lê arquivo diferente" é da [sessão zero do workspace](../sessao-zero/workspace.md).
- Import `@arquivo` que aponta para fora da pasta de trabalho abre uma tela de aprovação (ver Telas).

### Camada do Mestre

- Canal: `--append-system-prompt-file .mestre/agents/<id>/MESTRE.md`. Funciona no modo interativo e entra no prompt de sistema, acima do `AGENTS.md`.
- Atualização a cada turno (decretos): `additionalContext` do UserPromptSubmit; o Claude mostra como lembrete do sistema. Limite do provedor: 10.000 caracteres.
- Montagem do arquivo e reserva pela primeira mensagem: [personagem](../sessao-zero/personagem.md).
- Sessão retomada: o Mestre não usa `--system-prompt-snapshot`; o prompt gravado é reaproveitado, e mudanças na camada do Mestre entram pelo contexto do turno.

### Skills

- Só lê `.claude/skills/<nome>/SKILL.md` (e `~/.claude/skills/`, não usada pelo Mestre). `.agents/skills/` não aparece na documentação: tratar como não lida. Por isso o Mestre mantém a cópia gerenciada ([mesa](../sessao-zero/mesa.md), [ADR 0011](../decisoes/0011-agents-skills-pasta-oficial.md)).
- Num worktree, a busca para na raiz do worktree: a cópia precisa existir em cada worktree, **antes** de o agente iniciar (pasta criada depois exige `/reload-skills`; edições em pasta existente recarregam sozinhas).
- `description` (com `when_to_use`) é cortada em 1.536 caracteres.

### Hooks

- **Arquivo:** `.claude/settings.local.json`, chave `hooks`, parte gerenciada mesclada ([mesa](../sessao-zero/mesa.md)). Hooks de várias fontes somam; handler idêntico roda uma vez; edições recarregam sem reiniciar.
- **Eventos ligados:** SessionStart, UserPromptSubmit, PreToolUse, PermissionRequest, PostToolUse, PostToolUseFailure, Stop, StopFailure, Notification, PreCompact, SessionEnd. Matcher `*` nos eventos de ferramenta.
- **Entrada:** JSON no stdin, `snake_case`: `session_id`, `prompt_id`, `transcript_path`, `cwd`, `hook_event_name`, `permission_mode`; PreToolUse traz `tool_name`, `tool_input`, `tool_use_id`; Stop traz `last_assistant_message`; StopFailure traz `error` e `error_details`.
- **Saída:** exit 0 com JSON. PreToolUse: `hookSpecificOutput.permissionDecision` (`allow|deny|ask|defer`) e `permissionDecisionReason`; `allow` não passa por cima de regras nativas `deny`/`ask`. PermissionRequest: `hookSpecificOutput.decision.behavior`.
- **Falha:** exit 2 bloqueia (motivo no stderr); **exit 1 não bloqueia**; erro ou tempo esgotado liberam (falha aberta), a não ser com `"onFailure":"block"`, usado só em PreToolUse e PermissionRequest.
- **Tempos limite gerados:** 30 s em PreToolUse, PermissionRequest e UserPromptSubmit; 60 s nos demais. SessionEnd tem orçamento total de 1,5 s: o despachante só registra o evento.
- Hooks rodam sem TTY e herdam o ambiente do agente (inclusive `MESTRE_AGENT_ID`), menos `OTEL_*`.

```json
{"hooks": {"PreToolUse": [{"matcher": "*", "hooks": [{"type": "command",
  "command": "mestre hook --provider claude-code --event PreToolUse",
  "timeout": 30, "onFailure": "block"}]}]}}
```

### Confiança de pasta

- No modo interativo, sem confiança na pasta, **nenhum hook de arquivo de configuração e nenhuma statusLine rodam**, nem os do usuário.
- A chave é a raiz do repositório git; num worktree, a raiz do checkout principal. A pasta pessoal (home) nunca fica confiada.
- **Pré-confiar:** gravar `projects["<caminho absoluto>"].hasTrustDialogAccepted = true` no `.claude.json` do perfil de conta. O motor grava para o caminho do worktree e para a raiz que o Claude resolver, com escrita atômica (o Claude reescreve esse arquivo e guarda cópias em `backups/`). Quando acontece: [mesa](../sessao-zero/mesa.md).

### Flags

| Usadas pela base | Para quê |
|---|---|
| `--append-system-prompt-file <MESTRE.md>` | camada do Mestre |
| `--settings .mestre/agents/<id>/claude-settings.json` | regras nativas da classe e statusLine do personagem ([permissões](../classes/permissoes.md)) |
| `--session-id <uuid>` / `--resume <uuid>` | id gerado pelo motor, liga hooks ao PTY; retomada |
| `--permission-mode default` | único modo permitido |

**Proibidas:** `--dangerously-skip-permissions` e os modos `bypassPermissions`, `auto`, `dontAsk` e `acceptEdits`; `--bare` (pula hooks, skills e instruções); `--system-prompt[-file]` (substitui o prompt padrão); `--setting-sources` (pode descartar os hooks locais); `--add-dir` com qualquer pasta que não seja `.mestre/agents/<id>` do próprio personagem (essa única é passada pelo motor para as skills por personagem, ver [personagem](../sessao-zero/personagem.md)); `-p` (a base é interativa).

### Contas múltiplas

- `CLAUDE_CONFIG_DIR=<pasta do perfil>` no ambiente do processo: configuração, histórico e login próprios (`.credentials.json` dentro dela). O perfil de conta é definido em [bases e fichas](../orquestracao/bases-e-fichas.md).
- Verificar login: `claude auth status` (JSON com `configDirectory` e `authMethod`), usado pelo [checklist](../plataforma/checklist.md).

### Uso, custo e rate limit

- **statusLine** (no arquivo `--settings` do personagem, chamando `mestre hook --provider claude-code --event StatusLine`): recebe `cost.total_cost_usd` (estimativa pelo preço de lista, zera no `/clear`), `context_window.*` (tokens, `used_percentage`) e `rate_limits.five_hour` / `seven_day` (`used_percentage`, `resets_at`; só planos Pro/Max, depois da primeira resposta). Roda a cada mensagem, com intervalo mínimo de 300 ms.
- **StopFailure** com `error = rate_limit` (também `overloaded`, `billing_error`, `authentication_failed`): sinal estruturado de limite.
- **Notification** `quota_auto_resume_fired`, `quota_auto_resume_stale`, `quota_auto_resume_disabled`: a espera pelo limite continuou, parou esperando Enter ou terminou.
- Reserva pela tela: `You've hit your session limit · resets …`, `Usage limit reached`, `API Error: Request rejected (429)`.
- A continuação automática após o limite vem ligada; o personagem fica "aguardando cota" até ela disparar ([limites](../plataforma/limites-de-uso.md)).

### Telas que o PTY precisa tratar

| Tela | Ação |
|---|---|
| confiar na pasta | evitar (pré-confiança); se aparecer num worktree do Mestre, responder "confiar" |
| aprovação de import externo | escalar |
| aprovação de servidores do `.mcp.json` | escalar |
| "usar esta `ANTHROPIC_API_KEY`?" | escalar (uma vez por perfil) |
| menu de opções do limite de uso | responder com a opção de esperar o reinício do limite |
| espera parada após o computador dormir (`quota_auto_resume_stale`) | responder Enter quando o motor confirmar que o limite reiniciou |
| pedido de permissão nativo | responder com a decisão registrada; sem decisão, negar |
| primeiro uso e login | escalar como alerta do checklist |

## Regras e erros

- Versão abaixo de 2.1.295: base recusada no nó (sem `onFailure`, o despachante falharia aberto).
- Pasta sem confiança detectada (tela de confiança ou ausência de hooks): ver monitoramento reduzido no [adaptador](adaptador.md#monitoramento-reduzido).
- Se o `.claude.json` estiver sendo reescrito pelo Claude, a pré-confiança relê, mescla e grava de novo (até 3 tentativas) e confere o resultado.

## Critérios de aceite

- **CA-CLAUDE-01** · *rápida* — **Dado** um `settings.local.json` com hooks do usuário, **quando** `HookFiles` roda duas vezes, **então** o arquivo tem os hooks do usuário intactos, a parte gerenciada uma vez só, matcher `*` e `onFailure: block` só em PreToolUse e PermissionRequest.
- **CA-CLAUDE-02** · *rápida* — **Dado** qualquer base e ficha válidas, **quando** `BuildLaunch` monta o comando, **então** ele contém `--append-system-prompt-file`, `--settings` e `CLAUDE_CONFIG_DIR`, e nenhuma flag proibida.
- **CA-CLAUDE-03** · *rápida* — **Dado** um `.claude.json` com outras chaves, **quando** `PreTrust` roda duas vezes, **então** o worktree fica com `hasTrustDialogAccepted = true` e o resto do arquivo não muda.
- **CA-CLAUDE-04** · *rápida* — **Dado** um JSON de statusLine gravado com `rate_limits`, **quando** `ReadUsage` lê, **então** o registro de uso tem custo, % de contexto e as duas janelas com `used_pct` e `resets_at`.
- **CA-CLAUDE-05** · *completa* — **Dado** o agente falso do Claude mostrando o menu de limite de uso, **quando** o adaptador reconhece a tela, **então** responde a opção de esperar e o personagem fica "aguardando cota".

## Depende de

- [Adaptador](adaptador.md)
- [Sessão zero da mesa](../sessao-zero/mesa.md) e [do personagem](../sessao-zero/personagem.md)
- [Permissões](../classes/permissoes.md)
- [Bases e fichas](../orquestracao/bases-e-fichas.md)

## Verificar na implementação

Todos são conferidos pelo [teste de conformidade](conformidade.md).

- **Verificar na implementação:** com `CLAUDE_CONFIG_DIR`, o arquivo de confiança é `$CLAUDE_CONFIG_DIR/.claude.json`, e qual chave o Claude usa para um worktree saído do espelho sem área de trabalho. **Padrão:** esse arquivo, com as duas chaves descritas em Confiança de pasta. **Se falhar:** o motor grava também em `.claude.json` na pasta pessoal do processo e usa a chave que o log de debug (`--debug`) mostrar, registrada por versão do CLI. **Onde:** etapa 1 (sem tela de confiança).
- **Verificar na implementação:** textos exatos das opções do menu de limite de uso e da tela de permissão nativa. **Padrão:** os textos de "Uso, custo e rate limit" e a posição das opções. **Se falhar:** a tela não é reconhecida e escala ao Mestre da mesa ([telas](adaptador.md#prontidão-e-telas-interativas)); a transcrição da escalada vira o padrão novo. **Onde:** tela de permissão nas etapas 4 e 5; menu de limite no primeiro limite real, pela transcrição.
- **Verificar na implementação:** nome da ferramenta de shell no Windows. **Padrão:** `PowerShell`, tratado como `shell`. **Se falhar:** o nome capturado entra no mapa; até lá vale a regra de ferramenta não mapeada do [adaptador](adaptador.md#regras-e-erros). **Onde:** etapa 5 no nó Windows.
- **Verificar na implementação:** a statusLine declarada no arquivo `--settings` do personagem roda. **Padrão:** como descrito em Uso. **Se falhar:** a statusLine vai para a parte gerenciada de `.claude/settings.local.json` (o comando é o mesmo para todos; o personagem é identificado pelo `MESTRE_AGENT_ID`). **Onde:** etapa 7.

## Fontes

- https://code.claude.com/docs/en/cli-reference
- https://code.claude.com/docs/en/memory#agents-md
- https://code.claude.com/docs/en/skills
- https://code.claude.com/docs/en/hooks
- https://code.claude.com/docs/en/hooks#workspace-trust
- https://code.claude.com/docs/en/settings
- https://code.claude.com/docs/en/permissions#project-allow-rules-and-workspace-trust
- https://code.claude.com/docs/en/security
- https://code.claude.com/docs/en/authentication
- https://code.claude.com/docs/en/env-vars
- https://code.claude.com/docs/en/statusline#available-data
- https://code.claude.com/docs/en/costs
- https://code.claude.com/docs/en/monitoring-usage
- https://code.claude.com/docs/en/errors#usage-limits
- https://code.claude.com/docs/en/interactive-mode#wait-for-a-usage-limit-to-reset
