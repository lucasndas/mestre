# Claude Code

> **Assunto:** como o Mestre inicia, instrui, vigia e mede agentes Claude Code · **Componente:** Todos · **Atualizado:** 2026-10-18

## Objetivo

Registrar o que o [adaptador](adaptador.md) do Claude Code precisa saber: arquivos lidos, canal da camada do Mestre, hooks, confiança, flags, contas, uso e telas. Pesquisado na versão **2.1.296** e na documentação oficial de 2026-10-12; a primeira rodada da [conformidade](conformidade.md) (2026-10-14) passou nas etapas 1 a 10 e 12 com a **2.1.299**, para a qual o CLI se atualizou sozinho no perfil durante a rodada. **Versão mínima: 2.1.295** (por causa de `onFailure`).

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

- Só lê `.claude/skills/<nome>/SKILL.md` (e `~/.claude/skills/`, não usada pelo Mestre). `.agents/skills/` não é lida (etapa 9 da conformidade, 2.1.299). Por isso o Mestre mantém a cópia gerenciada ([mesa](../sessao-zero/mesa.md), [ADR 0011](../decisoes/0011-agents-skills-pasta-oficial.md)).
- Num worktree, a busca para na raiz do worktree: a cópia precisa existir em cada worktree, **antes** de o agente iniciar (pasta criada depois exige `/reload-skills`; edições em pasta existente recarregam sozinhas).
- `description` (com `when_to_use`) é cortada em 1.536 caracteres.
- **Skills por personagem** (nível e classe): ficam em `.mestre/agents/<id>/.claude/skills/`, com as [skills de apoio](../classes/skills-de-apoio.md) ligadas, e o motor passa `--add-dir .mestre/agents/<id>`; o worktree compartilhado só tem a de comunicação ([personagem](../sessao-zero/personagem.md#skills-por-personagem)).

### Hooks

- **Arquivo:** `.claude/settings.local.json`, chave `hooks`, parte gerenciada mesclada: os handlers do Mestre saem e entram de novo no fim de cada lista, os do usuário ficam ([mesa](../sessao-zero/mesa.md)). Hooks de várias fontes somam; handler idêntico roda uma vez; edições recarregam sem reiniciar.
- **Comando gravado:** a linha de guarda da [mesa](../sessao-zero/mesa.md#não-atrapalhar-quem-clona), não a chamada pura do exemplo abaixo: só chama `mestre hook` se `MESTRE_AGENT_ID` existe e o binário é achado; senão sai com 0 sem saída. O binário é o caminho estável, que nenhuma variável troca (o repositório poderia defini-la); só sem ele vale `${MESTRE_CLI:-mestre}`. O Claude Code roda o hook de comando com bash nos dois sistemas (`sh` no Linux; no Windows o Git Bash, que ele exige para um hook sem `"shell": "powershell"`), então a linha é a mesma, com o caminho do Windows em barras normais ([Verificar](#verificar-na-implementação)). **Configuração do repositório:** o arquivo `--settings` do personagem fica acima do `.claude/settings.json` do repositório e leva `env` com `MESTRE_AGENT_ID`, `MESTRE_CLI` e `MESTRE_ENDPOINT` do personagem, para o repositório não esvaziar nem desviar as variáveis dos hooks, e `disableAllHooks: false`.
- **Eventos ligados:** SessionStart, UserPromptSubmit, PreToolUse, PermissionRequest, PostToolUse, PostToolUseFailure, Stop, StopFailure, Notification, PreCompact, SessionEnd. Matcher `*` nos eventos de ferramenta.
- **Entrada:** JSON no stdin, `snake_case`: `session_id`, `prompt_id`, `transcript_path`, `cwd`, `hook_event_name`, `permission_mode`; PreToolUse traz `tool_name`, `tool_input`, `tool_use_id`; Stop traz `last_assistant_message`; StopFailure traz `error` e `error_details`.
- **Saída:** exit 0 com JSON. PreToolUse: `hookSpecificOutput.permissionDecision` (`allow|deny|ask|defer`) e `permissionDecisionReason`; `allow` não passa por cima de regras nativas `deny`/`ask`. PermissionRequest: `hookSpecificOutput.decision.behavior`.
- **Falha:** exit 2 bloqueia (motivo no stderr); **exit 1 não bloqueia**; erro ou tempo esgotado liberam (falha aberta), a não ser com `"onFailure":"block"`, usado só em PreToolUse e PermissionRequest.
- **Tempos limite gerados:** 30 s em PreToolUse, PermissionRequest e UserPromptSubmit; 60 s nos demais. SessionEnd tem orçamento total de 1,5 s: o despachante só registra o evento.
- Hooks rodam sem TTY e herdam o ambiente do agente (inclusive `MESTRE_AGENT_ID`), menos `OTEL_*`.

```json
{"hooks": {"PreToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "mestre hook --provider claude-code --event PreToolUse", "timeout": 30, "onFailure": "block"}]}]}}
```

### Confiança de pasta

- No modo interativo, sem confiança na pasta, **nenhum hook de arquivo de configuração e nenhuma statusLine rodam**, nem os do usuário.
- A confiança fica em `$CLAUDE_CONFIG_DIR/.claude.json` (confirmado no spike do Linux). A pasta pessoal (home) nunca fica confiada.
- **Pré-confiar:** gravar `projects["<caminho absoluto do worktree>"].hasTrustDialogAccepted = true` nesse arquivo, com escrita atômica (o Claude reescreve esse arquivo e guarda cópias em `backups/`). **Só a chave do worktree basta** (confirmado). Quando acontece: [mesa](../sessao-zero/mesa.md). Depois de gravar, o motor relê e confere; se o Claude reescreveu o arquivo no meio, repete (até 3 vezes). No Windows, a chave é a que já existe para a pasta (barras e maiúsculas podem diferir), senão o caminho como o motor o tem. Ao remover o worktree, `Untrust` retira a entrada.
- A 2.1.299 guarda as estatísticas da sessão em `projects["<espelho>"]` (sem `hasTrustDialogAccepted`) e a memória automática em `projects/<espelho>/memory/` do perfil: a memória é comum a todos os worktrees de um espelho, portanto a todos os personagens daquele perfil num workspace. **Por decisão do Dono (2026-10-14)** a memória automática fica desligada para os personagens, porque o Mestre tem a própria memória (pergaminhos e fichas) e a do Claude misturaria o que um personagem aprende com os outros: o arquivo `--settings` do personagem leva `autoMemoryEnabled: false` (chave de qualquer arquivo de configuração, documentada em "Enable or disable auto memory"). A variável `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` faz o mesmo e é o reserva se a chave não valer no `--settings` (não verificado numa rodada real); as estatísticas da sessão em `projects["<espelho>"]` continuam, pois não são memória.
- **A chave do espelho nunca é gravada:** respondendo "confiar" na tela, o Claude grava a chave do **espelho `--bare`** de onde o worktree saiu, o que confia **todos** os worktrees daquele espelho. Por isso o motor nunca grava essa chave e, ao pré-confiar, remove `hasTrustDialogAccepted` dela se a encontrar (no clone principal da Cidadela, em que o worktree é a própria raiz, a chave é uma só e fica confiada).

### Flags

| Usadas pela base | Para quê |
|---|---|
| `--append-system-prompt-file <MESTRE.md>` | camada do Mestre |
| `--settings .mestre/agents/<id>/claude-settings.json` | regras nativas do personagem, statusLine e `disableAllHooks: false`, num arquivo gerado do zero ([reforço nativo](../classes/reforco-nativo.md)) |
| `--session-id <uuid>` / `--resume=<uuid>` | id gerado pelo motor, liga hooks ao PTY; retomada |
| `--permission-mode default` | único modo permitido |

**Proibidas:** `--dangerously-skip-permissions` e os modos `bypassPermissions`, `auto`, `dontAsk` e `acceptEdits` (e `plan`: só `default` vale); `--bare` (pula hooks, skills e instruções); `--system-prompt[-file]` (substitui o prompt padrão); `--setting-sources` (pode descartar os hooks locais); `--add-dir` com qualquer pasta que não seja `.mestre/agents/<id>` do próprio personagem (essa única é passada pelo motor para as skills por personagem, ver [personagem](../sessao-zero/personagem.md)); `-p` (a base é interativa). Um flag curto junto de mais texto também é recusado (`-dc`, `-r1234`): o Commander o lê como vários flags (`-d -c`) ou como flag e valor (`--resume 1234`), que a conferência por nome não veria; cada flag curto vai sozinho.

`BuildLaunch` confere por **lista do que é permitido**, como no Codex e no Antigravity: a base só pode ter as opções de modelo e de tela da referência oficial (`--model`, `--fallback-model`, `--effort` com `low`, `medium`, `high`, `xhigh`, `max` ou `ultracode`, `--autocompact`, `--name`/`-n`, `--verbose`, `-d`/`--debug[=filtro]`, `--no-chrome`) e `--permission-mode default`, e as variáveis de ambiente que mudam a segurança do provedor são recusadas a cada início ([bases](../orquestracao/bases-e-fichas.md#regras-e-erros)); o motor, só `--append-system-prompt-file`, `--settings`, `--add-dir` e `--disallowedTools` e, desde o M7, `--mcp-config` com um arquivo da pasta do personagem que só tem o gateway `mestre`, junto de `--strict-mcp-config` ([MCP por classe](../classes/mcp-por-classe.md)). Qualquer outra opção (também as que ninguém nomeou: `--safe-mode`, `--managed-settings`, `--worktree`, `--rc`, `--mcp-config` vindo da base, `--allowedTools`…), um argumento que não é opção (prompt, subcomando como `mcp`) e `--` são recusados: depois de `--` o `--session-id`, o `--settings` e o `--permission-mode` do motor virariam texto do prompt. Os argumentos do motor vêm por último e inteiros. Recusa também o comando sem a camada do Mestre, sem o `--settings` do próprio personagem ou sem `CLAUDE_CONFIG_DIR`, e acrescenta `--permission-mode default` se faltar, para nenhum modo da configuração do usuário valer.

### Contas múltiplas

- `CLAUDE_CONFIG_DIR=<pasta do perfil>` no ambiente do processo: configuração, histórico e login próprios (`.credentials.json` dentro dela). O perfil de conta é definido em [bases e fichas](../orquestracao/bases-e-fichas.md).
- O processo roda como `mestre-agente` nos dois sistemas; no Windows, `USERPROFILE` e `APPDATA` são os do perfil carregado dessa conta e só `CLAUDE_CONFIG_DIR` aponta para o perfil de conta ([Windows](../arquitetura/windows.md#conta-dos-agentes-mestre-agente)). Exceção: o Operador do PC roda com a conta do usuário; `--chrome` é recusado em toda base, e o navegador do usuário vem do catálogo MCP ([Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc)).
- Verificar login: `claude auth status` (JSON com `configDirectory` e `authMethod`), usado pelo [checklist](../plataforma/checklist.md).
- **Linux, como `mestre-agente`** (spike, [segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)): a credencial fica em `$CLAUDE_CONFIG_DIR/.credentials.json`, modo `0600`, sem chaveiro nem D-Bus. A autoatualização fica ligada e grava no `HOME` do perfil ([atualização dos CLIs](atualizacao-dos-clis.md)). Escritas fora do perfil: `/tmp/claude-<uid>/…`, `/tmp/cc-socks-<uid>/` e `/tmp/claude-{plugin,skill}-<pid>-*.zip` (às vezes sobram); limpeza em [personagem](../sessao-zero/personagem.md#variáveis-de-ambiente).

### Uso, custo e rate limit

- **statusLine** (no arquivo `--settings` do personagem, chamando `mestre hook --provider claude-code --event StatusLine`): recebe `cost.total_cost_usd` (estimativa pelo preço de lista, zera no `/clear`), `context_window.*` (tokens, `used_percentage`) e `rate_limits.five_hour` / `seven_day` (`used_percentage`, `resets_at`; só planos Pro/Max, depois da primeira resposta). Roda a cada mensagem, com intervalo mínimo de 300 ms. O adaptador normaliza as janelas como `5h` e `7d` (`used_pct`, `resets_at`); `ReadUsage` lê o JSON ou o evento já normalizado.
- **StopFailure** com `error = rate_limit` (também `overloaded`, `billing_error`, `authentication_failed`): sinal estruturado de limite.
- **Notification** `quota_auto_resume_fired`, `quota_auto_resume_stale`, `quota_auto_resume_disabled`: a espera pelo limite continuou, parou esperando Enter ou terminou.
- Reserva pela tela: `You've hit your session limit · resets …`, `Usage limit reached`, `API Error: Request rejected (429)`.
- A continuação automática após o limite vem ligada; o personagem fica `blocked` com motivo `quota` ("aguardando cota") até ela disparar ([limites](../plataforma/limites-de-uso.md)).

### Digitação

Mensagens vão com colagem entre colchetes e `\r` ([digitação de mensagens](adaptador.md#digitação-de-mensagens)). Colagens longas aparecem na tela como `[Pasted text #N +M lines]`, mas o texto chega inteiro ao modelo; o reconhecimento de telas ignora esse marcador.

### Telas que o PTY precisa tratar

| Tela | Ação |
|---|---|
| confiar na pasta | evitar (pré-confiança); **nunca responder "confiar"** (gravaria a chave do espelho). Se aparecer antes de a sessão começar (depois, o texto só pode ser do modelo): encerrar o processo sem responder a tela, regravar a chave do worktree, remover a do espelho e reiniciar o personagem uma vez; se aparecer de novo, escalar (`blocked`, motivo `input`, alerta ALL-12) |
| aprovação de import externo | escalar |
| aprovação de servidores do `.mcp.json` | escalar |
| "usar esta `ANTHROPIC_API_KEY`?" | escalar (uma vez por perfil) |
| menu de opções do limite de uso | Enter na opção 1, só se ela for a de esperar ("Stop and wait for limit to reset"); depois o personagem fica `blocked`, motivo `quota` |
| espera parada após o computador dormir (`quota_auto_resume_stale`) | responder Enter quando o motor confirmar que o limite reiniciou |
| área de entrada (pronto): `❯` entre duas linhas `─`, com o rodapé abaixo; com `esc to interrupt` nas últimas linhas, ocupado | lida pelo adaptador (2.1.299) |
| pedido de permissão nativo | decisão registrada: Enter ("Yes", uma vez) ou Esc (nega); sem decisão, negar; nunca a opção "don't ask again" |
| primeiro uso e login (num `CLAUDE_CONFIG_DIR` novo: seletor de tema e depois a confiança, com o cursor em "No, exit") | resolvidos na configuração inicial do perfil de conta, no terminal de login; num personagem, escalar como alerta do checklist |

Quem chama estas operações na criação de mesas e personagens é a sessão zero ([mesa](../sessao-zero/mesa.md), [personagem](../sessao-zero/personagem.md)); os perfis de conta estão em [bases e fichas](../orquestracao/bases-e-fichas.md#perfis-de-conta).

## Regras e erros

- Versão abaixo de 2.1.295: base recusada na máquina (sem `onFailure`, o despachante falharia aberto); a recusa chega no M7, com a detecção da versão instalada ([ordem](../desenvolvimento/ordem-de-implementacao.md#regras-de-execução-da-ordem)).
- Pasta sem confiança detectada (tela de confiança ou ausência de hooks): ver monitoramento reduzido no [adaptador](adaptador.md#monitoramento-reduzido).
- Se o `.claude.json` estiver sendo reescrito pelo Claude, a pré-confiança relê, mescla e grava de novo (até 3 tentativas) e confere o resultado.

## Critérios de aceite

- **CA-CLAUDE-01** · *rápida* — **Dado** um `settings.local.json` com hooks do usuário, **quando** `HookFiles` roda duas vezes, **então** o arquivo tem os hooks do usuário intactos, a parte gerenciada uma vez só, matcher `*` e `onFailure: block` só em PreToolUse e PermissionRequest.
- **CA-CLAUDE-02** · *rápida* — **Dado** qualquer base e ficha válidas, **quando** `BuildLaunch` monta o comando, **então** ele contém `--append-system-prompt-file`, `--settings` e `CLAUDE_CONFIG_DIR`, e nenhuma flag proibida.
- **CA-CLAUDE-03** · *rápida* — **Dado** um `.claude.json` com outras chaves e a chave do espelho com `hasTrustDialogAccepted = true`, **quando** `PreTrust` roda duas vezes, **então** o worktree fica com `hasTrustDialogAccepted = true`, a chave do espelho perde esse campo e o resto do arquivo não muda.
- **CA-CLAUDE-04** · *rápida* — **Dado** um JSON de statusLine gravado com `rate_limits`, **quando** `ReadUsage` lê, **então** o registro de uso tem custo, % de contexto e as duas janelas com `used_pct` e `resets_at`.
- **CA-CLAUDE-05** · *completa* — **Dado** o agente falso do Claude mostrando o menu de limite de uso, **quando** o adaptador reconhece a tela, **então** responde a opção de esperar e o personagem fica `blocked` com motivo `quota`.

## Depende de

- [Adaptador](adaptador.md) · [Despachante de hooks](despachante-de-hooks.md)
- [Reforço nativo](../classes/reforco-nativo.md)

## Verificar na implementação

Todos são conferidos pelo [teste de conformidade](conformidade.md).

- **Verificar na implementação:** textos exatos das opções do menu de limite de uso e da tela de permissão nativa. **Padrão:** os textos de "Uso, custo e rate limit" e a posição das opções. **Se falhar:** a tela não é reconhecida e escala ao Mestre da mesa ([telas](adaptador.md#prontidão-e-telas-interativas)); a transcrição da escalada vira o padrão novo. **Onde:** tela de permissão nas etapas 4 e 5; menu de limite no primeiro limite real, pela transcrição. **Resultado (2026-10-14, 2.1.299):** nenhuma das duas apareceu nas duas primeiras rodadas (os hooks decidiram tudo); o item continua aberto.
- **Verificar na implementação:** nome da ferramenta de shell no Windows. **Padrão:** `PowerShell`, tratado como `shell`. **Se falhar:** o nome capturado entra no mapa; até lá vale a regra de ferramenta não mapeada do [adaptador](adaptador.md#regras-e-erros). **Onde:** etapa 5 na máquina Windows (rodada do Windows).
- **Verificar na implementação:** no Windows, o Claude Code roda a linha dos hooks e da statusLine com o Git Bash, e ela chama o `mestre.exe` pelo caminho estável em barras normais (ou pelo `MESTRE_CLI` com as barras invertidas trocadas) e passa o código de saída (2 bloqueia). **Padrão:** como em Hooks; a suíte completa do CI Windows roda a linha no Git Bash. **Se falhar:** cada handler leva `"shell": "powershell"` e a linha vira a do PowerShell (`if ($env:MESTRE_AGENT_ID -and (Test-Path -LiteralPath '<caminho>')) { & '<caminho>' hook …; exit $LASTEXITCODE }`). **Onde:** etapas 4 a 7 na rodada do Windows.
- **Verificar na implementação:** um `.claude/settings.json` do repositório com `disableAllHooks: true` e `env` que esvazia `MESTRE_AGENT_ID` e desvia `MESTRE_CLI` e `MESTRE_ENDPOINT` não desliga os hooks do Mestre. **Padrão:** não desliga: o `--settings` do personagem está acima e fixa essas chaves. **Se falhar:** o personagem não inicia num repositório que tenha essas chaves (o motor confere o arquivo antes do início) e o checklist avisa. **Onde:** o repositório de teste da conformidade traz esse arquivo, e as etapas 4 a 6 falham se ele vencer (rodadas do Linux e do Windows). **Resultado (2026-10-14, 2.1.299, rodada 3, Linux):** Padrão confirmado: com o arquivo hostil no repositório, a etapa 4 negou a escrita, a 5 viu `pre_tool` e `post_tool` e a 6 viu o hook; o `--settings` do personagem venceu.
- **Verificar na implementação:** a statusLine declarada no arquivo `--settings` do personagem roda. **Padrão:** como descrito em Uso. **Se falhar:** a statusLine vai para a parte gerenciada de `.claude/settings.local.json` (o comando é o mesmo para todos; o personagem é identificado pelo `MESTRE_AGENT_ID`); a chave é `ClaudeStatusLineInLocal` dos `NativeFallbacks`, e uma statusLine do usuário nunca é trocada. **Onde:** etapa 7. **Resultado (2026-10-14, 2.1.299):** Padrão confirmado; o `rate_limits` só vem depois da primeira resposta.
- **Verificar na implementação:** texto de várias linhas colado entre colchetes e seguido de `\r` é enviado inteiro como uma mensagem. **Padrão:** como em Digitação. **Se falhar:** o motor manda mensagens de várias linhas pelo arquivo de mensagem longa ([esperas](../orquestracao/esperas-e-caixa-de-entrada.md)) e digita só a linha que pede para lê-lo (opção `ClaudeMultilineViaFile` do adaptador: `TypeMessage` recusa o texto de várias linhas como o de texto longo). **Onde:** etapa 2 do [teste de conformidade](conformidade.md), com a pergunta em três linhas. **Resultado (2026-10-14, 2.1.299):** Padrão confirmado.

## Fontes

- https://code.claude.com/docs/en/cli-reference
- https://code.claude.com/docs/en/memory#agents-md
- https://code.claude.com/docs/en/memory#enable-or-disable-auto-memory
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
