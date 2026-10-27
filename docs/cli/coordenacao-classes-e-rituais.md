# CLI: classes e rituais

> **Assunto:** sintaxe de `class` e `ritual` · **Componente:** CLI · **Atualizado:** 2026-10-19

## Objetivo

Referência de sintaxe dos comandos que gerenciam classes (pacotes de função do agente) e rituais (disparos agendados). Saída e códigos seguem [convencoes](convencoes.md); o comportamento está em [classes](../classes/classes.md) e [rituais](../orquestracao/rituais.md).

## Comportamento

| Comando | Quem pode usar |
|---|---|
| `class list\|show\|check` | Mestre da mesa, Grão-Mestre; classes autorizadas |
| `class test` (desde o M7) | Mestre da mesa (classes da mesa), Grão-Mestre (classes da campanha); nunca liberável por classe, porque gasta cota |
| `class create\|edit\|assign` | Mestre da mesa (escopo da mesa), Grão-Mestre (escopo da campanha, só propõe) |
| `class delete` | Mestre da mesa, Grão-Mestre, sempre com pedido explícito de usuário |
| `ritual …` | Grão-Mestre (rituais da campanha), Mestre da mesa (rituais da própria mesa) |

### `mestre class`
```
mestre class list [--scope global|workspace|table]
mestre class show "<classe>" [--export]
mestre class create "<classe>" --app <novo | herdar:<classe> | <nome-do-app>> --from <pasta> [--scope table|workspace]
mestre class edit "<classe>" [--from <pasta>] [--app <…>]
mestre class assign "<Personagem>" "<classe>"
mestre class check "<classe>"
mestre class test "<classe>" [--provider claude|codex|antigravity]… [--case <nome>]   # desde o M7
mestre class delete "<classe>"
```
- `list`: nome, escopo, App vinculado, se o vínculo é herdado (`inherited: true`), estado do App com os identificadores de [criação de Apps](../github/criacao-de-apps.md) (`solicitado` enquanto o Grão-Mestre ainda não preparou o link) e provedor preferido. Padrão: todas as visíveis ao chamador.
- `show`: `CLASS.md`, resumo do `permissions.yaml`, hooks extras, skills extras e App. Com `--export`, copia o pacote da **versão aprovada** para `$MESTRE_TMP/classes/<classe>/` (sobrescreve) e devolve o caminho, para ser editado e enviado de volta com `edit --from`.
- `create`: `--app` é **obrigatório** (sem ele → 2 `app_required`). `--from` aponta uma pasta com o pacote (`CLASS.md`, `permissions.yaml`, `hooks/`, lista de skills; estrutura em [classes](../classes/classes.md)); o motor valida, confere contra o teto e copia para o lugar do escopo. `--scope` padrão `table`.
  - escopo da mesa, dentro do teto: criada na hora (exit 0);
  - escopo da campanha: vira proposta para um usuário Administrador ou acima (exit 9);
  - `--app novo`: a classe já funciona com o App "Mestre" e a marca `[Classe]`; o pedido vai ao Grão-Mestre (`app_status: "solicitado"`), que prepara o App (`mestre app create`, com o link de criação enviado ao Senhor da Guilda) ou vincula a uma classe existente explicando o porquê ([identidades-e-apps](../github/identidades-e-apps.md)).
  - `--json` → `{name, scope, path, app, app_status}`.
