# ADR 0015: Épico como issue com rótulo `epico`

> **Assunto:** como marcar o épico de uma mesa num repositório de conta pessoal · **Componente:** Motor · **Atualizado:** 2026-10-13

## Status

Aceita, 2026-10-12. **Termos atualizados em 2026-10-13** pelo [ADR 0022](0022-guilda-cidadela-e-torres.md): Core e nó viram Cidadela e Torre (ou máquina); a decisão não muda.

## Contexto

- Cada mesa é ligada a um **épico** no GitHub. Abaixo dele ficam as issues das missões e as sub-issues das etapas, formando uma árvore visível no GitHub (MVP-5).
- A spec pedia uma "issue do tipo épico".
- **Tipos de issue** só existem em **organizações**.
- Os repositórios do Mestre ficam na **conta pessoal** do usuário.
- **Sub-issues** funcionam em repositórios pessoais:
  - até 8 níveis;
  - até 100 sub-issues por pai;
  - a API REST de sub-issues usa o `id` da issue, não o número.

## Opções consideradas

1. **Tipo de issue "Épico".**
   - Prós: é o recurso nativo para isso, com filtro próprio.
   - Contras: não existe em repositórios de conta pessoal.
2. **Rótulo `epico`.**
   - Prós:
     - Funciona em qualquer repositório.
     - Visível e filtrável no GitHub.
     - Criado por API.
   - Contras: qualquer pessoa com acesso de triagem pode remover ou renomear o rótulo.
3. **Campo num GitHub Project.**
   - Prós: visões ricas.
   - Contras:
     - Exige criar e manter um Project.
     - Exige permissões a mais para os Apps.
     - O épico não fica marcado na própria issue.
4. **Prefixo no título** (ex.: "[Épico] Login").
   - Prós: zero configuração.
   - Contras:
     - Frágil: quem edita o título quebra a marca.
     - Mistura metadado com texto.

## Decisão

- **Épico = issue com o rótulo `epico`.** O motor cria o rótulo no repositório se ele não existir.
- **Hierarquia:** usa **sub-issues** nativas, com épico → issue → sub-issue.
- **Fonte da verdade:** o vínculo entre mesa e épico fica no banco da Guilda. O rótulo serve para a visualização no GitHub.
- **Onde estão os detalhes:**
  - Quando cada nível é criado: [mesas](../orquestracao/mesas.md) e [missões](../orquestracao/missoes.md).
  - Quem pode criar cada nível: [ações por nível](../github/acoes-por-nivel.md).

## Consequências

- **Positivas:**
  - Funciona em repositórios pessoais, públicos ou privados, sem plano pago.
  - A árvore aparece nativamente no GitHub.
- **Negativas:**
  - **Rótulo removido:** se alguém remover o rótulo, a mesa continua funcionando, porque o banco é a fonte. Mas o GitHub deixa de mostrar a issue como épico até alguém recolocar o rótulo.
  - **Ids:** o motor precisa guardar o `id` de cada issue, além do número, para usar a API de sub-issues.
  - **Limites:** 100 sub-issues por pai limita o tamanho de uma missão ou de um épico. Acima disso, o Mestre da mesa precisa agrupar etapas.
  - **Conta pessoal é a premissa:** os repositórios ficam na conta pessoal do Dono, onde o tipo de issue não existe; o rótulo é o único marcador de épico.

## Fontes

- Sub-issues: https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-sub-issues , https://docs.github.com/en/rest/issues/sub-issues
- Tipos de issue (organização): https://docs.github.com/en/issues/tracking-your-work-with-issues/configuring-issues/managing-issue-types-in-an-organization
