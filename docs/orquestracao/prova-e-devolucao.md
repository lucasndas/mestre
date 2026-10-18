# Prova e devolução

> **Assunto:** a prova de cada critério no `done`, o `done` que a classe exige (prova e verificação pelo motor), a devolução com causa e a pausa por "mesma falha repetida", e o pergaminho de revisão com achados de identificador estável · **Componente:** Motor · **Atualizado:** 2026-10-18

## Objetivo

Fazer a [missão](missoes.md) conferir o **conteúdo** do trabalho, não só o fim de cada etapa. Hoje o motor sabe que uma etapa terminou (o `done`) e o que mudou (o `git diff`), mas não se cada critério da issue foi provado, se o teste passou depois da última edição, nem se uma devolução está andando ou repetindo a mesma falha. Este documento dá ao [Mestre da mesa](mestre-da-mesa.md) a evidência que o aceite dele confere e ao usuário um "pronto" que vem com prova.

## Comportamento

Tudo aqui é do motor e vale **igual nos três provedores**: o `done` é um comando da CLI e a verificação roda no motor. Nada depende do `post_tool`, porque eventos informativos de hook vêm do terminal do personagem e não provam nada ([despachante](../provedores/despachante-de-hooks.md)).

### Prova por critério

- Os critérios da issue da missão (Dado/Quando/Então, escritos pelo Mestre da mesa) são numerados na ordem em que aparecem: `C1`, `C2`… O motor guarda a lista ao criar a missão.
- A etapa que prova um critério diz isso no `done`: `mestre done "<resumo>" --proof "C1=<prova>"` (a flag se repete; sintaxe em [comunicação](../cli/comunicacao.md#mestre-done)). A prova é texto curto: o teste, o comando com o resultado ou o caminho de uma captura no worktree.
- O motor grava a prova na execução da etapa e no [pergaminho da missão](pergaminhos-e-tomos.md#pergaminho-da-missão), e marca o critério na issue (App "Mestre"), com a linha de prova e de onde ela veio:
  - **declarada por <personagem>:** o agente afirmou;
  - **verificada pelo motor:** a mesma etapa passou na verificação abaixo.
- Critério sem prova fica desmarcado. O [aceite](mestre-da-mesa.md#o-pedido-que-chega-o-mestre-da-mesa-como-po) do Mestre da mesa e o "pronto" que o Grão-Mestre dá ao usuário leem esta marcação; a justificativa do agente continua sendo texto não verificado ([cadeia](cadeia-de-aprovacao.md)).

### O `done` que a classe exige

O frontmatter do `CLASS.md` ([classes](../classes/classes.md)) pode declarar:

```yaml
done_requires:
  proof: all       # all: cada critério da issue precisa de --proof nesta etapa; none (padrão)
  verify: true     # o motor roda a verificação do workspace antes de concluir; false (padrão)
```

- **`proof: all`:** um `done` sem prova para algum critério é recusado na hora (7 `proof_missing`, com os critérios que faltam); a etapa continua `running`. Um `done --return` não exige prova.
- **`verify: true`:** "a chegada é a verificação", como na [etapa `sync`](sincronia-com-a-base.md#a-chegada-é-a-verificação). O `done` não conclui a etapa sozinho: o motor roda os [comandos de verificação](../sessao-zero/workspace.md#comandos-de-verificação) num worktree efêmero da mesa, na ponta da branch, como `mestre-agente` e sob a trava por repositório, e então:
  - **verde:** a etapa conclui e o bastão passa; as provas desse `done` ficam "verificadas pelo motor";
  - **vermelha:** a etapa continua `running` com o mesmo personagem, que recebe na caixa de entrada um item `system` com o comando que falhou e o fim da saída (sem contar volta: quem corrige é ele mesmo).
- Enquanto a verificação roda, o `done` volta na hora com `verification: "pending"` e o tempo de etapa travada (`stuck_after`) fica parado.
- Alteração não commitada com `verify: true`: recusado com 7 `uncommitted_changes`, com os arquivos, porque a verificação olha a branch e não o worktree.
- Workspace sem comando de verificação confirmado: a etapa conclui marcada **não verificada**, o Mestre da mesa é avisado e nenhuma prova dela conta como verificada. Não trava o trabalho; o PR do épico já nasce `human_required` nesse caso ([comandos de verificação](../sessao-zero/workspace.md#comandos-de-verificação)).

### Devolução com causa

- A devolução pode dizer o que falhou: `mestre done "<resumo>" --return "<motivo>" --cause <classe> --where <arquivo:linha>` (até 3 `--where`). Classes: `test`, `build`, `lint`, `conflict`, `network`, `auth`, `quota`, `other`.
- O motor calcula a **impressão da falha**: os 12 primeiros hexadecimais do SHA-256 de `etapa | classe | os --where` (caminhos relativos ao worktree, sem repetidos, em ordem). Quando a falha vem de uma verificação do motor (`sync` ou `verify: true`), a classe é `verify` e os `--where` são os 3 primeiros `arquivo:linha` da saída que existem no worktree.
- **Mesma falha repetida:** devoluções **seguidas** da mesma etapa com a mesma impressão contam juntas; ao chegar a `same_failure` (campo da missão, padrão 3), a devolução não acontece, a missão vai para `paused` com o motivo "mesma falha repetida" e o Mestre da mesa recebe o evento `return_limit` com a impressão, as tentativas e os `--where`, antes de gastar as voltas que sobram de `max_returns` ([devolução com limite](missoes.md#devolução-com-limite)). Uma impressão diferente zera essa contagem (houve progresso); `max_returns` continua contando tudo.
- Devolução sem `--cause` não tem impressão e só conta para `max_returns`.

### Pergaminho de revisão

- Missão com alguma etapa que devolve (`can_return_to`) ganha, ao ser criada, o pergaminho **"Revisão: <missão>"**, conectado aos personagens da etapa que devolve e das etapas para onde ela devolve ([pergaminhos](pergaminhos-e-tomos.md)).
- Ele nasce com a tabela que o revisor mantém; o identificador de um achado nunca muda nem é reaproveitado:

  ```markdown
  | ID | Etapa dona | Classe | Onde | Achado | Estado |
  |---|---|---|---|---|---|
  | R1 | dev | Bug | `src/login.go:40` | aceita senha vazia | aberto |
  ```

  Estados: `aberto`, `corrigido` (com `arquivo:linha`), `não corrigido`, `não se aplica mais`. Classes: `Gap`, `Bug`, `Verification miss`, `Scope drift`, `Over-engineering` (nunca bloqueia sozinho).
- Na segunda rodada em diante, o item `quest` da etapa que revisa traz o conteúdo atual do pergaminho de revisão antes das instruções, com a ordem de conferir cada achado `aberto` ou `não corrigido` antes de qualquer revisão nova; o da etapa que recebe a devolução traz os achados dela. Quem edita usa `mestre note edit --expect-rev`, para não apagar a edição de outro.
- O motor não interpreta a tabela: ele cria, conecta e entrega. A disciplina de revisão é da classe ([classes](../classes/classes.md)).

## Regras e erros

- `--proof` com critério que a issue não tem: 4 `not_found`, e o `done` não é registrado. Prova acima de 500 caracteres (padrão): 8 `too_large`.
- `--cause` fora da lista ou mais de 3 `--where`: 2 `usage`. `--cause` e `--where` sem `--return`: 2 `usage`.
- Uma verificação por etapa de cada vez: um segundo `done` durante a verificação sai com 7 `verification_running`.
- A verificação que estoura o `timeout` do comando conta como vermelha, com o motivo "tempo esgotado".
- `done_requires` desconhecido ou com valor fora da lista recusa a classe ao salvar ([classes](../classes/classes.md)).

## Critérios de aceite

- **CA-PROVA-01** · *rápida* — **Dado** uma missão cuja issue tem os critérios C1 a C3 e o GitHub falso, **quando** a etapa `qa` roda `mestre done "ok" --proof "C1=login_test.go:TestVazia" --proof "C2=captura em docs/c2.png"`, **então** a issue tem C1 e C2 marcados com a prova e "declarada por" o personagem, C3 desmarcado e o pergaminho da missão com as duas provas; **dado** `--proof "C9=x"`, **então** sai com 4 `not_found` e nada é registrado.
- **CA-PROVA-02** · *rápida* — **Dado** uma classe com `done_requires: {proof: all}` e uma issue com C1 e C2, **quando** o personagem roda `mestre done "x" --proof "C1=t"`, **então** sai com 7 `proof_missing` citando C2 e a etapa continua `running`; **dado** `--return "y"`, **então** a devolução é aceita.
- **CA-PROVA-03** · *rápida* — **Dado** uma classe com `done_requires: {verify: true}` e o executor de verificação falso, **quando** o personagem dá `done` e a verificação sai vermelha, **então** o `done` volta com `verification: "pending"`, a etapa continua `running`, nenhuma volta é contada e o personagem recebe um item `system` com o comando e a saída; **dado** a verificação verde, **então** a etapa conclui e as provas desse `done` ficam "verificadas pelo motor".
- **CA-PROVA-04** · *rápida* — **Dado** `verify: true` e o git falso com um arquivo não commitado, **quando** o personagem dá `done`, **então** sai com 7 `uncommitted_changes` listando o arquivo e nenhuma verificação começa.
- **CA-PROVA-05** · *rápida* — **Dado** `verify: true` e um workspace sem comando de verificação confirmado, **quando** o personagem dá `done`, **então** a etapa conclui marcada "não verificada", o Mestre da mesa recebe o aviso e nenhuma prova dela fica "verificada pelo motor".
- **CA-PROVA-06** · *rápida* — **Dado** `max_returns: 5` e `same_failure: 3`, **quando** o Revisor devolve três vezes seguidas com `--cause test --where src/login.go:40`, **então** as duas primeiras voltam ao Dev e a terceira deixa a missão `paused` com "mesma falha repetida" e o evento `return_limit` com a impressão e o `--where`; **dado** a segunda com `--where src/login.go:52`, **então** a contagem recomeça e a terceira volta ao Dev.
- **CA-PROVA-07** · *rápida* — **Dado** três devoluções seguidas sem `--cause` e `max_returns: 5`, **quando** chegam, **então** todas voltam ao Dev e nenhuma pausa por "mesma falha repetida".
- **CA-PROVA-08** · *rápida* — **Dado** a missão Dev → Revisor com `can_return_to: dev`, **quando** é criada, **então** existe o pergaminho "Revisão: <missão>" com a tabela vazia, conectado ao Revisor e ao Dev; **quando** o Revisor recebe a segunda rodada, **então** o item `quest` dele começa pelo conteúdo do pergaminho de revisão e pela ordem de conferir os achados abertos.
- **CA-PROVA-09** · *completa* — **Dado** uma mesa com `verificacao.yaml` confirmado e um Dev (agente falso) com `verify: true` que deixa um teste quebrado, **quando** ele dá `done`, **então** o motor roda os comandos reais num worktree efêmero como `mestre-agente`, a etapa continua `running` com a saída do teste na caixa de entrada; **quando** ele corrige e dá `done` de novo, **então** a etapa conclui.

## Depende de

- [Missões](missoes.md)
- [Pergaminhos e tomos](pergaminhos-e-tomos.md)
- [CLI de comunicação](../cli/comunicacao.md)
- [Classes](../classes/classes.md)
- [Sessão zero do workspace](../sessao-zero/workspace.md)
- [Ações por nível](../github/acoes-por-nivel.md)
- [Sincronia com a base](sincronia-com-a-base.md)
