# CLI: comandos de comunicação

> **Assunto:** sintaxe de `list`, `ask`, `check`, `note`, `reply`, `done`, `wait`, `inbox` e `waits` · **Componente:** CLI · **Atualizado:** 2026-10-18

## Objetivo

Referência de sintaxe dos comandos que qualquer personagem usa para falar com outros agentes, usar pergaminhos, entregar resultados e esperar. Saída, códigos de saída e identificação seguem [convencoes](convencoes.md); o comportamento de cada ação está no documento linkado em cada comando.

## Comportamento

| Comando | Quem pode usar | Comportamento em |
|---|---|---|
| `list` | qualquer personagem | [conexoes](../orquestracao/conexoes.md) |
| `ask`, `check` | qualquer personagem; alvo conectado ao chamador | [esperas](../orquestracao/esperas-e-caixa-de-entrada.md) |
| `note …` | qualquer personagem de mesa (não o Grão-Mestre: pergaminhos só existem dentro das mesas); pergaminho conectado ao chamador | [pergaminhos-e-tomos](../orquestracao/pergaminhos-e-tomos.md) |
| `reply` | Grão-Mestre | [esperas](../orquestracao/esperas-e-caixa-de-entrada.md) |
| `done` | qualquer personagem | [missoes](../orquestracao/missoes.md) |
| `wait`, `inbox`, `waits` | qualquer personagem | [esperas](../orquestracao/esperas-e-caixa-de-entrada.md) |

