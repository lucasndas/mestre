# CLI: coordenação da party

> **Assunto:** sintaxe de `recruit`, `spawn`, `dismiss`, `connect`, `sheet`, `base` e `quest` · **Componente:** CLI · **Atualizado:** 2026-10-18

## Objetivo

Referência de sintaxe dos comandos com que o Mestre da mesa monta e conduz a party (e o Grão-Mestre recruta Mestres de mesa e o Operador do PC). Classes e rituais estão em [coordenacao-classes-e-rituais](coordenacao-classes-e-rituais.md); issues e PRs em [coordenacao-github](coordenacao-github.md). Saída e códigos seguem [convencoes](convencoes.md).

## Comportamento

| Comando | Quem pode usar | Comportamento em |
|---|---|---|
| `recruit`, `spawn` | Mestre da mesa; Grão-Mestre só recruta Mestres de mesa e o Operador do PC | [personagens](../orquestracao/personagens.md), [bases-e-fichas](../orquestracao/bases-e-fichas.md) |
| `dismiss` | Mestre da mesa; Grão-Mestre ao encerrar mesa | [personagens](../orquestracao/personagens.md) |
| `connect` | Mestre da mesa (na própria mesa); Grão-Mestre (em qualquer mesa do workspace) | [conexoes](../orquestracao/conexoes.md) |
| `sheet list\|show`, `base list\|show` | Mestre da mesa, Grão-Mestre; classes autorizadas | [bases-e-fichas](../orquestracao/bases-e-fichas.md) |
| `quest create\|run\|pause\|resume\|cancel` | Mestre da mesa | [missoes](../orquestracao/missoes.md) |
| `quest status` | Mestre da mesa, Grão-Mestre | [missoes](../orquestracao/missoes.md) |

