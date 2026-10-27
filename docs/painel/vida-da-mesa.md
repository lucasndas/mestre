# Vida da mesa no painel

> **Assunto:** como o painel conta a vida de uma mesa, do pedido à entrega (situação, ciclo, faixa "Precisa de você", filtros, avisos na tela e no sino, histórico e erros), no computador e no celular · **Componente:** Painel · **Atualizado:** 2026-10-27

## Objetivo

Quem abre a campanha todo dia precisa saber, em segundos, o que cada mesa está fazendo, o que espera por ele e o que já foi entregue, sem decifrar campos. Este documento é o dono da **situação da mesa** e de como ela chega à pessoa; o desenho foi aprovado num protótipo clicável (2026-10-27) e fica dentro da [jornada](primeira-jornada.md) (passos 5 a 8).

## Comportamento

### Situação: uma palavra por mesa

O painel deriva **uma situação** de cada bloco do [`/view`](visao-do-workspace.md#bloco-da-mesa) (estado, `paused`, `waits`, `stalled`, `pr`), na ordem abaixo; a primeira que vale ganha. Função pura no navegador, sem rota nova, a mesma para a campanha e para o Início ([Acontecendo agora](primeira-jornada.md#início-no-dia-a-dia)).

| Situação | Quando | Frase do cartão | Urgente |
|---|---|---|---|
| Entregue | `closed` com PR | "PR #n mesclado. Nada mais a fazer." | não |
| Encerrada / Cancelada | `closed` sem PR / `cancelled` | a retenção de 30 dias, na cancelada | não |
| Cancelando / Encerrando | `cancelling` / `closing` | o que o motor está fazendo; se algo ficar sem commit, o Grão-Mestre pergunta | não |
| Travada | espera `same_failure` | "A mesma falha voltou", com impressão e `arquivo:linha` | sim |
| Espera a sua decisão | espera `approval` | o que pedem e que o passo fica parado | sim (só para quem decide; os demais veem "Espera uma aprovação") |
| Pausada | `paused` | quem pausou e que o turno atual termina | não |
| Esperando | espera `quota`, `table` ou `other` | o que falta e que segue sozinha | não |
| Sem entrega há N h | `stalled` | "Está trabalhando, mas nada foi entregue há N h" | sim |
| Em revisão | `in_review` | "PR #n aberto. Falta o merge." | não |
| Preparando | `open` | "A Cidadela cria a branch e chama o Mestre da mesa" | não |
| Trabalhando | `working` | missão, etapa e companhia por máquina | não |

Cada situação tem **palavra, ícone de forma própria e tom** (a cor nunca é a única informação, [design](design.md#acessibilidade-e-movimento)): verde trabalhando, âmbar esperando ou parada, tijolo para o que pede a pessoa, vermelho para travada, verde cheio para entregue.

### O cartão da mesa

Substitui a grade de campos do [bloco](visao-do-workspace.md#bloco-da-mesa); os campos do bloco continuam, só mudam de lugar:

- **Em cima:** a situação, a marca "pausada" e "1 aviso novo" quando há aviso não lido daquela mesa no [sino](conversa-e-aprovacoes.md#avisos).
- **Título** (leva à mesa, [canvas](canvas.md)) e `#épico` (leva ao GitHub); **uma frase** da tabela acima, com missão, etapa e companhia embaixo.
- **Motivo**, quando há: a impressão e o `arquivo:linha` da falha repetida; o aviso "sem issue no GitHub" com o motivo.
- **Trilha do ciclo:** Aberta › Em trabalho › Em revisão › Encerrando › Encerrada, com o passo atual (`aria-current="step"`), só nas mesas que seguem o ciclo normal; cancelamento não tem trilha.
- **Ações** (só o que a situação pede): **Ver o pedido** (decisão), **Abrir a mesa** e **Conversar com o Grão-Mestre** (travada), **Pausar mesa** (sem entrega, Aprovador ou acima, sem PIN), **Ver o PR** (entregue).
- **Detalhes**, recolhido: branch, critérios (com prova e verificados), última entrega, PR e uso.

### Campanha: o que se vê e em que ordem

- **Faixa "Precisa de você (N)"** no alto, só com as mesas urgentes, cada uma com a frase e o botão; sem nenhuma, a linha "Nada espera por você agora." A faixa existe porque a posição que a pessoa arrasta no quadro continua salva, e o urgente não pode se perder num quadro bagunçado.
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
| Pede decisão | aviso na tela e faixa | número em Aprovações, título da aba "(N)" e Telegram |
| Travada ou sem entrega | aviso na tela e faixa | sino, no alerta de etapa travada |
| Em revisão | aviso discreto | nada |
| Entregue | aviso na tela, cartão esmaecido | sino, se o Grão-Mestre avisar; **Entregas recentes** no Início |
| Cancelada | aviso na tela | conversa |

- **Aviso na tela** (`role="status"`, some em 9 s, fecha com ×): nasce de **comparar a leitura nova do `/view` com a anterior**, só para as mudanças que importam (nasceu, decisão, travada, em revisão, entregue, cancelada), nunca na primeira leitura da tela nem para "trabalhando". Depois de reconectar o canal, a comparação cobre o que faltou. No celular fica acima das abas.
- **Título da aba:** "(N) Mestre", com N de `/api/approvals/count`; some quando N é 0.

### Sino e histórico

O sino é o de [avisos](conversa-e-aprovacoes.md#avisos). Esta unidade acrescenta: **Novos** e **Todos** (até 30 dias, que já é a janela do servidor), avisos agrupados por dia, o tipo dito em palavras (Grão-Mestre, Mesa parada, Alerta do motor, Sistema, GitHub) com ícone, e **Marcar todos como lidos**. Abrir um aviso o marca como lido e leva ao lugar. O histórico de uma mesa que acabou é a própria mesa encerrada (campanha com **Mostrar encerradas**) e, para decisões, a aba [Histórico](conversa-e-aprovacoes.md#central-de-aprovações) das aprovações.

### Erros

| Caso | O que a tela faz |
|---|---|
| Falha ao ler `/view` | mantém o que havia, diz o horário do último dado bom e oferece **Tentar de novo** (`role="alert"`) |
| Canal `/ws/ui` caído | a faixa âmbar de [reconexão](design.md#reconexão); ao voltar, lê de novo e compara |
| Mesa sem issue | o aviso no cartão, com o motivo |
| Máquina sem sinal | o selo de saúde da campanha fica vermelho e leva à Guilda ([administração](administracao.md#guilda-e-saúde-das-máquinas)) |
| Pausar recusado | a recusa do servidor, dita no cartão ([recusa](design.md#recusa-e-desfazer)) |

### O que o servidor não dá (fora desta unidade)

Nenhuma rota nova. Ficam como lacunas, e só entram por decisão do Senhor da Guilda sobre o [escopo do MVP](../produto/escopo-mvp.md): o **nome e a tarefa de cada personagem** no bloco (hoje só a contagem por máquina, no canvas da mesa há mais); o **id do pedido de aprovação** no bloco (o botão abre Aprovações filtrada pela mesa); o **merge confirmado** antes do `table close`; o **motivo de uma mesa presa em encerrando** (vai pela conversa); a **máquina caída por mesa**; e um **aviso garantido de entrega** (hoje só se o Grão-Mestre notificar).

## Regras e erros

- Só se mostra o que o `/view` e os avisos já dizem; a situação é derivada, nada é guardado, e o servidor decide o que cada nível pode.
- Texto de situação e de aviso vem do catálogo `workspace`, nos dois idiomas; horários, no fuso da Guilda.

## Critérios de aceite

- **CA-VIDA-01** · *rápida* — **Dado** blocos trabalhando, esperando cota, pedindo aprovação, com a mesma falha repetida, em revisão, encerrando, entregue (com PR) e cancelada, **quando** renderizam, **então** cada um mostra a palavra, o ícone e a frase da tabela de situações, e nenhum depende só da cor.
- **CA-VIDA-02** · *rápida* — **Dado** uma mesa com espera de aprovação e de cota, **quando** um Aprovador a vê, **então** a situação é "Espera a sua decisão" com **Ver o pedido**; **quando** um Observador a vê, **então** é "Espera uma aprovação", sem botão e fora da faixa.
- **CA-VIDA-03** · *rápida* — **Dado** duas mesas urgentes (falha repetida e decisão) e três normais, **quando** a campanha abre, **então** a faixa "Precisa de você (2)" lista a travada primeiro; **dado** nenhuma urgente, **então** a faixa mostra "Nada espera por você agora." e não some em silêncio.
- **CA-VIDA-04** · *rápida* — **Dado** seis mesas e uma entregue, **quando** escolho o filtro Esperando, **então** só as com cota, outra mesa ou pausa aparecem, cada chip mostra a contagem e as entregues só vêm com **Mostrar encerradas**.
- **CA-VIDA-05** · *rápida* — **Dado** uma mesa em revisão, **quando** o cartão renderiza, **então** a trilha marca "Em revisão" com `aria-current="step"` e as duas etapas anteriores como feitas; **dado** uma mesa cancelada, **então** não há trilha.
- **CA-VIDA-06** · *rápida* — **Dado** a leitura anterior com a mesa trabalhando, **quando** o evento traz a mesa em revisão, **então** aparece um aviso `role="status"` "está em revisão (PR #n)" uma vez; **dado** a primeira leitura ou trabalhando → trabalhando, **então** nenhum aviso aparece; **dado** uma decisão nova e um Observador, **então** ele não recebe o aviso de decisão.
- **CA-VIDA-07** · *rápida* — **Dado** uma mesa `working` há 4 h sem entrega e um Aprovador, **quando** toca **Pausar mesa**, **então** o cartão passa a "Pausada" com "Mesa pausada."; **dado** o servidor recusar, **então** o cartão diz quem recusou e por quê e a situação não muda.
- **CA-VIDA-08** · *rápida* — **Dado** a leitura do `/view` falhando depois de uma boa, **quando** a tela atualiza, **então** os cartões continuam, aparece "Não consegui atualizar a campanha" com o horário do último dado bom e **Tentar de novo**, e nenhuma mesa vira "encerrada" por isso.
- **CA-VIDA-09** · *rápida* — **Dado** uma mesa com `issue_warning` e outra com falha repetida e `where`, **quando** renderizam, **então** a primeira mostra o aviso com o motivo e a segunda a impressão e o `arquivo:linha`.
- **CA-VIDA-10** · *rápida* — **Dado** a tela de 390 px, **quando** a campanha abre, **então** há só a lista, o Grão-Mestre primeiro e a faixa "Precisa de você" antes das mesas, os alvos têm 44 px ou mais e nada rola na horizontal.
- **CA-VIDA-11** · *rápida* — **Dado** duas aprovações pendentes, **quando** qualquer tela abre, **então** o título da aba começa com "(2)", e com zero não há prefixo.
- **CA-VIDA-12** · *rápida* — **Dado** uma mesa cujo estado muda por evento do tópico `workspaces:all`, **quando** o cartão se atualiza, **então** a situação, a frase e a faixa mudam sem recarregar e a mesa nova aparece destacada.

## Depende de

- [Visão da campanha](visao-do-workspace.md)
- [Conversa e aprovações](conversa-e-aprovacoes.md)
- [Canvas da mesa](canvas.md)
- [Mesas](../orquestracao/mesas.md)
- [Supervisão da cadeia](../orquestracao/supervisao-da-cadeia.md)
