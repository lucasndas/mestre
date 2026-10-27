# Primeira jornada e Início

> **Assunto:** a jornada de quem acabou de instalar o Mestre (assistente do Senhor da Guilda, Primeiros passos, primeira campanha, primeiro pedido, a mesa, uma aprovação, a primeira entrega), a tela Início do dia a dia e o que cada passo pede do servidor · **Componente:** Painel · **Atualizado:** 2026-10-27

## Objetivo

Fazer o painel contar uma história só: quem acaba de instalar sabe o que fazer a seguir, chega à primeira entrega sem procurar tela em menu, e quem volta no dia seguinte vê em segundos o que os agentes estão fazendo e o que espera por ele. O protótipo clicável desta rodada (decidida em 2026-10-27, depois da primeira volta do Senhor da Guilda pelo painel) mostra as telas; a estrutura (menu, migalhas, barra de cima) está em [design](design.md#estrutura-da-tela).

## Comportamento

### A jornada

| # | Passo | O que a pessoa vê | Do servidor | Existe? |
|---|---|---|---|---|
| 0 | Instalar e confiar na CA | fora do painel ([primeiro acesso](../operacao/primeiro-acesso.md)) | `/ca.crt` | sim |
| 1 | Criar o Senhor da Guilda | assistente ([login](login.md#assistente-do-senhor-da-guilda-primeira-vez)); termina no **Início** com Primeiros passos, não numa tela vazia | `POST /api/setup` | sim |
| 2 | Primeiros passos | o cartão do Início (abaixo) | leituras listadas abaixo | sim, sem rota nova |
| 3 | Primeira campanha | **Campanhas → Criar**; a lista mostra o cartão "em preparação" com a etapa da sessão zero e vira "Ativo" sozinho (tópico `workspaces:all`) | `POST /api/workspaces`, `GET /api/workspaces` | sim |
| 4 | Primeiro pedido | **Falar com o Grão-Mestre** abre a conversa na campanha atual, com três sugestões de pedido; a pessoa escreve do jeito dela | `POST /api/chat/{workspace}/messages` | sim |
| 5 | A mesa aparece | a mensagem de progresso lista a mesa com link; o Início e a visão da campanha a mostram sem recarregar; "Abrir mesa" leva ao quadro | tópico `workspaces:all`, `GET /api/workspaces/{slug}/view` | sim |
| 6 | Acompanhar | no quadro, "Em palavras simples" diz o que cada personagem faz e o que espera; o terminal fica numa aba | `/view`, terminal | sim |
| 7 | Uma aprovação | o contador de **Aprovações** sobe, o sino avisa; o cartão diz o que pedem, por que, quem pediu e o que acontece se aprovar ou negar | `GET /api/approvals`, `POST /api/approvals/{id}/answer` | sim; texto: ver [conversa e aprovações](conversa-e-aprovacoes.md#central-de-aprovações) |
| 8 | A entrega | o aviso "PR mesclado", a mesa encerrada em **Entregas recentes** do Início e a mensagem do Grão-Mestre na conversa | eventos e mesas `closed` do `/view` | sim; **sem lista global de entregas** (lacuna abaixo) |

### Primeiros passos

- Cartão no topo do Início, **só para o Senhor da Guilda**; some quando os quatro primeiros passos estão feitos e não guarda estado: cada passo é **derivado** de dados que já existem, então reabrir o painel, trocar de aparelho ou fazer o passo por outro caminho (Telegram, Configurações) o marca sozinho.
- Os passos, com o que os marca como feitos:
  1. **Conectar o GitHub:** os dois Apps fixos criados (`GET /api/github/apps`); leva a Apps do GitHub ([criação de Apps](../github/criacao-de-apps.md)).
  2. **Entrar numa conta de IA:** ao menos um perfil de conta com a configuração concluída, `setup_done_at` preenchido (`GET /api/profiles`); leva a Perfis de conta ([perfis de conta](../orquestracao/bases-e-fichas.md#perfis-de-conta)).
  3. **Criar a primeira campanha:** ao menos uma campanha (`GET /api/workspaces`); leva a Campanhas, com o formulário aberto. Desligado até o passo 1.
  4. **Fazer o primeiro pedido:** ao menos uma mensagem do usuário na conversa de uma campanha (`GET /api/chat/{workspace}`); abre a conversa. Desligado até os passos 2 e 3.
  5. **Parear o Telegram (opcional):** o próprio usuário com `telegram` verdadeiro (`GET /api/users`); gera o código de pareamento ([parear o Telegram](../operacao/parear-telegram.md)). Não conta para o fim do cartão: depois dos quatro, o cartão vira uma linha com "Agora não", que guarda a escolha **só neste navegador**.
- Um passo desligado diz o motivo ("Faça antes o passo 1"), nunca fica mudo. O passo atual é o primeiro pendente e leva o botão principal.

### Início no dia a dia

Da esquerda para a direita e de cima para baixo, sempre nesta ordem; uma parte sem conteúdo some ou mostra um vazio que diz o que fazer, nunca contradiz o que está em outra parte:

- **Falar com o Grão-Mestre**, botão grande, com a campanha atual dita ao lado; desligado, com o motivo, sem campanha.
- **Acontecendo agora:** as mesas não encerradas de todas as campanhas do usuário, com campanha, título, a **situação** no rótulo do [vocabulário de estados](design.md#vocabulário-de-estados) (a mesma função da [campanha](vida-da-mesa.md#situação-uma-palavra-por-mesa)), quem está trabalhando e a última entrega; cada linha leva à mesa. Vazio: "Nada em andamento. Peça algo ao Grão-Mestre e a mesa aparece aqui."
- **Precisa de você:** o único nome das pendências, igual ao da campanha ([vida da mesa](vida-da-mesa.md#campanha-o-que-se-vê-e-em-que-ordem)): os pedidos de aprovação que o usuário pode decidir (só quem decide), as mesas Travadas e as sem entrega, cada um levando ao pedido ou à mesa. Vazio: "Nada espera por você agora."
- **Entregas recentes:** as mesas encerradas há pouco, com o PR mesclado.
- **Saúde da Guilda** (só para quem vê a Guilda): Cidadela e Torres, com "sem sinal desde HH:MM" ([Guilda](administracao.md#guilda-e-saúde-das-máquinas)).
- O Início se atualiza pelos tópicos que as telas já seguem (`workspaces:all`, `approvals:<usuário>`, `chat:<usuário>`); o horário vai no fuso da Guilda.

### O que o servidor ainda não dá (nenhuma rota nova nesta rodada)

- **Mesas de todas as campanhas:** não há uma lista só; o Início lê o `/view` de cada campanha que o usuário vê. É o suficiente para poucas campanhas; um agregador só entra se isso pesar, e aí é decisão do Senhor da Guilda (escopo do MVP).
- **Entregas recentes:** vêm das mesas `closed` que o `/view` já devolve (o filtro "mostrar encerradas"); não há histórico além delas.
- **Texto legível das aprovações:** o servidor devolve o texto do Telegram; o painel monta o cartão pelo `type`, pelo `risk` e pelo `about` e deixa o texto cru em "Ver detalhes técnicos" ([conversa e aprovações](conversa-e-aprovacoes.md#central-de-aprovações)).

## Regras e erros

- Quem não é Senhor da Guilda não vê Primeiros passos; sem campanha, vê "Peça ao Senhor da Guilda para dar acesso a uma campanha".
- Uma leitura que falha deixa o passo como "não sei" (ponto de interrogação e "tentar de novo"), nunca como feito nem como pendente.
- O Início não repete regras: níveis, campanhas e PIN são os do servidor, que decide de qualquer forma.

## Critérios de aceite

- **CA-JORN-01** · *rápida* — **Dado** o Senhor da Guilda recém-criado, sem GitHub, perfil nem campanha, **quando** o Início abre, **então** mostra Primeiros passos com "0 de 4", **Falar com o Grão-Mestre** desligado dizendo por quê e nenhuma frase de vazio que contradiga outra parte.
- **CA-JORN-02** · *rápida* — **Dado** o mesmo Senhor da Guilda sem os Apps, **quando** olha o passo 3, **então** o botão está desligado com "Faça antes o passo 1"; com os Apps criados, o botão liga.
- **CA-JORN-03** · *rápida* — **Dado** Apps criados, perfil configurado, uma campanha ativa e uma mensagem do usuário na conversa, **quando** o Início abre, **então** o cartão não aparece, e depois de recarregar continua sem aparecer (nada foi guardado).
- **CA-JORN-04** · *rápida* — **Dado** uma campanha ativa com três mesas e um pedido pendente, **quando** o Início abre, **então** lista as mesas com a situação e quem trabalha, e o pedido em Precisa de você com link, e a frase "Ainda não há nenhuma campanha" não aparece; **quando** uma quarta mesa nasce (evento do tópico `workspaces:all`), **então** ela aparece sem recarregar.
- **CA-JORN-05** · *rápida* — **Dado** um Administrador e um Observador, **quando** abrem o Início, **então** nenhum vê Primeiros passos; o Observador também não vê aprovações em Precisa de você.
- **CA-JORN-06** · *rápida* — **Dado** os quatro passos feitos e o Telegram sem parear, **quando** o Início abre, **então** só uma linha opcional aparece, e "Agora não" a esconde só naquele navegador.
- **CA-JORN-07** · *rápida* — **Dado** a conversa aberta numa campanha sem mesas, **quando** o Grão-Mestre abre uma mesa, **então** a mensagem de progresso lista a mesa com um link que leva a ela.
- **CA-JORN-08** · *rápida* — **Dado** uma leitura de passo que falha, **quando** o cartão renderiza, **então** o passo mostra "não sei" com "tentar de novo", e não "feito".
- **CA-JORN-09** · *rápida* — **Dado** um usuário em qualquer tela do painel, **quando** olha o menu, **então** vê Início, Aprovações, Campanhas e Configurações, e o item da seção atual está marcado, inclusive na visão da campanha, na mesa, no terminal e em cada tela de configuração.
- **CA-JORN-10** · *rápida* — **Dado** a mesa "Login com Google" da campanha `loja`, **quando** abre o quadro, **então** a migalha diz Campanhas › loja › Login com Google, os dois primeiros itens levam de volta e a campanha da migalha abre a troca de campanha.
- **CA-JORN-11** · *rápida* — **Dado** a tela de 390 px, **quando** o painel abre, **então** a barra de cima cabe numa linha sem sobrepor nada, o menu fica numa barra de quatro abas embaixo e nenhum item do menu fica cortado.
- **CA-JORN-13** · *rápida* — **Dado** a interface em português (painel, Telegram e CLI), **quando** os catálogos e as mensagens são varridos, **então** nenhum texto mostrado à pessoa usa "Workspace", "Party" ou "Dono" como nome, e o glossário tem as linhas Campanha, Companhia e Senhor da Guilda, com os identificadores técnicos (`workspace`, `owner`, as rotas) intactos ([ADR 0029](../decisoes/0029-vocabulario-campanha-senhor-companhia.md)).
- **CA-JORN-14** · *rápida* — **Dado** o painel em inglês, **quando** os catálogos são varridos, **então** o nível mais alto aparece como Guildmaster e o workspace como Campaign, e Party continua Party.
- **CA-JORN-12** · *pesada* — **Dado** um painel de instalação limpa com GitHub e agentes falsos, **quando** o Senhor da Guilda cria o usuário, conclui os Primeiros passos, faz o primeiro pedido, aprova o que pedirem e espera a entrega, **então** vê a mesa no Início, o pedido nas Aprovações e a mesa em Entregas recentes (MVP-11).

## Depende de

- [Login](login.md)
- [Visão da campanha](visao-do-workspace.md)
- [Campanhas no painel](workspaces.md)
- [Conversa e aprovações](conversa-e-aprovacoes.md)
- [Administração](administracao.md)
- [Canvas da mesa](canvas.md)
