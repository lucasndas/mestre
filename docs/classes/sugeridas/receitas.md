# Receitas de missão

> **Assunto:** as missões sugeridas por tipo de pedido (bug, funcionalidade nova, ajuste, investigação) e o grimório "Refino de UI" · **Componente:** Motor · **Atualizado:** 2026-10-18

Receitas do pacote [sugerido](../classes-sugeridas.md), usadas com `mestre quest create --recipe <nome> [--skip <id>]…` ([receitas e etapa opcional](../classes-sugeridas.md#receitas-e-etapa-opcional)). O `name` de cada uma é o nome da receita. Usam as fichas leves das [classes sugeridas](INDEX.md); o Mestre da mesa troca pela pesada nas etapas difíceis e tira as etapas opcionais que não cabem no pedido (`skip`). Revisor e QA rodam em paralelo depois do Dev, e os dois podem devolver ao Dev; o motor refaz o Dev e volta a pôr na fila o que dependia dele ([missões](../../orquestracao/missoes.md#devolução-com-limite)).

## Bug

```yaml
name: Bug
max_returns: 3
steps:
  - {id: investigar, sheet: Investigador leve}
  - {id: dev,     sheet: Dev leve,     after: [investigar]}
  - {id: revisao, sheet: Revisor leve, after: [dev], can_return_to: dev}
  - {id: qa,      sheet: QA leve,      after: [dev], can_return_to: dev}
  - {id: retro,   sheet: Revisor leve, after: [revisao, qa], optional: true}
```

"Pronto": a causa achada, o teste que falhava passando, cada critério provado.

## Funcionalidade nova

```yaml
name: Funcionalidade
max_returns: 3
steps:
  - {id: pesquisa, sheet: Pesquisador leve, optional: true}
  - {id: design,   sheet: Designer leve,    optional: true}
  - {id: dev,      sheet: Dev leve,      after: [pesquisa, design]}
  - {id: revisao,  sheet: Revisor leve,  after: [dev], can_return_to: dev}
  - {id: qa,       sheet: QA leve,       after: [dev], can_return_to: dev}
  - {id: docs,     sheet: Documentador leve, after: [revisao, qa], optional: true}
  - {id: retro,    sheet: Revisor leve,  after: [revisao, qa, docs], optional: true}
```

## Ajuste do que existe

```yaml
name: Ajuste
max_returns: 3
steps:
  - {id: dev,     sheet: Dev leve}
  - {id: revisao, sheet: Revisor leve, after: [dev], can_return_to: dev}
  - {id: qa,      sheet: QA leve,      after: [dev], can_return_to: dev}
  - {id: docs,    sheet: Documentador leve, after: [revisao, qa], optional: true}
```

"Pronto": só a mudança pedida; o Revisor aponta como Scope drift o que passou disso.

## Investigação

```yaml
name: Investigacao
steps:
  - {id: investigar, sheet: Pesquisador leve}
```

Termina com o diagnóstico no `done` e num pergaminho da mesa, sem PR. Para bug confirmado, o Mestre da mesa abre depois a receita "Bug".

## Refino de UI

Receita para quem tem interface: uma etapa critica e a outra corrige, em contextos separados, até a interface passar ou as voltas acabarem.

```yaml
name: Refino de UI
max_returns: 4
steps:
  - {id: critica,  sheet: Designer leve}
  - {id: correcao, sheet: Dev leve,    after: [critica]}
  - {id: conferir, sheet: Designer leve, after: [correcao], can_return_to: correcao}
```

- A etapa `critica` lista até 8 problemas com `arquivo:linha`, em ordem de gravidade, e grava a nota de cada rodada no pergaminho da missão (que faz o papel de registro entre rodadas); `conferir` julga pelo que vê e pelo código, nunca pelo resumo de quem corrigiu.
- Precisa de navegador para capturar a tela: a classe Designer só consegue isso com o `playwright` ligado na classe ([MCP por classe](../mcp-por-classe.md)) e liberado no teto ([formato das permissões](../formato-das-permissoes.md)), em qualquer provedor; sem ele, a receita roda só pelo código.