- `edit`: troca o pacote e/ou o App, com as mesmas validações e aprovações do escopo ([onde a classe é alterada](../classes/classes.md#onde-a-classe-é-alterada)). Trocar o App só vale para ações novas.
- `--from` (em `create` e `edit`) só aceita uma pasta dentro de `$MESTRE_TMP` do próprio personagem; fora dela → 2 `from_outside_tmp`. Usuários pelo painel não usam a CLI.
- `assign`: muda a classe de um personagem da mesa; as permissões e os hooks valem na próxima chamada de hook dele (a Cidadela já publicou as regras efetivas) e o `CLASS.md`, no contexto do próximo turno ([classes](../classes/classes.md)).
- `check`: valida o pacote e mostra, por provedor, como cada permissão será aplicada (`mestre hook` e reforço nativo) e o App com o estado dele. Exit 0 se válido; 2 `invalid_class` com a lista de problemas. Também avisa quando o `CLASS.md` passa do tamanho recomendado ([personagem](../sessao-zero/personagem.md)).
- `test` (desde o M7): roda os casos de `cases/` da classe com o provedor real e volta na hora com o `run_id`; o resultado chega como item `system`. Casos, rodada, custo e erros em [teste de classe](../provedores/teste-de-classe.md).
- `delete`: ação destrutiva (regra 1 de [convencoes](convencoes.md)) → 9 sem pedido explícito; classe em uso por personagem vivo → 7 `in_use`.
- Erros comuns: permissão acima do teto → 3 `ceiling_denied` (com o item que passou); App inexistente → 4; classe de outra mesa → 4 `not_visible`.

### `mestre ritual`
```
mestre ritual create "<nome>" (--target "<Personagem>" (--prompt "<texto>" | --command "<linha>") | --quest "<Missão>")
                     (--every <dur> | --daily <HH:MM> | --weekly "<dias> <HH:MM>" | --once "<ISO 8601>")
                     [--table "<mesa>"] [--pre-run "<comando>"] [--timeout <dur>]
mestre ritual list
mestre ritual show "<nome>"
mestre ritual edit "<nome>" [mesmas flags de create]
mestre ritual enable "<nome>"      mestre ritual disable "<nome>"
mestre ritual run "<nome>"
mestre ritual delete "<nome>"
```
- O alvo é sempre um **personagem existente** (`--target`) ou uma **missão** (`--quest`), nunca uma ficha. `--prompt` é o texto entregue a um agente; `--command` é uma linha digitada num terminal de `spawn`, seguida de Enter; `--quest` dispara uma missão da mesa. `--table` indica a mesa quando o Grão-Mestre cria o ritual para um Mestre de mesa ou uma missão (sem isso → 2 `table_required`).
- Agenda: `--every 30m`; `--daily 02:00`; `--weekly "mon,thu 09:00"` (dias `mon`…`sun`); `--once "2026-10-20T14:00"`. No fuso da campanha (padrão: o [fuso da Guilda](../painel/administracao.md#fuso-da-guilda); [rituais](../orquestracao/rituais.md)). Exatamente uma das quatro.
- `--pre-run`: comando rodado antes do disparo; a saída dele substitui `{{output}}` no `--prompt` ou no `--command` (onde roda e com que permissões: [rituais](../orquestracao/rituais.md)). `{{output}}` sem `--pre-run` → 2.
- `--timeout`: quanto o disparo pode ficar na fila do alvo ocupado antes de ser cancelado e devolvido ao dono do ritual (padrão 30m).
- `list`: nome, alvo, agenda, próximo disparo, ativo, último resultado. `show`: tudo, mais o `content` e os 10 últimos disparos. `--json` → `{name, state, target, quest, table, action: "prompt" | "command" | "quest", schedule, pre_run, timeout, enabled, next_run, last_result}`, com `timeout` em segundos (inteiro) e `schedule` como `every 3600s`, `daily 02:00`…
- `run`: dispara agora, pelo mesmo caminho da agenda (entra na fila se o alvo estiver ocupado); de ritual desativado → 2 `usage`. `enable`/`disable`: liga e desliga sem apagar.
- `delete`: ação destrutiva → 9 sem pedido explícito.
- Erros: alvo fora da mesa do chamador → 4 `not_visible`; agenda inválida → 2 `invalid_schedule`; nome repetido (sem diferenciar maiúsculas) → 7 `name_taken`.

O estado do App das classes vem com o GitHub, no M5 ([criação de Apps](../github/criacao-de-apps.md)).

### Como o motor decide o que o texto deixa em aberto

- **Nome da classe:** é o *slug* (`[a-z0-9-]`); `"QA API"` vira `qa-api`, e `show`, `edit`, `check`, `assign` e `delete` aceitam o nome com ou sem o disfarce (`"Dev"` acha `dev`).
- **`--from`:** a CLI, que roda como o agente, lê a pasta e manda os arquivos (só texto, sem links, até 512 KiB); o motor nunca abre o caminho. A pasta tem de estar em `$MESTRE_TMP` depois de resolvidos os links (`from_outside_tmp`, 2); sem `$MESTRE_TMP` no processo, também.
- **`show --export`:** o motor grava em `$MESTRE_TMP/classes/<classe>/` só com `safefile` (um link no caminho recusa) e tira o que uma exportação anterior deixou e o pacote não tem. `--json` → `{name, scope, version, description, class_md, permissions, hooks, skills, app, text}` (e `path` com `--export`).
- **`list`:** `--json` → `{classes: [{name, scope, table, state, app, inherited, app_state, provider}]}`; `state` é o da listagem do motor (`approved`, `pending`, `new`, `missing`, `unreadable`).
- **`create`:** o Grão-Mestre sem `--scope` faz classe da campanha; o Mestre da mesa, da mesa. `--scope table` sem mesa (Grão-Mestre) → 2 `table_required`. Proposta (campanha pelo Grão-Mestre) sai com 9 `pending` e o número em `details.proposal`; só um Administrador ou acima aprova, nunca quem propôs. `--app novo` aplicado põe um item de sistema na caixa do Grão-Mestre pedindo o App.
- **`assign`:** grava a classe no registro e publica as regras efetivas na hora; o `CLASS.md` é lido pelo motor da máquina no início do turno. O Grão-Mestre não tem mesa → 2. Personagem fora da mesa → 4 `not_visible`. Alvo que não é personagem comum (o Mestre da mesa, e o próprio chamador) → 3 `forbidden`: as regras dos orquestradores vêm da classe `orquestrador`, e o Mestre não muda as próprias.
- **`delete`:** confere o direito no escopo (3 `forbidden`), depois o uso (7 `in_use`, com os nomes), depois o pedido explícito do usuário; sem ele, 9 `approval_required` (o fluxo de aprovação é do M5).
- **Ritual:** o escopo vem de quem chama (Mestre da mesa: a mesa; Grão-Mestre: a campanha). `--table` só existe em `create` e `edit` e, para o Grão-Mestre, nomeia a mesa do Mestre alvo (sem ele, alvo que não é ele mesmo → 2 `table_required`); `--quest` do Grão-Mestre → 2, porque missão é de mesa. `--timeout` do ritual é dado do ritual, não o limite da chamada. `delete` por personagem → 9 `approval_required` (o fluxo é do M5).

## Regras e erros

- O Mestre de uma mesa nunca libera numa classe algo que o teto proíbe; o motor recusa (3) mesmo que o pacote peça ([teto-do-workspace](../classes/teto-do-workspace.md)).
- O Grão-Mestre pode propor classe da campanha, mas não aprova a própria proposta.

## Critérios de aceite

- **CA-CLI-CLR-01** · *rápida* — **Dado** `mestre class create "QA" --from ./qa` sem `--app`, **quando** roda, **então** sai com 2 `app_required` e nada é criado.
- **CA-CLI-CLR-02** · *rápida* — **Dado** um teto que proíbe internet e um pacote que libera `example.com`, **quando** o Mestre da mesa roda `class create`, **então** sai com 3 `ceiling_denied` citando o item de rede.
- **CA-CLI-CLR-03** · *rápida* — **Dado** o Grão-Mestre criando classe com `--scope workspace`, **quando** roda, **então** sai com 9 e a proposta aparece para Administradores, não para o próprio Grão-Mestre.
- **CA-CLI-CLR-04** · *rápida* — **Dado** `class create "QA API" --app novo` pelo Mestre da mesa, **quando** roda, **então** sai com 0, `app_status: "solicitado"`, e o Grão-Mestre recebe o pedido na caixa de entrada.
- **CA-CLI-CLR-05** · *rápida* — **Dado** `ritual create` com `--daily` e `--every` juntos, **quando** roda, **então** sai com 2 `invalid_schedule`.
- **CA-CLI-CLR-06** · *rápida* — **Dado** um ritual com `--pre-run "date"` e `--prompt "Hoje é {{output}}"`, **quando** dispara, **então** o alvo recebe o texto com a saída do comando no lugar do marcador.
- **CA-CLI-CLR-08** · *rápida* — **Dado** `ritual create "x" --target "Dev Claude" --prompt "oi" --daily 09:00` em que `Dev Claude` é o nome de uma ficha e não de um personagem da mesa, **quando** roda, **então** sai com 4 `not_visible` e nenhum ritual é criado.
- **CA-CLI-CLR-09** · *rápida* — **Dado** `ritual create "limpa" --target "srv" --command "make clean" --every 1h`, **quando** dispara com `srv` livre, **então** o terminal recebe `make clean` seguido de Enter.
- **CA-CLI-CLR-10** · *rápida* — **Dado** um pacote em `./rascunho/qa` dentro do worktree, **quando** o Mestre da mesa roda `mestre class edit "qa" --from ./rascunho/qa`, **então** sai com 2 `from_outside_tmp` e nada muda; e, depois de `class show "qa" --export`, o pacote aparece em `$MESTRE_TMP/classes/qa/` igual à versão aprovada.
- **CA-CLI-CLR-07** · *rápida* — **Dado** `mestre class check "Dev"` de uma classe válida, **quando** roda, **então** a saída tem uma seção por provedor (Claude Code, Codex, Antigravity) e a linha do App com o estado.
- **CA-CLI-CLR-11** · *rápida* — **Dado** um ritual `--daily 02:00` com o tempo limite padrão, **quando** se roda `mestre ritual list --json`, **então** `timeout` é `1800` (inteiro, em segundos) e `schedule` é `daily 02:00`; **quando** se roda `ritual edit --every 30m`, **então** a resposta traz `every 1800s`.

## Depende de

- [convencoes](convencoes.md)
- [classes](../classes/classes.md) · [permissoes](../classes/permissoes.md) · [teto-do-workspace](../classes/teto-do-workspace.md)
- [rituais](../orquestracao/rituais.md) · [missoes](../orquestracao/missoes.md)
