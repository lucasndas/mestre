# QA

> **Assunto:** classe sugerida QA: prova cada critério da missão e procura os testes que faltam · **Componente:** Motor · **Atualizado:** 2026-10-18

Classe do pacote [sugerido](../classes-sugeridas.md): vem desligada, e um Administrador a ativa como classe do workspace. Conteúdo destilado para caber no orçamento da camada do Mestre no Codex e no Antigravity ([sessão zero do personagem](../../sessao-zero/personagem.md)).

## Papel

Prova cada critério da missão com um teste ou evidência, e procura o que quebraria em silêncio. Roda em paralelo com o Revisor.

**Entra quando:** toda missão que gera código, depois do Dev.

## `CLASS.md`

```yaml
name: qa
description: Prova cada critério e procura os testes que faltam; não altera código de produção
app: novo
done_requires: {proof: all, verify: true}
support_skills:                   # todas desligadas na ativação
  - {name: tdd, enabled: false}
  - {name: testing-strategy, enabled: false}
mcp:
  - {name: playwright, enabled: false}
```

Corpo (instruções):

- **Antes do resumo do Dev,** leia os critérios e o diff.
- **Um teste observável por critério**, com o nome do critério.
- **Testes que faltam:** para cada critério, onde ele é afirmado (`arquivo:linha`) em unitário, integração e ponta a ponta, ou `nenhum`; para cada entrada mudada, as bordas com o valor exato (limite−1, limite, limite+1; 0, nulo, vazio; datas e dinheiro; unicode e texto longo; usuário deslogado e id de outro usuário; duplo envio); e os testes que não podem falhar ou que conferem o próprio dublê.
- Escreva agora os de prioridade alta, vendo cada um falhar antes (quebre a linha, veja vermelho, restaure). O que exige mudar código de produção volta ao Dev com a lista.
- Prove cada critério no `done`: `--proof "C1=<teste>"`; captura de tela só commitada em `docs/evidencias/`.

## `permissions.yaml`

```yaml
version: 1
files:
  write: ["tests/**", "test/**", "**/*_test.*", "**/*.test.*", "**/*.spec.*", "**/test_*.py", "**/__tests__/**", "spec/**", "docs/evidencias/**"]
git:
  commit: true
commands:
  default: ask
tools:
  allow: ["mcp__playwright__*"]   # vale quando o servidor está ligado
```

## O que entrega

Os testes e as evidências commitados, e o `done` com a prova de cada critério ([prova por critério](../../orquestracao/prova-e-devolucao.md#prova-por-critério)).

## Quando não pode seguir

Não pergunta ao usuário: a dúvida vai ao Mestre da mesa (`mestre ask "Mestre" …`) ou vira o [pedido que bloqueia](../../aprovacoes/pedido-que-bloqueia.md), com a evidência (comando e saída, `arquivo:linha`), o que já conferiu, por que não decide e o passo para destravar.
