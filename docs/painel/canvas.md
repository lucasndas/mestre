# Canvas da mesa

> **Assunto:** canvas infinito da mesa: personagens com terminal ao vivo, conexões, pergaminhos, missões, esperas, ajuda nos termos e digitação no terminal · **Componente:** Painel · **Atualizado:** 2026-10-27

## Objetivo

Mostrar, em tempo real, tudo o que acontece numa mesa: o Mestre da mesa, a party, os pergaminhos e as missões, e permitir agir sobre eles com a mesma API que a CLI usa. Fechar o navegador não interrompe nada ([visão geral](../arquitetura/visao-geral.md)).

## Comportamento

### Tecnologia

- React + TypeScript + Vite, embutido no binário `mestre` e servido só pela Cidadela ([componentes](../arquitetura/componentes.md)).
- Canvas: **@xyflow/react 12 (React Flow, MIT)**; terminais: **xterm.js**. O porquê está no [ADR 0017](../decisoes/0017-canvas-react-flow.md).
- Atualizações e fluxo dos terminais chegam pelo WebSocket do navegador ([protocolo](../conectividade/protocolo.md)), autenticado pela sessão ([login](login.md)).

### Elementos

| Elemento | Tipo no React Flow | O que mostra | Dono do comportamento |
|---|---|---|---|
| Personagem | nó personalizado com xterm.js | nome, classe, **selo da máquina** onde roda, estado e motivo (ocioso, ocupado, aguardando, aguardando permissão, cota ou entrada, travado, interrompido…), ícone de fixado, aviso de monitoramento reduzido. O Grão-Mestre da campanha aparece em toda mesa, para a conexão automática com o Mestre da mesa ter as duas pontas | [personagens](../orquestracao/personagens.md) |
| Terminal simples (`spawn`) | igual ao personagem, sem classe | base usada e máquina | [personagens](../orquestracao/personagens.md) |
| Pergaminho | nó com editor Markdown | conteúdo ao vivo, editável por pessoas e agentes | [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md) |
| Tomo | nó-grupo de pergaminhos empilhados | título e quantidade; abre ao clicar | [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md) |
| Conexão | aresta contínua | ligação personagem↔personagem, nota↔personagem ou nota↔nota | [conexões](../orquestracao/conexoes.md) |
| Etapa de missão | aresta com seta e número | ordem das etapas; a etapa atual fica destacada; devoluções em seta de volta | [missões](../orquestracao/missoes.md) |
| Espera | aresta **tracejada** de quem espera para o que espera | motivo ("aguardando Revisor", "aguardando 14:00"); impasse em vermelho com o ciclo | [esperas](../orquestracao/esperas-e-caixa-de-entrada.md) |
| Grupo | nó-grupo do React Flow | agrupamento espacial livre, só visual | este documento |

### Canvas infinito

