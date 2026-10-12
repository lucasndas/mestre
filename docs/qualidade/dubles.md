# Dublês de teste

> **Assunto:** relógio injetável, agente falso por provedor, Telegram, GitHub e rede falsos, e as gravações que os alimentam · **Componente:** Projeto · **Atualizado:** 2026-10-12

## Objetivo

Permitir que quase todos os testes rodem sem tempo real, sem provedores reais, sem rede e sem cota, como exige a [política de testes](testes.md). Cada dublê imita o comportamento real com os formatos reais gravados; os serviços reais só aparecem na suíte pesada.

## Comportamento

### Relógio injetável

- Interface `Clock` no pacote do relógio (`pkg/clock/`, compartilhado com o Guardião; ver [convenções](../desenvolvimento/convencoes.md)): `Now()`, `NewTimer(d)`, `NewTicker(d)`, `AfterFunc(d, f)`. Implementação real sobre `time`; implementação falsa com `Advance(d)`, `Set(t)` e `BlockUntil(n)` (espera até `n` temporizadores estarem armados, para evitar corrida no teste).
- Todo código do motor que usa tempo (heartbeat, backoff, tempos limite de espera, rituais, expiração de aprovações, intervalo de consulta ao GitHub) recebe o `Clock` por parâmetro.
- **`testing/synctest`** (Go 1.25+) é a primeira escolha para lógica com goroutines e temporizadores dentro de um processo: dentro de `synctest.Test`, o tempo só anda quando todas as goroutines estão bloqueadas, e `synctest.Wait()` espera o sistema assentar. Nesse caso o código usa o `Clock` real, e o tempo falso vem da "bolha".
- **Relógio falso** quando o teste precisa de uma data de calendário (ritual diário às 02:00, fuso horário, horário de verão), porque a bolha do synctest sempre começa em 2000-01-01 UTC.
- Rede e disco reais não contam como "bloqueio durável" para o synctest: dentro da bolha, use a rede falsa em memória (abaixo).
- **Suíte completa (vários processos):** binários compilados com a tag `full` aceitam `MESTRE_TEST_CLOCK=<socket>`, e o teste avança o relógio de todos os processos por esse canal. Binários de produção não contêm esse código.

### Agente falso (`fakeagent`)

Um único programa que imita Claude Code, Codex e Antigravity. O provedor vem de `--provider claude|codex|antigravity` ou do nome com que foi chamado (cópias chamadas `claude`, `codex`, `agy`, com `.exe` no Windows), assim as [bases](../orquestracao/bases-e-fichas.md) funcionam sem mudança.

- **Dois modos, mesmo código:** na rápida, roda dentro do processo do teste, ligado ao PTY falso; na completa, roda como processo num PTY real (ConPTY no Windows).
- **TUI mínima:** cabeçalho, linha de entrada, indicador de "ocupado" enquanto trabalha e o sinal de "pronto" que a base usa; aceita redimensionamento e entrada crua (`\x03` interrompe, setas movem em menus).
- **Telas de início**, com os textos do provedor imitado: login (quando o perfil de conta não tem credencial falsa), confiança da pasta e, no Codex, a revisão de hooks. O agente falso lê **os mesmos arquivos de confiança** que o real, então o teste prova que a pré-confiança do motor funciona. Regras de cada provedor: [claude-code.md](../provedores/claude-code.md), [codex.md](../provedores/codex.md), [antigravity.md](../provedores/antigravity.md).
- **Instruções e skills:** lê o que o real leria (arquivo do projeto, arquivo extra pela flag ou configuração do provedor, pastas de skills) e responde ao comando `/fake-dump` com um JSON do que recebeu. É assim que testes rápidos conferem a entrega da camada do Mestre ([personagem.md](../sessao-zero/personagem.md)).
- **Hooks:** lê o arquivo de hooks do provedor, chama os comandos configurados com os payloads gravados (preenchendo `cwd`, ids de sessão, ferramenta e entrada) e aplica a resposta com a semântica do provedor: no Claude e no Codex, só exit 2 ou decisão explícita bloqueia e falha libera; no Antigravity, falha, saída inválida ou ausência de `allow` bloqueia; no Codex, hook novo ou modificado só roda depois de confiado. O contrato está em [adaptador.md](../provedores/adaptador.md).
- **Uso e limites:** emite o uso no formato do provedor (JSON da statusLine, eventos `token_count` no JSONL da sessão, `/usage` em JSON) e simula limite de assinatura (no Claude, `StopFailure` com `rate_limit`). Ver [limites-de-uso.md](../plataforma/limites-de-uso.md).
- **Roteiro:** um arquivo YAML diz, para cada prompt recebido (expressão regular), os passos: `say` (responde texto), `tool` (pede uma ferramenta, disparando os hooks de antes e depois), `edit` (altera um arquivo), `run` (roda um comando, ex. `mestre done "..."`, usando a CLI real como um agente faria), `wait` (pelo relógio), `hang` (fica parado sem sinalizar) e `crash` (encerra com erro). Sem roteiro, ecoa o prompt e chama `mestre done`.

