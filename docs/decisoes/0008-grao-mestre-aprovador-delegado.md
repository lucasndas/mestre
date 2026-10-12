# ADR 0008: Grão-Mestre como aprovador delegado

> **Assunto:** por que o Grão-Mestre aprova em nome dos usuários, com política, e como isso chega ao GitHub · **Componente:** Core · **Atualizado:** 2026-10-12

## Status

Aceita, 2026-10-12.

## Contexto

- Muita coisa no Mestre pede aprovação:
  - pontos de aprovação de missão;
  - permissões "pedir aprovação" das classes;
  - PRs e merges;
  - custo extra.
- Se tudo fosse para uma pessoa, o trabalho pararia a cada passo. Se nada fosse, áreas sensíveis passariam sem olhar humano.
- **No GitHub:**
  - O repositório pode exigir revisões aprovadas, e ainda não se sabe se a aprovação de um App próprio conta para isso.
  - Os repositórios ficam na **conta pessoal** do usuário.
  - Em conta pessoal, a proteção de branch clássica **não aceita lista de quem pode ignorar a regra**. Só os rulesets aceitam.
  - Rulesets em repositório **privado** pessoal exigem GitHub Pro.

## Opções consideradas

1. **Toda aprovação vai para um humano.**
   - Prós: controle máximo.
   - Contras: o usuário vira gargalo, e as missões ficam paradas esperando.
2. **O Grão-Mestre aprova tudo.**
   - Prós: fluxo contínuo.
   - Contras: áreas sensíveis (autenticação, pagamentos, migrações, segredos) passam só pelo julgamento de um agente.
3. **Regras determinísticas no motor aprovam sozinhas** (sem LLM).
   - Prós: previsível.
   - Contras: não entende escopo, intenção do pedido nem critérios de aceite. Na prática, mandaria quase tudo para humanos.
4. **O Grão-Mestre como aprovador delegado, guiado por uma política editável, que pergunta aos usuários na dúvida.**
   - Prós:
     - Fluxo contínuo no caso comum.
     - Humanos ficam nas áreas sensíveis e nas dúvidas.
     - Tudo auditado.
   - Contras: depende do julgamento do agente dentro das regras da política.

Para o GitHub, as alternativas foram:

- **(a) Aprovação pelo App "Grão-Mestre".** Se não contar, entra o **plano B**: um ruleset que deixa o App fazer o merge sem a revisão obrigatória.
- **(b) Proteção clássica com lista de quem pode ignorar.** Descartada: não existe em conta pessoal.
- **(c) Pedir a um humano no Telegram.** Usada como último recurso.

## Decisão

- **Papel padrão:** o Grão-Mestre é o **aprovador padrão**, com nível de **aprovador delegado**.
  - Pode aprovar e negar ações pontuais.
  - Não pode criar decretos permanentes nem ampliar o teto do workspace.
- **Política:** uma **política de aprovação** por workspace, com padrão sensato e editável, diz quando ele aprova sozinho e quando pergunta ([política](../aprovacoes/politica.md)).
- **Pergunta a humanos:** segue o [fluxo de aprovação](../aprovacoes/fluxo.md).
- **No GitHub:**
  - Ele aprova com o App "Grão-Mestre" ([ADR 0007](0007-apps-fixos-grao-mestre-e-mestre.md)).
  - Se a aprovação do App não contar, vale o **plano B, só por ruleset** e só com aprovação do usuário.
  - Se nem isso for possível, aquele repositório passa a pedir a aprovação de uma pessoa.
- **Credencial pessoal**, para quando o repositório exige a aprovação de uma pessoa específica:
  - É um **user access token do App "Grão-Mestre"**, obtido pelo **device flow**, que não exige porta aberta. Fica guardado no cofre.
  - Só é usado com delegação por decreto ou com a aprovação daquela pessoa.
  - Detalhes em [aprovação no GitHub](../github/aprovacao-no-github.md).
- **Ordem entre decretos e aprovações:** está em [precedência](../arquitetura/precedencia.md).

## Consequências

- **Positivas:**
  - O caso comum (CI verde, revisão sem pendências, dentro do escopo e do teto) anda sozinho.
  - Os usuários só são chamados quando importa.
- **Negativas:**
  - **Erro de julgamento:** o Grão-Mestre pode errar. As mitigações são:
    - áreas sensíveis sempre perguntam;
    - toda aprovação fica presa à ação e ao estado exatos e expira se a ação mudar;
    - auditoria com o motivo de cada decisão;
    - resumo periódico do que ele aprovou sozinho.
  - **Teste na sessão zero:** a aprovação de App próprio para revisões obrigatórias não tem confirmação oficial. Há indícios de que conta: a Renovate usa Apps aprovadores. Por isso a sessão zero do workspace faz o teste numa branch de teste ([sessão zero do workspace](../sessao-zero/workspace.md)).
  - **Plano B limitado:**
    - Um App como quem ignora a regra pula a revisão; não a satisfaz.
    - Em repositório privado sem GitHub Pro não há ruleset, e o plano B não existe.
  - **Token de usuário:** é mais um segredo de pessoa no cofre, que precisa ser renovado e pode ser revogado por ela.
- **Verificar na implementação:** se o review do App próprio conta e com quais permissões, se `actor_type: Integration` vale como exceção de ruleset em repositório pessoal e se o review com o user access token conta como do usuário. Padrão e plano B de cada um: [aprovação no GitHub](../github/aprovacao-no-github.md#verificar-na-implementação).

## Fontes

- Revisões obrigatórias: https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches
- Bypass só em organização (proteção clássica): https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/managing-a-branch-protection-rule
- Rulesets e quem pode ignorar: https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository , https://docs.github.com/en/rest/repos/rules
- Relato de App como bypass em repo pessoal: https://github.com/cbusillo/codex-skills/issues/692
- Apps aprovadores da Renovate: https://docs.renovatebot.com/key-concepts/automerge/
- Aprovação com GITHUB_TOKEN não conta: https://github.com/orgs/community/discussions/181487
