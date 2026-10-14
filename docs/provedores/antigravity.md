# Antigravity CLI (`agy`)

> **Assunto:** como o Mestre inicia, instrui, vigia e mede agentes Antigravity CLI · **Componente:** Todos · **Atualizado:** 2026-10-14

## Objetivo

Registrar o que o [adaptador](adaptador.md) do Antigravity precisa saber. O `agy` é o agente de terminal do Google que substituiu o Gemini CLI (aposentado para usuários individuais desde 2026-06-18). Pesquisado na versão **1.3.3** (instalada; 8 execuções reais sem interface), no texto embutido no binário e na documentação oficial de 2026-10-12. **Versão mínima: 1.3.3**. A primeira rodada da [conformidade](conformidade.md) (Linux, 2026-10-14) foi com a **1.3.5**: passou nas etapas 1 a 4, 7, 8 e 10, falhou nas 5 e 6 pelo `--sandbox` (abaixo). A segunda rodada, sem a flag, passou nas etapas 1 a 8 e 10 (9 e 11 não se aplicam) e repetiu a falha da 12, que liga o plano B `deny_subagent`.

## Comportamento

### Instruções do projeto

- Lê `AGENTS.md` e `GEMINI.md` (Markdown puro, sem frontmatter), cada um como regra `always_on` do seu escopo. Regras somam (global, workspace, pasta); em conflito, a mais específica vence.
- Quando o agente lê ou edita um arquivo, sobe da pasta dele até a raiz do workspace carregando, em cada nível: `AGENTS.md`/`GEMINI.md`, `.agents/AGENTS.md`/`.agents/GEMINI.md` e `.agents/rules/*.md`.
- Arquivos em `.agents/rules/` precisam de frontmatter com `trigger` (`always_on`, `model_decision`, `glob`, `manual`); sem frontmatter válido são **descartados em silêncio**.
- Limites: 24.000 bytes por arquivo; regras globais e `always_on` dividem 20.000 tokens (acima disso, os maiores viram ponteiros lidos sob demanda).
- Arquivos globais (`~/.gemini/…`) não são usados pelo Mestre.

### Camada do Mestre

