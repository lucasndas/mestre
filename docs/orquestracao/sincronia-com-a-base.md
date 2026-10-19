# Sincronia com a base

> **Assunto:** a etapa `sync` da mesa: integrar a branch principal na branch da mesa e provar que o resultado verifica, com o conflito como tarefa do Dev e a verificação verde como chegada · **Componente:** Motor · **Atualizado:** 2026-10-18

## Objetivo

Impedir que o PR da mesa trave por conflito de rotina ou chegue ao merge com a base andada e a `main` quebrada. "Sem conflito de texto" não é "funciona": uma mescla limpa pode não compilar. Por isso o conflito é uma **tarefa do Dev**, nunca um bloqueio para o usuário, e a linha de chegada é a **verificação verde**, não a ausência de marcadores de conflito. Neste documento, "a base" é a branch principal do repositório, não a [Base](../produto/glossario.md) de um terminal.

## Comportamento

### Quando a etapa nasce

O motor cria a etapa `sync` quando:

1. o [gate de frescor](../github/acoes-por-nivel.md#merge) falha: a base integrada num worktree efêmero não passa na verificação;
2. o GitHub avisa que a base andou depois do PR aberto (evento `pr_alterado` com o PR atrás da base ou em conflito, [eventos](../github/eventos.md));
3. o Mestre da mesa pede `mestre pr open` e a base andou desde a abertura da mesa: o envio é recusado com 7 `conflict` e a dica de que a etapa `sync` foi criada ([CLI](../cli/coordenacao-github.md)).

Nenhum dos três depende de a mesa ter conflito de texto: a base ter andado basta para o motor conferir.

### A etapa

- É uma missão de uma etapa da mesa ([missões](missoes.md)), de risco `baixo`, entregue ao **Dev da mesa**: o personagem que fez a última etapa que mudou código; sem ele, o Mestre da mesa escolhe. Uma mesa aberta em `in_review` volta a `working` ([estados](mesas.md#estados)).
- O motor monta a instrução: integrar a base no worktree da mesa (merge, nunca push forçado), resolver cada conflito mantendo a intenção dos dois lados, rodar a verificação do workspace (os [comandos confirmados](../sessao-zero/workspace.md#comandos-de-verificação), descobertos na análise do projeto) e só então dar `done`.
- **Conflito é tarefa de build.** O Dev resolve; só chega a pessoa o que é decisão de intenção (dois lados do pedido que não cabem juntos), pelo caminho normal da pergunta ao Mestre da mesa e ao Grão-Mestre.
- **Quem empurra** é o motor, pelo App da classe do Dev, no `mestre pr update` seguinte; o agente não faz `git push`.

### A chegada é a verificação

O `done` da `sync` não encerra a etapa sozinho. O motor roda a verificação num worktree efêmero da mesa, sob a trava por repositório, e confere o resultado:

- **verde:** a etapa conclui; o Dev (ou o Mestre da mesa) faz o `pr update` e a mesa volta a `in_review`. Um pedido de aprovação do PR já aberto sobre o hash antigo fica `invalidado` ([invalidação](../aprovacoes/fluxo.md#invalidação)) e nasce de novo no hash novo;
- **vermelha:** a etapa volta ao Dev com a saída da verificação como motivo, contando uma volta (`max_returns` da missão, [devolução](missoes.md)). Passou do limite: a mesa fica pausada e o Grão-Mestre recebe o motivo. A mesma falha repetida (mesma impressão, causa `verify`) pausa antes do limite ([prova e devolução](prova-e-devolucao.md#devolução-com-causa-e-mesma-falha)).

## Regras e erros

- A etapa `sync` não amplia permissões: o Dev usa as da classe dele ([permissões](../classes/permissoes.md)). Quem não pode rodar a verificação a devolve ao Mestre da mesa.
- Sem comando de verificação confirmado, a `sync` não consegue provar a chegada: o motor a devolve ao Mestre da mesa, que leva ao Grão-Mestre a pergunta de quais comandos usar.
- Só uma etapa `sync` aberta por mesa; um evento de base andada com uma já em andamento é ignorado.
- Base que anda de novo durante a `sync` repete o gate no fim, antes de concluir.

## Critérios de aceite

- **CA-SYNC-01** · *rápida* — **Dado** uma mesa com PR aberto e a base com um commit que conflita com a branch dela, **quando** o motor recebe o evento de base andada, **então** cria uma etapa `sync` de risco `baixo` para o Dev da mesa, a mesa vai a `working` e nenhum aviso de decisão chega ao usuário.
- **CA-SYNC-02** · *rápida* — **Dado** uma `sync` em que o Dev resolveu os marcadores mas a verificação falha, **quando** ele dá `done`, **então** o motor devolve a etapa ao Dev com a saída da verificação e conta uma volta; **dado** a verificação verde, **então** a etapa conclui e o `pr update` leva a mesa a `in_review`.
- **CA-SYNC-03** · *rápida* — **Dado** `max_returns: 3` e uma `sync` cuja verificação falha 4 vezes, **quando** a quarta falha chega, **então** a mesa fica pausada e o Grão-Mestre recebe o motivo.

## Depende de

- [Ações por nível](../github/acoes-por-nivel.md)
- [Mesas](mesas.md)
- [Missões](missoes.md)
