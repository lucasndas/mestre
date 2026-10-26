# Aprovação no GitHub

> **Assunto:** como as aprovações viram reviews no GitHub, quando um review feito direto no GitHub conclui um pedido, o teste do App aprovador, o plano B por ruleset e a credencial pessoal dos usuários · **Componente:** Motor · **Atualizado:** 2026-10-20

## Objetivo

Aplicar no GitHub as aprovações decididas pelo Grão-Mestre ou pelos usuários ([fluxo](../aprovacoes/fluxo.md)), respeitando as regras do repositório e sem nunca contorná-las. Quando decidir aprovar sozinho é assunto da [política](../aprovacoes/politica.md).

## Comportamento

### Quem abre e quem aprova

- O PR de uma mesa é aberto com o App "Mestre" ou o da classe que pediu; a aprovação sai com o App "Grão-Mestre", com o motivo no texto do review ([identidades](identidades-e-apps.md)). Como são identidades diferentes, a regra "ninguém aprova o próprio PR" é respeitada.
- PR aberto pelo próprio App "Grão-Mestre" (ex.: a proposta de `AGENTS.md` ou a pasta `.mestre/` versionada) não pode ser aprovado por ele: sempre vai para aprovação humana.
- Todo review do motor leva `commit_id` = hash aprovado, e o merge leva `sha` = mesmo hash. Se o PR mudou, o GitHub recusa.

### Teste na sessão zero: o review do App conta?

Não há confirmação oficial de que o review de um App próprio conta para as revisões obrigatórias (o do `github-actions[bot]` não conta; o Renovate usa Apps aprovadores). Por isso a [sessão zero do workspace](../sessao-zero/workspace.md) dispara este teste (é a etapa `approval_test`; este documento é a fonte única do passo a passo):

1. O motor lê as regras da branch principal: `GET /repos/{o}/{r}/rules/branches/{branch}` e a proteção clássica da branch.
2. Se nenhuma regra exige revisão, o resultado é `sem_exigencia` e o teste termina.
3. Se exige: o App "Mestre" cria a branch `mestre/teste-aprovacao` com um arquivo de teste e abre o PR "[Mestre] Teste de aprovação (pode ignorar)".
4. O App "Grão-Mestre" envia um review `APPROVE`.
5. O motor lê `reviewDecision` do PR (GraphQL): `APPROVED` → `conta`; qualquer outro valor → `nao_conta`.
6. O PR é fechado sem merge e a branch é apagada, sempre, mesmo se um passo anterior falhar.

O resultado fica no banco, por workspace, com a data e um resumo das regras lidas. O teste roda de novo quando um merge aprovado pelo App é recusado por falta de revisão.

### Plano B: ruleset com o Grão-Mestre na lista de exceção

Se o resultado for `nao_conta`:

- Repositório pessoal **não** aceita lista de exceção na proteção clássica de branch; só **rulesets** aceitam.
- O Grão-Mestre propõe ao Dono um pedido de aprovação do tipo `configuracao_repositorio` (com PIN): criar o ruleset `mestre-aprovacao` na branch principal, contendo **só** a regra de PR com o mesmo número de aprovações exigido hoje, e com `bypass_actors: [{actor_id: <app_id do Grão-Mestre>, actor_type: "Integration", bypass_mode: "pull_request"}]`.
- Quem está na lista de exceção **ignora todas as regras daquele ruleset**, não só a revisão. Por isso o ruleset novo tem só a regra de revisão. As outras regras (CI obrigatório etc.) continuam onde estão, sem exceção. Se a regra de revisão hoje está na proteção clássica ou num ruleset com outras regras, a proposta mostra a mudança completa: tirar a revisão de lá e criá-la no ruleset novo.
- Se o repositório exige review de **code owner** ou a aprovação do **último push**, não há plano B: a exceção ignoraria essas regras do ruleset, então o workspace vai direto ao modo "aprovação humana".
- Mesmo com a exceção, o Grão-Mestre envia o review `APPROVE` antes do merge, para registro.
- Rulesets em **repositório privado** de conta pessoal exigem **GitHub Pro**. Sem isso, ou se o Dono negar, o repositório passa ao modo "aprovação humana": cada merge pede a aprovação de uma pessoa pelo Telegram, aplicada como abaixo.

### Regra da aprovação do push mais recente

- Com "exigir aprovação do push mais recente", quem aprova não pode ser quem fez o último push. Os envios da branch da mesa saem com o App "Mestre" ou o da classe, nunca com o "Grão-Mestre". Por isso o Grão-Mestre nunca atualiza a branch de uma mesa pelo GitHub (nem o botão "Update branch"); conflitos se resolvem na mesa ([ações por nível](acoes-por-nivel.md)).
- Com "descartar aprovações antigas", um push novo derruba a aprovação. Isso já acontece no Mestre: a aprovação está presa ao hash e é invalidada ([fluxo](../aprovacoes/fluxo.md)).

