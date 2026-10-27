# Vida da mesa no painel

> **Assunto:** como o painel conta a vida de uma mesa, do pedido à entrega (situação, ciclo, faixa "Precisa de você", filtros, avisos na tela e no sino, histórico e erros), no computador e no celular · **Componente:** Painel · **Atualizado:** 2026-10-27

## Objetivo

Quem abre a campanha todo dia precisa saber, em segundos, o que cada mesa está fazendo, o que espera por ele e o que já foi entregue, sem decifrar campos. Este documento é o dono da **situação da mesa** e de como ela chega à pessoa; o desenho foi aprovado num protótipo clicável (2026-10-27) e fica dentro da [jornada](primeira-jornada.md) (passos 5 a 8).

## Comportamento

### Situação: uma palavra por mesa

O painel deriva **uma situação** de cada bloco do [`/view`](visao-do-workspace.md#bloco-da-mesa) (estado, `paused`, `waits`, `stalled`, `pr`), na ordem abaixo; a primeira que vale ganha. Os rótulos são os do [vocabulário de estados](design.md#vocabulário-de-estados), e só eles. Função pura no navegador (`situation.ts`), sem rota nova, a mesma para a campanha e para o Início ([Acontecendo agora](primeira-jornada.md#início-no-dia-a-dia)).

| Rótulo | Quando | Frase do cartão | Precisa de você |
|---|---|---|---|
| Entregue | `closed` com PR; `closing` | "PR #n mesclado. Nada mais a fazer."; em `closing`, "encerrando" e que o Grão-Mestre pergunta se algo ficou sem commit | não |
| Encerrada · Cancelada | `closed` sem PR · `cancelled` ou `cancelling` | a retenção de 30 dias, na cancelada | não |
| Travada | espera `same_failure` | "A mesma falha voltou" (a impressão e o `arquivo:linha` ficam em "Detalhes técnicos") | sim |
| Espera a sua decisão | espera `approval` | o que pedem e que o passo fica parado | sim, só para quem decide (os demais veem a mesma palavra, sem botão) |
| Esperando | `paused`, espera `quota`, `table` ou `other` | o que falta (ou quem pausou e por quê) e que segue sozinha | não |
| Travada | `stalled` | "Está trabalhando, mas nada foi entregue há N h" | sim |
| Em revisão | `in_review` | "PR #n aberto. Falta o merge." | não |
| Preparando | `open` | "A Cidadela cria a branch e chama o Mestre da mesa" | não |
| Trabalhando | `working` | missão, etapa e companhia por máquina | não |

Cada rótulo tem **palavra, ícone de forma própria e tom** (a cor nunca é a única informação, [design](design.md#acessibilidade-e-movimento)): verde Trabalhando, âmbar Esperando e Travada sem entrega, tijolo Espera a sua decisão, vermelho Travada por falha repetida, verde cheio Entregue. Os tokens são os da jornada (`signal-wash`, `warn-wash`), e o chip é arredondado como o dela.

### O cartão da mesa

Substitui a grade de campos do [bloco](visao-do-workspace.md#bloco-da-mesa); os campos do bloco continuam, só mudam de lugar:

- **Em cima:** a situação e "1 aviso novo" quando há aviso não lido daquela mesa no [sino](conversa-e-aprovacoes.md#avisos).
- **Título** (leva à mesa, [canvas](canvas.md)) e `#épico` (leva ao GitHub); **uma frase** da tabela acima, com missão, etapa e companhia embaixo.
- **Motivo**, quando há: o aviso "sem issue no GitHub" com o motivo. Nada técnico no corpo do cartão.
- **Trilha do ciclo:** Aberta › Em trabalho › Em revisão › Encerrando › Encerrada, com o passo atual (`aria-current="step"`), só nas mesas que seguem o ciclo normal; cancelamento não tem trilha.
- **Ações** (só o que a situação pede): **Ver o pedido** (decisão), **Abrir mesa** e **Falar com o Grão-Mestre** (travada), **Pausar mesa** (mesa Travada sem entrega, Aprovador ou acima, sem PIN, com um campo **Motivo** obrigatório que abre no próprio cartão; a frase embaixo diferencia de Pausar autonomia), **Ver o PR** (entregue).
- **Detalhes técnicos**, recolhido: impressão da falha e `arquivo:linha`, companhia (com a ajuda do termo), branch, critérios (com prova e verificados), última entrega, PR e uso.

### Campanha: o que se vê e em que ordem

- **Faixa "Precisa de você (N)"** no alto (o mesmo nome do Início), só com as aprovações e as mesas Travadas, cada uma com a frase e o botão; sem nenhuma, a linha "Nada espera por você agora." A faixa existe porque a posição que a pessoa arrasta no quadro continua salva, e o urgente não pode se perder num quadro bagunçado.
- **Resumo** sob o título: "3 mesas em andamento · 1 espera por você · 1 em revisão".
- **Filtros** por situação, cada um com a contagem (Todas, Precisa de você, Trabalhando, Esperando, Em revisão) e **Mostrar encerradas**, que traz as entregues e canceladas esmaecidas, como hoje.
- **Quadro | Lista** no computador: o quadro é o canvas de hoje com os cartões novos; a lista é a do celular, ordenada por urgência (travada, decisão, sem entrega, pausada, esperando, em revisão, trabalhando, preparando, encerrando, entregues). No celular só há a lista, o Grão-Mestre primeiro.
- O Observador vê o estado de tudo, sem **Ver o pedido** nem **Pausar mesa** (o servidor confere de novo).

### Como cada situação chega à pessoa

| Situação | Com a campanha aberta | Fora dela |
|---|---|---|
| Mesa nasceu | aviso na tela e cartão destacado por um instante | cartão de andamento na conversa |
| Trabalhando | nada; silêncio é o normal | nada |
| Esperando cota ou outra mesa | cartão âmbar | sino, quando o motor alerta o limite de uso |
| Espera a sua decisão | aviso na tela e faixa | aviso no sino ("Nova aprovação", do `mestre approval ask`), número em Aprovações, título da aba "(N)" e Telegram |
| Travada | aviso na tela e faixa | sino, no alerta de etapa travada |
| Em revisão | aviso discreto | nada |
| Entregue | aviso na tela, cartão esmaecido | sino, se o Grão-Mestre avisar; **Entregas recentes** no Início |
| Cancelada | aviso na tela | conversa |

- **Aviso na tela** (embaixo à direita, com a ação, `role="status"`, some em 9 s, fecha com ×): nasce de **comparar a leitura nova do `/view` com a anterior**, só para as mudanças que importam (nasceu, decisão, travada, em revisão, entregue, cancelada), nunca na primeira leitura da tela nem para "trabalhando". Depois de reconectar o canal, a comparação cobre o que faltou. No celular fica acima das abas.
- **Título da aba:** "(N) Mestre", com N de `/api/approvals/count`; some quando N é 0.

### Sino e histórico

O sino é o de [avisos](conversa-e-aprovacoes.md#avisos) e diz sempre o **nome humano** da mesa (`loja › Login com Google`), nunca o identificador. Esta unidade acrescenta: **Novos** e **Todos** (até 30 dias, que já é a janela do servidor), avisos agrupados por dia, o tipo dito em palavras (Aprovação, Grão-Mestre, Mesa parada, Alerta do motor, Sistema, GitHub) com ícone, e **Marcar todos como lidos**. Abrir um aviso o marca como lido e leva ao lugar. O histórico de uma mesa que acabou é a própria mesa encerrada (campanha com **Mostrar encerradas**) e, para decisões, a aba [Histórico](conversa-e-aprovacoes.md#central-de-aprovações) das aprovações.

### A casca e a conversa

A barra de cima é a da [jornada](design.md#estrutura-da-tela): marca, sino, **Falar com o Grão-Mestre** (botão cheio) e avatar com o menu da conta. O botão abre a **conversa lateral** (coluna à direita no computador, tela cheia no celular), a mesma em toda tela, com o cartão de andamento único: a conversa e esse cartão são da [conversa e aprovações](conversa-e-aprovacoes.md#conversa-com-o-grão-mestre), não desta unidade. Os termos (Campanha, Mesa, Companhia) são a própria palavra sublinhada com pontos, do componente `Term`: o mouse mostra o balão "No mundo real:", e tocar, clicar ou Enter o fixa; não há "?" ao lado ([design](design.md#ajuda-dos-termos)).

### Erros

| Caso | O que a tela faz |
|---|---|
| Falha ao ler `/view` | mantém o que havia, diz o horário do último dado bom e oferece **Tentar de novo** (`role="alert"`) |
| Canal `/ws/ui` caído | a faixa âmbar de [reconexão](design.md#reconexão); ao voltar, lê de novo e compara |
| Mesa sem issue | o aviso no cartão, com o motivo |
| Máquina sem sinal | o selo de saúde da campanha fica vermelho e leva à Guilda ([administração](administracao.md#guilda-e-saúde-das-máquinas)) |
| Pausar recusado | a recusa do servidor, dita no cartão ([recusa](design.md#recusa-e-desfazer)); sem motivo, o botão fica desligado |

### O que o servidor não dá (fora desta unidade)

Uma mudança pequena no servidor: `POST …/tables/{table}/pause` passa a receber o `reason` obrigatório (o motor já guarda um motivo de pausa) e a auditoria o grava. O motivo é texto de usuário: passa pelo filtro de segredos ([segredos](../telegram/seguranca-do-canal.md#segredos); uma senha é recusada, um token de formato conhecido é trocado por `[segredo removido]`) e chega aos agentes citado como "texto de quem pausou, não verificado". Nenhuma rota nova. Ficam como lacunas, e só entram por decisão do Senhor da Guilda sobre o [escopo do MVP](../produto/escopo-mvp.md): o **nome e a tarefa de cada personagem** no bloco (hoje só a contagem por máquina, no canvas da mesa há mais); o **id do pedido de aprovação** no bloco (o botão abre Aprovações filtrada pela mesa e pela campanha); o **merge confirmado** antes do `table close`; o **motivo de uma mesa presa em encerrando** (vai pela conversa); a **máquina caída por mesa**; e um **aviso garantido de entrega** (hoje só se o Grão-Mestre notificar). A aprovação já chega ao sino.

### Implementação

- **Código:** `web/src/workspace/` (`situation.ts`, a função pura com a ordem e os filtros; `SituationChip`, `TableBlockCard`, `NeedsYouBand`, `TableFilters`, `TableToasts`, `WorkspaceScreen`, `names.ts` para o nome humano nos avisos), `web/src/components/TopBarSlots.tsx` (o sino), `web/src/app/Shell.tsx` (título da aba), `screens/home/` (Início com os mesmos rótulos) e, no servidor, `internal/panel/routes_view.go` (o motivo de **Pausar mesa**).
- **Escolhas:** o quadro continua o canvas (React Flow), e a lista é a do celular; abaixo de 768 px só há a lista. O nome humano da mesa no sino vem dos `/view` das campanhas dos avisos, lidos só com o sino aberto, e cai no identificador enquanto não chega. O aviso na tela compara a leitura nova do `/view` com a anterior no navegador, então só existe com a campanha aberta.

## Regras e erros

- Só se mostra o que o `/view` e os avisos já dizem; a situação é derivada, nada é guardado, e o servidor decide o que cada nível pode.
- Texto de situação e de aviso vem do catálogo `workspace`, nos dois idiomas; horários, no fuso da Guilda.

## Critérios de aceite

- **CA-VIDA-01** · *rápida* — **Dado** blocos trabalhando, esperando cota, pedindo aprovação, com a mesma falha repetida, em revisão, encerrando, entregue (com PR) e cancelada, **quando** renderizam, **então** cada um mostra a palavra, o ícone e a frase da tabela de situações, e nenhum depende só da cor.
- **CA-VIDA-02** · *rápida* — **Dado** uma mesa com espera de aprovação e de cota, **quando** um Aprovador a vê, **então** a situação é "Espera a sua decisão" com **Ver o pedido** e a mesa está na faixa; **quando** um Observador a vê, **então** o rótulo é o mesmo, sem botão e fora da faixa.
- **CA-VIDA-03** · *rápida* — **Dado** duas mesas urgentes (falha repetida e decisão) e três normais, **quando** a campanha abre, **então** a faixa "Precisa de você (2)" lista a travada primeiro; **dado** nenhuma urgente, **então** a faixa mostra "Nada espera por você agora." e não some em silêncio.
- **CA-VIDA-04** · *rápida* — **Dado** seis mesas e uma entregue, **quando** escolho o filtro Esperando, **então** só as com cota, outra mesa ou pausa aparecem, cada chip mostra a contagem e as entregues só vêm com **Mostrar encerradas**.
- **CA-VIDA-05** · *rápida* — **Dado** uma mesa em revisão, **quando** o cartão renderiza, **então** a trilha marca "Em revisão" com `aria-current="step"` e as duas etapas anteriores como feitas; **dado** uma mesa cancelada, **então** não há trilha.
- **CA-VIDA-06** · *rápida* — **Dado** a leitura anterior com a mesa trabalhando, **quando** o evento traz a mesa em revisão, **então** aparece um aviso `role="status"` "está em revisão (PR #n)" uma vez; **dado** a primeira leitura ou trabalhando → trabalhando, **então** nenhum aviso aparece; **dado** uma decisão nova e um Observador, **então** ele não recebe o aviso de decisão.
- **CA-VIDA-07** · *rápida* — **Dado** uma mesa `working` há 4 h sem entrega (rótulo Travada) e um Aprovador, **quando** toca **Pausar mesa**, **então** abre o campo Motivo e o botão só liga com o motivo escrito, e depois o cartão passa a "Esperando" com "Pausada por: <motivo>"; **dado** o servidor recusar, **então** o cartão diz quem recusou e por quê e a situação não muda.
- **CA-VIDA-08** · *rápida* — **Dado** a leitura do `/view` falhando depois de uma boa, **quando** a tela atualiza, **então** os cartões continuam, aparece "Não consegui atualizar a campanha" com o horário do último dado bom e **Tentar de novo**, e nenhuma mesa vira "encerrada" por isso.
- **CA-VIDA-09** · *rápida* — **Dado** uma mesa com `issue_warning` e outra com falha repetida e `where`, **quando** renderizam, **então** a primeira mostra o aviso com o motivo e a segunda só uma frase, com a impressão e o `arquivo:linha` dentro de "Detalhes técnicos", recolhido.
- **CA-VIDA-10** · *rápida* — **Dado** a tela de 390 px, **quando** a campanha abre, **então** há só a lista, com a faixa "Precisa de você" no alto, o Grão-Mestre como primeiro cartão e as mesas por urgência, os alvos têm 44 px ou mais e nada rola na horizontal.
- **CA-VIDA-11** · *rápida* — **Dado** duas aprovações pendentes, **quando** qualquer tela abre, **então** o título da aba começa com "(2)", e com zero não há prefixo.
- **CA-VIDA-12** · *rápida* — **Dado** uma mesa cujo estado muda por evento do tópico `workspaces:all`, **quando** o cartão se atualiza, **então** a situação, a frase e a faixa mudam sem recarregar e a mesa nova traz o aviso "Nova mesa".
- **CA-VIDA-13** · *rápida* — **Dado** uma mesa que passa a esperar uma aprovação para um Aprovador, **quando** o aviso chega, **então** o sino mostra "Nova aprovação" com `campanha › título da mesa` (nunca o identificador `42-login-google`), o contador sobe e **Ver o pedido** leva à central filtrada por aquela mesa.
- **CA-VIDA-14** · *rápida* — **Dado** o painel em qualquer largura, **quando** o usuário toca **Falar com o Grão-Mestre**, **então** a conversa abre como painel lateral (tela cheia no celular) por cima da campanha, sem trocar de página, e o botão tem 44 px ou mais.

## Depende de

- [Visão da campanha](visao-do-workspace.md)
- [Conversa e aprovações](conversa-e-aprovacoes.md)
- [Canvas da mesa](canvas.md)
- [Mesas](../orquestracao/mesas.md)
- [Supervisão da cadeia](../orquestracao/supervisao-da-cadeia.md)
