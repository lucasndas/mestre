# Antigravity CLI (`agy`)

> **Assunto:** como o Mestre inicia, instrui, vigia e mede agentes Antigravity CLI · **Componente:** Todos · **Atualizado:** 2026-10-12

## Objetivo

Registrar o que o [adaptador](adaptador.md) do Antigravity precisa saber. O `agy` é o agente de terminal do Google que substituiu o Gemini CLI (aposentado para usuários individuais desde 2026-06-18). Pesquisado na versão **1.3.3** (instalada; 8 execuções reais sem interface), no texto embutido no binário e na documentação oficial de 2026-10-12. **Versão mínima: 1.3.3** (a testada).

## Comportamento

### Instruções do projeto

- Lê `AGENTS.md` e `GEMINI.md` (Markdown puro, sem frontmatter), cada um como regra `always_on` do seu escopo. Regras somam (global, workspace, pasta); em conflito, a mais específica vence.
- Quando o agente lê ou edita um arquivo, sobe da pasta dele até a raiz do workspace carregando, em cada nível: `AGENTS.md`/`GEMINI.md`, `.agents/AGENTS.md`/`.agents/GEMINI.md` e `.agents/rules/*.md`.
- Arquivos em `.agents/rules/` precisam de frontmatter com `trigger` (`always_on`, `model_decision`, `glob`, `manual`); sem frontmatter válido são **descartados em silêncio**.
- Limites: 24.000 bytes por arquivo; regras globais e `always_on` dividem 20.000 tokens (acima disso, os maiores viram ponteiros lidos sob demanda).
- Arquivos globais (`~/.gemini/…`) não são usados pelo Mestre.

### Camada do Mestre

- Não existe flag nem configuração de prompt de sistema.
- **Canal principal:** arquivo de agente próprio `.agents/agents/mestre-<id>.md`, gerado por personagem (frontmatter + corpo; o corpo vira o prompt de sistema desse agente), com `--agent mestre-<id>` na base. Fica fora do git ([mesa](../sessao-zero/mesa.md)).
- **Atualização a cada turno e início de sessão:** PreInvocation devolvendo `injectSteps: [{"ephemeralMessage": "…"}]`. Não existe `additionalContext`. O primeiro PreInvocation de uma conversa conta como início de sessão.
- Reserva final: primeira mensagem ([personagem](../sessao-zero/personagem.md)).

### Skills

- Lê `.agents/skills/<nome>/SKILL.md` nativamente (`.agent/skills` antiga também). `description` é obrigatória; `name` é opcional (padrão: nome da pasta), mas o Mestre sempre escreve, por causa do Codex.
- Carregamento progressivo; cada skill também vira comando `/<nome>`; `/skills reload` recarrega. Não há menção a `.claude/skills/`.

### Hooks

