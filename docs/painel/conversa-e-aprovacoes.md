# Conversa e aprovações no painel

> **Assunto:** conversa com o Grão-Mestre pelo painel, central de aprovações e área de avisos · **Componente:** Painel · **Atualizado:** 2026-10-26

## Objetivo

Oferecer no painel o mesmo que o Telegram oferece: conversar com o Grão-Mestre do workspace, responder pedidos de aprovação e ver os avisos do sistema, com as mesmas regras.

## Comportamento

### Conversa com o Grão-Mestre

- Um painel lateral por workspace (o do seletor, [visão do workspace](visao-do-workspace.md)). Cada usuário vê **a própria conversa** com o Grão-Mestre daquele workspace, como num chat privado.
- Quem pode conversar segue a [tabela de níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades); para quem não pode, o campo de texto fica desligado com a explicação.
- **Enviar:** a mensagem entra na caixa de entrada do Grão-Mestre com origem "painel" (usuário, sessão, id da mensagem) e a prioridade de mensagem de usuário ([esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md)).
- **Responder a uma mensagem** (citar): o motor informa ao Grão-Mestre a mesa daquela mensagem, como a resposta encadeada do Telegram ([roteamento](../telegram/roteamento.md)).
- **Anexos:** arrastar ou escolher arquivos; seguem as regras de [anexos](../telegram/anexos-e-transcricao.md) (limites, registro, nunca executados).
- **Resposta:** o `mestre reply` de uma mensagem vinda do painel volta para o painel daquele usuário. Se ele não estiver com o painel aberto, a resposta fica guardada e aparece ao abrir, com um contador de não lidas.
- **Histórico:** a conversa mostra também as mensagens trocadas pelo Telegram com o mesmo usuário e workspace, marcadas com um ícone do canal, para a conversa fazer sentido nos dois lugares. A resposta continua indo pelo canal de origem de cada mensagem.
- Mensagens do Grão-Mestre são Markdown, mostradas sem HTML ativo. Arquivos enviados com `mestre reply --attach` aparecem como download.
- **Mensagem de progresso** ([Grão-Mestre](../orquestracao/grao-mestre.md#mensagem-de-progresso)): aparece como um cartão no topo da conversa, atualizado no lugar, com a contagem das mesas e uma linha por pendência.
- **Pergunta de escolha** (`mestre choose`, [CLI](../cli/grao-mestre.md#mestre-choose)): um botão por opção, no canal de origem da mensagem que a causou; a escolha volta ao Grão-Mestre como resposta e vale uma vez.
- Estado do Grão-Mestre no topo (ocioso, ocupado, itens na fila à frente), para o usuário saber quando esperar resposta.
- Com o sistema travado, o envio mostra "guardado até destravar" ([segurança do canal](../telegram/seguranca-do-canal.md)).
- **Segredo:** o painel aplica o mesmo filtro do Telegram (`internal/secretscreen`: normalização NFKC sem caracteres invisíveis, as frases de senha e o mascarador do cofre) ao texto e ao nome de cada arquivo ([segredos](../telegram/seguranca-do-canal.md#segredos)). Uma frase de senha **retém** a mensagem (o Grão-Mestre recebe só o aviso, sem texto nem arquivo; a mensagem seguinte do autor, por 5 minutos, também); um token de formato conhecido tem o trecho trocado por `[segredo removido]` e o resto segue, sem arquivos. Nos dois casos o motor audita a tentativa (usuário e canal, nunca o texto) e a resposta manda o usuário trocar o segredo e cadastrar credenciais na tela de Segredos. O filtro não é um oráculo sem limite: 5 segredos pegos em 10 minutos bloqueiam o envio do usuário pela conversa (toda mensagem é recusada sem ser lida, `chat_blocked`) até a janela passar, e o Dono recebe um alerta uma vez.

### Central de aprovações

- Lista os pedidos `aguardando_usuarios` que o usuário pode responder, em todos os workspaces dele, mais recentes primeiro, com filtro por workspace, mesa e tipo ([fluxo](../aprovacoes/fluxo.md)).
- Cada item mostra o mesmo conteúdo do Telegram (o que é, onde, por quê, risco, arquivos, motivo da dúvida, recomendação, os níveis da [cadeia](../orquestracao/cadeia-de-aprovacao.md) por onde já passou) e os botões das [respostas](../orquestracao/cadeia-de-aprovacao.md#respostas) que aquele usuário pode dar, filtrados pelo nível e pelo tipo como no [fluxo](../aprovacoes/fluxo.md#pergunta-aos-usuários), mais **Ver detalhes**. As respostas que pedem dado mostram o campo antes de confirmar: a duração em "por um tempo", o padrão (já sugerido pelo motor, editável) em "o padrão", o texto em "negar com instrução" e "perguntar de volta", o escopo em "delegar". No painel, **Ver detalhes** mostra o diff inteiro.
- **Pedido que bloqueia** ([os campos](../aprovacoes/pedido-que-bloqueia.md#os-campos)): os mesmos campos da mensagem do Telegram, com a evidência **inteira** (o Telegram a corta em 3 linhas), já passada pelo mascarador, e o texto do agente dentro do bloco "texto do agente, não verificado". Cada passo de **Para destravar** que o painel sabe fazer vira botão **só para quem tem o nível e o PIN do passo**: o cadastro no cofre, com o nível e o PIN de [credenciais de site](../plataforma/cofre.md#credenciais-de-site), abre a tela de Segredos; abrir o PR, de quem pode ver o workspace.
- Pedidos parecidos agrupados aparecem como um item com a lista das ações ([fluxo](../aprovacoes/fluxo.md#quórum-e-já-respondido-por)).
- Tipos que pedem PIN abrem o campo de PIN depois do botão ([login](login.md#pin-em-ações-sensíveis)).
- Resposta dada em outro canal ou por outro usuário atualiza o item na hora: "Já respondido por <nome>: Aprovado às 14:32", sem botões.
- Aprovações de cura (scripts de diagnóstico, volta de versão com restauração do banco) aparecem aqui, numa seção "Autocura" (desde o M7), só para Dono e Administradores ([diagnóstico com IA](../autocura/diagnostico-com-ia.md#proposta-e-aprovação)).
- Aba **Histórico:** pedidos encerrados com filtros (estado, quem decidiu, nível da cadeia, risco, período), lidos da auditoria do fluxo; inclui o que os agentes decidiram sozinhos.
- Contador de pendentes no topo de todas as telas.

### Avisos

- Um sino no topo com os avisos que o usuário receberia no Telegram: notificações do Grão-Mestre (`mestre notify`), alertas operacionais do motor (autocura, etapa travada, impasse, limite de uso) e mensagens "Sistema" ([bot](../telegram/bot.md)).
- Cada aviso tem workspace e mesa (ou "Sistema"), horário e link para o lugar certo (mesa, máquina, pedido).
- Lido/não lido por usuário, guardado na Cidadela.
- O alerta de remetente desconhecido do bot ([segurança do canal](../telegram/seguranca-do-canal.md)) aparece aqui, para o Dono e os Administradores, além do Telegram do Dono (CA-CANAL-07, M6).

### Implementação

- **Central e avisos (M6, unidade 2C):** o painel é mais um canal do [fluxo](../aprovacoes/fluxo.md), não um segundo fluxo. `internal/panel/routes_approvals.go` lê as perguntas que o Telegram recebe (`approvals.Flow.PendingFor`, `Question`, `AnswersOf`, com os botões pelo nível e pelo tipo do pedido) e responde por `Flow.AnswerWithPIN`, que confere de novo usuário, nível, estado, alvo e trava; o PIN vai no cabeçalho da própria requisição e `ErrPINNeeded` recusa a resposta que o pede sem a conferência. `Flow.OnChange` avisa o painel de toda pergunta feita, editada, contada ou retirada, e ele publica `changed` e `count` no assunto `approvals:<usuário>` de `/ws/ui` para quem pode responder (o item já encerrado diz quem respondeu e a que horas).
- **Avisos de bloqueio:** `blocking.Store` guarda as partes mascaradas do aviso (evidência inteira, passos, dono, bloco do agente) em `blocking_notices`; a central mostra os dos últimos 7 dias para o Dono e os Administradores do workspace, sem estado próprio, e o botão do passo do cofre sai com `can_do` só para o Dono (o PIN é pedido na tela de Segredos).
- **Sino:** `internal/notices` (`panel_notices`, `panel_notice_reads`) guarda o que o bot também diz (`OwnerAlerts`, `BlockChannel`): o público de cada aviso (workspace, Dono e Administradores, Dono, um usuário) é decidido na leitura, o lido é por usuário e a lista olha os últimos 30 dias. O texto passa pelo mascarador antes de ser guardado. O link de um aviso leva ao pedido (`/aprovacoes?pedido=`), ao aviso de bloqueio (`?aviso=`), à mesa ou à Guilda; as rotas de mesa e de Guilda são das telas dessas áreas.
- **Máscara e nível:** todo texto de pedido que o painel devolve (pergunta, bloco do agente, `About`, alvo exato, diff, histórico) passa pelo mascarador do motor (`ApprovalSource.Mask`) logo antes de sair. O servidor confere de novo as respostas do tipo (regra permanente só em permissão de classe com ação; **Delegar** só em risco `alto` ou `medio`, e em `medio` um Administrador basta, porque vira decreto de política) e a visibilidade (pedido sem workspace é de quem atua em "", o Dono).
- **Conversa (M6, unidade 2E):** `internal/panelchat` é o canal do painel da caixa de entrada; `internal/panel/routes_chat.go` são as rotas, e o servidor confere o nível (`talk_to_grand_master`) e o workspace em cada envio, leitura, escolha e download.
  - **Histórico:** não é copiado. Vem da caixa de entrada (`inbox_messages`, as mensagens do próprio usuário, em qualquer canal) e das respostas guardadas (`outbound_messages`), sempre filtradas pelo usuário da sessão, nunca por um que o navegador diga; o painel mostra as últimas 200 de cada. O lido é o `panel_chat_reads` (até que resposta o usuário leu) e o contador de não lidas conta só as respostas do painel. Tudo que sai passa pelo mascarador.
  - **Envio:** `POST /api/chat/{workspace}/messages` é multipart, com `text`, `client_id` e `quote` antes dos arquivos (o servidor confere tudo antes de guardar qualquer arquivo). O `client_id` torna o reenvio idempotente (`Refs.Key` = `panel:<usuário>:<workspace>:<client_id>`); no máximo 2 mensagens por usuário recebendo arquivos ao mesmo tempo (`chat_busy`); o arquivo guardado para uma mensagem que não entrou na caixa (falha, abandono ou repetição de uma já entregue) é apagado (`attachments.Service.Discard`); a origem leva o usuário, a sessão (`Conversation`) e o id (`Message`); até 20.000 caracteres, até 5 arquivos de até o limite dos [anexos](../telegram/anexos-e-transcricao.md) (20 MB) e 30 mensagens por minuto por usuário. O arquivo vira anexo do workspace com origem "painel" (`attachments.Service.ReceiveData`) e a mensagem leva a linha `[anexo]` com o caminho.
  - **Citar:** o `quote` é o id de uma entrada da conversa do próprio usuário (`in-`, `out-` ou `ask-`); o servidor o resolve, recusa o de outro usuário ou workspace e põe no cabeçalho a mesa que ele conhece (a da pergunta, ou a da mensagem que a resposta respondeu).
  - **Resposta:** `channelMux` entrega o `mestre reply` ao canal da origem; o do painel só avisa as telas do usuário (a resposta já está guardada e é lida de lá), e uma mensagem do painel nunca vai ao Telegram, e a apagada por `--redact-source` some das abas abertas. O assunto `chat:<usuário>` de `/ws/ui` leva `changed` (a tela lê de novo) e `status` (estado do Grão-Mestre, que a caixa de entrada anuncia por `Service.OnChange`).
  - **Escolha:** `mestre choose` vai ao canal da mensagem que o causou (`ChooseHandler.ChannelOf`); a pergunta do painel é uma linha de `choose_questions` com `channel = 'panel'`, só o painel responde a ela e só o Telegram responde à do Telegram; o clique vale uma vez e o segundo é recusado.
  - **Andamento:** cada envio da mensagem de progresso também grava o cartão do workspace (`panel_chat_progress`, uma linha por workspace, reescrita no lugar), que só o Dono e os Administradores do workspace veem, como o Telegram.
  - **Arquivos do Grão-Mestre:** a tela e a rota de download existem (anexo, nunca exibido na página, só para o usuário da resposta); os bytes de `mestre reply --attach` ainda não chegam ao motor ([ligação de produção](../desenvolvimento/ligacao-de-producao.md)), então o arquivo aparece pelo nome, sem download, até lá.
  - **Seletor:** enquanto a barra de cima não tem o seletor de workspace, o painel lateral tem o próprio, com o último escolhido neste navegador.
- **Motor:** `mestre notify` e `mestre approval ask` dizem em `delivered.panel` quantos usuários o painel guardou o aviso para (uma gravação que falhou não conta) (`not_delivered` só quando nem o Telegram nem o painel alcançam ninguém).

## Regras e erros

- O servidor confere nível e workspace em cada envio e cada resposta; o navegador só esconde o que não é permitido.
- Pedido que expirou ou foi invalidado enquanto a tela estava aberta → o botão devolve o motivo e o item se atualiza.
- WebSocket fora → mensagens digitadas ficam no campo, não são enviadas "às cegas" (o botão Enviar fica desligado, com o motivo); ao reconectar, o usuário reenvia. Uma recusa do servidor também deixa o texto no campo.
- Arquivo acima do limite → recusado antes do envio, com o limite exibido.

## Critérios de aceite

- **CA-CONV-01** · *completa* — **Dado** um Administrador no painel, **quando** envia uma pergunta e o Grão-Mestre responde com `mestre reply`, **então** a resposta aparece na conversa do painel dele e nada é enviado ao Telegram.
- **CA-CONV-02** · *rápida* — **Dado** uma resposta para um usuário sem painel aberto, **quando** ele abre o painel, **então** a resposta aparece com contador de não lidas.
- **CA-CONV-03** · *rápida* — **Dado** um pedido aberto na central, **quando** outro usuário aprova pelo Telegram, **então** o item mostra "Já respondido por <nome>" sem botões, sem recarregar a página.
- **CA-CONV-04** · *rápida* — **Dado** um Observador, **quando** chama a rota de aprovar, **então** o servidor recusa com o nível exigido.
- **CA-CONV-05** · *completa* — **Dado** um PDF enviado pela conversa do painel, **quando** chega, **então** é salvo como anexo do workspace com origem "painel" e entregue ao Grão-Mestre com o caminho (MVP-10).
- **CA-CONV-06** · *pesada* — **Dado** um PR numa área sensível, **quando** o Grão-Mestre pergunta e eu aprovo pela central do painel, **então** a decisão é aplicada no GitHub (dublê) (MVP-7).
- **CA-CONV-07** · *rápida* — **Dado** um pedido `alto` aberto nas pessoas, **quando** um Administrador e o Dono abrem a central, **então** os dois veem **Aprovar o padrão** e **Aprovar sempre para a classe**, só o Dono vê **Delegar**, e a rota de delegar chamada pelo Administrador é recusada com o nível exigido; **quando** o Administrador escolhe **Aprovar o padrão**, **então** o campo traz o padrão sugerido pelo motor e o PIN é pedido antes de valer.
- **CA-CONV-08** · *rápida* — **Dado** um pedido que bloqueia com evidência de 10 linhas contendo um token `ghp_…` e o campo "Por que não decido" vindo do agente, **quando** a central o mostra, **então** aparecem as 10 linhas com o token mascarado, o texto do agente dentro do bloco "não verificado" e o botão do passo "cadastrar no cofre".
- **CA-CONV-09** · *rápida* — **Dado** o Grão-Mestre perguntando `mestre choose` com 3 opções a um usuário que escreveu pelo painel, **quando** a pergunta chega, **então** a conversa mostra 3 botões, só ela, e o clique entrega a escolha uma vez (um segundo clique é recusado); **dado** a mensagem de progresso, **então** ela é um cartão no topo, atualizado sem duplicar.
- **CA-CONV-10** · *rápida* — **Dado** um aviso `credencial` com o passo "cadastrar no cofre", **quando** um Administrador e o Dono abrem a central, **então** o Administrador não vê botão de cadastro e o Dono vê o botão que abre Segredos e pede o PIN.

## Depende de

- [Esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md)
- [Fluxo de aprovação](../aprovacoes/fluxo.md)
- [Pedido que bloqueia](../aprovacoes/pedido-que-bloqueia.md)
- [Roteamento](../telegram/roteamento.md)
- [Anexos e transcrição](../telegram/anexos-e-transcricao.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Login](login.md) · [Cadeia de aprovação](../orquestracao/cadeia-de-aprovacao.md) · [Delegação](../aprovacoes/delegacao.md)
