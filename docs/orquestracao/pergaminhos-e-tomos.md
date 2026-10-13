# Pergaminhos e tomos

> **Assunto:** notas em Markdown no canvas (pergaminhos), grupos de notas (tomos), operações, concorrência e o pergaminho da missão · **Componente:** Motor · **Atualizado:** 2026-10-13

## Objetivo

Pergaminhos são notas em Markdown que usuários e agentes conectados leem e escrevem, atualizadas ao vivo no canvas. Tomos agrupam pergaminhos. Servem para combinar planos, registrar resultados de missões e compartilhar contexto sem conversa.

## Comportamento

### Arquivo e índice

- Pergaminhos existem **só dentro das mesas** (a visão do workspace não tem pergaminhos). Cada um é um arquivo Markdown na Cidadela, com o clone principal como fonte: `.mestre/mesas/<mesa>/pergaminhos/<id>.md`. O `<id>` é imutável (`p0001`, `p0002`… por mesa), para o arquivo não mudar de nome quando o título muda.
- O banco guarda só o índice e as conexões: id, mesa, nome, se o nome é fixo, tomo, revisão, tamanho, autor e data da última alteração ([persistência](../arquitetura/persistencia.md)).
- Nos worktrees onde há personagens (Cidadela e Torres), o motor mantém cópias somente leitura atualizadas a cada mudança ([materialização](../sessao-zero/mesa.md)). Alterações vão sempre pela CLI ou pelo canvas; editar a cópia direto não tem efeito e é sobrescrito.
- Se `.mestre/` é versionado no projeto ou não é decidido na [sessão zero do workspace](../sessao-zero/workspace.md).

### Nome

- Com `--name`, o nome é fixo. Sem isso, o nome é a primeira linha do conteúdo (sem `#` e espaços iniciais, até 80 caracteres; vazia → "Sem título") e acompanha as mudanças dela.
- Único na mesa, sem diferenciar maiúsculas; em colisão, o motor acrescenta " (2)", " (3)"… Os comandos aceitam o nome ou o id.

### Operações

Sintaxe de `mestre note create|read|write|edit|stack|delete` em [comunicação](../cli/comunicacao.md). Significado:

| Operação | Efeito |
|---|---|
| `create` | cria o pergaminho (conteúdo opcional) já conectado a quem criou |
| `read` | devolve o conteúdo atual e a revisão |
| `write` | substitui o conteúdo inteiro; com revisão esperada informada, falha se outra pessoa alterou antes |
| `edit` | troca um trecho exato por outro; falha se o trecho aparece 0 ou mais de 1 vez |
| `stack` | coloca o pergaminho num tomo (cria o tomo se o nome é novo); um pergaminho está em no máximo um tomo |
| `delete` | apaga; só com pedido explícito de um usuário autorizado ([convenções da CLI](../cli/convencoes.md)) |

- **Quem:** agentes só operam pergaminhos visíveis para eles ([conexões](conexoes.md#visibilidade)). Usuários Administrador ou acima editam no canvas; os demais níveis só leem.
- **Concorrência:** a Cidadela aplica as mudanças uma de cada vez, na ordem de chegada; cada uma aumenta a revisão em 1 e é enviada ao canvas na hora ([canvas](../painel/canvas.md)).
- **Tamanho máximo:** 256 KiB por pergaminho (padrão configurável).

### Tomos

- Grupo de pergaminhos com nome único na mesa. Conectar um tomo a um personagem equivale a conectá-lo a todos os pergaminhos do tomo, inclusive os que entrarem depois.
- Retirar um pergaminho do tomo: pelo canvas, ou empilhando-o em outro tomo. Tomo vazio é removido.

### Pergaminho da missão

Criado pelo motor ao criar uma [missão](missoes.md), com nome fixo "Missão: <nome>" e conectado a todos os personagens das etapas. O motor acrescenta (nunca reescreve) seções:

```markdown
## Rodada 1 · 2. dev — Dev Claude 1 — 2026-10-12 14:03
**Resumo:** <texto do done>
**Arquivos:** M src/login.go, A src/login_test.go
**Não commitados:** (nenhum)
```

Também recebe o resumo de encerramento de cada personagem da missão ([encerramento seguro](personagens.md#encerramento-seguro)) e as devoluções com o motivo. Agentes conectados podem ler e escrever nele como em qualquer pergaminho.

Pergaminhos e tomos são salvos em [grimórios](grimorios.md).

## Regras e erros

- Conteúdo acima do tamanho máximo: a operação falha dizendo o tamanho atual e o limite.
- Pergaminho não visível ou inexistente: erro com o nome pedido.
- `stack` de pergaminho de outra mesa: recusado.
- Criar pergaminho fora de uma mesa (ex.: pelo Grão-Mestre, que não tem mesa): recusado.
- Conteúdo de pergaminho é texto; o motor nunca o executa.

## Critérios de aceite

- **CA-PERG-01** · *rápida* — **Dado** um pergaminho sem `--name` com primeira linha "# Plano do login", **quando** é criado, **então** o nome é "Plano do login"; **quando** a primeira linha muda, **então** o nome muda e o arquivo continua `p0001.md`.
- **CA-PERG-02** · *rápida* — **Dado** um pergaminho com o trecho "TODO" duas vezes, **quando** um agente faz `edit` de "TODO", **então** falha por ambiguidade e nada muda.
- **CA-PERG-03** · *rápida* — **Dado** a revisão 4, **quando** dois `write` chegam esperando a revisão 4, **então** o primeiro vence (revisão 5) e o segundo falha.
- **CA-PERG-04** · *rápida* — **Dado** um tomo conectado ao QA, **quando** um pergaminho novo é empilhado nele, **então** o QA passa a lê-lo.
- **CA-PERG-05** · *completa* — **Dado** um personagem numa máquina, **quando** o pergaminho muda na Cidadela, **então** a cópia no worktree da máquina é atualizada.
- **CA-PERG-06** · *pesada* — **Dado** o painel aberto, **quando** um agente escreve num pergaminho da mesa, **então** o canvas mostra a mudança sem recarregar (MVP-11).
- **CA-PERG-07** · *rápida* — **Dado** o Grão-Mestre, **quando** chama `mestre note create`, **então** é recusado porque pergaminhos só existem em mesas, e nenhum arquivo é criado fora de `.mestre/mesas/`.

## Depende de

- [Conexões](conexoes.md)
- [Persistência](../arquitetura/persistencia.md)
- [Sessão zero da mesa](../sessao-zero/mesa.md)
