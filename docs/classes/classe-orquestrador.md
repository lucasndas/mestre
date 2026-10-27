# Classe `orquestrador`

> **Assunto:** a classe fixa, embutida no binário, de onde o Grão-Mestre e os Mestres de mesa tiram as regras efetivas: conteúdo, o teto e a ficha que só restringem, e o que ela dá à atenuação da cadeia · **Componente:** Motor · **Atualizado:** 2026-10-14

## Objetivo

Dar aos orquestradores regras efetivas de verdade, iguais em toda campanha, para que a atenuação da [cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md#salvaguardas) ("ninguém aprova além do que poderia fazer") tenha o que conferir, e para que as próprias ações deles passem pelo mesmo [pipeline](permissoes.md#pipeline-de-decisão) que as dos personagens ([ADR 0024](../decisoes/0024-cadeia-de-aprovacao-por-risco.md), revisão de 2026-10-14). É a única classe que o Mestre traz pronta.

## Comportamento

### Conteúdo

O `permissions.yaml` da classe, versionado com o código (muda com a versão do binário, nunca por arquivo da campanha):

```yaml
version: 1
files: { read: ["**"], write: [] }   # não editam o projeto: pergaminhos e classes só pela CLI
commands: { default: ask }           # baixo sem regra roda direto; o resto vira pedido
tools: { default: ask }
git: { commit: false, request_push: false, workflows: never }  # o pr open do Mestre da mesa vem do nível dele
# network ausente: qualquer domínio, que o teto restringe
```

- Sem `CLASS.md`: as instruções dos orquestradores são as do nível ([Grão-Mestre](../orquestracao/grao-mestre.md#instruções-do-sistema), [Mestre da mesa](../orquestracao/mestre-da-mesa.md#instruções-do-sistema)); sem hooks extras nem skills de classe; sem App próprio (o GitHub de cada nível está em [identidades e Apps](../github/identidades-e-apps.md)).
- Os padrões dos campos ausentes são os do [formato](formato-das-permissoes.md#o-arquivo); as [regras fixas](permissoes.md#regras-fixas-de-segurança) valem por cima, como para todos.

### Camadas

- As regras efetivas de um orquestrador são `orquestrador` cortada pelo [teto da campanha](teto-do-workspace.md) e pela ficha dele, com os decretos de pessoas por cima ([precedência](../arquitetura/precedencia.md)). O teto e a ficha só restringem; nada amplia a classe.
- Não aparece em `mestre class list` como classe editável, não tem escopo de pasta nem proposta: nenhum agente nem usuário a edita. O painel a mostra só para leitura.
- Mesmo nome reservado em todos os escopos: uma classe nova chamada `orquestrador` é recusada. Pedir para editá-la (`class edit`) é recusado com `built_in`; uma pasta com esse nome (vinda do repositório) não vira proposta nem aparece na listagem, e os usuários são avisados uma vez de que ela é ignorada.
- Quem pede a classe `orquestrador` à biblioteca recebe sempre a embutida: não há versão aprovada, pasta nem proposta a conferir.

### Na atenuação

- O motor confere a decisão de um orquestrador contra as regras efetivas dele no worktree de quem pediu: aprova só o que elas não negam ([salvaguardas](../orquestracao/cadeia-de-aprovacao.md#salvaguardas)). A conferência é a decisão das regras do aprovador sobre a ação do pedido, sem contar exceções pontuais dele: `allow` e `ask` passam, `deny` não (`Attenuates`, em `internal/classes`; a cadeia a chama na etapa 10). Conferir um padrão ("nada do que ele casa é negado") é da cadeia.
- Como `write: []`, nenhum orquestrador aprova escrita fora do worktree: esse pedido vai às pessoas.
- A ficha do orquestrador conta: o Mestre da mesa cuja ficha nega `npm install *` não aprova um `npm install x` da party, que sobe ao Grão-Mestre.

## Regras e erros

- Classe criada com o nome `orquestrador`, em qualquer escopo: recusada como nome em uso (`name_taken`).
- Decreto de pessoa que libera algo a um orquestrador vale como para qualquer personagem; um que tenta mudar a classe em si é recusado: o que muda é a regra efetiva, não a classe (a recusa chega com os decretos, na etapa 4 do M5: [CA-DEC-12](../telegram/decretos.md#critérios-de-aceite)).

## Critérios de aceite

- **CA-ORQ-01** · *rápida* — **Dado** uma campanha qualquer, **quando** a Cidadela monta as regras efetivas do Grão-Mestre e de um Mestre da mesa, **então** as duas vêm da classe `orquestrador` embutida, com `files.write` vazio, `commands.default` e `tools.default` em `ask`, `git.commit` e `request_push` em `deny`, e são iguais em duas campanhas com o mesmo teto.
- **CA-ORQ-02** · *rápida* — **Dado** as regras efetivas do Mestre da mesa e um personagem da mesa com `files.write: ["**"]`, **quando** o motor confere a atenuação de uma escrita fora do worktree e de `npm install x` no worktree do personagem, **então** a primeira não pode ser aprovada pelo Mestre da mesa (a classe não escreve) e a segunda pode.
- **CA-ORQ-03** · *rápida* — **Dado** a biblioteca de classes, **quando** se salva uma classe de campanha ou de mesa chamada `orquestrador`, **então** é recusada com `name_taken`; **e** a classe embutida não aparece entre as classes editáveis nem aceita proposta.
- **CA-ORQ-04** · *rápida* — **Dado** o Mestre da mesa, **quando** roda ele mesmo `echo ok` e `pip install x`, **então** o primeiro roda direto ("padrão: risco baixo") e o segundo vira pedido (`medio`).

## Depende de

- [Classes](classes.md) · [Permissões](permissoes.md) · [Teto da campanha](teto-do-workspace.md) · [Precedência](../arquitetura/precedencia.md)
