# ADR 0005: O motor executa o GitHub; o Grão-Mestre é o porteiro

> **Assunto:** por que nenhum agente tem credencial do GitHub · **Componente:** Motor · **Atualizado:** 2026-10-13

## Status

Aceita, 2026-10-12.

## Contexto

- As mesas precisam fazer muita coisa no GitHub: criar e atualizar issues do épico, comentar, abrir e atualizar o PR, responder revisões.
- O Grão-Mestre precisa de ações de repositório: épicos, revisão, aprovação, merge e configurações.
- O GitHub **não permite limitar um token a "só as issues deste épico"**. Um token que edita issues edita todas as do repositório.
- Agentes de IA podem errar ou ser induzidos a erro por conteúdo do repositório ou de anexos.

## Opções consideradas

1. **Dar a cada agente o token de instalação do App da sua classe** (com `gh` ou variável de ambiente).
   - Prós:
     - Pouco código no motor.
     - Os agentes usam `gh` como já sabem.
   - Contras:
     - Qualquer agente consegue mexer em qualquer issue ou PR do repositório.
     - O token pode vazar em logs, arquivos ou prompts.
     - `git push` fica livre.
2. **Token fino (fine-grained) por repositório, entregue às mesas.**
   - Prós: escopo por repositório.
   - Contras:
     - O escopo continua "o repositório inteiro", não o épico.
     - É credencial de pessoa, não do App da classe, e perde a autoria por papel.
3. **O motor executa toda ação no GitHub; os agentes pedem pela CLI.**
   - Prós:
     - O motor confere, para cada pedido, se a ação é permitida àquela mesa e àquela classe.
     - Nenhum segredo chega ao agente.
     - Cada ação sai com o App de quem pediu.
     - Um ponto único de auditoria e de controle de cota.
   - Contras: o motor precisa implementar cada operação que os agentes usam.

## Decisão

- **Ninguém recebe credencial do GitHub.** Nenhum agente, nem o Grão-Mestre, recebe token, `gh` ou credencial.
- **Toda ação no GitHub é executada pelo motor**, a pedido pela CLI:
  - `mestre issue` e `mestre pr`, para a mesa;
  - `mestre gh`, só para o Grão-Mestre.
- **O que cada nível pode fazer:**
  - A mesa só mexe no que é do seu épico.
  - O Grão-Mestre é o único com ações de repositório. Configurações exigem aprovação de usuário.
- **Envio para o GitHub:** agentes só fazem commits locais. Quem envia a branch do espelho para o GitHub é o motor.
- **Onde estão os detalhes:**
  - Tabela de ações por nível: [ações por nível](../github/acoes-por-nivel.md).
  - Comandos: [coordenação](../cli/coordenacao.md) e [CLI do Grão-Mestre](../cli/grao-mestre.md).
  - Onde as credenciais ficam: [cofre](../plataforma/cofre.md).

## Consequências

- **Positivas:**
  - O isolamento entre mesas existe de fato, não só por instrução.
  - Um vazamento de prompt não expõe credencial.
  - Toda ação fica auditável e marcada com o App correto ([ADR 0006](0006-app-por-classe-criado-pelo-navegador.md), [ADR 0007](0007-apps-fixos-grao-mestre-e-mestre.md)).
- **Negativas:**
  - O motor ganha uma superfície grande de código GitHub: issues, sub-issues, PRs, revisões, comentários, CI, envio de branch e configurações. Tudo isso precisa de dublês de teste ([dublês](../qualidade/dubles.md)).
  - Os agentes não podem usar `gh` nem `git push`. Isso é reforçado pelas permissões da classe e pelo despachante de hooks ([ADR 0012](0012-despachante-unico-de-hooks.md)). Uma operação que a CLI não cobre exige evoluir a CLI ou pedir ao Grão-Mestre.
  - O Grão-Mestre vira um gargalo para tudo o que é de nível de repositório e para mexer em outra mesa. Isso é aceito, porque ele não bloqueia: delega e reage à caixa de entrada ([Grão-Mestre](../orquestracao/grao-mestre.md)).
  - A cota da API é consumida pelo motor. Cada instalação de App tem 5.000 requisições por hora, o que, com um App por papel, dá a cada papel uma cota própria.

## Fontes

- Limites de requisição por instalação: https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
