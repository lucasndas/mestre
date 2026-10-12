# CLI: classes e rituais

> **Assunto:** sintaxe de `class` e `ritual` · **Componente:** CLI · **Atualizado:** 2026-10-12

## Objetivo

Referência de sintaxe dos comandos que gerenciam classes (pacotes de função do agente) e rituais (disparos agendados). Saída e códigos seguem [convencoes](convencoes.md); o comportamento está em [classes](../classes/classes.md) e [rituais](../orquestracao/rituais.md).

## Comportamento

| Comando | Quem pode usar |
|---|---|
| `class list\|show\|check` | Mestre da mesa, Grão-Mestre; classes autorizadas |
| `class create\|edit\|assign` | Mestre da mesa (escopo da mesa), Grão-Mestre (escopo do workspace, só propõe) |
| `class delete` | Mestre da mesa, Grão-Mestre, sempre com pedido explícito de usuário |
| `ritual …` | Grão-Mestre (rituais do workspace), Mestre da mesa (rituais da própria mesa) |

### `mestre class`
```
mestre class list [--scope global|workspace|table]
mestre class show "<classe>"
mestre class create "<classe>" --app <novo | herdar:<classe> | <nome-do-app>> --from <pasta> [--scope table|workspace]
mestre class edit "<classe>" [--from <pasta>] [--app <…>]
mestre class assign "<Personagem>" "<classe>"
mestre class check "<classe>"
mestre class delete "<classe>"
```
- `list`: nome, escopo, App vinculado e estado do App (`ready`, `in_creation`, `inherited`), provedor preferido. Padrão: todas as visíveis ao chamador.
- `show`: `CLASS.md`, resumo do `permissions.yaml`, hooks extras, skills extras e App.
- `create`: `--app` é **obrigatório** (sem ele → 2 `app_required`). `--from` aponta uma pasta com o pacote (`CLASS.md`, `permissions.yaml`, `hooks/`, lista de skills; estrutura em [classes](../classes/classes.md)); o motor valida, confere contra o teto e copia para o lugar do escopo. `--scope` padrão `table`.
  - escopo da mesa, dentro do teto: criada na hora (exit 0);
  - escopo do workspace: vira proposta para um usuário Administrador ou acima (exit 9);
  - `--app novo`: a classe já funciona com o App "Mestre" e a marca `[Classe]`; o pedido vai ao Grão-Mestre (`app_status: "in_creation"`), que cria o App ou vincula a uma classe existente explicando o porquê ([identidades-e-apps](../github/identidades-e-apps.md)).
  - `--json` → `{name, scope, path, app, app_status}`.
- `edit`: troca o pacote e/ou o App, com as mesmas validações e aprovações do escopo. Trocar o App só vale para ações novas.
- `assign`: muda a classe de um personagem da mesa; vale a partir do próximo turno dele (nunca no meio de um).
- `check`: valida o pacote e mostra, por provedor, como cada permissão será aplicada (`mestre hook` e reforço nativo) e o App com o estado dele. Exit 0 se válido; 2 `invalid_class` com a lista de problemas. Também avisa quando o `CLASS.md` passa do tamanho recomendado ([personagem](../sessao-zero/personagem.md)).
- `delete`: ação destrutiva (regra 1 de [convencoes](convencoes.md)) → 9 sem pedido explícito; classe em uso por personagem vivo → 7 `in_use`.
- Erros comuns: permissão acima do teto → 3 `ceiling_denied` (com o item que passou); App inexistente → 4; classe de outra mesa → 4 `not_visible`.

