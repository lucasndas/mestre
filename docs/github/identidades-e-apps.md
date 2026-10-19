# Identidades e GitHub Apps

> **Assunto:** quais Apps existem, com que nome e permissões, e como cada ação sai com o App de quem fez · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Fazer o histórico do GitHub mostrar quem fez cada coisa: todo commit, comentário, atualização de issue, PR, revisão e aprovação sai com o **GitHub App de quem fez**, sem que nenhum agente veja credencial.

## Comportamento

### Apps fixos e Apps por classe

| App | Quem usa | Para quê |
|---|---|---|
| **Grão-Mestre** | o Grão-Mestre de todos os workspaces | criar épicos, revisar, aprovar, fazer merge, configurações do repositório aprovadas (`configuracao_repositorio`), clonar e atualizar o espelho |
| **Mestre** | os Mestres de mesa | abrir e atualizar o PR da mesa, organizar as issues do épico. É separado do "Grão-Mestre" porque o GitHub não deixa ninguém aprovar o próprio PR |
| **um por classe** (ex.: Dev, QA, Revisor) | os personagens da classe | commits, comentários, atualizações de issue e revisões pedidas pelo personagem |

- Os fixos nascem no primeiro acesso ([ADR 0007](../decisoes/0007-apps-fixos-grao-mestre-e-mestre.md)); os de classe, quando surge uma classe que pede App próprio. Todos são preparados pelo Grão-Mestre e criados pelo Dono com um clique, por link ([criação de Apps](criacao-de-apps.md)).
- O App de uma classe é **o mesmo em todos os workspaces** e é instalado em cada repositório onde for usado.
- Como uma classe escolhe o App (`--app novo|herdar:<classe>|<nome>`) e quando o Grão-Mestre nega um App próprio a uma variação: [classes](../classes/classes.md).
- Trocar o App de uma classe só vale para as ações novas; o histórico continua com o App que fez.

### Nomes

- Prefixo configurável, padrão `<login-do-dono>-mestre` (ex.: `lucas-mestre`). Nomes: `<prefixo>-grao-mestre`, `<prefixo>-mestre` e `<prefixo>-<classe>` (ex.: `lucas-mestre-qa`).
- O nome tem no máximo **34 caracteres** e é **único no GitHub inteiro**. O motor normaliza a classe (minúsculas, sem acento, espaços viram `-`) e, se passar do limite, corta o nome da classe. Se o nome já estiver em uso, acrescenta `-2`, `-3`…
- O slug é o nome normalizado pelo GitHub; o bot aparece como `<slug>[bot]`.

### Limite de 100 Apps

