# Eventos do GitHub

> **Assunto:** consulta periódica, condicional e adaptativa à API do GitHub, transformada em eventos na caixa de entrada · **Componente:** Core · **Atualizado:** 2026-10-12

## Objetivo

Saber o que mudou no GitHub (comentário, revisão, CI, mudança de issue ou PR) sem receber webhooks, porque nenhuma porta fica exposta ([ADR 0009](../decisoes/0009-github-por-consulta-periodica.md)). O que chega de novo vira evento na caixa de entrada do Grão-Mestre, que repassa à mesa certa.

## Comportamento

### Quem consulta

O motor do Core, um laço por workspace, com o token de instalação do App "Grão-Mestre" ([identidades](identidades-e-apps.md)). Nenhum agente consulta a API.

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
- **Eco:** o que o próprio motor criou ou alterou (registrado em `github_objects` com o `updated_at` devolvido pela API, ver [ações por nível](acoes-por-nivel.md)) não vira evento.

### Entrega na caixa de entrada

- Cada evento é normalizado: tipo (`comentario`, `revisao`, `ci`, `issue_alterada`, `pr_alterado`, `pr_mergeado`, `pr_fechado`), workspace, mesa, autor, link e um resumo curto.
- A mesa é descoberta pelas regras de [ações por nível](acoes-por-nivel.md). Sem mesa, o evento vai marcado "sem mesa".
- Os eventos do mesmo ciclo e da mesma mesa viram **uma única mensagem** na caixa de entrada do Grão-Mestre, para não gastar um turno por evento.
- Prioridade e entrega: [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md).
- Revisões feitas por pessoas direto no GitHub também servem para concluir pedidos de aprovação ([fluxo](../aprovacoes/fluxo.md)).

### Orçamento de cota

Com 5 PRs de mesa abertos no modo "ativo", o pior caso (nenhum `304`) é de cerca de (3 + 3 × 5) × 120 = 2.160 requisições por hora, abaixo das 5.000 por hora da instalação.

## Regras e erros

- Erro 403 ou 429: o motor respeita `retry-after` ou `x-ratelimit-reset` e só volta depois disso.
- Cota restante abaixo de 10%: o workspace fica em "parado" até a cota renovar, e o [checklist](../plataforma/checklist.md) registra o alerta. `GET /rate_limit` pode ser usado, porque não gasta cota.
- No máximo 4 requisições simultâneas por workspace, abaixo dos limites secundários do GitHub. Para esses limites, o motor conta toda requisição, inclusive as que voltam `304` (hipótese conservadora; nada depende de medir isso).
- GitHub fora do ar ou erro de rede: o laço tenta de novo no próximo ciclo, com backoff até o intervalo "parado". Nada se perde, porque o cursor só avança depois que o evento é gravado.
- Consultas só leem; nenhuma consulta muda dados no GitHub.

## Critérios de aceite

- **CA-GH-EV-01** · *rápida* — **Dado** um `ETag` guardado para a lista de comentários, **quando** o motor consulta de novo, **então** a requisição leva `If-None-Match` com esse valor, e uma resposta `304` não gera evento.
- **CA-GH-EV-02** · *rápida* — **Dado** um workspace "ativo" sem mudanças, **quando** o relógio falso avança 5 minutos, **então** o intervalo passa a 2 minutos; e uma mudança vista volta o intervalo a 30 s.
- **CA-GH-EV-03** · *rápida* — **Dado** um comentário já processado, **quando** o Core reinicia e a primeira consulta devolve `200` com o mesmo comentário, **então** nenhum evento novo é criado.
- **CA-GH-EV-04** · *rápida* — **Dado** um comentário criado pelo próprio motor, **quando** ele aparece na consulta, **então** não vira evento.
- **CA-GH-EV-05** · *rápida* — **Dado** uma revisão e o fim do CI do PR da mesa A no mesmo ciclo, **quando** o motor entrega, **então** a caixa de entrada do Grão-Mestre recebe uma única mensagem citando a mesa A e os dois eventos.
- **CA-GH-EV-06** · *rápida* — **Dado** uma resposta 429 com `retry-after: 60`, **quando** o motor processa, **então** a próxima consulta daquele workspace só acontece após 60 s no relógio falso.
- **CA-GH-EV-07** · *completa* — **Dado** o GitHub falso com um PR de mesa aberto e CI em andamento, **quando** o CI termina com falha, **então** em até um ciclo o Grão-Mestre recebe o evento `ci` com o link do check.

## Depende de

- [Identidades e Apps](identidades-e-apps.md)
- [Ações por nível](acoes-por-nivel.md)
- [Esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md)
- [Persistência](../arquitetura/persistencia.md)

## Fontes

- https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api
- https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