### `mestre recruit`
```
mestre recruit "<Nome>" --sheet "<Ficha>" [--class "<Classe>"] [--machine "<máquina>"]
mestre recruit --replace "<Nome>" --sheet "<Ficha>" [--class "<Classe>"]
mestre recruit "<Nome>" --sheet "<Ficha da base Operador>" --task "<tarefa>" [--for <duração>] [--table "<mesa>"]
```
- Cria um personagem a partir de uma ficha existente, já conectado a quem recrutou. `--class` troca a classe padrão da ficha; `--machine` pede uma máquina da Guilda (que ainda precisa atender aos requisitos; [ficha da máquina](../guilda/ficha-da-maquina.md)).
- `--replace` troca a ficha de um personagem existente, mantendo nome, conexões e posição no canvas; o terminal antigo só é encerrado pelas regras de encerramento seguro.
- Texto: `recruited Dev-2 (class Dev, machine windows-pc, starting)`. `--json` → `{name, id, sheet, class, machine, status, connected_to}` (`connected_to`: quem recrutou; no `--replace`, o novo herda também as conexões do antigo). Sem `<Nome>`, o nome é `<ficha> <n>`.
- Erros: sem `--sheet` → 2; ficha, classe ou máquina inexistente → 4 (a mensagem lista as fichas válidas); nenhuma máquina atende → 4 `no_capable_machine` (com o primeiro requisito não atendido de cada máquina; o Dono também é avisado); nome em uso → 7 `name_taken`; `--replace` com alvo ocupado → 9 `scheduled` (o antigo é encerrado no próximo ponto seguro e o novo nasce em seguida); limite de personagens atingido → 9 `waiting_slot` ("pendente": o pedido espera vaga até o tempo limite e o personagem criado, ou a falha `agent_limit` com o aviso, chega pela caixa de entrada; `--json` traz `data: {status: "pending", wait, limit}`; [limites](../orquestracao/personagens.md#limites)); ficha fora do teto → 3 `ceiling_denied`. O Grão-Mestre indica a mesa com `--table` (sem isso → 2 `table_required`) e só recruta a ficha de Mestre de mesa do workspace e as da base Operador.
- **Operador do PC** (ficha da base Operador): não cria o personagem na hora; o motor abre o pedido `ativacao_operador` com a tarefa (`--task`, obrigatória) e a duração (`--for`, padrão `30m`) e volta com 9 `approval_pending` e o id do pedido; o personagem nasce depois da aprovação, e o resultado chega pela caixa de entrada. O Grão-Mestre indica a mesa com `--table`. Classe sem `operator: true` ou teto que não permite → 3 `operator_denied`; nenhuma máquina com o Operador habilitado → 4 `no_capable_machine`; `--for` acima do máximo → 2; `--task` ou `--for` numa ficha que não é da base Operador → 2. O pedido ao usuário é do M5 e o Operador do M7: até lá o motor responde 5 `unavailable` a quem pede. Comportamento em [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc).

### `mestre spawn`
`mestre spawn "<Nome>" --base "<Base>" [--machine "<máquina>"]` — cria um terminal simples (sem agente) a partir de uma base, conectado a quem chamou. Não há opção de comando livre. Use `ask --raw` para digitar e `check` para ler. `--json` → `{name, id, base, machine, status}`. Só o Mestre da mesa (o Grão-Mestre → 3 `level_denied`). Erros iguais aos de `recruit`, e uma base inexistente é 4. Texto: `spawned srv (base bash, machine linux, starting)`.

### `mestre dismiss`
`mestre dismiss "<Nome>"` — encerra um personagem ou terminal. Antes, o resumo do que ele fez vai para o pergaminho da missão. Fixado → 3 `pinned`; ocupado ou sem `done` entregue → 9 `scheduled` (encerrado no próximo ponto seguro, regra 2 de [convencoes](convencoes.md)); outra mesa → 4 `not_visible`. O Grão-Mestre indica a mesa com `--table`. `--json` → `{name, status: "dismissed" | "scheduled"}`; `scheduled` sai com exit 9 e o `status` vai em `data`, ao lado do erro.

### `mestre connect`
`mestre connect "<A>" "<B>" [--table "<mesa>"]` — liga agente↔agente, pergaminho↔agente ou pergaminho↔pergaminho na mesma mesa. Já ligados: exit 0, `already_connected: true`. Elemento inexistente → 4. O Grão-Mestre indica a mesa com `--table` (sem isso → 2 `table_required`); itens de mesas diferentes → 2 `different_tables`.

### `mestre sheet` e `mestre base`
```
mestre sheet list            mestre sheet show "<Ficha>"
mestre base list             mestre base show "<Base>"
```
Somente leitura. `list` mostra nome, base/agente, classe padrão, ciclo de vida (efêmero/persistente), requisitos e máquinas que atendem agora; `show` mostra todos os campos, sem segredos (a conta aparece só pelo nome do perfil; da base, as variáveis de ambiente aparecem só pelo nome, nunca pelo valor). Inexistente → 4. `--json` → `{sheets: […]}`/`{sheet, machines}` e `{bases: […]}`/`{base}`, com os campos de [bases-e-fichas](../orquestracao/bases-e-fichas.md).

### `mestre quest`
```
mestre quest create "<Missão>" (--file <arq | -> | --recipe <nome> [--skip <id>]…)
mestre quest run "<Missão>" [--input "<texto>" | --input-file <arq>]
mestre quest status ["<Missão>"]
mestre quest pause "<Missão>"      mestre quest resume "<Missão>"
mestre quest cancel "<Missão>" [--reason "<texto>"]
```
- `create`: a CLI lê a definição de `--file` (`-` lê de stdin; o motor nunca abre um caminho) e a envia; ela é YAML ou JSON, formato em [missoes](../orquestracao/missoes.md). O motor valida fichas, classes, junções, devoluções e pontos de aprovação, e cria a issue (e sub-issues) sob o épico da mesa. `--json` → `{name, id, issue, steps}`. Definição inválida → 2 `invalid_quest` com a lista de problemas, cada um com a etapa; ficha inexistente → 4. `max_returns` tem padrão 3, e só um valor negativo (devolução sem limite) é inválido numa etapa com `can_return_to`.
- `run`: `--input-file` lê o `--input` de um arquivo (`-` é stdin; os dois juntos → 2). Dispara e **volta na hora** (exit 0, `status: "running"`); o motor conduz e avisa o Mestre da mesa pela caixa de entrada. Já rodando → 7 `already_running`.
- `status`: sem nome, lista as missões da mesa; com nome, mostra cada etapa (número, personagem, estado, voltas usadas, espera atual) e a etapa atual. `--json` → `{name, state, current_step, steps: [{n, sheet, agent, state, loops, waiting_for, issue}]}` (`current_step`: o `n` da etapa em andamento, ou `null`; sem nome, `{quests: [{name, id, state, round}]}`). Texto: a missão com o estado e uma linha por etapa (`1. dev  Dev-1  running  loops 0`). O Grão-Mestre indica a mesa com `--table`.
- `pause` deixa a etapa atual terminar e não dispara a próxima; `resume` continua de onde parou; `cancel` encerra a missão (a etapa em andamento termina antes) e atualiza a issue. Estado incompatível → 7 `invalid_state`.

## Regras e erros

- O Grão-Mestre não monta party de mesa: `recruit` dele só aceita a ficha de Mestre de mesa e as da base Operador (outras → 3 `level_denied`); normalmente o Mestre é recrutado por `table open` ([grao-mestre](grao-mestre.md)).
- Personagens criados por etapa de missão são recrutados pelo motor, não pela CLI.
- Ao atingir um limite, nada que está trabalhando é derrubado: a criação espera vaga (9 `waiting_slot`) e, vencido o tempo limite, falha com aviso pela caixa de entrada, conforme [personagens](../orquestracao/personagens.md#limites).

## Critérios de aceite

- **CA-CLI-COORD-01** · *rápida* — **Dado** uma ficha "QA Codex" que exige `gpu: true` e nenhuma máquina com GPU, **quando** o Mestre da mesa roda `mestre recruit "QA-1" --sheet "QA Codex"`, **então** sai com 4 `no_capable_machine` e a mensagem diz que requisito faltou em cada máquina.
- **CA-CLI-COORD-02** · *rápida* — **Dado** um personagem comum, **quando** roda `mestre recruit "X" --sheet "Dev Claude"`, **então** sai com 3 `level_denied`.
- **CA-CLI-COORD-03** · *rápida* — **Dado** um personagem fixado, **quando** o Mestre da mesa roda `mestre dismiss` nele, **então** sai com 3 `pinned` e o personagem continua vivo.
- **CA-CLI-COORD-04** · *completa* — **Dado** "Dev-1" com duas conexões, **quando** o Mestre roda `mestre recruit --replace "Dev-1" --sheet "Dev Codex"`, **então** o novo terminal usa a ficha nova, mantém o nome, as duas conexões e a posição.
- **CA-CLI-COORD-05** · *rápida* — **Dado** `mestre spawn "srv" --base "bash" --command "rm -rf /"`, **quando** roda, **então** sai com 2 `usage` (opção inexistente).
- **CA-CLI-COORD-06** · *rápida* — **Dado** uma definição com `can_return_to` e `max_returns: -1` (devolução sem limite), **quando** roda `mestre quest create`, **então** sai com 2 `invalid_quest` apontando a etapa.
- **CA-CLI-COORD-08** · *rápida* — **Dado** um personagem `busy`, **quando** o Mestre da mesa roda `mestre dismiss` nele, **então** sai com 9 `scheduled` e o personagem continua vivo até o próximo ponto seguro.
- **CA-CLI-COORD-07** · *rápida* — **Dado** uma missão válida, **quando** o Mestre roda `mestre quest run`, **então** o comando volta em menos de 1s com `status: "running"`.

## Depende de

- [convencoes](convencoes.md)
- [personagens](../orquestracao/personagens.md) · [bases-e-fichas](../orquestracao/bases-e-fichas.md) · [conexoes](../orquestracao/conexoes.md)
- [missoes](../orquestracao/missoes.md) · [teto-do-workspace](../classes/teto-do-workspace.md)
