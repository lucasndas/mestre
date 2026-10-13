# Convenções de desenvolvimento

> **Assunto:** layout do código Go e do painel, idioma, estilo, código gerado, commits, branches, PRs e definição de pronto · **Componente:** Projeto · **Atualizado:** 2026-10-13

## Layout do repositório

Um único módulo Go na raiz (Go 1.25+, `CGO_ENABLED=0`; [ADR 0001](../decisoes/0001-stack-go.md)) e o painel em `web/`. O `go.mod` fixa `toolchain go1.27.2` ([ambiente](ambiente.md)).

```
cmd/
  mestre/            main do binário mestre: só monta os subcomandos
  mestre-guardiao/ mestre-pty/   mains do Guardião e do mestre-pty (um por terminal, ver terminal.md)
  fakeagent/         agente falso dos testes (ver dubles.md); fora dos pacotes de release
internal/
  app/               montagem do motor e das funções que a Cidadela liga e desliga
  guild/ machinesheet/ replication/   Guilda (entrada, época, troca de Cidadela), ficha da máquina, replicação
  cli/               comandos da CLI, definição única dos comandos e saída
  localchan/         canal local CLI e hook ↔ motor (ver canal-local.md)
  hook/              despachante `mestre hook`
  providers/         adaptadores: claude, codex, antigravity, shell
  orchestration/     workspaces, tables, characters, quests, waits, rituals, notes, grimoires
  sessionzero/       geração de arquivos de workspace, mesa e personagem
  classes/           pacotes de classe, permissions.yaml, teto, regras efetivas
  decrees/ approvals/ users/ audit/   decretos e aprovações; usuários, níveis, login, sessões e PIN; trilha de auditoria
  github/            cliente, Apps, consulta periódica
  gitx/              wrapper único do binário git (worktrees, bundles, info/exclude)
  sync/              sincronização de worktrees Torre ↔ Cidadela por bundle
  telegram/          bot, roteamento, cliente do whisper-server
  usage/             coleta de uso e limites
  checklist/         itens do checklist e alertas
  update/            autoatualização do Mestre e detecção de versão nova dos CLIs dos provedores
  healing/           lado do motor da autocura (supervisão cruzada, runbook)
  protocol/          envelope (com época) e mensagens entre máquinas, versões
  transport/         WSS, TLS, CA da Guilda, mTLS
  discovery/         descoberta da Guilda (broadcast e mDNS)
  terminal/          mestre-pty e o lado do motor: PTY/ConPTY (go-pty), emulador, buffer, protocolo, aperto de mão, reconciliação
  launcher/ winsvc/  `mestre-launcher` (protocolo, servidor, cliente de `terminal.Spawner` e a parte Linux) e as partes só do Windows: serviço, contas virtuais e `mestre-agente`, parte Windows do launcher, ACLs, DPAPI
  store/             SQLite e migrações (guild/: banco da Guilda; machine/: banco local de máquina)
  vault/ logging/    cofre; slog + mascaramento de segredos
  privdir/           pastas privadas (único lugar que cria uma pasta 0700)
  glossary/          textos de ajuda gerados do glossário
  panel/             HTTP do painel e API; webdist/ embutido com //go:embed
  testkit/ realaccount/   dublês compartilhados (ver dubles.md); testes de contas reais (tag `realaccount`, ver contas-reais.md)
guardian/            código do Guardião; não importa nada de internal/
pkg/clock/           relógio injetável: único pacote que usa time.Now e temporizadores (usado pelo motor e pelo Guardião)
web/                 painel: React + TypeScript + Vite
  src/gen/           tipos TypeScript gerados a partir do Go
tools/
  testrun/ docscheck/ codecheck/ rulesetcheck/ realaccount/   executor das suítes (testes.md), verificação de docs, regras fora do linter (regras-de-codigo.md), conferência do ruleset (ambiente.md), preparo e execução dos testes de contas reais (contas-reais.md)
  githooks/          ganchos de pré-commit e de pré-push versionados (ver ambiente.md)
  spikes/ winsandbox/   spikes de Linux; Windows Sandbox e spikes/ de Windows (ver Estilo e ambiente.md)
.github/workflows/   CI: rápida e completa em Linux e Windows (ver ambiente.md)
test/quarantine.yaml testes em quarentena
testdata/projetos/   projetos de treino dos testes (ver projetos-de-treino.md)
packaging/           units do systemd e instalador do Windows
docs/                esta documentação
```

