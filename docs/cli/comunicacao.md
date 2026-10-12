# CLI: comandos de comunicação

> **Assunto:** sintaxe de `list`, `ask`, `check`, `note`, `reply`, `done`, `wait`, `inbox` e `waits` · **Componente:** CLI · **Atualizado:** 2026-10-12

## Objetivo

Referência de sintaxe dos comandos que qualquer personagem usa para falar com outros agentes, usar pergaminhos, entregar resultados e esperar. Saída, códigos de saída e identificação seguem [convencoes](convencoes.md); o comportamento de cada ação está no documento linkado em cada comando.

## Comportamento

| Comando | Quem pode usar | Comportamento em |
|---|---|---|
| `list` | qualquer personagem | [conexoes](../orquestracao/conexoes.md) |
| `ask`, `check` | qualquer personagem; alvo conectado ao chamador | [esperas](../orquestracao/esperas-e-caixa-de-entrada.md) |
| `note …` | qualquer personagem; pergaminho conectado ao chamador | [pergaminhos-e-tomos](../orquestracao/pergaminhos-e-tomos.md) |
| `reply` | Grão-Mestre | [esperas](../orquestracao/esperas-e-caixa-de-entrada.md) |
| `done` | qualquer personagem | [missoes](../orquestracao/missoes.md) |
| `wait`, `inbox`, `waits` | qualquer personagem | [esperas](../orquestracao/esperas-e-caixa-de-entrada.md) |

A classe pode restringir qualquer um deles ([permissoes](../classes/permissoes.md)).

### `mestre list`
`mestre list` — sem argumentos. Mostra quem chamou (`you:` nome, id, mesa, nó), os agentes, pergaminhos e conexões visíveis, com o nó e o status de cada agente (inclusive o motivo de espera, ex.: `waiting for Revisor`).
`--json` → `data`: `{self, agents: [{name, id, class, node, status, waiting_for, pinned}], notes: [{name, stack}], connections: [{from, to}]}`.

