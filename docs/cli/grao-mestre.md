# CLI: comandos do Grão-Mestre

> **Assunto:** sintaxe de `table`, `gh`, `app`, `approval`, `decree` e `notify`, exclusivos do Grão-Mestre · **Componente:** CLI · **Atualizado:** 2026-10-12

## Objetivo

Referência de sintaxe dos comandos que só o Grão-Mestre do workspace usa: abrir e encerrar mesas, agir no nível do repositório no GitHub, cuidar dos GitHub Apps, tratar aprovações, propor decretos e avisar os usuários. Qualquer outro chamador recebe exit 3 `level_denied`. Saída e códigos seguem [convencoes](convencoes.md).

## Comportamento

| Comando | Comportamento em |
|---|---|
| `table open\|list\|status\|close` | [mesas](../orquestracao/mesas.md), [grao-mestre](../orquestracao/grao-mestre.md) |
| `gh epic\|review\|approve\|merge\|settings` | [acoes-por-nivel](../github/acoes-por-nivel.md), [aprovacao-no-github](../github/aprovacao-no-github.md), [politica](../aprovacoes/politica.md) |
| `app list\|status\|create` | [identidades-e-apps](../github/identidades-e-apps.md), [criacao-de-apps](../github/criacao-de-apps.md) |
| `approval ask\|decide\|list` | [fluxo](../aprovacoes/fluxo.md), [politica](../aprovacoes/politica.md) |
| `decree propose\|list` | [decretos](../telegram/decretos.md) |
| `notify` | [bot](../telegram/bot.md), [roteamento](../telegram/roteamento.md) |

### `mestre table`
```
mestre table open --epic <n> --slug <slug> [--sheet "<Ficha do Mestre>"] [--base-branch <branch>] [--from-message <id>]
mestre table list [--state open|closed|all]
mestre table status "<mesa>"
mestre table close "<mesa>" [--cancel --reason "<texto>"]
```
- `open`: abre a mesa do épico `<n>`: branch `epico/<n>-<slug>` a partir de `--base-branch` (padrão: a branch principal), worktree no Core e Mestre da mesa recrutado com `--sheet` (padrão: a ficha de Mestres de mesa do workspace). `--from-message` indica a mensagem de usuário de origem: o motor copia o texto original e os anexos para a caixa de entrada do novo Mestre (sem reescrever). Volta quando o Mestre foi recrutado; não espera ele trabalhar. `--json` → `{name, branch, epic, worktree, master: {name, id, node}}`. Épico inexistente ou sem rótulo `epico` → 4 `epic_not_found`; mesa já aberta para o épico → 7 `table_exists`; `slug` fora de `[a-z0-9-]` → 2.
- `list`: nome, estado (aberta, em trabalho, em revisão, encerrada, cancelada), épico, branch, Mestre, nós em uso, PR e consumo.
- `status`: missões e etapa atual, esperas, PR (CI e revisões), personagens e consumo da mesa.
- `close`: encerra depois do merge; o motor só remove o worktree se a branch foi integrada e não há alterações pendentes, senão sai com 7 `unmerged_changes` listando o que falta (o Grão-Mestre pergunta aos usuários). `--cancel` fecha o PR sem merge e a issue como "não planejada"; é ação destrutiva: sem pedido explícito de usuário sai com 9 (regra 1 de [convencoes](convencoes.md)).