- **Arquivo:** `.agents/hooks.json` do worktree, fora do git ([mesa](../sessao-zero/mesa.md)). As chaves de primeiro nível são nomes livres; o Mestre usa a chave `mestre` e preserva as outras.
- **Só cinco eventos:** PreToolUse, PostToolUse, PreInvocation, PostInvocation, Stop. Não há SessionStart, UserPromptSubmit, PreCompact nem SessionEnd ([mapa de eventos](adaptador.md#mapa-de-eventos)). Todos são ligados.
- PreToolUse e PostToolUse usam grupos `{matcher, hooks}` (matcher é regex; `*` casa tudo); os outros três são listas simples de handlers. Handler: `type: command`, `command`, `timeout` (segundos, padrão 30).
- Os comandos rodam com a pasta do `hooks.json` como pasta atual, por `sh -c` (Linux) ou `cmd /c` (Windows), de forma síncrona: o agente espera.
- **Entrada:** JSON no stdin, `camelCase`, **sem o nome do evento** (o despachante usa o `--event` da linha de comando): `conversationId`, `modelName`, `stepIdx`, `toolCall {name, args}` (argumentos em PascalCase, ex.: `CommandLine`, `Cwd`), `transcriptPath`, `workspacePaths`. PostToolUse acrescenta `error`; Pre/PostInvocation trazem `invocationNum`; Stop traz `executionNum`, `terminationReason` (`model_stop`, `max_steps_exceeded`, `error`), `error`, `fullyIdle`.
- **Saída:** PreToolUse `{"decision": "allow"|"deny"|"ask"|"force_ask"|"deny_unless_prior_grant", "reason", "permissionOverrides", "overwrite"}`; PostToolUse `{}`; PreInvocation `{"injectSteps": […]}`; PostInvocation `{"injectSteps": [], "terminationBehavior": ""}`; Stop `{}` (só `"decision":"continue"` impediria a parada).
- **Falha fechada (testado no PreToolUse):** exit diferente de 0, saída que não é JSON, tempo esgotado e `{}` sem decisão **bloqueiam** a ferramenta. O despachante sempre imprime a decisão explícita ([adaptador](adaptador.md#mestre-hook-despachante)).
- Um `allow` do hook **não** passa por cima do sistema de permissões nativo.
- **Tempos limite gerados:** 30 s em PreToolUse, PreInvocation e PostInvocation; 60 s em PostToolUse e Stop.
- **Binário ausente:** um hook cujo comando não existe bloqueia o agente. O comando gerado testa se o `mestre` existe no caminho estável e, se não existir, imprime a resposta neutra (`{"decision":"allow"}` ou `{}`). A forma exata por sistema (sh ou cmd) fica no gerador.

```json
{"mestre": {"enabled": true,
  "PreToolUse": [{"matcher": "*", "hooks": [{"type": "command",
    "command": "mestre hook --provider antigravity --event PreToolUse", "timeout": 30}]}],
  "Stop": [{"type": "command", "command": "mestre hook --provider antigravity --event Stop", "timeout": 60}]}}
```

### Confiança de pasta

- No modo interativo, pasta sem confiança mostra "Do you trust the contents of this project?" ("Yes, I trust this folder" / "No, exit").
- A confiança fica no array `trustedWorkspaces` (caminhos absolutos) de `~/.gemini/antigravity-cli/settings.json`, com o `HOME` do perfil de conta. **Pré-confiar:** acrescentar o caminho de cada worktree (a lista parece exigir caminho exato). JSON inválido impede o `agy` de iniciar. Não existe flag `--trust`.

### Flags

| Usadas pela base | Para quê |
|---|---|
| `--agent mestre-<id>` | camada do Mestre |
| `--sandbox` | quando a classe não acessa a internet: sem rede, acesso só ao workspace e à pasta temporária |
| `--model <modelo>` | modelo preferido da classe ou ficha, se houver |
| `--conversation <id>` | retomada da conversa do personagem |

**Proibidas:** `--dangerously-skip-permissions`. Ela **não** desliga os hooks (testado: o PreToolUse continuou bloqueando), o que corrige a especificação; segue proibida porque aprova sozinha todo pedido de permissão nativo e anula o `--sandbox` (issue #36), tirando a defesa em profundidade. Também proibidas: `-p` (a base é interativa) e `--add-dir`.

No `settings.json` do perfil: `toolPermission` fica em `request-review` (nunca `always-proceed`), com as regras nativas descritas em [permissões](../classes/permissoes.md).

### Contas múltiplas

- Não há variável de pasta de configuração (issue #155 aberta). O estado fica em `~/.gemini/antigravity-cli/` e o login no chaveiro do sistema (com arquivo de reserva), uma conta Google por usuário.
- Contas separadas só com `HOME` diferente por perfil de conta ([bases e fichas](../orquestracao/bases-e-fichas.md)). Esse `HOME` vale para tudo o que o agente roda (git, caches); a identidade git vem do ambiente montado pelo motor ([identidades](../github/identidades-e-apps.md)).
- `AGY_ACCOUNT` **não** é o login Google (escolhe contas conectadas para comandos como `gh`): não usar.

### Uso, custo e rate limit

- `agy -p "/usage" --output-format json`, com o `HOME` do perfil: `command.data.groups[].buckets[]` com `id`, `window` (`weekly` ou `5h`), `remaining_fraction` e `reset_time`. Não chama modelo nem gasta tokens. O motor roda no fim de cada turno (no máximo uma vez por minuto por perfil, padrão) e a cada 10 minutos com personagem ativo. Uso da janela = 1 − `remaining_fraction`.
- Há um limite semanal e um de 5 horas por grupo de modelos (Gemini; Claude e GPT), consumidos conforme o custo dos tokens.
- Desde a 1.3.3, cota esgotada é avisada na hora: o turno termina com Stop `terminationReason = error`, e o personagem fica "aguardando cota" ([limites](../plataforma/limites-de-uso.md)).

### Telas que o PTY precisa tratar

| Tela | Ação |
|---|---|
| confiar na pasta | evitar (pré-confiança); se aparecer num worktree do Mestre, responder "Yes, I trust this folder" |
| login (URL e código) | escalar como alerta do checklist |
| pedido de permissão nativo | responder com a decisão registrada; sem decisão, negar |
| revisão de artefato ou plano | escalar |

## Regras e erros

- O Antigravity sempre roda com monitoramento reduzido ([adaptador](adaptador.md#monitoramento-reduzido)).
- `settings.json` do perfil é editado relendo, mesclando e validando o JSON antes de gravar; nunca deixar arquivo inválido.

## Critérios de aceite

- **CA-AGY-01** · *rápida* — **Dado** um `.agents/hooks.json` do projeto com outro hook nomeado, **quando** `HookFiles` roda duas vezes, **então** o arquivo mantém o outro hook e tem a chave `mestre` uma vez, com os cinco eventos.
- **CA-AGY-02** · *rápida* — **Dado** um payload gravado em `camelCase` sem nome de evento, **quando** `NormalizeHook` roda com `--event PreToolUse`, **então** o evento interno sai como `pre_tool`, com `tool.kind = shell` e o comando de `CommandLine`.
- **CA-AGY-03** · *rápida* — **Dado** um `settings.json` com outras chaves, **quando** `PreTrust` roda duas vezes, **então** o worktree aparece uma vez em `trustedWorkspaces` e o JSON continua válido.
- **CA-AGY-04** · *rápida* — **Dado** qualquer base e ficha válidas, **quando** `BuildLaunch` monta o comando, **então** ele tem `--agent mestre-<id>` e o `HOME` do perfil, e nunca `--dangerously-skip-permissions`.
- **CA-AGY-05** · *rápida* — **Dado** a saída gravada de `/usage` em JSON, **quando** `ReadUsage` lê, **então** cada janela tem `used_pct = 1 − remaining_fraction` e `resets_at`.
- **CA-AGY-06** · *completa* — **Dado** um nó sem o binário `mestre` no caminho estável, **quando** o comando gerado do PreToolUse roda, **então** imprime `{"decision":"allow"}` e sai com 0.

## Depende de

- [Adaptador](adaptador.md)
- [Sessão zero da mesa](../sessao-zero/mesa.md) e [do personagem](../sessao-zero/personagem.md)
- [Permissões](../classes/permissoes.md)
- [Bases e fichas](../orquestracao/bases-e-fichas.md)

## A confirmar

- **A confirmar:** no modo interativo, se o corpo de `.agents/agents/<nome>.md` com `--agent` soma ao prompt do agente ou o substitui, e quais campos de frontmatter ele exige.
- **A confirmar:** se o `AGENTS.md` da raiz carrega no primeiro turno ou só quando o agente toca um arquivo; e o que acontece com `AGENTS.md` e `GEMINI.md` na mesma pasta.
- **A confirmar:** se `permissionOverrides` no `allow` evita a tela de permissão nativa; e o comportamento de falha de PostToolUse, PreInvocation, PostInvocation e Stop.
- **A confirmar:** se `HOME` separado isola o chaveiro; qual variável vale no Windows (`HOME` ou `USERPROFILE`); se confiar numa pasta cobre as subpastas.
- **A confirmar:** fonte de tokens por turno no modo interativo (linha de status ou `transcript_full.jsonl`) e o texto de erro de cota esgotada.

## Fontes

- https://github.com/google-antigravity/antigravity-cli
- https://github.com/google-antigravity/antigravity-cli/blob/main/CHANGELOG.md
- https://antigravity.google/docs/cli/overview
- https://antigravity.google/docs/cli/install
- https://antigravity.google/docs/cli/headless
- https://antigravity.google/docs/cli/modes
- https://antigravity.google/docs/cli/reference
- https://antigravity.google/docs/hooks
- https://antigravity.google/docs/rules
- https://antigravity.google/docs/skills
- https://antigravity.google/docs/permissions
- https://antigravity.google/docs/subagents
- https://antigravity.google/docs/cli/gcli-migration
- https://antigravity.google/docs/cli/commands/usage
- https://codelabs.developers.google.com/antigravity-cli-hands-on
- https://github.com/quaywin/agys
