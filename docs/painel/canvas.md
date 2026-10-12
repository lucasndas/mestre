# Canvas da mesa

> **Assunto:** canvas infinito da mesa: personagens com terminal ao vivo, conexões, pergaminhos, missões, esperas, ajuda nos termos e digitação no terminal · **Componente:** Painel · **Atualizado:** 2026-10-12

## Objetivo

Mostrar, em tempo real, tudo o que acontece numa mesa: o Mestre da mesa, a party, os pergaminhos e as missões, e permitir agir sobre eles com a mesma API que a CLI usa. Fechar o navegador não interrompe nada ([visão geral](../arquitetura/visao-geral.md)).

## Comportamento

### Tecnologia

- React + TypeScript + Vite, embutido no binário do Core ([componentes](../arquitetura/componentes.md)).
- Canvas: **@xyflow/react 12 (React Flow, MIT)**; terminais: **xterm.js**. O porquê está no [ADR 0017](../decisoes/0017-canvas-react-flow.md).
- Atualizações e fluxo dos terminais chegam pelo WebSocket do navegador ([protocolo](../conectividade/protocolo.md)), autenticado pela sessão ([login](login.md)).

### Elementos

| Elemento | Tipo no React Flow | O que mostra | Dono do comportamento |
|---|---|---|---|
| Personagem | nó personalizado com xterm.js | nome, classe, **selo do nó** onde roda, estado e motivo (ocioso, ocupado, aguardando, aguardando permissão ou cota, travado, interrompido…), ícone de fixado, aviso de monitoramento reduzido | [personagens](../orquestracao/personagens.md) |
| Terminal simples (`spawn`) | igual ao personagem, sem classe | base usada e nó | [personagens](../orquestracao/personagens.md) |
| Pergaminho | nó com editor Markdown | conteúdo ao vivo, editável por pessoas e agentes | [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md) |
| Tomo | nó-grupo de pergaminhos empilhados | título e quantidade; abre ao clicar | [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md) |
| Conexão | aresta contínua | ligação personagem↔personagem, nota↔personagem ou nota↔nota | [conexões](../orquestracao/conexoes.md) |
| Etapa de missão | aresta com seta e número | ordem das etapas; a etapa atual fica destacada; devoluções em seta de volta | [missões](../orquestracao/missoes.md) |
| Espera | aresta **tracejada** de quem espera para o que espera | motivo ("aguardando Revisor", "aguardando 14:00"); impasse em vermelho com o ciclo | [esperas](../orquestracao/esperas-e-caixa-de-entrada.md) |
| Grupo | nó-grupo do React Flow | agrupamento espacial livre, só visual | este documento |

### Canvas infinito

- Zoom, arrastar o fundo, selecionar vários, arrastar elementos e agrupar.
- Posição, tamanho e grupos de cada elemento ficam salvos no banco do Core por mesa. Reabrir o navegador mostra tudo como estava.
- Elementos criados pelo motor (recrutamento, missão) aparecem perto de quem os criou, sem sobrepor (posicionamento automático simples).

### Terminais no canvas

- O PTY tem tamanho fixo, definido pela base (padrão 120 × 36). O xterm.js desenha nesse tamanho e o nó do canvas só **escala** o desenho; redimensionar o nó não muda o PTY.
- Ao conectar ou reconectar, o motor do nó manda o **buffer recente** do terminal (padrão 256 KiB) e depois o fluxo ao vivo. Cliente lento recebe um retrato da tela em vez da fila inteira.
- Para não pesar o navegador: no máximo 8 terminais com renderização WebGL ao mesmo tempo (padrão); os fora da tela ou muito afastados no zoom viram um retrato estático, atualizado a cada 2 s.
- As classes `nodrag`, `nowheel` e `nopan` do React Flow ficam no terminal, para o mouse e a rolagem irem para o xterm.

### Digitar no terminal