### Aprovação humana aplicada no GitHub

Quando um usuário aprova pelo Telegram ou pelo painel:

| Caso | O que o motor faz |
|---|---|
| Basta uma aprovação qualquer | o App "Grão-Mestre" aprova, com o texto "Aprovado pelo Grão-Mestre após aprovação de <nome> (pedido <id>)" |
| O repositório exige uma pessoa específica (ex.: code owner) e ela tem credencial | review `APPROVE` real dela, com a credencial dela |
| A pessoa exigida não tem credencial | o bot manda o link do PR para ela aprovar no GitHub; o motor percebe o review pelos [eventos](eventos.md) e conclui |

**Delegação:** se a pessoa exigida **delegou por decreto** ao Grão-Mestre ([decretos](../telegram/decretos.md)), e a política deixa o Grão-Mestre aprovar sozinho aquela ação, ele aprova com a credencial dela e o texto "Aprovado pelo Grão-Mestre em nome de <nome>, por delegação". Sem delegação, pede a ela pelo Telegram.

A credencial de uma pessoa nunca é usada sem a delegação dela ou a aprovação dela para aquela ação.

### Review feito direto no GitHub

É o único caminho em que uma aprovação chega por fora do canal autenticado (Telegram e painel), então o motor só a aceita como resposta ao pedido quando **todas** as condições valem:

