# ADR 0014: Commits sem assinatura

> **Assunto:** por que os commits dos agentes saem com o autor do bot do App, mas sem assinatura · **Componente:** Core · **Atualizado:** 2026-10-12

## Status

Aceita, 2026-10-12.

## Contexto

- Os agentes fazem **commits locais** na branch da mesa. Só o motor envia a branch para o GitHub ([ADR 0005](0005-motor-executa-github-grao-mestre-porteiro.md)).
- O objetivo é que o GitHub mostre o App da classe como autor de cada commit (MVP-5).
- **Assinatura de commits de bot:** o GitHub só marca como **"Verified"** commits criados pela própria API, autenticada como o App, **sem** autor, committer ou assinatura personalizados:
  - pela Git Data API;
  - pela mutação GraphQL `createCommitOnBranch`.
- Um commit local assinado com uma chave qualquer não é verificável, porque o App não tem chave GPG ou SSH registrada.
- O e-mail do bot segue um formato que o GitHub **não documenta oficialmente**.

## Opções consideradas

1. **Commits locais sem assinatura, com o autor do bot do App.**
   - Prós:
     - Fluxo git normal para os agentes.
     - Os hashes não mudam no envio.
     - O GitHub liga o commit ao bot pelo e-mail.
   - Contras:
     - Não aparece "Verified".
     - Falha se um ruleset exigir commits assinados.
2. **Commits locais assinados com chave própria do Mestre.**
   - Prós: commits assinados localmente.
   - Contras: o GitHub não consegue verificar, porque a chave não pertence a um usuário. Aparece "Unverified", que é pior que sem assinatura.
3. **Recriar os commits pela Git Data API no envio** (árvore, commit e referência criados pelo App).
   - Prós: saem "Verified".
   - Contras:
     - Os hashes mudam no envio, então o espelho e os worktrees precisam se realinhar.
     - Mais chamadas de API por commit.
4. **GraphQL `createCommitOnBranch`.**
   - Prós: sai "Verified".
   - Contras:
     - Só acrescenta a uma branch existente.
     - Exige o hash esperado da cabeça.
     - Não suporta modos de arquivo, links simbólicos nem submódulos.
     - Também muda os hashes.

## Decisão

- **Commits locais sem assinatura.** O autor e o e-mail são os do **usuário bot do App** da classe (ou do App "Mestre", para o Mestre da mesa). O formato e a forma de obter o id do bot estão em [identidades e Apps](../github/identidades-e-apps.md).
- **Ruleset que exige assinatura:** se um ruleset do repositório exigir commits assinados, o motor explica o motivo e o Grão-Mestre avisa os usuários. Nada é contornado ([ações por nível](../github/acoes-por-nivel.md)).
- **Por que não a opção 3:** recriar os commits no envio troca os hashes entre o espelho, os worktrees e o GitHub ([ADR 0013](0013-sincronizacao-de-worktrees-por-git-bundle.md)) e multiplica as chamadas de API, em troca só do selo "Verified"; a autoria por classe já aparece sem ele.

## Consequências

- **Positivas:**
  - Implementação simples.
  - O histórico local e o do GitHub têm os mesmos hashes.
  - A autoria por classe aparece no GitHub.
- **Negativas:**
  - **Sem selo:** nenhum commit dos agentes aparece como "Verified".
  - **Repositórios bloqueados:** repositórios com "exigir commits assinados" não aceitam os envios do Mestre; o Dono decide se tira a regra daquele repositório.
  - **Formato do e-mail:** o formato do e-mail do bot pode mudar sem aviso, segundo uma resposta da equipe do GitHub. Isso quebraria a ligação do commit com o bot, embora o commit continue válido.
- **Ponto em aberto, conferido na implementação** (o e-mail `<id>+<slug>[bot]@users.noreply.github.com` liga o commit ao bot): o item completo está em [identidades e Apps](../github/identidades-e-apps.md#verificar-na-implementação). O Mestre não usa a Contents API para commits, então não há o que verificar sobre commits "Verified" por ela.

## Fontes

- Verificação de assinatura de bots: https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification
- Git Data API: https://docs.github.com/en/rest/git/commits ; relato de "Verified": https://github.com/orgs/community/discussions/50055
- `createCommitOnBranch`: https://docs.github.com/en/graphql/reference/mutations#createcommitonbranch
- E-mail do bot não documentado: https://jvt.me/posts/2023/04/20/github-app-email-address
