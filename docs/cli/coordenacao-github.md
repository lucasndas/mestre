# CLI: issues e PRs da mesa

> **Assunto:** sintaxe de `issue` e `pr`, as operações no GitHub restritas ao épico da mesa · **Componente:** CLI · **Atualizado:** 2026-10-12

## Objetivo

Referência de sintaxe dos comandos com que a mesa trabalha no GitHub sem ter credencial: o agente pede pela CLI e o motor executa com o App de quem pediu, conferindo se a ação cabe na mesa e na classe. O que cada nível pode fazer está em [acoes-por-nivel](../github/acoes-por-nivel.md); o App usado em cada ação, em [identidades-e-apps](../github/identidades-e-apps.md). Saída e códigos seguem [convencoes](convencoes.md).

## Comportamento

**Quem pode usar:** Mestre da mesa (sempre) e classes autorizadas pelo `permissions.yaml` ([permissoes](../classes/permissoes.md)); o Grão-Mestre também, sem a restrição do épico. Todo número de issue fora da árvore do épico da mesa → 3 `outside_epic` (dica: pedir ao Grão-Mestre). Leitura vale para o repositório todo.

### `mestre issue`
```
mestre issue create --title "<título>" [--body "<texto>" | --body-file <arq>] [--parent <n>] [--label <rótulo>]…
mestre issue update <n> [--title "<…>"] [--body "<…>" | --body-file <arq>] [--add-label <r>]… [--remove-label <r>]…
                        [--state open|closed] [--reason completed|not_planned]
mestre issue comment <n> ("<texto>" | --body-file <arq>)
mestre issue list [<n>…] [--all] [--state open|closed|all] [--comments]
```
- `create`: cria a issue como sub-issue de `--parent` (padrão: o épico da mesa). O pai precisa estar na árvore do épico. `--json` → `{number, id, url, parent}`.
- `update`: título, descrição (inclusive o checklist em Markdown), rótulos e estado.
- `comment`: comenta, publicado com o App de quem pediu.
- `list`: sem números, a árvore do épico da mesa (número, título, estado, pai); `--all`, issues do repositório inteiro; com números, mostra essas issues por completo (`--comments` inclui os comentários). `--json` → `{issues: [{number, title, state, parent, labels, url, body?, comments?}]}`.

### `mestre pr`
```
mestre pr open [--title "<…>"] [--body "<…>" | --body-file <arq>] [--draft]
mestre pr update [--title "<…>"] [--body "<…>" | --body-file <arq>] [--force] [--ready]
mestre pr status
mestre pr comment ("<texto>" | --body-file <arq>)
mestre pr reply <id-comentário> ("<texto>" | --body-file <arq>)
```
- `open`: o motor sincroniza os worktrees da mesa com o espelho, envia a branch da mesa ao GitHub e abre o PR da mesa contra a branch principal, ligado ao épico e às issues (`Closes #<n>`). Título padrão: o do épico. `--json` → `{number, url, head_sha}`. PR já aberto → 7 `pr_exists` (use `update`).
- `update`: envia de novo a branch e atualiza título e descrição. `--force` faz push forçado **só** na branch da mesa e **só** a pedido do Mestre da mesa (outros → 3 `level_denied`). `--ready` tira do rascunho.
- `status`: estado do PR, CI por verificação, revisões (quem, decisão), se pode fazer merge e o motivo quando não pode, comentários sem resposta. `--json` → `{number, state, draft, head_sha, checks: [{name, state}], reviews: [{by, state}], mergeable, blocked_by, unresolved_comments}`.
- `comment`: comenta na conversa do PR; `reply`: responde a um comentário de revisão (o id vem de `pr status --json`).
- Sem PR da mesa (`status`, `comment`, `reply`, `update`) → 4 `no_pr`.

## Regras e erros

| Situação | Exit | `error.code` | O que o motor explica |
|---|---|---|---|
| branch altera `.github/workflows/` e o App não tem a permissão "Workflows" | 3 | `workflow_permission` | o envio foi recusado; o Grão-Mestre pergunta aos usuários se autoriza ([identidades-e-apps](../github/identidades-e-apps.md)) |
| regra do repositório exige commits assinados | 3 | `signature_required` | commits do MVP não são assinados ([0014](../decisoes/0014-commits-sem-assinatura-no-mvp.md)); o Grão-Mestre avisa os usuários |
| proteção ou ruleset recusou o envio | 3 | `protected` | qual regra recusou; nada é contornado |
| branch da mesa em conflito com a principal | 7 | `conflict` | atualizar a branch dentro da mesa ([acoes-por-nivel](../github/acoes-por-nivel.md)) |
| push forçado pedido na branch principal | 3 | `force_main_denied` | push forçado só na branch da mesa |
| GitHub fora ou cota esgotada | 5 | `github_unavailable` | quando tentar de novo (a resposta do GitHub diz) |

- Nenhum comando recebe ou mostra token; nenhum agente faz `git push` para o GitHub.
- Nenhum comando de mesa revisa, aprova ou faz merge: isso é `mestre gh` ([grao-mestre](grao-mestre.md)).

## Critérios de aceite

- **CA-CLI-GH-01** · *rápida* — **Dado** a mesa do épico #42 e a issue #50 de outra mesa, **quando** o Mestre da mesa roda `mestre issue comment 50 "x"`, **então** sai com 3 `outside_epic` e nada é publicado.
- **CA-CLI-GH-02** · *rápida* — **Dado** um personagem cuja classe não libera `pr`, **quando** roda `mestre pr open`, **então** sai com 3 `class_denied`.
- **CA-CLI-GH-03** · *completa* — **Dado** um GitHub falso, **quando** o Mestre da mesa roda `mestre pr open`, **então** a branch enviada é a do espelho do Core, o PR sai com o App "Mestre" e a descrição contém `Closes #42` (MVP-5).
- **CA-CLI-GH-04** · *rápida* — **Dado** um QA autorizado a usar `pr`, **quando** roda `mestre pr update --force`, **então** sai com 3 `level_denied`.
- **CA-CLI-GH-05** · *completa* — **Dado** um GitHub falso que recusa o envio por falta da permissão "Workflows", **quando** roda `mestre pr update`, **então** sai com 3 `workflow_permission` e o Grão-Mestre recebe o evento na caixa de entrada.
- **CA-CLI-GH-06** · *rápida* — **Dado** `mestre issue create --title "Testes" --parent 42`, **quando** roda na mesa do épico #42, **então** a sub-issue é criada sob a #42 com o App da classe de quem pediu.

## Depende de

- [convencoes](convencoes.md)
- [acoes-por-nivel](../github/acoes-por-nivel.md) · [identidades-e-apps](../github/identidades-e-apps.md)
- [mesas](../orquestracao/mesas.md) (espelho e sincronização) · [missoes](../orquestracao/missoes.md)
- [permissoes](../classes/permissoes.md)
