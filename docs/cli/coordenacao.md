# CLI: coordenação da party

> **Assunto:** sintaxe de `recruit`, `spawn`, `dismiss`, `connect`, `sheet`, `base` e `quest` · **Componente:** CLI · **Atualizado:** 2026-10-12

## Objetivo

Referência de sintaxe dos comandos com que o Mestre da mesa monta e conduz a party (e o Grão-Mestre recruta Mestres de mesa). Classes e rituais estão em [coordenacao-classes-e-rituais](coordenacao-classes-e-rituais.md); issues e PRs em [coordenacao-github](coordenacao-github.md). Saída e códigos seguem [convencoes](convencoes.md).

## Comportamento

| Comando | Quem pode usar | Comportamento em |
|---|---|---|
| `recruit`, `spawn` | Mestre da mesa; Grão-Mestre só recruta Mestres de mesa | [personagens](../orquestracao/personagens.md), [bases-e-fichas](../orquestracao/bases-e-fichas.md) |
| `dismiss` | Mestre da mesa; Grão-Mestre ao encerrar mesa | [personagens](../orquestracao/personagens.md) |
| `connect` | Mestre da mesa (na própria mesa); Grão-Mestre (em qualquer mesa do workspace) | [conexoes](../orquestracao/conexoes.md) |
| `sheet list\|show`, `base list\|show` | Mestre da mesa, Grão-Mestre; classes autorizadas | [bases-e-fichas](../orquestracao/bases-e-fichas.md) |
| `quest create\|run\|pause\|resume\|cancel` | Mestre da mesa | [missoes](../orquestracao/missoes.md) |
| `quest status` | Mestre da mesa, Grão-Mestre | [missoes](../orquestracao/missoes.md) |