### `mestre gh`
```
mestre gh epic create --title "<…>" (--body "<…>" | --body-file <arq>) [--label <r>]…
mestre gh epic update <n> [--title …] [--body … | --body-file …] [--depends-on <m>]… [--state open|closed]
mestre gh epic list [--state open|closed|all]
mestre gh review <pr> [--diff]
mestre gh review <pr> (--comment | --request-changes) (--body "<…>" | --body-file <arq>)
mestre gh approve <pr> --reason "<…>" [--on-behalf "<usuário>"]
mestre gh merge <pr> [--method merge|squash|rebase]
mestre gh settings show
mestre gh settings apply --file <arq.json>
```
- `epic create`: cria a issue do épico com o rótulo `epico` (o motor cria o rótulo se faltar; [0015](../decisoes/0015-epico-como-rotulo.md)), com o App "Grão-Mestre". `--json` → `{number, id, url}`. `epic update --depends-on` liga dependência entre épicos.
- `review <pr>`: sem flags, mostra arquivos alterados, CI, revisões e comentários (`--diff` inclui o diff); com `--comment`/`--request-changes`, publica uma revisão com o App "Grão-Mestre".
- `approve`: aprova o PR no commit atual (o motor amarra a aprovação ao `head_sha`; se mudou desde o último `review`, sai com 7 `head_changed`). O motivo vira o comentário da aprovação. Se a [politica](../aprovacoes/politica.md) exige uma pessoa, o motor não aprova: abre o pedido aos usuários e sai com 9. `--on-behalf` usa a credencial do usuário só com delegação por decreto; sem ela → 3 `no_delegation` ([aprovacao-no-github](../github/aprovacao-no-github.md)).
- `merge`: faz o merge com o App "Grão-Mestre", respeitando as regras do repositório. `--method` padrão `merge` (preserva a autoria de cada commit pelo App da classe). Bloqueado por regra → 3 `protected` com o motivo; não mergeável → 7 `not_mergeable`.
- `settings show`: proteção da branch principal, rulesets, Apps instalados e o resultado do teste de aprovação de App ([workspace](../sessao-zero/workspace.md)). `settings apply`: aplica a configuração descrita no arquivo (corpo da API do GitHub para proteção ou ruleset) **só com aprovação de usuário**: sempre sai com 9 e aplica depois de aprovado.

### `mestre app`
```
mestre app list
mestre app status "<app>"
mestre app create --class "<classe>" [--name "<nome>"]
```
- `list`: cada App (fixo ou de classe), classes vinculadas, repositórios onde está instalado, estado, e a contagem `n/100` da conta.
- `status`: permissões, instalação por workspace, se a chave está no cofre (nunca a chave) e o estado da criação, se houver.
- `create`: cria e instala o App da classe pelo navegador dedicado; volta com 9 e o id do pedido, e o resultado chega pela caixa de entrada. Nome padrão: `<prefixo>-<classe>`; mais de 34 caracteres → 2 `name_too_long`; conta com 100 Apps → 8 `app_limit`; classe já com App próprio → 7 `app_exists`.

### `mestre approval`
```
mestre approval ask --action "<ação>" --summary "<resumo>" (--details "<texto>" | --details-file <arq>) --recommend approve|deny --reason "<motivo da dúvida>"
mestre approval decide <id> approve|deny --reason "<motivo>"
mestre approval list [--state pending|decided|all] [--table "<mesa>"]
```
- `ask`: encaminha aos usuários um pedido de aprovação (o que é, onde, por quê, detalhes ou diff, motivo da dúvida e recomendação), com os botões do Telegram e do painel. `<ação>` referencia o pedido que está esperando (o id do pedido pendente, visto em `approval list`) ou descreve a ação nova; o motor amarra a aprovação ao estado atual dela. Volta com 9 e o id; a decisão chega pela caixa de entrada.
- `decide`: o Grão-Mestre decide como aprovador delegado um pedido pendente; o motivo vai para a auditoria. Fora do que a política deixa aprovar sozinho, ação destrutiva ou pedido que exige uma pessoa → 3 `human_required` (use `approval ask`); pedido já decidido ou expirado → 7 `already_decided`.
- `list`: pedidos com id, ação, mesa, quem pediu, estado, quem decidiu e prazo. `--json` → `{items: [{id, action, table, requested_by, state, decided_by, expires_at}]}`.

### `mestre decree`
```
mestre decree propose --kind exception|rule --text "<o que vai valer>" --scope "<workspace|mesa|classe>" [--until "<ISO 8601>"] --from-message <id>
mestre decree list [--all]
```
- `propose`: transforma a ordem de um usuário (a mensagem de `--from-message`) numa proposta estruturada; o motor mostra ao autor exatamente o que vai valer e pede confirmação por botão (e PIN, quando o decreto afrouxa segurança). Volta com 9; o resultado chega pela caixa de entrada. Autor sem nível para aquele tipo de decreto → 3 `level_denied`. O Grão-Mestre nunca cria decreto sozinho.
- `list`: decretos ativos (número, autor e nível, texto, escopo, validade); `--all` inclui revogados e expirados.

