# ADR 0018: Licença PolyForm Noncommercial 1.0.0

> **Assunto:** sob que licença o código do Mestre é publicado · **Componente:** Projeto · **Atualizado:** 2026-10-12

## Status

Aceita, 2026-10-12.

## Contexto

- O repositório do Mestre será **público no GitHub**.
- O autor quer:
  - permitir uso, estudo e modificação para fins **não comerciais**;
  - exigir o **aviso de autoria**;
  - reservar o uso comercial, que só pode acontecer **com autorização dele**.
- O texto da licença fica em [LICENSE](../../LICENSE).

## Opções consideradas

1. **MIT ou Apache-2.0.**
   - Prós:
     - Licenças "open source" (OSI), muito conhecidas.
     - Facilitam contribuições e adoção.
   - Contras: permitem uso comercial sem pedir nada, o que contraria o objetivo.
2. **AGPL-3.0.**
   - Prós:
     - Copyleft forte: quem oferece o software como serviço precisa publicar o código.
     - É OSI.
   - Contras: ainda permite uso comercial, desde que o código seja aberto. Não reserva o uso comercial ao autor.
3. **Business Source License 1.1.**
   - Prós:
     - Restringe produção comercial por um período.
     - Converte-se em licença aberta após um prazo fixo.
   - Contras:
     - A conversão automática para licença aberta não é o desejado.
     - Os termos de "uso adicional" precisam ser redigidos caso a caso.
4. **PolyForm Noncommercial 1.0.0.**
   - Prós:
     - Texto padronizado e curto.
     - Permite uso não comercial amplo (pessoal, pesquisa, organizações sem fins lucrativos).
     - Exige repassar os termos e o aviso de autoria.
     - Uso comercial só com licença separada do autor.
   - Contras:
     - Não é "open source" pela definição da OSI.
     - Menos conhecida.
5. **Código público sem licença.**
   - Prós: nenhum.
   - Contras: ninguém teria permissão legal para usar ou modificar, nem para fins pessoais.

## Decisão

- **Licença:** **PolyForm Noncommercial 1.0.0**, no arquivo `LICENSE` da raiz, com a linha de aviso obrigatório (`Required Notice`) identificando o autor.
- **Como chamar:** o projeto se descreve como **"código disponível"**, nunca como "open source".
- **Uso comercial:** só com autorização do autor.
- **Contribuições externas:** o projeto **não aceita contribuições externas de código**; pull requests de terceiros são fechados com um agradecimento e a indicação de abrir uma issue. Issues (erros, ideias, dúvidas) são bem-vindas. Não há acordo de contribuição (CLA).
- **Repositório:** https://github.com/lucasndas/mestre (público).

## Consequências

- **Positivas:**
  - Uso não comercial livre.
  - Autoria preservada.
  - O autor mantém o controle do uso comercial.
- **Negativas:**
  - **Fora de "open source":** o projeto não pode ser chamado de "open source" nem entrar em catálogos que exigem licença OSI. Algumas pessoas e empresas evitam contribuir nesse modelo.
  - **Contribuições externas:** para continuar podendo licenciar comercialmente, o autor precisaria de direitos sobre as contribuições recebidas. Por isso, código de terceiros não entra (seção Decisão).
  - **Dependências:** precisam ter licenças compatíveis com a redistribuição (MIT, BSD, Apache). Bibliotecas com licença paga ou restrita, como o tldraw, ficam fora ([ADR 0017](0017-canvas-react-flow.md)). As licenças das dependências são conferidas antes de adicionar cada uma ([convenções](../desenvolvimento/convencoes.md)).
  - **Detecção pelo GitHub:** o GitHub pode não reconhecer a licença automaticamente e mostrá-la como "Other". Isso é só visual.

## Fontes

- Texto da licença: https://polyformproject.org/licenses/noncommercial/1.0.0/
- Definição de open source (OSI): https://opensource.org/osd
