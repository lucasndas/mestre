# Adaptador de provedor

> **Assunto:** contrato do adaptador, formato interno de evento, mapa de eventos e ferramentas, e o `mestre hook` · **Componente:** Todos · **Atualizado:** 2026-10-12

## Objetivo

Isolar tudo o que muda entre Claude Code, Codex e Antigravity num único ponto. O motor, as classes e a sessão zero falam só com o contrato abaixo. Para adicionar um provedor, basta implementar o contrato, escrever o documento do provedor, criar o agente falso dele ([dublês](../qualidade/dubles.md)) e passar no [teste de conformidade](conformidade.md). O porquê do despachante único está no [ADR 0012](../decisoes/0012-despachante-unico-de-hooks.md).

## Comportamento

### Contrato (interface Go `Adapter`, descrita em prosa)

Cada provedor tem um adaptador registrado pelo seu id (`claude-code`, `codex`, `antigravity`). Terminais sem agente (bases `bash`, `powershell`) usam o **adaptador nulo**: sem hooks, sem camada do Mestre, prontidão só pela tela.

| Operação | Recebe | Devolve | Quando |
|---|---|---|---|
| `BuildLaunch` (montar comando de início) | base, ficha, personagem, perfil de conta, SO do nó, regras efetivas do personagem | executável, argumentos, ambiente, pasta | ao criar ou retomar um personagem |
| `HookFiles` (gerar arquivos de hooks) | worktree, SO do nó, caminho estável do binário `mestre` | lista de arquivos e a parte gerenciada de cada um | ao abrir a mesa ([mesa](../sessao-zero/mesa.md)) e ao regenerar |
| `PreTrust` (pré-confiar pasta) | perfil de conta, caminho do worktree, raiz do repositório | entradas gravadas no arquivo de confiança do provedor | antes do primeiro personagem do worktree; idempotente |
| `DeliverMestreLayer` (entregar camada do Mestre) | caminho de `MESTRE.md` do personagem | argumentos e/ou arquivos extras | junto com `BuildLaunch` ([personagem](../sessao-zero/personagem.md)) |
| `NormalizeHook` (normalizar entrada de hook) | evento nativo, JSON do stdin | evento no formato interno | a cada chamada de `mestre hook` |
| `FormatResponse` (formatar resposta) | evento, decisão ou contexto | stdout e código de saída no formato do provedor | no fim de cada `mestre hook` |
| `ReadUsage` (ler uso e rate limit) | personagem, último evento | registro de uso normalizado | no fim de turno e periodicamente ([limites](../plataforma/limites-de-uso.md)) |
| `Screens` (prontidão e telas interativas) | tela atual do PTY | estado (`starting`, `ready`, `busy`) e tela reconhecida com a ação | a cada mudança da tela |

Regras do contrato: nenhuma operação escreve no `AGENTS.md`; toda geração é idempotente e mescla só a parte gerenciada; nenhum segredo vai em argumento de linha de comando (fica visível na lista de processos).

### Formato interno normalizado de evento

| Campo | Conteúdo |
|---|---|
| `version` | versão do formato (1) |
| `provider`, `native_event` | id do provedor e nome nativo do evento |
| `event` | evento do Mestre (tabela seguinte) |
| `agent_id`, `table_id` | de `MESTRE_AGENT_ID` e `MESTRE_MESA` |
| `session_id` | `session_id` (Claude, Codex) ou `conversationId` (Antigravity) |
| `turn_id` | `prompt_id` (Claude), `turn_id` (Codex), `executionNum` (Antigravity) |
| `cwd` | `cwd` (Claude, Codex); no Antigravity, `toolCall.args.Cwd` ou o primeiro de `workspacePaths` |
| `transcript_path`, `model` | quando o provedor informa |
| `tool` | `native_name`, `kind`, `command`, `paths[]` (absolutos, normalizados), `url`, `raw_input` |
| `tool_result` | só em `post_tool`: `ok`, `error`, `output` (truncado em 16 KiB, padrão) |
| `prompt`, `last_message` | texto do turno e última resposta, quando existem (truncados) |
| `stop_reason`, `error` | `normal`, `interrupted`, `max_steps`, `rate_limit`, `auth`, `error` e o detalhe nativo |
| `usage` | só em `usage`: tokens, custo estimado, % de contexto, janelas de limite (`window`, `used_pct`, `resets_at`) |
| `received_at` | instante pelo relógio do nó |