### `mestre notify`
`mestre notify "<mensagem>" [--table "<mesa>"]` — avisa os usuários do workspace no painel e no Telegram, conforme o nível de cada um; `--table` põe a mesa no cabeçalho. `--json` → `{delivered: {panel, telegram}, queued}`. Texto com cara de segredo → 3 `secret_detected`. Para responder a uma mensagem use `reply` ([comunicacao](comunicacao.md)).

## Regras e erros

- Nenhum comando entrega token ao Grão-Mestre: o motor executa tudo com as credenciais do cofre.
- Configurações do repositório e uso de credencial de pessoa nunca saem sem a aprovação ou delegação descrita nos documentos linkados.
- Quando uma regra do GitHub não pode ser satisfeita, o erro explica qual e por quê; nenhum comando tenta contornar.

## Critérios de aceite

- **CA-CLI-GM-01** · *rápida* — **Dado** um Mestre de mesa, **quando** roda `mestre table open --epic 42 --slug login`, **então** sai com 3 `level_denied`.
- **CA-CLI-GM-02** · *completa* — **Dado** o épico #42 com rótulo `epico`, **quando** o Grão-Mestre roda `mestre table open --epic 42 --slug login --from-message <id>`, **então** existe a branch `epico/42-login`, o worktree, o Mestre da mesa recrutado e a mensagem original na caixa de entrada dele (MVP-4).
- **CA-CLI-GM-03** · *rápida* — **Dado** um PR cujo `head_sha` mudou depois do `gh review`, **quando** o Grão-Mestre roda `mestre gh approve`, **então** sai com 7 `head_changed` e nada é aprovado.
- **CA-CLI-GM-04** · *rápida* — **Dado** um PR que toca uma área sensível da política, **quando** o Grão-Mestre roda `mestre gh approve`, **então** sai com 9 e um pedido de aprovação vai aos usuários.
- **CA-CLI-GM-05** · *rápida* — **Dado** `mestre gh settings apply --file r.json`, **quando** roda, **então** sai com 9 e nada muda no GitHub antes da aprovação.
- **CA-CLI-GM-06** · *rápida* — **Dado** uma conta com 100 Apps, **quando** o Grão-Mestre roda `mestre app create --class "QA"`, **então** sai com 8 `app_limit`.
- **CA-CLI-GM-07** · *rápida* — **Dado** uma mesa com commit não enviado, **quando** o Grão-Mestre roda `mestre table close`, **então** sai com 7 `unmerged_changes` e o worktree continua.
- **CA-CLI-GM-08** · *rápida* — **Dado** um pedido pendente numa área sensível da política, **quando** o Grão-Mestre roda `mestre approval decide <id> approve --reason "ok"`, **então** sai com 3 `human_required` e o pedido continua pendente.
- **CA-CLI-GM-09** · *rápida* — **Dado** uma mensagem de um Administrador pedindo uma regra permanente, **quando** o Grão-Mestre roda `mestre decree propose --kind rule … --from-message <id>`, **então** sai com 9 e nenhum decreto fica ativo antes da confirmação do autor.

## Depende de

- [convencoes](convencoes.md) · [comunicacao](comunicacao.md)
- [mesas](../orquestracao/mesas.md) · [grao-mestre](../orquestracao/grao-mestre.md)
- [acoes-por-nivel](../github/acoes-por-nivel.md) · [aprovacao-no-github](../github/aprovacao-no-github.md) · [identidades-e-apps](../github/identidades-e-apps.md) · [criacao-de-apps](../github/criacao-de-apps.md)
- [politica](../aprovacoes/politica.md) · [fluxo](../aprovacoes/fluxo.md) · [decretos](../telegram/decretos.md) · [bot](../telegram/bot.md)
