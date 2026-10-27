# Conexões

> **Assunto:** conexões automáticas e manuais entre personagens e pergaminhos, e o que cada agente enxerga · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Conexões dizem **quem fala com quem e quem lê o quê**. Um agente só conversa com personagens conectados a ele e só lê e escreve pergaminhos visíveis para ele. As linhas no canvas ([canvas](../painel/canvas.md)) são essas conexões.

## Comportamento

### Tipos

| Tipo | Efeito |
|---|---|
| personagem ↔ personagem | os dois podem usar `ask`, `check` e `wait` um com o outro |
| pergaminho ↔ personagem | o personagem pode ler e escrever o pergaminho |
| pergaminho ↔ pergaminho | quem enxerga um enxerga o outro (ver visibilidade) |

Conexões não têm direção. Ligar a um **tomo** equivale a ligar a todos os pergaminhos dele ([tomos](pergaminhos-e-tomos.md#tomos)).

### Automáticas

O motor cria (e recria, se faltarem) sem ninguém pedir:

| Quando | Conexão | Motivo registrado |
|---|---|---|
| Mestre da mesa nasce | Mestre da mesa ↔ Grão-Mestre | `mestre` |
| Qualquer personagem da mesa nasce | personagem ↔ Mestre da mesa | `mesa` |
| Personagem nasce por `recruit`/`spawn` | personagem ↔ quem o criou | `criador` |
| Etapa de missão recebe personagem | personagem da etapa ↔ personagem da etapa seguinte; e ↔ da anterior quando a etapa pode devolver para ela | `missao:<id>` |
| Missão é criada | cada personagem de etapa ↔ pergaminhos da missão (desde o M5, também o de revisão, [prova e devolução](prova-e-devolucao.md#pergaminho-de-revisão), ligado aos personagens da etapa que devolve e das etapas para onde ela devolve) | `missao:<id>` |

Conexões de missão são removidas quando a missão termina, se nenhum outro motivo as mantiver. Uma conexão pode ter vários motivos; ela existe enquanto houver ao menos um.

### Manuais

- **Mestre da mesa e Grão-Mestre:** `mestre connect "A" "B"`, o Mestre da mesa dentro da própria mesa e o Grão-Mestre em qualquer mesa da campanha (sintaxe em [coordenação](../cli/coordenacao.md)). Motivo `manual`.
- **Usuários:** arrastando no canvas (nível Administrador ou acima). Também podem remover conexões manuais; conexões automáticas não podem ser removidas enquanto o motivo existir (o canvas mostra o motivo).
- Agentes comuns não criam conexões: `connect` chamado por eles é recusado pelo nível do comando ([convenções da CLI](../cli/convencoes.md)).

### Visibilidade

Para um agente X, o motor calcula na hora de cada comando:
- **Personagens visíveis:** os conectados diretamente a X. O Grão-Mestre vê todos os Mestres de mesa da campanha; o Mestre da mesa vê o Grão-Mestre e toda a party.
- **Pergaminhos visíveis:** os conectados a X e, a partir deles, os alcançáveis por cadeias de conexões pergaminho ↔ pergaminho.
- Nada atravessa mesas, exceto a conexão Mestre da mesa ↔ Grão-Mestre.

`mestre list` mostra exatamente isso: o próprio nome de X, os personagens visíveis (com máquina, classe e estado/motivo de espera) e os pergaminhos visíveis com suas conexões. Os comandos que miram um personagem não visível falham com "<Nome> não está conectado a você" (exit 3, `not_connected`; [convenções da CLI](../cli/convencoes.md)). O encaminhamento entre máquinas da Guilda é transparente ([convenções da CLI](../cli/convencoes.md)).

### No código

`internal/orchestration/connections`. Escolhas que o código fecha:
- **Guardado:** `connections` (um par por linha, sem direção, na ordem em que o banco o guarda) e `connection_reasons` (os motivos); a ponta é `character`, `scroll` ou `tome`. Um tomo é guardado como tomo e expandido na hora de calcular a visibilidade, então os pergaminhos que entram depois já contam. Pergaminhos e tomos chegaram com a etapa 7: o `scrolls.Service` implementa `Scrolls`, e a ponta é a chave `<workspace>/<mesa>/<id>` ([pergaminhos](pergaminhos-e-tomos.md#no-código)); o pergaminho é conectado a quem o criou (motivo `manual`, por esse personagem) e suas conexões somem com ele.
- **Automáticas:** `Born` (chamado pelo `Service` dos personagens ao registrar), `Ended` (ao passar a `stopped`) e `Inherit` (a troca de ficha leva as conexões do antigo ao novo, motivos incluídos). `criador` só liga a um personagem vivo da mesma mesa que não seja o Grão-Mestre. `Reconcile` refaz as que faltam, na ordem Grão-Mestre, Mestres, resto; um Mestre novo liga toda a party da mesa. A missão chama `LinkQuest` (os pares são dela) e `EndQuest`.
- **`connect`:** o nome vale na mesa (sem diferenciar maiúsculas): personagem primeiro, depois pergaminho ou tomo. Item que só existe em outra mesa: 2 `different_tables`; o Grão-Mestre como ponta: 3 `level_denied`; Mestre da mesa com `--table` de outra mesa: 3 `level_denied`; Grão-Mestre sem `--table`: 2 `table_required`; de algo consigo mesmo: 2 `same_item`; nome inexistente: 4 `not_visible`. Repetido responde `already_connected: true`.
- **Remover:** `Disconnect` (Senhor da Guilda e Administrador) tira só o motivo `manual`; se há outro, a conexão fica. Sem `manual`, a recusa é 3 `automatic_connection` com os motivos; desconectar um par sem conexão é 4 `not_found`.
- **Alvo por nome:** `Target` e `Guard` (os comandos `ask`, `check` e `wait` da etapa 7 resolvem o nome por `Target` eles mesmos, porque aceitam vários nomes e `--batch`; o `Guard` fica para os de um nome só): nome que existe na mesa do chamador e não é visível: 3 `not_connected`; de outra mesa ou inexistente: 4 `not_visible`. `CanSeeScroll` é o que `note` consulta.

## Regras e erros

- `connect` entre itens de mesas diferentes, ou envolvendo o Grão-Mestre (exceto pelo motor), é recusado.
- `connect` de algo consigo mesmo, ou de alvo inexistente: erro com o nome não encontrado.
- `connect` repetido não duplica: só acrescenta o motivo `manual`.
- Quando um personagem é encerrado, todas as conexões dele são removidas; pergaminhos continuam.

## Critérios de aceite

- **CA-CONX-01** · *rápida* — **Dado** uma mesa com Mestre, Dev e QA recrutados pelo Mestre, **quando** o Dev chama `mestre list`, **então** vê o Mestre e não vê o QA nem o Grão-Mestre.
- **CA-CONX-02** · *rápida* — **Dado** uma missão Dev → Revisor com devolução ao Dev, **quando** a missão é criada, **então** existem Dev ↔ Revisor e as conexões com o pergaminho da missão; **quando** a missão termina, **então** elas somem se não houver outro motivo.
- **CA-CONX-03** · *rápida* — **Dado** pergaminhos P1 ↔ P2 e P1 ligado ao Dev, **quando** o Dev lê P2, **então** consegue; um pergaminho sem caminho até o Dev é recusado.
- **CA-CONX-04** · *rápida* — **Dado** duas mesas, **quando** o Mestre da mesa A tenta `connect` com um personagem da mesa B, **então** é recusado.
- **CA-CONX-05** · *rápida* — **Dado** a conexão automática Mestre da mesa ↔ Grão-Mestre, **quando** um usuário tenta removê-la no canvas, **então** é recusado com o motivo mostrado.
- **CA-CONX-06** · *rápida* — **Dado** um Dev de classe comum, **quando** chama `mestre connect "QA" "Revisor"`, **então** é recusado pelo nível; **quando** o Grão-Mestre faz o mesmo na mesa deles, **então** a conexão é criada com motivo `manual`.
- **CA-CONX-07** · *rápida* — **Dado** um Dev não conectado ao QA, **quando** chama `mestre ask "QA" "oi"`, **então** sai com exit 3 `not_connected` e a mensagem "QA não está conectado a você".

## Depende de

- [Personagens](personagens.md)
- [Convenções da CLI](../cli/convencoes.md)
