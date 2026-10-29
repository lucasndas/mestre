# Idioma por canal

> **Assunto:** de onde vem o idioma de cada pessoa em cada canal (painel, Telegram, CLI de pessoa), o comando de idioma do Telegram, o que é texto para agente e não muda, e o Grão-Mestre respondendo no idioma do usuário · **Componente:** Motor · **Atualizado:** 2026-10-28

## Objetivo

Cada pessoa lê o Mestre no seu idioma (pt-BR ou en) em qualquer canal, e o Grão-Mestre responde nele. As mensagens em si, código e parâmetros traduzidos pelo catálogo, estão em [mensagens por código](mensagens-por-codigo.md).

## Comportamento

### A regra única

- O idioma guardado do usuário é `users.User.Language` na Cidadela (`pt-BR`, `en` ou vazio quando ele nunca escolheu). É o mesmo para o painel, o Telegram e o Grão-Mestre.
- Sem escolha guardada, cada canal usa a pista que tem (abaixo). Toda pista passa pela mesma função, `i18n.Match`: começa com `en` dá en, e qualquer outra coisa (`pt`, `de`, `C`, vazia) dá pt-BR. É a regra que o painel já usa com o idioma do navegador.
- Uma pista nunca grava `Language`: só a escolha da pessoa grava.

### Painel