### `mestre recruit`
```
mestre recruit "<Nome>" --sheet "<Ficha>" [--class "<Classe>"] [--node "<nó>"]
mestre recruit --replace "<Nome>" --sheet "<Ficha>" [--class "<Classe>"]
```
- Cria um personagem a partir de uma ficha existente, já conectado a quem recrutou. `--class` troca a classe padrão da ficha; `--node` pede um nó (que ainda precisa atender às capacidades).
- `--replace` troca a ficha de um personagem existente, mantendo nome, conexões e posição no canvas; o terminal antigo só é encerrado pelas regras de encerramento seguro.
- Texto: `recruited Dev-2 (class Dev, node windows-pc, starting)`. `--json` → `{name, id, sheet, class, node, status, connected_to}`.
- Erros: ficha, classe ou nó inexistente → 4; nenhum nó atende → 4 `no_capable_node` (com o motivo por nó); nome em uso → 7 `name_taken`; `--replace` com alvo ocupado → 9 `scheduled` (o antigo é encerrado no próximo ponto seguro e o novo nasce em seguida); limite de personagens atingido → 9 `waiting_slot` ("pendente": o pedido espera vaga até o tempo limite e o personagem criado, ou a falha `agent_limit` com o aviso, chega pela caixa de entrada; [limites](../orquestracao/personagens.md#limites)); ficha fora do teto → 3 `ceiling_denied`.

### `mestre spawn`
`mestre spawn "<Nome>" --base "<Base>" [--node "<nó>"]` — cria um terminal simples (sem agente) a partir de uma base, conectado a quem chamou. Não há opção de comando livre. Use `ask --raw` para digitar e `check` para ler. `--json` → `{name, id, base, node, status}`. Erros iguais aos de `recruit`.

### `mestre dismiss`
`mestre dismiss "<Nome>"` — encerra um personagem ou terminal. Antes, o resumo do que ele fez vai para o pergaminho da missão. Fixado → 3 `pinned`; ocupado ou sem `done` entregue → 9 `scheduled` (encerrado no próximo ponto seguro, regra 2 de [convencoes](convencoes.md)); outra mesa → 4 `not_visible`. `--json` → `{name, status: "dismissed" | "scheduled"}`.

### `mestre connect`
`mestre connect "<A>" "<B>" [--table "<mesa>"]` — liga agente↔agente, pergaminho↔agente ou pergaminho↔pergaminho na mesma mesa. Já ligados: exit 0, `already_connected: true`. Elemento inexistente → 4. O Grão-Mestre indica a mesa com `--table` (sem isso → 2 `table_required`); itens de mesas diferentes → 2.

### `mestre sheet` e `mestre base`
```
mestre sheet list            mestre sheet show "<Ficha>"
mestre base list             mestre base show "<Base>"
```
Somente leitura. `list` mostra nome, base/agente, classe padrão, ciclo de vida (efêmero/persistente) e nós possíveis; `show` mostra todos os campos, sem segredos (a conta aparece só pelo nome do perfil). `--json` → os campos de [bases-e-fichas](../orquestracao/bases-e-fichas.md).

### `mestre quest`
```
mestre quest create "<Missão>" --file <arq | ->
mestre quest run "<Missão>" [--input "<texto>" | --input-file <arq>]
mestre quest status ["<Missão>"]
mestre quest pause "<Missão>"      mestre quest resume "<Missão>"
mestre quest cancel "<Missão>" [--reason "<texto>"]
```
- `create`: lê a definição (YAML ou JSON, formato em [missoes](../orquestracao/missoes.md)), valida fichas, classes, junções, devoluções e pontos de aprovação, e cria a issue (e sub-issues) sob o épico da mesa. `--json` → `{name, id, issue, steps}`. Definição inválida → 2 `invalid_quest` com a lista de problemas; ficha inexistente → 4.
- `run`: dispara e **volta na hora** (exit 0, `status: "running"`); o motor conduz e avisa o Mestre da mesa pela caixa de entrada. Já rodando → 7 `already_running`.
- `status`: sem nome, lista as missões da mesa; com nome, mostra cada etapa (número, personagem, estado, voltas usadas, espera atual) e a etapa atual. `--json` → `{name, state, current_step, steps: [{n, sheet, agent, state, loops, waiting_for, issue}]}`.
- `pause` deixa a etapa atual terminar e não dispara a próxima; `resume` continua de onde parou; `cancel` encerra a missão (a etapa em andamento termina antes) e atualiza a issue. Estado incompatível → 7 `invalid_state`.

## Regras e erros

- O Grão-Mestre não monta party de mesa: `recruit` dele só aceita a ficha de Mestre de mesa (outras → 3 `level_denied`); normalmente o Mestre é recrutado por `table open` ([grao-mestre](grao-mestre.md)).
- Personagens criados por etapa de missão são recrutados pelo motor, não pela CLI.
- Ao atingir um limite, nada que está trabalhando é derrubado: a criação espera vaga (9 `waiting_slot`) e, vencido o tempo limite, falha com aviso pela caixa de entrada, conforme [personagens](../orquestracao/personagens.md#limites).

## Critérios de aceite

- **CA-CLI-COORD-01** · *rápida* — **Dado** uma ficha "QA Codex" que exige `gpu: true` e nenhum nó com `gpu`, **quando** o Mestre da mesa roda `mestre recruit "QA-1" --sheet "QA Codex"`, **então** sai com 4 `no_capable_node` e a mensagem diz que capacidade faltou em cada nó.
- **CA-CLI-COORD-02** · *rápida* — **Dado** um personagem comum, **quando** roda `mestre recruit "X" --sheet "Dev Claude"`, **então** sai com 3 `level_denied`.
- **CA-CLI-COORD-03** · *rápida* — **Dado** um personagem fixado, **quando** o Mestre da mesa roda `mestre dismiss` nele, **então** sai com 3 `pinned` e o personagem continua vivo.
- **CA-CLI-COORD-04** · *completa* — **Dado** "Dev-1" com duas conexões, **quando** o Mestre roda `mestre recruit --replace "Dev-1" --sheet "Dev Codex"`, **então** o novo terminal usa a ficha nova, mantém o nome, as duas conexões e a posição.
- **CA-CLI-COORD-05** · *rápida* — **Dado** `mestre spawn "srv" --base "bash" --command "rm -rf /"`, **quando** roda, **então** sai com 2 `usage` (opção inexistente).
- **CA-CLI-COORD-06** · *rápida* — **Dado** uma definição com devolução sem número máximo de voltas, **quando** roda `mestre quest create`, **então** sai com 2 `invalid_quest` apontando a etapa.
- **CA-CLI-COORD-08** · *rápida* — **Dado** um personagem `busy`, **quando** o Mestre da mesa roda `mestre dismiss` nele, **então** sai com 9 `scheduled` e o personagem continua vivo até o próximo ponto seguro.
- **CA-CLI-COORD-07** · *rápida* — **Dado** uma missão válida, **quando** o Mestre roda `mestre quest run`, **então** o comando volta em menos de 1s com `status: "running"`.

## Depende de

- [convencoes](convencoes.md)
- [personagens](../orquestracao/personagens.md) · [bases-e-fichas](../orquestracao/bases-e-fichas.md) · [conexoes](../orquestracao/conexoes.md)
- [missoes](../orquestracao/missoes.md) · [teto-do-workspace](../classes/teto-do-workspace.md)
