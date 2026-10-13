# ADR 0017: Canvas com React Flow e xterm.js

> **Assunto:** por que o painel usa React + TypeScript + Vite, @xyflow/react e xterm.js · **Componente:** Painel · **Atualizado:** 2026-10-13

## Status

Aceita, 2026-10-12. **Termos atualizados em 2026-10-13** pelo [ADR 0022](0022-guilda-cidadela-e-torres.md): Core e nó viram Cidadela e Torre (ou máquina); a decisão não muda.

## Contexto

- **O que o painel mostra:** um **canvas infinito** com zoom, arrastar e agrupar. Nele ficam:
  - personagens com o terminal ao vivo, em que se pode digitar;
  - conexões desenhadas entre personagens e pergaminhos;
  - pergaminhos em Markdown;
  - setas de missão e linhas tracejadas de espera.
- **Como é servido:** pela Cidadela por HTTPS, de qualquer aparelho da rede.
- **Licença:** o Mestre é distribuído sob licença de código disponível, não comercial ([ADR 0018](0018-licenca-polyform-noncommercial.md)). As dependências precisam permitir isso sem chave paga.
- **Entrega:** o build do painel vai embutido no binário `mestre` ([ADR 0001](0001-stack-go.md)).

## Opções consideradas

1. **tldraw SDK.**
   - Prós:
     - Canvas de quadro branco completo (desenho livre, formas, multiusuário).
     - Formas personalizadas podem conter HTML.
   - Contras:
     - Licença própria. Em produção (HTTPS, fora de localhost) **exige chave de licença**: sem ela, registra erros e **para de renderizar após 5 segundos**.
     - Preço comercial só com vendas; a licença hobby é discricionária e com marca d'água.
2. **React Flow (@xyflow/react 12, MIT).**
   - Prós:
     - Licença MIT.
     - Feito para grafos de nós e arestas, que é exatamente personagens e conexões.
     - Nós são componentes React livres.
     - As classes `nodrag`, `nowheel` e `nopan` deixam o xterm receber mouse e rolagem.
   - Contras:
     - Não é um quadro branco.
     - Agrupamento e anotações livres precisam ser montados com nós de grupo.
3. **Canvas próprio** (sobre Canvas 2D, Konva ou Pixi).
   - Prós: controle total.
   - Contras:
     - Muito trabalho: zoom, seleção, arestas, acessibilidade e desempenho.
     - Terminais HTML dentro de um canvas de pixels exigem camadas sobrepostas.

**Terminal:** o **xterm.js** é o padrão de fato para terminais no navegador. Recebe bytes direto do WebSocket e tem addons de ajuste de tamanho e renderização WebGL. Não houve alternativa séria.

## Decisão

- **Frontend:**
  - **React + TypeScript + Vite.**
  - **@xyflow/react 12** para o canvas.
  - **xterm.js** para os terminais dentro dos nós do canvas.
- **Entrega:** o build é **embutido no binário `mestre`** com `embed`.
- **Onde estão os detalhes:**
  - O que o canvas mostra e como se interage: [canvas](../painel/canvas.md).
  - Organização do código TypeScript: [convenções](../desenvolvimento/convencoes.md).

## Consequências

- **Positivas:**
  - Sem custo de licença e sem risco de o canvas parar de renderizar.
  - Personagens e conexões mapeiam direto para nós e arestas.
  - O painel sai junto com o motor, sem servidor web separado.
- **Negativas:**
  - **Duas toolchains:** o build do motor passa a depender do build do painel (Node e Vite) antes do `go build` ([convenções](../desenvolvimento/convencoes.md)).
  - **Limite de WebGL:** navegadores limitam os contextos WebGL ativos (cerca de 16, estimativa). Com muitos personagens, os terminais fora da tela ou com zoom muito afastado viram imagens estáticas.
  - **Zoom:** o xterm mede as células em pixels sem escala. É preciso reajustar o tamanho nos extremos de zoom, ou renderizar o terminal em 1:1 e escalar o contêiner.
  - **Selo de atribuição:** o React Flow mostra um selo de atribuição. Ele pode ser escondido, porque a licença não exige mantê-lo, mas o projeto pede assinatura Pro de quem o remove. Padrão: manter o selo.
  - **Recursos de quadro branco:** desenho livre e anotações não vêm prontos. Isso não é exigido pela spec.

## Fontes

- tldraw, preço e chave de licença: https://tldraw.dev/pricing , https://tldraw.dev/sdk-features/license-key
- React Flow: https://github.com/xyflow/xyflow , https://reactflow.dev/learn/troubleshooting/remove-attribution
