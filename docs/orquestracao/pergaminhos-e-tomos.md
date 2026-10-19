# Pergaminhos e tomos

> **Assunto:** notas em Markdown no canvas (pergaminhos), grupos de notas (tomos), operações, concorrência e o pergaminho da missão · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Pergaminhos são notas em Markdown que usuários e agentes conectados leem e escrevem, atualizadas ao vivo no canvas. Tomos agrupam pergaminhos. Servem para combinar planos, registrar resultados de missões e compartilhar contexto sem conversa.

## Comportamento

### Arquivo e índice

- Pergaminhos existem **só dentro das mesas** (a visão do workspace não tem pergaminhos). Cada um é um arquivo Markdown na Cidadela, com o clone principal como fonte: `.mestre/mesas/<mesa>/pergaminhos/<id>.md`. O `<id>` é imutável (`p0001`, `p0002`… por mesa), para o arquivo não mudar de nome quando o título muda.
- O banco guarda só o índice e as conexões: id, mesa, nome, se o nome é fixo, tomo, revisão, tamanho, autor e data da última alteração ([persistência](../arquitetura/persistencia.md)).
- Nos worktrees onde há personagens (Cidadela e Torres), o motor mantém cópias somente leitura atualizadas a cada mudança ([materialização](../sessao-zero/mesa.md)). Alterações vão sempre pela CLI ou pelo canvas; editar a cópia direto não tem efeito e é sobrescrito. **As cópias são uma conveniência de leitura, não uma fronteira de confidencialidade dentro da mesa:** o worktree é um só para todos os personagens da mesa, então quem lê arquivos lê também as cópias dos pergaminhos que a CLI não lhe mostraria. Quem faz valer a visibilidade é a CLI (e o canvas); o que precisa ficar fora do alcance de um personagem da mesa não vai num pergaminho.
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
| `stack` | coloca o pergaminho num tomo (cria o tomo se o nome é novo); um pergaminho está em no máximo um tomo; quem empilha num tomo que já existe precisa vê-lo (um pergaminho que entra num tomo passa a ser visto por todos os conectados a ele, então empilhar num tomo que o personagem não vê abriria um canal) |
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

O resumo do `done`, o resumo de encerramento e o motivo de uma devolução são texto de agentes: de uma linha, seguem o rótulo (`**Resumo:** …`); de mais, vêm abaixo do rótulo com `> ` em cada linha, seguidos de uma linha em branco, e nenhuma linha deles abre uma seção do motor. Isso vale para o que o motor acrescenta; um personagem que escreve no pergaminho escreve o que quiser, e a marca não o protege disso. Também recebe o resumo de encerramento de cada personagem da missão ([encerramento seguro](personagens.md#encerramento-seguro)), as devoluções com o motivo e as provas por critério. Missão com etapa que devolve ganha também (desde o M5) o pergaminho "Revisão: <nome>" ([prova e devolução](prova-e-devolucao.md#pergaminho-de-revisão)). Agentes conectados podem ler e escrever nele como em qualquer pergaminho.

Pergaminhos e tomos são salvos em [grimórios](grimorios.md).

### No código

`internal/orchestration/scrolls`. Escolhas que o código fecha:
- **Guardado:** `notes`, `tomes` e `note_counters` (o último número de cada mesa: um id nunca se repete). O `<id>` (`p0001`) repete de mesa em mesa; a identidade nas [conexões](conexoes.md) é a chave `<workspace>/<mesa>/<id>`, e a do tomo `<workspace>/<mesa>/t0001`. O arquivo fica em `<clone principal>/.mestre/mesas/<mesa>/pergaminhos/<id>.md`, escrito pelo `safefile` antes do índice (se o índice falha, o arquivo volta ao que era, ou é removido numa criação; a réplica nunca recebe uma linha que cita um arquivo que ainda não existe).
- **Uma de cada vez:** um cadeado ordena as mudanças. A revisão sobe 1 a cada mudança de **conteúdo** (`write`, `edit`); empilhar não a muda. Cada mudança é dita ao canvas por `OnChange`, na ordem em que foi aplicada.
- **Nome:** `--name` repetido na mesa é 7 `name_taken` (o sufixo " (2)" é só para o nome tirado da primeira linha). Pergaminho e tomo se resolvem pelo nome ou pelo id da mesa; o pergaminho vence um tomo de mesmo nome.
- **Quem:** o personagem só opera o que `CanSeeScroll` deixa; inexistente e invisível dão o mesmo erro, com o nome pedido. `delete` por personagem sai com 9 e nada se apaga até o fluxo de aprovação do M5 (`Approvals`); Administrador ou acima apaga. O Grão-Mestre não tem mesa: `ErrNoTable`, antes de qualquer arquivo.
- **Cópias:** `Targets` (os worktrees das máquinas que têm personagens da mesa) e `Copies` (`LocalCopies` escreve pelo `safefile`, modo só leitura; uma Torre chega com o protocolo, no [M5, antes da etapa 1](../desenvolvimento/ordem-de-implementacao.md#regras-de-execução-da-ordem)). Na montagem real, `CharacterTargets` (uma Torre que não responde é pulada, sem parar as outras) e `LocalCopies` estão ligados, e `Sync` roda quando um personagem nasce, antes de ele iniciar. As cópias saem com o cadeado do serviço solto. Uma cópia que falha não impede a mudança; `Sync` refaz as da mesa. `Cites` diz à [replicação](../guilda/replicacao.md#arquivos-e-espelhos) o arquivo que cada linha de `notes` cita.
- **O motor escreve:** `Ensure` e `AppendSection` (só acrescenta) servem ao pergaminho da missão; `Record` escreve o resumo de encerramento em "Missão: <nome>" ou, sem missão, em "Histórico da mesa".

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
- **CA-PERG-08** · *rápida* — **Dado** o resumo de encerramento de um personagem com uma linha `## Encerramento · …` e uma linha `**Resumo:** …` falsas, **quando** o motor acrescenta a seção ao pergaminho, **então** o resumo vem citado com `> ` abaixo do rótulo e o pergaminho tem uma só seção desse encerramento.

## Depende de

- [Conexões](conexoes.md)
- [Persistência](../arquitetura/persistencia.md)
- [Sessão zero da mesa](../sessao-zero/mesa.md)
