# Fluxo de PRs

> **Assunto:** tamanho de um PR, revisão antes da fila, trem de PRs, a fila de merge do GitHub do M5+, quem atualiza com a `main` e o que fazer quando o CI falha · **Componente:** Projeto · **Atualizado:** 2026-10-20

## Por que existe

O ruleset da `main` exige a branch em dia com a `main` antes do merge e só aceita squash ([CI e ruleset](ambiente.md#ci-e-proteção-da-main)). Cada merge deixa todos os outros PRs abertos para trás: cada um atualiza e roda o CI de novo (cerca de 10 minutos em Linux e Windows). Com muitos PRs pequenos ao mesmo tempo, o custo cresce com o quadrado do número de PRs: no M5 foram mais de 200 PRs em poucos dias, e a maior parte do tempo foi espera de CI, não trabalho.

Empresas grandes resolvem isso com uma **fila de merge com lotes** (o merge queue do GitHub, Bors, Mergify, Zuul): os PRs aprovados entram num lote, o CI roda uma vez no lote sobre a `main`, e o lote entra inteiro; se falhar, a ferramenta tira o culpado e roda de novo. O merge queue do GitHub não existe para repositório de conta pessoal, então o Mestre faz o mesmo à mão: o **trem**.

## Tamanho de um PR

- **Um PR por unidade coerente:** uma funcionalidade, ou um grupo de critérios do mesmo documento, ou uma peça da [ligação de produção](ligacao-de-producao.md) com o que ela precisa. Não um PR por comando, por critério ou por achado de revisão.
- **Correções de revisão vão no mesmo PR**, nunca num PR novo.
- **Dividir só quando as partes são independentes de verdade** (arquivos diferentes, nenhuma precisa da outra) e cada uma é grande; aí cada sessão leva uma parte.
- **Referência de tamanho:** até umas mil linhas de código fora testes e documentos; acima disso, dividir por assunto, não por arquivo.
- **Documentação e código juntos**, como pede a [definição de pronto](convencoes.md#definição-de-pronto).

## Revisão antes da fila

Um PR só entra num trem com a revisão independente aplicada (só leitura; Opus quando toca uma fronteira de segurança: permissões, credenciais, git, hooks, aprovações, cofre, rede) e a suíte rápida e a completa passando na máquina de quem fez. Sob carga alta na máquina, a rápida que falha só pelo orçamento de tempo, sem teste quebrado, conta como passou; o CI decide.

## O trem

Quem coordena junta os PRs prontos num trem quando há dois ou mais, ou quando um está pronto há mais de uma hora:

1. Cria `train/<marco>-<n>` a partir da `main` e faz `git merge` de cada branch, na ordem em que ficaram prontos.
2. Resolve os conflitos no próprio trem, mantendo as duas partes; migrações ganham a próxima numeração livre ali (`git mv`), sem lacunas.
3. Confere o resultado: `go build ./...`, `GOOS=windows go vet ./...`, docscheck e os testes dos pacotes que tiveram conflito.
4. Abre um PR do trem, com a lista dos PRs de origem e o que foi resolvido, e o põe na fila.
5. Depois do merge, fecha cada PR de origem com o comentário "Merged as part of the merge train #N".

Um PR com conflito grande, ou que pede renumeração que mexe em outro, fica fora do trem: quem o fez o prepara em cima do branch do trem e o empurra quando o trem entrar.

## Quem atualiza com a `main`

- **Enquanto espera o trem, ninguém atualiza o próprio PR com a `main`** só para rodar o CI de novo. Atualiza só se o PR ficar com conflito e o trem pedir.
- O trem é o único que fica em dia com a `main`: um CI por trem, não um por PR.

## Quando o CI falha

- **Falha só por orçamento de tempo** (rápida ou completa acima do limite, nenhum teste quebrado): rodar de novo só o job que falhou (`gh run rerun <id> --failed`), até duas vezes; se continuar, o orçamento ou o custo da suíte é um assunto à parte ([testes](../qualidade/testes.md)).
- **Teste quebrado no trem:** achar o PR culpado pelos merges do trem, tirá-lo (o trem é refeito sem ele) e devolvê-lo a quem o fez; o resto segue.
- **Teste instável** (passa e falha sem mudança): não é motivo para tirar o PR; vira uma tarefa própria, com a causa e a correção, nunca um retry no teste.

## Fila de merge do GitHub (M5+)

O trem manual é a ponte até a fila de merge do GitHub, que só existe para repositório público de uma organização (o plano Pro da conta pessoal não a libera). O [M5+](ordem-de-implementacao.md#marcos) faz a troca, depois da auditoria do M5:

1. **Dono:** cria uma organização gratuita no GitHub, transfere o repositório para ela (rulesets, secrets, workflows, issues e PRs vão junto; o endereço antigo redireciona) e garante que o `gh` e os tokens da máquina de desenvolvimento têm acesso à organização.
2. **Dono:** reinstala na organização os GitHub Apps que o repositório usa e confere os secrets dos workflows de contas reais.
3. **Um PR mecânico, sozinho na fila:** o caminho do módulo Go (`go.mod`, imports, `-ldflags`, ferramentas e links dos documentos) passa a ser o da organização; nada mais muda nesse PR.
4. **CI:** o `.github/workflows/ci.yml` passa a rodar também no evento `merge_group`, e o job de corrida (`race`) roda nele em vez de só no push da `main`.
5. **Ruleset da `main`:** troca "branch em dia antes do merge" por "exigir fila de merge" (squash, lotes de até 5 PRs, o CI de Linux e Windows como verificação do lote); o `tools/rulesetcheck` passa a esperar essa configuração ([CI e ruleset](ambiente.md#ci-e-proteção-da-main)).
6. **Este documento:** a seção do trem vira a da fila: um PR revisado entra com `gh pr merge --auto`, e o GitHub monta os lotes, roda o CI uma vez por lote e tira o PR que quebra.

Critério de pronto do M5+: um lote com dois PRs entra pela fila com uma só execução do CI, e a `main` continua protegida.

## Ver também

[convenções](convencoes.md#branches-e-prs) (nomes de branch, título e merge com squash) · [testes](../qualidade/testes.md) (suítes e orçamentos) · [ambiente](ambiente.md#ci-e-proteção-da-main) (CI e ruleset)
