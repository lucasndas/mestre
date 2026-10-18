# Teste de classe

> **Assunto:** `mestre class test`: casos que a classe traz no pacote, rodados com o provedor real, efeitos esperados e proibidos, várias execuções com limiar, resultado por versão da classe e do CLI, custo e o que acontece quando um CLI muda · **Componente:** Motor · **Atualizado:** 2026-10-18

## Objetivo

Deixar o usuário saber se uma classe **faz o que diz** em cada provedor: se a classe `qa` dele só escreve testes, se a skill dela dispara no Codex, se o Dev dá `done` com a prova. O [teste de conformidade](conformidade.md) prova que o **sistema** funciona (instruções chegam, hooks disparam, `pre_tool` nega) com uma classe temporária; o [`class check`](../classes/classes.md#validação-mestre-class-check) valida o pacote sem rodar o agente. Nenhum dos dois roda a classe do usuário.

## Comportamento

### Os casos

Ficam no pacote da classe, em `cases/<nome>.yaml` (contam no limite de arquivos do pacote, [classes](../classes/classes.md#estrutura-do-pacote)):

```yaml
version: 1
prompt: "Corrija o login que aceita senha vazia e escreva o teste"
seed:                         # arquivos do repositório de teste, antes do primeiro commit
  src/login.go: |
    package login
    func Valida(s string) bool { return true }
expect:                       # o que precisa acontecer (todos)
  - {changed: "tests/**"}
  - {mestre: done}
forbid:                       # o que não pode acontecer (nenhum)
  - {changed: "src/**"}
  - {attempt: "git push*"}
runs: 3                       # execuções por provedor (padrão 3)
pass_at: 3                    # quantas precisam passar (padrão: todas)
```

- `changed` é um glob sobre os arquivos alterados no fim (o `git diff` do worktree e os não commitados); `mestre` é um comando da CLI que o personagem chamou (`done`, `ask`, `note`…), com o código de saída opcional (`{mestre: done, exit: 0}`); `attempt` é um comando ou ferramenta que o personagem tentou, aceito ou negado.
- **De onde vem cada efeito.** `changed` e `mestre` vêm do que o motor sabe por conta própria: o repositório e as chamadas da CLI identificadas pelo [canal local](../arquitetura/canal-local.md). `attempt` vem dos pedidos `pre_tool` ao despachante, que o agente poderia forjar ([despachante](despachante-de-hooks.md)): por isso `attempt` só vale em `forbid`, onde um evento forjado só faria o caso falhar, nunca passar.
- Um caso inválido (campo desconhecido, `pass_at` maior que `runs`, glob malformado) aparece no `class check` como erro e a classe não é salva.

### A rodada

- `mestre class test "<classe>" [--provider claude|codex|antigravity]… [--case <nome>]` ([CLI de classes](../cli/coordenacao-classes-e-rituais.md)) pede a rodada e volta na hora com o `run_id`; o resultado chega na caixa de entrada de quem pediu, como item `system`. Pelo painel, Administrador ou acima pede o mesmo na tela da classe ([administração](../painel/administracao.md)).
- Sem `--provider`, roda em cada provedor que tem ficha da classe no workspace. Para cada provedor, o motor escolhe uma máquina e um perfil de conta **conformes** ([conformidade](conformidade.md#resultado)), como num recrutamento.
- **Preparação, como na conformidade:** repositório de teste numa pasta própria, com espelho e worktree, o `seed` no primeiro commit, e um personagem com a classe testada (a versão aprovada, ou a proposta que está sendo aprovada), a camada do Mestre, os hooks e as regras efetivas de verdade. O personagem recebe o `prompt` como item `agent` e tem até `timeout` (padrão 10 min por execução) para dar `done`. GitHub e Telegram são os dublês: nada sai para fora.
- **Execuções:** cada execução começa do zero (repositório e sessão novos). O caso passa num provedor quando ao menos `pass_at` das `runs` têm todos os `expect` e nenhum `forbid`. Não há execução extra além das declaradas: a variação do modelo é tratada pelo limiar, não por nova tentativa (a mesma regra da conformidade).
- Tudo é igual nos três provedores, porque os efeitos vêm do motor. O que muda é o que a classe consegue fazer em cada um: um caso que depende de subagente falha no Antigravity enquanto lá vale o `deny_subagent` ([conformidade](conformidade.md#resultado)), e o resultado diz isso.

### Resultado

- Fica registrado por classe, **hash da versão** da classe, provedor, versão do CLI, máquina e perfil (`class_test_results`, [persistência](../arquitetura/persistencia.md)): cada caso com quantas execuções passaram, os `expect` que faltaram e os `forbid` que aconteceram, a duração, o custo e a transcrição.
- O `class check` e a tela da classe mostram uma linha por provedor: "testada no Claude Code 2.1.299: 4 de 4 casos", "Codex: não testada", "Antigravity: testada em versão anterior".
- **Pedido de aprovação de uma versão nova da classe** (proposta, [classes](../classes/classes.md#onde-a-classe-é-alterada)): o pedido mostra o resultado dos casos naquele hash, ou "não testada". É informação para quem aprova, não uma trava.

### Custo

- Cada execução gasta cota real do perfil de conta. O consumo entra no [registro de uso](../plataforma/limites-de-uso.md) do workspace como "teste de classe".
- Antes de cada execução, o motor confere o `cost.per_day` do [teto](../classes/teto-do-workspace.md) do workspace: se ele já foi atingido, a rodada para com o motivo "limite de custo" e o resultado parcial é guardado.

### Quando um CLI muda

Quando a versão de um CLI muda num perfil e a conformidade passa ([atualização dos CLIs](atualizacao-dos-clis.md)), os resultados dessa classe naquele provedor passam a "testada em versão anterior", e o [checklist](../plataforma/checklist.md) mostra um aviso com o botão **Testar de novo** para as classes com casos. A rodada não é automática, porque gasta cota.

## Regras e erros

- Classe sem `cases/`: `mestre class test` sai com 4 `not_found` ("a classe não tem casos").
- Nenhuma máquina conforme para o provedor pedido: aquele provedor fica "não testado" com o motivo; os outros rodam.
- Uma rodada por classe de cada vez: outro pedido sai com 7 `conflict` e o `run_id` da que está rodando.
- O repositório, a sessão e o personagem de teste são apagados no fim, como na conformidade; a transcrição fica com o resultado.
- Quem pode pedir: os mesmos de `class check` ([CLI de classes](../cli/coordenacao-classes-e-rituais.md)) e, pelo painel, Administrador ou acima.

## Critérios de aceite

- **CA-CLTEST-01** · *rápida* — **Dado** um caso com `forbid: [{changed: "src/**"}]`, `runs: 3`, `pass_at: 2` e o agente falso que altera `src/a.go` numa das três execuções, **quando** a rodada termina, **então** o caso passa (2 de 3) e o resultado lista o `forbid` que aconteceu na execução que falhou.
- **CA-CLTEST-02** · *rápida* — **Dado** um caso com `expect: [{mestre: done}]` e o agente falso que só escreve no terminal "pronto" sem chamar a CLI, **quando** a execução termina, **então** o `expect` falta, mesmo que o despachante tenha recebido um `post_tool` dizendo que o `done` rodou.
- **CA-CLTEST-03** · *rápida* — **Dado** um caso com `attempt` em `expect`, **quando** a classe é salva, **então** o `class check` aponta o erro e a classe não é salva.
- **CA-CLTEST-04** · *rápida* — **Dado** o teto com `cost.per_day` já atingido, **quando** uma rodada vai começar a segunda execução, **então** ela para com "limite de custo" e a primeira fica guardada.
- **CA-CLTEST-05** · *rápida* — **Dado** resultados no Claude Code 2.1.299 e a conformidade passando no 2.1.300, **quando** o motor registra a versão nova, **então** os resultados da classe nesse provedor ficam "testada em versão anterior" e o checklist mostra o aviso com **Testar de novo**, sem rodar nada.
- **CA-CLTEST-06** · *completa* — **Dado** uma classe `qa` com um caso e o agente falso de cada provedor, **quando** `mestre class test "qa"` roda, **então** volta na hora com o `run_id`, cada provedor roda numa pasta própria que é apagada no fim, e o resultado chega como item `system` com uma linha por provedor.
- **CA-CLTEST-07** · *pesada* — **Dado** Claude Code, Codex e Antigravity reais e uma classe `qa` com um caso que proíbe alterar `src/**`, **quando** a rodada termina, **então** há um resultado por provedor com o hash da classe e a versão do CLI, e o `class check` mostra as três linhas (MVP-6).

## Depende de

- [Teste de conformidade](conformidade.md)
- [Classes](../classes/classes.md)
- [CLI de classes e rituais](../cli/coordenacao-classes-e-rituais.md)
- [Teto do workspace](../classes/teto-do-workspace.md)
- [Limites de uso](../plataforma/limites-de-uso.md)
- [Administração](../painel/administracao.md)
- [Checklist](../plataforma/checklist.md)

## A confirmar

- **Juiz:** um avaliador por modelo, com rubrica de passa ou não passa, só sobre o resumo do `done`, para o que os efeitos não pegam (o relatório diz a verdade?). Fica de fora até haver casos de efeito rodando e se saber quanto o juiz custa e quanto ele varia.
