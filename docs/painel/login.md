# Login do painel

> **Assunto:** login, assistente de criação do Dono, primeiro acesso de cada usuário, sessões com expiração e PIN em ações sensíveis · **Componente:** Painel · **Atualizado:** 2026-10-26

## Objetivo

Garantir que só usuários autorizados usem o painel, cada um com o seu nível e os seus workspaces. Os usuários e níveis são os mesmos do Telegram; a tabela de níveis é única e mora em [usuários e níveis](../telegram/usuarios-e-niveis.md).

## Comportamento

### Acesso por HTTPS

- O painel é servido pelo motor da Cidadela por HTTPS, na porta padrão de [componentes](../arquitetura/componentes.md#portas), com o certificado da CA interna da Guilda ([segurança](../arquitetura/seguranca.md#tls-e-ca-interna)).
- **Qualquer máquina leva ao painel:** toda máquina da Guilda responde no endereço do painel e redireciona o navegador para a Cidadela atual com `307`, **preservando o caminho e a query** (um link de criação de App ou de aprovação aberto numa Torre chega inteiro). O nome mDNS do painel (`mestre.local`) é publicado só pela Cidadela e muda de máquina junto com ela numa troca de Cidadela ([Cidadela e Torres](../guilda/cidadela-e-torres.md)). Uma Torre nunca serve o painel nem a API.
- **Cidadela suspensa:** responde `307` para a Cidadela atual quando a conhece; senão mostra só uma página "Cidadela suspensa", sem login e sem ações, e não anuncia `mestre.local` ([Cidadela suspensa](../guilda/cidadela-e-torres.md#cidadela-suspensa)).
- Confiar na CA no navegador é feito uma vez por aparelho; o passo a passo está em [primeiro acesso](../operacao/primeiro-acesso.md).
- Qualquer rota do painel ou da API sem sessão válida responde 401 e o navegador vai para a tela de login.

### Assistente do Dono (primeira vez)

1. A instalação que cria a Guilda (a primeira, que vira Cidadela) gera um **código de configuração** de uso único (12 caracteres) e o mostra no terminal da instalação ([instalação e atualização](../plataforma/instalacao-e-atualizacao.md)). Ele vale até ser usado.
2. Enquanto não existe nenhum usuário, o painel abre o assistente em vez do login.
3. O assistente pede: o código de configuração, nome, nome de login, senha (duas vezes) e PIN (duas vezes, regras do PIN em [usuários e níveis](../telegram/usuarios-e-niveis.md#pin-pessoal)).
4. Cria o usuário com nível **Dono**, marca o código como usado e abre a sessão.
5. Em seguida, o painel leva aos próximos passos do primeiro acesso (Apps fixos, Telegram, outras máquinas da Guilda), descritos em [primeiro acesso](../operacao/primeiro-acesso.md).

- Depois que existe um usuário, o assistente não abre mais; a rota dele responde 404.
- Código errado: 5 tentativas por endereço IP a cada 15 min (padrão).
- Perdeu o código antes de usar: o subcomando local `mestre setup-code`, só para root ou administrador na máquina da Cidadela, gera outro e invalida o anterior ([componentes](../arquitetura/componentes.md#mestre)).

### Primeiro acesso de cada usuário

- O Dono cadastra o usuário ([administração](administracao.md)) e o painel gera um **código de primeiro acesso** de uso único, válido por 24 h (padrão). O Dono entrega o código pessoalmente ou por um canal que ele escolher; o Mestre nunca o envia pelo Telegram.
- Na tela de login, "primeiro acesso" pede o código, o nome de login, a senha e o PIN.
- O mesmo mecanismo serve para **redefinir a senha** de alguém: o Dono gera um código novo; a senha antiga deixa de valer e as sessões do usuário acabam.

### Senha

- Mínimo de 12 caracteres, sem outras regras de composição.
- Guardada só como hash argon2id (com sal por usuário). O mesmo hash vai (desde o M7), criptografado, para a réplica dos Guardiões ([cofre](../plataforma/cofre.md#réplica-para-os-guardiões)), para o login da [página reserva](../autocura/pagina-reserva.md).
- Trocar a senha pede a senha atual e encerra as outras sessões do usuário.
- 5 senhas erradas em 15 min para o mesmo login → login bloqueado por 15 min (padrão), com aviso ao Dono.

### Sessões

- Ao entrar, a Cidadela cria uma sessão (tabela `panel_sessions`, [persistência](../arquitetura/persistencia.md)): guarda só o hash do token, usuário, aparelho (agente do navegador), IP, criação e último uso.
- O token vai num cookie `HttpOnly`, `Secure`, `SameSite=Strict`, com caminho `/`.
- **Expiração (padrão):** 12 h sem uso, e no máximo 7 dias desde o login. Depois disso, novo login.
- Toda requisição que muda algo, e a abertura do WebSocket, conferem o cabeçalho `Origin` contra o endereço do painel.
- Quando a sessão acaba, o WebSocket do painel é fechado pela Cidadela.
- **Encerram na hora** todas as sessões do usuário: suspensão, remoção, mudança de nível, redefinição ou troca de senha.
- Cada usuário vê e encerra as próprias sessões; o Dono vê e encerra as de todos.
- O [`mestre attach`](../cli/attach.md) entra pelas mesmas rotas, com as mesmas regras e auditoria; a sessão dele aparece com o aparelho "CLI em <máquina>" e acaba quando o comando sai.

### PIN em ações sensíveis

- Ações sensíveis pedem o PIN **a cada vez**, num campo que aparece depois do botão; não há "lembrar PIN".
- Quais ações pedem PIN: lista única em [PIN pessoal](../telegram/usuarios-e-niveis.md#pin-pessoal).
- Conferência e bloqueio por erros seguem as regras únicas de [PIN pessoal](../telegram/usuarios-e-niveis.md#pin-pessoal). Bloqueio do PIN também encerra a sessão em que os erros aconteceram.
- O servidor exige o PIN na própria requisição da ação; um PIN conferido não vale para outra ação.

### Nível em cada tela

O painel esconde o que o nível não permite, mas quem decide é o servidor, em toda requisição, pela [tabela de níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades).

### Implementação

- **Código:** `internal/panel` (HTTP, sessões, PIN, `/ws/ui`), `internal/users` (senha, códigos, fim das sessões) e as migrações `0049_panel.sql` (`users.login` e `password_hash`, `panel_setup_codes`, `panel_access_codes`, `panel_sessions`, `panel_attempts`) e `0050_user_language.sql` (`users.language`, o idioma do painel que o usuário escolheu; vazio até escolher). Os tipos TypeScript são gerados por `tygo` (`go generate ./internal/panel`) em `web/src/gen/panel.ts`; um teste da suíte rápida falha se o arquivo estiver velho.
- **Montagem:** o painel responde em todo caminho que nenhuma outra rota da 7443 tem (`/`), atrás do `Gate`: a Cidadela que age serve; Torre e Cidadela suspensa fazem o `307`. Cada área registra as rotas em `routes_<area>.go` com `RegisterArea`. Os arquivos do front vêm de um `fs.FS` (`Deps.PanelAssets`; o padrão é o `webdist` embutido).
- **Cookie** `__Host-mestre`. **PIN:** cabeçalho `X-Mestre-Pin` na própria requisição da ação (`Router.Sensitive`); o nível é conferido antes. **Origin:** `https://<Host>` em toda requisição que muda algo e na abertura do WebSocket.
- **Erros:** `{"error": {"code", "params", "message"}}`, o `code` estável (a tradução é do front) e a mensagem em português só de reserva. Códigos: `invalid_credentials` 401, `unauthenticated` 401, `login_blocked` e `rate_limited` 429 (`retry_after_seconds`), `forbidden_level` 403 (`capability`, `need`, `have`), `bad_origin`, `pin_required`, `pin_invalid`, `pin_locked`, `no_pin` e `invalid_code` 403, `password_wrong` 403, `password_too_short` 422 (`min`), `invalid_login` e `invalid_pin` 422, `login_taken` 409, `bad_request` 400, `not_found` 404, `unavailable` 503, `internal` 500.

| Rota | Quem | O que faz |
|---|---|---|
| `GET /api/status` | todos | `{setup_open}`: o front abre o assistente ou o login |
| `POST /api/setup` | todos, enquanto não há usuário | `{code, name, login, password, pin}` cria o Dono e abre a sessão; depois, 404 |
| `POST /api/login` | todos | `{login, password}` abre a sessão |
| `POST /api/first-access` | todos | `{code, login, password, pin?}`: primeiro acesso, ou nova senha após redefinição (sem PIN) |
| `POST /api/logout`, `GET /api/me`, `POST /api/password` | sessão | sair; `{user, session, capabilities, timezone}` (`timezone` é o fuso da Guilda, em que o painel mostra todo horário); `{current, new}` |
| `PUT /api/me/language` | sessão | `{language: "pt-BR"\|"en"}` guarda o idioma do usuário e devolve o `Me`; outro valor é `bad_request` |
| `GET /api/sessions`, `DELETE /api/sessions/{id}` | sessão | as próprias; o Dono encerra qualquer uma |
| `GET /api/admin/sessions` | Dono | as de todos |
| `POST /api/users/{id}/access-code` | Dono, com PIN | `{kind: "first"\|"reset"}` devolve `{code, kind, expires_at}`; o código só aparece nessa resposta |

- **`GET /ws/ui`:** envelope do protocolo sem `machine_id` e `epoch`. O navegador manda `ui.subscribe` e `ui.unsubscribe` com `{topics}`; a Cidadela responde `ui.subscribed {topics, rejected: [{topic, code}]}` (com `reply_to`), manda o retrato como `ui.event {topic, event: "snapshot", data}` e depois cada `ui.event`; um pedido recusado volta como `error` com o corpo de erro acima. As áreas registram o tipo de tópico com `Hub.Provide` (quem pode seguir e o retrato) e publicam com `Hub.Publish` e `Hub.PublishFrame` (frame binário `0x01`). Fila cheia de eventos fecha o socket; a de frames binários perde frames. O fim da sessão fecha com o código `4001`.
- **Auditoria** (`audit_log`, sem senha, PIN, código nem token): `panel.setup`, `panel.login`, `panel.login.failed`, `panel.login.suspended`, `panel.login.blocked`, `panel.logout`, `panel.first_access`, `panel.password.changed`, `panel.session.ended`, `panel.access_code.created`, `panel.origin_refused`, `panel.pin.ok`, `panel.pin.failed`, `panel.pin.required`, `panel.pin.locked`. O nome de login só entra se for de um usuário.
- **Código de configuração:** `users.Service.NewSetupCode` o gera (só guarda o hash); `mestre setup-code` abre o banco da Cidadela e o chama, enquanto a instalação (M7) o mostrará no terminal. O comando recusa agente, quem não é root nem administrador, máquina que não é a Cidadela e Guilda que já tem usuário.
- **Escolhas:** a tentativa de senha é reservada antes de a senha ser conferida (requisições ao mesmo tempo não passam do limite), e uma senha atual errada em `/api/password` conta no mesmo limite; no máximo 3 hashes argon2 ao mesmo tempo, e o excedente recebe `rate_limited`; a troca do posto de Dono encerra as sessões dos dois; o bloqueio do login conta por nome de login, e os erros de código de configuração e de primeiro acesso por IP; o `307` usa `mestre.local` na porta do motor da própria máquina (a Guilda usa uma porta só); a atividade do usuário grava `last_used_at` no máximo uma vez por minuto, porque cada escrita é replicada.

## Regras e erros

- Mensagem de login errado é sempre "usuário ou senha inválidos", sem dizer qual.
- Usuário suspenso tentando entrar → mesma mensagem; a tentativa fica na auditoria.
- Relógio: expirações usam o relógio injetável do motor ([dublês](../qualidade/dubles.md)).
- Toda entrada, saída, falha e ação com PIN fica na auditoria (sem senha nem PIN).

## Critérios de aceite

- **CA-LOGIN-01** · *completa* — **Dado** uma Guilda recém-criada, sem usuários, **quando** abro o painel e informo o código de configuração, **então** o assistente cria o Dono e, depois disso, a rota do assistente responde 404 (MVP-1).
- **CA-LOGIN-02** · *rápida* — **Dado** uma sessão sem uso por 12 h (relógio falso), **quando** chega uma requisição, **então** a resposta é 401 e o WebSocket da sessão é fechado.
- **CA-LOGIN-03** · *rápida* — **Dado** uma ação de cofre sem PIN na requisição, **quando** chega ao servidor, **então** é recusada com "PIN obrigatório" e nada muda.
- **CA-LOGIN-04** · *rápida* — **Dado** um usuário com duas sessões abertas, **quando** o Dono o suspende, **então** as duas sessões deixam de valer na hora.
- **CA-LOGIN-05** · *rápida* — **Dado** 5 senhas erradas em 15 min, **quando** chega a sexta, **então** o login é recusado sem conferir a senha e o Dono recebe aviso.
- **CA-LOGIN-06** · *rápida* — **Dado** uma requisição de mudança com `Origin` diferente do painel, **quando** chega, **então** é recusada.
- **CA-LOGIN-07** · *rápida* — **Dado** um código de primeiro acesso com mais de 24 h, **quando** é usado, **então** é recusado.
- **CA-LOGIN-08** · *completa* — **Dado** uma Guilda com Cidadela A e Torre B, **quando** o navegador abre o endereço do painel em B, **então** é redirecionado para A, e a API em B não responde com dados; **depois** de B ser promovida, o mesmo endereço em A redireciona para B.
- **CA-LOGIN-09** · *rápida* — **Dado** uma Torre e a Cidadela no ar, **quando** o navegador abre na Torre `/github/apps/novo/abc?origem=telegram`, **então** recebe `307` para o mesmo caminho e a mesma query na Cidadela; **dado** uma Cidadela suspensa que não conhece a Cidadela atual, **então** ela mostra só a página "Cidadela suspensa", sem login nem ações.

## Depende de

- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Segurança](../arquitetura/seguranca.md)
- [Persistência](../arquitetura/persistencia.md)
- [Cofre](../plataforma/cofre.md)