Os caminhos são normalizados (absolutos, sem `..`, com links resolvidos; sem diferença de maiúsculas no Windows) antes de qualquer decisão.

### Mapa de eventos

| Evento do Mestre | Claude Code | Codex | Antigravity |
|---|---|---|---|
| `session_start` | SessionStart | SessionStart | derivado do primeiro PreInvocation da conversa |
| `turn_start` | UserPromptSubmit | UserPromptSubmit | PreInvocation |
| `pre_tool` | PreToolUse | PreToolUse | PreToolUse |
| `post_tool` | PostToolUse, PostToolUseFailure | PostToolUse | PostToolUse |
| `permission_request` | PermissionRequest | PermissionRequest | — (tela nativa, ver [antigravity](antigravity.md)) |
| `turn_end` | Stop | Stop | Stop (PostInvocation só registra a invocação) |
| `turn_failure` | StopFailure | — (lido do transcript no Stop) | Stop com `terminationReason = error` |
| `interrupt` | — (tela) | Interrupt | — |
| `pre_compact` | PreCompact | PreCompact | — |
| `session_end` | SessionEnd | SessionEnd | — (fim do processo no PTY) |
| `notification` | Notification | — | — |
| `usage` | statusLine (pseudo-evento) | `token_count` no transcript | `agy -p "/usage"` |

Mapa atualizado pela pesquisa de 2026-10-12. O Antigravity só tem cinco eventos: PreToolUse, PostToolUse, PreInvocation, PostInvocation e Stop.

### Ferramentas: nome nativo → tipo (`tool.kind`)

| Tipo | Claude Code | Codex | Antigravity |
|---|---|---|---|
| `shell` | `Bash`, `PowerShell` no Windows | `Bash` | `run_command` |
| `read` | `Read` | — | `view_file` |
| `write` | `Write` | `apply_patch` (arquivo novo) | `write_to_file` |
| `edit` | `Edit`, `NotebookEdit` | `apply_patch` | `replace_file_content`, `multi_replace_file_content` |
| `search` | `Glob`, `Grep` | — | `list_dir`, `find_by_name`, `grep_search` |
| `web` | `WebFetch`, `WebSearch` | (busca hospedada não dispara hook) | `search_web`, `read_url_content`, `browser_*` |
| `subagent` | `Agent` | — | `invoke_subagent`, `define_subagent`, `send_message`, `manage_subagents` |
| `mcp` | `mcp__<servidor>__<ferramenta>` | `mcp__…` | o nome visto no hook ([verificar](#verificar-na-implementação)) |
| `internal` | `TodoWrite` | `update_plan` | `manage_task`, `ask_question`, `list_permissions`, `ask_permission` |
| `other` | qualquer nome não mapeado | idem | `generate_image`, `schedule`, não mapeados |

Ferramentas `mcp` (inclusive as de navegador que o agente usa para navegar na web; o Mestre não fornece navegador) e `other` são decididas pelo nome nativo, com os padrões de `tools` da classe ([permissões](../classes/permissoes.md#formato-do-permissionsyaml)). O comando vem de `tool_input.command` (Claude, Codex) ou `args.CommandLine` (Antigravity). Os caminhos vêm de `file_path`/`notebook_path` (Claude), dos cabeçalhos `*** Add/Update/Delete File:` do `apply_patch` (Codex) e dos argumentos de arquivo do Antigravity. Cada provedor tem a sua tabela completa no próprio documento; nomes novos aparecem no teste de conformidade.

### `mestre hook` (despachante)

Uso interno, chamado só pelos arquivos de hooks gerados: `mestre hook --provider <id> --event <evento-nativo>`. Agentes não usam.

1. **Sem `MESTRE_AGENT_ID` no ambiente:** responde neutro e sai na hora, sem ler configuração nem abrir conexão (meta: 50 ms). Neutro é: nada no stdout e exit 0 (Claude, Codex); `{"decision":"allow"}` no PreToolUse e JSON vazio válido nos demais eventos (Antigravity).
2. Lê o stdin (até 4 MiB, padrão; o excesso é descartado e marcado como truncado) e normaliza.
3. Pede ao motor do próprio nó, pelo mesmo canal local da CLI ([convenções da CLI](../cli/convencoes.md)), a decisão (`pre_tool`, `permission_request`) ou o contexto (`session_start`, `turn_start`). A decisão é local: o nó já tem as regras efetivas do personagem, que o Core envia pela mensagem `agent.rules` do [protocolo](../conectividade/protocolo.md), e aplica o [pipeline de permissões](../classes/permissoes.md) sem ida ao Core. A resposta traz também os hooks síncronos da classe a rodar ([classes](../classes/classes.md)).
4. **Prazo:** a decisão do despachante sai em menos de 1 s (padrão: 800 ms para `pre_tool` e `permission_request`; 300 ms para eventos só informativos, que o motor confirma e processa depois). Hooks síncronos da classe podem usar mais tempo, até o tempo limite deles, sempre 2 s abaixo do tempo limite configurado no provedor.
5. Formata a resposta no formato do provedor (tabela abaixo) e sai.

**Motor fora do ar** (sem conexão ou sem resposta no prazo): modo degradado. Valem só as regras locais que o hook checa sozinho: as regras fixas de segurança embutidas no binário e o retrato das regras efetivas do personagem ([permissões](../classes/permissoes.md#modo-degradado)). O que elas negarem é negado; o que pediria aprovação é negado com o motivo "motor indisponível, tente depois"; o resto é liberado. O evento vai para uma fila local no disco do nó e é reenviado quando o motor voltar. Em `turn_start`, o turno segue sem contexto novo.

**Erro interno do próprio hook:** em `pre_tool` e `permission_request`, bloqueia (Claude e Codex: exit 2 com o motivo no stderr; Antigravity: decisão `deny`); nos demais eventos, exit 0 sem saída (no Antigravity, exit 0 com a resposta neutra em JSON: o despachante trata todos os eventos dele como falha fechada, sem depender de testar cada um).

| Resposta | Claude Code | Codex | Antigravity |
|---|---|---|---|
| liberar | exit 0, `permissionDecision: allow` | exit 0, `permissionDecision: allow` | `{"decision":"allow"}` |
| negar | exit 0, `permissionDecision: deny` + motivo | exit 0, `permissionDecision: deny` + motivo | `{"decision":"deny","reason":…}` |
| permissão nativa | `decision.behavior` no PermissionRequest | `decision.behavior` no PermissionRequest | — |
| contexto do turno | `additionalContext` | `additionalContext` | `injectSteps[{ephemeralMessage}]` no PreInvocation |
| fim de turno | exit 0, sem saída | exit 0, sem saída | `{}` |

No Claude, nunca usar exit 1 para bloquear: exit 1 não bloqueia. No Antigravity, toda resposta precisa ser JSON válido com decisão explícita no PreToolUse, porque `{}`, saída inválida, exit diferente de 0 ou tempo esgotado bloqueiam.

### Monitoramento reduzido

O personagem fica marcado com monitoramento reduzido, com o motivo, quando o provedor não tem algum evento do mapa (o Antigravity sempre) ou quando nenhum hook chega em 60 s (padrão) depois do primeiro prompt entregue (hooks não carregados, pasta sem confiança). No segundo caso o motor também gera um alerta do [checklist](../plataforma/checklist.md). O canvas mostra a marca ([canvas](../painel/canvas.md)). O plano B (contexto na primeira mensagem, resumo do estado salvo a cada `turn_end`) está em [personagem](../sessao-zero/personagem.md) e [personagens](../orquestracao/personagens.md).

### Prontidão e telas interativas

Cada adaptador traz uma lista de telas conhecidas: id, padrão de texto na tela renderizada e ação. **Evitar:** pré-configurar para a tela não aparecer. **Responder:** o adaptador digita a resposta, só quando ela vem de uma decisão já tomada pelo motor (ex.: confiar num worktree criado pelo Mestre; responder a um pedido de permissão nativo com a decisão já registrada para aquela chamada; sem decisão registrada, nega). **Escalar:** o personagem fica "aguardando entrada" e o Mestre da mesa é avisado (pode responder com `mestre ask --raw`); login vira alerta do checklist. Tela parada sem hook e sem padrão conhecido por 30 s (padrão) durante o início também escala. "Pronto" é: prompt de entrada visível e tela estável por 500 ms, ou `session_start` recebido e prompt visível.

## Regras e erros

- Versão do provedor abaixo da mínima do documento dele: a base é recusada naquele nó com a explicação, e o checklist avisa.
- Evento nativo desconhecido: registrado no log e respondido com a resposta neutra.
- Ferramenta não mapeada: tipo `other`, decidida pelo padrão da classe ([permissões](../classes/permissoes.md)).
- Fila local de eventos cheia (10.000 eventos, padrão): descarta os mais antigos de tipo informativo, nunca decisões já tomadas.

## Critérios de aceite

- **CA-ADAPT-01** · *rápida* — **Dado** um payload gravado de cada provedor para cada evento do mapa, **quando** `NormalizeHook` roda, **então** o evento interno tem `event`, `session_id`, `turn_id` e, em eventos de ferramenta, `tool.kind`, `command` ou `paths` preenchidos conforme as tabelas.
- **CA-ADAPT-02** · *rápida* — **Dado** o ambiente sem `MESTRE_AGENT_ID`, **quando** `mestre hook` roda para qualquer provedor e evento, **então** devolve a resposta neutra do provedor em menos de 50 ms, sem abrir conexão.
- **CA-ADAPT-03** · *rápida* — **Dado** um motor falso que não responde, **quando** chega um `pre_tool` que as regras locais não negam, **então** a resposta é liberar em menos de 1 s e o evento fica na fila local.
- **CA-ADAPT-04** · *rápida* — **Dado** um motor falso que não responde, **quando** chega um `pre_tool` de `git push`, **então** a resposta é negar com motivo.
- **CA-ADAPT-05** · *rápida* — **Dado** um erro interno simulado em `pre_tool`, **quando** o hook termina, **então** sai com exit 2 (Claude, Codex) ou `deny` (Antigravity), e nunca com exit 1.
- **CA-ADAPT-06** · *rápida* — **Dado** cada decisão (liberar, negar), **quando** `FormatResponse` formata para o Antigravity, **então** o stdout é JSON válido com `decision` explícito.
- **CA-ADAPT-07** · *completa* — **Dado** um agente falso que nunca dispara hooks, **quando** passam 60 s (relógio falso) após o primeiro prompt, **então** o personagem fica com monitoramento reduzido e o checklist registra o alerta.

## Depende de

- [Componentes](../arquitetura/componentes.md)
- [Convenções da CLI](../cli/convencoes.md) e [protocolo](../conectividade/protocolo.md)
- [Permissões](../classes/permissoes.md) e [classes](../classes/classes.md)
- [Sessão zero da mesa](../sessao-zero/mesa.md) e [do personagem](../sessao-zero/personagem.md)
- [Bases e fichas](../orquestracao/bases-e-fichas.md)

## Verificar na implementação

- **Verificar na implementação:** nomes dos campos de caminho nos argumentos do Antigravity, nome da ferramenta de shell do Codex e do Claude no Windows, e o nome com que o Antigravity entrega ferramentas MCP ao hook. **Padrão:** o mapa usa os nomes vistos nos payloads gravados (no Antigravity, `CommandLine` e `Cwd` já são conhecidos). **Se falhar:** campo ou ferramenta fora do mapa vira tipo `other`, decidido pelo padrão da classe (regra acima), e o reforço nativo segue valendo. **Onde:** etapas 4 a 6 do [teste de conformidade](conformidade.md), que gravam os payloads.

## Fontes

- https://code.claude.com/docs/en/hooks
- https://learn.chatgpt.com/docs/hooks
- https://antigravity.google/docs/hooks
