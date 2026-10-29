# Mensagens por código

> **Assunto:** o motor diz o motivo de uma recusa e o conteúdo de um aviso como código e parâmetros, os catálogos de mensagens do Go em pt-BR e en, a paridade de chaves na suíte rápida, os textos guardados no banco e o formato de datas e números · **Componente:** Motor · **Atualizado:** 2026-10-28

## Objetivo

Tirar do motor as frases prontas para pessoa. O motor diz *o quê* (um código e os parâmetros); quem mostra (painel, Telegram, CLI) escolhe *como*, no idioma de quem lê ([ADR 0028](../decisoes/0028-painel-bilingue-e-marco-m6-mais.md)). De que idioma é cada pessoa em cada canal está em [idioma por canal](idioma-por-canal.md).

## Comportamento

### A mensagem

- Uma mensagem é `i18n.Msg{Code, Params}`: um **código** estável e um mapa de **parâmetros**. O código é `<área>.<motivo>` em inglês e `snake_case` (`decree.scope_required`, `table.already_closed`, `approval.answered_by`); os códigos da API do painel que já existem (`invalid_code`, `pin_required`…, [`internal/panel/errors.go`](../../internal/panel/errors.go)) ficam como estão, sem área.
- Um parâmetro é texto, número inteiro, instante (`time.Time`, que sai no JSON como o objeto `{"instant": "<RFC 3339>"}`, que o cliente formata; um texto nunca vira data, qualquer que seja a forma dele) ou **outra mensagem** (`Msg`), traduzida antes no mesmo idioma: o nome do nível, o rótulo de uma resposta, "o Senhor da Guilda" ou "um Administrador".
- Texto que **não se traduz** (o que um agente ou uma pessoa escreveu, o nome de uma campanha, mesa, máquina ou usuário, um erro de programa externo) entra como parâmetro de texto, já mascarado ([mascaramento](../arquitetura/seguranca.md#mascaramento-em-logs)), e a moldura em volta é que vem do catálogo ("— texto do agente, não verificado —").
- As quatro recusas de hoje, `panel.Refusal`, `decrees.Refusal`, `chain.Refusal` e `github.Refusal`, passam a levar `Msg` no lugar de `Message`; `fmt.Sprintf` de frase para pessoa sai do motor. Cada recusa guarda também o código do *tipo* (`level`, `human_required`…) que o motor e a CLI usam para decidir o que fazer; o código da *mensagem* é o que a pessoa lê. O `Error()` de uma recusa é só o código e os parâmetros, nunca traduzido; para o que fica em auditoria e log, `chain.Refusal.Text()` dá a frase em inglês. O que vai ao log continua em inglês e é escrito à parte.
- Um nível (`users.Level`), um estado e um nível da cadeia (`chain.Level`) são mensagens também (`level.owner`, `approval.state_open`, `approval.level_mesa`): entram como parâmetro e saem no idioma de quem lê.
- Os erros de texto livre que vêm de baixo (o `err.Error()` de um validador de YAML, de um programa externo) entram no parâmetro `detail` de `api.detail` (`{{detail}}`): são do programa, não se traduzem.
- Os canais para pessoa recebem `Msg`, não `string`: o envio do Telegram (`Out`, `OwnerAlerts`, o canal de aprovação), o sino do painel (`notices.Feed.Add`) e os erros do painel (`panel.Err`). Assim o compilador barra frase pronta nova.

### Os catálogos

- Ficam em `internal/i18n/locales/<idioma>/<área>.json` (`pt-BR` e `en`), embutidos no binário com `go:embed`. O formato é o JSON do i18next, o mesmo do painel: `{{nome}}` para parâmetro e os sufixos `_one` e `_other` para plural pelo parâmetro `count`.
- Os arquivos são por área, e a chave de um arquivo começa pelo nome dele (`decree.json` guarda `decree.*`). As áreas de hoje: `api` (o `message` dos erros da API), `approval`, `canvas`, `cli`, `decree`, `github`, `guild`, `level`, `person` (os textos da CLI de pessoa, [idioma por canal](idioma-por-canal.md#cli)), `table` e `workspace`.
- O pacote `internal/i18n` traduz (`Render(lang, msg)`), sem biblioteca de fora: o formato é pequeno e já fixado pelo painel, e uma biblioteca Go de i18n usaria outro formato e dobraria a manutenção. A regra de plural segue a do `Intl.PluralRules` do navegador: em pt-BR, 0 e 1 são `_one`; em en, só 1 é `_one`.
- `Match(tag)` dá en para uma tag que começa com "en" e pt-BR para o resto; `FromAcceptLanguage` olha só o primeiro idioma do cabeçalho. Um parâmetro faltando fica como `{{nome}}`, e uma lista sai separada por vírgula. Idioma desconhecido ou vazio usa pt-BR. Um código sem texto (que a paridade impede) sai como o próprio código, nunca como frase de outro idioma.
- **Painel:** os textos que o painel mostra e que nascem no motor (recusas, avisos do sino, cartões de bloqueio e de aprovação) são gerados do catálogo do Go para `web/src/gen/messages.ts` por `go generate ./internal/i18n`, como o glossário e os tipos da API. As áreas `api` e `person` ficam de fora do gerado: o painel traduz os erros da API pelo código, no catálogo `errors` dele, e o `message` de `api` serve a quem não tem o código. O painel os usa pelo código e não os repete nos catálogos dele, que ficam com os textos próprios das telas ([design](../painel/design.md#idioma-datas-e-números)).

### Paridade e guardas

- Um teste da rápida confere que pt-BR e en têm as mesmas chaves, nenhum texto vazio, os mesmos `{{parâmetros}}` em cada texto e as duas formas de plural onde há `count`. Confere também que todo código declarado no Go tem texto e que todo texto tem código; "declarado" é o literal de código (`<área>.<motivo>`, com a área de um arquivo dos catálogos) passado a `i18n.M`, `Err`, `ErrMsg`, `refuse`, `Refuse`, `human`, `FailMsg` ou no campo `Code` de um `Msg`, então um código nunca se monta por concatenação. Outro confere que o `web/src/gen/messages.ts` gravado é o que o gerador faz hoje.
- O guarda de vocabulário dos catálogos do painel (Campanha, Senhor da Guilda, Companhia; em en, Campaign, Guildmaster, Party) vale também para os do Go. A única exceção, como no painel, é o rótulo do campo do dono de um pedido que bloqueia (`Dono: {{owner}}`; em en, `Owner: {{owner}}`).
- Uma regra das [regras de código](../qualidade/regras-de-codigo.md) recusa literal com letra acentuada de português em `internal/telegram`, `internal/panel`, `internal/notices`, `internal/blocking` e `internal/approvals` fora dos testes, com uma lista curta de exceções justificadas (nomes do glossário em texto para agente).

### Respostas

- **HTTP do painel:** `{"error": {"code", "params", "message"}}`, como hoje, com o código da recusa no `code` (o `refused` com `params.reason` dá lugar ao código da área; `refused`, `decree_refused` e `approval_refused` deixam de existir). O status de uma recusa é 409, ou 422 nas de decreto e de aprovação. `message` é o texto já traduzido no idioma de quem pediu, só para quem não tem o código (um cliente velho): o `Language` do usuário da sessão, senão o primeiro idioma do `Accept-Language`, senão pt-BR. O canal do navegador (`UIRejected` e o `error` do socket) leva código e parâmetros, e o `message` do `error` sai no idioma do usuário do socket.
- **CLI `--json`:** `error` ganha `params` ao lado de `code`, `message` e `hint`. Os códigos de hoje (`level_denied`, `busy`…) não mudam. A mensagem para agente sai do catálogo em en ([idioma por canal](idioma-por-canal.md#cli)), e os `params` são só nomes, números e instantes em RFC 3339 (uma mensagem dentro de um parâmetro vira o texto em en).
- **Protocolo entre máquinas:** `protocol.Error` não muda; o texto dele é técnico, em inglês, e não chega a pessoa.

### Textos guardados

O que fica no banco e alguém lê depois guarda o código e os parâmetros (coluna `msg`, JSON), e cada leitor traduz no idioma dele, na hora de ler:

| Tabela | O que passa a guardar |
|---|---|
| `panel_notices` (sino do painel) | `msg` no lugar de `text` |
| `blocking_notices` (aviso de bloqueio) | `msg` da moldura; `stopped`, `evidence` e o texto do agente seguem como texto, por serem dele |
| `approval_flow` (cartão de aprovação) | `msg` da pergunta, com os campos do agente como parâmetros |
| `panel_chat_progress` (progresso na conversa) | `msg` de cada linha |

- Uma linha gravada antes da migração, só com `text`, é mostrada como está, em pt-BR.
- Código: `notices.Feed.Add` e `Update` guardam o `Msg` em `panel_notices.msg` (e `text` vazio); `blocking.Store` guarda em `blocking_notices.msg` a moldura do aviso (resumo, texto inteiro, responsável, passos e bloco do agente, com o texto do agente como parâmetro já mascarado); `approvals.Flow` guarda o cartão em `approval_flow.msg`; `panelchat.SetProgress` guarda o progresso em `panel_chat_progress.msg`. A migração é `0059_stored_messages.sql`. O sino leva `text` (já no idioma do usuário) e `msg` (código e parâmetros), e só ele é traduzido pela tela, pelo gerado. O cartão de aprovação e o aviso de bloqueio do painel chegam com o texto já traduzido no servidor, no idioma do usuário da sessão (o `Language` dele, ou o idioma guardado do Telegram, ou pt-BR), e a tela os mostra como vêm.
- Um aviso para várias pessoas (Senhor da Guilda e Administradores) é **um** registro e uma tradução por destinatário.
- O que só agente lê (`inbox_messages.body`, os avisos do motor na caixa de entrada, as respostas do Grão-Mestre) não muda nesta etapa.

### Datas e números

- Todo instante sai no fuso da Guilda (`guild.timezone`), com o nome do fuso, como hoje. Em pt-BR, `25/10/2026 23:59`; em en, `2026-10-25 23:59` (o fuso vem depois, `(America/Sao_Paulo, UTC-03:00)`, e os segundos só aparecem quando não são zero; quem monta a mensagem passa o instante já no fuso da Guilda, e em UTC sai "UTC"). Duração e contagem usam a forma do idioma ("3 h", "2 dias" / "3 h", "2 days").

## Regras e erros

- Mensagem nova para pessoa nasce no catálogo nos dois idiomas, no mesmo PR; chave faltando reprova a rápida.
- Código é contrato: mudar o texto não muda o código; trocar o sentido cria código novo.
- Parâmetro de texto livre nunca é interpretado pelo tradutor: `{{x}}` dentro dele sai literal.
- O mesmo `Msg` vai ao log só pelo código e pelos parâmetros já mascarados, nunca traduzido.

## Critérios de aceite

- **CA-MSG-01** · *rápida* — **Dado** um texto em `pt-BR/decree.json` sem a chave em `en/decree.json`, ou com `{{name}}` num e `{{nome}}` no outro, ou com `_one` sem `_other`, **quando** a rápida roda, **então** o teste de paridade falha nomeando o arquivo e a chave.
- **CA-MSG-02** · *rápida* — **Dado** um código declarado no Go sem texto no catálogo, ou um texto sem código declarado, **quando** a rápida roda, **então** falha nomeando o código.
- **CA-MSG-03** · *rápida* — **Dado** `Msg{table.characters, count}` com `count` 0, 1 e 2 e um parâmetro que é outra mensagem (o nível), **quando** se traduz em pt-BR e en, **então** saem "0 personagem", "1 personagem", "2 personagens", "0 characters", "1 character", "2 characters" e o nível no mesmo idioma; um idioma desconhecido sai em pt-BR, e `{{x}}` dentro de um parâmetro de texto sai literal.
- **CA-MSG-04** · *rápida* — **Dado** um usuário com idioma en e um decreto recusado por falta de escopo, **quando** o painel recebe a recusa pelo HTTP, **então** o corpo é `{"error":{"code":"decree.scope_required","params":{…},"message":"<texto em en>"}}`, sem frase em pt-BR.
- **CA-MSG-05** · *rápida* — **Dado** a recusa de CA-MSG-04 e o painel em pt-BR, **quando** a tela mostra o erro, **então** o texto vem de `web/src/gen/messages.ts` pelo código em pt-BR, e não do `message`; **dado** o gerado diferente do que o gerador faz, **então** a rápida falha pedindo o `go generate`.
- **CA-MSG-06** · *rápida* — **Dado** um agente com um comando recusado pelo motor, **quando** chama com `--json`, **então** `error` tem `code`, `params`, `message` e `hint`, o `code` é o de antes e o `message` está em en.
- **CA-MSG-07** · *rápida* — **Dado** um aviso do sino para o Senhor da Guilda em pt-BR e um Administrador em en, com o nome de uma mesa como parâmetro, **quando** cada um abre o sino, **então** cada um lê o aviso no seu idioma, com o nome da mesa igual, e o banco tem um registro só, com `msg` e sem frase.
- **CA-MSG-08** · *rápida* — **Dado** um aviso de bloqueio e um cartão de aprovação com texto de agente, **quando** um usuário em en e outro em pt-BR os leem no painel e no Telegram, **então** a moldura sai em cada idioma e o texto do agente sai igual e mascarado nos dois.
- **CA-MSG-09** · *rápida* — **Dado** uma linha de `panel_notices` gravada antes da migração, só com `text`, **quando** um usuário em en abre o sino, **então** o texto aparece como foi gravado.
- **CA-MSG-10** · *rápida* — **Dado** o instante 2026-10-26T02:59:00Z e a Guilda em `America/Sao_Paulo`, **quando** se traduz uma mensagem com ele em pt-BR e en, **então** saem `25/10/2026 23:59` e `2026-10-25 23:59`, cada um com o fuso.
- **CA-MSG-11** · *rápida* — **Dado** um texto pt-BR do catálogo do Go com "Workspace" ou "Dono", ou um en com "Owner", **quando** a rápida roda, **então** o guarda de vocabulário falha nomeando a chave.
- **CA-MSG-12** · *rápida* — **Dado** `"Mesa pausada."` como literal em `internal/telegram` fora de teste, **quando** as regras de código rodam, **então** falham com o arquivo e a linha; **dado** o literal numa exceção listada, **então** passam.

## Depende de

- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Bot](../telegram/bot.md)
- [Convenções da CLI](../cli/convencoes.md)
- [Fluxo de aprovação](../aprovacoes/fluxo.md)
- [Pedido que bloqueia](../aprovacoes/pedido-que-bloqueia.md)
- [Administração](../painel/administracao.md)
- [Regras de código](../qualidade/regras-de-codigo.md)
