# Eventos do GitHub

> **Assunto:** consulta periódica, condicional e adaptativa à API do GitHub, transformada em eventos na caixa de entrada · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Saber o que mudou no GitHub (comentário, revisão, CI, mudança de issue ou PR) sem receber webhooks, porque nenhuma porta fica exposta ([ADR 0009](../decisoes/0009-github-por-consulta-periodica.md)). O que chega de novo vira evento na caixa de entrada do Grão-Mestre, que repassa à mesa certa.

## Comportamento

### Quem consulta

O motor da Cidadela, um laço por workspace, com o token de instalação do App "Grão-Mestre" ([identidades](identidades-e-apps.md)). Nenhum agente consulta a API. Só a Cidadela consulta, com o aluguel da trava válido ([aluguel](../guilda/trava-no-github.md#aluguel)); com o [GitHub fora](../guilda/trava-no-github.md#github-fora), os laços param e retomam pelos cursores; depois de uma troca de Cidadela, a nova reconcilia pelos marcadores de operação ([regras da troca](../guilda/cidadela-e-torres.md)) e retoma os laços pelo estado do banco replicado (cursores e ETags; [replicação](../guilda/replicacao.md)).

### Intervalo adaptativo (padrão, configurável por workspace)

| Situação do workspace | Intervalo |
|---|---|
| **Ativo:** PR de mesa com CI em andamento, ou mudança vista nos últimos 5 minutos, ou ação do motor no GitHub (envio, merge) nos últimos 5 minutos | 30 s |
| **Atento:** há mesa aberta ou PR de mesa aberto, sem nada do item anterior | 2 min |
| **Parado:** nenhuma mesa aberta | 5 min |

- Qualquer mudança vista leva o workspace para "ativo" na hora.
- Cada intervalo tem variação aleatória de ±10% para os workspaces não consultarem todos juntos.
- Todo o tempo vem do relógio injetável ([dublês](../qualidade/dubles.md)).

### O que é consultado

| O que | Endpoint | Quando |
|---|---|---|
| Issues e PRs alterados | `GET /repos/{o}/{r}/issues?state=all&sort=updated&direction=desc&per_page=100` | todo ciclo |
| Comentários em issues e PRs | `GET /repos/{o}/{r}/issues/comments?sort=updated&direction=desc&per_page=100` | todo ciclo |
| Comentários de revisão no código | `GET /repos/{o}/{r}/pulls/comments?sort=updated&direction=desc&per_page=100` | todo ciclo |
| PRs abertos (cabeça, estado) | `GET /repos/{o}/{r}/pulls?state=open&per_page=100` | se há PR de mesa aberto |
| Revisões de cada PR de mesa aberto | `GET /repos/{o}/{r}/pulls/{n}/reviews?per_page=100` | se há PR de mesa aberto |
| CI da cabeça de cada PR de mesa | `GET /repos/{o}/{r}/commits/{sha}/check-runs?per_page=100` e `GET /repos/{o}/{r}/commits/{sha}/status` | até todos os checks daquele `sha` terminarem |

- As URLs são sempre as mesmas (mesmos parâmetros e ordem), para o GitHub responder `304` sempre que possível.
- Revisões de PR de mesa vêm sempre da consulta por PR, nunca da lista de issues: o motor não depende de uma revisão mudar o `updated_at` do PR.
- Nas listas ordenadas por `updated`, o motor lê só a primeira página. Se todos os itens dela forem mais novos que o cursor, lê a próxima, até alcançar o cursor.

### Requisições condicionais

- O motor guarda o `ETag` de cada URL e manda `If-None-Match` na consulta seguinte.
- Resposta `304` (nada mudou) não gasta a cota principal, desde que a requisição vá com o cabeçalho `Authorization`.
- Resposta `200`: o motor compara cada item com o cursor da URL (o maior `updated_at` já processado) e só trata os mais novos.
- `ETag` e cursor ficam no banco (tabela `github_poll_state`, [persistência](../arquitetura/persistencia.md)), para sobreviver a reinícios.
- Num workspace novo, o cursor começa no momento da criação: o histórico antigo não vira evento.

### Deduplicação

Cada mudança recebe uma chave única, guardada na tabela `github_seen_events` (apagada após 30 dias):

| Tipo | Chave |
|---|---|
| issue ou PR alterado | `issue:<id>:<updated_at>` |
| comentário (novo ou editado) | `comentario:<id>:<updated_at>` |
| revisão | `revisao:<id>:<state>` |
| check run | `check:<id>:<status>:<conclusion>` |
| status combinado | `status:<sha>:<state>` |

- Chave já vista é ignorada. Isso cobre páginas lidas de novo, reinícios e o mesmo fato visto por dois endpoints.
- **Eco:** o que o próprio motor criou ou alterou (registrado em `github_objects` com o `updated_at` devolvido pela API, ver [ações por nível](acoes-por-nivel.md)) não vira evento. Item com o [marcador de operação](acoes-por-nivel.md#marcador-de-operação) da própria Guilda também nunca vira evento externo, mesmo que não esteja em `github_objects` (ex.: criado pela Cidadela antiga logo antes de uma troca); o motor o importa em `github_objects`.

### Entrega na caixa de entrada

- Cada evento é normalizado: tipo (`comentario`, `revisao`, `ci`, `issue_alterada`, `pr_alterado`, `pr_mergeado`, `pr_fechado`), workspace, mesa, autor, link e um resumo curto.
- A mesa é descoberta pelas regras de [ações por nível](acoes-por-nivel.md). Sem mesa, o evento vai marcado "sem mesa".
- Os eventos do mesmo ciclo e da mesma mesa viram **uma única mensagem** na caixa de entrada do Grão-Mestre, para não gastar um turno por evento.
- Prioridade e entrega: [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md).
- `pr_mergeado` do PR de uma mesa confirma o merge: o motor marca a mesa como mesclada e o item ao Grão-Mestre diz isso, para ele encerrar a mesa ([encerramento](../orquestracao/mesas.md#encerramento)). **Antes** de marcar, confere pelo espelho que o `merge_commit_sha` é alcançável da base (`git merge-base --is-ancestor`); se não é (merge desfeito logo depois, ou o espelho ainda sem o commit), a mesa não é marcada e o Grão-Mestre recebe o aviso; o motor repete a checagem a cada busca do espelho até o commit ser alcançável ou até 1 h, e passado o prazo registra o alerta e deixa de repetir. Vale também para o merge feito por uma pessoa no GitHub e para o resultado de `mestre gh merge` ([mesas](../orquestracao/mesas.md#encerramento)). Nenhum "pronto" vai ao usuário antes disso.
- Revisões feitas por pessoas direto no GitHub também servem para concluir pedidos de aprovação, só sob as condições de [review feito direto no GitHub](aprovacao-no-github.md#review-feito-direto-no-github) ([fluxo](../aprovacoes/fluxo.md)).

### Orçamento de cota

Cada ciclo "ativo" custa 3 listas + `pulls?state=open` + 3 chamadas por PR de mesa (revisões, `check-runs`, `status`). Com 5 PRs, o pior caso (nenhum `304`) é (4 + 3 × 5) × 120 = 2.280 requisições por hora **por workspace**. A cota de 5.000 por hora é da **instalação** do App "Grão-Mestre", dividida entre todos os workspaces da conta e a trava (120 por hora, [trava](../guilda/trava-no-github.md)): dois workspaces assim já somam 4.680, e três passam do limite. Por isso, ao bater o limite primário (abaixo), o laço para em todos os workspaces.

## Regras e erros

- Erro 403 ou 429: o motor respeita `retry-after` ou `x-ratelimit-reset` e só volta depois disso.
- Cota restante abaixo de 10%: o workspace fica em "parado" até a cota renovar, e o [checklist](../plataforma/checklist.md) (desde o M7) registra o alerta. `GET /rate_limit` pode ser usado, porque não gasta cota.
- **Escrita também espera.** Com a cota nessa faixa, as ações que mudam dados (comentário, issue, review, merge) não saem: ficam na fila de saída, cada uma com o mesmo uuid e o [marcador de operação](acoes-por-nivel.md#marcador-de-operação), e saem uma vez só quando a cota renova, como no [GitHub fora](../guilda/trava-no-github.md#github-fora). Só segurar a leitura deixaria o motor gastando a cota que sobra em escrita.
- No máximo 4 requisições simultâneas por workspace, abaixo dos limites secundários do GitHub. Para esses limites, o motor conta toda requisição, inclusive as que voltam `304` (hipótese conservadora; nada depende de medir isso).
- GitHub fora do ar ou erro de rede: o laço tenta de novo no próximo ciclo, com backoff até o intervalo "parado". Nada se perde, porque o cursor só avança depois que o evento é gravado.
- **Limite batido (a cota chegou a zero, `403` ou `429` por limite primário):** o motor para os laços da instalação até a renovação e avisa o Dono. Reduzir também o número de mesas em `working` ao mesmo tempo é **pendente**: o campo, a tela, o valor inicial e a espera da mesa nova ainda não têm documento dono em `orquestracao/`. Limite **secundário** (abuso, `retry-after`): a escrita falha com o motivo e nada que muda dados é repetido sozinho ([ações por nível](acoes-por-nivel.md#regras-e-erros)).
- Consultas só leem; nenhuma consulta muda dados no GitHub.

## Critérios de aceite

- **CA-GH-EV-01** · *rápida* — **Dado** um `ETag` guardado para a lista de comentários, **quando** o motor consulta de novo, **então** a requisição leva `If-None-Match` com esse valor, e uma resposta `304` não gera evento.
- **CA-GH-EV-02** · *rápida* — **Dado** um workspace "ativo" sem mudanças, **quando** o relógio falso avança 5 minutos, **então** o intervalo passa a 2 minutos; e uma mudança vista volta o intervalo a 30 s.
- **CA-GH-EV-03** · *rápida* — **Dado** um comentário já processado, **quando** a Cidadela reinicia e a primeira consulta devolve `200` com o mesmo comentário, **então** nenhum evento novo é criado.
- **CA-GH-EV-04** · *rápida* — **Dado** um comentário criado pelo próprio motor, **quando** ele aparece na consulta, **então** não vira evento.
- **CA-GH-EV-05** · *rápida* — **Dado** uma revisão e o fim do CI do PR da mesa A no mesmo ciclo, **quando** o motor entrega, **então** a caixa de entrada do Grão-Mestre recebe uma única mensagem citando a mesa A e os dois eventos.
- **CA-GH-EV-06** · *rápida* — **Dado** uma resposta 429 com `retry-after: 60`, **quando** o motor processa, **então** a próxima consulta daquele workspace só acontece após 60 s no relógio falso.
- **CA-GH-EV-07** · *completa* — **Dado** o GitHub falso com um PR de mesa aberto e CI em andamento, **quando** o CI termina com falha, **então** em até um ciclo o Grão-Mestre recebe o evento `ci` com o link do check.
- **CA-GH-EV-08** · *rápida* — **Dado** um evento `pr_mergeado` cujo `merge_commit_sha` não é alcançável da base no espelho, **quando** o motor o processa, **então** a mesa não é marcada como mesclada e o Grão-Mestre recebe o aviso; **dado** o commit alcançável, **então** a mesa é marcada.
- **CA-GH-EV-09** · *rápida* — **Dado** o GitHub falso com a cota em zero e um comentário pedido nesse momento, **quando** o motor processa, **então** os laços param, o Dono é avisado, o comentário espera na fila de saída com o uuid e, renovada a cota (relógio falso), sai uma vez só.
- **CA-GH-EV-10** · *rápida* — **Dado** a cota restante em 9%, **quando** o motor processa, **então** o workspace passa a "parado", uma ação que muda dados fica na fila de saída e a leitura continua no intervalo "parado"; **dado** um `403` com `retry-after` de limite secundário, **então** a escrita sai com falha e o motivo e não é repetida.
- **CA-GH-EV-11** · *rápida* — **Dado** um `pr_mergeado` com o `merge_commit_sha` ainda ausente do espelho, **quando** o espelho busca e passa a alcançá-lo (relógio falso), **então** a mesa é marcada na busca seguinte sem novo aviso; **dado** que ele nunca chega, **quando** passa 1 h (relógio falso), **então** o motor registra o alerta e não checa mais.

## Depende de

- [Identidades e Apps](identidades-e-apps.md)
- [Ações por nível](acoes-por-nivel.md)
- [Esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md)
- [Persistência](../arquitetura/persistencia.md)
- [Trava no GitHub](../guilda/trava-no-github.md)

## Fontes

- https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api
- https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
