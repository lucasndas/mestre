# Testes

> **Assunto:** política de testes: suítes, orçamentos de tempo, executor, testes afetados, quarentena e mapa do MVP · **Componente:** Projeto · **Atualizado:** 2026-10-12

## Objetivo

Manter os testes rápidos desde o primeiro dia. Tempo de execução é requisito: cada suíte tem orçamento, e o orçamento é verificado por máquina. Este documento é a **fonte única** da política de testes; os dublês (relógio, agente, Telegram, GitHub e rede falsos) ficam em [dubles.md](dubles.md).

## Comportamento

### Suítes

| Suíte | Nome no executor | Quando roda | Orçamento | O que contém |
|---|---|---|---|---|
| **rápida** | `fast` | a cada alteração e no gancho de pré-commit | **30 s** | a grande maioria: unitários Go, unitários e de componente do painel (Vitest), [verificação de docs](verificacao-de-docs.md). Sem rede real, sem disco lento, sem processos externos |
| **completa** | `full` | no PR, antes de juntar na branch principal (verificação obrigatória) | **3 min** por máquina | rápida + integração: Core e nó reais na mesma máquina, banco temporário, agente falso como processo num PTY real |
| **pesada** | `heavy` | sob demanda, toda noite (agendada) e quando a versão de um provedor muda | sem limite rígido, **medida** | ponta a ponta dos 12 critérios do MVP, testes de falha da autocura e [conformidade com os provedores reais](../provedores/conformidade.md) |

### Como as suítes são selecionadas

- **Go:** por *build tags*. Teste sem tag é da rápida. Integração usa `//go:build full` em arquivos `*_full_test.go`; ponta a ponta e falhas usam `//go:build heavy` em `*_heavy_test.go`. A completa compila com `-tags full`; a pesada com `-tags full,heavy`. Testes que dependem do Windows (ConPTY, serviço) somam `&& windows`.
- **Painel:** Vitest. A rápida roda `vitest run` (unitários e componentes com DOM simulado e servidor WebSocket falso). A pesada usa navegador real com Playwright (proposta) para o que o MVP-11 exige.
- **Onde rodam:** rápida na máquina de quem desenvolve; completa no CI do GitHub em Linux e em Windows, em paralelo, cada um com o seu orçamento; pesada no laboratório (abaixo).

### Executor `testrun`

Programa do repositório que roda as suítes e mede o tempo. Uso: `go run ./tools/testrun fast|full|heavy|affected|quarantine`.

