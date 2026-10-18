# Visão do workspace

> **Assunto:** tela do workspace com o Grão-Mestre ao centro e as mesas ao redor, e o seletor de workspaces · **Componente:** Painel · **Atualizado:** 2026-10-18

## Objetivo

Dar, numa tela só, o estado de todas as mesas de um workspace e o caminho para entrar em cada uma.

## Comportamento

### Seletor de workspaces

- Fica no topo de todas as telas do painel.
- Lista só os workspaces que o usuário pode ver ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)); o Dono vê todos.
- O último workspace aberto fica lembrado por usuário (no banco da Cidadela, não no navegador).
- Criar workspace (Dono ou Administrador) abre o fluxo de [sessão zero do workspace](../sessao-zero/workspace.md). Enquanto o workspace está `preparing`, a tela mostra a etapa atual da sessão zero, as concluídas e o erro de uma etapa que falhou, com o botão "tentar de novo" ([estados do workspace](../orquestracao/workspaces.md#estados)).

### Layout

- Canvas do mesmo tipo do [canvas da mesa](canvas.md) (React Flow), só que com elementos do workspace.
- **Grão-Mestre ao centro**, como personagem com terminal ao vivo (mesmas regras de leitura e digitação do [canvas](canvas.md#digitar-no-terminal)).
- **Cada mesa é um bloco** ligado ao Grão-Mestre por uma linha. Posição inicial automática em círculo; o usuário pode arrastar, e a posição fica salva.
- Não há outros elementos: pergaminhos existem só dentro das mesas ([pergaminhos e tomos](../orquestracao/pergaminhos-e-tomos.md)).

### Bloco da mesa

| Campo | Conteúdo |
|---|---|
| Título | título do épico e número da issue, com link para o GitHub |
| Status | estado da mesa ([mesas](../orquestracao/mesas.md#estados)) e a marca "pausada", quando houver |
| Branch | nome da branch da mesa (ex.: `epico/42-login`) |
| Em andamento | a issue da missão atual, com link, e a etapa atual |
| Party | quantos personagens, e em quais máquinas (selos) |
| Esperas | marcador quando a mesa está aguardando algo (aprovação, cota, outra mesa), com o motivo; a missão pausada por "mesma falha repetida" mostra a impressão e o `arquivo:linha` ([prova e devolução](../orquestracao/prova-e-devolucao.md#devolução-com-causa-e-mesma-falha)) |
| Critérios | da missão atual: quantos têm prova e quantos destes foram verificados pelo motor ([prova por critério](../orquestracao/prova-e-devolucao.md#prova-por-critério)) |
| Última entrega | o instante da última [entrega útil](../orquestracao/supervisao-da-cadeia.md#entrega-útil-e-mesa-que-gira); passado o limite do aviso, o selo "sem entrega há <tempo>" |
| PR | link do PR da mesa, quando existir |
| Uso | consumo da mesa em relação ao limite, quando houver limite ([limites de uso](../plataforma/limites-de-uso.md)) |

- **Clicar numa mesa** abre o [canvas da mesa](canvas.md) (rota própria por mesa, que pode ser guardada como favorito).
- Mesas **encerradas e canceladas** (`closed`, `cancelled`) ficam escondidas por padrão; um filtro "mostrar encerradas" as exibe esmaecidas.
- Atualização em tempo real pelo WebSocket do painel ([protocolo](../conectividade/protocolo.md)).

### Atalhos da tela

- Abrir a [conversa com o Grão-Mestre](conversa-e-aprovacoes.md) do workspace.
- Contador de aprovações pendentes do usuário, que leva à [central de aprovações](conversa-e-aprovacoes.md#central-de-aprovações).
- Selo de saúde (Cidadela e Torres), que leva a [administração](administracao.md#guilda-e-saúde-das-máquinas).

## Regras e erros

- Workspace sem permissão (link antigo, por exemplo) → tela "sem acesso" com a lista dos workspaces permitidos.
- Mesa sem issue ainda criada no GitHub (falha na criação) → o bloco aparece com aviso e o motivo; o Grão-Mestre já foi avisado pelo motor ([mesas](../orquestracao/mesas.md)).
- Sem nenhuma mesa → mensagem curta explicando que mesas são abertas pelo Grão-Mestre a partir de pedidos na conversa.

## Critérios de aceite

- **CA-VWS-01** · *rápida* — **Dado** um workspace com três mesas (em trabalho, em revisão, encerrada), **quando** a tela renderiza, **então** aparecem dois blocos com status, branch, épico e link, e a encerrada só com o filtro ligado.
- **CA-VWS-02** · *rápida* — **Dado** um Aprovador com acesso a um workspace de três, **quando** abre o seletor, **então** vê só aquele workspace.
- **CA-VWS-03** · *rápida* — **Dado** um bloco de mesa, **quando** clico nele, **então** a rota do canvas daquela mesa abre.
- **CA-VWS-04** · *rápida* — **Dado** uma mesa que muda de "em trabalho" para "em revisão" no motor, **quando** o evento chega pelo WebSocket, **então** o bloco muda sem recarregar a página.
- **CA-VWS-05** · *pesada* — **Dado** duas mesas trabalhando em paralelo, **quando** abro a visão do workspace e entro em cada mesa, **então** vejo o estado de cada uma e o canvas certo de cada uma (MVP-11).
- **CA-VWS-06** · *rápida* — **Dado** um workspace em `preparing` com a etapa `mirror` com erro, **quando** a tela abre, **então** mostra `apps` concluída, `mirror` com o motivo do erro e o botão "tentar de novo", e nenhum bloco de mesa nem pergaminho.
- **CA-VWS-07** · *rápida* — **Dado** a missão atual de uma mesa com C1 verificado pelo motor, C2 declarado e C3 sem prova, e a mesa `working` há 4 h sem entrega útil, **quando** o bloco renderiza, **então** mostra "Critérios 2 de 3 (1 verificado)" e o selo "sem entrega há 4 h"; **dado** a missão pausada por "mesma falha repetida", **então** a coluna de esperas mostra o motivo e o `arquivo:linha`.

## Depende de

- [Canvas da mesa](canvas.md)
- [Mesas](../orquestracao/mesas.md)
- [Workspaces](../orquestracao/workspaces.md)
- [Grão-Mestre](../orquestracao/grao-mestre.md)
- [Supervisão da cadeia](../orquestracao/supervisao-da-cadeia.md)
- [Prova e devolução](../orquestracao/prova-e-devolucao.md)
- [Login](login.md)
- [Protocolo](../conectividade/protocolo.md)