### Gravações

- Ficam em `internal/testkit/testdata/recordings/<origem>/<versão>/`, um arquivo JSON por evento ou resposta, com versão e data de captura no próprio arquivo.
- **Provedores:** capturadas dos CLIs reais pelo [teste de conformidade](../provedores/conformidade.md). Quando ele encontra um campo diferente do gravado, falha apontando o campo e grava a versão nova para revisão em PR.
- **Telegram e GitHub:** formatos tirados da documentação oficial e de respostas reais da suíte pesada.
- O agente falso usa a gravação mais nova por padrão; o teste pode fixar uma versão.

### Telegram falso

- Servidor da Bot API em memória com só o que o [bot](../telegram/bot.md) usa: `getUpdates` (long polling, com o tempo limite pelo relógio), envio e edição de mensagens, botões (`answerCallbackQuery`), envio de documentos e `getFile` com download.
- Controle para o teste: "usuário X manda texto, voz, foto, documento, clique em botão ou resposta a uma mensagem", em chat privado ou em grupo; lista do que o bot enviou; falhas injetadas (429 com `retry_after`, erro 5xx, conexão cortada).
- Limites de tamanho de arquivo configuráveis no dublê, com os valores de [anexos-e-transcricao.md](../telegram/anexos-e-transcricao.md).

### GitHub falso

- API REST em memória com o que o motor usa: issues e rótulos (inclusive `epico`), sub-issues (pelo `id`, como o real), comentários, PRs, revisões, merge, CI, rulesets, instalações e tokens de instalação (expiram em 1 hora pelo relógio), conversão de manifesto (código vale 1 hora), device flow e `GET /users/<slug>[bot]`.
- Fiel no que importa: `ETag` e `If-None-Match` devolvem 304 sem gastar cota; cabeçalhos de limite por instalação; cada ação registra o App que a fez; um App não aprova o PR que abriu; envio que altera `.github/workflows/` é recusado sem a permissão "Workflows".
- Páginas mínimas de criação e instalação de App que redirecionam com o código, para testar a parte do motor no fluxo de [criação de Apps](../github/criacao-de-apps.md). A automação da tela real só é testada na pesada.
- Envio de branch: na completa, um repositório git local faz o papel do remoto; na rápida, a camada de envio é uma interface falsa.

### Rede falsa

- Rede em memória: `Listen` e `Dial` por nome, sem sockets, funciona dentro do synctest. TLS real roda por cima, com uma CA de teste, então mTLS e [pareamento](../conectividade/pareamento.md) são testados de verdade.
- Falhas injetadas: latência (pelo relógio), conexão cortada, partição entre dois pontos, conexão meio aberta (sem aviso de fim), perda de pacotes UDP e troca do IP do Core.
- Broadcast UDP e mDNS falsos para a [descoberta](../conectividade/descoberta.md).
- `http.RoundTripper` em memória para os clientes do Telegram, do GitHub e do whisper-server.

### Outros dublês

