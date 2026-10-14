# ADR 0025: Padrões seguem os provedores

> **Assunto:** por que o que uma classe deixa sem dizer vale o que os CLIs dos provedores fazem num terminal comum (a maioria decide), e por que as regras fixas de segurança não seguem esse princípio · **Componente:** Motor · **Atualizado:** 2026-10-14

## Status

Aceita, 2026-10-14, por decisão do Dono. Complementa o [ADR 0024](0024-cadeia-de-aprovacao-por-risco.md) (o `default` ausente vale `ask`, e o pedido se decide perto de quem pediu).

## Contexto

- Uma classe escrita só com o essencial herdava padrões fechados: sem escrita, sem internet, sem commit, sem envio e subagentes em `medio`. Quem chega do Claude Code, do Codex ou do Antigravity espera que o agente faça, sem perguntar, o que faria no terminal; cada negação inesperada vira pedido ou retrabalho.
- Pesquisa do Dono nos documentos dos fornecedores (2026-10-14): o Claude Code (a partir da 2.1.283) começa no modo automático, que libera o que um classificador acha seguro; o Codex roda sem perguntar os comandos dentro do sandbox, que escreve no workspace e, por padrão, sem rede; o Antigravity pergunta. Claude Code e Antigravity têm rede por padrão.
- O Mestre tem o que os provedores não têm: a [classificação de risco](../classes/classificacao-de-risco.md), determinística, que dá o equivalente verificável do modo automático.

## Opções consideradas

1. **Padrões fechados** (como antes). Prós: o mínimo de surpresa de segurança. Contras: a classe mínima não trabalha; tudo vira pedido.
2. **Padrões de um provedor só.** Prós: simples. Contras: o mesmo personagem muda de comportamento conforme o provedor da ficha.
3. **Padrões da maioria dos três provedores, com o risco do motor no lugar do classificador.** Prós: o agente faz o que faria no terminal; o que é arriscado continua pedindo. Contras: mais aberto que antes; o Dono que quer fechar precisa escrever o campo.

## Decisão

Opção 3. O campo ausente numa classe vale o comportamento da maioria dos provedores num terminal comum; as regras fixas e o teto continuam valendo por cima.

| Campo ausente | Antes | Agora | Claude Code · Codex · Antigravity |
|---|---|---|---|
| `commands.default` / `tools.default` sem regra que cubra | pedido | `baixo` roda direto; `medio` e acima, pedido | automático · sem perguntar no sandbox · pergunta |
| `files.write` | nada | o worktree (`**`); fora dele, pedido | edita na pasta · escreve no workspace · pergunta |
| `network.internet` | sem internet | qualquer domínio | rede · sem rede · rede |
| subagentes nativos | pedido `medio` | `baixo`: rodam direto | parte do agente, sem pedido próprio |
| `git.commit` | negado | pedido; `baixo` no próprio branch e dentro de `files.write`, logo roda direto | — |
| `git.request_push` | negado | pedido (o envio continua do motor) | — |

- Onde ficam: [formato](../classes/formato-das-permissoes.md#o-arquivo), [pipeline](../classes/permissoes.md#pipeline-de-decisão), [reforço nativo](../classes/reforco-nativo.md), [teto](../classes/teto-do-workspace.md#onde-o-teto-age).
- **Não são padrões:** as regras fixas de segurança (refs compartilhadas do espelho, credenciais, cofre, pastas do Mestre e arquivos de outros personagens, elevação, novos agentes) são [invariantes arquiteturais](../arquitetura/precedencia.md#invariantes-arquiteturais) e não seguem este princípio, nem quando um provedor faria diferente.
- O padrão do sistema não é concessão da classe: não conta ao conferir a classe contra o teto, e o teto o corta ao montar as regras efetivas.
- **Modo degradado:** o `baixo` por padrão não roda sem o motor; o que pediria é negado, como antes ([modo degradado](../classes/permissoes.md#modo-degradado)).
- **Marco:** M3 (código em `internal/classes`).

## Consequências

- **Positivas:** a classe mínima trabalha como o agente no terminal; os pedidos que sobram são os que valem uma decisão; o comportamento é o mesmo nos três provedores.
- **Negativas:**
  - Mais aberto que antes: uma classe antiga sem `files.write`, sem `network` ou sem `git.commit` passa a escrever, acessar a rede e commitar. Quem quer fechar escreve o campo (`write: []`, `internet: false`, `commit: false`) ou restringe no teto.
  - O `baixo` que roda direto depende da tabela de risco estar certa; a tabela erra para cima ([sinais de risco](../classes/classificacao-de-risco.md#sinais-de-risco)), e o Dono pode subir um padrão.
  - No Codex, a internet ligada por padrão tira a barreira nativa de rede da classe mínima.
