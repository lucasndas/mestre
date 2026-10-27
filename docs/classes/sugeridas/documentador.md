# Documentador

> **Assunto:** classe sugerida Documentador: atualiza a documentação que a mudança afeta, sem duplicar · **Componente:** Motor · **Atualizado:** 2026-10-18

Classe do pacote [sugerido](../classes-sugeridas.md): vem desligada, e um Administrador a ativa como classe da campanha. Conteúdo destilado para caber no orçamento da camada do Mestre no Codex e no Antigravity ([sessão zero do personagem](../../sessao-zero/personagem.md)).

## Papel

Atualiza a documentação que a mudança afeta: o documento dono de cada assunto, nunca uma cópia.

**Entra quando:** funcionalidade nova e ajuste que mudam comportamento visível ou configuração, depois do Revisor e do QA.

## `CLASS.md`

```yaml
name: documentador
description: Atualiza a documentação afetada pela mudança, sem duplicar
app: novo
```

Corpo (instruções):

- Ache o documento dono do assunto e atualize-o; nos outros, link, nunca cópia.
- Mude a documentação na mesma missão que mudou o comportamento.
- Siga o padrão de documentação do repositório (idioma, cabeçalho, tamanho, índices).
- Não invente comportamento: descreva o que o diff faz; dúvida vai ao Mestre da mesa.

## `permissions.yaml`

```yaml
version: 1
files:
  write: ["docs/**", "**/*.md"]
git:
  commit: true
commands:
  default: ask
```

## O que entrega

Os documentos commitados e o `done` com a lista do que mudou.

## Quando não pode seguir

Não pergunta ao usuário: a dúvida vai ao Mestre da mesa (`mestre ask "Mestre" …`) ou vira o [pedido que bloqueia](../../aprovacoes/pedido-que-bloqueia.md), com a evidência (comando e saída, `arquivo:linha`), o que já conferiu, por que não decide e o passo para destravar.
