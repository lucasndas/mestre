# Prova e devolução

> **Assunto:** a prova de cada critério no `done`, o `done` que a classe exige (prova e verificação pelo motor), a devolução com causa e a pausa por "mesma falha repetida", e o pergaminho de revisão com achados de identificador estável · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Fazer a [missão](missoes.md) conferir o **conteúdo** do trabalho, não só o fim de cada etapa. Hoje o motor sabe que uma etapa terminou (o `done`) e o que mudou (o `git diff`), mas não se cada critério da issue foi provado, se o teste passou depois da última edição, nem se uma devolução está andando ou repetindo a mesma falha. Este documento dá ao [Mestre da mesa](mestre-da-mesa.md) a evidência que o aceite dele confere e ao usuário um "pronto" que vem com prova.

## Comportamento

Tudo aqui é do motor e vale **igual nos três provedores**: o `done` é um comando da CLI e a verificação roda no motor. Nada depende do `post_tool`, porque eventos informativos de hook vêm do terminal do personagem e não provam nada ([despachante](../provedores/despachante-de-hooks.md)).

### Campos que a missão ganha

Somam-se à [definição da missão](missoes.md#definição), com a mesma validação (inválido: 2 `invalid_quest`, [coordenação](../cli/coordenacao.md)):

| Campo | Significado |
|---|---|
| `issue` | número de uma issue do épico já criada pelo Mestre da mesa (`mestre issue create`); a missão a usa em vez de criar outra, e o motor só acrescenta o checklist das etapas e as sub-issues. Sem o campo, o motor cria a issue, como hoje |
| `same_failure` | quantas falhas seguidas com a mesma impressão pausam a missão (padrão 3; menor que 2 é inválido; acima de `max_returns` não tem efeito) |

### Prova por critério

- **Os critérios** são os itens `- [ ] C<n> <texto>` do corpo da issue da missão, escritos pelo Mestre da mesa ([o pedido que chega](mestre-da-mesa.md#o-pedido-que-chega-o-mestre-da-mesa-como-po)). O motor os lê no `quest run` e os congela no banco para a rodada: editar a issue depois só vale na próxima rodada. O escopo é a missão: duas missões do mesmo épico têm cada uma o seu C1.
- A etapa que prova um critério diz isso no `done`: `mestre done "<resumo>" --proof "C1=<prova>"` (a flag se repete; sintaxe em [comunicação](../cli/comunicacao.md#mestre-done)). A prova é texto curto: o teste, o comando com o resultado, ou o caminho de uma captura **commitada** na branch da mesa (a prova vai com o PR).
- O motor grava a prova na execução da etapa e no [pergaminho da missão](pergaminhos-e-tomos.md#pergaminho-da-missão), e marca o item na issue (App "Mestre"), com a linha de prova e de onde ela veio: **declarada por <personagem>** (o agente afirmou) ou **verificada pelo motor** (a mesma etapa passou na verificação abaixo).
- **Missão de correção** (aceite do Mestre da mesa, [aceite](mestre-da-mesa.md#o-pedido-que-chega-o-mestre-da-mesa-como-po)): uma missão nova com o campo `issue` igual não desmarca nada; só prova os itens que faltam, e o `criteria_unproven` do `mestre pr open` olha a marcação da issue, seja de que missão for.
- **Rodada** é cada execução da missão, do `quest run` ao `completed` (rodar de novo uma missão `completed` abre outra, [missões](missoes.md)); uma devolução repete a etapa dentro da mesma rodada e não desmarca nada. Dentro de uma rodada, a última prova de cada critério vale. Uma nova rodada desmarca todos.
- Critério sem prova fica desmarcado. O aceite do Mestre da mesa e o "pronto" que o Grão-Mestre dá ao usuário leem esta marcação; a justificativa do agente continua sendo texto não verificado ([cadeia](cadeia-de-aprovacao.md)).

### O `done` que a classe exige

O frontmatter do `CLASS.md` ([classes](../classes/classes.md)) pode declarar:

```yaml
done_requires:
  proof: all       # all: cada critério da missão precisa de prova na etapa (contam as dadas em `done` anteriores da mesma rodada); none (padrão)
  verify: true     # o motor roda a verificação do workspace antes de concluir; false (padrão)
```

- **`done_requires` vale só para o `done` que conclui a etapa.** A devolução (`done --return`, de qualquer classe, como o QA) não exige prova nem verificação: exige o motivo e, se quiser contar para a mesma falha, `--cause` e `--where`.
- **`proof: all`:** um `done` sem prova para algum critério é recusado (7 `proof_missing`, com os que faltam); a etapa continua `running`. Prova dada num `done` anterior da mesma rodada conta.
- **`verify: true`:** "a chegada é a verificação", como na [etapa `sync`](sincronia-com-a-base.md#a-chegada-é-a-verificação). O `done` não conclui a etapa sozinho: o motor roda os [comandos de verificação](../sessao-zero/workspace.md#comandos-de-verificação) num worktree efêmero da mesa, na ponta da branch na Cidadela, como `mestre-agente` e sob a trava por repositório, e então:
  - **verde:** a etapa conclui e o bastão passa; as provas desse `done` ficam "verificadas pelo motor";
  - **vermelha:** a etapa continua `running` com o mesmo personagem, que recebe na caixa de entrada um item `system` com o comando que falhou e o fim da saída (4 KiB, padrão). **Conta uma volta da etapa**, como na `sync`: passou de `max_returns`, a missão fica `paused` com "limite de voltas"; a mesma impressão repetida pausa antes (abaixo).
- Enquanto a verificação roda, o `done` volta na hora com `verification: "pending"` e o tempo de etapa travada (`stuck_after`) fica parado.
- **Não commitado** é arquivo rastreado modificado ou não rastreado e não ignorado. Com `verify: true`, o `done` que o tem é recusado com 7 `uncommitted_changes`, com os arquivos, porque a verificação olha a branch. Um `done` sem mudança nenhuma passa. Por isso a classe com `verify: true` precisa poder commitar: salvá-la com `git.commit` efetivo em `deny` é recusado.
- Workspace sem comando de verificação confirmado: a etapa conclui marcada **não verificada**, o Mestre da mesa é avisado e nenhuma prova dela conta como verificada. Não trava o trabalho; o PR do épico já nasce `human_required` nesse caso ([comandos de verificação](../sessao-zero/workspace.md#comandos-de-verificação)).
- **`done` na fila de saída** (Cidadela fora, [convenções](../cli/convencoes.md)): as conferências desta seção rodam quando a fila é entregue; uma recusa chega ao personagem como item `system` com o `op_id` do pedido, e a etapa continua `running`. A verificação só começa depois de a ponta da Torre chegar à Cidadela ([sincronização](mesas.md#worktree-por-máquina-e-sincronização)); se a integração for recusada, ela não começa e o Mestre da mesa é avisado.

### Devolução com causa e mesma falha

- A devolução pode dizer o que falhou: `mestre done "<resumo>" --return "<motivo>" --cause <causa> --where <arquivo:linha>` (até 3 `--where`). Causas: `test`, `build`, `lint`, `conflict`, `network`, `auth`, `quota`, `other`.
- **Impressão da falha:** os 12 primeiros hexadecimais do SHA-256 de `etapa | causa | os --where`. Os `--where` são normalizados: caminho relativo ao worktree com `/`, sem a coluna (`a.go:12:5` vira `a.go:12`), sem repetidos, em ordem. **Sem nenhum `--where`, não há impressão:** a devolução conta só para `max_returns`.
- **Na verificação do motor** (`sync` e `verify: true`), a causa é `verify` e os `--where` são os 3 primeiros, depois de normalizados e ordenados, dos trechos `<caminho>:<linha>` da saída (stdout e stderr) cujo caminho existe no worktree, com o prefixo do worktree efêmero retirado. Outros formatos de localização (como `File "x", line 12`) não são reconhecidos; sem nenhum trecho reconhecido, não há impressão. Como a linha muda quando o código acima muda, uma impressão nova também pode ser só o código andando: conta como progresso.
- **Mesma falha repetida:** devoluções ou verificações vermelhas **seguidas** da mesma etapa com a mesma impressão contam juntas; ao chegar a `same_failure`, a missão vai para `paused` com o motivo "mesma falha repetida" e o Mestre da mesa recebe o evento `return_limit` com a impressão, as tentativas e os `--where`, antes de gastar as voltas que sobram de `max_returns` ([devolução com limite](missoes.md#devolução-com-limite)). Uma impressão diferente, ou nenhuma, zera essa contagem; `max_returns` continua contando tudo.

### Pergaminho de revisão

- Missão com alguma etapa que devolve (`can_return_to`) ganha, ao ser criada, o pergaminho **"Revisão: <missão>"**, conectado aos personagens da etapa que devolve e das etapas para onde ela devolve ([pergaminhos](pergaminhos-e-tomos.md)).
- Ele nasce com a tabela que o revisor mantém; o identificador de um achado nunca muda nem é reaproveitado:

  ```markdown
  | ID | Etapa dona | Tipo | Onde | Achado | Estado |
  |---|---|---|---|---|---|
  | R1 | dev | Bug | `src/login.go:40` | aceita senha vazia | aberto |
  ```

  Estados: `aberto`, `corrigido` (com `arquivo:linha`), `não corrigido`, `não se aplica mais`. Tipos de achado: `Gap`, `Bug`, `Verification miss`, `Scope drift`, `Over-engineering` (nunca bloqueia sozinho).
- Da segunda passada da etapa que revisa em diante (depois de uma devolução), o item `quest` dela traz o conteúdo atual do pergaminho de revisão antes das instruções, com a ordem de conferir cada achado `aberto` ou `não corrigido` antes de qualquer revisão nova; o da etapa que recebe a devolução traz os achados dela. Quem edita usa `mestre note edit --expect-rev`, para não apagar a edição de outro.
- O motor não interpreta a tabela: ele cria, conecta e entrega. A disciplina de revisão é da classe ([classes](../classes/classes.md)).

### No código

- **Pacote:** `internal/orchestration/quests` (`proofs.go`, `checks.go`, `review.go`, `proofstore.go`); o `done_requires` é lido do `CLASS.md` por `internal/classes` (`Meta.Done`). Tabelas `quest_criteria` (congeladas no `quest run`, com a marca de quais itens o motor marcou na issue), `quest_proofs` (a última prova de cada critério por rodada) e `step_failures` (impressão e contagem seguidas da etapa).
- **A issue do Mestre da mesa:** o motor não a reescreve. A lista de etapas vai entre `<!-- mestre:etapas -->` e `<!-- /mestre:etapas -->`, e cada critério com prova ganha `[x]` e uma linha `  - prova (declarada por <personagem>): <texto>`. Uma nova rodada desmarca só o que o motor marcou; os itens marcados por outra missão ou por uma pessoa ficam.
- **Verificação:** `Options.Verifier` (`verify.go`) é a porta do executor (`Confirmed` e `Verify`); o `done` de uma classe com `verify: true` volta com `verification: "pending"` e o resultado chega depois (`Service.WaitVerifications` espera as que rodam). Sem executor ou sem comando confirmado a etapa conclui com `verification: "unverified"`. O tempo de uma verificação não conta para a etapa travada.
- Um `done` recusado por `proof_missing` guarda as provas que trouxe (valem para o `done` seguinte da rodada); recusado por `not_found`, `too_large` ou `usage`, não guarda nada.

## Regras e erros

- `--proof` com critério que a missão não tem: 4 `not_found`, e o `done` não é registrado. Prova acima de 500 caracteres (padrão): 8 `too_large`. `--proof` junto de `--return`, ou numa tarefa sem missão: 2 `usage`.
- Texto com cara de segredo em `--proof`, `--return` ou no resumo: 3 `secret_detected`, como nos demais comandos ([convenções da CLI](../cli/convencoes.md)); a prova vai para a issue e para o pergaminho.
- `--cause` fora da lista, mais de 3 `--where`, ou `--cause`/`--where` sem `--return`: 2 `usage`.
- Uma verificação por etapa de cada vez: um segundo `done` durante a verificação sai com 7 `verification_running`.
- A verificação que estoura o `timeout` do comando conta como vermelha, com o motivo "tempo esgotado".
- `done_requires` desconhecido, com valor fora da lista, ou `verify: true` com `git.commit` efetivo em `deny`: a classe é recusada ao salvar ([classes](../classes/classes.md)).
- Issue sem nenhum item `C<n>`: a missão roda sem critérios; `proof: all` não exige nada e o aceite do Mestre da mesa não tem o que conferir (ele avisa o Grão-Mestre).

## Critérios de aceite

- **CA-PROVA-01** · *rápida* — **Dado** uma missão cuja issue tem os itens C1 a C3 e o GitHub falso, **quando** a etapa `qa` roda `mestre done "ok" --proof "C1=login_test.go:TestVazia" --proof "C2=captura em docs/c2.png"`, **então** a issue tem C1 e C2 marcados com a prova e "declarada por" o personagem, C3 desmarcado e o pergaminho da missão com as duas provas; **dado** `--proof "C9=x"`, **então** sai com 4 `not_found` e nada é registrado.
- **CA-PROVA-02** · *rápida* — **Dado** uma classe com `done_requires: {proof: all}` e uma missão com C1 e C2, **quando** o personagem roda `mestre done "x" --proof "C1=t"`, **então** sai com 7 `proof_missing` citando C2 e a etapa continua `running`; **quando** roda depois `mestre done "y" --proof "C2=u"`, **então** é aceito, porque C1 já tem prova na rodada.
- **CA-PROVA-03** · *rápida* — **Dado** uma classe com `done_requires: {verify: true}` e o executor de verificação falso, **quando** o personagem dá `done` e a verificação sai vermelha, **então** o `done` volta com `verification: "pending"`, a etapa continua `running`, uma volta é contada e o personagem recebe um item `system` com o comando e a saída; **dado** a verificação verde, **então** a etapa conclui e as provas desse `done` ficam "verificadas pelo motor".
- **CA-PROVA-04** · *rápida* — **Dado** `verify: true` e o git falso com um arquivo não rastreado e não ignorado, **quando** o personagem dá `done`, **então** sai com 7 `uncommitted_changes` listando o arquivo e nenhuma verificação começa; **dado** nenhuma mudança, **então** a verificação começa.
- **CA-PROVA-05** · *rápida* — **Dado** `verify: true` e um workspace sem comando de verificação confirmado, **quando** o personagem dá `done`, **então** a etapa conclui marcada "não verificada", o Mestre da mesa recebe o aviso e nenhuma prova dela fica "verificada pelo motor".
- **CA-PROVA-06** · *rápida* — **Dado** `max_returns: 5` e `same_failure: 3`, **quando** o Revisor devolve três vezes seguidas com `--cause test --where src/login.go:40`, **então** as duas primeiras voltam ao Dev e a terceira deixa a missão `paused` com "mesma falha repetida" e o evento `return_limit` com a impressão e o `--where`; **dado** a segunda devolução com `--where src/login.go:52` e a terceira de novo com `:40`, **então** a contagem recomeça na segunda e a terceira volta ao Dev.
- **CA-PROVA-07** · *rápida* — **Dado** três devoluções seguidas sem `--cause`, ou com `--cause test` e sem `--where`, e `max_returns: 5`, **quando** chegam, **então** todas voltam ao Dev e nenhuma pausa por "mesma falha repetida".
- **CA-PROVA-08** · *rápida* — **Dado** a missão Dev → Revisor com `can_return_to: dev`, **quando** é criada, **então** existe o pergaminho "Revisão: <missão>" com a tabela vazia, conectado ao Revisor e ao Dev; **quando** o Revisor recebe a segunda rodada, **então** o item `quest` dele começa pelo conteúdo do pergaminho de revisão e pela ordem de conferir os achados abertos.
- **CA-PROVA-09** · *pesada* — **Dado** uma mesa com `verificacao.yaml` confirmado e um Dev (agente falso) com `verify: true` que deixa um teste quebrado, **quando** ele dá `done`, **então** o motor roda os comandos reais num worktree efêmero (a conta em que rodam é conferida nas [contas reais](../qualidade/contas-reais.md)), a etapa continua `running` com a saída do teste na caixa de entrada; **quando** ele corrige e dá `done` de novo, **então** a etapa conclui (MVP-16).
- **CA-PROVA-10** · *rápida* — **Dado** `verify: true`, `same_failure: 3` e o executor falso que falha três vezes com a mesma saída `src/a.go:7: x`, **quando** o personagem dá `done` três vezes, **então** a missão fica `paused` com "mesma falha repetida" na terceira e o Mestre da mesa recebe o `return_limit`.
- **CA-PROVA-11** · *rápida* — **Dado** uma missão com `issue: 41` e a issue #41 com os itens C1 e C2, **quando** a missão roda e alguém edita a issue acrescentando C3, **então** a rodada continua com C1 e C2, nenhuma issue nova é criada e a próxima rodada passa a ter C3.
- **CA-PROVA-12** · *rápida* — **Dado** uma classe com `done_requires: {verify: true}` e `git.commit` efetivo em `deny`, **quando** é salva, **então** é recusada; **dado** `mestre done "x" --return "y" --proof "C1=t"`, **então** sai com 2 `usage`.
- **CA-PROVA-13** · *rápida* — **Dado** uma classe QA com `done_requires: {proof: all, verify: true}` e uma missão com C1 e C2, **quando** ela roda `mestre done "x" --return "bug" --cause test --where src/a.go:7` sem prova e com arquivo não commitado, **então** a devolução é aceita, nenhuma verificação roda e conta uma volta.
- **CA-PROVA-14** · *rápida* — **Dado** uma verificação em andamento, **quando** chega um segundo `done`, **então** sai com 7 `verification_running`; **dado** o comando que estoura o `timeout`, **então** conta como vermelha com "tempo esgotado"; **dado** uma prova de 501 caracteres, **então** sai com 8 `too_large`; **dado** `--cause urgente`, **então** sai com 2 `usage`.
- **CA-PROVA-15** · *rápida* — **Dado** `--where ./a.go:12:5` e `a.go:12`, **quando** a impressão é calculada, **então** as duas são a mesma; **dado** o Cidadela fora e um `done` na fila de saída com `--proof "C9=x"`, **quando** a fila é entregue, **então** a recusa chega como item `system` com o `op_id` e a etapa segue `running`; **dado** uma issue sem nenhum `C<n>`, **então** a missão roda sem critérios e o Mestre da mesa avisa o Grão-Mestre.

## Depende de

- [Missões](missoes.md)
- [Pergaminhos e tomos](pergaminhos-e-tomos.md)
- [CLI de comunicação](../cli/comunicacao.md)
- [CLI de coordenação](../cli/coordenacao.md)
- [Classes](../classes/classes.md)
- [Sessão zero do workspace](../sessao-zero/workspace.md)
- [Mesas](mesas.md)
- [Ações por nível](../github/acoes-por-nivel.md)
- [Sincronia com a base](sincronia-com-a-base.md)