- **Zoom inicial legível:** a mesa abre em zoom 100%, nunca encolhida para caber tudo; o texto dos personagens, selos e pergaminhos tem 17 px ou mais ([design](design.md#direção-pergaminho)), e o "Ajustar à tela" é um botão, não o estado inicial. Se a mesa não cabe, o quadro abre centrado no personagem que está trabalhando (senão, no Mestre da mesa). Terminais seguem o tamanho fixo do PTY.
- **Cabeçalho da tela da mesa:** migalha `Workspaces › workspace › título da mesa` ([visão da campanha](visao-do-workspace.md#como-se-chega-e-a-campanha-atual)), estado da mesa, branch e última entrega, o botão **Conversar sobre esta mesa**, abas **Quadro** e **Terminal**, e, ao lado do quadro, "Em palavras simples": uma frase sobre o que cada personagem faz e o que espera. No celular o quadro vira uma lista vertical de personagens ligados por uma linha (tracejada na espera), cada um com **Ver terminal**.
- Zoom, arrastar o fundo, selecionar vários, arrastar elementos e agrupar.
- Posição, tamanho e grupos de cada elemento ficam salvos no banco da Cidadela por mesa (tabela `canvas_items`, chave `<tipo>:<id>`; um grupo é um elemento com rótulo; `internal/orchestration/canvas`). Reabrir o navegador mostra tudo como estava. Quem vê a mesa move e redimensiona; **agrupar** é de quem edita o canvas.
- Elementos criados pelo motor (recrutamento, missão) aparecem perto de quem os criou, sem sobrepor: ao ler o estado, o que ainda não tem lugar ganha o primeiro espaço livre, em anéis a partir do criador (à direita dele), com uma folga entre os elementos para a linha e o rótulo caberem. Quem some da mesa some do layout; um grupo fica até o usuário o apagar.
- Um personagem que substitui outro (`mestre recruit --replace`) herda a posição, o tamanho e o grupo do antigo, na hora em que nasce (`characters.Placer`).

### API e atualização ao vivo

- `GET /api/workspaces/{ws}/tables/{mesa}/canvas` devolve o estado inteiro da mesa (personagens, pergaminhos, tomos, conexões, missões, esperas, impasses, layout); `PUT …/canvas/layout` guarda o que o usuário moveu. Cada ação tem a sua rota (conexão, fixar, pergaminho, missão, grimório, ritual) e o painel só repassa ao serviço do motor que a CLI também usa; o nível é conferido de novo no servidor (capacidade `edit_canvas`: Administrador ou acima; ler e mover é de quem vê a mesa).
- O tópico `canvas:<workspace>/<mesa>` entrega o estado ao assinar e o evento `changed` a cada mudança do banco da Cidadela (agrupadas em 150 ms); o navegador lê o estado de novo, e é assim que o que um agente escreve num pergaminho aparece sem recarregar. Com o socket caído, a faixa "reconectando" aparece e, ao voltar, tudo é lido de novo.

### Terminais no canvas

- O PTY tem tamanho fixo, definido pela máquina (padrão 200 × 50; [terminal](../arquitetura/terminal.md)). O navegador nunca redimensiona: o xterm.js desenha nesse tamanho e o nó do canvas mostra com rolagem e zoom; redimensionar o nó do canvas não muda o PTY.
- Ao conectar ou reconectar, a Cidadela pede à máquina o **buffer recente** do terminal (`terminal.replay`; o buffer de 1 MiB fica na máquina, a Cidadela não guarda) e depois repassa o fluxo ao vivo. Cliente lento recebe um retrato da tela em vez da fila inteira: o evento `screen` (texto e deslocamento), e o fluxo segue dali. O navegador assina `terminal:<id>`, recebe o retrato do tópico (`snapshot`, com o `frame_id` dos quadros, o estado e o tamanho 200 × 50), os quadros binários desde o deslocamento 0, e os eventos `replayed`, `signal` e `screen` ([protocolo](../conectividade/protocolo.md#canal-do-navegador)); um primeiro quadro depois do 0, ou um salto, é "trecho perdido" e a tela mostra a marca. O componente é `web/src/terminal/TerminalView`, que o canvas embute; a rota `/terminal/<personagem>` mostra um terminal sozinho (`GET /api/characters/{id}/terminal` acha o terminal) e mantém a tela também como texto para leitor de tela.
- Para não pesar o navegador: no máximo 8 terminais com renderização WebGL ao mesmo tempo (padrão); todos os demais, os fora da tela, os muito afastados no zoom e os que passam do limite, viram um retrato estático, atualizado a cada 2 s.
- As classes `nodrag`, `nowheel` e `nopan` do React Flow ficam no terminal, para o mouse e a rolagem irem para o xterm.

### Digitar no terminal

- **Poder deliberado de pessoa:** digitar no terminal de um personagem é o mesmo poder de abrir um terminal naquela máquina, e por isso é só de Administrador ou acima. Pode rodar comandos que não passam pela cadeia de aprovação nem pelos decretos (o modo de shell de uma CLI, por exemplo): é uma escolha do produto, não uma brecha, e o limite é o nível, a auditoria de cada sessão (abaixo) e a recusa enquanto o personagem espera uma tela nativa de permissão. Ver também [segurança](../arquitetura/seguranca.md).
- Só **Administrador ou acima** ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)). Para os outros, o terminal é só leitura (cursor oculto, sem foco de teclado).
- A digitação vai ao PTY pelo motor da máquina, sem passar pela caixa de entrada ([digitação](../arquitetura/terminal.md#digitação)). Antes de liberar o teclado num personagem ocupado, o painel mostra o aviso "o agente está trabalhando".
- Cada sessão de digitação fica na auditoria (`panel.terminal_typing_started` e `panel.terminal_typing_ended`): usuário, personagem, início e fim (o conteúdo digitado não é guardado na auditoria; ele já está no log do terminal). A sessão começa na primeira entrada aceita e termina em "Parar de digitar", ao sair do terminal ou ao fechar o socket; uma entrada recusada também a encerra e vai à auditoria (`panel.terminal_input_refused`, sem o conteúdo).
- O servidor confere o nível de novo a cada mensagem de entrada; o bloqueio no navegador é só conveniência. Personagem parado numa tela nativa de permissão não recebe teclas (`permission_prompt`): a decisão vai pela aprovação ([esperas](../orquestracao/esperas-e-caixa-de-entrada.md)). As respostas automáticas do xterm.js às perguntas do programa (posição do cursor, atributos, cores) nunca vão como digitação. Cada usuário segue no máximo 16 terminais ao mesmo tempo (`too_many_streams`), e quem perde a campanha do terminal tem as sessões encerradas, como numa mudança de nível.

### Ações no canvas

Toda ação usa a mesma API do motor que a CLI usa; as regras são as do documento dono.

| Ação | Como | Regras |
|---|---|---|
| Criar conexão | arrastar de um elemento para outro (`ConnectByUser`: motivo `manual`, só Senhor da Guilda e Administrador) | [conexões](../orquestracao/conexoes.md) |
| Remover conexão | selecionar a aresta e apagar; a automática Mestre da mesa ↔ Grão-Mestre é recusada | [conexões](../orquestracao/conexoes.md) |
| Montar ou ajustar missão | no painel lateral, "Montar missão": arrastar de um personagem ao próximo cria uma etapa que vem depois (arrastar para uma etapa anterior é a devolução) e a ordem da lista é o número. Nasce como **rascunho**, que pode ser ajustado (`quests.Service.Redraw`) até rodar; uma missão que já roda não se desenha de novo por aqui. Uma etapa por personagem | [missões](../orquestracao/missoes.md) |
| Fixar personagem | ícone de alfinete | [personagens](../orquestracao/personagens.md) |
| Editar pergaminho | editor no próprio nó | [pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md) |
| Salvar seleção como grimório e restaurar em outra mesa | botão na barra da seleção | [grimórios](../orquestracao/grimorios.md) |
| Ver e editar rituais da mesa | painel lateral: ativar, desativar e mudar o que o ritual faz | [rituais](../orquestracao/rituais.md) |

Depois de `mestre recruit --replace`, o nó do personagem novo fica na posição do antigo, com as mesmas conexões ([coordenação](../cli/coordenacao.md); a parte do canvas de CA-CLI-COORD-04 é do M6).

### Operador do PC

- Enquanto uma sessão do [Operador do PC](../orquestracao/bases-e-fichas.md#base-especial-operador-do-pc) está ativa, uma faixa fixa no topo de todas as telas do painel, para os usuários daquela campanha, mostra máquina, tarefa, "até HH:MM" e a contagem regressiva, com o botão **Encerrar agora** para quem tem o nível ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)).
- No canvas, o personagem do Operador tem o selo "Operador do PC" e a hora do fim. As capturas de tela não aparecem aqui: só o Senhor da Guilda as vê, em [administração](administracao.md#operador-do-pc).

### Ajuda nos termos

- Todo termo do vocabulário (Companhia, Ficha, Classe, Grimório, Pergaminho, Ritual, Decreto etc.) tem um ícone "?" ao lado; passar o mouse ou tocar mostra a **Ajuda curta**.
- O texto vem do [glossário](../produto/glossario.md), lido no build e embutido no painel. Nenhum texto de ajuda é escrito no código do painel.
- No build, um termo usado na interface sem linha no glossário faz o build falhar.

### Desenho do quadro por dentro (M6, unidade 4F)

O [protótipo clicável](https://claude.ai/artifact/3EqrEGizKHmLPNT4oGkQiu) mostra o quadro no computador e no celular, com os estados. Usa só dados e ações que já existem; a abertura em 100% e a lista do celular são da unidade 4D e o resto vem em cima delas.

- **Um cartão de personagem só** (é daqui; a 4E só desenha a janela do terminal): a 100% e com texto de 17 px ou mais, traz nome, classe, selo "Máquina X", a **situação** no vocabulário único do [design](design.md) (Trabalhando, Ocioso, Espera a sua decisão, Esperando <quem>, Sem sinal, Encerrado; palavra, forma e tom, nunca só cor), a janela do terminal, a linha de **missão e etapa** ("Missão entrega · Etapa 1 de 2" com uma barrinha por etapa) e, havendo aprovação do personagem, **Decidir · N aprovação** (leva a Aprovações), e dois botões com texto: **Abrir terminal** e **Fixar / Desafixar** (a dica diz o que fixar faz). **Digitar** não está no cartão: só na aba Terminal. Sem sinal: a janela fica cinza com a hora.
- **Linhas:** cada tipo se lê sem decorar: contínua, "trabalham juntos"; seta com selo "etapa N", a ordem da missão; tracejada com o motivo, "um espera o outro"; vermelha, impasse. O botão **Como ler o quadro** abre essa legenda. Num impasse, a faixa diz quem espera quem, o que fazer e leva a **Conversar sobre isto**.
- **Pergaminho:** o texto no cartão e **Editar**; ao editar, **Salvar** e **Descartar**; se alguém mudou antes, a faixa "Mudou enquanto você editava" e **Salvar** desligado.
- **Barra de seleção** (no lugar dos botões soltos "Agrupar seleção" e "Salvar seleção como grimório"): sem seleção, uma dica fixa acima do quadro ("Clique num cartão para selecionar. Shift + clique seleciona vários"); com seleção, "N selecionados" e as ações com nome claro: **Juntar em grupo** (pede 2 ou mais e diz isso), **Guardar como grimório** e **Soltar**, com a frase "Grupo é só visual… Grimório é um modelo desta parte da mesa que você restaura em outra mesa". O botão **Selecionar vários** liga um modo em que tocar num cartão o marca ou desmarca (para quem não tem Shift, no toque ou no teclado). A barra não cobre os cartões. A ajuda de **Grupo**, **Grimório**, **Missão** e **Ritual** é o balão `Term` com "No mundo real" (a mesma da 4G), nunca só o `title`.
- **Grupo:** moldura tracejada com nome editável e **Desfazer grupo**; só organiza o quadro.
- **Painel lateral:** o botão passa a se chamar **Missões, rituais e grimórios** (e **Esconder**). Cada aba abre com uma frase do que é (missão: sequência de personagens; ritual: tarefa que repete sozinha na hora marcada; grimório: modelo guardado de parte de uma mesa). Grimório: **Guardar a seleção como grimório** (mostra o que vai guardar, o nome e "só a estrutura") e **Restaurar nesta mesa** (mostra o que será renomeado e a classe que falta, antes de confirmar), sem janela: tudo no painel.
- **Controles:** zoom com o número, **Ajustar à tela** com nome e a legenda; o mapa do canvas só fica no computador.
- **Só leitura:** para quem não edita, os botões de editar ficam desligados com o motivo ("Só Administrador ou acima…"), nunca somem sem explicação.
- **Celular:** a lista da 4D, com **Ver terminal**, o menu "Mais ações" (**Fixar**, **Conectar a outro personagem…**) e abas Missões, Rituais e Grimórios em tela cheia. **Selecionar** liga caixas de marcar nos cartões e a barra "N selecionados · Guardar como grimório · Soltar" (sem **Juntar em grupo**, que só organiza o quadro, e o quadro não aparece no celular); arrastar, ligar e agrupar só no computador, e a tela diz isso. O aviso de uma ação recusada ou concluída é o aviso da vida da mesa (4H) (embaixo, à direita, com ação; acima das abas no celular).
- **Fora do que existe:** desfazer de movimentos, ver quem editou o pergaminho e quando, e atalhos de teclado do quadro não existem no motor nem no MVP.

## Regras e erros

- Ação recusada pelo motor (nível, regra do documento dono) → o canvas desfaz o gesto e mostra o motivo devolvido: a resposta é `refused` com `params.reason` (chave estável, traduzida pelo painel; sem tradução vale o texto do motor) e, na conexão automática, `params.reasons`. Nada é desenhado antes de o motor responder: a linha arrastada fica pontilhada até lá. Pergaminho editado por outra pessoa no meio é `conflict` (a revisão base não bate).
- WebSocket caiu → faixa "reconectando", com backoff; ao voltar, o estado é recarregado da Cidadela.
- Máquina do personagem fora do ar → terminal em cinza com "máquina sem sinal desde HH:MM" (no fuso da Guilda); o retrato fica visível e, ao voltar o sinal, a Cidadela pede o fluxo de novo de onde parou. A digitação nessa máquina é recusada (`unavailable`).

## Critérios de aceite

- **CA-CANVAS-01** · *pesada* — **Dado** uma mesa com missão em andamento em duas máquinas, **quando** abro o canvas, **então** vejo personagens com selo da máquina, conexões, pergaminhos ao vivo, setas numeradas com a etapa atual destacada e linhas tracejadas de espera com motivo (MVP-11).
- **CA-CANVAS-02** · *rápida* — **Dado** o glossário com o termo "Companhia", **quando** o build roda, **então** a ajuda do "?" de Companhia é exatamente a Ajuda curta da tabela; e um termo sem linha faz o build falhar.
- **CA-CANVAS-03** · *rápida* — **Dado** um Aprovador, **quando** envia entrada para um terminal pelo WebSocket, **então** o servidor recusa e nada chega ao PTY.
- **CA-CANVAS-04** · *completa* — **Dado** um terminal com saída, **quando** o navegador reconecta, **então** a Cidadela pede à máquina o `terminal.replay` e o fluxo reenviado é, byte a byte, o do buffer da máquina (comparado em Go).
- **CA-CANVAS-09** · *rápida* — **Dado** esse replay, **quando** o painel (Vitest, xterm.js sem navegador) o recebe, **então** o buffer de linhas renderizado é igual ao do emulador da máquina.
- **CA-CANVAS-05** · *rápida* — **Dado** uma conexão arrastada que o motor recusa, **quando** a resposta chega, **então** a aresta some e o motivo aparece.
- **CA-CANVAS-06** · *rápida* — **Dado** elementos movidos, **quando** recarrego a página, **então** as posições e grupos são os mesmos (MVP-12).
- **CA-CANVAS-10** · *rápida* — **Dado** a conexão automática do Mestre da mesa e uma conexão criada pelo usuário, **quando** ele apaga as duas, **então** a segunda some e a primeira é recusada com o motivo.
- **CA-CANVAS-08** · *rápida* — **Dado** uma sessão do Operador ativa e o relógio falso, **quando** um Aprovador e o Senhor da Guilda abrem o painel, **então** os dois veem a faixa com a contagem, o botão **Encerrar agora** aparece só para o Senhor da Guilda (e para um Administrador, que também pode encerrar, [níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)), não para o Aprovador (e a API recusa o encerramento vindo dele), e um toque nele manda encerrar a sessão na hora; ao fim, a faixa some.
- **CA-CANVAS-11** · *rápida* — **Dado** uma mesa com três personagens, **quando** o quadro abre, **então** o zoom é 100% e o menor texto renderizado tem 17 px ou mais; **dado** uma mesa que não cabe na tela, **então** abre centrada no personagem que trabalha, e **Ajustar à tela** muda o zoom.
- **CA-CANVAS-13** · *rápida* — **Dado** o quadro com nada selecionado, **quando** olho os controles, **então** não há botão **Agrupar seleção** nem **Salvar seleção como grimório** soltos: há a dica de seleção; **quando** seleciono dois cartões (com Shift ou com **Selecionar vários**), **então** a barra mostra "2 selecionados", **Juntar em grupo**, **Guardar como grimório** e **Soltar**, e com um só cartão **Juntar em grupo** está desligado dizendo que pede 2.
- **CA-CANVAS-14** · *rápida* — **Dado** dois cartões selecionados, **quando** uso **Juntar em grupo** e depois **Desfazer grupo**, **então** a moldura aparece com nome editável, as posições salvas incluem o grupo e ao desfazer os cartões ficam onde estavam.
- **CA-CANVAS-15** · *rápida* — **Dado** o painel lateral, **quando** abro cada aba, **então** a frase do que ela é aparece antes da lista, e o botão se chama **Missões, rituais e grimórios**; **dado** um grimório com a classe `Revisor` ausente e o nome `Dev-1` repetido, **quando** uso **Restaurar nesta mesa**, **então** a tela diz o novo nome e a classe a escolher antes de confirmar.
- **CA-CANVAS-16** · *rápida* — **Dado** um Observador, **quando** abre o quadro, **então** vê e move cartões, e Fixar, Editar, Montar missão, os rituais e os grimórios estão desligados com o motivo escrito.
- **CA-CANVAS-17** · *rápida* — **Dado** um impasse entre Dev-1 e Revisor, **quando** o quadro abre, **então** a linha vermelha diz quem espera quem, a faixa explica o que fazer e **Como ler o quadro** descreve as quatro linhas.
- **CA-CANVAS-18** · *rápida* — **Dado** o celular (390 px), **quando** abro a mesa, **então** vejo a lista de personagens com **Ver terminal**, **Fixar** e **Conectar a outro personagem…** no menu, as abas Missões, Rituais e Grimórios, e o aviso de que agrupar e guardar grimório são do computador; nenhum alvo tem menos de 44 px.
- **CA-CANVAS-19** · *rápida* — **Dado** o cartão de um personagem trabalhando na etapa 1 de 2 com uma aprovação pendente, **quando** o olho, **então** a situação é "Espera a sua decisão", a linha diz "Missão entrega · Etapa 1 de 2", há **Decidir · 1 aprovação**, **Abrir terminal** e **Fixar** (ou **Desafixar**) com texto, e não há **Digitar**, que só existe na aba Terminal; o mesmo vale para os cartões da lista do celular.
- **CA-CANVAS-12** · *rápida* — **Dado** o canvas da mesa `Login com Google` da campanha `loja`, **quando** abro a tela, **então** a migalha mostra Campanhas › loja › Login com Google com os dois primeiros itens levando de volta, e o item Campanhas do menu está marcado.
- **CA-CANVAS-07** · *rápida* — **Dado** 12 terminais visíveis, **quando** o canvas renderiza, **então** exatamente 8 usam WebGL e os 4 restantes mostram retrato.

## Depende de

- [Visão geral](../arquitetura/visao-geral.md)
- [Terminal](../arquitetura/terminal.md)
- [Protocolo](../conectividade/protocolo.md)
- [Login](login.md)
- [Personagens](../orquestracao/personagens.md)
- [Conexões](../orquestracao/conexoes.md)
- [Missões](../orquestracao/missoes.md)
- [Rituais](../orquestracao/rituais.md)
- [Grimórios](../orquestracao/grimorios.md)
- [Esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md)
- [Pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md)
- [Glossário](../produto/glossario.md)

## Fontes

- React Flow: https://github.com/xyflow/xyflow
- Selo de atribuição do React Flow: https://reactflow.dev/learn/troubleshooting/remove-attribution
