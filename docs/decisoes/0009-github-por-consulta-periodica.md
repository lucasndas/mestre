# ADR 0009: Eventos do GitHub por consulta periódica

> **Assunto:** por que o Mestre consulta a API em vez de receber webhooks · **Componente:** Core · **Atualizado:** 2026-10-12

## Status

Aceita, 2026-10-12.

## Contexto

- O Grão-Mestre precisa saber de comentários, revisões, resultados de CI e mudanças em issues e PRs, para repassar às mesas.
- O GitHub entrega eventos por **webhook**, mas isso exige uma URL pública que receba conexões.
- O Mestre não expõe nenhuma porta do Core na internet. Os Apps são criados com o webhook inativo ([ADR 0006](0006-app-por-classe-criado-pelo-navegador.md)).
- **Regras da API do GitHub:**
  - Uma requisição condicional que recebe `304` **não gasta a cota principal**.
  - Cada instalação de App tem 5.000 requisições por hora.

## Opções consideradas

1. **Webhook com porta pública no Core.**
   - Prós: eventos na hora, sem gastar cota.
   - Contras:
     - Exige expor o Core na internet, com domínio e certificado público.
     - Viola a regra de nenhuma porta exposta.
2. **Webhook por um repassador externo** (ex.: túnel ou serviço de relay).
   - Prós: eventos quase na hora, sem abrir porta.
   - Contras:
     - Depende de serviço de terceiros, que passa a ver os eventos do repositório.
     - Mais um componente para cair, e mais um segredo.
3. **Consulta periódica com requisições condicionais e intervalo adaptativo.**
   - Prós:
     - Nenhuma porta, nenhum terceiro.
     - Quando nada muda, o custo é quase zero.
     - Funciona igual na rede local e fora dela.
   - Contras:
     - Atraso de segundos a minutos.
     - Mais código para detectar o que mudou.

## Decisão

- **Consulta periódica à API**, com requisições condicionais (`etag` com `If-None-Match`, ou `last-modified` com `If-Modified-Since`).
- **Intervalo adaptativo:** mais frequente com PRs abertos e CI rodando, mais espaçado quando tudo está parado.
- **Destino do que é novo:** vira evento na caixa de entrada do Grão-Mestre ([esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md)).
- **Onde estão os detalhes:** recursos consultados, intervalos padrão e deduplicação estão em [eventos do GitHub](../github/eventos.md).

## Consequências

- **Positivas:**
  - Nada é exposto à internet.
  - Não há dependência de terceiros.
  - Os testes usam um GitHub falso, que responde 200 ou 304 de forma determinística com relógio falso ([dublês](../qualidade/dubles.md)).
- **Negativas:**
  - **Atraso:** um comentário de revisão ou o fim do CI chegam com atraso, que depende do intervalo. Isso é aceitável para o ritmo de agentes.
  - **Mais requisições:** há eventos que exigem consultar vários endpoints (PR, revisões, comentários, checks). O motor precisa comparar estados para descobrir o que é novo.
  - **Requisições estáveis:** os parâmetros e a ordenação precisam ser sempre os mesmos para aproveitar o `304`.
  - **Limites secundários:** mesmo com `304`, a consulta pode contar para eles (no máximo 100 requisições simultâneas, pontos por minuto). O motor respeita `retry-after` e `x-ratelimit-reset`.
- **A confirmar:**
  - Se respostas `304` contam para os limites secundários.
  - Se `hook_attributes` pode ser omitido do manifesto ou precisa ir com `active: false`.

## Fontes

- Requisições condicionais e boas práticas: https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api
- Limites de requisição: https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
- Webhook inativo no manifesto: https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest
