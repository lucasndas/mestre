# CLI: issues e PRs da mesa

> **Assunto:** sintaxe de `issue` e `pr`, as operações no GitHub restritas ao épico da mesa · **Componente:** CLI · **Atualizado:** 2026-10-19

## Objetivo

Referência de sintaxe dos comandos com que a mesa trabalha no GitHub sem ter credencial: o agente pede pela CLI e o motor executa com o App de quem pediu, conferindo se a ação cabe na mesa e na classe. O que cada nível pode fazer está em [acoes-por-nivel](../github/acoes-por-nivel.md); o App usado em cada ação, em [identidades-e-apps](../github/identidades-e-apps.md). Saída e códigos seguem [convencoes](convencoes.md).

## Comportamento

**Quem pode usar:** Mestre da mesa (sempre) e classes autorizadas pelo `permissions.yaml` ([permissoes](../classes/permissoes.md)); o Grão-Mestre também, sem a restrição do épico. Todo número de issue fora da árvore do épico da mesa → 3 `outside_epic` (dica: pedir ao Grão-Mestre). Leitura vale para o repositório todo.

### `mestre issue`
```
mestre issue create --title "<título>" [--body "<texto>" | --body-file <arq>] [--parent <n>] [--label <rótulo>]… [--fingerprint "<texto>"]
mestre issue update <n> [--title "<…>"] [--body "<…>" | --body-file <arq>] [--add-label <r>]… [--remove-label <r>]…
                        [--state open|closed] [--reason completed|not_planned]
mestre issue comment <n> ("<texto>" | --body-file <arq>)
mestre issue list [<n>…] [--all] [--state open|closed|all] [--comments]
```
- `create`: cria a issue como sub-issue de `--parent` (padrão: o épico da mesa; o Grão-Mestre, fora de uma mesa, cria uma issue solta, sem a regra do pai, com o App "Grão-Mestre"). O pai precisa estar na árvore do épico. `--json` → `{number, id, url, parent}`.
- `--fingerprint`: a **impressão** do problema, para não registrar o mesmo duas vezes. O motor normaliza o texto (minúsculas, espaços juntados), guarda o hash com o número da issue nos objetos do GitHub ([persistência](../arquitetura/persistencia.md)) e o põe no corpo (`<!-- mestre:impressao=<hash> -->`); se a issue está aberta ou fechada, ele lê do GitHub na hora, porque uma pessoa pode fechá-la. Com uma issue **aberta** de mesma impressão no repositório, não cria outra: comenta "visto de novo" com o título e a data, com o App de quem pediu, e devolve a existente (`seen_again: true`). Com só uma **fechada**, cria a nova com "Recorrência de #<n>" no início do corpo e o rótulo `recorrencia` (`recurrence_of`). O motor cria os rótulos `candidato` e `recorrencia` se faltarem, como o `epico`. Usado pelo [Vigia](../classes/classes-sugeridas.md#vigia).
- `update`: título, descrição (inclusive o checklist em Markdown), rótulos e estado. Os rótulos `epico` e `mestre:humano` são reservados: pôr ou tirar um deles pela mesa → 3 `reserved_label` (o Grão-Mestre também não tira `mestre:humano`) ([ações por nível](../github/acoes-por-nivel.md#árvore-do-épico)).
- `comment`: comenta, publicado com o App de quem pediu.
- `list`: sem números, a árvore do épico da mesa (número, título, estado, pai); `--all`, issues do repositório inteiro; com números, mostra essas issues por completo (`--comments` inclui os comentários). `--json` → `{issues: [{number, title, state, parent, labels, url, body?, comments?}]}`.

### `mestre pr`
```
mestre pr open [--title "<…>"] [--body "<…>" | --body-file <arq>] [--draft]
mestre pr update [--title "<…>"] [--body "<…>" | --body-file <arq>] [--force] [--ready]
mestre pr update --block <nome> ("<texto>" | --block-file <arq>) --expect-head <sha>
mestre pr note <arquivo>:<linha> ("<texto>" | --body-file <arq>)
mestre pr status
mestre pr comment ("<texto>" | --body-file <arq>)
mestre pr reply <id-comentário> ("<texto>" | --body-file <arq>)
```
- `open`: o motor sincroniza os worktrees da mesa com o espelho, envia a branch da mesa ao GitHub e abre o PR da mesa contra a branch principal, ligado ao épico e às issues (`Closes #<n>`). Título padrão: o do épico. `--json` → `{number, url, head_sha}`. PR já aberto → 7 `pr_exists` (use `update`).
- `update`: envia de novo a branch e atualiza título e descrição. `--force` faz push forçado **só** na branch da mesa e **só** a pedido do Mestre da mesa (outros → 3 `level_denied`). `--ready` tira do rascunho.
- **Chave no diff:** em `open` e `update`, antes de enviar, o motor varre as linhas **adicionadas** do diff da branch (este documento é o dono da lista e das exceções da varredura; o mascarador de logs não as tem). **Formatos:** os do mascarador ([segurança](../arquitetura/seguranca.md#mascaramento-em-logs), regra 2: GitHub, Telegram, Slack, JWT, `Authorization:`, `sk-…`, bloco `-----BEGIN … PRIVATE KEY`), mais `AKIA` seguido de 16 letras ou dígitos e `token`, `secret` ou `password` `= "…"` com 24 caracteres ou mais. **Exceções:** só valem na varredura: leitura de variável de ambiente (`process.env`, `os.environ`, `${VAR}`) e valor com `example` ou `your_`. Achou: recusa o envio com 3 `secret_detected`, citando `arquivo:linha` e o tipo (`src/x.ts:12 github_token`). **Nunca** mostra nem grava o valor, na saída ou no log. Só o motor envia a branch, então este é o ponto único que também cobre escrita por shell sem caminho literal ([permissões](../classes/permissoes.md#regras-e-erros)).
- **Corpo em blocos:** o corpo do PR do épico é feito de blocos `<!-- mestre:bloco=<nome> -->…<!-- /mestre:bloco -->` (`status`, `problema`, `solucao`, `criterios`, `historico`, `antes-depois`, `risco`). `--block` reescreve **só** esse bloco, e só se a cabeça do PR no GitHub é `--expect-head`; senão 7 `head_changed` e o corpo não muda. O bloco tem uma classe dona, a que o criou; outra classe escrever nele é 3 `block_owned`. O Mestre da mesa escreve em qualquer um. Assim mais de uma classe escreve no mesmo PR sem sobrescrever a outra.
- `note`: nota do autor como **comentário de revisão na linha** do diff, na cabeça atual do PR (a explicação fica onde o revisor lê, não em comentário no código). Linha fora do diff → 2 `usage`.
- `status`: estado do PR, CI por verificação, revisões (quem, decisão), se pode fazer merge e o motivo quando não pode, comentários sem resposta. `--json` → `{number, state, draft, head_sha, checks: [{name, state}], reviews: [{by, state}], mergeable, blocked_by, unresolved_comments}`.
- `comment`: comenta na conversa do PR; `reply`: responde a um comentário de revisão (o id vem de `pr status --json`).
- Sem PR da mesa (`status`, `comment`, `reply`, `update`) → 4 `no_pr`.

## Regras e erros

| Situação | Exit | `error.code` | O que o motor explica |
|---|---|---|---|
| branch altera `.github/workflows/` e a classe tem `git.workflows: never` | 3 | `workflow_not_allowed` | o envio foi recusado; o Grão-Mestre pergunta aos usuários se mudam a classe ([workflows](../github/identidades-e-apps.md#workflows)) |
| o GitHub recusou por falta da permissão "Workflows" no App (ex.: permissão nova ainda não aceita na instalação) | 3 | `workflow_permission` | o Grão-Mestre manda o link para aceitar a permissão ([workflows](../github/identidades-e-apps.md#workflows)) |
| branch altera `.github/workflows/` e a classe tem `git.workflows: ask` | 9 | `approval_pending` | o envio espera o "Aprovar" de um usuário ([fluxo](../aprovacoes/fluxo.md)) |
| regra do repositório exige commits assinados | 3 | `signature_required` | os commits do Mestre não são assinados ([0014](../decisoes/0014-commits-sem-assinatura.md)); o Grão-Mestre avisa os usuários |
| proteção ou ruleset recusou o envio | 3 | `protected` | qual regra recusou; nada é contornado |
| o diff adicionado traz uma chave com formato conhecido | 3 | `secret_detected` | `arquivo:linha` e o tipo da chave, sem o valor; nada é enviado |
| `--block` com a cabeça do PR diferente de `--expect-head` | 7 | `head_changed` | a cabeça atual; ler o PR de novo e repetir |
| `--block` num bloco de outra classe | 3 | `block_owned` | qual classe é a dona |
| branch da mesa em conflito com a principal | 7 | `conflict` | atualizar a branch dentro da mesa, na etapa `sync` ([sincronia](../orquestracao/sincronia-com-a-base.md)) |
| push forçado pedido na branch principal | 3 | `force_main_denied` | push forçado só na branch da mesa |
| GitHub fora (`5xx`, rede) ou limite secundário (abuso) | 5 | `github_unavailable` | quando tentar de novo (a resposta do GitHub diz); a ação que muda dados não é repetida sozinha |
| cota primária esgotada ou abaixo de 10% | 0 | `queued: true` | o comando que muda dados espera na fila de saída e sai uma vez na renovação; só as leituras (`issue list`, `pr status`) saem com 5 `github_unavailable` e a hora da renovação ([eventos](../github/eventos.md#regras-e-erros)) |
| aluguel da trava vencido | 0 | `queued: true` | os comandos que mudam dados esperam na fila de saída; só as leituras saem com 5 `github_unavailable` ([GitHub fora](../guilda/trava-no-github.md#github-fora)) |

- Nenhum comando recebe ou mostra token; nenhum agente faz `git push` para o GitHub.
- Nenhum comando de mesa revisa, aprova ou faz merge: isso é `mestre gh` ([grao-mestre](grao-mestre.md)).

## Critérios de aceite

- **CA-CLI-GH-01** · *rápida* — **Dado** a mesa do épico #42 e a issue #50 de outra mesa, **quando** o Mestre da mesa roda `mestre issue comment 50 "x"`, **então** sai com 3 `outside_epic` e nada é publicado.
- **CA-CLI-GH-02** · *rápida* — **Dado** um personagem cuja classe não libera `pr`, **quando** roda `mestre pr open`, **então** sai com 3 `class_denied`.
- **CA-CLI-GH-03** · *completa* — **Dado** um GitHub falso, **quando** o Mestre da mesa roda `mestre pr open`, **então** a branch enviada é a do espelho da Cidadela, o PR sai com o App "Mestre" e a descrição contém `Closes #42` (MVP-5).
- **CA-CLI-GH-04** · *rápida* — **Dado** um QA autorizado a usar `pr`, **quando** roda `mestre pr update --force`, **então** sai com 3 `level_denied`.
- **CA-CLI-GH-05** · *completa* — **Dado** um GitHub falso que recusa o envio por falta da permissão "Workflows", **quando** roda `mestre pr update`, **então** sai com 3 `workflow_permission` e o Grão-Mestre recebe o evento na caixa de entrada.
- **CA-CLI-GH-06** · *rápida* — **Dado** `mestre issue create --title "Testes" --parent 42`, **quando** roda na mesa do épico #42, **então** a sub-issue é criada sob a #42 com o App da classe de quem pediu.
- **CA-CLI-GH-07** · *rápida* — **Dado** um diff com `token = "ghp_…"` em `src/x.ts:12`, **quando** o personagem roda `mestre pr update`, **então** sai com 3 `secret_detected` citando `src/x.ts:12 github_token`, o valor não aparece na saída nem no log, e nada é enviado; **dado** `token = process.env.GH_TOKEN`, **então** envia; **dado** um diff com um token do Telegram, um JWT e `AKIA` + 16 caracteres, **então** cada um sai com 3 `secret_detected` e o tipo certo, e `token = "your_token_here_000000000"` envia.
- **CA-CLI-GH-08** · *rápida* — **Dado** um PR cujo bloco `risco` foi criado pela classe Revisor, **quando** o Dev roda `mestre pr update --block risco "x" --expect-head <sha>`, **então** sai com 3 `block_owned`; **quando** o Revisor roda com um `--expect-head` que não é a cabeça atual, **então** sai com 7 `head_changed` e o corpo não muda; **com** a cabeça certa, **então** só o bloco `risco` é reescrito e os outros ficam como estavam.
- **CA-CLI-GH-09** · *rápida* — **Dado** um PR com `src/x.ts` alterado, **quando** o personagem roda `mestre pr note src/x.ts:12 "por que"`, **então** o GitHub falso recebe um comentário de revisão na linha 12 daquele arquivo, na cabeça atual; **dado** `README.md:1`, fora do diff, **então** sai com 2 `usage`.
- **CA-CLI-GH-10** · *rápida* — **Dado** `mestre issue create --title "Login lento" --fingerprint "auth: login lento"`, **quando** roda, **então** cria a issue com a impressão no corpo; **quando** roda de novo com `--fingerprint "Auth:  login LENTO"`, **então** não cria outra, comenta "visto de novo" na primeira e devolve `seen_again: true`; **dado** a primeira fechada, **quando** roda de novo, **então** cria uma nova com "Recorrência de #<n>", o rótulo `recorrencia` (criado no GitHub falso, que não o tinha) e `recurrence_of`.
- **CA-CLI-GH-11** · *rápida* — **Dado** o GitHub falso com a cota em zero e o aluguel válido, **quando** o Mestre da mesa roda `mestre issue comment 50 "x"`, **então** sai com 0 e `queued: true`, e `mestre pr status` sai com 5 `github_unavailable`; **dado** um limite secundário (`403` com `retry-after`), **então** o comentário sai com 5 `github_unavailable` e não é repetido sozinho.
- **CA-CLI-GH-12** · *rápida* — **Dado** `mestre issue update 42 --remove-label mestre:humano` (ou `--add-label epico`) pedido por um personagem da mesa, **quando** o motor avalia, **então** sai com 3 `reserved_label` e o GitHub falso não recebe nada.

## Depende de

- [convencoes](convencoes.md)
- [acoes-por-nivel](../github/acoes-por-nivel.md) · [identidades-e-apps](../github/identidades-e-apps.md)
- [mesas](../orquestracao/mesas.md) (espelho e sincronização) · [missoes](../orquestracao/missoes.md)
- [permissoes](../classes/permissoes.md)
