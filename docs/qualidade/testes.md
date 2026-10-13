# Testes

> **Assunto:** política de testes: suítes, orçamentos de tempo, executor, testes afetados, quarentena e mapa do MVP · **Componente:** Projeto · **Atualizado:** 2026-10-13

## Objetivo

Manter os testes rápidos desde o primeiro dia. Tempo de execução é requisito: cada suíte tem orçamento, e o orçamento é verificado por máquina. Este documento é a **fonte única** da política de testes; os dublês (relógio, agente, Telegram, GitHub e rede falsos) ficam em [dubles.md](dubles.md).

## Comportamento

### Suítes

| Suíte | Nome no executor | Quando roda | Orçamento | O que contém |
|---|---|---|---|---|
| **rápida** | `fast` | a cada alteração e no gancho de pré-commit | **30 s** | a grande maioria: unitários Go, unitários e de componente do painel (Vitest), [verificação de docs](verificacao-de-docs.md). Sem rede real, sem disco lento, sem processos externos |
| **completa** | `full` | no PR, antes de juntar na branch principal (verificação obrigatória) | **3 min** por máquina | rápida + integração: Cidadela e Torres reais na mesma máquina, como [instâncias](../arquitetura/componentes.md#instâncias) rodando como **processos comuns** (motor e Guardião de cada uma; sem instalar serviços) ou como instâncias do motor no processo do teste, banco temporário, git real, agente falso como processo num PTY real; inclui a [troca de Cidadela e a replicação](#troca-de-cidadela-e-replicação-na-completa) |
| **pesada** | `heavy` | sob demanda, toda noite (agendada) e quando a versão de um provedor muda | sem limite rígido, **medida** | ponta a ponta dos 15 critérios do MVP, testes de falha da autocura, [conformidade com os provedores reais](../provedores/conformidade.md) e tudo o que exige instalar serviços ou mexer na integração com o sistema (contas, ACLs, mDNS do sistema), em máquinas reais (Linux e Windows) com os CLIs reais dos provedores; Telegram e GitHub são os dublês ([escopo da pesada](#escopo-da-pesada-telegram-e-github)) |

### Como as suítes são selecionadas

- **Go:** por *build tags*. Teste sem tag é da rápida. Integração usa `//go:build full` em arquivos `*_full_test.go`; ponta a ponta e falhas usam `//go:build heavy` em `*_heavy_test.go`. A completa compila com `-tags full`; a pesada com `-tags full,heavy` e roda com `-timeout 0` (sem o limite de 10 min do `go test`). Testes que dependem do Windows (ConPTY, serviço) somam `&& windows`. Código de spike tem a tag própria `spike`, fora de todas as suítes, e roda à mão ([convenções](../desenvolvimento/convencoes.md#estilo)).
- **Painel:** Vitest. A rápida roda `vitest run` (unitários e componentes com DOM simulado e servidor WebSocket falso). A pesada usa navegador real com Playwright (proposta) para o que o MVP-5 (link de criação de App) e o MVP-11 exigem.
- **Onde rodam:** rápida na máquina de quem desenvolve (e no gancho de pré-commit); rápida e completa no **CI** (`.github/workflows/ci.yml`, desde o M1), numa matriz `ubuntu-latest` e `windows-latest` em paralelo, cada uma com o orçamento de 3 min, junto do lint, da verificação de docs e da de código; as verificações `test (ubuntu-latest)` e `test (windows-latest)` são exigidas para juntar na `main` por um ruleset que o Dono cria uma vez ([CI e proteção da `main`](../desenvolvimento/ambiente.md#ci-e-proteção-da-main)); pesada no laboratório (abaixo). Como qualquer máquina pode virar Cidadela, o painel, o Telegram (falso), o banco, o cofre, a replicação e o cliente do `whisper-server` têm testes da completa que passam no CI Windows ([tudo roda nos dois sistemas](../desenvolvimento/ordem-de-implementacao.md#tudo-roda-nos-dois-sistemas)); o Whisper com o modelo real fica na pesada.

### Troca de Cidadela e replicação na completa

- Duas ou três [instâncias](../arquitetura/componentes.md#instâncias) na mesma máquina, como processos comuns (motor e Guardião de cada uma), cada uma com nome, pastas, bloco de portas, banco e certificados próprios, ligadas por rede real em `127.0.0.1`, com o **relógio falso** de todos os processos ([dublês](dubles.md)) e o Telegram e o GitHub falsos (inclusive as refs da [trava](../guilda/trava-no-github.md)). Uma cria a Guilda e é a Cidadela; as outras entram como Torres. Os testes de lógica ficam com as instâncias do motor no processo do teste.
- Cobrem: replicação contínua e cópia completa ([replicação](../guilda/replicacao.md)), época e fencing, aluguel da trava, espera do promovido, GitHub fora, Cidadela suspensa, fila de saída, aviso e promoção com aprovação pelo Telegram falso, concessão dos Guardiões e `mestre.local`, volta da antiga como Torre, retomada depois de reiniciar o processo e painel redirecionado ([Cidadela e Torres](../guilda/cidadela-e-torres.md)).
- No Windows, o mesmo teste roda no CI; o Dono repete à mão com duas instâncias **instaladas** como serviços (entrada, replicação e troca de Cidadela).
- Rodam nos dois sistemas do CI, dentro do orçamento da completa.

### Cgroup delegado na completa

- A completa roda como processos comuns, sem cgroup delegado, e o que depende dele (o neto que saiu da sessão em CA-TERM-05, o cgroup do terminal e o reinício do launcher em CA-TERM-07, os órfãos de CA-LOCAL-11) **roda de novo dentro de `systemd-run --user --scope -p Delegate=yes`**: o teste se reexecuta sozinho (`internal/testkit/delegated`) e relata o resultado; só é pulado, com o motivo, onde não há gerenciador systemd de usuário (contêiner, WSL sem systemd).
- O `ubuntu-latest` do GitHub **tem** esse gerenciador (conferido em 2026-10-13: `systemd-run --user --scope -p Delegate=yes` sai com 0, cgroup v2, systemd 255), então essas partes rodam no CI Linux; no Windows não há cgroup e o Job Object cumpre o papel. A parte que exige trocar de conta (`CAP_SETUID`: o `kill` do launcher como `mestre-agente`) fica na pesada e no [checklist](../plataforma/checklist.md).

### Executor `testrun`

Programa do repositório que roda as suítes e mede o tempo. Uso: `go run ./tools/testrun fast|full|heavy|affected|quarantine`.

1. Roda `go test -json -count=1` (sem cache de resultado, para medir de verdade) e `vitest run --reporter=json` em paralelo.
2. Mede o **tempo de parede** total, do início ao fim, com o cache de compilação já aquecido.
3. Se o total passar do orçamento, **falha** e mostra os **10 testes mais lentos** (nome, pacote, tempo).
4. **Sinaliza** todo teste da rápida acima de **100 ms**. Antes de sinalizar, roda o teste sozinho uma vez para descontar a disputa de CPU (só mede tempo; não é nova tentativa de aprovação). A sinalização é aviso na saída e no resumo do CI; não falha a suíte.
5. Na pesada, guarda o tempo de cada execução (em `.testrun/heavy-history.json`, fora do git) e avisa quando o total passa **20%** (padrão) da mediana das últimas 7.
6. Define `MESTRE_TEST_SUITE=fast|full|heavy` no ambiente dos testes (uso em [Regras e erros](#regras-e-erros)).
7. Na rápida e na completa, roda também o `golangci-lint run ./...` em paralelo (na completa com `--build-tags full`), dentro do orçamento; problema do linter falha a suíte, e o binário ausente falha apontando para o [ambiente](../desenvolvimento/ambiente.md).

### Testes afetados

`testrun affected` roda só o que as alterações podem ter quebrado:
- **Go:** `go test ./...` **com** o cache de testes. O Go só roda de novo os pacotes cujo código, dependências, arquivos lidos ou variáveis de ambiente mudaram; o resto sai do cache. Esse é o mecanismo de "afetados" do Go, sem lista manual.
- **Painel:** `vitest related --run <arquivos alterados>`, com a lista vinda de `git diff --name-only HEAD` mais os arquivos novos (`git ls-files --others --exclude-standard`).
- **Docs:** a verificação de docs é um teste Go que lê `docs/`; o cache detecta a mudança e a roda de novo.
- Antes do commit roda a rápida inteira; antes do merge, a completa inteira.

### Quarentena

- Teste instável vai para `test/quarantine.yaml`: identificador do teste (o nome Go de nível mais alto, como no `-skip`: `test`), link da issue (rótulo `flaky`: `issue`), data de entrada (`added`, AAAA-MM-DD) e motivo (`reason`).
- O executor pula esses testes (`go test -skip`, `exclude` do Vitest) e lista os pulados no fim.
- Prazo padrão: **14 dias**. Entrada vencida faz a completa falhar até o teste ser corrigido ou apagado.
- Toda noite, `testrun quarantine` roda cada teste da quarentena 20 vezes; quando passa todas, o executor avisa que ele pode sair.
- Para achar instáveis: toda noite a rápida roda com `-count=5 -shuffle=on` (`testrun fast -count 5 -shuffle`; Vitest com ordem embaralhada); resultado misto vira issue `flaky`.

### Mapa do MVP para a suíte pesada

O texto de cada critério está em [escopo-mvp.md](../produto/escopo-mvp.md); aqui fica só o teste que o cobre.

| Critério | Teste | Peças reais |
|---|---|---|
| MVP-1 | `TestMVP01_InstallAndJoinGuild` | instalador único no Ubuntu e no Windows, descoberta da Guilda na rede local, entrada com aprovação (Telegram falso), CA da Guilda |
| MVP-2 | `TestMVP02_SelfHealing` (+ testes de falha abaixo) | systemd, serviço Windows, Guardiões, painel (Telegram falso) |
| MVP-3 | `TestMVP03_SessionZero` | clone de terceiro sem o Mestre (GitHub falso) |
| MVP-4 | `TestMVP04_EpicToResult` | três provedores reais, Cidadela Linux e Torre Windows, duas mesas em paralelo |
| MVP-5 | `TestMVP05_GitHubIdentities` | link de criação de App aberto num navegador real (Playwright), contra as páginas de criação e instalação do GitHub falso |
| MVP-6 | `TestMVP06_ClassPermissions` | três provedores reais |
| MVP-7 | `TestMVP07_ApprovalPolicy` | painel, provedores reais (Telegram e GitHub falsos) |
| MVP-8 | `TestMVP08_PermanentDecree` | provedores reais (Telegram falso) |
| MVP-9 | `TestMVP09_UnauthorizedUser` | Cidadela e painel reais (Telegram falso) |
| MVP-10 | `TestMVP10_AudioAndAttachments` | whisper-server com modelo real (Telegram falso) |
| MVP-11 | `TestMVP11_PanelOverview` | navegador real (Playwright) |
| MVP-12 | `TestMVP12_RestartRestoresState` | reinício real das máquinas da Cidadela e das Torres, com os serviços subindo sozinhos |
| MVP-13 | `TestMVP13_FourCompositions` | instalador real nas quatro composições (só Linux, Cidadela Linux + Torre Windows, Cidadela Windows + Torre Linux, só Windows), cada uma montada do zero |
| MVP-14 | `TestMVP14_PCOperator` | VM Windows com o Operador habilitado, o usuário de teste logado (logon automático) e a tela desbloqueada, Chrome com a extensão Claude in Chrome, Claude Code real, um site local de teste, painel em navegador real (Playwright) (Telegram falso) |
| MVP-15 | `TestMVP15_CitadelTakeover` | as duas VMs com a Cidadela em cada sistema (Linux com Torre Windows, e o inverso): derrubar a VM da Cidadela no meio de gravações, autocura sem sucesso, aviso e promoção manual pelo Telegram falso, trava no GitHub falso, perda conferida na auditoria, volta da antiga como Torre |

**Critério *pesada* vira subteste:** cada CA *pesada* é um subteste `TestMVPnn/CA-<SIGLA>-<NN>` do primeiro MVP que ele cita (ex.: `TestMVP13_FourCompositions/CA-GUILDA-11`), e o comentário `// CA-…` fica acima do subteste.

**Onde roda cada teste:** instalação (MVP-1 e MVP-13) e os testes de falha da autocura (MVP-2) rodam nas **quatro composições**; a troca de Cidadela (MVP-15) roda com a Cidadela em cada sistema; os demais testes de ponta a ponta rodam **uma vez, na composição Cidadela Linux + Torre Windows** ([ADR 0022](../decisoes/0022-guilda-cidadela-e-torres.md)).

**Testes de falha da autocura** (`TestChaos_*`): matar o motor, travar o processo, encher o disco, derrubar a rede e falhar uma atualização, em cada máquina das quatro composições, inclusive o [modo máquina única](../autocura/camadas.md#modo-máquina-única). O comportamento esperado está em [camadas.md](../autocura/camadas.md); os critérios desses testes citam `(MVP-2)`, e os de conformidade citam `(MVP-6)`.

### Escopo da pesada: Telegram e GitHub

- **Decisão:** a pesada usa máquinas reais e os CLIs reais dos provedores, e Telegram e GitHub **falsos**: os dublês de [dubles.md](dubles.md), com os formatos gravados. Vale para todos os testes de ponta a ponta, inclusive MVP-5, 7, 8, 9 e 10.
- Por quê: o lado do usuário do Telegram não é automatizável pela Bot API (um bot não fala com outro bot), e o MVP-5 criaria Apps reais a cada noite, contra o limite de 100 por conta.
- Nenhuma suíte usa Telegram ou GitHub reais. O comportamento real do GitHub que os docs marcam como **Verificar na implementação** é conferido à mão no repositório sandbox ([projetos de treino](projetos-de-treino.md#sandbox-real-no-github)), e as respostas reais conferidas viram gravações dos dublês. O repositório que os testes usam no lugar do projeto do usuário também está lá.
- Na pesada, os dublês do Telegram e do GitHub rodam como servidores HTTP no hospedeiro do laboratório, e a Cidadela instalada aponta para eles, inclusive depois de uma troca de Cidadela ([como](dubles.md#telegram-e-github-na-pesada)).

### Laboratório

- **Duas VMs dedicadas**, separadas das máquinas de uso: uma Ubuntu LTS e uma Windows 11 ou superior, na mesma rede local, com as contas de teste dos provedores já logadas, num hospedeiro Linux. Para o MVP-14, a VM Windows tem um usuário de teste com logon automático, o Chrome com a extensão Claude in Chrome logada na conta de teste e o UAC no padrão.
- **As quatro composições** saem dessas duas VMs, restauradas de um retrato limpo a cada uma: só Linux (VM Ubuntu sozinha), Cidadela Linux + Torre Windows (a Ubuntu instalada primeiro), Cidadela Windows + Torre Linux (a Windows instalada primeiro) e só Windows (VM Windows sozinha). A **troca de Cidadela** (MVP-15) roda nas duas composições de duas máquinas, para que haja Cidadela em cada sistema antes e depois da troca.
- **Disparo:** toda noite por um timer do systemd no hospedeiro (`mestre-heavy.timer`, que roda `testrun heavy` e restaura as VMs), e sob demanda por quem desenvolve, rodando o mesmo comando no hospedeiro. O resultado e o tempo de cada execução ficam guardados para a comparação com a mediana (passo 5 do executor).

## Regras e erros

- **Proibido `sleep` em teste.** Código que depende de tempo recebe o relógio injetável ([dubles.md](dubles.md)). O linter (`forbidigo` no golangci-lint; [divisão das regras](../desenvolvimento/convencoes.md#estilo)) recusa `time.Sleep`, `time.Now`, `time.After`, `time.NewTimer`, `time.NewTicker`, `time.AfterFunc` e `time.Since` fora do pacote do relógio. No painel, `vi.useFakeTimers()`; nada de esperar com `setTimeout`.
- **Rápida sem processos e sem rede:** com `MESTRE_TEST_SUITE=fast`, a camada única que inicia processos e a que abre conexões recusam com erro claro ("processo externo na suíte rápida: use o PTY falso"). Na rápida, o agente falso, o `mestre hook` e a CLI rodam dentro do processo do teste pelo lançador em memória ([dublês](dubles.md#agente-falso-fakeagent)). Teste que precisa de processo real é da completa.
- **Git:** o motor usa o binário `git` do sistema (2.32 ou mais novo) só pelo wrapper `internal/gitx` ([convenções](../desenvolvimento/convencoes.md)). Na rápida, `gitx` é o git falso do `testkit`; critério que precisa de git real (bundle, worktree, `info/exclude`, commit) é da completa.
- **Serviços do sistema:** a completa nunca instala serviços nem muda contas, ACLs ou o mDNS do sistema; critério que precisa disso é da pesada e cita `(MVP-1)` ou `(MVP-13)`.
- **Paralelo e isolado:** todo teste Go chama `t.Parallel()` (linter `paralleltest`). Cada teste usa `t.TempDir()` para pastas e banco SQLite próprios, porta `127.0.0.1:0` quando houver rede real, e nenhum estado global. `t.Setenv` e `t.Chdir` não combinam com `t.Parallel()`: passe a configuração por parâmetro.
- **Um comportamento, um teste, no nível mais baixo possível.** Nada de testar a mesma regra em unitário, integração e ponta a ponta. Ponta a ponta só para os critérios MVP-1..15; a [verificação de docs](verificacao-de-docs.md) recusa critério *pesada* que não cite um MVP.
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
- **CA-TESTES-08** · *rápida* — **Dado** o `golangci-lint` fora do `PATH`, **quando** o `testrun fast` ou o `full` roda, **então** a suíte falha com a mensagem que aponta para `docs/desenvolvimento/ambiente.md`; **dado** o linter com problemas, **então** a suíte falha mostrando a saída dele.

## Depende de

- [convencoes.md](../desenvolvimento/convencoes.md) · [escopo-mvp.md](../produto/escopo-mvp.md)

## Fontes

- Testes com tempo falso no Go (`testing/synctest`): https://go.dev/blog/testing-time
- Cache de testes e flags do `go test`: https://pkg.go.dev/cmd/go
- CLI do Vitest (`vitest related`): https://vitest.dev/guide/cli
