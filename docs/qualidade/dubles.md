# Dublês de teste

> **Assunto:** relógio injetável, agente falso por provedor, Telegram, GitHub, rede, PTY e git falsos, e as gravações que os alimentam · **Componente:** Projeto · **Atualizado:** 2026-10-14

## Objetivo

Permitir que quase todos os testes rodem sem tempo real, sem provedores reais, sem rede e sem cota, como exige a [política de testes](testes.md). Cada dublê imita o comportamento real com os formatos reais gravados. Os provedores reais só aparecem na suíte pesada; Telegram e GitHub são dublês em todas as suítes, inclusive na pesada ([decisão](testes.md#escopo-da-pesada-telegram-e-github)).

## Comportamento

### Relógio injetável

- Interface `Clock` no pacote do relógio (`pkg/clock/`, compartilhado com o Guardião; ver [convenções](../desenvolvimento/convencoes.md)): `Now()`, `NewTimer(d)`, `NewTicker(d)`, `AfterFunc(d, f)`. Implementação real sobre `time`; implementação falsa com `Advance(d)`, `Set(t)` e `BlockUntil(n)` (espera até `n` temporizadores estarem armados, para evitar corrida no teste). Como o relógio real desde o Go 1.23, o falso não entrega tique velho: `Stop` e `Reset` de temporizador ou ticker descartam o tique que ninguém leu. `clock.WithTimeout(ctx, c, d)` troca o `context.WithTimeout`: ao vencer, `ctx.Err()` é `context.DeadlineExceeded` (também a causa) e `Deadline()` é `c.Now()+d`, ou o do contexto pai se for antes.
- Todo código do motor que usa tempo (heartbeat, backoff, tempos limite de espera, rituais, expiração de aprovações, intervalo de consulta ao GitHub) recebe o `Clock` por parâmetro.
- **`testing/synctest`** (Go 1.25+) é a primeira escolha para lógica com goroutines e temporizadores dentro de um processo: dentro de `synctest.Test`, o tempo só anda quando todas as goroutines estão bloqueadas, e `synctest.Wait()` espera o sistema assentar. Nesse caso o código usa o `Clock` real, e o tempo falso vem da "bolha".
- **Relógio falso** quando o teste precisa de uma data de calendário (ritual diário às 02:00, fuso horário, horário de verão), porque a bolha do synctest sempre começa em 2000-01-01 UTC.
- Rede e disco reais não contam como "bloqueio durável" para o synctest: dentro da bolha, use a rede falsa em memória (abaixo).
- **Suíte completa (vários processos):** binários compilados com a tag `full` aceitam `MESTRE_TEST_CLOCK=<socket>`, e o teste avança o relógio de todos os processos por esse canal: o teste é o servidor; cada processo conecta e recebe linhas `set <RFC 3339>` ou `advance <duração>`, aplica no relógio falso dele e responde `ok <agora em RFC 3339>`. O teste só segue depois do `ok` de todos. Binários de produção não contêm esse código. O lado do teste é `internal/testkit/clocksock`; o do processo, `clock.Remote` (só com a tag `full`).

### Agente falso (`fakeagent`)

Um único programa (`cmd/fakeagent`, [layout](../desenvolvimento/convencoes.md#layout-do-repositório)) que imita Claude Code, Codex e Antigravity. O provedor vem de `--provider claude|codex|antigravity` ou do nome com que foi chamado (cópias chamadas `claude`, `codex`, `agy`, com `.exe` no Windows), assim as [bases](../orquestracao/bases-e-fichas.md) funcionam sem mudança.

- **Dois modos, mesmo código:** na rápida, roda dentro do processo do teste, ligado ao PTY falso, por uma interface de **lançador**: o lançador em memória troca "iniciar processo" por uma goroutine, e as chamadas ao `mestre hook` e à CLI viram chamadas de função ao mesmo código, com o canal local falso. Na completa, o lançador real inicia o agente num `mestre-pty` real, com PTY (ConPTY no Windows) ([terminal](../arquitetura/terminal.md#mestre-pty)).
- **TUI mínima:** cabeçalho, linha de entrada, indicador de "ocupado" enquanto trabalha e o sinal de "pronto" que a base usa; aceita entrada crua (`\x03` interrompe, setas movem em menus; no Windows, põe o console em modo cru, sem `ENABLE_PROCESSED_INPUT`, para receber o `\x03` como byte, como os CLIs reais, [Windows](../arquitetura/windows.md#conpty)) e colagem entre colchetes, com a tecla de envio do provedor imitado ([digitação](../provedores/adaptador.md#digitação-de-mensagens)).
- **Telas de início**, com os textos do provedor imitado: login (quando o perfil de conta não tem credencial falsa: `.credentials.json` no Claude, `auth.json` no Codex e `antigravity-oauth-token` no Antigravity, que no Windows real fica no Gerenciador de Credenciais, mas o falso lê o arquivo nos dois sistemas), confiança da pasta e, no Codex, a revisão de hooks; com `FAKEAGENT_START_SCREEN=<texto>`, antes delas, uma tela que nenhum adaptador conhece, que espera Enter (a [escalada](../provedores/adaptador.md#prontidão-e-telas-interativas)). O agente falso lê **os mesmos arquivos de confiança** que o real, então o teste prova que a pré-confiança do motor funciona. Regras de cada provedor: [claude-code.md](../provedores/claude-code.md), [codex.md](../provedores/codex.md), [antigravity.md](../provedores/antigravity.md).
- **Instruções e skills:** lê o que o real leria (arquivo do projeto, arquivo extra pela flag ou configuração do provedor, pastas de skills) e responde ao comando `/fake-dump` com um JSON do que recebeu. É assim que testes rápidos conferem a entrega da camada do Mestre ([personagem.md](../sessao-zero/personagem.md)).
- **Hooks:** lê o arquivo de hooks do provedor, chama os comandos configurados (pelo shell do sistema: `sh -c` no Linux, `cmd /s /c` no Windows) com os payloads gravados (preenchendo `cwd`, ids de sessão, ferramenta e entrada) e aplica a resposta com a semântica do provedor: no Claude e no Codex, só exit 2 ou decisão explícita bloqueia e falha libera (só o Claude aceita `onFailure: "block"`; o Codex libera sempre, [codex.md](../provedores/codex.md#hooks)); no Antigravity, falha, saída inválida ou ausência de `allow` bloqueia; no Codex, hook novo ou modificado só roda depois de confiado (o agente falso grava a confiança no `config.toml` com um hash próprio; o algoritmo real é conferido na [conformidade](../provedores/conformidade.md)) e as camadas somam: o `hooks.json` do perfil e o `.codex/hooks.json` do projeto (pasta atual e raiz git, já confiados). Evento que o falso não dispara, tipo de hook que ele não roda e evento sem gravação fazem o agente terminar com "não implementado no dublê" (o `MemLauncher` também falha o teste). Hoje ele dispara SessionStart, UserPromptSubmit, PreToolUse, PostToolUse e Stop (no Antigravity, PreInvocation, PreToolUse, PostToolUse e Stop); no Claude, também PermissionRequest, PostToolUseFailure (ferramenta que falha), StopFailure, Notification, PreCompact (`/compact`) e SessionEnd (`/exit`), e roda a statusLine do `--settings`. O contrato está em [adaptador.md](../provedores/adaptador.md).
- **Uso e limites:** emite o uso no formato do provedor (JSON da statusLine, eventos `token_count` no JSONL da sessão, `/usage` em JSON) e simula limite de assinatura (no Claude, `StopFailure` com `rate_limit`). Ver [limites-de-uso.md](../plataforma/limites-de-uso.md). No Claude, a statusLine recebe custo e contexto que crescem a cada turno; o passo `limit: session|weekly` do roteiro mostra a mensagem e o menu "What do you want to do?" e, na opção de esperar, dispara o `StopFailure`; `tool` com `ask: true` pede permissão: decide o hook PermissionRequest ou, sem decisão, a tela de permissão (Enter é "Yes", Esc é "No").
- **Roteiro:** um arquivo YAML diz, para cada prompt recebido (expressão regular), os passos: `say` (responde texto), `tool` (pede uma ferramenta, disparando os hooks de antes e depois), `edit` (altera um arquivo), `run` (roda um comando, ex. `mestre done "..."`, usando a CLI real como um agente faria), `wait` (pelo relógio), `hang` (fica parado sem sinalizar) e `crash` (encerra com erro). Formato: `prompts:` com itens `match` e `steps`; o arquivo vem de `--fake-script <arquivo>` ou de `FAKEAGENT_SCRIPT`; campo desconhecido é erro, como no [tarefas.yaml](projetos-de-treino.md#tarefas-tarefasyaml). `tool`, `edit` e `run` passam pela ferramenta do provedor e disparam os hooks. Sem roteiro, ecoa o prompt e chama `mestre done`.

### Gravações

- Ficam em `internal/testkit/testdata/recordings/<origem>/<versão>/` (ex.: `claude-code/2.1.296/`, `telegram/bot-api-10.3/`, `github/rest-2022-11-28/`), um arquivo JSON por evento ou resposta com `origin`, `version`, `captured_at`, `source` (documentação ou captura) e `body`.
- **Provedores:** capturadas dos CLIs reais pelo [teste de conformidade](../provedores/conformidade.md). Quando ele encontra um campo diferente do gravado, falha apontando o campo e grava a versão nova para revisão em PR.
- **Telegram e GitHub:** formatos tirados da documentação oficial e de respostas reais conferidas à mão (ex.: os itens do GitHub verificados no [sandbox](projetos-de-treino.md#sandbox-real-no-github)), já mascaradas.
- O agente falso usa a gravação mais nova por padrão (versão com número vale mais que a identificada só por commit, como a do Codex); o teste pode fixar uma versão: campo `RecordingVersion` do lançador em memória, ou `--fake-recording-version <versão>` / `FAKEAGENT_RECORDING_VERSION` no binário. Versão que não existe falha o teste.

### Telegram falso

- Servidor da Bot API em memória com só o que o [bot](../telegram/bot.md) usa: `getUpdates` (long polling, com o tempo limite pelo relógio), envio e edição de mensagens, botões (`answerCallbackQuery`), envio de documentos e `getFile` com download.
- Controle para o teste: "usuário X manda texto, voz, foto, documento, clique em botão ou resposta a uma mensagem", em chat privado ou em grupo; lista do que o bot enviou; falhas injetadas (429 com `retry_after`, erro 5xx, conexão cortada). Um `getUpdates` longo novo aborta o anterior com `409 Conflict`, como o real, para os testes da [trava](../guilda/trava-no-github.md#o-409-do-telegram).
- Limites de tamanho de arquivo configuráveis no dublê, com os valores de [anexos-e-transcricao.md](../telegram/anexos-e-transcricao.md).

### GitHub falso

- API REST em memória com o que o motor usa: o repositório da [trava](../guilda/trava-no-github.md) (`POST /user/repos`, commits, criação de ref com `422` para ref existente, `matching-refs` por prefixo com `ETag`), issues e rótulos (inclusive `epico`), sub-issues (pelo `id`, como o real), comentários, PRs, revisões, merge, CI, rulesets, instalações e tokens de instalação (expiram em 1 hora pelo relógio), conversão de manifesto (código vale 1 hora), device flow, `GET /users/<slug>[bot]` e Releases (última release e download dos arquivos, para a [atualização automática](../plataforma/autoatualizacao.md)).
- Falhas injetadas: GitHub fora (conexão cortada, tempo esgotado, `5xx`, `403`/`429` de limite com `retry-after`), na API inteira ou só para uma máquina (partição), por quanto tempo o teste quiser no relógio falso, para o [GitHub fora](../guilda/trava-no-github.md#github-fora).
- Fiel no que importa: `ETag` e `If-None-Match` devolvem 304 sem gastar cota; cabeçalhos de limite por instalação; cada ação registra o App que a fez; um App não aprova o PR que abriu; envio que altera `.github/workflows/` é recusado sem a permissão "Workflows".
- Páginas mínimas de criação e instalação de App que recebem o formulário do manifesto, têm os botões "Create GitHub App" e "Install" e redirecionam com o `code` e com o `installation_id`, para testar o link de [criação de Apps](../github/criacao-de-apps.md) de ponta a ponta com um navegador real. As páginas reais do GitHub são usadas só pelo Dono, no próprio navegador; o Mestre não as automatiza.
- Envio de branch: na completa, a cópia de um [projeto de treino](projetos-de-treino.md) faz o papel do remoto; na rápida, a camada de envio é uma interface falsa.

### Telegram e GitHub na pesada

- Os mesmos dublês rodam como servidores HTTP no hospedeiro do [laboratório](testes.md#laboratório), com TLS pela CA de teste, para servir às quatro composições e à troca de Cidadela.
- Binários compilados com a tag `full` (os que o laboratório instala) aceitam `MESTRE_TEST_TELEGRAM_URL` e `MESTRE_TEST_GITHUB_URL` para trocar a URL base das duas APIs. Binários de produção não contêm esse código.
- O teste controla o lado do usuário (mensagens, cliques, revisões humanas) pelo mesmo controle da completa.

### Rede falsa

- Rede em memória: `Listen` e `Dial` por nome, sem sockets, funciona dentro do synctest; `Host(<nome>)` é a rede vista de uma máquina, no formato da rede do `transport`. TLS real roda por cima, com uma CA de teste ou a da Guilda, então mTLS e a [entrada na Guilda](../guilda/guilda.md#entrada-na-guilda) são testados de verdade. Como no TCP, a primeira escrita depois que o outro lado fechou é aceita e perdida, e as seguintes falham com reset. Os prazos de leitura seguem o relógio da rede; com `net/http` por cima, use o relógio real (o da bolha do synctest).
- Falhas injetadas: latência (pelo relógio), conexão cortada, partição entre dois pontos, conexão meio aberta (nada passa, nem o aviso de fim), perda de pacotes UDP, troca do IP de uma máquina e máquina isolada da rede inteira (para o [GitHub fora](../guilda/trava-no-github.md#github-fora) na Cidadela isolada).
- Para a [descoberta](../conectividade/descoberta.md): UDP em memória com porta compartilhada entre soquetes (o broadcast chega a todos, inclusive aos da própria máquina; o unicast, a um só), `BlockBroadcast` (o firewall), perda dos próximos datagramas, e mDNS falso (`AnnounceMDNS`, `WatchMDNS`, `BlockMDNS`); `SetIP` dá um IP à máquina e o troca, reiniciando as conexões. `internal/testkit/discorig` liga a descoberta real a isso e anuncia um serviço como o Guardião faz.
- `http.RoundTripper` em memória para os clientes do Telegram, do GitHub e do whisper-server.

### Outros dublês

- **PTY falso:** par de terminal em memória com a mesma interface do PTY real; a tela vem do mesmo emulador do [terminal](../arquitetura/terminal.md).
- **Git falso:** `internal/testkit/fakegit` implementa em memória as interfaces `gitx.Git` e `gitx.Repo` (refs, commits com autor e committer, arquivos preparados, bundles, worktrees, `info/exclude` e `skip-worktree` como dados), para a rápida. Os arquivos de um worktree são os que o teste põe com `SetFile`; o falso não cria pastas, só grava e lê os arquivos de bundle. Uma suíte de conformidade (`internal/testkit/gitxsuite`) roda os mesmos cenários no falso, na rápida, e no git real, na completa, para o falso não se afastar do real. Critério que precisa do comportamento real do git vai para a completa ([testes](testes.md#regras-e-erros)).
- **Canal local falso:** `internal/testkit/localrig` liga o lado do motor do [canal local](../arquitetura/canal-local.md) (o servidor real, `internal/localchan`) à rede falsa, com um `/proc` falso (cgroup, pai e horário de início de cada processo), terminais falsos, pares do socket e pidfds que o teste "colhe" quando quer, e Jobs falsos para a identificação do Windows. Quem chama é o nome do processo que disca; a CLI roda em memória por cima disso. O que depende de `SO_PEERPIDFD`, do cgroup ou do Job Object de verdade é da completa.
- **Protocolo falso:** `internal/testkit/protorig` põe o `protocol.Conn` na rede falsa, com o mesmo formato da conexão do `transport` (mensagens de texto e binárias, fechamento com código, identidade do par) e um ping próprio, já que a rede falsa não tem o do `transport`; como no WebSocket, ping e pong só são vistos enquanto a conexão é lida (quem para de ler para de ver os pongs); traz o `outbox` e os comandos processados em memória, a época e o verificador de certidão falsos (assinatura `ok` vale) para a Cidadela e a Torre do [protocolo](../conectividade/protocolo.md) rodarem num teste só. A conexão real entra pelo adaptador `protocol.FromTransport`, testado sobre `transport.Pipe`.
- **Máquina falsa da ficha:** `internal/testkit/sheetfake` implementa todas as sondas da [ficha da máquina](../guilda/ficha-da-maquina.md) (sistema, recursos, sessão gráfica, IPs, provedores e perfis, Operador, navegador, ferramentas, transcrição) com o que o teste manda, `Linux` e `Windows` como pontos de partida, e deixa uma parte falhar (`Fail`) ou mudar depois, mais um `Runner` que responde `--version`; o sistema real é lido só na completa.
- **whisper-server falso:** devolve o texto configurado, erro ou demora.
- **Trava falsa:** `internal/testkit/fakelock` implementa a trava da época (`guild.Lock`, [época e fencing](../guilda/cidadela-e-torres.md#época-e-fencing)) em memória: as refs `epoca-N` (criar uma que existe não vence), a trava fora para a Guilda inteira ou para uma máquina, uma conferência presa até o teste soltar (`Hold`, `Release`) e as contagens de conferências e de tentativas por máquina.
- **Processos da completa:** além do relógio, binários com a tag `full` aceitam `MESTRE_TEST_LOCK=<pasta>` (a trava falsa dos processos: um arquivo `epoca-N` por época, criado com `O_EXCL`), `MESTRE_TEST_OWNER=accept` (o Dono falso aprova entradas e promoções), `MESTRE_TEST_EXTERNAL=<pasta>` (uma função externa falsa que, pela cerca, registra quando a Cidadela age para fora) e `MESTRE_TEST_CONTROL=<socket>` (estado, promover, troca planejada e consultas ao banco da Guilda, uma linha JSON cada) (`internal/app/testhooks_full.go`).

### Quando cada dublê cresce

O `testkit` nasce no M0 com relógio, rede, PTY, git, o esqueleto do agente falso e o mínimo do Telegram e do GitHub falsos que os critérios deste documento pedem (mensagem de texto, `ETag` e cota); cada dublê ganha o comportamento de um assunto no marco desse assunto: rede e descoberta falsas com o [protocolo](../conectividade/protocolo.md) e a [descoberta](../conectividade/descoberta.md) (M2), o agente falso de cada provedor com o [adaptador](../provedores/adaptador.md) e a [conformidade](../provedores/conformidade.md) (M3), Telegram e GitHub falsos com o [bot](../telegram/bot.md) e os [eventos](../github/eventos.md) (M5); a trava falsa e o relógio compartilhado entre processos entram no M2, com [Cidadela e Torres](../guilda/cidadela-e-torres.md); o isolamento de uma máquina na rede falsa, as refs da trava no GitHub falso e o `409 Conflict` do Telegram falso entram no M5, com a [trava no GitHub](../guilda/trava-no-github.md) ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md)).

O que este documento descreve e ainda não existe entra assim:

| Comportamento | Marco | Com quê |
|---|---|---|
| Eventos de hook além dos cinco que o agente falso dispara (`PermissionRequest`, `PreCompact`, `PostInvocation`…) e o uso e os limites de assinatura (prontos para o Claude Code; os outros dois com o adaptador de cada um) | M3 | [adaptador](../provedores/adaptador.md) e [conformidade](../provedores/conformidade.md) |
| Parâmetros, campos e rotas do Telegram e do GitHub falsos além do mínimo do M0 (botões, `parse_mode`, `state` e `labels` nas issues…) | M5 | [bot](../telegram/bot.md) e [eventos](../github/eventos.md) |
| whisper-server falso | M5 | [anexos e transcrição](../telegram/anexos-e-transcricao.md) |
| Refs da trava no GitHub falso, máquina isolada da rede falsa e `409 Conflict` do Telegram falso | M5 | [trava no GitHub](../guilda/trava-no-github.md) |
| `MESTRE_TEST_TELEGRAM_URL` e `MESTRE_TEST_GITHUB_URL` nos binários `full` | M7 | suíte pesada e [laboratório](testes.md#laboratório) |

## Regras e erros

- Dublês ficam em `internal/testkit/` ([layout do código](../desenvolvimento/convencoes.md)); código de produção não pode importá-lo (linter `depguard`).
- Um dublê implementa só o que o Mestre usa. Rota, método, parâmetro de consulta, campo ou evento que ele não conhece faz o teste falhar com "não implementado no dublê: <rota>", nunca devolve sucesso vazio. A rota vem **antes** das credenciais: uma rota desconhecida sem token não vira um 401 que passa despercebido, e um parâmetro ignorado em silêncio (`?state=closed`) responderia outra pergunta.
- Gravações nunca contêm segredos: a captura mascara tokens e chaves, e um teste da rápida procura padrões de segredo em `testdata/recordings/` (tokens do GitHub, token de bot do Telegram, chaves PEM).
- Provedores reais só na suíte pesada. Telegram e GitHub reais não entram em nenhuma suíte.

## Critérios de aceite

- **CA-DUBLES-01** · *rápida* — **Dado** um heartbeat de 30 s com tempo limite de 90 s rodando dentro do synctest, **quando** o outro lado para de responder, **então** a queda é detectada no tempo falso de 90 s e o teste leva menos de 100 ms reais.
- **CA-DUBLES-02** · *rápida* — **Dado** o relógio falso em 2026-10-12 01:59 no fuso `America/Sao_Paulo` e um disparo diário às 02:00, **quando** o teste avança 1 minuto, **então** o disparo acontece uma vez.
- **CA-DUBLES-03** · *rápida* — **Dado** o agente falso como Claude numa pasta não confiada, **quando** ele inicia, **então** mostra a tela de confiança e não dispara hooks; **dado** a pasta pré-confiada no arquivo da conta, **então** dispara o hook de início com o payload gravado.
- **CA-DUBLES-04** · *rápida* — **Dado** o agente falso como Antigravity e um hook que responde texto que não é JSON, **quando** ele pede uma ferramenta, **então** a ferramenta é bloqueada.
- **CA-DUBLES-05** · *rápida* — **Dado** o agente falso como Codex com um hook já confiado, **quando** o arquivo de hooks muda, **então** ele mostra a revisão de hooks e não roda o hook modificado.
- **CA-DUBLES-06** · *rápida* — **Dado** o Telegram falso, **quando** o teste injeta uma mensagem do usuário 123 em chat privado, **então** ela sai no próximo `getUpdates` com o formato gravado, e a resposta do bot aparece na lista de enviadas.
- **CA-DUBLES-07** · *rápida* — **Dado** o GitHub falso e uma consulta que devolveu `ETag`, **quando** a mesma consulta volta com `If-None-Match` e nada mudou, **então** a resposta é 304 e a cota restante não diminui.
- **CA-DUBLES-08** · *rápida* — **Dado** uma Torre conectada à Cidadela pela rede falsa, **quando** o teste corta a conexão, **então** a Torre reconecta com o backoff configurado, medido no tempo falso.
- **CA-DUBLES-09** · *rápida* — **Dado** um dublê que recebe uma rota, um parâmetro ou um campo que não implementa, com ou sem credencial, **quando** o código o chama, **então** o teste falha com a mensagem de não implementado.
- **CA-DUBLES-10** · *rápida* — **Dado** um arquivo em `testdata/recordings/` com um token de bot do Telegram, **quando** a suíte rápida roda, **então** ela falha apontando o arquivo.
- **CA-DUBLES-11** · *completa* — **Dado** o binário do agente falso chamado `claude` no PATH e uma base `claude-code`, **quando** o motor cria um personagem, **então** o agente roda num PTY real e os hooks chamam o `mestre hook` de verdade.

## Depende de

- [testes.md](testes.md)

## Fontes

- `testing/synctest`: https://go.dev/blog/testing-time
- Fluxo de manifesto de GitHub App: https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest
