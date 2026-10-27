# Visão da campanha

> **Assunto:** tela da campanha com o Grão-Mestre no alto e as mesas ligadas a ele, como se chega a ela (lista, migalha, troca de campanha) e a campanha atual · **Componente:** Painel · **Atualizado:** 2026-10-27

## Objetivo

Dar, numa tela só, o estado de todas as mesas de uma campanha e o caminho para entrar em cada uma.

## Comportamento

### Como se chega e a campanha atual

- **Pela lista:** cada cartão da [lista de campanhas](workspaces.md) tem **Abrir** (leva a esta tela) e **Conversar** (abre a conversa naquela campanha); o cartão em preparação mostra a etapa e deixa **Abrir** desligado. Também chegam: o Início (mesas em andamento levam à mesa, e a migalha leva à campanha), os avisos e os pedidos de aprovação.
- **Migalha** (`Workspaces › loja`): o primeiro nível volta à lista; a campanha abre a **troca de campanha**, que lista só as que o usuário vê (o Senhor da Guilda vê todas) e "Ver todos". Escolher a campanha em que já se está fecha a lista e nada mais acontece. A mesa tem a migalha `Workspaces › loja › título da mesa` ([canvas](canvas.md)); o item Campanhas do menu fica marcado nas duas telas.
- **Campanha atual** é uma só para o painel inteiro: a da migalha; fora de uma campanha, a última aberta (`users.last_workspace`, guardado por usuário na Cidadela, não no navegador); sem nenhuma, a primeira da lista. A conversa, o botão **Falar com o Grão-Mestre** e os atalhos usam sempre esse, e a conversa diz de qual campanha fala. Não há seletor na barra de cima nem outro na conversa.
- A troca lista só as campanhas que o usuário pode ver ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)).
- **Criar campanha** (Senhor da Guilda ou Administrador, na lista de campanhas) abre o fluxo de [sessão zero da campanha](../sessao-zero/workspace.md). Enquanto a campanha está `preparing`, a tela mostra a etapa atual da sessão zero, as concluídas e o erro de uma etapa que falhou, com o botão "tentar de novo" ([estados da campanha](../orquestracao/workspaces.md#estados)).

### Layout

- Canvas do mesmo tipo do [canvas da mesa](canvas.md) (React Flow), só que com elementos da campanha, abrindo num zoom em que o texto é legível (17 px ou mais, como no [canvas](canvas.md#canvas-infinito)); no celular, em vez do canvas, cartões em lista, o do Grão-Mestre primeiro.
- **Grão-Mestre no alto**, como personagem com terminal ao vivo (mesmas regras de leitura e digitação do [canvas](canvas.md#digitar-no-terminal)).
- **Cada mesa é um bloco**, com o botão **Abrir mesa**, ligado ao Grão-Mestre por uma linha. Posição inicial automática (as mesas em fileira abaixo do Grão-Mestre, quebrando linha); o usuário pode arrastar, e a posição fica salva.
- Não há outros elementos: pergaminhos existem só dentro das mesas ([pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md)).

### Bloco da mesa

O bloco abre com a **situação** da mesa (uma palavra, um ícone e uma frase) e a tela ganha a faixa "Precisa de você", filtros por situação e a lista por urgência: ver [vida da mesa](vida-da-mesa.md). Os campos abaixo continuam, só que recolhidos em "Detalhes" quando não são a situação.

| Campo | Conteúdo |
|---|---|
| Título | título do épico e número da issue, com link para o GitHub |
| Status | estado da mesa ([mesas](../orquestracao/mesas.md#estados)) e a marca "pausada", quando houver |
| Branch | nome da branch da mesa (ex.: `epico/42-login`) |
| Em andamento | a issue da missão atual, com link, e a etapa atual |
| Companhia | quantos personagens, e em quais máquinas (selos) |
| Esperas | marcador quando a mesa está aguardando algo (aprovação, cota, outra mesa), com o motivo; a missão pausada por "mesma falha repetida" mostra a impressão e, quando a devolução trouxe `--where`, o `arquivo:linha` ([prova e devolução](../orquestracao/prova-e-devolucao.md#devolução-com-causa-e-mesma-falha)) |
| Critérios | da missão atual: quantos têm prova e quantos destes foram verificados pelo motor ([prova por critério](../orquestracao/prova-e-devolucao.md#prova-por-critério)) |
| Última entrega | o instante da última [entrega útil](../orquestracao/supervisao-da-cadeia.md#entrega-útil-e-mesa-que-gira); passado o limite do aviso, o selo "sem entrega há <tempo>", só em mesa `working` (nunca com a mesa pausada ou aguardando cota ou outra mesa), com o botão **Pausar mesa** para Aprovador ou acima, sem PIN ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)) |
| PR | link do PR da mesa, quando existir |
| Uso | consumo da mesa em relação ao limite, quando houver limite ([limites de uso](../plataforma/limites-de-uso.md)) |

- **Clicar numa mesa** abre o [canvas da mesa](canvas.md) (rota própria por mesa, que pode ser guardada como favorito).
- Mesas **encerradas e canceladas** (`closed`, `cancelled`) ficam escondidas por padrão; um filtro "mostrar encerradas" as exibe esmaecidas.
- Atualização em tempo real pelo WebSocket do painel ([protocolo](../conectividade/protocolo.md)).

### Atalhos da tela

- Abrir a [conversa com o Grão-Mestre](conversa-e-aprovacoes.md) da campanha.
- Contador de aprovações pendentes do usuário (o botão "N aprovações pendentes" da tela, além do número do menu), que leva à [central de aprovações](conversa-e-aprovacoes.md#central-de-aprovações).
- Selo de saúde (Cidadela e Torres), que leva a [administração](administracao.md#guilda-e-saúde-das-máquinas).

### Implementação

- **Código:** `internal/panel/routes_view.go` e `types_view.go` (rotas e tipos), `internal/app/viewhost.go` (o motor: blocos, entrega útil, esperas, uso), `internal/orchestration/quests/viewing.go` (critérios e falha repetida) e `web/src/workspace/` (seletor, tela, canvas e bloco). A rota do navegador é `/workspaces/<workspace>`.
- **Rotas** (o servidor confere nível e campanha em todas): `GET /api/workspaces/{slug}/view` (Observador ou acima; uma campanha que o usuário não vê é `not_found`, e `preparing` traz as etapas e nenhuma mesa), `PUT /api/workspaces/{slug}/view/layout` (só as chaves `gm` e `table:<id>`), `POST /api/workspaces/{slug}/tables/{table}/pause` (**Pausar mesa**: Aprovador ou acima que atua na campanha, sem PIN, auditada como `panel.workspace.table_paused`) e `PUT /api/me/workspace` (o último aberto, em `users.last_workspace`; só um que o usuário vê).
- **Tempo real:** a tela segue o tópico `workspaces:all`; cada `changed` (qualquer gravação no banco, depois de 150 ms de calma) faz ler a visão de novo.
- **Escolhas:** o selo "sem entrega há" usa o limite padrão de 3 h da [supervisão](../orquestracao/supervisao-da-cadeia.md#entrega-útil-e-mesa-que-gira); a posição é por campanha, compartilhada entre os usuários, como a do canvas da mesa; a impressão da falha repetida fica em `quest_paused_failure`, porque a contagem do passo recomeça na pausa; "Criar campanha" é da [tela das campanhas](workspaces.md), onde a sessão zero roda; o selo de saúde só aparece para quem vê a Guilda (Senhor da Guilda).
- **Mesa sem issue:** só o aviso do bloco quando o épico da mesa não tem número (`epic` 0), com o motivo; o `table open` já exige o épico, então é um caso defensivo.

## Regras e erros

- Campanha sem permissão (link antigo, por exemplo) → tela "sem acesso" com a lista das campanhas permitidas.
- Mesa sem issue ainda criada no GitHub (falha na criação) → o bloco aparece com aviso e o motivo; o Grão-Mestre já foi avisado pelo motor ([mesas](../orquestracao/mesas.md)).
- Sem nenhuma mesa → só o Grão-Mestre e uma mensagem curta explicando que mesas são abertas pelo Grão-Mestre a partir de pedidos na conversa.

## Critérios de aceite

- **CA-VWS-01** · *rápida* — **Dado** uma campanha com três mesas (em trabalho, em revisão, encerrada), **quando** a tela renderiza, **então** aparecem dois blocos com status, branch, épico e link, e a encerrada só com o filtro ligado.
- **CA-VWS-02** · *rápida* — **Dado** um Aprovador com acesso a uma campanha de três, **quando** abre o seletor, **então** vê só aquela campanha.
- **CA-VWS-03** · *rápida* — **Dado** um bloco de mesa, **quando** clico nele, **então** a rota do canvas daquela mesa abre.
- **CA-VWS-04** · *rápida* — **Dado** uma mesa que muda de "em trabalho" para "em revisão" no motor, **quando** o evento chega pelo WebSocket, **então** o bloco muda sem recarregar a página.
- **CA-VWS-05** · *pesada* — **Dado** duas mesas trabalhando em paralelo, **quando** abro a visão da campanha e entro em cada mesa, **então** vejo o estado de cada uma e o canvas certo de cada uma (MVP-11).
- **CA-VWS-06** · *rápida* — **Dado** uma campanha em `preparing` com a etapa `mirror` com erro, **quando** a tela abre, **então** mostra `apps` concluída, `mirror` com o motivo do erro e o botão "tentar de novo", e nenhum bloco de mesa nem pergaminho.
- **CA-VWS-07** · *rápida* — **Dado** a missão atual com C1 verificado pelo motor, C2 declarado e C3 sem prova, **quando** o bloco renderiza, **então** mostra "Critérios 2 de 3 (1 verificado)".
- **CA-VWS-08** · *rápida* — **Dado** a mesa `working` há 4 h sem entrega útil (relógio falso), **quando** o bloco renderiza, **então** mostra o selo "sem entrega há 4 h" e **Pausar mesa**, que um Aprovador toca e um Observador não vê; **dado** a mesma mesa pausada, **então** o selo não aparece.
- **CA-VWS-10** · *rápida* — **Dado** a lista com uma campanha ativa e outra em preparação, **quando** a renderizo, **então** o ativo tem **Abrir** (leva a `/workspaces/<slug>`) e **Conversar**, o outro tem **Abrir** desligado com a etapa, e escolher na migalha a campanha em que já estou não faz nada.
- **CA-VWS-11** · *rápida* — **Dado** a última campanha aberta `loja` e o usuário no Início, **quando** abre a conversa, **então** ela fala de `loja`; **dado** o usuário na visão de `blog`, **então** a conversa muda para `blog` sem outro seletor.
- **CA-VWS-09** · *rápida* — **Dado** a missão pausada por "mesma falha repetida", **quando** o bloco renderiza, **então** a coluna de esperas mostra o motivo e o `arquivo:linha`.

## Depende de

- [Canvas da mesa](canvas.md)
- [Mesas](../orquestracao/mesas.md)
- [Campanhas](../orquestracao/workspaces.md)
- [Grão-Mestre](../orquestracao/grao-mestre.md)
- [Supervisão da cadeia](../orquestracao/supervisao-da-cadeia.md)
- [Prova e devolução](../orquestracao/prova-e-devolucao.md)
- [Login](login.md)
- [Protocolo](../conectividade/protocolo.md)
