# Fluxo de PRs

> **Assunto:** tamanho de um PR, revisão antes da fila, a fila de merge do GitHub, o trem manual que ela substituiu, quem atualiza com a `main` e o que fazer quando o CI falha · **Componente:** Projeto · **Atualizado:** 2026-10-26

## Por que existe

O ruleset da `main` só aceita squash e exige o CI de Linux e Windows ([CI e ruleset](ambiente.md#ci-e-proteção-da-main)). Sem fila, cada merge deixa os outros PRs para trás e cada um atualiza e roda o CI de novo: no M5 foram mais de 200 PRs em poucos dias, e a maior parte do tempo foi espera de CI. Empresas grandes resolvem isso com uma **fila de merge com lotes**: os PRs aprovados entram num lote, o CI roda uma vez no lote sobre a `main`, e o lote entra inteiro. Desde o [M5+](ordem-de-implementacao.md#marcos) o repositório está na organização `mestre-ia` e usa a fila de merge do GitHub.

## Tamanho de um PR

- **Um PR por unidade coerente:** uma funcionalidade, ou um grupo de critérios do mesmo documento, ou uma peça da [ligação de produção](ligacao-de-producao.md) com o que ela precisa. Não um PR por comando, por critério ou por achado de revisão.
- **Correções de revisão vão no mesmo PR**, nunca num PR novo.
- **Dividir só quando as partes são independentes de verdade** (arquivos diferentes, nenhuma precisa da outra) e cada uma é grande; aí cada sessão leva uma parte.
- **Referência de tamanho:** até umas mil linhas de código fora testes e documentos; acima disso, dividir por assunto, não por arquivo.
- **Documentação e código juntos**, como pede a [definição de pronto](convencoes.md#definição-de-pronto).

## Revisão antes da fila

Um PR só entra na fila com a revisão independente aplicada (só leitura; Opus quando toca uma fronteira de segurança: permissões, credenciais, git, hooks, aprovações, cofre, rede) e a suíte rápida e a completa passando na máquina de quem fez. Sob carga alta na máquina, a rápida que falha só pelo orçamento de tempo, sem teste quebrado, conta como passou; o CI decide.

## A fila de merge

1. Com o PR revisado e o CI do PR verde, quem coordena o põe na fila: `gh pr merge <n> --squash --auto` (ou o botão **Merge when ready**).
2. O GitHub monta lotes de até 5 PRs (espera até 5 minutos por mais PRs antes de montar), cria a branch temporária do lote sobre a `main` do momento e roda nela o CI inteiro (`merge_group`): as duas verificações exigidas e o `race`, que não é exigido.
3. Lote verde: os PRs entram na `main`, um squash por PR, sem outro CI. Lote vermelho: o GitHub tira o PR que quebrou, avisa no PR e refaz o lote com os outros.
4. Um PR não precisa estar em dia com a `main` para entrar na fila; só precisa não ter conflito com ela.

Migrações com o mesmo número em dois PRs da mesma fila: o segundo entra em conflito; quem o fez renumera (`git mv`, sem lacunas) e o põe na fila de novo.

## Quem atualiza com a `main`

- **Ninguém atualiza o PR com a `main` só para rodar o CI de novo:** a fila roda o CI sobre a `main` no lote. Atualiza só para resolver um conflito.

## Quando o CI falha

- **Falha só por orçamento de tempo** (rápida ou completa acima do limite, nenhum teste quebrado): no PR, rodar de novo só o job que falhou (`gh run rerun <id> --failed`), até duas vezes; no lote, pôr o PR na fila de novo; se continuar, o orçamento ou o custo da suíte é um assunto à parte ([testes](../qualidade/testes.md)).
- **Teste quebrado no lote da fila:** o GitHub já tira o PR culpado; quem o fez corrige no mesmo PR e o põe na fila de novo. Se o culpado não for claro (dois PRs que só quebram juntos), quem coordena tira um deles da fila e o devolve.
- **Teste instável** (passa e falha sem mudança): não é motivo para tirar o PR; vira uma tarefa própria, com a causa e a correção, nunca um retry no teste.

## O trem manual (antes do M5+)

Até o M5, com o repositório na conta pessoal, a fila do GitHub não existia e quem coordenava fazia o mesmo à mão: um branch `train/<marco>-<n>` a partir da `main`, `git merge` de cada PR pronto, conflitos resolvidos no próprio trem, um PR só e os de origem fechados com "Merged as part of the merge train #N". Continua valendo quando a fila estiver fora do ar ou para juntar PRs que conflitam entre si.

## Ver também

[convenções](convencoes.md#branches-e-prs) (nomes de branch, título e merge com squash) · [testes](../qualidade/testes.md) (suítes e orçamentos) · [ambiente](ambiente.md#ci-e-proteção-da-main) (CI e ruleset)
