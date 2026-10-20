# AGENTS.md — regras para agentes que trabalham no Mestre

Este arquivo é curto de propósito. O detalhe está em [`docs/`](docs/INDEX.md).

## Como ler a documentação

1. Comece em [`docs/INDEX.md`](docs/INDEX.md).
2. Desça pelos `INDEX.md` das pastas até achar o documento do assunto.
3. Abra só esse documento; siga links apenas se precisar de mais detalhe.
4. Nunca carregue uma pasta inteira de `docs/`.

Termos como Guilda, Cidadela, Torre, Mesa, Ficha, Classe e Grimório estão no [glossário](docs/produto/glossario.md).

## Testes

Ferramentas e versões para desenvolver: [docs/desenvolvimento/ambiente.md](docs/desenvolvimento/ambiente.md).

Política completa em [docs/qualidade/testes.md](docs/qualidade/testes.md); não a repita aqui. Comandos: `go run ./tools/testrun fast` (antes de cada commit), `go run ./tools/testrun affected` (durante o trabalho) e `go run ./tools/testrun full` (antes do merge). Sem `sleep` em teste; Telegram e GitHub são sempre falsos, em todas as suítes ([dublês](docs/qualidade/dubles.md)).

## Documentação

- **Fonte única:** cada assunto tem um documento dono. Em outro lugar, faça link; não copie.
- Antes de criar um documento, procure no índice. Se o assunto existe, atualize aquele.
- A mesma alteração que muda um comportamento atualiza o documento correspondente.
- Proibido criar relatórios ou resumos (`SUMMARY.md`, `PROGRESS.md`, `CHANGES.md`, `IMPLEMENTATION_NOTES.md`…). Histórico fica no git.
- Todo documento tem o cabeçalho de assunto, componente e data, fica listado num `INDEX.md` e tem até ~150 linhas. A suíte rápida verifica ([verificação](docs/qualidade/verificacao-de-docs.md)).
- Em documento de funcionalidade, "Depende de" lista só o que precisa existir antes (mesmo marco ou anterior, sem ciclos); "ver também" vai no corpo ([ordem de implementação](docs/desenvolvimento/ordem-de-implementacao.md)).
- Documentação em português; código, identificadores e commits em inglês.

## Código, commits e branches

Convenções em [docs/desenvolvimento/convencoes.md](docs/desenvolvimento/convencoes.md): layout do código, Conventional Commits, branches e PRs. PRs são unidades coerentes, não um por comando ou achado, e entram na `main` em trens, com um CI para vários: [fluxo de PRs](docs/desenvolvimento/fluxo-de-prs.md). A ordem de desenvolvimento (marcos) está em [docs/desenvolvimento/ordem-de-implementacao.md](docs/desenvolvimento/ordem-de-implementacao.md).

## Quando uma tarefa está pronta

A definição de pronto, com a auditoria do marco, está em [docs/desenvolvimento/convencoes.md](docs/desenvolvimento/convencoes.md#definição-de-pronto); não a repita aqui. Em resumo: critérios de aceite com teste, suíte rápida (e a completa antes do merge) passando, documentação afetada atualizada com a verificação de docs passando, nada fora do [escopo do MVP](docs/produto/escopo-mvp.md) e, para um marco, a auditoria feita e datada na [tabela de marcos](docs/desenvolvimento/ordem-de-implementacao.md#marcos).
