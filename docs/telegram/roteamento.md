# Roteamento de mensagens

> **Assunto:** para qual workspace vai cada mensagem do Telegram, o cabeçalho das mensagens do bot e as mensagens "Sistema" · **Componente:** Core · **Atualizado:** 2026-10-12

## Objetivo

Com um único bot para todos os workspaces, entregar cada mensagem ao Grão-Mestre certo sem obrigar o usuário a escolher toda vez. Deixar claro, em toda mensagem do bot, de onde ela veio.

## Comportamento

### Escolha do workspace

Depois do [filtro do canal](seguranca-do-canal.md), o motor decide o workspace nesta ordem. A primeira regra que se aplica vence:

1. **Resposta encadeada:** a mensagem responde a uma mensagem do bot (`reply_to_message`). Vai para o workspace daquela mensagem.
2. **Atalho:** o texto (ou a legenda) começa com `#<nome-do-workspace>`. Vai para esse workspace, e o workspace ativo **não muda**.
   - O prefixo é retirado do texto entregue, e o roteamento fica registrado.
3. **Workspace ativo:** nos demais casos, vai para o workspace ativo do usuário.

```mermaid
flowchart TD
  M[mensagem autorizada] --> E{responde a msg do bot?}
  E -->|sim, de workspace| W1[workspace da msg + mesa]
  E -->|sim, Sistema| S[motor trata]
  E -->|não| P{"começa com #nome?"}
  P -->|sim| W2[workspace do atalho]
  P -->|não| A{tem workspace ativo?}
  A -->|sim| W3[workspace ativo]
  A -->|não| B[bot pergunta com botões]
  W1 & W2 & W3 --> K{usuário atua nele?}
  K -->|não| R[recusa e lista os permitidos]
  K -->|sim| G[caixa de entrada do Grão-Mestre]
```

- **Workspace ativo:**
  - Guardado por usuário no banco.
  - Quem atua num workspace só tem esse como ativo, sempre, e nunca precisa escolher.
  - Quem atua em vários troca com `/workspace`: o bot lista os workspaces permitidos em botões e marca o atual.
  - Se o usuário atua em vários e ainda não escolheu, o bot guarda a mensagem, pergunta com botões e entrega a mensagem depois da escolha. A escolha passa a ser o ativo.
- **Sem permissão:** se o workspace indicado (por qualquer regra) não está na lista do usuário ([usuários e níveis](usuarios-e-niveis.md)), o bot recusa e diz quais workspaces ele pode usar. Um `#nome` que não existe recebe a mesma resposta.

### Resposta encadeada

- Para cada mensagem enviada, o motor guarda: `chat_id`, `message_id`, workspace (ou "Sistema"), mesa (se houver), origem (motor ou Grão-Mestre) e data.
- O registro é mantido por 180 dias (padrão). Uma resposta a uma mensagem mais antiga cai na regra 2 ou 3.
- Se a mensagem respondida era sobre uma mesa, o Grão-Mestre recebe a indicação da mesa junto. Isso é uma dica: dentro do workspace, **não há escolha a fazer pelo usuário**. Tudo vai ao Grão-Mestre, que decide para qual mesa repassar e, na dúvida, pergunta ([Grão-Mestre](../orquestracao/grao-mestre.md)).

### Entrega ao Grão-Mestre

- A mensagem entra na caixa de entrada do Grão-Mestre do workspace. A fila e a prioridade estão em [esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md).
- **Origem gravada com a mensagem:**
  - canal `telegram`;
  - ID e nome do usuário e o nível dele;
  - `chat_id`;
  - `message_id`;
  - mesa indicada (se houver);
  - anexos;
  - se o texto veio de transcrição.
  - É com isso que `mestre reply` volta pelo mesmo canal.
- **Primeira linha do texto entregue ao agente,** para ele saber quem fala:
  `[Telegram · <nome> (<nível>) · mesa <mesa>]`. A parte da mesa só aparece quando há indicação de mesa. Depois vem o texto original, sem alteração.
- **Áudio e anexos:** o agente recebe as linhas do formato descrito em [anexos e transcrição](anexos-e-transcricao.md).

### Cabeçalho

- Toda mensagem do bot começa com uma linha em negrito que indica a origem:
  - `<workspace>`: mensagem do workspace;
  - `<workspace> · Mesa #<nº do épico> <título curto>`: mensagem sobre uma mesa;
  - `Sistema`: mensagem que não pertence a um workspace.
- O motor põe o cabeçalho a partir do workspace e da mesa da mensagem. O Grão-Mestre informa a mesa ao responder ou notificar ([CLI de comunicação](../cli/comunicacao.md)).

### Mensagens "Sistema"

- **O que é "Sistema":** alertas que não pertencem a um workspace, como autocura de um nó, checklist da máquina, tentativas não autorizadas, pareamento e `/travar`.
- **Quem responde:** as respostas a essas mensagens são tratadas **pelo próprio motor**, não pelo Grão-Mestre:
  - botões (por exemplo, a aprovação de uma ação de cura, [camadas](../autocura/camadas.md)) são executados pelo motor;
  - texto livre respondendo a uma mensagem "Sistema" recebe a lista de ações possíveis naquela mensagem.

## Regras e erros

- O roteamento é do motor. O Grão-Mestre nunca recebe mensagens de workspaces que não são dele.
- Comandos rápidos não passam pelo roteamento. O argumento opcional deles indica o escopo ([bot](bot.md#comandos-rápidos)).
- O painel tem o seletor de workspace na própria tela ([conversa no painel](../painel/conversa-e-aprovacoes.md)). Lá, as regras desta página não se aplicam.

## Critérios de aceite

- **CA-ROTA-01** · *rápida* — **Dado** um usuário com workspace ativo A, **quando** ele responde a uma mensagem do bot do workspace B, **então** a mensagem vai ao Grão-Mestre de B e o ativo continua A.
- **CA-ROTA-02** · *rápida* — **Dado** um usuário com ativo A, **quando** ele envia `#b corrige o build`, **então** o Grão-Mestre de B recebe "corrige o build" e o ativo continua A.
- **CA-ROTA-03** · *rápida* — **Dado** um usuário sem acesso a C, **quando** ele envia `#c oi`, **então** o bot recusa e lista os workspaces permitidos, sem enfileirar nada.
- **CA-ROTA-04** · *rápida* — **Dado** um usuário de dois workspaces sem ativo, **quando** ele manda texto, **então** o bot pergunta com botões e entrega o texto ao workspace escolhido, que vira o ativo.
- **CA-ROTA-05** · *rápida* — **Dado** uma resposta a uma mensagem do bot sobre a mesa #42, **quando** ela é entregue, **então** a primeira linha para o Grão-Mestre traz "mesa #42" e a origem guarda a mesa.
- **CA-ROTA-06** · *rápida* — **Dado** qualquer mensagem de saída, **quando** é enviada, **então** a primeira linha é o cabeçalho correto (workspace, workspace · mesa ou Sistema).
- **CA-ROTA-07** · *rápida* — **Dado** uma resposta em texto a um alerta "Sistema", **quando** ela chega, **então** o motor responde com as ações possíveis e nada entra na caixa de entrada de agentes.

## Depende de

- [Bot](bot.md)
- [Segurança do canal](seguranca-do-canal.md)
- [Usuários e níveis](usuarios-e-niveis.md)
- [Esperas e caixa de entrada](../orquestracao/esperas-e-caixa-de-entrada.md)
- [Workspaces](../orquestracao/workspaces.md)
