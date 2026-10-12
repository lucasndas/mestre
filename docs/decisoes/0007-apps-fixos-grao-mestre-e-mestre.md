# ADR 0007: Apps fixos "Grão-Mestre" e "Mestre"

> **Assunto:** por que há dois Apps fixos separados e como o motor se autentica com eles · **Componente:** Core · **Atualizado:** 2026-10-12

## Status

Aceita, 2026-10-12.

## Contexto

- O PR do épico é aberto pela mesa e aprovado pelo Grão-Mestre (MVP-5).
- **Regras do GitHub para aprovação:**
  - Quem abre um PR não pode aprová-lo. Um App também não aprova PR que ele mesmo abriu.
  - Com a regra "aprovação do push mais recente", quem aprova também não pode ser quem fez o último push.
- O Grão-Mestre precisa de permissões que nenhuma outra identidade deve ter: merge e administração das configurações aprovadas pelo usuário.
- O motor precisa de credenciais do GitHub que não dependam de uma pessoa logada.

## Opções consideradas

1. **Um App só para a orquestração** (abre e aprova).
   - Prós: um App a menos.
   - Contras: não consegue aprovar o PR que abriu; a regra do GitHub inviabiliza.
2. **Mesa abre com o App da classe; Grão-Mestre aprova com a conta do usuário.**
   - Prós: a aprovação conta como revisão humana.
   - Contras:
     - Toda aprovação usaria a credencial de uma pessoa, o que a spec só permite com delegação ([ADR 0008](0008-grao-mestre-aprovador-delegado.md)).
     - Mistura identidades.
3. **Dois Apps fixos criados na instalação:**
   - **"Grão-Mestre":** revisa, aprova, faz merge, cria épicos e cuida das configurações;
   - **"Mestre":** usado pelos Mestres de mesa para abrir e atualizar o PR e organizar as issues do épico.
   - Prós:
     - Identidades distintas para abrir e aprovar.
     - Permissões de merge e administração ficam só num App.
   - Contras: um App a mais contando no limite de 100.

**Credenciais do motor**, alternativas consideradas:

- **(a) Chave privada do App** (Credentials → Key pairs) no cofre, gerando tokens de instalação de 1 hora.
  - Prós: não depende de pessoa, os tokens expiram sozinhos e cada instalação tem cota própria.
  - Contras: exige assinar um JWT e renovar o token.
- **(b) Token pessoal (PAT) do usuário.**
  - Prós: simples.
  - Contras: age como a pessoa, perde a autoria por papel e não expira sozinho.

## Decisão

- **Apps fixos:** dois, **"Grão-Mestre"** e **"Mestre"**, criados na instalação do Mestre ([criação de Apps](../github/criacao-de-apps.md)).
- **Quem usa qual:**
  - A mesa abre o PR com o App "Mestre" (ou o da classe que pediu).
  - O Grão-Mestre aprova com o App "Grão-Mestre".
- **Último push:** o App "Grão-Mestre" **não faz push** em branches de mesa. O envio da branch é sempre feito com o App de quem pediu, para que a regra "aprovação do push mais recente" seja satisfeita.
- **Permissões:**
  - Só o "Grão-Mestre" tem merge e a administração necessária.
  - Nenhum App tem permissão de segredos. A permissão de workflows só existe no App de classes que a pedem (`git.workflows` `ask` ou `allow`), decisão do usuário em 2026-10-12.
  - Detalhes em [identidades e Apps](../github/identidades-e-apps.md).
- **Credenciais:** o motor usa a **chave privada de cada App**, guardada no [cofre](../plataforma/cofre.md), e gera **tokens de instalação** sob demanda. Nenhum token chega aos agentes.

## Consequências

- **Positivas:**
  - Abrir e aprovar acontecem com identidades diferentes.
  - O poder de merge fica concentrado num só App.
  - Nenhuma credencial de pessoa é usada no dia a dia.
- **Negativas:**
  - **Correção pelo Grão-Mestre:** se o Grão-Mestre quiser corrigir algo no PR, ele não faz isso. Pede à mesa, que faz o push. Isso preserva a validade da aprovação.
  - **Aprovação de App pode não contar:** ainda não se sabe se a aprovação de um App próprio conta para revisões obrigatórias. O plano B e o teste estão no [ADR 0008](0008-grao-mestre-aprovador-delegado.md) e em [aprovação no GitHub](../github/aprovacao-no-github.md).
  - **Arquivos de workflow:** uma mesa que altera `.github/workflows/` só consegue enviar se a classe permitir (`git.workflows`); o App da classe é criado com ou sem a permissão "Workflows" conforme essa escolha ([workflows](../github/identidades-e-apps.md#workflows)).
  - **Tokens de instalação:** o motor precisa renovar tokens de 1 hora e lidar com expiração no meio de uma operação.
  - **Instalação:** os dois Apps entram na contagem de 100 e precisam ser instalados em cada repositório de workspace antes da sessão zero ([sessão zero do workspace](../sessao-zero/workspace.md)).

## Fontes

- Autor não aprova o próprio PR e aprovação do push mais recente: https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/reviewing-changes-in-pull-requests/approving-a-pull-request-with-required-reviews , https://github.blog/changelog/2023-06-06-security-enhancements-to-required-approvals-on-pull-requests/
- Permissão Workflows: https://docs.github.com/en/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app
- Limites de requisição: https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
