# Sincronia com a base

> **Assunto:** a etapa `sync` da mesa: integrar a branch principal na branch da mesa e provar que o resultado verifica, com o conflito como tarefa do Dev e a verificação verde como chegada · **Componente:** Motor · **Atualizado:** 2026-10-20

## Objetivo

Impedir que o PR da mesa trave por conflito de rotina ou chegue ao merge com a base andada e a `main` quebrada. "Sem conflito de texto" não é "funciona": uma mescla limpa pode não compilar. Por isso o conflito é uma **tarefa do Dev**, nunca um bloqueio para o usuário, e a linha de chegada é a **verificação verde**, não a ausência de marcadores de conflito. Neste documento, "a base" é a branch principal do repositório, não a [Base](../produto/glossario.md) de um terminal.

## Comportamento

### Quando a etapa nasce

O motor cria a etapa `sync` quando:

1. o [gate de frescor](../github/acoes-por-nivel.md#merge) falha: a base integrada num worktree efêmero não passa na verificação;
2. a base andou depois do PR aberto: o motor deduz isso a cada `fetch` do espelho (a ponta da base mudou desde a abertura da mesa ou desde a última `sync`; na prática, a ponta de `origin/<base>` no espelho não está na branch da mesa, sem estado guardado para esquecer) e, como segundo sinal, do `pr_alterado` com o PR atrás da base ou em conflito ([eventos](../github/eventos.md)): o `basesync.EventSink` chama a mesma checagem, que lê o espelho e ignora o evento se a branch tem a base; nenhum evento novo do GitHub é preciso;
3. o Mestre da mesa pede `mestre pr open` e a base andou desde a abertura da mesa: o envio é recusado com 7 `conflict` e a dica de que a etapa `sync` foi criada ([CLI](../cli/coordenacao-github.md)).

Nenhum dos três depende de a mesa ter conflito de texto: a base ter andado basta para o motor conferir.

### A etapa

- É uma missão de uma etapa da mesa ([missões](missoes.md)), de risco `baixo`, entregue ao **Dev da mesa**: o personagem que fez a última etapa que mudou código; sem ele, o Mestre da mesa escolhe. Uma mesa aberta em `in_review` volta a `working` ([estados](mesas.md#estados)).
- O motor monta a instrução: integrar a base no worktree da mesa (merge, nunca push forçado), resolver cada conflito mantendo a intenção dos dois lados, rodar a verificação do workspace (os [comandos confirmados](../sessao-zero/workspace.md#comandos-de-verificação), descobertos na análise do projeto) e só então dar `done`.
- **Conflito é tarefa de build.** O Dev resolve; só chega a pessoa o que é decisão de intenção (dois lados do pedido que não cabem juntos), pelo caminho normal da pergunta ao Mestre da mesa e ao Grão-Mestre.
- **Quem empurra** é o motor, pelo App da classe do Dev, no `mestre pr update` seguinte; o agente não faz `git push`.

### A chegada é a verificação

O `done` da `sync` não encerra a etapa sozinho, qualquer que seja a classe do Dev (a `sync` sempre passa pela verificação do motor, como a de uma classe com `verify: true`, [prova e devolução](prova-e-devolucao.md#o-done-que-a-classe-exige)). O motor roda a verificação num worktree efêmero da mesa, sob a trava por repositório, e confere o resultado:

- **antes de rodar:** um `done` com a branch ainda sem a ponta da base é recusado (7 `conflict`) sem rodar a verificação;
- **verde:** a etapa conclui; o Dev (ou o Mestre da mesa) faz o `pr update` e a mesa volta a `in_review`. Um pedido de aprovação do PR já aberto sobre o hash antigo fica `invalidado` ([invalidação](../aprovacoes/fluxo.md#invalidação)) e nasce de novo no hash novo;
- **vermelha:** a etapa volta ao Dev com a saída da verificação como motivo, contando uma volta (`max_returns` da missão, [devolução](missoes.md)). Passou do limite: a missão da `sync` fica `paused` com "limite de voltas" e o Mestre da mesa recebe o evento `return_limit`, como em [missões](missoes.md#devolução-com-limite); a mesa continua `working` e só chega ao Grão-Mestre o que o Mestre da mesa escalar. A mesma falha repetida (mesma impressão, causa `verify`) pausa antes do limite ([prova e devolução](prova-e-devolucao.md#devolução-com-causa-e-mesma-falha)).

## Regras e erros

- A etapa `sync` não amplia permissões: o Dev usa as da classe dele ([permissões](../classes/permissoes.md)). Quem não pode rodar a verificação a devolve ao Mestre da mesa.
- Sem comando de verificação confirmado, a `sync` não consegue provar a chegada: o motor pausa a missão ("sem comando de verificação", nunca "concluída sem verificação") e a devolve ao Mestre da mesa, que leva ao Grão-Mestre a pergunta de quais comandos usar e retoma a missão com `mestre quest resume`; o Dev dá o `done` de novo (o Mestre é avisado uma vez).
- Sem Dev da mesa vivo (ninguém vivo fez uma etapa que mudou código) a missão não nasce: o Mestre da mesa recebe um item `system` (uma vez por ponta da base) pedindo que escolha quem integra e crie a missão `sync`; `mestre pr open` segue recusado. Mesa pausada, ou que não está em `working` ou `in_review`, não recebe a etapa, e `mestre pr open` continua recusado: na dúvida o envio não sai.
- O risco `baixo` da etapa quer dizer que ela não tem ponto de aprovação; a missão não tem campo de risco.
- Só uma etapa `sync` aberta por mesa (missão `sync`; uma nova depois da concluída roda de novo a mesma missão, e a cancelada deixa o nome, então a seguinte é `sync-2`); um evento de base andada com uma já em andamento é ignorado.
- Base que anda de novo durante a `sync` repete o gate no fim, antes de concluir: com a verificação verde mas a branch sem a ponta nova, a etapa segue com o Dev (sem contar volta), avisado de que deve integrar a base outra vez e dar o `done`. Se o gate não puder ser conferido, a etapa não conclui (o Dev e o Mestre da mesa são avisados de que a verificação não pôde rodar).

## Critérios de aceite

- **CA-SYNC-01** · *rápida* — **Dado** uma mesa com PR aberto e a base com um commit que conflita com a branch dela, **quando** o motor recebe o evento de base andada, **então** cria uma etapa `sync` de risco `baixo` para o Dev da mesa, a mesa vai a `working` e nenhum aviso de decisão chega ao usuário.
- **CA-SYNC-02** · *rápida* — **Dado** uma `sync` em que o Dev resolveu os marcadores mas a verificação falha, **quando** ele dá `done`, **então** o motor devolve a etapa ao Dev com a saída da verificação e conta uma volta; **dado** a verificação verde, **então** a etapa conclui e o `pr update` leva a mesa a `in_review`.
- **CA-SYNC-03** · *rápida* — **Dado** `max_returns: 3` e uma `sync` cuja verificação falha 4 vezes com saídas diferentes em cada uma, **quando** a quarta falha chega, **então** as três primeiras voltam ao Dev, a missão fica `paused` com "limite de voltas", o Mestre da mesa recebe o `return_limit` e a mesa segue `working`; **dado** a mesma saída `src/a.go:7: x` três vezes com `same_failure: 3`, **então** a missão pausa na terceira com "mesma falha repetida".
- **CA-SYNC-04** · *rápida* — **Dado** a mesa aberta e a ponta da base mudada por um `fetch` do espelho (sem nenhum evento do GitHub), **quando** o Mestre da mesa pede `mestre pr open`, **então** sai com 7 `conflict` e a `sync` é criada; **dado** uma `sync` já aberta, **então** um novo sinal de base andada é ignorado e nenhuma segunda etapa nasce.
- **CA-SYNC-05** · *rápida* — **Dado** uma `sync` em andamento e a base andando de novo, **quando** o Dev dá `done` e a verificação passa, **então** o gate roda outra vez antes de concluir; **dado** um workspace sem comando de verificação confirmado, **então** a `sync` volta ao Mestre da mesa com a pergunta dos comandos.

## Depende de

- [Ações por nível](../github/acoes-por-nivel.md)
- [Mesas](mesas.md)
- [Missões](missoes.md)
- [Sessão zero do workspace](../sessao-zero/workspace.md)
