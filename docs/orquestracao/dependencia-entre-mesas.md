# Dependência entre mesas

> **Assunto:** a espera do tipo "épico": a mesa que depende de outra espera, sem ninguém lembrar, até o PR da outra ser mesclado e confirmado na base · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Pedidos encadeados ("o checkout só depois do login") andam sozinhos. Hoje a dependência entre mesas é registrada como dependência entre épicos no GitHub (`mestre gh epic update --depends-on`) e respeitada por julgamento do Grão-Mestre; aqui o **motor** a segura e a solta, como faz com as outras [esperas](esperas-e-caixa-de-entrada.md), e o Grão-Mestre não precisa lembrar de reiniciar a mesa dependente.

## Comportamento

### A espera do tipo "épico"

- **Quem cria:** o motor, quando o Grão-Mestre registra a dependência (`gh epic update <n> --depends-on <m>`) de uma mesa aberta ou ao abrir uma mesa cujo épico já depende de outro com mesa em andamento. Vale também para a resposta "esperar a outra terminar" da [regra de decisão](grao-mestre.md#triagem-e-roteamento-do-pedido).
- **Satisfeita quando** o PR da mesa do épico `<m>` é mesclado **e** o motor confirma o commit na base ([eventos](../github/eventos.md#entrega-na-caixa-de-entrada)). Se o épico `<m>` não tem mesa aberta, a dependência já vale como satisfeita só se o épico está fechado; senão o Grão-Mestre recebe o aviso e decide.
- **Enquanto espera:** a mesa recém-aberta fica `open` com o passo 6 da [abertura](mesas.md#abertura) adiado (o Mestre da mesa já está recrutado, mas só recebe o pedido quando a espera é satisfeita; nenhuma etapa dependente é recrutada), e aparece como "aguardando a mesa #12" no canvas e no `mestre table list`. Dependência registrada numa mesa que já está `working` ou `in_review` vale só para a próxima etapa ou missão que ela iniciar, sem mudar o estado. Uma mesa sem dependência pendente não é afetada.
- **Satisfeita:** o motor registra a espera como `satisfied` e coloca um item `wake` na caixa de entrada do Grão-Mestre ("a mesa #12 foi mesclada; a #15 pode seguir"), que manda a mesa dependente continuar. A mesa dependente integra a base com a etapa `sync` ([sincronia com a base](sincronia-com-a-base.md)) antes do PR.
- **Tempo limite:** sem tempo limite próprio; a mesa aguardando dependência fica fora do aviso das mesas paradas ([supervisão](supervisao-da-cadeia.md#entrega-útil-e-mesa-que-gira)). Mesa cancelada, ou épico `<m>` fechado como "não planejado": a espera é `cancelled` e o Grão-Mestre decide o que fazer com a dependente.

### Sobreposição sem dependência

Quando duas mesas mexem na mesma área e nenhuma depende da outra (resultado do `mestre table overlap`, [CLI do Grão-Mestre](../cli/grao-mestre.md#mestre-table)), a decisão é do Grão-Mestre, com a pergunta ao usuário da regra 6 ([roteamento](grao-mestre.md#triagem-e-roteamento-do-pedido)); o motor não julga. A recomendação de um só caminho é: **o épico de número menor segue e o outro espera**, nunca os dois um atrás do outro. Escolhida a espera, o Grão-Mestre registra a dependência do de número maior no menor (`gh epic update --depends-on`) e daí vale a espera do motor, acima.

### Ciclo

Dependência que fecharia um ciclo entre épicos (#12 depende de #15, #15 de #12) é recusada com o ciclo escrito, e o Grão-Mestre pergunta aos usuários qual vem primeiro. O grafo de esperas entre personagens ([impasse](esperas-e-caixa-de-entrada.md#detecção-de-impasse)) é outro e não muda.

## Regras e erros

- Só o Grão-Mestre cria dependências entre épicos; um Mestre da mesa pede a ele ([mestre da mesa](mestre-da-mesa.md)).
- A espera sobrevive a reinício e a troca de Cidadela: fica no banco com as demais esperas ([persistência e retomada](esperas-e-caixa-de-entrada.md#persistência-e-retomada)).
- PR da mesa `<m>` mesclado mas desfeito antes de o motor confirmar na base: a espera continua `active`.

## Critérios de aceite

- **CA-DEPMESA-01** · *rápida* — **Dado** o épico #15 dependendo do #12, com a mesa #12 em `in_review`, **quando** o Grão-Mestre abre a mesa do #15, **então** o motor registra uma espera de épico, a mesa fica `open` sem recrutar ninguém para a etapa dependente e o `table list` mostra "aguardando a mesa #12".
- **CA-DEPMESA-02** · *rápida* — **Dado** a espera acima, **quando** o PR da #12 é mesclado e o commit está na base, **então** a espera vira `satisfied` e o Grão-Mestre recebe um `wake` citando a #12 e a #15; **dado** o merge sem o commit na base, **então** a espera continua `active`.
- **CA-DEPMESA-03** · *rápida* — **Dado** o épico #12 dependendo do #15, **quando** o Grão-Mestre registra que o #15 depende do #12, **então** a dependência é recusada com o ciclo `#12 → #15 → #12` e nenhuma espera nasce.
- **CA-DEPMESA-04** · *rápida* — **Dado** duas mesas (#12 e #15) alterando `src/login/`, sem dependência entre elas e com a resposta "esperar", **quando** o Grão-Mestre registra a dependência da #15 na #12, **então** a #15 espera e a #12 segue; a #12 mesclada acorda a #15.
- **CA-DEPMESA-05** · *rápida* — **Dado** a espera da #15 pela #12, **quando** a mesa #12 é cancelada, ou o épico #12 é fechado como "não planejado", **então** a espera vira `cancelled` e o Grão-Mestre é avisado; **dado** um Mestre da mesa que tenta `gh epic update --depends-on`, **então** sai com `level_denied`; **dado** a mesa #15 aguardando, **então** ela não gera aviso de mesa parada.

## Depende de

- [Esperas e caixa de entrada](esperas-e-caixa-de-entrada.md)
- [Mesas](mesas.md)
- [Eventos do GitHub](../github/eventos.md)
- [Sincronia com a base](sincronia-com-a-base.md)
