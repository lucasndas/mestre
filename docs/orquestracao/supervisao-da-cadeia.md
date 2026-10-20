# Supervisão da cadeia

> **Assunto:** freios e visibilidade das decisões tomadas perto do trabalho: o resumo não bloqueante das decisões das mesas ao Grão-Mestre, a pausa de autonomia por mesa as métricas de fila por nível com alerta e o aviso de mesa que gira sem entregar · **Componente:** Motor · **Atualizado:** 2026-10-20

## Objetivo

Com os Mestres de mesa decidindo o `medio` da própria mesa ([cadeia de aprovação](cadeia-de-aprovacao.md)), o Grão-Mestre, um por workspace, deixa de ser o gargalo de muitas mesas, agentes e máquinas e vira supervisor: vê depois o que as mesas decidiram, pode frear uma mesa, e todos veem onde os pedidos se acumulam ([ADR 0024](../decisoes/0024-cadeia-de-aprovacao-por-risco.md), revisão de 2026-10-14).

## Comportamento

### Resumo das decisões das mesas

- A cada 30 min (padrão, `cadeia.resumo_mesas` no [arquivo de política](../aprovacoes/arquivo-de-politica.md)), se houve decisão, o motor coloca na caixa de entrada do Grão-Mestre um item `system` com as decisões dos Mestres de mesa no período, por mesa: o pedido montado pelo motor, o risco, a resposta, o motivo e as regras temporárias criadas.
- É **não bloqueante**: não pede resposta, não espera o Grão-Mestre e não muda prazo nenhum; uma decisão já executada não se desfaz por ele.
- Com ele, o Grão-Mestre pode revogar uma regra temporária ([como acaba](regras-temporarias-de-mesa.md#como-acaba)) ou pausar a autonomia da mesa. O resumo do Dono traz, à parte, a amostra das decisões dos Mestres de mesa ([amostra](../aprovacoes/resumo-e-sugestoes.md#amostra-para-revisão)).

### Pausa de autonomia

- **Por mesa:** suspende o `medio` do Mestre da mesa, também o das regras temporárias `medio` dele ([reconferência](regras-temporarias-de-mesa.md#lugar-na-precedência)). Os pedidos `medio` da mesa passam a nascer no Grão-Mestre e os abertos no Mestre da mesa sobem na hora, com o mesmo id; o `baixo` continua com ele.
- **Quem pausa:** o Grão-Mestre (`mestre approval autonomy pause`, [CLI do Grão-Mestre](../cli/grao-mestre.md#mestre-approval)) e um Aprovador ou acima, com um toque: o botão **Pausar autonomia** no aviso de fila e no resumo do Telegram ([bot](../telegram/bot.md), M5) e no painel ([administração](../painel/administracao.md), M6), sem PIN (só restringe).
- **Quem retoma:** a pausa do Grão-Mestre, ele mesmo ou um Aprovador ou acima; a de uma pessoa, só um Aprovador ou acima.
- Não tem prazo: vale até retomar ou até a mesa ser encerrada. Aparece no `mestre table status` e no canvas, e cada pausa e retomada vai à auditoria, com quem e o motivo.
- No M4 a pausa entra pelos tratadores do motor `approval autonomy pause|resume` (só o Grão-Mestre do workspace chama; um Mestre da mesa recebe `level_denied`) e pela interface `Actor`, com um Aprovador como dublê nos testes; a definição do comando na CLI e o botão chegam no M5. O botão **Pausar autonomia** vem no aviso de fila que vai às pessoas (`chain.QueuePeople`, implementado pelo canal do Telegram, `internal/telegram/stallchan.go`): quando o Mestre de uma mesa acumula fila, além do item `system` ao Grão-Mestre, cada Aprovador ou acima do workspace recebe o mesmo aviso com o botão, que chama `Supervisor.PauseAutonomy` em nome de quem tocou (nível e workspace conferidos de novo no toque, motivo "a fila de aprovações da mesa acumulou" na auditoria, sem PIN). O resumo periódico do Grão-Mestre é um item da caixa de entrada, sem botão; o botão **Pausar autonomia** de cada mesa da amostra vem no resumo que o motor manda às pessoas ([resumo](../aprovacoes/resumo-e-sugestoes.md#amostra-para-revisão)); o painel (M6) terá o botão ao lado das métricas.
- **Como vale:** a pausa é uma linha por mesa no banco e o motor a lê a cada decisão, a cada pedido e a cada uso de uma regra temporária `medio` do Mestre da mesa; por isso nenhuma regra é suspensa nem apagada, e a retomada devolve tudo na hora. Os pedidos abertos sobem pelo mesmo caminho da subida (mesmo id). O `mestre table status` mostra a pausa e a fila da mesa.

### Métricas de fila

- **Por aprovador:** cada Mestre da mesa, o Grão-Mestre e as pessoas (uma fila por nível mínimo); o superior agente que não é Mestre também tem fila própria por mesa. Para cada um: pedidos abertos no nível agora, a idade do mais antigo e o tempo médio de espera nas últimas 24 h, calculados do histórico de níveis dos pedidos ([fluxo](../aprovacoes/fluxo.md)) pelo relógio injetável.
- Vão na mensagem de retomada e no resumo do Grão-Mestre, no `mestre table status` de cada mesa e no painel (M6). O nível de um pedido, e quando entrou e saiu dele, vem da auditoria da subida; a fila do Mestre da mesa é uma por mesa, e a espera média conta as passagens que terminaram nas últimas 24 h.
- **Alerta:** um nível acumula quando tem mais de `cadeia.alerta_fila.abertos` pedidos abertos (padrão 10) ou o mais antigo passa de `cadeia.alerta_fila.fator` vezes o prazo do nível (padrão 2). O aviso vai a quem está acima: de um Mestre da mesa, ao Grão-Mestre; do Grão-Mestre, aos Administradores e ao Dono; das pessoas, ao Dono. Um aviso por vez, até o nível voltar abaixo dos dois limites.
- O tempo que cada personagem passa ocioso, ocupado ou aguardando já se mede pelo histórico de estados ([estados](personagens.md#estados)); as métricas de fila são o lado das aprovações, e um painel de produtividade pode juntar as duas.

### Entrega útil e mesa que gira

- **Entrega útil** é trabalho que pousou, não trabalho em andamento: um commit novo na branch da mesa, o PR aberto ou atualizado, ou uma etapa de missão concluída. O motor guarda, por mesa, o instante da última (`última entrega útil`) e a mostra no `mestre table status` ([CLI](../cli/grao-mestre.md#mestre-table)). Ele a marca quando o PR é aberto ou atualizado e quando uma etapa de missão termina com o resultado aceito, e vê o commit novo na branch a cada rodada da supervisão (o instante é o da rodada que o viu; a primeira ponta vista só serve de referência, e o commit de uma Torre conta quando chega ao espelho da Cidadela pela sincronização da mesa).
- **Aviso** (desde o M5): a mesa `working` há mais de 3 h (padrão; `SupervisorOptions.StallAfter`, ainda sem chave no arquivo de política) sem entrega útil, e não pausada nem aguardando [cota](../plataforma/limites-de-uso.md) ou a [dependência de outra mesa](dependencia-entre-mesas.md), gera um item `system` ao Grão-Mestre e uma mensagem a quem tem nível de Aprovador ou acima, com o botão **Pausar mesa** ([bot](../telegram/bot.md), M5; painel no M6). Um aviso por mesa, até a próxima entrega útil; a contagem também recomeça quando a mesa volta a `working`. A mesa retida por [dependência](dependencia-entre-mesas.md) (`Held`) e a pausada não entram.
- **Pausar mesa** (desde o M5; painel, M6) liga a [pausa](mesas.md#pausa) da mesa, sem PIN (só restringe), como o botão **Pausar autonomia** da fila; quem pausa e o motivo vão à auditoria (`supervision_audit`, ação `pause_table`, e `paused_by`/`paused_reason` da mesa). Não confundir com a pausa de autonomia, que só muda quem decide o `medio`.

## Regras e erros

- Pausa pedida por quem não tem o nível, ou numa mesa encerrada: recusada, e nada muda.
- Retomar uma pausa que não existe: sai com sucesso, sem efeito.
- A amostra das decisões dos Mestres de mesa para o resumo do Dono (M5) é sorteada por uma fonte aleatória injetada sobre as decisões que o motor guarda (`DecisionSource`); o M4 entrega a fonte e o sorteio, e o M5, o resumo.
- Limites de alerta fora do intervalo (`abertos` de 1 a 1000, `fator` de 1 a 10): o arquivo de política é inválido e vale a versão anterior.

## Critérios de aceite

- **CA-SUPCAD-01** · *rápida* — **Dado** duas decisões do Mestre da mesa `42-login` e uma do `43-busca` no relógio falso, **quando** passam 30 min, **então** o Grão-Mestre recebe um item `system` com as três, por mesa, que não abre espera nem prazo; **dado** um período sem decisões, **então** nada é entregue.
- **CA-SUPCAD-02** · *rápida* — **Dado** a mesa `42-login` com um pedido `medio` aberto no Mestre da mesa, **quando** o motor recebe a pausa de autonomia da mesa (dublê), **então** o pedido sobe ao Grão-Mestre com o mesmo id, o `medio` seguinte da mesa nasce no Grão-Mestre e um `baixo` ainda nasce no Mestre da mesa, e uma regra temporária `medio` dele não libera nada; **quando** a pausa é retomada, **então** o `medio` seguinte e a regra voltam ao Mestre da mesa.
- **CA-SUPCAD-03** · *rápida* — **Dado** o relógio falso e 11 pedidos abertos no Mestre da mesa, **quando** o motor recalcula as filas, **então** o Grão-Mestre recebe um único aviso com a mesa, o total e a idade do mais antigo; **dado** um pedido no Grão-Mestre há mais de 20 min, **então** os Administradores e o Dono recebem o aviso (dublê); **quando** as filas voltam abaixo dos limites e passam de novo, **então** sai um aviso novo.
- **CA-SUPCAD-04** · *rápida* — **Dado** pedidos que passaram pelo Mestre da mesa e pelo Grão-Mestre, **quando** o motor calcula as métricas de fila, **então** saem, por aprovador, os abertos, a idade do mais antigo e o tempo médio de espera nas últimas 24 h.
- **CA-SUPCAD-05** · *rápida* — **Dado** o Grão-Mestre, **quando** roda `mestre approval autonomy pause --table 42-login --reason "muitas falhas"`, **então** a mesa fica em pausa e a auditoria registra quem e o motivo; **dado** um Aprovador, **quando** toca **Pausar autonomia** no aviso de fila, **então** a mesa fica em pausa e um `resume` do Grão-Mestre nela recebe `level_denied`.
- **CA-SUPCAD-06** · *rápida* — **Dado** o relógio falso e a mesa `42-login` em `working` há 3 h sem commit, PR nem etapa concluída, **quando** o motor recalcula, **então** o Grão-Mestre recebe um aviso e um Aprovador recebe a mensagem com o botão **Pausar mesa**, uma única vez; **quando** o Aprovador toca o botão, **então** a mesa fica pausada e a auditoria registra quem e o motivo; **dado** um commit novo depois do aviso, **então** o contador recomeça e um novo aviso só sai depois de mais 3 h sem entrega.

## Depende de

- [Cadeia de aprovação](cadeia-de-aprovacao.md) · [Regras temporárias de mesa](regras-temporarias-de-mesa.md) · [Esperas e caixa de entrada](esperas-e-caixa-de-entrada.md) · [Mesas](mesas.md)