### `mestre ritual`
```
mestre ritual create "<nome>" --target "<Personagem>" (--prompt "<texto>" | --quest "<Missão>")
                     (--every <dur> | --daily <HH:MM> | --weekly "<dias> <HH:MM>" | --once "<ISO 8601>")
                     [--pre-run "<comando>"] [--timeout <dur>]
mestre ritual list
mestre ritual show "<nome>"
mestre ritual edit "<nome>" [mesmas flags de create]
mestre ritual enable "<nome>"      mestre ritual disable "<nome>"
mestre ritual run "<nome>"
mestre ritual delete "<nome>"
```
- `--prompt` é o texto entregue ao alvo (um prompt para agente, uma linha de comando para terminal de `spawn`); `--quest` dispara uma missão da mesa.
- Agenda: `--every 30m`; `--daily 02:00`; `--weekly "mon,thu 09:00"` (dias `mon`…`sun`); `--once "2026-10-20T14:00"`. Fuso do Core. Exatamente uma das quatro.
- `--pre-run`: comando rodado antes do disparo; a saída dele substitui `{{output}}` no `--prompt` (onde roda e com que permissões: [rituais](../orquestracao/rituais.md)). `{{output}}` sem `--pre-run` → 2.
- `--timeout`: quanto o disparo pode ficar na fila do alvo ocupado antes de ser cancelado e devolvido ao dono do ritual (padrão 30m).
- `list`: nome, alvo, agenda, próximo disparo, ativo, último resultado. `show`: tudo, mais o histórico recente. `--json` → `{name, target, action, schedule, pre_run, timeout, enabled, next_run, last_result}`.
- `run`: dispara agora, pelo mesmo caminho da agenda (entra na fila se o alvo estiver ocupado). `enable`/`disable`: liga e desliga sem apagar.
- `delete`: ação destrutiva → 9 sem pedido explícito.
- Erros: alvo fora da mesa do chamador → 4 `not_visible`; agenda inválida → 2 `invalid_schedule`; nome repetido → 7 `name_taken`.

## Regras e erros

- O Mestre de uma mesa nunca libera numa classe algo que o teto proíbe; o motor recusa (3) mesmo que o pacote peça ([teto-do-workspace](../classes/teto-do-workspace.md)).
- O Grão-Mestre pode propor classe do workspace, mas não aprova a própria proposta.

## Critérios de aceite

- **CA-CLI-CLR-01** · *rápida* — **Dado** `mestre class create "QA" --from ./qa` sem `--app`, **quando** roda, **então** sai com 2 `app_required` e nada é criado.
- **CA-CLI-CLR-02** · *rápida* — **Dado** um teto que proíbe internet e um pacote que libera `example.com`, **quando** o Mestre da mesa roda `class create`, **então** sai com 3 `ceiling_denied` citando o item de rede.
- **CA-CLI-CLR-03** · *rápida* — **Dado** o Grão-Mestre criando classe com `--scope workspace`, **quando** roda, **então** sai com 9 e a proposta aparece para Administradores, não para o próprio Grão-Mestre.
- **CA-CLI-CLR-04** · *rápida* — **Dado** `class create "QA API" --app novo` pelo Mestre da mesa, **quando** roda, **então** sai com 0, `app_status: "in_creation"`, e o Grão-Mestre recebe o pedido na caixa de entrada.
- **CA-CLI-CLR-05** · *rápida* — **Dado** `ritual create` com `--daily` e `--every` juntos, **quando** roda, **então** sai com 2 `invalid_schedule`.
- **CA-CLI-CLR-06** · *rápida* — **Dado** um ritual com `--pre-run "date"` e `--prompt "Hoje é {{output}}"`, **quando** dispara, **então** o alvo recebe o texto com a saída do comando no lugar do marcador.
- **CA-CLI-CLR-07** · *rápida* — **Dado** `mestre class check "Dev"` de uma classe válida, **quando** roda, **então** a saída tem uma seção por provedor (Claude Code, Codex, Antigravity) e a linha do App com o estado.

## Depende de

- [convencoes](convencoes.md)
- [classes](../classes/classes.md) · [permissoes](../classes/permissoes.md) · [teto-do-workspace](../classes/teto-do-workspace.md)
- [identidades-e-apps](../github/identidades-e-apps.md)
- [rituais](../orquestracao/rituais.md) · [missoes](../orquestracao/missoes.md)