- Não existe flag nem configuração de prompt de sistema.
- **Canal principal:** arquivo de agente próprio `.agents/agents/mestre-<id>.md`, gerado por personagem (frontmatter + corpo; o corpo vira o prompt de sistema desse agente), com `--agent mestre-<id>` na base. Fica fora do git ([mesa](../sessao-zero/mesa.md)) e é apagado quando o personagem é encerrado ([personagem](../sessao-zero/personagem.md#ao-encerrar)).
- **Frontmatter gerado:** sempre do zero (o arquivo está no worktree, que o agente escreve): `name` (igual ao do `--agent`), `description`, `mainAgent: true`, `subagent: false` e `tools` (reforço nativo, [reforço nativo](../classes/reforco-nativo.md)). Nunca `excludeDefaultComponents` (tiraria as seções de fábrica do prompt). `permissionMode` não existe na 1.3.3 (não aparece no binário) e não é usado. `tools` nunca lista `list_permissions`: a 1.3.5 recusa todo turno com "tool \"list_permissions\" not found in registry".
- **Nome inexistente cai no agente padrão sem erro** (issue #1213). Defesa: antes de iniciar, o motor confere que o arquivo existe e que o `name` é igual ao do `--agent`; depois, a falta do código `M-…` na etapa 2 do [teste de conformidade](conformidade.md) acusa o problema.
- **Atualização a cada turno e início de sessão:** PreInvocation devolvendo `injectSteps: [{"ephemeralMessage": "…"}]`. Não existe `additionalContext`. O primeiro PreInvocation de uma conversa conta como início de sessão.
- Reserva final: primeira mensagem ([personagem](../sessao-zero/personagem.md)). Os caminhos "Se falhar" de [Verificar](#verificar-na-implementação) são chaves que a conformidade liga (`LayerFallbacks`, `AntigravityNoPaste`): camada por `injectSteps` ou pela primeira mensagem (o arquivo de agente fica só com o frontmatter e um corpo curto), ordem de ler o `AGENTS.md` da raiz, e digitação sem colagem.

### Skills

- Lê `.agents/skills/<nome>/SKILL.md` nativamente (`.agent/skills` antiga também). `description` é obrigatória; `name` é opcional (padrão: nome da pasta), mas o Mestre sempre escreve, por causa do Codex.
- Carregamento progressivo; cada skill também vira comando `/<nome>`; `/skills reload` recarrega. Não há menção a `.claude/skills/`.
- **Skills por personagem** (nível e classe): não há pasta por personagem, então o conteúdo delas vai como seção compacta no arquivo de agente `.agents/agents/mestre-<id>.md`; `.agents/skills/` do worktree só tem a de comunicação ([personagem](../sessao-zero/personagem.md#skills-por-personagem)).

### Hooks

- **Arquivo:** `.agents/hooks.json` do worktree, fora do git ([mesa](../sessao-zero/mesa.md)). As chaves de primeiro nível são nomes livres; o Mestre usa a chave `mestre` e preserva as outras.
- **Só cinco eventos:** PreToolUse, PostToolUse, PreInvocation, PostInvocation, Stop. Não há SessionStart, UserPromptSubmit, PreCompact nem SessionEnd ([mapa de eventos](adaptador.md#mapa-de-eventos)). Todos são ligados.
- PreToolUse e PostToolUse usam grupos `{matcher, hooks}` (matcher é regex; `*` casa tudo); os outros três são listas simples de handlers. Handler: `type: command`, `command`, `timeout` (segundos, padrão 30).
- Os comandos rodam com a pasta do `hooks.json` como pasta atual, por `sh -c` (Linux) ou `cmd /c` (Windows), de forma síncrona: o agente espera.
- **Entrada:** JSON no stdin, `camelCase`, **sem o nome do evento** (o despachante usa o `--event` da linha de comando): `conversationId`, `modelName`, `stepIdx`, `toolCall {name, args}` (argumentos em PascalCase: `CommandLine`, `Cwd`; o caminho é `TargetFile` no `write_to_file` e `AbsolutePath` no `view_file`, gravados na 1.3.5), `transcriptPath`, `workspacePaths`. PostToolUse acrescenta `error`; Pre/PostInvocation trazem `invocationNum`; Stop traz `executionNum` (os dois contam a partir de 0), `terminationReason` (`model_stop`, `max_steps_exceeded`, `error`; a 1.3.5 mandou também `NO_TOOL_CALL`, lido como normal), `error`, `fullyIdle`.
- **Saída:** PreToolUse `{"decision": "allow"|"deny"|"ask"|"force_ask"|"deny_unless_prior_grant", "reason", "permissionOverrides", "overwrite"}`; PostToolUse `{}`; PreInvocation `{"injectSteps": […]}`; PostInvocation `{"injectSteps": [], "terminationBehavior": ""}`; Stop `{}` (só `"decision":"continue"` impediria a parada).
- **Falha fechada (testado no PreToolUse):** exit diferente de 0, saída que não é JSON, tempo esgotado e `{}` sem decisão **bloqueiam** a ferramenta. O despachante sempre imprime a decisão explícita ([despachante](despachante-de-hooks.md#respostas-por-provedor)). Um `allow` do hook **não** passa por cima do sistema de permissões nativo.
- Para PostToolUse, PreInvocation, PostInvocation e Stop, o despachante supõe a mesma falha fechada do PreToolUse: sempre devolve JSON válido e nunca sai com erro de propósito, então não depende do comportamento de falha desses eventos. **Tempos limite gerados:** 30 s em PreToolUse, PreInvocation e PostInvocation; 60 s em PostToolUse e Stop.
- **Binário ausente:** um hook cujo comando não existe bloqueia o agente. O comando gerado testa se o `mestre` existe no caminho estável e, se não existir, imprime a resposta neutra (`{"decision":"allow"}` ou `{}`); se existir, executa-o e devolve o exit dele (uma falha do binário nunca vira `allow`). Em `sh`: `if [ -e '<mestre>' ]; then exec '<mestre>' hook …; else printf '%s\n' '<neutra>'; fi`; em `cmd`: `if exist "<mestre>" ("<mestre>" hook …) else (echo <neutra>)`, e o caminho não pode ter `"` nem `%`.

```json
{"mestre": {"enabled": true,
  "PreToolUse": [{"matcher": "*", "hooks": [{"type": "command",
    "command": "mestre hook --provider antigravity --event PreToolUse", "timeout": 30}]}],
  "Stop": [{"type": "command", "command": "mestre hook --provider antigravity --event Stop", "timeout": 60}]}}
```

### Confiança de pasta

- No modo interativo, pasta sem confiança mostra "Do you trust the contents of this project?" ("Yes, I trust this folder" / "No, exit").
- A confiança fica no array `trustedWorkspaces` (caminhos absolutos) de `~/.gemini/antigravity-cli/settings.json`, com o `HOME` do perfil de conta. **Pré-confiar:** acrescentar o caminho exato de cada worktree; o Mestre não depende de a confiança de uma pasta cobrir as subpastas. JSON inválido impede o `agy` de iniciar. Não existe flag `--trust`. Pré-confiança confirmada no spike do Linux.

### Flags

| Usadas pela base | Para quê |
|---|---|
| `--agent mestre-<id>` | camada do Mestre |
| `--sandbox` | **só no Windows**, quando a classe não acessa a internet: sem rede, acesso só ao workspace e à pasta temporária (AppContainer; não verificado; se falhar, `NativeFallbacks.AntigravityNoSandbox` o tira). **No Linux a base não o usa** (decisão do Dono, 2026-10-14, como no Codex): neste Ubuntu, com namespaces de usuário restritos pelo AppArmor (`unshare -Ur` é recusado), a 1.3.5 como `mestre-agente` não roda comando nenhum ("connecting to sandbox server: … connection reset by peer") e o modelo repete com `BypassSandbox: true`. A rede fica ligada e a internet é barrada só pelo despachante (falha fechada no `agy`): sem reforço nativo de rede (`AntigravityNoNativeNetwork`, [reforço nativo](../classes/reforco-nativo.md)). `run_command:BypassSandbox` (tipo `other`, decidido pelo `tools` da classe) continua mapeado: sem a flag o modelo não o pede, e se pedir vale a mesma regra |
| `--model <modelo>` | modelo preferido da classe, se houver |
| `--conversation <id>` | retomada da conversa do personagem |

**Proibidas:** `--dangerously-skip-permissions`. Ela **não** desliga os hooks (testado: o PreToolUse continuou bloqueando), o que corrige a especificação; segue proibida porque aprova sozinha todo pedido de permissão nativo e anula o `--sandbox` onde ele vale (issue #36), tirando a defesa em profundidade. Também proibidas: `-p` (a base é interativa; apelidos `--print` e `--prompt`), `--add-dir`, e o que o Mestre mesmo define ou que retoma "a mais recente": `--agent`, `--conversation`, `--continue` e `-c`; `--remote-control` (a conversa passa a ser conduzida de fora do Mestre); `--mode` diferente de `default` (`accept-edits` e `plan` mudam o que é perguntado); `--sandbox=false`; `-i`/`--prompt-interactive` (a primeira mensagem é do motor); `--project`/`--new-project` (o workspace é o worktree); as opções do modo `-p` (`--output-format`, `--input-format`, `--json-schema`, `--print-timeout`). O `agy` lê as opções com o pacote `flag` do Go: `-opção` e `--opção` são a mesma, o valor vem depois de `=` ou no argumento seguinte, e o primeiro argumento que não é opção (ou `--`) encerra as opções, de modo que tudo depois dele, até o `--agent` do Mestre, deixaria de valer. Por isso o `BuildLaunch` lê os argumentos da mesma forma (um traço ou dois, com `=` ou não) e recusa as proibidas, uma opção que não conhece e qualquer argumento que não seja opção.

No `settings.json` do perfil: `toolPermission` fica em `request-review` (nunca `always-proceed`), com as regras nativas descritas em [reforço nativo](../classes/reforco-nativo.md), que também dizem as únicas chaves que ficam a cada gravação (`permissions.allow` e o que amplia o acesso saem). Quem grava o arquivo é o motor; o `agy` só o regrava na escolha "always allow … (Persist to settings.json)" e na tela de confiança, que o Mestre nunca escolhe. O arquivo regravado fica legível pelo `mestre-agente` ([modo dos arquivos](../classes/reforco-nativo.md#quando-as-regras-mudam)).

### Contas múltiplas

- Não há variável de pasta de configuração (issue #155 aberta). O estado fica em `~/.gemini/antigravity-cli/`, uma conta Google por `HOME`. No Linux, como `mestre-agente` (spike, [segurança](../arquitetura/seguranca.md#contas-do-sistema-e-isolamento-dos-agentes)), o login fica em arquivo, `$HOME/.gemini/antigravity-cli/antigravity-oauth-token`, modo `0600`, sem chaveiro nem D-Bus.
- Contas separadas só com `HOME` diferente por perfil de conta ([bases e fichas](../orquestracao/bases-e-fichas.md)). O `HOME` do perfil é **obrigatório**: sem `HOME` gravável o `agy` não inicia. Esse `HOME` vale para tudo o que o agente roda (git, caches); a identidade git vem do `gitconfig` do personagem, apontado por `GIT_CONFIG_GLOBAL` ([personagem](../sessao-zero/personagem.md), [identidades](../github/identidades-e-apps.md)).
- **No Windows** (spike), o login não vai para arquivo: vai para o **Gerenciador de Credenciais** da conta `mestre-agente` (credencial genérica `gemini:antigravity`), comum a todos os perfis; `HOME` e `USERPROFILE` não o isolam, e o Gerenciador de Credenciais só funciona numa sessão de logon com o perfil da conta carregado ([Windows](../arquitetura/windows.md#conta-dos-agentes-mestre-agente)). Por isso há **um perfil de conta Antigravity por máquina Windows**: o painel e o checklist (WIN-20, [checklist](../plataforma/checklist.md)) recusam o segundo, dizendo por quê.
- `AGY_ACCOUNT` **não** é o login Google (escolhe contas conectadas para comandos como `gh`): não usar.

### Uso, custo e rate limit

- `agy -p "/usage" --output-format json`, com o `HOME` do perfil e sem nenhuma variável `MESTRE_*` (sem diferença de maiúsculas no Windows), para o comando não passar por personagem nem alcançar o motor pelos hooks: `command.data.groups[].buckets[]` com `id`, `window` (`weekly` ou `5h`), `remaining_fraction` e `reset_time`. Não chama modelo nem gasta tokens. O motor roda no fim de cada turno (no máximo uma vez por minuto por perfil, padrão) e a cada 10 minutos com personagem ativo. Uso da janela, em %, = (1 − `remaining_fraction`) × 100.
- Há um limite semanal e um de 5 horas por grupo de modelos (Gemini; Claude e GPT), consumidos conforme o custo dos tokens. Tokens por turno no modo interativo não são lidos (nem da linha de status nem do `transcript_full.jsonl`): o uso vem do `/usage`, com tempo ativo e turnos.
- Desde a 1.3.3, cota esgotada é avisada na hora: o turno termina com Stop `terminationReason = error`, e o personagem fica `blocked` com motivo `quota` ([limites](../plataforma/limites-de-uso.md)). O adaptador não depende do texto do erro: Stop com `error` seguido de `/usage` com `remaining_fraction` 0 numa janela basta.

### Digitação

Mensagens vão com colagem entre colchetes e `\r` ([digitação de mensagens](adaptador.md#digitação-de-mensagens)).

### Telas que o PTY precisa tratar

| Tela | Ação |
|---|---|
| confiar na pasta | evitar (pré-confiança); se aparecer num worktree do Mestre antes de a sessão começar, responder "Yes, I trust this folder" |
| login (URL e código) | escalar como alerta do checklist |
| área de entrada (pronto): `>` entre duas linhas `─`, com o rodapé do agente abaixo; com `esc to cancel` nas últimas linhas, ocupado | lida pelo adaptador (1.3.5) |
| pedido de permissão nativo (sem caixa: "Requesting permission for:", o comando recuado, "Run this command?" com "1. Yes, run command" … "4. No, cancel", a dica e o rodapé abaixo; "Allow creation of this file?" com "1. Yes, allow creation" / "2. No, deny creation"; na 1.3.3, "Yes, allow tool call") | responder com a decisão registrada: Enter na opção 1, uma vez (nunca "always allow"); Esc cancela, o que interrompe o turno; sem decisão, Esc |
| subagente pedindo aprovação ("<nome> needs approval for …", `ctrl+k` aprova, sem tecla de negar) | escalar |
| revisão de artefato ou plano ("Review artifact", "Review plan") | escalar |

Quem chama estas operações na criação de mesas e personagens é a sessão zero ([mesa](../sessao-zero/mesa.md), [personagem](../sessao-zero/personagem.md)); os perfis de conta estão em [bases e fichas](../orquestracao/bases-e-fichas.md#perfis-de-conta).

## Regras e erros

- O Antigravity sempre roda com monitoramento reduzido ([adaptador](adaptador.md#monitoramento-reduzido)).
- `settings.json` do perfil é editado relendo, mesclando e validando o JSON antes de gravar; nunca deixar arquivo inválido.
- **Risco:** o `agy` grava `/tmp/unleash-repo-schema-v1-codeium-language-server.json`, nome fixo que colide entre personagens e perfis da máquina. Plano: `TMPDIR` próprio de cada personagem ([personagem](../sessao-zero/personagem.md#variáveis-de-ambiente)).

## Critérios de aceite

- **CA-AGY-01** · *rápida* — **Dado** um `.agents/hooks.json` do projeto com outro hook nomeado, **quando** `HookFiles` roda duas vezes, **então** o arquivo mantém o outro hook e tem a chave `mestre` uma vez, com os cinco eventos.
- **CA-AGY-02** · *rápida* — **Dado** um payload gravado em `camelCase` sem nome de evento, **quando** `NormalizeHook` roda com `--event PreToolUse`, **então** o evento interno sai como `pre_tool`, com `tool.kind = shell` e o comando de `CommandLine`.
- **CA-AGY-03** · *rápida* — **Dado** um `settings.json` com outras chaves, **quando** `PreTrust` roda duas vezes, **então** o worktree aparece uma vez em `trustedWorkspaces` e o JSON continua válido.
- **CA-AGY-04** · *rápida* — **Dado** qualquer base e ficha válidas, **quando** `BuildLaunch` monta o comando, **então** ele tem `--agent mestre-<id>` e o `HOME` do perfil, e nunca `--dangerously-skip-permissions`, em nenhuma forma (um traço ou dois, com `=`, depois de um argumento que não é opção).
- **CA-AGY-05** · *rápida* — **Dado** a saída gravada de `/usage` em JSON, **quando** `ReadUsage` lê, **então** cada janela tem `used_pct = 1 − remaining_fraction` (em %, de 0 a 100) e `resets_at`.
- **CA-AGY-07** · *rápida* — **Dado** um personagem Antigravity, **quando** `BuildLaunch` gera o arquivo de agente, **então** o `name` do frontmatter é igual ao do `--agent`, há `mainAgent: true` e nunca `excludeDefaultComponents` nem `permissionMode`; e, se o arquivo não existir no disco na hora de iniciar, o início falha com erro em vez de abrir o agente padrão.
- **CA-AGY-06** · *completa* — **Dado** uma máquina sem o binário `mestre` no caminho estável, **quando** o comando gerado do PreToolUse roda, **então** imprime `{"decision":"allow"}` e sai com 0.

## Depende de

- [Adaptador](adaptador.md) · [Despachante de hooks](despachante-de-hooks.md)
- [Reforço nativo](../classes/reforco-nativo.md)

## Verificar na implementação

Conferidos pelo [teste de conformidade](conformidade.md), salvo onde indicado.

- **Verificar na implementação:** `--agent mestre-<id>` no modo interativo (existe desde a 1.1.1), e se o corpo **soma** às seções de fábrica do prompt. **Padrão:** soma — o changelog 1.2.1 só tira as seções de fábrica com `excludeDefaultComponents: true`, que o Mestre nunca usa; ainda assim o corpo é escrito para valer sozinho. **Se falhar:** a camada do Mestre vai pelo primeiro `PreInvocation` (`injectSteps`) e, por último, pela primeira mensagem ([personagem](../sessao-zero/personagem.md)). **Onde:** etapas 2 e 5. **Resultado (2026-10-14, 1.3.5):** Padrão confirmado: no modo interativo o rodapé mostra o agente `mestre-<id>`, e a resposta da etapa 2 trouxe `M-…` e a ferramenta de fábrica `run_command`.
- **Verificar na implementação:** o `AGENTS.md` da raiz carrega já no primeiro turno (e não só quando o agente toca um arquivo), e o que acontece com `AGENTS.md` e `GEMINI.md` na mesma pasta. **Padrão:** carrega no primeiro turno, e os dois somam. **Se falhar:** a camada do Mestre do Antigravity manda ler o `AGENTS.md` da raiz antes de começar; com só um dos dois lido, a sessão zero avisa como faz com o `CLAUDE.md` ([sessão zero do workspace](../sessao-zero/workspace.md)). **Onde:** etapa 2 (código `P-…`); a pasta com os dois arquivos, à mão. **Resultado (2026-10-14, 1.3.5):** a primeira parte confirmada (o `P-…` veio sem ferramenta nenhuma); a pasta com os dois arquivos continua para conferir à mão.
- **Verificar na implementação:** o `agy` aceita colagem entre colchetes no modo interativo e envia com `\r`. **Padrão:** como em Digitação. **Se falhar:** mensagens de várias linhas vão pelo arquivo de mensagem longa ([esperas](../orquestracao/esperas-e-caixa-de-entrada.md)) e só a linha que pede para lê-lo é digitada. **Onde:** etapa 2 do [teste de conformidade](conformidade.md), com a pergunta em três linhas. **Resultado (2026-10-14, 1.3.5):** Padrão confirmado.
- **Verificar na implementação:** `permissionOverrides` no `allow` evita a tela de permissão nativa. **Padrão:** o despachante devolve, no `allow` de um comando de shell, o comando já permitido em `permissionOverrides` (lista de textos `command(<comando>)`; comando com `*`, parênteses ou quebra de linha não é repetido). **Se falhar:** o adaptador responde a tela nativa com a decisão registrada (tabela de telas; textos do binário 1.3.3 e teclas a confirmar). **Onde:** etapa 5. **Resultado (2026-10-14, 1.3.5):** falhou: com `toolPermission` em `request-review`, a tela aparece depois de todo `allow`, também com `permissionOverrides`; vale o Se falhar, com os textos e as teclas da tabela de telas (Esc cancela e interrompe o turno). Rodada 3 (1.3.5): a tela apareceu cinco vezes na rodada e foi respondida com a decisão da chamada que ela mostra (allow); a etapa 5 passou. Rodada 4: idem, depois de o título "Requesting permission for:" entrar no casamento estrito.
- **Verificar na implementação:** no Linux, `HOME` separado isola o login com duas contas reais (o spike confirmou o login só em arquivo sob `HOME`, sem chaveiro). **Padrão:** isola. **Se falhar:** só um perfil de conta Antigravity por máquina Linux também; o checklist recusa o segundo. **Onde:** suíte pesada, com dois perfis de contas diferentes na VM Linux.
- **Verificar na implementação:** no Windows, o `agy` mantém a credencial do Gerenciador de Credenciais rodando no `mestre-pty` iniciado pelo `mestre-launcher` ([Windows](../arquitetura/windows.md#iniciador-mestre-launcher)). Na rodada 2 do Sandbox, manteve em todas as execuções, cada uma numa sessão de logon nova, também pelo token entregue a outro processo; a hipótese da rodada 1 (o `agy` apagou a credencial ao falhar uma renovação) ficou para o item seguinte. **Padrão:** mantém. **Se falhar:** o `mestre-launcher` mantém a sessão de logon do `mestre-agente` aberta enquanto a máquina roda, e o checklist mostra o perfil deslogado até lá. **Onde:** spike 5 no Windows Sandbox ([Windows](../arquitetura/windows.md#verificar-na-implementação)); depois, a suíte pesada.
- **Verificar na implementação:** o `agy` renova sozinho o token de login, que vale 1 h, gravando o arquivo do perfil. **Padrão:** renova. **Se falhar:** o perfil aparece deslogado no ALL-11 do [checklist](../plataforma/checklist.md), o personagem fica `blocked` e o alerta pede novo login. **Onde:** suíte pesada, com um personagem ativo por mais de 1 h, no Linux e no Windows (no Windows, no `mestre-pty` iniciado pelo `mestre-launcher`).

## Fontes

- https://github.com/google-antigravity/antigravity-cli
- https://github.com/google-antigravity/antigravity-cli/blob/main/CHANGELOG.md
- https://antigravity.google/docs/cli/overview · https://antigravity.google/docs/cli/install · https://antigravity.google/docs/cli/headless
- https://antigravity.google/docs/cli/modes
- https://antigravity.google/docs/cli/reference
- https://antigravity.google/docs/hooks
- https://antigravity.google/docs/rules
- https://antigravity.google/docs/skills
- https://antigravity.google/docs/permissions
- https://antigravity.google/docs/subagents
- Issue #1213 (nome de agente inexistente cai no padrão): https://github.com/google-antigravity/antigravity-cli/issues/1213
- https://antigravity.google/docs/cli/gcli-migration · https://antigravity.google/docs/cli/commands/usage
- https://codelabs.developers.google.com/antigravity-cli-hands-on
- https://github.com/quaywin/agys