1. o estado do review é `APPROVED` (não `DISMISSED`, `COMMENTED`, `CHANGES_REQUESTED` nem `PENDING`);
2. o `commit_id` do review é o hash atual do pedido ([fluxo](../aprovacoes/fluxo.md#invalidação)); um review de um commit antigo não conta;
3. o autor é um usuário **cadastrado e ativo** do Mestre, pelo campo `github_login` do cadastro, que não depende da credencial pessoal ([usuários e níveis](../telegram/usuarios-e-niveis.md#cadastro)), com o nível e o workspace que o pedido exige: as mesmas condições do toque no botão ([fluxo](../aprovacoes/fluxo.md));
4. o tipo da conta é `User`, nunca `Bot` nem App;
5. ele tem permissão de escrita **atual** no repositório, lida na hora e não guardada;
6. o tipo do pedido não tem `pin: true`. Com PIN, o review válido fica registrado na auditoria e **não conclui**: o motor pede o PIN a esse usuário pelo canal, e o pedido só conclui com ele.

Um review que falha em qualquer condição é registrado na auditoria com o motivo e **não muda nada**: o pedido continua `aguardando_usuarios`. Um review que cumpre tudo vale como a resposta "aprovar" daquele usuário (entra no quórum como o toque dele) e conclui o pedido. Rótulo no PR nunca autoriza. É a conferência que protege a política: sem ela, qualquer conta com acesso de leitura de review poderia liberar um PR numa área sensível.


### Credencial pessoal: user access token pelo device flow

Opcional por usuário (campo do cadastro em [usuários e níveis](../telegram/usuarios-e-niveis.md#cadastro); o login que ela confere é o `github_login`, que existe mesmo sem ela). Não precisa de porta aberta:

1. No painel (M6) ou no Telegram com `/github`, o usuário escolhe "Conectar GitHub". O motor chama `POST https://github.com/login/device/code` com o `client_id` do App "Grão-Mestre".
2. O painel, ou o bot, mostra o código curto e o link `https://github.com/login/device`; o usuário digita o código no navegador dele e autoriza.
3. O motor consulta `POST https://github.com/login/oauth/access_token` (`grant_type=urn:ietf:params:oauth:grant-type:device_code`) no intervalo indicado pelo GitHub, até receber o token.
4. O motor confere com `GET /user` que o login do GitHub é o do cadastro e guarda o token e o token de renovação no [cofre](../plataforma/cofre.md). Ele renova o token antes de vencer.

- O token só faz o que o usuário **e** o App "Grão-Mestre" podem fazer, e só nos repositórios onde o App está instalado.
- A credencial acaba (token revogado no GitHub, tokens e linha apagados) quando o usuário é removido, suspenso, vira Observador ou fica sem workspace; mudar para outro nível que ainda conecta a mantém.
- Renovação recusada: a credencial fica "expirada", o painel mostra e o bot avisa o usuário para conectar de novo (sem mandar segredo).
- Usuário suspenso: a credencial não é usada. Usuário removido: a credencial é apagada do cofre e revogada no GitHub.

## Regras e erros

- O Mestre nunca contorna proteção do GitHub. Se uma regra não pode ser satisfeita pelos caminhos acima, o Grão-Mestre explica o motivo aos usuários e para.
- Mudar ruleset ou proteção só com a aprovação do tipo `configuracao_repositorio` ([política](../aprovacoes/politica.md#tipos-de-ação)).
- `mestre gh approve` só aprova com uma aprovação vigente para aquele PR e aquele hash: a do próprio Grão-Mestre, quando a política deixa, ou a de um usuário ([o Grão-Mestre como aprovador delegado](../aprovacoes/politica.md#o-grão-mestre-como-aprovador-delegado)).
- Review recusado com 422 (ex.: autor tentando aprovar o próprio PR): o motor registra e o Grão-Mestre pede aprovação humana.

- **Implementação (etapa 5, parte 1):** `github.ReviewProbe` e `sessionzero.GitHubReview` (teste do App), `github.PlanBRuleset` e `approvals.Settings` (plano B e modo de aprovação humana, tabela `github_review_modes`), `approvals.Approver` (`gh approve`, os três casos da tabela e a delegação), `github.CodeOwners` (quem o repositório exige) e `approvals.GitHubReviews` (review feito no GitHub, auditoria em `approval_github_reviews`, PIN pelo canal). Ficam fora e na [ligação de produção](../desenvolvimento/ligacao-de-producao.md): a composição no `mestre run` (poller, sinks, clientes dos Apps).
- **Implementação da credencial pessoal:** `github.Personal` (`internal/github/personal.go`): `Begin` e `Poll` fazem o device flow (o `device_code` fica só na memória, o intervalo e o `slow_down` do GitHub são respeitados), conferem com `GET /user` que a conta é o `github_login` do cadastro (outra conta: o token é revogado e descartado, `ErrLoginMismatch`, CA-GH-APROV-06) e guardam os tokens no cofre (`github_user_token:<usuário>`) e o estado na tabela `github_user_credentials`. `Client` devolve o cliente que age como a pessoa, renovando o token 5 minutos antes de vencer com o segredo de cliente do App; renovação recusada deixa a credencial `expired`, e usuário suspenso, removido, Observador, sem atuar em nenhum workspace ou com `github_login` mudado não tem credencial em uso; uma varredura periódica (`Sweep`) apaga a credencial de quem perdeu o direito, caso a remoção feita junto da mudança tenha falhado. `Remove` revoga no GitHub e apaga. Nenhum token vai para log, auditoria, banco ou erro. `approvals.PersonalCredentials` a entrega ao `approvals.Approver`; `github.Installs` a usa para o `PUT` de [criação de Apps](criacao-de-apps.md#repositórios-novos-e-permissões-novas). O bot (`/github`) e o painel ("Conectar GitHub", `/api/me/github/begin` e `/poll`, [administração](../painel/administracao.md#implementação-usuários-segredos-e-auditoria)) chamam `Begin` e `Poll`; a conta diferente da cadastrada aparece no painel como `github_login_mismatch`.

## Critérios de aceite

- **CA-GH-APROV-01** · *rápida* — **Dado** o GitHub falso com regra de 1 aprovação e `reviewDecision` `APPROVED` após o review do App, **quando** o teste da sessão zero roda, **então** o resultado é `conta`, o PR de teste é fechado sem merge e a branch é apagada.
- **CA-GH-APROV-02** · *rápida* — **Dado** o resultado `nao_conta`, **quando** o Grão-Mestre gera a proposta de ruleset, **então** ela tem só a regra de revisão e o App "Grão-Mestre" como `Integration` na lista de exceção, e fica pendente de aprovação do Dono.
- **CA-GH-APROV-03** · *rápida* — **Dado** um repositório privado sem GitHub Pro, **quando** o plano B é tentado, **então** o workspace passa ao modo "aprovação humana" e o Dono é avisado do motivo.
- **CA-GH-APROV-04** · *rápida* — **Dado** um PR aberto pelo App "Grão-Mestre", **quando** a política permitiria aprovar sozinho, **então** o motor exige aprovação humana.
- **CA-GH-APROV-05** · *rápida* — **Dado** um code owner exigido, sem decreto de delegação, **quando** o Grão-Mestre tenta aprovar com a credencial dele, **então** o motor recusa e cria o pedido para essa pessoa.
- **CA-GH-APROV-06** · *rápida* — **Dado** o device flow com o GitHub falso devolvendo um login diferente do cadastro, **quando** o motor confere, **então** descarta o token e mostra o erro no painel.
- **CA-GH-APROV-07** · *pesada* — **Dado** um PR de mesa aberto pelo App "Mestre", **quando** o Grão-Mestre aprova pela política, **então** o review aparece no GitHub (dublê) com o App "Grão-Mestre" e o merge acontece (MVP-5).
- **CA-GH-APROV-08** · *pesada* — **Dado** um PR que toca área sensível, **quando** o Lucas toca "Aprovar" no Telegram (dublê), **então** a aprovação aparece no GitHub (dublê) e o merge acontece (MVP-7).
- **CA-GH-APROV-09** · *rápida* — **Dado** um pedido `aguardando_usuarios` e um review `APPROVED` feito no GitHub por uma conta não cadastrada, ou com `commit_id` diferente do hash atual do pedido, **quando** o motor recebe o evento, **então** o pedido continua `aguardando_usuarios` e a auditoria registra o motivo; **dado** o review de um usuário cadastrado e ativo, do tipo `User`, com permissão de escrita e o hash certo, **então** o pedido é concluído como se ele tivesse tocado em "Aprovar"; **dado** o mesmo review depois de `DISMISSED`, **então** não conta; **dado** um pedido `configuracao_repositorio` (`pin: true`) e o review válido do Dono, **então** o pedido segue `aguardando_usuarios`, o Dono recebe o pedido do PIN pelo canal e só com o PIN o pedido conclui.

## Depende de

- [Identidades e Apps](identidades-e-apps.md)
- [Fluxo de aprovação](../aprovacoes/fluxo.md)
- [Política de aprovação](../aprovacoes/politica.md)
- [Ações por nível no GitHub](acoes-por-nivel.md)
- [Decretos](../telegram/decretos.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md) (cadastro, nível e `github_login`)
- [Cofre](../plataforma/cofre.md)
- [Sessão zero do workspace](../sessao-zero/workspace.md)
- [Eventos do GitHub](eventos.md)

## Verificar na implementação

As respostas reais conferidas à mão viram gravações do [GitHub falso](../qualidade/dubles.md).

- **Verificar na implementação:** `reviewDecision` reflete as revisões obrigatórias de rulesets (não só da proteção clássica) num repositório pessoal. **Padrão:** reflete, e o teste usa só ele. **Se falhar:** o teste deixa de confiar só em `reviewDecision`: um merge aprovado pelo App e recusado por falta de revisão registra `nao_conta` direto e leva ao plano B. **Onde:** à mão, no repositório [sandbox](../qualidade/projetos-de-treino.md#sandbox-real-no-github), com ruleset de revisão.
- **Verificar na implementação:** o review `APPROVE` do App "Grão-Mestre" conta para as revisões obrigatórias (é o teste acima), com as permissões da [tabela](identidades-e-apps.md#permissões-mínimas). **Padrão:** conta (o Renovate usa Apps aprovadores). **Se falhar:** plano B do ruleset; se ele também falhar, modo "aprovação humana". **Onde:** à mão, no repositório [sandbox](../qualidade/projetos-de-treino.md#sandbox-real-no-github), exigindo revisão.
- **Verificar na implementação:** `actor_type: Integration` é aceito na lista de exceção de ruleset em repositório pessoal (a documentação da API só exclui `OrganizationAdmin` ali), e `bypass_mode: pull_request` permite o merge pela API. **Padrão:** os dois funcionam, como no plano B acima. **Se falhar:** com `pull_request` recusado, a proposta ao Dono passa a usar `bypass_mode: always` (o ruleset só tem a regra de revisão, e o motor nunca envia direto à principal); com `Integration` recusado, modo "aprovação humana". **Onde:** à mão, no primeiro repositório real com resultado `nao_conta`.
- **Verificar na implementação:** o review feito com o user access token aparece como review real do usuário e conta como aprovação dele. **Padrão:** conta. **Se falhar:** a credencial pessoal deixa de ser usada para aprovar; vale o caso "a pessoa exigida não tem credencial" da tabela acima (link do PR pelo Telegram). **Onde:** à mão, com o primeiro usuário que conectar o GitHub pelo device flow.
- **Verificar na implementação:** a permissão atual de escrita do autor de um review, lida pela instalação do App "Grão-Mestre". **Padrão:** `GET /repos/{o}/{r}/collaborators/{login}/permission` funciona com o token de instalação e as permissões da [tabela](identidades-e-apps.md#permissões-mínimas). **Se falhar:** vale o `author_association` do review (`OWNER` ou `COLLABORATOR`, só em repositório de conta pessoal, onde colaborador escreve); qualquer outro valor não conta. **Onde:** à mão, no repositório [sandbox](../qualidade/projetos-de-treino.md#sandbox-real-no-github), com um colaborador e uma conta sem acesso.

## Fontes

- https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches
- https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/reviewing-changes-in-pull-requests/approving-a-pull-request-with-required-reviews
- https://docs.renovatebot.com/key-concepts/automerge/
- https://github.com/orgs/community/discussions/181487
- https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository
- https://docs.github.com/en/rest/repos/rules
- https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/managing-a-branch-protection-rule
- https://docs.github.com/en/rest/pulls/reviews
- https://github.blog/changelog/2023-06-06-security-enhancements-to-required-approvals-on-pull-requests/
- https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app