- Só **Administrador ou acima** ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)). Para os outros, o terminal é só leitura (cursor oculto, sem foco de teclado).
- A digitação vai direto ao PTY, sem passar pela caixa de entrada. Antes de liberar o teclado num personagem ocupado, o painel mostra o aviso "o agente está trabalhando".
- Cada sessão de digitação fica na auditoria: usuário, personagem, início e fim (o conteúdo digitado não é guardado na auditoria; ele já está no log do terminal).
- O servidor confere o nível de novo a cada mensagem de entrada; o bloqueio no navegador é só conveniência.

### Ações no canvas

Toda ação usa a mesma API do motor que a CLI usa; as regras são as do documento dono.

| Ação | Como | Regras |
|---|---|---|
| Criar conexão | arrastar de um elemento para outro | [conexões](../orquestracao/conexoes.md) |
| Montar ou ajustar missão | arrastar setas de etapa entre personagens e numerar | [missões](../orquestracao/missoes.md) |
| Fixar personagem | ícone de alfinete | [personagens](../orquestracao/personagens.md) |
| Editar pergaminho | editor no próprio nó | [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md) |
| Salvar seleção como grimório e restaurar em outra mesa | botão na barra da seleção | [grimórios](../orquestracao/grimorios.md) |
| Ver e editar rituais da mesa | painel lateral | [rituais](../orquestracao/rituais.md) |

### Ajuda nos termos

- Todo termo do vocabulário (Party, Ficha, Classe, Grimório, Pergaminho, Ritual, Decreto etc.) tem um ícone "?" ao lado; passar o mouse ou tocar mostra a **Ajuda curta**.
- O texto vem do [glossário](../produto/glossario.md), lido no build e embutido no painel. Nenhum texto de ajuda é escrito no código do painel.
- No build, um termo usado na interface sem linha no glossário faz o build falhar.

## Regras e erros

- Ação recusada pelo motor (nível, regra do documento dono) → o canvas desfaz o gesto e mostra o motivo devolvido.
- WebSocket caiu → faixa "reconectando", com backoff; ao voltar, o estado é recarregado do Core.
- Nó do personagem fora do ar → terminal em cinza com "nó sem sinal desde HH:MM"; o retrato fica visível.

## Critérios de aceite

- **CA-CANVAS-01** · *pesada* — **Dado** uma mesa com missão em andamento em dois nós, **quando** abro o canvas, **então** vejo personagens com selo do nó, conexões, pergaminhos ao vivo, setas numeradas com a etapa atual destacada e linhas tracejadas de espera com motivo (MVP-11).
- **CA-CANVAS-02** · *rápida* — **Dado** o glossário com o termo "Party", **quando** o build roda, **então** a ajuda do "?" de Party é exatamente a Ajuda curta da tabela; e um termo sem linha faz o build falhar.
- **CA-CANVAS-03** · *rápida* — **Dado** um Aprovador, **quando** envia entrada para um terminal pelo WebSocket, **então** o servidor recusa e nada chega ao PTY.
- **CA-CANVAS-04** · *completa* — **Dado** um terminal com saída, **quando** o navegador reconecta, **então** recebe o buffer recente e a tela fica igual à do motor.
- **CA-CANVAS-05** · *rápida* — **Dado** uma conexão arrastada que o motor recusa, **quando** a resposta chega, **então** a aresta some e o motivo aparece.
- **CA-CANVAS-06** · *rápida* — **Dado** elementos movidos, **quando** recarrego a página, **então** as posições e grupos são os mesmos (MVP-12).
- **CA-CANVAS-07** · *rápida* — **Dado** 12 terminais visíveis, **quando** o canvas renderiza, **então** no máximo 8 usam WebGL e os demais mostram retrato.

## Depende de

- [Visão geral](../arquitetura/visao-geral.md)
- [Protocolo](../conectividade/protocolo.md)
- [Login](login.md)
- [Personagens](../orquestracao/personagens.md)
- [Conexões](../orquestracao/conexoes.md)
- [Missões](../orquestracao/missoes.md)
- [Esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md)
- [Pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md)
- [Glossário](../produto/glossario.md)

## Fontes

- React Flow: https://github.com/xyflow/xyflow
- Selo de atribuição do React Flow: https://reactflow.dev/learn/troubleshooting/remove-attribution
