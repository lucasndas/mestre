# Despachante de hooks (`mestre hook`)

> **Assunto:** o comando `mestre hook`: fluxo de cada chamada, prazos, modo degradado, erro interno e formato das respostas por provedor · **Componente:** Todos · **Atualizado:** 2026-10-12

## Objetivo

Ser o único comando que os arquivos de hooks dos três provedores chamam, com a mesma lógica para todos: normaliza o evento pelo [adaptador](adaptador.md), pede ao motor local a decisão ou o contexto e devolve a resposta no formato do provedor, rápido e com falha segura. O porquê está no [ADR 0012](../decisoes/0012-despachante-unico-de-hooks.md).

## Comportamento

### Uma chamada

Uso interno, só pelos arquivos de hooks gerados: `mestre hook --provider <id> --event <evento-nativo>`. Agentes não usam.

1. **Sem `MESTRE_AGENT_ID` no ambiente:** responde neutro e sai na hora, sem ler configuração nem abrir conexão. Neutro é: nada no stdout e exit 0 (Claude, Codex); `{"decision":"allow"}` no PreToolUse e JSON vazio válido nos demais eventos (Antigravity).
2. Lê o stdin (até 4 MiB, padrão; o excesso é descartado e marcado como truncado) e normaliza (`NormalizeHook`).
3. Pede ao motor do próprio nó, pelo [canal local](../arquitetura/canal-local.md) (`cmd: "hook"`), a decisão (`pre_tool`, `permission_request`) ou o contexto (`session_start`, `turn_start`). A decisão é local: o nó já tem as [regras efetivas](../classes/permissoes.md#regras-efetivas) do personagem e aplica o [pipeline de permissões](../classes/permissoes.md#pipeline-de-decisão) sem ida ao Core. A resposta traz também os hooks síncronos da classe a rodar ([classes](../classes/classes.md#hooks-extras-da-classe)).
4. **Prazo:** a decisão sai em menos de 1 s (padrão: 800 ms para `pre_tool` e `permission_request`; 300 ms para eventos só informativos, que o motor confirma e processa depois). Hooks síncronos da classe podem usar mais tempo, até o tempo limite deles, sempre 2 s abaixo do tempo limite configurado no provedor.
5. Formata a resposta (`FormatResponse`, tabela abaixo) e sai.

### Modo degradado

Motor fora do ar (sem conexão ou sem resposta no prazo): valem só as regras locais que o hook checa sozinho:
- as regras fixas de segurança embutidas no binário ([permissões](../classes/permissoes.md#regras-fixas-de-segurança));
- o retrato das regras efetivas do personagem, `policy.json`, lido de `/var/lib/mestre/agentes/<id>/` no Linux ou `%ProgramData%\Mestre\no\agentes\<id>\` no Windows ([persistência](../arquitetura/persistencia.md#arquivos)). Nos dois sistemas o hook roda como `mestre-agente`, que pode ler o retrato e não pode alterá-lo.

O que elas negarem é negado; o que pediria aprovação é negado com o motivo "motor indisponível, tente depois"; o resto é liberado. O evento vai para a fila local do hook (`/var/spool/mestre/hook/`, ou `no\fila-hook\` no Windows), em que o hook só cria arquivos; o motor reenvia ao voltar e valida cada arquivo antes de usar. Em `turn_start`, o turno segue sem contexto novo.

### Erro interno do próprio hook

Em `pre_tool` e `permission_request`, bloqueia (Claude e Codex: exit 2 com o motivo no stderr; Antigravity: decisão `deny`); nos demais eventos, exit 0 sem saída (no Antigravity, exit 0 com a resposta neutra em JSON: o despachante trata todos os eventos dele como falha fechada, sem depender de testar cada um).

### Respostas por provedor

| Resposta | Claude Code | Codex | Antigravity |
|---|---|---|---|
| liberar | exit 0, `permissionDecision: allow` | exit 0, `permissionDecision: allow` | `{"decision":"allow"}` |
| negar | exit 0, `permissionDecision: deny` + motivo | exit 0, `permissionDecision: deny` + motivo | `{"decision":"deny","reason":…}` |
| permissão nativa | `decision.behavior` no PermissionRequest | `decision.behavior` no PermissionRequest | — |
| contexto do turno | `additionalContext` | `additionalContext` | `injectSteps[{ephemeralMessage}]` no PreInvocation |
| fim de turno | exit 0, sem saída | exit 0, sem saída | `{}` |

No Claude, nunca usar exit 1 para bloquear: exit 1 não bloqueia. No Antigravity, toda resposta precisa ser JSON válido com decisão explícita no PreToolUse, porque `{}`, saída inválida, exit diferente de 0 ou tempo esgotado bloqueiam.

## Regras e erros

- Evento nativo desconhecido: registrado no log e respondido com a resposta neutra.
- Fila local cheia (10.000 eventos, padrão): descarta os mais antigos de tipo informativo, nunca decisões já tomadas.
- `policy.json` ausente ou ilegível no modo degradado: só as regras fixas valem, e tudo o que não é só leitura é negado com "motor indisponível".

## Critérios de aceite

- **CA-HOOK-01** · *rápida* — **Dado** o ambiente sem `MESTRE_AGENT_ID` e o despachante montado com dublês de arquivos e de canal, **quando** roda para qualquer provedor e evento, **então** devolve a resposta neutra do provedor sem nenhuma leitura de arquivo nem abertura de conexão.
- **CA-HOOK-02** · *rápida* — **Dado** um motor falso que não responde, **quando** chega um `pre_tool` que as regras locais não negam, **então** a resposta é liberar no prazo (relógio falso) e o evento fica na fila local.
- **CA-HOOK-03** · *rápida* — **Dado** um motor falso que não responde, **quando** chega um `pre_tool` de `git push`, **então** a resposta é negar com motivo.
- **CA-HOOK-04** · *rápida* — **Dado** um erro interno simulado em `pre_tool`, **quando** o hook termina, **então** sai com exit 2 (Claude, Codex) ou `deny` (Antigravity), e nunca com exit 1.
- **CA-HOOK-05** · *rápida* — **Dado** cada decisão (liberar, negar), **quando** `FormatResponse` formata para o Antigravity, **então** o stdout é JSON válido com `decision` explícito.
- **CA-HOOK-06** · *rápida* — **Dado** o plano de pastas da instalação Linux, **quando** é gerado, **então** `agentes/` tem modo 0750 e `policy.json` 0640, ambos do dono `mestre` e grupo `mestre-agente`, e a fila do hook tem 1730.
- **CA-HOOK-07** · *completa* — **Dado** motor real e o agente falso num PTY real, **quando** o agente dispara 200 `pre_tool`, **então** o tempo de cada chamada de `mestre hook`, do início do processo à saída, tem p95 abaixo de 1 s.

## Depende de

- [Adaptador](adaptador.md) · [Canal local](../arquitetura/canal-local.md)
- [Permissões](../classes/permissoes.md) (pipeline, regras efetivas, regras fixas)

## Verificar na implementação

- **Verificar na implementação:** os hooks disparam para as ferramentas usadas **dentro** de um subagente nativo, com o mesmo `MESTRE_AGENT_ID` do personagem, nos três provedores ([subagentes nativos](../classes/permissoes.md#subagentes-nativos)). **Padrão:** disparam (o subagente roda no mesmo processo e herda o ambiente). **Se falhar:** naquele provedor e nó, as regras efetivas passam a negar a ferramenta de subagente no próprio personagem (a chamada ao subagente passa pelo `pre_tool` do personagem antes de existir), e o checklist avisa. **Onde:** etapa 12 do [teste de conformidade](conformidade.md).