### `mestre ask`
```
mestre ask "<Nome>" "<prompt>" [--table "<mesa>"] [--timeout <dur>] [--no-wait] [--from-message <id>]
mestre ask --batch '{"A": "<prompt>", "B": "<prompt>"}' [--timeout <dur>]
mestre ask "<Nome>" --raw "<texto>"
```
- Forma normal: põe o prompt na caixa de entrada do alvo e espera a resposta (o texto final do turno dele). `--timeout` padrão e máximo: [tempos limite](../orquestracao/esperas-e-caixa-de-entrada.md#tempos-limite). Texto: a resposta. `--json` → `{to, request_id, status: "answered", response}`.
- Ao estourar o tempo: exit 6, `status: "pending"`; a resposta chega depois na caixa de entrada de quem perguntou (padrão "ask back"). Nunca reenviar: usar `check` e esperar.
- `--no-wait`: entrega e volta na hora (exit 0, `status: "sent"`); a resposta chega pela caixa de entrada. É o jeito de delegar sem ficar bloqueado.
- `--from-message <id>`: repassa uma mensagem da própria caixa de entrada (texto original e anexos, sem reescrever); o `<prompt>`, se houver, vai como nota antes dela. Anexos são copiados para a mesa do alvo ([anexos](../telegram/anexos-e-transcricao.md)).
- `--batch`: pede a vários em paralelo; a saída é sempre JSON, mesmo sem `--json`: `{results: {"A": {status, response}, …}}`. Exit 0 se todos responderam; 6 se algum ficou `pending`.
- `--raw`: digita entrada crua, com escapes `\n`, `\t`, `\xNN` (ex.: `\x03` = Ctrl+C) e setas `\e[A` `\e[B` `\e[C` `\e[D`; volta assim que digita (exit 0), sem esperar resposta; use `check` para ver a tela. Só para menus e terminais de `spawn`.
- Erros: alvo não conectado → 3 `not_connected`; perguntar a si mesmo → 2; alvo ocupado com `--raw` → 7 `busy`; alvo aguardando permissão com `--raw` → 3 `permission_prompt`.

### `mestre check`
`mestre check "<Nome>" [--table "<mesa>"] [--lines <n>]` — lê a tela atual do alvo sem enviar nada. Padrão: a tela visível; `--lines` inclui até `n` linhas do histórico. Texto: a tela, sem sequências de controle. `--json` → `{name, status, waiting_for, screen, captured_at}`. Nó do alvo fora → 5 `node_unreachable`.

### `mestre note`
```
mestre note create ["<conteúdo>"] [--name "<nome>"] [--file <arq>] [--stack "<tomo>"]
mestre note read "<nome>"
mestre note write "<nome>" ("<conteúdo>" | --file <arq>) [--append] [--expect-rev <n>]
mestre note edit "<nome>" --old "<trecho>" --new "<trecho>" [--expect-rev <n>]
mestre note stack "<tomo>" "<nome>" ["<nome>" …]
mestre note delete "<nome>"
```
- `create`: cria o pergaminho já conectado a quem chamou. Sem `--name`, o nome é a primeira linha do conteúdo (sem `#`). `--file -` lê de stdin. Nome repetido → 7 `name_taken`.
- `read`: imprime o Markdown; `--json` → `{name, stack, rev, content, updated_at, updated_by}`.
- `write` troca o conteúdo inteiro (ou acrescenta, com `--append`); `edit` troca um trecho exato, que precisa aparecer uma única vez (senão 7 `edit_ambiguous` ou 4 `edit_not_found`).
- `--expect-rev`: só grava se a revisão ainda for aquela (senão 7 `rev_conflict`), para não apagar a edição de outro agente ou de um usuário.
- `stack`: agrupa os pergaminhos num tomo (cria o tomo se não existir).
- `delete`: ação destrutiva; sem pedido explícito de usuário sai com 9 (regra 1 de [convencoes](convencoes.md)).

### `mestre reply`
`mestre reply "<texto>" [--attach <arq>]… [--to-message <id>]` — responde à mensagem de usuário em tratamento (ou à indicada por `--to-message`, id visto em `mestre inbox`); o motor entrega pelo canal de origem. Só o Grão-Mestre: outros → 3 `level_denied`. Sem mensagem de usuário em tratamento → 7 `nothing_to_reply`. Canal de origem fora: exit 0 com `delivery: "queued"`. Anexo inexistente → 4; acima do limite → 8 ([anexos](../telegram/anexos-e-transcricao.md)). Texto com cara de segredo → 3 `secret_detected`.

### `mestre done`
`mestre done "<resumo>" [--file <arq>] [--return "<motivo>"]` — encerra a tarefa ou etapa atual e entrega o resultado. `--return` devolve o trabalho à etapa anterior prevista na missão (ex.: Revisor reprova), com o motivo; sem devolução prevista → 7 `no_return_step`; voltas esgotadas → 8 `return_limit` (regras em [missoes](../orquestracao/missoes.md)). Resumo até 4.000 caracteres (padrão); maior, use `--file` (Markdown, até 64 KiB). Os arquivos alterados o motor tira do git, não do resumo. `--json` → `{task, quest, step, returned_to, queued}`. Sem tarefa ativa → 7 `no_active_task`. Depois do `done` o motor sincroniza o worktree ([mesas](../orquestracao/mesas.md)).

### `mestre wait`
```
mestre wait "<Nome>" ["<Nome>" …] [--first] [--timeout <dur>]
mestre wait --quest "<Missão>" [--timeout <dur>]
mestre wait --until "<HH:MM | ISO 8601>"
```
Registra a espera no motor e **volta na hora** com o id da espera; quem chamou deve encerrar o turno, e o motor o acorda com uma mensagem quando a espera termina ou o tempo estoura. Com vários nomes, espera todos; `--first`, o primeiro. `--timeout` padrão: o do workspace. `--json` → `{wait_id, kind, targets, timeout_at}`. Alvo ou missão inexistente → 4.

### `mestre inbox`
`mestre inbox [--full]` — fila do próprio personagem, na ordem de entrega: id, origem (`user`, `agent`, `quest`, `github`, `ritual`, `system`), prioridade, chegada e início do texto (`--full`: texto inteiro). Só leitura: quem entrega é o motor. `--json` → `{items: [{id, from, kind, priority, received_at, text}]}`.

### `mestre waits`
`mestre waits [--table <mesa> | --all]` — mapa de esperas: padrão a mesa do chamador (no Grão-Mestre, todas). Uma linha por espera (`Dev -> Revisor  reason  since  timeout_at`) e, no fim, os ciclos (`CYCLE: A -> B -> A`). `--json` → `{waits: [{id, waiter, target, kind, reason, since, timeout_at}], cycles: [[…]]}`. `--all` e outra mesa: só o Grão-Mestre.

## Regras e erros

- Nomes fixos: o Grão-Mestre se chama `Grão-Mestre` e o Mestre de cada mesa se chama `Mestre` em `list`, `ask`, `check` e `wait`. Como o Grão-Mestre enxerga os Mestres de todas as mesas, ele indica a mesa com `--table "<mesa>"` (sem isso → 2 `table_required`); dentro de uma mesa, `Mestre` é sempre o da própria mesa.
- Alvos e pergaminhos são resolvidos pelo nome dentro do que o chamador enxerga; fora disso → 4 `not_visible`, mesmo que existam em outra mesa.
- `ask` a um alvo ocupado não é erro: a mensagem fica na fila e o tempo limite corre.
- Um `ask` com tempo estourado continua valendo: a resposta tardia resolve o pedido e é entregue completa.

## Critérios de aceite

- **CA-CLI-COM-01** · *rápida* — **Dado** um alvo que não responde no tempo limite padrão, **quando** alguém roda `mestre ask "Dev" "…"` sem `--timeout`, **então** sai com 6, `status: "pending"`, e a resposta posterior chega na caixa de entrada de quem perguntou.
- **CA-CLI-COM-02** · *rápida* — **Dado** `--batch '{"A":"x","B":"y"}'` sem `--json`, **quando** os dois respondem, **então** stdout é um JSON com `results.A` e `results.B` e o código é 0.
- **CA-CLI-COM-03** · *rápida* — **Dado** `--raw "1\n\x1b[B"`, **quando** o motor recebe, **então** os bytes digitados são `31 0a 1b 5b 42`.
- **CA-CLI-COM-04** · *rápida* — **Dado** `mestre note create "# Plano de testes\n…"` sem `--name`, **quando** roda, **então** o pergaminho se chama "Plano de testes" e já está conectado a quem chamou.
- **CA-CLI-COM-05** · *rápida* — **Dado** um pergaminho na revisão 4, **quando** alguém roda `note write … --expect-rev 3`, **então** sai com 7 `rev_conflict` e o conteúdo não muda.
- **CA-CLI-COM-06** · *rápida* — **Dado** um personagem que não é Grão-Mestre, **quando** roda `mestre reply "oi"`, **então** sai com 3 `level_denied`.
- **CA-CLI-COM-07** · *rápida* — **Dado** um personagem sem tarefa ativa, **quando** roda `mestre done "x"`, **então** sai com 7 `no_active_task`.
- **CA-CLI-COM-08** · *rápida* — **Dado** `mestre wait "Revisor" --timeout 10m`, **quando** roda, **então** volta em menos de 1s com `wait_id` e o chamador aparece como aguardando Revisor em `mestre list`.
- **CA-CLI-COM-09** · *rápida* — **Dado** `mestre ask "Mestre" --table "42-login" --from-message <id>` de uma mensagem com um anexo, **quando** roda, **então** o alvo recebe o texto original sem alteração e o caminho da cópia do anexo na mesa dele.
- **CA-CLI-COM-10** · *rápida* — **Dado** uma etapa de Revisor com devolução prevista ao Dev, **quando** o Revisor roda `mestre done "faltam testes" --return "sem testes de erro"`, **então** sai com 0 e `returned_to` aponta a etapa do Dev.
- **CA-CLI-COM-11** · *rápida* — **Dado** o Grão-Mestre com duas mesas abertas, **quando** roda `mestre ask "Mestre" "status?"` sem `--table`, **então** sai com 2 `table_required`.

## Depende de

- [convencoes](convencoes.md)
- [esperas-e-caixa-de-entrada](../orquestracao/esperas-e-caixa-de-entrada.md) · [conexoes](../orquestracao/conexoes.md)
- [pergaminhos-e-tomos](../orquestracao/pergaminhos-e-tomos.md) · [missoes](../orquestracao/missoes.md) · [mesas](../orquestracao/mesas.md)
- [anexos-e-transcricao](../telegram/anexos-e-transcricao.md)
