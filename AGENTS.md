# AGENTS.md — regras para agentes que trabalham no Mestre

Este arquivo é curto de propósito. O detalhe está em [`docs/`](docs/INDEX.md).

## Como ler a documentação

1. Comece em [`docs/INDEX.md`](docs/INDEX.md).
2. Desça pelos `INDEX.md` das pastas até achar o documento do assunto.
3. Abra só esse documento; siga links apenas se precisar de mais detalhe.
4. Nunca carregue uma pasta inteira de `docs/`.

Termos como Mesa, Ficha, Classe e Grimório estão no [glossário](docs/produto/glossario.md).

## Testes

Política completa em [docs/qualidade/testes.md](docs/qualidade/testes.md). O essencial:

- Três suítes com orçamento: **rápida** (≤ 30 s, antes de cada commit), **completa** (≤ 3 min, antes do merge), **pesada** (noturna e sob demanda).
- Proibido `sleep` em teste: use o relógio injetável. Provedores, Telegram, GitHub e rede são falsos nas suítes rápida e completa ([dublês](docs/qualidade/dubles.md)).
- Cada comportamento é testado uma vez, no nível mais baixo possível. Durante o trabalho, rode só os testes afetados.
- Teste instável vai para quarentena; nunca "com retry".

## Documentação

- **Fonte única:** cada assunto tem um documento dono. Em outro lugar, faça link; não copie.
- Antes de criar um documento, procure no índice. Se o assunto existe, atualize aquele.
- A mesma alteração que muda um comportamento atualiza o documento correspondente.
- Proibido criar relatórios ou resumos (`SUMMARY.md`, `PROGRESS.md`, `CHANGES.md`, `IMPLEMENTATION_NOTES.md`…). Histórico fica no git.
- Todo documento tem o cabeçalho de assunto, componente e data, fica listado num `INDEX.md` e tem até ~150 linhas. A suíte rápida verifica ([verificação](docs/qualidade/verificacao-de-docs.md)).
- Documentação em português; código, identificadores e commits em inglês.

## Código, commits e branches

Convenções em [docs/desenvolvimento/convencoes.md](docs/desenvolvimento/convencoes.md): layout do código, Conventional Commits, branches e PRs.

## Quando uma tarefa está pronta

- Os critérios de aceite do documento da funcionalidade estão cobertos por testes na suíte indicada.
- A suíte rápida passa dentro do orçamento, e a completa também antes do merge.
- A documentação afetada foi atualizada e a verificação de docs passa.
- Nada fora do escopo do MVP ([escopo](docs/produto/escopo-mvp.md)) foi acrescentado.