- **Guardião isolado:** `guardian/` só importa a biblioteca padrão, `pkg/clock` e as dependências permitidas: `golang.org/x/sys`, `golang.org/x/crypto` (argon2id do login e do PIN da página reserva), `kardianos/service`, `coder/websocket`, `go-systemd`, `aead.dev/minisign` (verificação no `apply-update`) e `brutella/dnssd` (mDNS). O protocolo motor ↔ Guardião mora em `guardian/protocol`, que o motor importa (nunca o contrário). Regra verificada pelo linter (`depguard`). Binários, subcomandos e portas: [componentes](../arquitetura/componentes.md).
- **Dependências escolhidas:** mDNS e DNS-SD com `github.com/brutella/dnssd` (consulta em toda máquina, anúncio de reserva no Guardião de máquina Windows); minisign com `aead.dev/minisign`; emulador de terminal com `github.com/charmbracelet/x/vt` ([terminal](../arquitetura/terminal.md)); git pelo binário do sistema, na versão mínima do ADR 0001, só por `internal/gitx`, nunca por biblioteca Go (worktrees, bundles e `info/exclude` precisam de fidelidade total), sempre com ambiente controlado (sem configuração do usuário nem do sistema, sem hooks, sem prompts, mensagens em inglês, a configuração do repositório impedida de rodar comandos), credenciais só pelo `gitx.Auth`, no ambiente do processo e nunca na URL nem nos argumentos ([git do motor](../arquitetura/seguranca.md#git-do-motor)), e as operações que mudam refs, configuração ou `info/exclude` uma por vez em cada repositório (todos os worktrees contam como um; leituras em paralelo); YAML (quarentena, roteiros do agente falso, `permissions.yaml`) com `go.yaml.in/yaml/v3` (MIT e Apache 2.0, o fork mantido do `gopkg.in/yaml.v3`). O resto da stack está no [ADR 0001](../decisoes/0001-stack-go.md).
- **Código por sistema:** um código só para os dois sistemas sempre que der; o que muda vai em arquivos `_linux.go` e `_windows.go` atrás de uma interface, e o resto compila nos dois. No `terminal`, só três coisas são por sistema: iniciar o processo como `mestre-agente`, matar a árvore (`cgroup.kill` ou Job Object) e identificar um processo pelo horário de início ([terminal](../arquitetura/terminal.md#mestre-pty)).
- **Painel embutido:** o build do painel vai para `internal/panel/webdist/` (fora do git, exceto um arquivo marcador para o `go build` funcionar sem o build do painel). Em desenvolvimento, o servidor do Vite encaminha a API para um `mestre run` local, Cidadela de uma Guilda de teste.

## Idioma

| O quê | Idioma |
|---|---|
| Documentação (`docs/`, `AGENTS.md`, `README.md`) | português (pt-BR); nomes de arquivo e pasta em português, sem acento |
| Código, identificadores, comentários, nomes de tabela, comandos da CLI | inglês |
| Mensagens de commit, títulos de PR e de branch | inglês |
| Rótulos da interface e mensagens do Telegram | português, com o vocabulário do [glossário](../produto/glossario.md) |

Nomes dos termos do produto no código (use sempre estes):

| Termo | Código | Termo | Código |
|---|---|---|---|
| Workspace | `Workspace` | Pergaminho / Tomo | `Note` / `Tome` |
| Mesa | `Table` | Grimório | `Grimoire` |
| Personagem | `Character` | Ritual | `Ritual` |
| Party | `Party` | Missão / Etapa | `Quest` / `Step` |
| Base / Ficha | `Base` / `Sheet` | Decreto | `Decree` |
| Classe | `Class` | Caixa de entrada / Espera | `Inbox` / `Wait` |
| Grão-Mestre | `GrandMaster` | Guardião | `Guardian` |
| Mestre da mesa | `TableMaster` | Guilda / Cidadela / Torre | `Guild` / `Citadel` / `Tower` |
| Ficha da máquina | `MachineSheet` | Época | `Epoch` |
| Sessão zero | `SessionZero` | Perfil de conta | `AccountProfile` |

Exceções herdadas, mantidas como estão: identificadores de protocolo, variáveis de ambiente e pastas usam `agent` para personagem (`agent.rules`, `MESTRE_AGENT_ID`, `.mestre/agents/`) e `mesa` para mesa (`MESTRE_MESA`, `.mestre/mesas/`). No código Go, os tipos continuam `Character` e `Table`.

## Estilo

**Go**
- `gofmt` e `goimports`; `golangci-lint` 2.14.0 ([ambiente](ambiente.md)) com `govet`, `staticcheck`, `errcheck`, `forbidigo`, `paralleltest` e `depguard` (configuração em `.golangci.yml`), rodado pelo `testrun` na rápida e na completa ([executor](../qualidade/testes.md#executor-testrun)).
- **Cada regra de código vive numa ferramenta só.** O golangci-lint cobre: tempo só em `pkg/clock` (`forbidigo`; esta é a **lista única** das funções proibidas, citada pelos [testes](../qualidade/testes.md#regras-e-erros): `time.Sleep`, `Now`, `After`, `NewTimer`, `NewTicker`, `AfterFunc`, `Since`, `Until` e `Tick`, e `context.WithTimeout` e `WithDeadline`, com as variantes `Cause`; use `clock.WithTimeout`); `internal/testkit` importado só por testes, pelo próprio `testkit`, por `cmd/fakeagent` e por `tools`, e o isolamento do `guardian/` ([layout](#layout-do-repositório)) (`depguard`); `os/exec` (`depguard`), `net.Dial*`, `net.Listen*`, `net.Dialer`, `net.ListenConfig` e `os.StartProcess` (`forbidigo`) só em `internal/terminal`, `internal/gitx` (só processos), `internal/launcher`, `internal/winsvc`, `internal/testkit`, `internal/realaccount`, `internal/localchan` (só rede), `internal/transport` (só rede), `internal/discovery` (só rede: UDP e mDNS), `cmd`, `tools` e nos testes `*_full_test.go` e `*_heavy_test.go`, as camadas que carregam o [guarda da suíte rápida](../qualidade/testes.md#regras-e-erros); e `t.Parallel()` em toda função `TestXxx` (`paralleltest`, com `ignore-missing-subtests: true`). O `tools/codecheck` (teste `TestCodeRules`, na rápida) cobre sozinho o que o linter não vê, com a exceção única `// mestre:allow <regra>: <motivo>`: `t.Parallel()` dos subtestes, `select {}` e saída herdada nos testes, atributos de segurança `nil` no Windows, `0o700` fora de `internal/privdir` e segredos com `String`/`Format` em receptor ponteiro ([regras de código](../qualidade/regras-de-codigo.md)). O `tools/docscheck` cobre o que cruza código e documentos: critérios sem teste, marcos sem auditoria, comentários de marco velhos e os pinos do `go.mod` ([verificação de docs](../qualidade/verificacao-de-docs.md#checagens-de-código-e-de-marcos)).
- **Código de spike** (`tools/spikes/`, `tools/winsandbox/spikes/`): guardado como referência do resultado de um spike, fora do lint (exclusão em `.golangci.yml`) e nunca importado por código de produção; teste de spike usa a tag `spike` (`//go:build spike`), fora de todas as suítes, e roda só à mão (`go test -tags spike ./tools/spikes/...`); o que for aproveitado é reescrito no pacote definitivo, com testes e as regras acima ([ordem de implementação](ordem-de-implementacao.md#spikes-do-m1)).
- Erros com contexto: `fmt.Errorf("open table %s: %w", id, err)`. Sem `panic` fora de `main` e de invariantes impossíveis.
- `context.Context` como primeiro parâmetro de tudo que faz E/S ou espera.
- Tempo só pelo `clock` (regra e motivo em [testes](../qualidade/testes.md)). Processos, rede e git só pelas camadas únicas de `terminal/` (lançador e `mestre-pty`), `transport/` e `gitx/`, que os dublês substituem ([dublês](../qualidade/dubles.md)).
- Logs só por `logging` (slog em JSON por linha, com mascaramento; [segurança](../arquitetura/seguranca.md)).
- Sem estado global; dependências passadas por construtor. Interfaces pequenas, declaradas por quem as usa.

**TypeScript**
- `strict: true`; ESLint e Prettier com a configuração do repositório; componentes React em função; testes com Vitest.

## Código gerado

- Tipos do protocolo e da API para o painel: gerados do Go para `web/src/gen/` com `go generate` e `github.com/gzuidhof/tygo`.
- Textos de ajuda: `internal/glossary/terms_gen.go` é gerado a partir de `docs/produto/glossario.md` ([glossário](../produto/glossario.md)) com `go generate ./internal/glossary` (gerador em `internal/glossary/gen`); o teste `TestGeneratedFileMatchesTheGlossary` regenera em memória e falha se o arquivo estiver velho.
- Arquivos gerados **vão para o git**. Um teste da suíte rápida regenera em memória e falha se estiverem desatualizados.

## Commits

Conventional Commits 1.0.0, em inglês:

```
<type>(<scope>): <summary>

<body: why, not what>

<footer: Refs #12 | Closes #12 | BREAKING CHANGE: ...>
```

- **type:** `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `build`, `ci`, `chore`, `revert`.
- **scope** (opcional): `guild`, `machinesheet`, `replication`, `guardian`, `cli`, `hook`, `providers`, `orchestration`, `classes`, `github`, `telegram`, `approvals`, `healing`, `protocol`, `store`, `vault`, `panel`, `web`, `tools`, `docs`, `sessionzero`, `gitx`, `sync`, `update`, `usage`, `checklist`, `users`, `audit`, `terminal`, `localchan`, `transport`, `discovery`, `winsvc`, `logging`, `glossary`, `clock`, `testkit`, `ci`, `spike`, `winsandbox`.
- **summary:** imperativo, minúsculo, sem ponto final, até 72 caracteres. Ex.: `feat(hook): normalize antigravity camelCase payloads`.
- Mudança incompatível (protocolo, banco, CLI): `!` depois do tipo e rodapé `BREAKING CHANGE:`.
- Um commit, um assunto; código, testes e documentação da mesma mudança vão juntos.

## Branches e PRs

- **`main`:** sempre verde; protegida por um ruleset que exige o CI (`.github/workflows/ci.yml`: rápida e completa em Linux e Windows) e só aceita PR com squash ([CI e ruleset](ambiente.md#ci-e-proteção-da-main)).
- **Branches de trabalho:** `feat/<slug>`, `fix/<slug>`, `docs/<slug>`, com `slug` curto em inglês, kebab-case (ex.: `feat/table-lifecycle`). Testes, build e refatorações usam `fix/` quando corrigem algo e `feat/` nos demais casos.
- **PR:** um assunto, pequeno. Título no formato de commit (vira a mensagem do squash). Descrição: o quê e por quê, documentos alterados, critérios de aceite cobertos (`CA-…`), tempos da rápida e da completa.
- **Merge:** squash. A branch é apagada depois.
- **Versões:** SemVer. Uma tag e uma release por versão, `vX.Y.Z` (a versão do motor), com os três binários; as versões do Guardião e do `mestre-pty` vão só no manifesto `mestre-release.json` e no `version` de cada um, sem tag própria ([autoatualização](../plataforma/autoatualizacao.md)). As versões dos protocolos são independentes ([protocolo](../conectividade/protocolo.md)).

## Definição de pronto

Uma mudança está pronta quando:

1. O comportamento está descrito no documento da funcionalidade e os critérios de aceite dele têm teste, na suíte indicada, com o identificador acima do teste (`// CA-…`).
2. A **suíte rápida** passa dentro do orçamento (`go run ./tools/testrun fast`, também no gancho de pré-commit); antes do merge, a **completa** também ([testes](../qualidade/testes.md)).
3. A **documentação** afetada foi atualizada na mesma mudança (documento de funcionalidade novo entra no mapa da [ordem de implementação](ordem-de-implementacao.md)) e a [verificação de docs](../qualidade/verificacao-de-docs.md) passa.
4. Linter limpo e arquivos gerados em dia.
5. Nada fora do [escopo do MVP](../produto/escopo-mvp.md) foi acrescentado.
6. Commits e PR seguem as convenções acima.

## Fontes

- Conventional Commits 1.0.0: https://www.conventionalcommits.org/en/v1.0.0/