1. Roda `go test -json -count=1` (sem cache de resultado, para medir de verdade) e `vitest run --reporter=json` em paralelo.
2. Mede o **tempo de parede** total, do início ao fim, com o cache de compilação já aquecido.
3. Se o total passar do orçamento, **falha** e mostra os **10 testes mais lentos** (nome, pacote, tempo).
4. **Sinaliza** todo teste da rápida acima de **100 ms**. Antes de sinalizar, roda o teste sozinho uma vez para descontar a disputa de CPU (só mede tempo; não é nova tentativa de aprovação). A sinalização é aviso na saída e no resumo do CI; não falha a suíte.
5. Na pesada, guarda o tempo de cada execução e avisa quando o total passa **20%** (padrão) da mediana das últimas 7.
6. Define `MESTRE_TEST_SUITE=fast|full|heavy` no ambiente dos testes (uso em [Regras e erros](#regras-e-erros)).

### Testes afetados

`testrun affected` roda só o que as alterações podem ter quebrado:
- **Go:** `go test ./...` **com** o cache de testes. O Go só roda de novo os pacotes cujo código, dependências, arquivos lidos ou variáveis de ambiente mudaram; o resto sai do cache. Esse é o mecanismo de "afetados" do Go, sem lista manual.
- **Painel:** `vitest related <arquivos alterados>`, com a lista vinda de `git diff --name-only HEAD` mais os arquivos novos (`git ls-files --others --exclude-standard`).
- **Docs:** a verificação de docs é um teste Go que lê `docs/`; o cache detecta a mudança e a roda de novo.
- Antes do commit roda a rápida inteira; antes do merge, a completa inteira.

### Quarentena

- Teste instável vai para `test/quarantine.yaml`: identificador do teste, link da issue (rótulo `flaky`), data de entrada e motivo.
- O executor pula esses testes (`go test -skip`, `exclude` do Vitest) e lista os pulados no fim.
- Prazo padrão: **14 dias**. Entrada vencida faz a completa falhar até o teste ser corrigido ou apagado.
- Toda noite, `testrun quarantine` roda cada teste da quarentena 20 vezes; quando passa todas, o executor avisa que ele pode sair.
- Para achar instáveis: toda noite a rápida roda com `-count=5 -shuffle=on` (Vitest com ordem embaralhada); resultado misto vira issue `flaky`.

### Mapa do MVP para a suíte pesada

O texto de cada critério está em [escopo-mvp.md](../produto/escopo-mvp.md); aqui fica só o teste que o cobre.

| Critério | Teste | Peças reais |
|---|---|---|
| MVP-1 | `TestMVP01_InstallAndAutoPair` | instalador Ubuntu e Windows, descoberta na rede local, CA |
| MVP-2 | `TestMVP02_SelfHealing` (+ testes de falha abaixo) | systemd, serviço Windows, Guardiões, Telegram, painel |
| MVP-3 | `TestMVP03_SessionZero` | GitHub, clone de terceiro sem o Mestre |
| MVP-4 | `TestMVP04_EpicToResult` | três provedores reais, Core e nó Windows, duas mesas em paralelo |
| MVP-5 | `TestMVP05_GitHubIdentities` | GitHub, navegador dedicado, criação de App |
| MVP-6 | `TestMVP06_ClassPermissions` | três provedores reais |
| MVP-7 | `TestMVP07_ApprovalPolicy` | Telegram, painel, GitHub |
| MVP-8 | `TestMVP08_PermanentDecree` | Telegram |
| MVP-9 | `TestMVP09_UnauthorizedUser` | Telegram |
| MVP-10 | `TestMVP10_AudioAndAttachments` | Telegram, whisper-server com modelo real |
| MVP-11 | `TestMVP11_PanelOverview` | navegador real (Playwright) |
| MVP-12 | `TestMVP12_RestartRestoresState` | reinício real do Core e dos nós |

**Testes de falha da autocura** (`TestChaos_*`): matar o motor, travar o processo, encher o disco, derrubar a rede e falhar uma atualização, no Core e no nó. O comportamento esperado está em [camadas.md](../autocura/camadas.md); os critérios desses testes citam `(MVP-2)`, e os de conformidade citam `(MVP-6)`.

**Laboratório:** Core numa VM Ubuntu LTS, nó numa VM Windows 11 ou superior, mesma rede local; conta pessoal de teste no GitHub com um repositório de teste; bot de teste do Telegram; contas de teste dos provedores, já logadas.

## Regras e erros

- **Proibido `sleep` em teste.** Código que depende de tempo recebe o relógio injetável ([dubles.md](dubles.md)). O linter (`forbidigo` no golangci-lint) recusa `time.Sleep`, `time.Now`, `time.After`, `time.NewTimer`, `time.NewTicker`, `time.AfterFunc` e `time.Since` fora do pacote do relógio. No painel, `vi.useFakeTimers()`; nada de esperar com `setTimeout`.
- **Rápida sem processos e sem rede:** com `MESTRE_TEST_SUITE=fast`, a camada única que inicia processos e a que abre conexões recusam com erro claro ("processo externo na suíte rápida: use o PTY falso"). Teste que precisa disso é da completa.
- **Paralelo e isolado:** todo teste Go chama `t.Parallel()` (linter `paralleltest`). Cada teste usa `t.TempDir()` para pastas e banco SQLite próprios, porta `127.0.0.1:0` quando houver rede real, e nenhum estado global. `t.Setenv` e `t.Chdir` não combinam com `t.Parallel()`: passe a configuração por parâmetro.
- **Um comportamento, um teste, no nível mais baixo possível.** Nada de testar a mesma regra em unitário, integração e ponta a ponta. Ponta a ponta só para os critérios MVP-1..12; a [verificação de docs](verificacao-de-docs.md) recusa critério *pesada* que não cite um MVP.
- **Rastreabilidade:** o teste que cobre um critério de aceite traz o identificador num comentário logo acima (ex.: `// CA-MESAS-03`), para ser achado com `grep`.
- **Sem nova tentativa:** o executor nunca roda de novo um teste que falhou; Vitest com `retry: 0`; o CI não tem "rerun" automático. Instável vai para a quarentena.
- Orçamento estourado não é resolvido aumentando o orçamento: muda-se o teste de suíte, ou ele é acelerado.

## Critérios de aceite

- **CA-TESTES-01** · *rápida* — **Dado** uma saída `go test -json` gravada cuja duração passa de 30 s, **quando** o `testrun fast` a processa, **então** ele sai com código diferente de zero e lista os 10 testes mais lentos em ordem decrescente de tempo.
- **CA-TESTES-02** · *rápida* — **Dado** um teste da rápida com 150 ms e outro com 50 ms, **quando** o `testrun` termina, **então** só o primeiro aparece como sinalizado e a suíte não falha por isso.
- **CA-TESTES-03** · *rápida* — **Dado** `test/quarantine.yaml` com um teste dentro do prazo e outro vencido, **quando** o `testrun full` monta a execução, **então** os dois são pulados e listados, e a suíte falha citando o vencido.
- **CA-TESTES-04** · *rápida* — **Dado** um teste que falha, **quando** o `testrun` roda, **então** ele executa esse teste uma única vez e informa a falha.
- **CA-TESTES-05** · *rápida* — **Dado** `MESTRE_TEST_SUITE=fast`, **quando** o código pede para iniciar um processo externo, **então** recebe o erro de processo externo na suíte rápida.
- **CA-TESTES-06** · *rápida* — **Dado** arquivos alterados do painel, **quando** o `testrun affected` roda, **então** ele chama `vitest related` só com esses arquivos.
- **CA-TESTES-07** · *rápida* — **Dado** sete execuções anteriores da pesada com mediana de 40 min, **quando** uma nova leva 50 min, **então** o `testrun heavy` avisa o aumento.

## Depende de

- [dubles.md](dubles.md)
- [verificacao-de-docs.md](verificacao-de-docs.md)
- [escopo-mvp.md](../produto/escopo-mvp.md)
- [conformidade.md](../provedores/conformidade.md)
- [camadas.md](../autocura/camadas.md)
- [convencoes.md](../desenvolvimento/convencoes.md)

## A confirmar

- **A confirmar:** como automatizar o lado do usuário do Telegram na pesada (o bot não fala com outro bot): conta de teste dirigida por um cliente MTProto (ex.: biblioteca `gotd/td`) ou o ambiente de testes do Telegram. Verificar viabilidade e termos de uso.
- **A confirmar:** o MVP-5 cria Apps reais; o limite é 100 por conta. Definir se o teste apaga o App ao fim (exige o navegador) ou reaproveita um conjunto fixo de nomes.
- **A confirmar:** onde fica o laboratório da pesada (máquinas próprias ou executor auto-hospedado do GitHub Actions) e quem dispara a execução noturna.

## Fontes

- Testes com tempo falso no Go (`testing/synctest`): https://go.dev/blog/testing-time
- Cache de testes e flags do `go test`: https://pkg.go.dev/cmd/go
- CLI do Vitest (`vitest related`): https://vitest.dev/guide/cli
