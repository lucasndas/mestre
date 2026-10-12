# Conversa e aprovações no painel

> **Assunto:** conversa com o Grão-Mestre pelo painel, central de aprovações e área de avisos · **Componente:** Painel · **Atualizado:** 2026-10-12

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
- Estado do Grão-Mestre no topo (ocioso, ocupado, itens na fila à frente), para o usuário saber quando esperar resposta.
- Com o sistema travado, o envio mostra "guardado até destravar" ([segurança do canal](../telegram/seguranca-do-canal.md)).

### Central de aprovações

- Lista os pedidos `aguardando_usuarios` que o usuário pode responder, em todos os workspaces dele, mais recentes primeiro, com filtro por workspace, mesa e tipo ([fluxo](../aprovacoes/fluxo.md)).
- Cada item mostra o mesmo conteúdo do Telegram (o que é, onde, por quê, arquivos, motivo da dúvida, recomendação) e os botões **Aprovar**, **Negar** e **Ver detalhes**. No painel, **Ver detalhes** mostra o diff inteiro.
- Tipos que pedem PIN abrem o campo de PIN depois do botão ([login](login.md#pin-em-ações-sensíveis)).
- Resposta dada em outro canal ou por outro usuário atualiza o item na hora: "Já respondido por <nome>: Aprovado às 14:32", sem botões.
- Aprovações de cura (scripts de diagnóstico, volta de versão com restauração do banco) aparecem aqui, numa seção "Autocura", só para Dono e Administradores ([diagnóstico com IA](../autocura/diagnostico-com-ia.md#proposta-e-aprovação)).
- Aba **Histórico:** pedidos encerrados com filtros (estado, quem decidiu, período), lidos da auditoria do fluxo.
- Contador de pendentes no topo de todas as telas.

### Avisos

- Um sino no topo com os avisos que o usuário receberia no Telegram: notificações do Grão-Mestre (`mestre notify`), alertas operacionais do motor (autocura, etapa travada, impasse, limite de uso) e mensagens "Sistema" ([bot](../telegram/bot.md)).
- Cada aviso tem workspace e mesa (ou "Sistema"), horário e link para o lugar certo (mesa, nó, pedido).
- Lido/não lido por usuário, guardado no Core.

## Regras e erros

- O servidor confere nível e workspace em cada envio e cada resposta; o navegador só esconde o que não é permitido.
- Pedido que expirou ou foi invalidado enquanto a tela estava aberta → o botão devolve o motivo e o item se atualiza.
- WebSocket fora → mensagens digitadas ficam no campo, não são enviadas "às cegas"; ao reconectar, o usuário reenvia.
- Arquivo acima do limite → recusado antes do envio, com o limite exibido.

## Critérios de aceite

- **CA-CONV-01** · *completa* — **Dado** um Administrador no painel, **quando** envia uma pergunta e o Grão-Mestre responde com `mestre reply`, **então** a resposta aparece na conversa do painel dele e nada é enviado ao Telegram.
- **CA-CONV-02** · *rápida* — **Dado** uma resposta para um usuário sem painel aberto, **quando** ele abre o painel, **então** a resposta aparece com contador de não lidas.
- **CA-CONV-03** · *rápida* — **Dado** um pedido aberto na central, **quando** outro usuário aprova pelo Telegram, **então** o item mostra "Já respondido por <nome>" sem botões, sem recarregar a página.
- **CA-CONV-04** · *rápida* — **Dado** um Observador, **quando** chama a rota de aprovar, **então** o servidor recusa com o nível exigido.
- **CA-CONV-05** · *completa* — **Dado** um PDF enviado pela conversa do painel, **quando** chega, **então** é salvo como anexo do workspace com origem "painel" e entregue ao Grão-Mestre com o caminho (MVP-10).
- **CA-CONV-06** · *pesada* — **Dado** um PR numa área sensível, **quando** o Grão-Mestre pergunta e eu aprovo pela central do painel, **então** a decisão é aplicada no GitHub (dublê) (MVP-7).

## Depende de

- [Esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md)
- [Fluxo de aprovação](../aprovacoes/fluxo.md)
- [Roteamento](../telegram/roteamento.md)
- [Anexos e transcrição](../telegram/anexos-e-transcricao.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
- [Login](login.md)