Estes comandos valem para qualquer personagem pelo nível; a classe não os restringe: `cli.allow` só libera, para quem está abaixo do nível, os comandos marcados como liberáveis por classe ([convenções](convencoes.md#quem-pode-usar-nível)). Texto que começa com `--` e não tem a forma de uma flag é argumento ([forma geral](convencoes.md#forma-geral)).

### `mestre list`
`mestre list` — sem argumentos. Mostra quem chamou (`you:` nome, id, mesa, máquina), os agentes, pergaminhos e conexões visíveis, com a **máquina** de cada personagem (nome da máquina da Guilda onde ele roda) e o status de cada agente, inclusive o motivo de espera (`aguardando Revisor`, o rótulo do [canvas](../painel/canvas.md); os outros estados saem em inglês: `idle`, `busy`…). Texto: a linha `you:` e, depois, `agents:`, `notes:` e `connections:` com um item por linha (`<A> <-> <B>`).
`--json` → `data`: `{self: {name, id, table, machine}, agents: [{name, id, kind, class, machine, status, state, waiting_for, pinned}], notes: [{name, stack}], connections: [{from, to}]}`; `connections` traz as do chamador com personagens e as dos pergaminhos que ele enxerga.

### `mestre ask`
```
mestre ask "<Nome>" "<prompt>" [--table "<mesa>"] [--timeout <dur>] [--no-wait] [--from-message <id>]
mestre ask --batch '{"A": "<prompt>", "B": "<prompt>"}' [--timeout <dur>]
mestre ask "<Nome>" --raw "<texto>"
```
- Forma normal: põe o prompt na caixa de entrada do alvo e espera a resposta (o texto final do turno dele). `--timeout` padrão e máximo: [tempos limite](../orquestracao/esperas-e-caixa-de-entrada.md#tempos-limite). Texto: a resposta. `--json` → `{to, request_id, status: "answered", response}`.
- Ao estourar o tempo: exit 6, `status: "pending"` (em `--json`, `error.code: "timeout"` e `error.details: {to, request_id, status}`; em texto, o aviso de que o pedido continua aberto); a resposta chega depois na caixa de entrada de quem perguntou (padrão "ask back"). Nunca reenviar: usar `check` e esperar.
- `--no-wait`: entrega e volta na hora (exit 0, `status: "sent"`); a resposta chega pela caixa de entrada. É o jeito de delegar sem ficar bloqueado.
- `--from-message <id>`: repassa uma mensagem da própria caixa de entrada (texto original e anexos, sem reescrever; o `<Nome>` basta, o `<prompt>` é opcional); o `<prompt>`, se houver, vai como nota antes dela, separado por `--- Mensagem repassada ---`. Anexos são copiados para a mesa do alvo e o texto termina com uma linha `[anexo] <caminho da cópia>` para cada um ([anexos](../telegram/anexos-e-transcricao.md)). Mensagem que não está na caixa de quem chamou → 4 `not_found`; anexos que não puderam ser copiados → 5.
- `--batch`: pede a vários em paralelo; a saída é sempre JSON, mesmo sem `--json`: `{results: {"A": {status, response}, …}}`. Exit 0 se todos responderam; 6 se algum ficou `pending`.
- `--raw`: digita entrada crua, com escapes `\n`, `\t`, `\xNN` (ex.: `\x03` = Ctrl+C) e setas `\e[A` `\e[B` `\e[C` `\e[D`; volta assim que digita (exit 0), sem esperar resposta; use `check` para ver a tela. Só para menus, telas que o adaptador não reconheceu (alvo `blocked` com motivo `input`, que é digitado) e terminais de `spawn`.
- Erros: alvo não conectado → 3 `not_connected`; perguntar a si mesmo → 2; alvo ocupado com `--raw` → 7 `busy`; alvo aguardando permissão com `--raw` → 3 `permission_prompt` (alvo `blocked` por `input` recebe a entrada normalmente).

### `mestre check`
`mestre check "<Nome>" [--table "<mesa>"] [--lines <n>]` — lê a tela atual do alvo sem enviar nada. Padrão: a tela visível; `--lines` inclui até `n` linhas do histórico antes dela (`n` que não é número → 2). Texto: a tela, sem sequências de controle. `--json` → `{name, status, waiting_for, screen, captured_at}`. Máquina do alvo fora → 5 `machine_unreachable`.

### `mestre note`
```
mestre note create ["<conteúdo>"] [--name "<nome>"] [--file <arq>] [--stack "<tomo>"]
mestre note read "<nome>"
mestre note write "<nome>" ("<conteúdo>" | --file <arq>) [--append] [--expect-rev <n>]
mestre note edit "<nome>" --old "<trecho>" --new "<trecho>" [--expect-rev <n>]
mestre note stack "<tomo>" "<nome>" ["<nome>" …]
mestre note delete "<nome>"
```
- `create`: cria o pergaminho já conectado a quem chamou. Sem `--name`, o nome é a primeira linha do conteúdo (sem `#`; repetido, ganha ` (2)`, ` (3)`…). `--file -` lê de stdin. `--name` repetido → 7 `name_taken`. O conteúdo vem do argumento **ou** de `--file`, nunca dos dois (→ 2).
- `read`: imprime o Markdown; `--json` → `{name, stack, rev, content, updated_at, updated_by}`.
- `write` troca o conteúdo inteiro (ou acrescenta, com `--append`); `edit` troca um trecho exato, que precisa aparecer uma única vez (senão 7 `edit_ambiguous` ou 4 `edit_not_found`).
- `--expect-rev`: só grava se a revisão ainda for aquela (senão 7 `rev_conflict`), para não apagar a edição de outro agente ou de um usuário.
- `stack`: agrupa os pergaminhos num tomo (cria o tomo se não existir).
- `delete`: ação destrutiva; sem pedido explícito de usuário sai com 9 (regra 1 de [convencoes](convencoes.md)).
- O Grão-Mestre não tem pergaminhos: qualquer `note` dele sai com 3 `level_denied`.

### `mestre reply`
`mestre reply "<texto>" [--attach <arq>]… [--to-message <id>] [--redact-source]` — responde à mensagem de usuário em tratamento (ou à indicada por `--to-message`, id visto em `mestre inbox`); o motor entrega pelo canal de origem. Só o Grão-Mestre: outros → 3 `level_denied`. Sem mensagem de usuário em tratamento → 7 `nothing_to_reply`. Canal de origem fora: exit 0 com `delivery: "queued"`. `--attach` pode se repetir; a CLI confere cada arquivo antes de enviar: inexistente → 4; fora do clone principal e dos worktrees de mesa → 3 `path_not_allowed`; acima de 50 MB → 8 ([anexos](../telegram/anexos-e-transcricao.md)). No M4 só os nomes dos arquivos seguem ao motor; os bytes viajam com os anexos do canal, no M5. Texto com cara de segredo → 3 `secret_detected`. `--redact-source` (M5; até lá a CLI o recusa como flag desconhecida, exit 2): a mensagem respondida trazia uma senha que o motor não reconheceu; o motor a apaga do canal de origem, do histórico e da caixa de entrada e registra a tentativa bloqueada ([credenciais de site](../plataforma/cofre.md#credenciais-de-site)).

### `mestre done`
`mestre done "<resumo>" [--file <arq>] [--proof "C<n>=<prova>"]… [--return "<motivo>" [--cause <causa>] [--where <arq:linha>]…]` — encerra a tarefa ou etapa atual e entrega o resultado. `--proof`, `--cause` e `--where` (desde o M5) e os erros deles (`proof_missing`, `uncommitted_changes`, `verification_running`) estão em [prova e devolução](../orquestracao/prova-e-devolucao.md); com a verificação da classe, o `--json` traz `verification: "pending"`. `--return` devolve o trabalho à etapa anterior prevista na missão (ex.: Revisor reprova), com o motivo; sem devolução prevista → 7 `no_return_step`; voltas esgotadas: o `done` é registrado, a missão fica `paused` e o Mestre da mesa recebe o evento `return_limit`, e a CLI sai com 0, `returned_to: null` e `quest_state: "paused"` (regras em [missoes](../orquestracao/missoes.md)). Resumo até 4.000 caracteres (padrão); maior, use `--file` (Markdown, até 64 KiB; `-` lê de stdin; junto do `<resumo>`, o arquivo vai depois dele, separado por uma linha em branco); um resumo longo sem `--file` → 8 `too_large`. Os arquivos alterados o motor tira do repositório, não do resumo. `--json` → `{task, quest, step, returned_to, quest_state, queued, warnings}` (`warnings`: o que não foi sincronizado, como as alterações não commitadas; `queued` é `false`, e `true` só na forma da [fila de saída](convencoes.md#encaminhamento-à-cidadela); numa tarefa sem missão, `task` é `"task"`). Sem tarefa nem etapa ativa → 7 `no_active_task`, com o `done` registrado e ignorado.

**Tarefa ativa.** É a etapa de missão em andamento ou a mensagem de **outro agente** (item `agent`, de um `ask` ou da abertura da mesa) cujo turno começou, até o `done` que a responde. Um `ask` respondido pela última mensagem do turno, sem `done`, também a encerra. Mensagem de usuário, resposta (`answer`), despertar de espera e aviso do sistema não são tarefa: um `done` depois deles é 7 `no_active_task`. O `done` fora de tarefa fica registrado no histórico do personagem (entra no resumo do encerramento). Depois do `done` o motor sincroniza o worktree ([mesas](../orquestracao/mesas.md)).

### `mestre wait`
```
mestre wait "<Nome>" ["<Nome>" …] [--first] [--timeout <dur>]
mestre wait --quest "<Missão>" [--timeout <dur>]
mestre wait --until "<HH:MM | ISO 8601>"
```
Registra a espera no motor e **volta na hora** com o id da espera; quem chamou deve encerrar o turno, e o motor o acorda com uma mensagem quando a espera termina ou o tempo estoura. Com vários nomes, espera todos; `--first`, o primeiro. `--timeout` padrão: o do workspace. `--json` → `{wait_id, kind, targets, timeout_at}`. Alvo ou missão inexistente → 4; horário que já passou → 2. O chamador passa a `aguardando <alvo>` quando o turno dele termina.

### `mestre inbox`
`mestre inbox [--full]` — fila do próprio personagem, na ordem de entrega: id, origem (`user`, `agent`, `answer`, `quest`, `ritual`, `github`, `system`, `wake`; tipos em [esperas](../orquestracao/esperas-e-caixa-de-entrada.md)), prioridade, chegada e início do texto (`--full`: texto inteiro). Só leitura: quem entrega é o motor. `--json` → `{items: [{id, from, kind, priority, received_at, text}]}`.

### `mestre waits`
`mestre waits [--table <mesa> | --all]` — mapa de esperas: padrão a mesa do chamador (no Grão-Mestre, todas). Uma linha por espera (`Dev -> Revisor  reason  since  timeout_at`) e, no fim, os ciclos (`CYCLE: A -> B -> A`). `--json` → `{waits: [{id, waiter, target, kind, reason, since, timeout_at}], cycles: [[…]]}`. `--all` e outra mesa: só o Grão-Mestre.

`reply`, `--from-message` e `--attach` ganham as mensagens de usuário e os anexos no M5 ([anexos](../telegram/anexos-e-transcricao.md), [ordem de implementação](../desenvolvimento/ordem-de-implementacao.md)).

## Regras e erros

- Nomes fixos: o Grão-Mestre se chama `Grão-Mestre` e o Mestre de cada mesa se chama `Mestre` em `list`, `ask`, `check` e `wait`. Como o Grão-Mestre enxerga os Mestres de todas as mesas, ele indica a mesa com `--table "<mesa>"` (sem isso → 2 `table_required`); dentro de uma mesa, `Mestre` é sempre o da própria mesa.
- **Arquivos:** `--file` e `--attach` são lidos **pela CLI**, que roda como o próprio agente; o motor nunca abre um caminho que um agente nomeia, e o caminho nunca segue na chamada (só o texto, ou o nome do anexo). Arquivo inexistente → 4 `not_found`; acima do limite do comando → 8 `too_large`; binário (com byte nulo) → 2. Caminho relativo vale a partir da pasta atual do chamador; `-` lê de stdin onde o comando diz.
- Alvos e pergaminhos são resolvidos pelo nome dentro do que o chamador enxerga; fora disso → 4 `not_visible`, mesmo que existam em outra mesa.
- `ask`, `check` e `wait` recusam, antes de qualquer efeito, o nome que não está conectado ao chamador (3 `not_connected`), inclusive os nomes de `--batch`; perguntar a si mesmo é 2.
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
- **CA-CLI-COM-12** · *rápida* — **Dado** uma devolução com o limite de voltas já usado, **quando** o Revisor roda `mestre done "x" --return "y"`, **então** sai com 0, `returned_to: null`, a missão fica `paused` e o Mestre da mesa recebe `return_limit`.
- **CA-CLI-COM-13** · *rápida* — **Dado** o Grão-Mestre, **quando** roda `mestre note create "x"`, **então** sai com 3 `level_denied` e nenhum pergaminho é criado.
- **CA-CLI-COM-11** · *rápida* — **Dado** o Grão-Mestre com duas mesas abertas, **quando** roda `mestre ask "Mestre" "status?"` sem `--table`, **então** sai com 2 `table_required`.

## Depende de

- [convencoes](convencoes.md)
- [esperas-e-caixa-de-entrada](../orquestracao/esperas-e-caixa-de-entrada.md) · [conexoes](../orquestracao/conexoes.md)
- [pergaminhos-e-tomos](../orquestracao/pergaminhos-e-tomos.md) · [missoes](../orquestracao/missoes.md) · [mesas](../orquestracao/mesas.md)