- Uma conta pode registrar até **100 Apps**. O banco guarda os Apps do Mestre (tabela `github_apps`, [persistência](../arquitetura/persistencia.md): nome, slug, `app_id`, `client_id`, `bot_user_id`, estado).
- Contagem = Apps do Mestre + outros Apps da conta. Os outros o Dono informa no painel ([administração](../painel/administracao.md#contagem-de-apps)); o Mestre não lê a página de Apps da conta pelo navegador.
- Com 90 ou mais, o painel e o Dono recebem aviso ([administração](../painel/administracao.md)). Com 100, a criação é recusada e o Grão-Mestre vincula a classe nova a um App existente.

### Permissões mínimas

Todas as permissões são de repositório, menos **Repository creation: write** do App "Grão-Mestre", usada só para criar o [repositório da trava](#repositório-da-trava). O webhook fica inativo e nenhum evento é assinado ([eventos](eventos.md)).

| Permissão | Grão-Mestre | Mestre | Classe comum | Para quê |
|---|---|---|---|---|
| Metadata | leitura | leitura | leitura | obrigatória |
| Contents | escrita | escrita | escrita | espelho, envio da branch, merge |
| Issues | escrita | escrita | escrita | épicos, issues, sub-issues, rótulos, comentários |
| Pull requests | escrita | escrita | escrita | abrir e atualizar PR, comentar, revisar, aprovar |
| Checks | leitura | leitura | leitura | ler CI |
| Commit statuses | leitura | leitura | leitura | ler CI por status |
| Administration | escrita | — | — | rulesets e configurações aprovadas ([política](../aprovacoes/politica.md#tipos-de-ação)) |
| Workflows | — | — | só se a classe tiver `git.workflows` `ask` ou `allow` | enviar mudanças no CI (abaixo) |
| Repository creation | escrita | — | — | criar o repositório da trava (abaixo) |
| Secrets | — | — | — | nunca |

O GitHub não tem permissão separada de merge: merge exige Contents escrita, que o "Mestre" e as classes também têm para enviar a branch. Quem garante que só o Grão-Mestre faz merge é o motor ([ações por nível](acoes-por-nivel.md)), já que nenhum agente tem token.

### Tokens

- O motor assina um JWT com a chave privada do App (no [cofre](../plataforma/cofre.md)) e pede um token de instalação (`POST /app/installations/{id}/access_tokens`) **restrito ao repositório da ação** e às permissões que ela usa.
- O token dura 1 hora; o motor guarda em memória e renova 5 minutos antes. Nunca vai para disco, log ou ambiente de agente.
- Cada instalação tem a própria cota (5.000 requisições por hora no mínimo), então cada App tem orçamento separado; a do App "Grão-Mestre" é uma só para todos os workspaces da conta e a trava ([orçamento](eventos.md#orçamento-de-cota)).

### Repositório da trava

- A Guilda tem um repositório **privado** só para a trava de época: `<login-do-dono>/mestre-guilda-<guild_id curto>`. Como a trava funciona (refs `refs/mestre/epoca-N`, aluguel de 60 s, quem vence a promoção, GitHub fora e cota) está em [trava no GitHub](../guilda/trava-no-github.md); aqui só a criação e o acesso.
- **Criação:** na configuração do GitHub, logo depois da instalação do App "Grão-Mestre" ([criação de Apps](criacao-de-apps.md#quando-acontece)), o motor chama `POST /user/repos` (`private: true`, `auto_init: true`, porque não se cria ref em repositório vazio) com o token de instalação desse App. O App ganha acesso automático ao repositório que cria; o motor confere com `GET /installation/repositories` e grava o nome no banco.
- O repositório só guarda o commit inicial, os commits dos pretendentes e as refs da trava; nenhum agente o vê, e o Mestre nunca o apaga sozinho. Sem ele, a Guilda não tem promoção de Cidadela.

### Autoria dos commits

- Os agentes fazem commits locais. O autor e o committer são o bot do App: nome `<slug>[bot]`, e-mail `<bot_user_id>+<slug>[bot]@users.noreply.github.com`.
- `bot_user_id` **não** é o id do App: o motor lê `GET /users/<slug>%5Bbot%5D` → `.id` uma vez e guarda no banco.
- A identidade fica num arquivo de configuração git por personagem (`.mestre/agents/<id>/gitconfig`), apontado por `GIT_CONFIG_GLOBAL` (existe a partir do git 2.32; o [checklist](../plataforma/checklist.md), desde o M7, confere a versão em cada máquina). Esse arquivo inclui o `.gitconfig` global da conta dos agentes, quando há um, define `user.name` e `user.email` e, desde o M7, os `safe.directory` do clone principal e dos worktrees do personagem ([Windows](../arquitetura/windows.md#verificar-na-implementação)). É a **única** fonte da identidade: o motor nunca põe `GIT_AUTHOR_*` nem `GIT_COMMITTER_*` no ambiente, porque essas variáveis venceriam o arquivo e não mudariam com o processo vivo. Assim o motor pode trocar a identidade sem reiniciar o agente (ex.: quando o App da classe fica pronto). Outras variáveis do personagem: [personagem](../sessao-zero/personagem.md).
- Personagem sem classe própria: Mestre da mesa usa o App "Mestre"; Grão-Mestre usa o App "Grão-Mestre".
- A mensagem de commit cita a mesa e a issue, com trailers no fim: `Mestre-Mesa: epico/42-login` e `Refs: #57`. A skill ensina o formato ([skills geradas](../cli/skills-geradas.md)).
- **Commits sem assinatura:** não aparecem como "Verified" ([ADR 0014](../decisoes/0014-commits-sem-assinatura.md)).

### App temporário "Mestre" com [Classe]

Enquanto o App de uma classe está em criação, as ações dela saem com o App "Mestre", e o trabalho não trava:

- comentários, revisões e textos de issue e PR começam com **[QA]** (o nome da classe em negrito e colchetes);
- commits saem com o autor do App "Mestre" e o trailer `Mestre-Classe: QA`, sem mudar a primeira linha da mensagem (para respeitar as convenções do projeto);
- quando o App fica pronto, o motor reescreve o `gitconfig` dos personagens da classe, e as ações novas saem com o App próprio. O que já foi feito não é reescrito.

### Workflows

- O GitHub só aceita o envio de uma branch que altera `.github/workflows/` (o CI do repositório) se o App tiver a permissão **Workflows**. Sem ela, recusa ("refusing to allow a GitHub App to create or update workflow … without `workflows` permission").
- Quem pode mexer no CI é decidido **na classe** (escolha registrada no [ADR 0007](../decisoes/0007-apps-fixos-grao-mestre-e-mestre.md): em vez de um App dedicado, a permissão acompanha o App da classe), pela opção `git.workflows` do [`permissions.yaml`](../classes/permissoes.md), limitada pelo [teto](../classes/teto-do-workspace.md) (padrão `never`). O App da classe acompanha essa escolha:
  - `never`: o App é criado **sem** a permissão Workflows;
  - `ask` ou `allow`: o App é criado **com** a permissão. Como a chave do App nunca chega ao agente, a permissão é só a capacidade técnica; quem decide se ela é usada é o motor, pela regra da classe (`ask` = só depois do "Aprovar" de um usuário, no hash aprovado).
- O Grão-Mestre e o "Mestre" não têm a permissão. Uma classe que **herda** o App de outra (`herdar:<classe>`) só pode ter `git.workflows` igual ou menor que a da classe de origem; se precisar de mais, recebe App próprio ([classes](../classes/classes.md)).
- Antes de enviar, o motor confere os arquivos alterados. Mudança em `.github/workflows/` com classe `never`: o motor não envia, explica o motivo ao Grão-Mestre, que pergunta aos usuários se querem mudar a classe ou passar a tarefa a uma classe que pode.
- Mudar uma classe de `never` para `ask`/`allow` acrescenta a permissão ao App já instalado: o Grão-Mestre manda ao Dono o link da página de permissões do App e o do aceite na instalação ([criação de Apps](criacao-de-apps.md#repositórios-novos-e-permissões-novas)). Até o aceite, o envio sai com erro `workflow_permission` ([coordenação no GitHub](../cli/coordenacao-github.md)).

## Regras e erros

- Nenhum agente recebe chave, token, `gh` ou credencial. Só o motor usa o cofre ([segurança](../arquitetura/seguranca.md)).
- App sem instalação no repositório da ação: o pedido falha com "App `<nome>` não instalado em `<repo>`"; o motor acrescenta o repositório à instalação ou o Dono recebe o link para isso ([criação de Apps](criacao-de-apps.md#repositórios-novos-e-permissões-novas)).
- Falha ao ler `bot_user_id` de um App de classe: os personagens usam a identidade temporária do App "Mestre" (seção acima), o motor tenta de novo a cada início de personagem e o [checklist](../plataforma/checklist.md) (desde o M7) registra o alerta.
- Token recusado (chave revogada ou App apagado): o App vai para o estado "com erro" e o Grão-Mestre avisa o Dono.
- **Implementação:** `internal/github` (nomes, `Registry` dos Apps e das instalações no banco da Guilda, `Identities`, `Tokens`, `CheckPush` e `Lair`). O `Registry` é também o `classes.AppDirectory` das classes. A chave de cada App é lida do cofre só para assinar o JWT (`github_app_key:<nome>`); o `Token` não se imprime nem entra em log. O repositório da trava usa o final do `guild_id` (a parte aleatória do ULID) como "curto" e precisa ser privado; o token pede sempre a lista de permissões e só sai para um App `pronto`. O comando `mestre pr update` e o `Builder` da sessão zero do personagem, que usa a `Identity`, são das etapas 3 e 4.

## Critérios de aceite

- **CA-GH-ID-01** · *rápida* — **Dado** a classe "QA de integração com APIs externas" e o prefixo `lucas-mestre`, **quando** o motor gera o nome do App, **então** o nome tem até 34 caracteres, só minúsculas, números e `-`, e começa com `lucas-mestre-`.
- **CA-GH-ID-02** · *rápida* — **Dado** o App `lucas-mestre-qa` com `bot_user_id` 123, **quando** um personagem QA inicia, **então** o `gitconfig` dele tem `user.name = lucas-mestre-qa[bot]` e `user.email = 123+lucas-mestre-qa[bot]@users.noreply.github.com`, e o ambiente dele tem `GIT_CONFIG_GLOBAL` e nenhuma `GIT_AUTHOR_*` ou `GIT_COMMITTER_*`.
- **CA-GH-ID-03** · *rápida* — **Dado** a classe QA com App "em criação", **quando** um personagem QA comenta numa issue, **então** o comentário sai com o token do App "Mestre" e o texto começa com `**[QA]**`.
- **CA-GH-ID-04** · *rápida* — **Dado** o App da classe QA passando a "pronto", **quando** o motor processa a mudança, **então** o `gitconfig` dos personagens QA vivos passa a ter a identidade do App QA.
- **CA-GH-ID-05** · *rápida* — **Dado** uma branch que altera `.github/workflows/ci.yml` pedida por uma classe com `git.workflows: ask`, **quando** a mesa pede `mestre pr update`, **então** o motor não envia e cria um pedido de aprovação do tipo `envio_workflows`; com `never`, recusa com `workflow_not_allowed` e avisa o Grão-Mestre.
- **CA-GH-ID-06** · *rápida* — **Dado** 100 Apps contados, **quando** o Grão-Mestre pede um App novo, **então** a criação é recusada com o motivo.
- **CA-GH-ID-07** · *rápida* — **Dado** um pedido de token para uma ação no repositório X, **quando** o motor pede o token de instalação ao GitHub falso, **então** o pedido lista só o repositório X.
- **CA-GH-ID-08** · *pesada* — **Dado** uma mesa com personagens Dev e QA, **quando** eles fazem commits e comentam, **então** no GitHub (dublê) cada commit e comentário fica registrado com o App da classe de quem fez (MVP-5).
- **CA-GH-ID-09** · *rápida* — **Dado** uma classe nova com `git.workflows: allow` e `app: herdar:qa` (App QA sem a permissão Workflows), **quando** o motor valida a classe, **então** recusa e explica que ela precisa de App próprio.
- **CA-GH-ID-10** · *rápida* — **Dado** o App "Grão-Mestre" recém-instalado e o GitHub falso, **quando** a configuração do GitHub termina, **então** o motor cria `mestre-guilda-<guild_id curto>` privado e com `auto_init`, e grava o nome; **dado** o GitHub falso recusando a criação com `403`, **então** o Dono recebe o link para criar o repositório pelo navegador e nenhuma promoção é possível até o motor confirmar o acesso.

## Depende de

- [Cofre](../plataforma/cofre.md)
- [Persistência](../arquitetura/persistencia.md)
- [Workspaces](../orquestracao/workspaces.md)
- [Classes](../classes/classes.md)
- [Personagem na sessão zero](../sessao-zero/personagem.md)

## Verificar na implementação

- **Verificar na implementação:** o e-mail `<bot_user_id>+<slug>[bot]@users.noreply.github.com` liga o commit ao bot (formato não documentado oficialmente). **Padrão:** o formato acima. **Se falhar:** os commits continuam válidos, com o nome do bot mas sem a ligação ao perfil dele no GitHub; o checklist alerta, e a autoria por papel continua visível no nome do autor e nos trailers ([ADR 0014](../decisoes/0014-commits-sem-assinatura.md)). **Onde:** no primeiro envio real (o PR `mestre/sessao-zero` ou o primeiro PR de mesa), conferindo `author.login` em `GET /repos/{o}/{r}/commits/{sha}`.
- **Verificar na implementação:** ao ganhar a permissão Workflows, o App instalado passa a ter a permissão depois do aceite do dono na instalação. **Padrão:** os dois links acima; o motor confere `permissions.workflows` em `GET /app/installations/{id}` antes de liberar os envios. **Se falhar:** se o pedido de aceite não aparece para um App da própria conta, o Grão-Mestre manda ao Dono o link para desinstalar e instalar de novo o App naquele repositório. **Onde:** à mão, na primeira classe de teste mudada de `never` para `ask`.

- **Verificar na implementação:** o token de instalação do App "Grão-Mestre" com **Repository creation: write** cria o repositório privado na conta pessoal (`POST /user/repos`), e o App ganha acesso a ele. A documentação oficial lista o endpoint para esse token; discussões antigas da comunidade dizem que não funciona, e um relato recente diz que funciona. **Padrão:** funciona. **Se falhar:** o Mestre manda ao Dono, pelo Telegram e pelo painel, o link para criar o repositório privado pelo navegador, com o nome já preenchido, e o da instalação do App "Grão-Mestre" só nele, como no fluxo dos Apps ([criação de Apps](criacao-de-apps.md)); o motor confere o acesso e segue. **Onde:** na sandbox `lucasndas/mestre-sandbox`, no primeiro acesso de teste.

## Fontes

- https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/registering-a-github-app
- https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app
- https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps
- https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
- https://docs.github.com/en/rest/authentication/endpoints-available-for-github-app-installation-access-tokens
- https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification
- https://jvt.me/posts/2023/04/20/github-app-email-address
