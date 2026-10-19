# Teste de classe

> **Assunto:** `mestre class test`: casos que a classe traz no pacote, rodados com o provedor real, efeitos esperados e proibidos, várias execuções com limiar, resultado por versão da classe e do CLI, custo e o que acontece quando um CLI muda · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Deixar o usuário saber se uma classe **faz o que diz** em cada provedor: se a classe `qa` dele só escreve testes, se o Dev dá `done` com a prova. O que os efeitos não expressam (como se uma skill foi usada) fica fora. O [teste de conformidade](conformidade.md) prova que o **sistema** funciona (instruções chegam, hooks disparam, `pre_tool` nega) com uma classe temporária; o [`class check`](../classes/classes.md#validação-mestre-class-check) valida o pacote sem rodar o agente. Nenhum dos dois roda a classe do usuário.

## Comportamento

### Os casos

Ficam no pacote da classe, em `cases/<nome>.yaml` (contam no limite de arquivos do pacote e o `class check` os valida, [classes](../classes/classes.md#estrutura-do-pacote)):

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
timeout: 10m                  # por execução (padrão 10m)
```

- `changed` é um glob sobre os arquivos alterados no fim (o `git diff` do worktree e os não commitados), **sem** os que o motor monta para o personagem (camada do Mestre, skills, hooks e confiança, [sessão zero do personagem](../sessao-zero/personagem.md)). `mestre` é um comando da CLI que o personagem chamou (`done`, `ask`, `note`…), sem contar o `mestre hook`; sem `exit`, vale só o que saiu com 0. `attempt` é um comando ou ferramenta que o personagem tentou, aceito ou negado.
- **De onde vem cada efeito.** `changed` e `mestre` vêm do que o motor sabe por conta própria: o repositório e as chamadas da CLI identificadas pelo [canal local](../arquitetura/canal-local.md). `attempt` vem dos pedidos `pre_tool` ao despachante, que o agente poderia forjar ([despachante](despachante-de-hooks.md)): por isso `attempt` só vale em `forbid`, onde um evento forjado só faria o caso falhar. O contrário, um evento que **faltou**, não pode fazer o caso passar: se o despachante respondeu em modo degradado durante a execução, o `forbid` de `attempt` fica **não verificável** e a execução não conta como passada.
- **Uma execução sem `done` dentro do `timeout` falha**, mesmo que nenhum `forbid` tenha acontecido: um caso só com `forbid` não passa com o agente parado.
- **`seed`:** cada caminho é relativo ao repositório de teste, sem `..`, sem caminho absoluto nem link e fora de `.git/` e `.mestre/`; o motor grava os arquivos por `safefile`. **Tetos** (padrão): `runs` até 10, `timeout` até 30m, 20 casos por classe e 64 KiB de `seed` por caso; uma classe da mesa nasce sem aprovação, então o `class check` recusa o que passa disso, porque cada execução gasta cota.
- Caso inválido (campo desconhecido, `attempt` em `expect`, `pass_at` maior que `runs`, glob malformado) é erro do `class check`, e a classe não é salva.

### A rodada

- `mestre class test "<classe>" [--provider claude-code|codex|antigravity]… [--case <nome>]` ([CLI de classes](../cli/coordenacao-classes-e-rituais.md)) volta na hora com um `run_id` (o identificador da rodada, que não é o `op_id` da fila de saída); o resultado chega na caixa de entrada de quem pediu como item `system`. Pelo painel, Administrador ou acima pede o mesmo na tela da classe ([administração](../painel/administracao.md)), e o resultado chega a ele como aviso do painel ([avisos](../painel/conversa-e-aprovacoes.md#avisos)).
- **Só a versão aprovada** da classe roda (a do hash registrado, [classes](../classes/classes.md#onde-a-classe-é-alterada)): hooks e skills de uma proposta ainda não aprovada não executam em máquina nenhuma. O pedido de aprovação de uma versão nova mostra o resultado da versão aprovada e "proposta não testada".
- Sem `--provider`, roda em cada provedor que tem ficha da classe no workspace. Para cada provedor, o motor escolhe uma ficha da classe e uma máquina **conforme** ([conformidade](conformidade.md#resultado)), como num recrutamento; quem paga é o perfil de conta dessa ficha.
- **Preparação, como na conformidade:** repositório de teste numa pasta própria, com espelho e worktree, o `seed` no primeiro commit, e um personagem com a classe testada, a camada do Mestre, os hooks e as regras efetivas de verdade. O personagem recebe o `prompt` como item `agent`, que é uma tarefa ativa ([`done`](../cli/comunicacao.md#mestre-done)). GitHub e Telegram são os dublês: nada sai para fora.
- **Execuções:** cada execução começa do zero (repositório e sessão novos). O caso passa num provedor quando ao menos `pass_at` das `runs` têm todos os `expect` e nenhum `forbid`. Não há execução extra além das declaradas: a variação do modelo é tratada pelo limiar, não por nova tentativa (a mesma regra da conformidade).
- **Entre provedores:** `changed` e `mestre` são iguais nos três, porque vêm do motor; `attempt` depende de o `pre_tool` chegar, que existe nos três ([mapa de eventos](adaptador.md#mapa-de-eventos)). O que muda é o que a classe consegue fazer em cada um: um caso que depende de subagente falha no Antigravity enquanto lá vale o `deny_subagent` ([conformidade](conformidade.md#resultado)), e o resultado diz isso.

### Resultado

- Fica registrado por classe, **hash da versão** da classe, provedor, versão do CLI, máquina e perfil (`class_test_results`, [persistência](../arquitetura/persistencia.md)): cada caso com quantas execuções passaram, os `expect` que faltaram, os `forbid` que aconteceram ou ficaram não verificáveis, a duração, o uso e a transcrição.
- O `class check` e a tela da classe mostram uma linha por provedor: "testada no Claude Code 2.1.299: 4 de 4 casos", "Codex: não testada", "Antigravity: testada em versão anterior".

### Custo

- Cada execução gasta cota real do perfil de conta da ficha escolhida. O uso entra no [registro de uso](../plataforma/limites-de-uso.md) como o da conformidade, atribuído ao workspace da classe e marcado "teste de classe", e conta para o **limite diário do workspace** como qualquer trabalho (a regra dos 100% vale igual).
- Antes de cada execução, o motor confere o limite diário do workspace e o [limiar de início](../plataforma/limites-de-uso.md#limiar-de-início) do provedor e conta: atingido, a rodada para ("limite de custo" ou "aguardando cota") e o resultado parcial fica guardado. Sem limite diário configurado, só o limiar de janela vale, como para os personagens.

### Quando um CLI muda

Quando a versão de um CLI muda num perfil e a conformidade passa ([atualização dos CLIs](atualizacao-dos-clis.md)), os resultados das classes naquele provedor passam a "testada em versão anterior", e a tela da classe e o `class check` mostram o botão ou a dica **Testar de novo**. A rodada não é automática, porque gasta cota.

## Regras e erros

- Classe sem `cases/`: `mestre class test` sai com 4 `not_found` ("a classe não tem casos"). Limite diário do workspace já atingido no pedido: 8 `limit`.
- Nenhuma máquina conforme para o provedor pedido: aquele provedor fica "não testado" com o motivo; os outros rodam.
- Uma rodada por classe de cada vez: outro pedido sai com 7 `conflict` e o `run_id` da que está rodando.
- O repositório, a sessão e o personagem de teste são apagados no fim, como na conformidade; a transcrição fica com o resultado.
- Quem pode pedir: o Mestre da mesa (classes da mesa) e o Grão-Mestre (classes do workspace) pela CLI; pelo painel, Administrador ou acima. Classe nenhuma pode liberar `class test` a um personagem, porque gasta cota.

## Critérios de aceite

- **CA-CLTEST-01** · *rápida* — **Dado** um caso com `forbid: [{changed: "src/**"}]`, `runs: 3`, `pass_at: 2` e o agente falso que altera `src/a.go` numa das três execuções e dá `done` nas três, **quando** a rodada termina, **então** o caso passa (2 de 3) e o resultado lista o `forbid` que aconteceu na execução que falhou.
- **CA-CLTEST-02** · *rápida* — **Dado** um caso com `expect: [{mestre: done}]` e o agente falso que só escreve no terminal "pronto" sem chamar a CLI, **quando** a execução termina, **então** o `expect` falta, mesmo que o despachante tenha recebido um `post_tool` dizendo que o `done` rodou.
- **CA-CLTEST-03** · *rápida* — **Dado** um caso com `attempt` em `expect`, **quando** a classe é salva, **então** o `class check` aponta o erro e a classe não é salva.
- **CA-CLTEST-04** · *rápida* — **Dado** o limite diário do workspace atingido depois da primeira execução, **quando** a rodada vai começar a segunda, **então** ela para com "limite de custo" e a primeira fica guardada; **dado** o limite já atingido no pedido, **então** `mestre class test` sai com 8 `limit`.
- **CA-CLTEST-05** · *rápida* — **Dado** um caso com `seed` em `../x`, `/etc/x`, `.git/config`, `.mestre/x` ou um link, ou com `runs: 11`, `timeout: 31m`, 21 casos ou um `seed` de 65 KiB, **quando** o `class check` roda, **então** aponta o erro e a classe não é salva.
- **CA-CLTEST-06** · *completa* — **Dado** uma classe `qa` com um caso e o agente falso de cada provedor, **quando** `mestre class test "qa"` roda, **então** volta na hora com o `run_id`, cada provedor roda numa pasta própria que é apagada no fim, e o resultado chega como item `system` com uma linha por provedor.
- **CA-CLTEST-07** · *pesada* — **Dado** Claude Code, Codex e Antigravity reais e uma classe `qa` com um caso que exige `done` e proíbe alterar `src/**`, **quando** a rodada termina, **então** há um resultado por provedor com o hash da classe e a versão do CLI, e o `class check` mostra as três linhas (MVP-6).
- **CA-CLTEST-08** · *rápida* — **Dado** um caso só com `forbid` e o agente falso que não faz nada até o `timeout`, **quando** a execução termina, **então** ela falha; **dado** o despachante em modo degradado durante uma execução com `forbid` de `attempt`, **então** o resultado marca o `forbid` como não verificável e a execução não passa.
- **CA-CLTEST-09** · *rápida* — **Dado** uma proposta de versão nova da classe aguardando aprovação, **quando** alguém pede `mestre class test`, **então** roda a versão aprovada, nenhum hook ou skill da proposta executa e o pedido de aprovação mostra "proposta não testada"; **dado** um Aprovador no painel, **quando** chama a rota de testar, **então** o servidor recusa com o nível exigido.
- **CA-CLTEST-10** · *rápida* — **Dado** uma classe sem `cases/`, **quando** alguém roda `mestre class test`, **então** sai com 4 `not_found`; **dado** uma rodada em andamento da mesma classe, **então** um segundo pedido sai com 7 `conflict` e o `run_id` da que roda.
- **CA-CLTEST-11** · *rápida* — **Dado** nenhuma máquina conforme para o Codex, **quando** `mestre class test --provider codex` roda junto com `claude-code`, **então** o Codex fica "não testado" com o motivo e o Claude Code roda.
- **CA-CLTEST-12** · *rápida* — **Dado** uma classe com `cli.allow: ["class test"]`, **quando** o `class check` roda, **então** recusa; **dado** um personagem sem o direito, **quando** chama `mestre class test`, **então** recebe erro de permissão.
- **CA-CLTEST-13** · *rápida* — **Dado** um caso com `forbid: [{changed: "**"}]` e o agente falso que só recebe a camada do Mestre, as skills e os hooks que o motor monta, **quando** a execução termina, **então** `changed` não vê esses arquivos e o `forbid` não acontece.

## Depende de

- [Teste de conformidade](conformidade.md)
- [Classes](../classes/classes.md)
- [CLI de classes e rituais](../cli/coordenacao-classes-e-rituais.md)
- [Limites de uso](../plataforma/limites-de-uso.md)
- [Administração](../painel/administracao.md)
- [Conversa e aprovações](../painel/conversa-e-aprovacoes.md)

## A confirmar

- **Juiz:** um avaliador por modelo, com rubrica de passa ou não passa, só sobre o resumo do `done`, para o que os efeitos não pegam (o relatório diz a verdade?). Fica de fora até haver casos de efeito rodando e se saber quanto o juiz custa e quanto ele varia.