- Como hoje ([design](../painel/design.md#idioma-datas-e-números)): o usuário escolhe no menu da conta; sem escolha, vale o do navegador.
- O motor traduz o `message` das respostas pelo `Language` do usuário da sessão; sem ele, pelo `Accept-Language` do pedido; nas telas de entrada, sem sessão, só pelo `Accept-Language`.

### Telegram

- A mensagem a um usuário pareado sai no `Language` dele; sem escolha, no `language_code` que o Telegram manda no último update daquela pessoa (o motor passa a ler o campo e guarda o último junto do usuário do Telegram, sem gravar `Language`); sem nenhum dos dois, em pt-BR.
- Quem ainda não é usuário (um código de pareamento inválido) recebe a resposta pelo `language_code` do update.
- Um aviso para várias pessoas sai traduzido para cada uma.
- **Comando `/idioma`** (com o nome `/language` também, `telegram.LanguageChannel`): mostra o idioma em uso e dois botões, Português e English; o toque grava `Language`, como o seletor do painel, e responde já no idioma novo.
- O menu de comandos do bot é registrado nos dois idiomas (`setMyCommands` com `language_code` `en` e o padrão em pt-BR). Os nomes dos comandos não mudam (`/destravar` continua `/destravar`); só a descrição muda.
- A explicação de termos do glossário nas mensagens ([ajuda](../telegram/bot.md)) usa os nomes e as explicações do idioma da mensagem (`glossario-en.md` em en).

### CLI

Há dois públicos, e só um muda.

- **Agente** (os comandos que os personagens chamam, o `help`, as [skills geradas](../cli/skills-geradas.md) e as instruções da sessão zero): **continuam em inglês**, como mandam as [convenções da CLI](../cli/convencoes.md). O agente lê o texto e os modelos entendem inglês melhor e com menos tokens; mudar a língua mudaria o comportamento dos agentes sem ganho para a pessoa. O locale da máquina não tem efeito. As poucas frases em pt-BR que escaparam para a saída de agente (a trava do Mestre, "bytes digitados") passam a sair do catálogo em en.
- **Pessoa** (`mestre attach`, `mestre setup-code`, os erros de início do `mestre run` no terminal e, no M7, o instalador): no idioma da máquina. No Linux, a primeira variável não vazia de `LC_ALL`, `LC_MESSAGES` e `LANG`; no Windows, o idioma da interface do usuário (o primeiro da lista de `GetUserPreferredUILanguages`; sem resposta da API, vale pt-BR). Tudo passa por `i18n.Match`. Nos testes, a fonte de cada sistema é injetável (`i18n.MachineLanguageFrom` recebe o ambiente e a função que dá a tag da interface), e cada sistema ignora a do outro.
- Os textos da CLI de pessoa ficam na área `person` do catálogo (`internal/i18n/locales/<idioma>/person.json`), que o painel não usa e por isso fica fora do `messages.ts` gerado. `i18n.MachineLanguage()` dá o idioma da máquina; o `error:` e o `hint:` ficam fixos, e a recusa a um agente que tenta rodar um comando de pessoa (`MESTRE_AGENT_ID`) sai sempre em en.
- No `mestre attach`, depois do login, vale o `Language` do usuário que entrou (no `Me` do painel); antes, o da máquina.
- O log do serviço (`mestre run`) continua em inglês: é técnico e lido por quem diagnostica.

### Grão-Mestre

- A mensagem que chega ao Grão-Mestre pela caixa de entrada leva o idioma de quem a mandou: `inbox.Origin` ganha `Language`, e o cabeçalho do motor ([envelope](../orquestracao/esperas-e-caixa-de-entrada.md)) ganha `· idioma en` (ou `pt-BR`).
- Quem preenche: o Telegram, pelo `Language` do usuário ou, sem ele, pelo `language_code` guardado (`users.telegram_language`, atualizado a cada mensagem ou clique), ou pt-BR; o painel, pelo `Language` ou, sem ele, pelo idioma em que o painel está aberto, que vai junto da mensagem (campo `language` do envio e do corpo de `POST /api/chat-choices/{id}`), ou pt-BR. A resposta a uma pergunta (`mestre choose`) também leva.
- As instruções do Grão-Mestre ([sessão zero do personagem](../sessao-zero/personagem.md)) ganham, em inglês como o resto, a regra: *answer each person in the language named by `idioma` in the header of their message; when you write first (`notify`, `choose`), use the language of that person's last message, or Portuguese (pt-BR) when there is none.*
- O texto que o Grão-Mestre escreve é dele e não passa pelo catálogo: o Telegram e o painel o mostram como veio. Só a moldura do motor em volta ("Aviso do Grão-Mestre") é traduzida.
- Os avisos que o motor manda aos agentes pela caixa de entrada (missões, rituais, cadeia, esperas) **não mudam nesta etapa**: são para agente, e o idioma deles não chega à pessoa.

## Regras e erros

- Idioma fora de `users.Languages` no `/idioma` é recusado como no painel (`ErrBadLanguage`), e nada é gravado.
- Pessoa e agente nunca dividem o mesmo texto traduzido: o cartão de aprovação que vai a um usuário sai no idioma dele, e a cópia que vai ao agente aprovador sai em en.
- Nenhum canal adivinha o idioma pelo conteúdo da mensagem; só o Grão-Mestre, por instrução, olha a última mensagem quando escreve primeiro.

## Critérios de aceite

- **CA-IDIOMA-01** · *rápida* — **Dado** três usuários pareados no Telegram falso, um com `Language` en, um sem escolha cujo último update veio com `language_code` `en-US` e um sem escolha com `de`, **quando** o motor manda a cada um o aviso de mesa pausada, **então** os dois primeiros recebem o texto em en e o terceiro em pt-BR, e nenhum `Language` foi gravado.
- **CA-IDIOMA-02** · *rápida* — **Dado** um desconhecido que manda um código de pareamento inválido com `language_code` `en`, **quando** o bot responde, **então** a resposta está em en.
- **CA-IDIOMA-03** · *rápida* — **Dado** um usuário sem escolha, **quando** ele manda `/idioma` e toca English, **então** `Language` fica en, a confirmação já vem em en, o painel abre em en na próxima sessão e um valor fora da lista não grava nada.
- **CA-IDIOMA-04** · *rápida* — **Dado** o bot iniciando, **quando** registra o menu de comandos, **então** o Telegram falso recebe dois `setMyCommands`, um padrão em pt-BR e um com `language_code` `en`, com os mesmos nomes de comando.
- **CA-IDIOMA-05** · *rápida* — **Dado** `LANG=en_US.UTF-8` (Linux) ou a interface em inglês (Windows), **quando** o `mestre attach` pede o login, **então** o pedido está em en; **dado** depois o login de um usuário com `Language` pt-BR, **então** as mensagens seguintes ("terminal encerrado") saem em pt-BR.
- **CA-IDIOMA-06** · *rápida* — **Dado** `LANG=pt_BR.UTF-8`, **quando** um agente chama um comando recusado e o `help`, e a sessão zero gera as skills, **então** a saída, o `help` e as skills são idênticos aos de `LANG=C`, em inglês.
- **CA-IDIOMA-07** · *rápida* — **Dado** `LANG=C` e um `mestre setup-code` sem privilégio, **quando** ele recusa, **então** a mensagem está em pt-BR; **dado** `LC_ALL=en_US.UTF-8` e `LANG=pt_BR.UTF-8`, **então** está em en.
- **CA-IDIOMA-08** · *rápida* — **Dado** uma mensagem ao Grão-Mestre pelo Telegram de um usuário com `Language` en, e outra pelo painel de um usuário sem escolha com o painel aberto em pt-BR, **quando** chegam à caixa de entrada, **então** o `Origin` tem `Language` en e pt-BR e o cabeçalho entregue ao agente tem `idioma en` e `idioma pt-BR`.
- **CA-IDIOMA-09** · *rápida* — **Dado** a sessão zero do Grão-Mestre, **quando** gera as instruções dele, **então** elas têm a regra de responder no idioma do cabeçalho e, ao escrever primeiro, no da última mensagem da pessoa; as de um personagem que não é o Grão-Mestre não têm.
- **CA-IDIOMA-10** · *completa* — **Dado** a Cidadela com o agente falso como Grão-Mestre e um usuário com `Language` en, **quando** ele manda uma mensagem pelo Telegram falso e o agente responde com `mestre reply`, **então** o agente recebeu `idioma en` no cabeçalho, a resposta chega ao usuário como o agente a escreveu e a moldura do motor, se houver, está em en.
- **CA-IDIOMA-11** · *rápida* — **Dado** um cartão de aprovação para um usuário em en e para um agente aprovador, **quando** o motor envia, **então** o usuário recebe o cartão em en e a caixa de entrada do agente recebe a cópia em en, cada um com o texto do agente pedinte igual.

## Depende de

- [Mensagens por código](mensagens-por-codigo.md)
- [Roteamento](../telegram/roteamento.md)
- [Grão-Mestre](../orquestracao/grao-mestre.md)
- [Sessão zero do personagem](../sessao-zero/personagem.md)
- [`mestre attach`](../cli/attach.md)
- [Login](../painel/login.md)