- **PTY falso:** par de terminal em memória com a mesma interface do PTY real e uma tela virtual (para `mestre check`).
- **whisper-server falso:** devolve o texto configurado, erro ou demora.
- **Navegador dedicado falso:** devolve resultados prontos (sucesso, pedido de 2FA, tela mudou).

## Regras e erros

- Dublês ficam em `internal/testkit/` ([layout do código](../desenvolvimento/convencoes.md)); código de produção não pode importá-lo (linter `depguard`).
- Um dublê implementa só o que o Mestre usa. Rota, campo ou evento que ele não conhece faz o teste falhar com "não implementado no dublê: <rota>", nunca devolve sucesso vazio.
- Gravações nunca contêm segredos: a captura mascara tokens e chaves, e um teste da rápida procura padrões de segredo em `testdata/recordings/` (tokens do GitHub, token de bot do Telegram, chaves PEM).
- Provedores reais, Telegram real e GitHub real só na suíte pesada.

## Critérios de aceite

- **CA-DUBLES-01** · *rápida* — **Dado** um heartbeat de 30 s com tempo limite de 90 s rodando dentro do synctest, **quando** o outro lado para de responder, **então** a queda é detectada no tempo falso de 90 s e o teste leva menos de 100 ms reais.
- **CA-DUBLES-02** · *rápida* — **Dado** o relógio falso em 2026-10-12 01:59 no fuso `America/Sao_Paulo` e um disparo diário às 02:00, **quando** o teste avança 1 minuto, **então** o disparo acontece uma vez.
- **CA-DUBLES-03** · *rápida* — **Dado** o agente falso como Claude numa pasta não confiada, **quando** ele inicia, **então** mostra a tela de confiança e não dispara hooks; **dado** a pasta pré-confiada no arquivo da conta, **então** dispara o hook de início com o payload gravado.
- **CA-DUBLES-04** · *rápida* — **Dado** o agente falso como Antigravity e um hook que responde texto que não é JSON, **quando** ele pede uma ferramenta, **então** a ferramenta é bloqueada.
- **CA-DUBLES-05** · *rápida* — **Dado** o agente falso como Codex com um hook já confiado, **quando** o arquivo de hooks muda, **então** ele mostra a revisão de hooks e não roda o hook modificado.
- **CA-DUBLES-06** · *rápida* — **Dado** o Telegram falso, **quando** o teste injeta uma mensagem do usuário 123 em chat privado, **então** ela sai no próximo `getUpdates` com o formato gravado, e a resposta do bot aparece na lista de enviadas.
- **CA-DUBLES-07** · *rápida* — **Dado** o GitHub falso e uma consulta que devolveu `ETag`, **quando** a mesma consulta volta com `If-None-Match` e nada mudou, **então** a resposta é 304 e a cota restante não diminui.
- **CA-DUBLES-08** · *rápida* — **Dado** um nó conectado ao Core pela rede falsa, **quando** o teste corta a conexão, **então** o nó reconecta com o backoff configurado, medido no tempo falso.
- **CA-DUBLES-09** · *rápida* — **Dado** um dublê que recebe uma rota que não implementa, **quando** o código a chama, **então** o teste falha com a mensagem de não implementado.
- **CA-DUBLES-10** · *rápida* — **Dado** um arquivo em `testdata/recordings/` com um token de bot do Telegram, **quando** a suíte rápida roda, **então** ela falha apontando o arquivo.
- **CA-DUBLES-11** · *completa* — **Dado** o binário do agente falso chamado `claude` no PATH e uma base `claude-code`, **quando** o motor cria um personagem, **então** o agente roda num PTY real e os hooks chamam o `mestre hook` de verdade.

## Depende de

- [testes.md](testes.md)
- [adaptador.md](../provedores/adaptador.md)
- [conformidade.md](../provedores/conformidade.md)
- [bot.md](../telegram/bot.md)
- [eventos.md](../github/eventos.md)
- [protocolo.md](../conectividade/protocolo.md)
- [descoberta.md](../conectividade/descoberta.md)

## Fontes

- `testing/synctest`: https://go.dev/blog/testing-time
- Fluxo de manifesto de GitHub App: https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest
