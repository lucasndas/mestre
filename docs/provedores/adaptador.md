# Adaptador de provedor

> **Assunto:** contrato do adaptador, formato interno de evento, mapa de eventos e ferramentas, telas, digitação de mensagens e retomada de sessão · **Componente:** Todos · **Atualizado:** 2026-10-14

## Objetivo

Isolar tudo o que muda entre Claude Code, Codex e Antigravity num único ponto. O motor, as classes e a sessão zero falam só com o contrato abaixo. Para adicionar um provedor, basta implementar o contrato, escrever o documento do provedor, criar o agente falso dele ([dublês](../qualidade/dubles.md)) e passar no [teste de conformidade](conformidade.md). O comando `mestre hook`, que usa este contrato a cada hook, está em [despachante de hooks](despachante-de-hooks.md); o porquê do despachante único, no [ADR 0012](../decisoes/0012-despachante-unico-de-hooks.md).

## Comportamento

### Contrato (interface Go `Adapter`, descrita em prosa)

Cada provedor tem um adaptador registrado pelo seu id (`claude-code`, `codex`, `antigravity`). Terminais sem agente (bases `bash`, `powershell`) usam o **adaptador nulo**: sem hooks, sem camada do Mestre, prontidão só pela tela.

| Operação | Recebe | Devolve | Quando |
|---|---|---|---|
| `BuildLaunch` (montar comando de início) | base, ficha, personagem, perfil de conta, SO da máquina, regras efetivas, id de sessão a retomar (opcional) | executável, argumentos, ambiente, pasta | ao criar ou [retomar](#retomada-de-sessão) um personagem |
| `HookFiles` (gerar arquivos de hooks) | worktree ou perfil de conta, SO da máquina, caminho estável do binário `mestre` | lista de arquivos e a parte gerenciada de cada um | ao abrir a mesa ([mesa](../sessao-zero/mesa.md)), ao configurar o perfil e ao regenerar |
| `PreTrust` (pré-confiar pasta) | perfil de conta, caminho do worktree, raiz do repositório | entradas gravadas no arquivo de confiança do provedor | antes do primeiro personagem do worktree; idempotente |
| `DeliverMestreLayer` (entregar camada do Mestre) | caminho de `MESTRE.md` do personagem | argumentos e/ou arquivos extras | junto com `BuildLaunch` ([personagem](../sessao-zero/personagem.md)) |
| `NormalizeHook` (normalizar entrada de hook) | evento nativo, JSON do stdin | evento no formato interno | a cada chamada de `mestre hook` |
| `FormatResponse` (formatar resposta) | evento, decisão ou contexto | stdout e código de saída no formato do provedor | no fim de cada `mestre hook` |
| `ReadUsage` (ler uso e rate limit) | personagem, último evento | registro de uso normalizado | no fim de turno e periodicamente ([limites](../plataforma/limites-de-uso.md)) |
| `Screens` (prontidão e telas interativas) | tela atual, vinda do emulador do [terminal](../arquitetura/terminal.md) | estado (`starting`, `ready`, `busy`) e tela reconhecida com a ação | a cada mudança da tela |
| `TypeMessage` (digitar mensagem) | texto (pode ter várias linhas) | bytes a escrever no terminal | a cada entrega da caixa de entrada ([abaixo](#digitação-de-mensagens)) |

Regras do contrato: nenhuma operação escreve no `AGENTS.md`; toda geração é idempotente e mescla só a parte gerenciada; nenhum segredo vai em argumento de linha de comando (fica visível na lista de processos).

### Formato interno normalizado de evento

| Campo | Conteúdo |
|---|---|
| `version` | versão do formato (1) |
| `provider`, `native_event` | id do provedor e nome nativo do evento |
| `event` | evento do Mestre (tabela seguinte) |
| `agent_id`, `table_id` | de `MESTRE_AGENT_ID` e `MESTRE_MESA` (identificador da mesa, `<número>-<slug>`; [mesas](../orquestracao/mesas.md)) |
| `session_id` | `session_id` (Claude, Codex) ou `conversationId` (Antigravity) |
| `turn_id` | `prompt_id` (Claude), `turn_id` (Codex), `executionNum` (Antigravity); o evento que não traz o campo (statusLine, Notification, PreCompact e SessionEnd do Claude; no Antigravity, todos menos o Stop) recebe do motor o turno em curso, e o PreInvocation abre o seguinte ao último `executionNum`; vazio só em `session_start` |
| `cwd` | `cwd` (Claude, Codex); no Antigravity, `toolCall.args.Cwd` ou o primeiro de `workspacePaths` |
| `transcript_path`, `model` | quando o provedor informa |
| `tool` | `native_name`, `kind`, `command`, `paths[]` (absolutos, normalizados), `url`, `raw_input` |
| `tool_result` | só em `post_tool`: `ok`, `error`, `output` (truncado em 16 KiB, padrão) |
| `prompt`, `last_message` | texto do turno e última resposta, quando existem (truncados em 16 KiB, como o `output`) |
| `stop_reason`, `error` | `normal`, `interrupted`, `max_steps`, `rate_limit`, `auth`, `error` e o detalhe nativo |
| `usage` | só em `usage`: tokens, custo estimado, % de contexto, janelas de limite (`window`, `used_pct`, `resets_at`) |
| `received_at` | instante pelo relógio da máquina |

Os caminhos são normalizados (absolutos, sem `..`, com links resolvidos, inclusive os da pasta de um arquivo que ainda não existe; sem diferença de maiúsculas no Windows) antes de qualquer decisão. O `..` vale onde o sistema o aplica (`NormalizePath`, também para os argumentos dos comandos): no Linux, depois do link à esquerda dele, logo `link/../x` é o `x` ao lado do alvo do link, não ao lado do link; no Windows, que tira o `..` do texto antes de abrir o caminho, antes dos links.

### Mapa de eventos

| Evento do Mestre | Claude Code | Codex | Antigravity |
|---|---|---|---|
| `session_start` | SessionStart | SessionStart | derivado do primeiro PreInvocation da conversa |
| `turn_start` | UserPromptSubmit | UserPromptSubmit | PreInvocation |
| `pre_tool` | PreToolUse | PreToolUse | PreToolUse |
| `post_tool` | PostToolUse, PostToolUseFailure | PostToolUse | PostToolUse |
| `permission_request` | PermissionRequest | PermissionRequest | — (tela nativa, ver [antigravity](antigravity.md)) |
| `turn_end` | Stop | Stop | Stop (PostInvocation só registra a invocação) |
| `turn_failure` | StopFailure | — (lido do transcript, [Codex](codex.md#uso-custo-e-rate-limit)) | Stop com `terminationReason = error` |
| `interrupt` | — (tela) | Interrupt | — |
| `pre_compact` | PreCompact | PreCompact | — |
| `session_end` | SessionEnd | SessionEnd | — (fim do processo no PTY) |
| `notification` | Notification | — | — |
| `usage` | statusLine (pseudo-evento) | `token_count` no transcript | `agy -p "/usage"` |

Mapa atualizado pela pesquisa de 2026-10-12. O Antigravity só tem cinco eventos: PreToolUse, PostToolUse, PreInvocation, PostInvocation e Stop; o primeiro PreInvocation de uma conversa que o terminal vê vira dois eventos, `session_start` e `turn_start`, nessa ordem.

### Ferramentas: nome nativo → tipo (`tool.kind`)

| Tipo | Claude Code | Codex | Antigravity |
|---|---|---|---|
| `shell` | `Bash`, `PowerShell` no Windows | `Bash` (o modelo a chama `exec_command`) | `run_command` (com `BypassSandbox: true`, vira `run_command:BypassSandbox`, de tipo `other`) |
| `read` | `Read` | — | `view_file` |
| `write` | `Write` | `apply_patch` (arquivo novo) | `write_to_file` |
| `edit` | `Edit`, `NotebookEdit` | `apply_patch` | `replace_file_content`, `multi_replace_file_content` |
| `search` | `Glob`, `Grep` | — | `list_dir`, `find_by_name`, `grep_search` |
| `web` | `WebFetch`, `WebSearch` | (busca hospedada não dispara hook) | `search_web`, `read_url_content`, `browser_*` |
| `subagent` | `Agent` | `collaborationspawn_agent`, `collaborationwait_agent` | `invoke_subagent`, `define_subagent`, `send_message`, `manage_subagents` |
| `mcp` | `mcp__<servidor>__<ferramenta>` | `mcp__…` | o nome visto no hook ([verificar](#verificar-na-implementação)) |
| `internal` | `TodoWrite` | `update_plan` | `manage_task`, `ask_question`, `list_permissions`, `ask_permission` |
| `other` | qualquer nome não mapeado | idem | `generate_image`, `schedule`, não mapeados |

Ferramenta de tipo `shell` sem comando, ou `read`, `write`, `edit` ou `search` sem caminho lido de um campo do mapa, vira `other`: nenhuma regra de caminho decide sem ver o caminho. Ferramentas `mcp` (inclusive as do MCP de navegador do personagem) e `other` são decididas pelo nome nativo, com os padrões de `tools` da classe ([permissões](../classes/formato-das-permissoes.md)). O comando vem de `tool_input.command` (Claude, Codex) ou `args.CommandLine` (Antigravity). Os caminhos vêm de `file_path`/`notebook_path` (Claude), dos cabeçalhos `*** Add/Update/Delete File:` do `apply_patch` (Codex) e dos argumentos de arquivo do Antigravity. Cada provedor tem a sua tabela completa no próprio documento; nomes novos aparecem no teste de conformidade.

### Prontidão e telas interativas

Cada adaptador traz uma lista de telas conhecidas: id, padrão de texto na tela renderizada e ação. **Evitar:** pré-configurar para a tela não aparecer. **Responder:** o adaptador digita a resposta, só quando ela vem de uma decisão já tomada pelo motor (ex.: confiar num worktree criado pelo Mestre; responder a um pedido de permissão nativo com a decisão já registrada para a chamada que a tela mostra, usada uma vez; nunca a última decisão do personagem; sem decisão para aquela chamada, nega). **Escalar:** o personagem fica `blocked` com motivo `input` ([personagens](../orquestracao/personagens.md)) e o Mestre da mesa é avisado (pode responder com `mestre ask --raw`); login vira alerta do checklist. Tela parada sem hook e sem padrão conhecido por 30 s (padrão) durante o início também escala. "Pronto" é: prompt de entrada visível (na última linha, ou a área de entrada que o CLI desenha acima do rodapé, descrita na tabela de telas de cada provedor) e tela estável ([terminal](../arquitetura/terminal.md#emulador-de-terminal)), ou `session_start` recebido e prompt visível.

- **Qual tela está na frente:** o modelo pode citar o texto de qualquer diálogo, então uma tela conhecida só vale sem a área de entrada (o diálogo toma o lugar dela) e ancorada no fundo da tela: o menu numerado do diálogo (1, 2, … com a seleção num deles) nas últimas linhas, por mais alto que o diálogo seja, e o texto dele da borda de cima da caixa para baixo (sem caixa, a tela toda); tela sem menu, nas últimas 8 linhas. A área de entrada só conta se for a última caixa da tela (nada de menu, borda ou tela conhecida abaixo dela, só o rodapé) e, quando o emulador dá o cursor, com o cursor nela. Se várias telas casam, vem primeiro a de permissão (sem decisão, nega), depois as que escalam ou alertam, por último as respondidas com Enter; entre iguais, a mais perto do fundo. A tela cuja resposta conta com a primeira opção (permissão, menu do limite de uso) exige que o menu do fundo diga isso nela e a tenha selecionada; confiar na pasta e atualizar (Enter na opção 1) só valem antes de a sessão começar (primeiro hook ou primeira tela pronta). Na dúvida, nada é digitado: escala ou nega.
- **A chamada da tela** (`ScreenCall`) é procurada só no diálogo, do topo da caixa (sem caixa, do título ou cabeçalho de mais acima, senão do parágrafo logo acima do menu) até o menu. Comando: os textos próprios do diálogo saem pela posição (o título, a pergunta acima do menu, o cabeçalho e o `Reason:` do Codex, o `$ ` da primeira linha), e o que sobra tem de ser o comando exato, linha a linha, ou o comando e a descrição dele (Claude Code); uma quebra dentro de uma linha do comando só é a tela quebrando onde a caixa com laterais mostra a linha cheia (a palavra seguinte não cabia, ou a linha chega à borda), e sem caixa um comando quebrado não é reconhecido. Caminho: uma palavra do diálogo de arquivo é o caminho exato (relativo à pasta da chamada, quando ela vem), e nenhuma outra nomeia outro arquivo; URL, idem. Várias chamadas no diálogo: a mais recente, ou a negada mais recente se houver uma. Um diálogo sem caixa com laterais pode ter a borda de cima ou um parágrafo forjados pelo texto do comando; isso só engana se o hook daquela chamada falhou e outra chamada com o texto exato do resto está pendente.

### Digitação de mensagens

`TypeMessage` monta os bytes de uma entrega: o texto vai entre os marcadores de colagem (*bracketed paste*, `ESC[200~` … `ESC[201~`), para que quebras de linha não enviem a mensagem no meio, e depois vem a **tecla de envio** do provedor. O texto perde os caracteres de controle, menos a quebra de linha e a tabulação, para nada fechar a colagem antes da hora. Cada documento de provedor registra a tecla e as peculiaridades dele, conferidas na conformidade.

| Adaptador | Colagem | Tecla de envio |
|---|---|---|
| Claude Code, Codex, Antigravity | sim | `\r` ([Claude Code](claude-code.md#digitação), [Codex](codex.md#digitação), [Antigravity](antigravity.md#digitação)) |
| nulo (shell) | não: cada linha é digitada seguida de `\r` | `\r` |

- Texto acima de 8 KiB (padrão) não é digitado: vai para um arquivo e o agente recebe a linha que manda lê-lo ([esperas](../orquestracao/esperas-e-caixa-de-entrada.md)).
- **Confirmação:** depois da tecla de envio, o motor espera `turn_start` (ou a tela ocupada, onde o evento não existe) por até 10 s (padrão). Sem sinal, registra a entrega como não confirmada e avisa o Mestre da mesa; o texto nunca é digitado de novo sozinho.

### Retomada de sessão

O id da sessão fica no registro de terminais ([persistência](../arquitetura/persistencia.md#registro-de-terminais)). `BuildLaunch` com esse id monta o comando de retomada, com as mesmas flags da base e a camada do Mestre, hooks e confiança reaplicados. Opções do tipo "a mais recente" (`--continue`, `-c`, `--last`) não são usadas, porque vários personagens dividem a mesma pasta.

| Provedor | Comando de retomada | De onde vem o id |
|---|---|---|
| Claude Code | `claude --resume <uuid>` | o `--session-id` que o motor gerou ao iniciar |
| Codex | `codex resume <SESSION_ID>` com os mesmos `-c` | `session_id` dos hooks |
| Antigravity | `agy --conversation <id> --agent mestre-<id>` | `conversationId` dos hooks |

Se o comando falhar ou não houver id salvo, o personagem renasce com a mensagem de retomada ([personagens](../orquestracao/personagens.md#retomada-após-reinício)). A etapa 10 da [conformidade](conformidade.md) confere cada linha.

### Monitoramento reduzido

O personagem fica marcado com monitoramento reduzido, com o motivo, quando o provedor é o Antigravity, que não tem os eventos de permissão, interrupção, compactação, fim de sessão e notificação (a falta de `notification` no Codex e de `interrupt` no Claude Code, que é uma tela, não marca), ou quando nenhum hook chega em 60 s (padrão) depois do primeiro prompt entregue (hooks não carregados, pasta sem confiança). No segundo caso o motor também gera um alerta do [checklist](../plataforma/checklist.md). O canvas mostra a marca ([canvas](../painel/canvas.md)). O plano B (contexto na primeira mensagem, resumo do estado salvo a cada `turn_end`) está em [personagem](../sessao-zero/personagem.md) e [personagens](../orquestracao/personagens.md).

## Regras e erros

- Versão do provedor abaixo da mínima do documento dele: a base é recusada naquela máquina com a explicação, e o checklist avisa. Chega no M7, com a detecção da versão instalada ([ordem](../desenvolvimento/ordem-de-implementacao.md#regras-de-execução-da-ordem)).
- Evento nativo desconhecido: registrado no log e respondido com a resposta neutra ([despachante](despachante-de-hooks.md)).
- Ferramenta não mapeada: tipo `other`, decidida pelo padrão da classe ([permissões](../classes/permissoes.md)).

## Critérios de aceite

- **CA-ADAPT-01** · *rápida* — **Dado** um payload gravado de cada provedor para cada evento do mapa, **quando** `NormalizeHook` roda, **então** o evento interno tem `event`, `session_id`, `turn_id` e, em eventos de ferramenta, `tool.kind`, `command` ou `paths` preenchidos conforme as tabelas.
- **CA-ADAPT-07** · *rápida* — **Dado** um agente falso que nunca dispara hooks, **quando** passam 60 s (relógio falso) após o primeiro prompt, **então** o personagem fica com monitoramento reduzido e o checklist registra o alerta.
- **CA-ADAPT-08** · *rápida* — **Dado** o texto `"linha 1\nlinha 2"`, **quando** `TypeMessage` do Claude Code monta os bytes, **então** eles são `ESC[200~linha 1\nlinha 2ESC[201~\r`; no adaptador nulo, `linha 1\rlinha 2\r`.
- **CA-ADAPT-09** · *rápida* — **Dado** o agente falso parado numa tela sem padrão conhecido por 30 s durante o início, **quando** o adaptador avalia, **então** o personagem fica `blocked` com motivo `input` e o Mestre da mesa recebe o aviso.
- **CA-ADAPT-10** · *rápida* — **Dado** um personagem de cada provedor com id de sessão salvo, **quando** `BuildLaunch` recebe o id, **então** o comando é o da tabela de retomada, com as flags da base.
- **CA-ADAPT-11** · *rápida* — **Dado** uma entrega digitada sem `turn_start` em 10 s (relógio falso), **quando** o prazo passa, **então** a entrega fica "não confirmada", o Mestre da mesa é avisado e nada é digitado de novo.

## Depende de

- [Terminal](../arquitetura/terminal.md) (tela, digitação)
- [Dublês](../qualidade/dubles.md) (agente falso, gravações)

## Verificar na implementação

- **Verificar na implementação:** nomes dos campos de caminho nos argumentos do Antigravity, nome da ferramenta de shell do Codex e do Claude no Windows, e o nome com que o Antigravity entrega ferramentas MCP ao hook. **Padrão:** o mapa usa os nomes vistos nos payloads gravados (no Antigravity, `CommandLine` e `Cwd` já são conhecidos). **Se falhar:** campo ou ferramenta fora do mapa vira tipo `other`, decidido pelo padrão da classe (regra acima), e o reforço nativo segue valendo. **Onde:** etapas 4 a 6 do [teste de conformidade](conformidade.md), que gravam os payloads. **Resultado da primeira rodada (Linux, 2026-10-14):** o Antigravity 1.3.5 usa `TargetFile` (`write_to_file`) e `AbsolutePath` (`view_file`), que entraram no mapa; os demais campos de caminho e o nome das ferramentas MCP (nenhum MCP no perfil) continuam abertos; o shell do Codex 0.162.1 chega ao hook como `Bash`. O shell do Claude e do Codex no Windows fica para a rodada do Windows.
- **Verificar na implementação:** a forma real dos diálogos de permissão nativos (caixa com laterais ou só a borda de cima; título, comando e pergunta em parágrafos; o `$ ` do Codex) e a posição do cursor na área de entrada de cada CLI. **Padrão:** como em [qual tela está na frente](#prontidão-e-telas-interativas); o executor da conformidade ainda não passa o cursor (`Supervisor.ScreenAt`) nem a pasta da chamada (`CallDecision.Cwd`). **Se falhar:** o diálogo cuja chamada não é achada é negado (o modelo recebe a recusa), e a tela gravada vira o padrão novo; um cursor fora da área de entrada deixa o personagem sem ficar pronto, e o cursor deixa de ser passado para aquele CLI. **Onde:** etapas 4 e 5 (no Antigravity 1.3.5 a tela nativa aparece depois de todo `allow`), na próxima rodada real dos três provedores.

## Fontes

- https://code.claude.com/docs/en/hooks
- https://learn.chatgpt.com/docs/hooks
- https://antigravity.google/docs/hooks
