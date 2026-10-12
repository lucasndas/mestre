# Conexões

> **Assunto:** conexões automáticas e manuais entre personagens e pergaminhos, e o que cada agente enxerga · **Componente:** Core · **Atualizado:** 2026-10-12

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
| Missão é criada | cada personagem de etapa ↔ pergaminhos da missão | `missao:<id>` |

Conexões de missão são removidas quando a missão termina, se nenhum outro motivo as mantiver. Uma conexão pode ter vários motivos; ela existe enquanto houver ao menos um.

### Manuais

- **Mestre da mesa:** `mestre connect "A" "B"` dentro da própria mesa (sintaxe em [coordenação](../cli/coordenacao.md)). Motivo `manual`.
- **Usuários:** arrastando no canvas (nível Administrador ou acima). Também podem remover conexões manuais; conexões automáticas não podem ser removidas enquanto o motivo existir (o canvas mostra o motivo).
- Agentes comuns não criam conexões, salvo se a classe permitir `connect` ([permissões](../classes/permissoes.md)).

### Visibilidade

Para um agente X, o motor calcula na hora de cada comando:
- **Personagens visíveis:** os conectados diretamente a X. O Grão-Mestre vê todos os Mestres de mesa do workspace; o Mestre da mesa vê o Grão-Mestre e toda a party.
- **Pergaminhos visíveis:** os conectados a X e, a partir deles, os alcançáveis por cadeias de conexões pergaminho ↔ pergaminho.
- Nada atravessa mesas, exceto a conexão Mestre da mesa ↔ Grão-Mestre.

`mestre list` mostra exatamente isso: o próprio nome de X, os personagens visíveis (com nó, classe e estado/motivo de espera) e os pergaminhos visíveis com suas conexões. Os comandos que miram um alvo não visível falham com "<Nome> não está conectado a você". O encaminhamento entre nós é transparente ([convenções da CLI](../cli/convencoes.md)).

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

## Depende de

- [Personagens](personagens.md)
- [Pergaminhos e tomos](pergaminhos-e-tomos.md)
- [Missões](missoes.md)
- [Convenções da CLI](../cli/convencoes.md)
