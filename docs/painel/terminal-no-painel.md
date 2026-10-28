# Terminal no painel

> **Assunto:** a aparência e o uso do terminal de um personagem no painel (tela cheia, cartão do canvas e celular): janela de terminal de verdade, barra de identidade, linha de status com missão, aprovação e entrega, digitar, estados e o que já existe no servidor · **Componente:** Painel · **Atualizado:** 2026-10-27

## Objetivo

O terminal da mesa tem de parecer um terminal e ser mais que um: a tela do agente, inteira e legível, com uma camada fina do Mestre em volta (quem é o personagem, em que missão e etapa está, se há aprovação esperando) e as ações que o painel já tem. O desenho foi aprovado no [protótipo clicável](https://claude.ai/artifact/DnYyRWXq6PFrsQGZAu43FA); este documento é dono da interface do terminal. O PTY, o tamanho fixo e a digitação no servidor são do [terminal](../arquitetura/terminal.md) e do [canvas](canvas.md#digitar-no-terminal).

## Comportamento

### Uma janela de terminal, com uma camada do Mestre

- A janela tem três partes: **barra de identidade** (em cima), **tela do PTY** (o resto) e **linha de status** (embaixo). Nada se desenha sobre a tela, a não ser o rótulo "retrato" e o botão **Ir ao fim**. É **uma peça escura só**: barra, tela, linha de status e caixa de mensagem usam os tokens `--color-term-*` (fundo da tela, `chrome`, `chrome-2`, `line`, `dim`), não o pergaminho do painel. A altura é `clamp(460px, 100dvh − 300px, 860px)` (a aba da mesa desconta o cabeçalho dela): a janela cabe na tela e é a tela do PTY que rola.
- O componente continua sendo `web/src/terminal/TerminalView`, o mesmo nas três situações (a página `/terminal/<personagem>` de quem está numa mesa leva à **aba Terminal da mesa** ([canvas](canvas.md#canvas-infinito)); sem mesa, o terminal fica sozinho nela); só o tamanho muda ([cartão](#cartão-do-canvas), [celular](#celular)). A tela própria vive dentro da casca do painel ([design](design.md#estrutura-da-tela)): barra de cima com marca, sino, **Falar com o Grão-Mestre** e menu da conta, menu de quatro itens e migalhas; a conversa abre no painel lateral, como em toda tela ([conversa](conversa-e-aprovacoes.md)).
- Tudo na camada do Mestre vem de dados e ações que já existem ([tabela](#o-que-já-existe-e-o-que-não)). Um dado que falta some da linha; nunca é inventado.

### A tela

- Superfície escura (`--color-term-bg`), fonte monoespaçada **empacotada** (JetBrains Mono, OFL, por `@fontsource`, como as demais; a do sistema só de reserva), sem ligaduras, altura de linha 1,4. Tamanho da letra: 15 px na tela cheia, 13 px no cartão, 12 px no celular. O xterm só abre depois de a fonte carregar.
- **Cores:** as 16 cores ANSI saem de tokens `--color-term-*` no `@theme` (tons quentes do Pergaminho, texto claro sobre o fundo escuro com contraste de pelo menos 4,5:1); o valor mora no arquivo de tokens, não aqui. Diferenças (adicionado, removido) usam também o sinal `+` e `-`, nunca só a cor.
- **Cursor:** sem o teclado liberado, um bloco **oco** (como o de um terminal sem foco; troca a regra antiga de cursor oculto, [design](design.md#terminal)); com o teclado, bloco cheio âmbar que pisca (parado com `prefers-reduced-motion`).
- **Tamanho:** 200 × 50 fixo, com rolagem nos dois eixos e uma linha tracejada na coluna 200. A letra muda só por zoom no navegador: **A−** e **A+** (11 a 22 px) e **Ajustar à largura** (no mínimo 11 px); nada disso redimensiona o PTY.
- **Rolagem:** 2.000 linhas de histórico no navegador. Ao rolar para cima a tela para de seguir e mostra **Ir ao fim (+N)**; "trecho perdido" continua marcado na tela.
- **Moldura:** borda fina; com o teclado liberado, a borda e um anel passam a `--color-accent`, junto do modo **DIGITANDO**.

### Barra de identidade

- Ponto de conexão (verde ao vivo, âmbar pulsando ao reconectar, cinza sem sinal ou encerrado; sempre com o texto no `title` e no estado), nome em fonte de título, **estado em palavras**, o vocabulário de [design](design.md#estados) (Trabalhando, Ocioso, Espera a sua decisão, Esperando <quem ou o quê>, Sem sinal, Encerrado; nunca o identificador `idle`), classe ([Terminal simples](../produto/glossario.md) quando não tem) e a máquina.
- Classe e máquina são fichas (sem classe, "Terminal simples"). Ações à direita, **só o ícone**, com nome acessível e alvo de 44 px, e a letra com o tamanho entre **A−** e **A+**: **Fixar**, **Copiar tela**, **A−/A+/Ajustar**, **Pausar mesa** (vira **Retomar mesa**) e **Focar** (esconde o menu para dar largura; só layout). Fixar é de quem edita o canvas e Pausar mesa de Aprovador ou acima ([visão da campanha](visao-do-workspace.md)); para os outros o botão não aparece.
- **Pausar mesa** pergunta antes, num balão sob o botão (sem janela), com **motivo obrigatório** (o botão só habilita com o motivo escrito; retomar também pede motivo), usa a rota que já existe e a auditoria dela, e diz numa frase a diferença: Pausar mesa para o trabalho da mesa; **Pausar autonomia** (em Configurações) só muda quem decide os pedidos. A mesa pausada mostra o motivo numa faixa. Não existe pausar um personagem sozinho. **Fixar** e **Desafixar** avisam com o aviso padrão (embaixo à direita, com **Desfazer**).

### Linha de status

- À esquerda, o **modo**: **LEITURA** (olho; cadeado quando o nível não deixa digitar), **MENSAGEM** ou **TECLADO DIRETO** (teclado), em caixa alta, cada um uma palavra de termo (`Term`): sublinhada com pontos, abre o balão claro com "No mundo real:" em negrito, na mesma linha da frase, ao passar o mouse e o fixa ao tocar, clicar ou apertar Enter; sem "?" ao lado. Os termos Leitura, Mensagem e Teclado direto entram no glossário, em pt-BR e en.
- Depois, na tela cheia, a **missão** e a **etapa atual** (a mesma linha que o cartão da 4F mostra, num componente só) ("Etapa 2 de 4: Implementar login", com uma barrinha por etapa: feita, atual, a fazer), as **aprovações pendentes** deste personagem como botão **Decidir** (leva à [aprovação](conversa-e-aprovacoes.md)) e a **última entrega** da mesa. Sem missão, a parte some.
- À direita, o botão **Digitar** (ou **Parar de digitar**) e uma frase de apoio: o que digita vai ao agente, o motivo de o botão estar desligado, ou "200 × 50 fixo".

### Digitar

- Valem as regras do [canvas](canvas.md#digitar-no-terminal): só Administrador ou acima, servidor confere a cada entrada, auditoria da sessão, respostas automáticas do xterm não são digitação.
- **Caixa de mensagem (padrão), como no Claude Code:** **Digitar** abre uma caixa sob a tela. Com o personagem **livre**, **Enviar** manda o texto na hora; com ele **ocupado**, o botão vira **Pôr na fila** e a mensagem entra numa **fila** visível acima da caixa ("Na fila (2): vai para Dev-1 quando ele terminar"). Cada item tem **Enviar agora** (chega direto ao terminal e pode atrapalhar o trabalho; o aviso fica no próprio botão) e **Tirar**. Quando o estado do personagem volta a `idle`, a fila sai sozinha, uma mensagem por vez, na ordem.
- **Como a fila funciona (só no navegador):** o texto fica na página e sai pela digitação de hoje (`terminal.input`, modo `keys`: o texto e Enter), com o mesmo nível, a mesma auditoria e as mesmas recusas; se a página fechar, a fila some, e a faixa da fila diz isso. Uma fila que sobreviva ao navegador seria a [caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md) do servidor (trabalho novo, fora desta unidade). Um texto de várias linhas sai como uma colagem só (`ESC[200~ … ESC[201~` e Enter), para nenhuma linha enviar antes da hora; conferir nos três provedores quando o M7 ligar os terminais de produção.
- **Teclado direto** (botão de ação: o balão do termo aparece ao passar o mouse ou focar, sem fixar, para o clique continuar sendo a ação): um segundo modo, para telas que pedem teclas (menus e perguntas do provedor): cada tecla vai ao agente, como hoje. Num personagem ocupado pede antes a confirmação "está trabalhando", numa faixa âmbar no lugar da linha de status (sem janela), com **Cancelar** e **Digitar mesmo assim**.
- **Soltar o teclado:** no teclado direto, além do botão, **Ctrl+Alt+K** solta o teclado sem enviar nada ao agente; assim quem usa só o teclado nunca fica preso na tela. Antes de fixar a tecla, conferir que nenhum dos três provedores a usa.
- **Digitar fica desligado**, com a frase do motivo, quando: o nível não deixa; a máquina está sem sinal; o painel está reconectando; o personagem espera a sua decisão (`permission_prompt`); o terminal terminou.

### Estados

| Estado | Como aparece |
|---|---|
| Ao vivo | ponto verde, estado em palavras, tela seguindo o fim |
| Digitando | modo MENSAGEM (caixa e fila; com o agente ocupado, "Trabalhando: sua mensagem entra na fila") ou TECLADO (cursor cheio), moldura de destaque, **Parar de digitar** |
| Somente leitura | modo LEITURA com cadeado e "Somente leitura: seu nível não permite digitar aqui"; cursor oco |
| Reconectando | faixa âmbar com ícone e "Reconectando à Cidadela…" em negrito, tela mantida e atenuada, Digitar desligado; ao voltar, segue de onde parou ([reconexão](design.md#reconexão)) |
| Sem sinal | tela em cinza, estado "Sem sinal", faixa com ícone e "Máquina sem sinal desde HH:MM." em negrito (fuso da Guilda, `role="status"`), seguida de "A tela fica como estava…", Digitar desligado |
| Espera a sua decisão | faixa em `--color-accent` "espera a sua decisão: decida na aprovação" com **Decidir em Aprovações** |
| Encerrado ou interrompido | saída preservada para leitura, sem cursor, faixa "foi encerrado" com **Ver a mesa** |
| Retrato (fora do orçamento WebGL) | rótulo "retrato · atualiza a cada 2 s" sobre a tela |

### Cartão do canvas

- O **cartão do personagem é da 4F** ([canvas](canvas.md)): cabeçalho, linha de missão, etapa e aprovação, **Abrir terminal** e **Fixar/Desafixar**. Esta unidade desenha só o **terminal dentro do cartão**: a tela mostrando o **fim** do PTY e seguindo a saída, os avisos de estado sobre ela e uma faixa fina com o modo (LEITURA). **Digitar** não está no cartão: só na tela do terminal ([canvas](canvas.md)).
- Longe no zoom a tela vira retrato, com o rótulo. Redimensionar o cartão nunca muda o PTY ([canvas](canvas.md#terminais-no-canvas)). Personagem sem terminal mostra "Sem terminal aberto" (do cartão).

### Celular

- Até 700 px de largura da própria janela (consulta de contêiner, não só do aparelho): a barra mostra nome e estado em cima e classe e máquina embaixo; as ações secundárias vão para o menu **Mais** (ícone e nome, sem empilhar na barra). A linha de status ocupa a largura e o **Digitar** tem 48 px de altura, logo acima da barra de baixo do painel.
- **As 200 colunas não cabem em 390 px** (a 12 px, o mínimo legível, pedem cerca de 1.440 px). O terminal não encolhe a letra: a tela rola para o lado e uma tira no alto avisa "200 colunas: role para o lado", com **Quebrar linhas**, que troca a rolagem por linhas que quebram (muda só a leitura; o PTY segue 200 × 50). A página nunca rola de lado.
- **Ajustar à largura** (só onde há zoom) nunca passa de 11 px: se as colunas não cabem, diz quanto pedem e deixa a rolagem.

### O que já existe e o que não

| Mostra | Vem de |
|---|---|
| nome, máquina, estado, sinal, tamanho, pode digitar | `TerminalInfo` e os eventos do tópico `terminal:<id>` |
| classe, nome da máquina, fixado, motivo da espera | `CanvasCharacter` |
| missão, etapa atual, quantas etapas | `CanvasQuest` e `CanvasStep` do canvas da mesa |
| última entrega | visão da campanha (`/view`) |
| aprovações pendentes do personagem | lista e eventos de aprovações |
| zoom, Ajustar, Copiar tela, Ir ao fim, Focar, soltar teclado | só o navegador |

- **Mudança pequena no servidor (aditiva):** a tela própria só recebe `TerminalInfo`, que não traz a mesa. `table` entra na mesma resposta de `GET /api/characters/{id}/terminal` (campo novo, nada muda); com ele a tela pede o canvas da mesa (que traz a classe e o nome da máquina do personagem) e segue os eventos dele. Sem isso a tela própria mostra só o que `TerminalInfo` diz.
- **Fora desta unidade** (não existe, não está no [escopo do MVP](../produto/escopo-mvp.md)): pausar um personagem sozinho, código de saída, tempo gasto na etapa, atalhos de teclas para o agente.

## Regras e erros

- A recusa do servidor ao digitar continua dizendo quem recusou e por quê ([recusa e desfazer](design.md#recusa-e-desfazer)). Máquina sem sinal e reconexão não escondem a última tela.
- A saída real dos terminais de produção só existe quando o M7 fecha a [ligação de produção](../desenvolvimento/ligacao-de-producao.md#o-que-o-m7-fecha); até lá o painel mostra o que a máquina tiver, e a tela não diz nada sobre isso.
- Mesmo com a camada do Mestre ausente (dado que não chegou), o terminal funciona sozinho: tela, estado e digitar.

## Critérios de aceite

- **CA-TUI-01** · *rápida* — **Dado** um terminal ao vivo na tela cheia, **quando** abre, **então** o xterm usa a fonte empacotada a 15 px, linha 1,4, sem ligaduras, 200 × 50, e a barra mostra nome, classe, máquina e o estado em palavras ("Trabalhando", nunca `busy`).
- **CA-TUI-02** · *rápida* — **Dado** o terminal sem o teclado liberado (cursor oco), **quando** a pessoa toca **Digitar**, **então** o modo vira MENSAGEM com a caixa, o botão vira **Parar de digitar** e a moldura muda; no **Teclado direto**, Ctrl+Alt+K solta o teclado e a tecla não chega ao `feed`.
- **CA-TUI-03** · *rápida* — **Dado** um personagem ocupado, **quando** a pessoa envia duas mensagens, **então** as duas aparecem na fila e nada chega ao `feed`; **Tirar** remove uma, **Enviar agora** manda a escolhida na hora, e quando o personagem volta a `idle` (relógio falso) a outra sai sozinha; com o `feed` recusando (nível ou sinal) a recusa é mostrada e a mensagem não se perde. **Teclado direto** num personagem ocupado mostra a faixa de confirmação, sem diálogo modal.
- **CA-TUI-04** · *rápida* — **Dado** um usuário sem `type_in_terminal`, **quando** abre o terminal, **então** Digitar está desligado com "Somente leitura…" e o cursor é oco (o servidor recusa a entrada: CA-CANVAS-03).
- **CA-TUI-05** · *rápida* — **Dado** o `/ws/ui` que cai, **quando** a conexão volta, **então** a faixa âmbar, a tela atenuada e Digitar desligado valeram enquanto durou a queda, e a tela segue de onde parou sem repetir saída.
- **CA-TUI-06** · *rápida* — **Dado** o evento `signal` perdido às 14:02, **quando** a tela é desenhada, **então** fica em cinza, a faixa diz "desde 14:02" no fuso da Guilda e Digitar fica desligado; com o sinal de volta tudo se normaliza.
- **CA-TUI-07** · *rápida* — **Dado** um personagem `stopped` ou `interrupted`, **quando** o terminal abre, **então** a saída continua legível, não há cursor, Digitar está desligado e a faixa oferece **Ver a mesa**.
- **CA-TUI-08** · *rápida* — **Dado** um personagem bloqueado por permissão, **quando** o terminal abre, **então** a faixa em destaque tem **Decidir em Aprovações**, que leva ao pedido, e Digitar está desligado.
- **CA-TUI-09** · *rápida* — **Dado** uma missão na etapa 2 de 4 e uma aprovação pendente, **quando** a linha de status é desenhada, **então** mostra "Etapa 2 de 4", a barrinha e o botão da aprovação; e para um personagem sem missão essa parte não aparece.
- **CA-TUI-10** · *rápida* — **Dado** o terminal dentro do cartão do canvas, **quando** a mesa abre, **então** mostra só o fim da tela seguindo a saída e o modo LEITURA, sem **Digitar** (o cabeçalho, a linha de etapa e os botões são do cartão da 4F), e longe no zoom a tela vira retrato com o rótulo.
- **CA-TUI-11** · *rápida* — **Dado** uma janela de 390 px, **quando** o terminal abre, **então** a letra é de 12 px, a página não rola de lado, a tira "200 colunas: role para o lado" aparece com **Quebrar linhas** (que quebra as linhas sem mudar 200 × 50), as ações secundárias estão em **Mais** e Digitar tem 48 px de altura; **Ajustar à largura** nunca passa de 11 px e diz quando não cabe.
- **CA-TUI-12** · *rápida* — **Dado** um Aprovador e um Observador, **quando** abrem o terminal, **então** só o Aprovador vê **Pausar mesa**, que só habilita com o motivo escrito, chama a rota existente com ele e mostra a faixa com o motivo, e **Fixar** só aparece a quem edita o canvas.
- **CA-TUI-13** · *rápida* — **Dado** o zoom **A+** e **Ajustar à largura**, **quando** a pessoa os usa, **então** a letra muda, o terminal continua 200 × 50 e nada de redimensionar sai ao servidor.
- **CA-TUI-15** · *rápida* — **Dado** a barra do terminal, **quando** a pessoa passa o mouse na palavra LEITURA, MENSAGEM ou TECLADO DIRETO (sublinhada com pontos, sem "?"), **então** o balão acessível mostra o texto do glossário e "No mundo real:", nos dois idiomas; tocar, clicar ou Enter o fixa, Esc o fecha, e em **Teclado direto** (botão de ação) ele aparece só ao passar o mouse ou focar.
- **CA-TUI-14** · *rápida* — **Dado** a tela cheia, **quando** é varrida por acessibilidade, **então** a região tem nome, a tela existe também como texto para leitor de tela, as cores ANSI passam em 4,5:1 sobre o fundo e `prefers-reduced-motion` desliga o piscar e o pulso.

## Depende de

- [Terminal](../arquitetura/terminal.md)
- [Canvas da mesa](canvas.md)
- [Design do painel](design.md)
- [Visão da campanha](visao-do-workspace.md)
- [Conversa e aprovações](conversa-e-aprovacoes.md)
- [Missões](../orquestracao/missoes.md)
