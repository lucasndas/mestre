# Cidadela e Torres

> **Assunto:** papéis na Guilda e o que roda onde, época e fencing, Cidadela suspensa, Torre condutora, proposta de assumir com aprovação do Dono, troca planejada, volta da antiga Cidadela e painel redirecionado · **Componente:** Motor · **Atualizado:** 2026-10-13

## Objetivo

Garantir que a Guilda tenha **no máximo uma Cidadela agindo para fora** (GitHub, Telegram) a qualquer momento, e que, quando a Cidadela cai de vez, uma Torre possa assumir o comando com o Grão-Mestre e o banco, sempre com a aprovação do Dono. Nunca existem duas Cidadelas.

## Comportamento

### Papéis

Toda máquina tem a mesma instalação; o papel vem do estado da Guilda ([componentes](../arquitetura/componentes.md)).

| O quê | Cidadela (uma por vez) | Torre (as demais) |
|---|---|---|
| Grão-Mestre de cada workspace | roda aqui | — |
| Bot do Telegram, GitHub, painel e API, descoberta como Cidadela, nome `mestre.local` | sim | não (o painel redireciona, abaixo) |
| Banco | principal, fonte da verdade | réplica só de leitura ([replicação](replicacao.md)) |
| Cofre | ativo | cópia cifrada, só se candidata |
| Personagens (Mestres de mesa, party) | sim | sim |
| Banco local de máquina, Guardião, terminais, worktrees | sim | sim |
| Vigiar a Cidadela | — | sim, pela conexão do motor e pela do Guardião |

Uma Guilda de uma máquina só é a Cidadela sozinha ([modo máquina única](../autocura/camadas.md#modo-máquina-única)).

### Época e fencing

- A Guilda tem uma **época**: um número crescente, gravado no banco, no banco local de cada máquina e em cada Guardião, provado pela [certidão de época](replicacao.md#certidão-de-época). Promover uma Torre incrementa a época.
- **Toda mensagem entre máquinas leva a época** ([protocolo](../conectividade/protocolo.md#envelope)); quem recebe uma época menor que a sua recusa com `stale_epoch`. Quem recebe uma época maior pede a certidão e, se ela vale, passa a segui-la.
- **Toda ação externa leva a época confirmada:** o motor só chama o GitHub, o Telegram ou qualquer serviço de fora depois de confirmar que a época dele é a atual. A Cidadela confirma quando:
  1. uma máquina da Guilda conecta ou responde com época igual ou menor; ou
  2. a Guilda não tem outra máquina que possa ser Cidadela (ninguém poderia ter sido promovido); ou
  3. o Telegram responde a duas consultas seguidas de `getUpdates` sem `409 Conflict` e a descoberta não achou época maior (o bot só tem uma instância ativa por vez, então o Telegram serve de testemunha).
- A confirmação vale enquanto a Cidadela não vê sinal contrário; cai na hora com uma época maior vista ou com um `409 Conflict` em `getUpdates` (outra instância do bot ativa).

### Cidadela suspensa

- Uma Cidadela sem época confirmada fica **suspensa**: não age para fora; o que ia ao GitHub e ao Telegram fica na fila de saída. Continua servindo o painel com a faixa "Cidadela suspensa: confirmando a época" e atendendo as Torres que já seguem a época dela.
- Confirmou a mesma época: sai da suspensão e esvazia a fila, na ordem. Viu época maior: rebaixa-se a Torre (abaixo) e descarta a fila, com registro na auditoria.
- Caso típico: a máquina ficou isolada da rede. Sem rede, nada confirma, e ela espera suspensa.

### Torre condutora

- Quando o motor da Cidadela falha, a recuperação segue as [camadas](../autocura/camadas.md), conduzida pela **Torre condutora**: a Torre online de maior prioridade configurada pelo Dono; empate pela melhor ficha da máquina e depois pelo menor `machine_id` ([eleição da Torre condutora](../autocura/camadas.md#eleição-da-torre-condutora)).
- Enquanto não há Cidadela, o Telegram fica em [modo reduzido](../autocura/telegram-modo-reduzido.md) no Guardião da Torre condutora, e a [página reserva](../autocura/pagina-reserva.md) dela mostra o incidente.
- A autocura mútua vale entre quaisquer duas máquinas: cada Guardião cuida do próprio motor, e o lado saudável pede ao Guardião do outro ([Guardião](../autocura/guardiao.md)).

### Proposta de assumir

- **Quando:** as camadas terminaram sem resolver, ou a máquina da Cidadela está inalcançável (motor e Guardião) há 15 min (padrão).
- **Quem é proposta:** a própria Torre condutora, se for candidata com réplica em dia; senão, a candidata online de maior prioridade, pela mesma regra. Sem candidata online, a mensagem diz isso e aponta a [chave de recuperação](../plataforma/cofre.md#backup-e-chave-de-recuperação).
- **O pedido** (tipo `promocao_cidadela`, só o Dono, com PIN; [pedidos da Guilda](../aprovacoes/fluxo.md#pedidos-da-guilda)): o Guardião da condutora manda ao Dono, pelo Telegram em modo reduzido e na página reserva: "A Cidadela <máquina> está fora desde HH:MM e a autocura não resolveu. Promover <candidata> a Cidadela? A réplica está em dia até HH:MM:SS." Botões **Promover** e **Esperar**; Promover exige o PIN do Dono. Só o Dono decide, nunca o Grão-Mestre nem outro nível.
- O pedido vale até ser respondido ou até a Cidadela voltar (então é cancelado). Lembrete a cada 1 h (padrão).
- **Nunca há promoção automática.**

### Promoção

1. O Guardião da Torre condutora confere o PIN na cópia de usuários dele ([cofre](../plataforma/cofre.md)) e manda a ordem de promoção ao Guardião da candidata (`citadel.promote`, [mensagens](../conectividade/mensagens.md)), que a repassa ao motor local.
2. O motor para de aplicar replicação, abre o cofre com a chave da máquina, incrementa a época, assina a certidão nova e passa a réplica a banco principal. A auditoria registra o último `lsn` aplicado.
3. Liga as funções da Cidadela: GitHub, bot do Telegram (o Guardião sai do modo reduzido), painel e API, `/join`, respostas da descoberta como Cidadela e o nome `mestre.local` no mDNS ([descoberta](../conectividade/descoberta.md)).
4. Inicia o Grão-Mestre de cada workspace a partir do banco, com a [retomada](../provedores/adaptador.md#retomada-de-sessão) quando possível ([Grão-Mestre](../orquestracao/grao-mestre.md)).
5. As outras Torres reconectam na nova Cidadela, aceitam a certidão e retomam a replicação (cópia completa se preciso). Os personagens que rodavam na antiga ficam interrompidos até serem recriados.

### Troca planejada

O Dono pode passar a Cidadela para uma candidata online pelo painel, com PIN: a Cidadela atual para de agir para fora, espera a candidata confirmar o último `lsn`, e a promoção segue os passos acima sem perda. A antiga vira Torre na hora. É o caminho para desinstalar a Cidadela ([saída](guilda.md#saída-e-remoção)).

### Volta da antiga Cidadela

- Ao subir, a antiga se acha Cidadela da época antiga e fica suspensa até confirmar. Uma Torre, a descoberta ou o `409 Conflict` mostram a época maior: ela se **rebaixa a Torre antes de qualquer ação externa**, registra o que descartou ([perda na troca](replicacao.md#perda-na-troca)), recebe cópia completa do banco e conecta na nova Cidadela. O banco local de máquina e os terminais dela continuam.
- Ela nunca disputa o comando; voltar a ser Cidadela só por troca planejada.

### Painel redirecionado

- Toda máquina da Guilda responde na porta do painel ([portas](../arquitetura/componentes.md#portas)). A Cidadela serve o painel; uma Torre ou uma Cidadela suspensa responde com redirecionamento `307` para `https://mestre.local:7443` (ou o nome da Cidadela atual, se o mDNS não alcança).
- Sem Cidadela no ar, a Torre responde uma página curta "Guilda sem Cidadela" com o link da página reserva da Torre condutora.

## Regras e erros

- Uma máquina nunca liga as funções da Cidadela sem certidão de época assinada e mais alta que todas as que viu.
- Promoção sem PIN válido do Dono, ou de máquina que não é candidata, é recusada e registrada.
- Se duas propostas chegarem ao Dono (condutora trocada durante o pedido), a segunda cancela a primeira.

## Critérios de aceite

- **CA-CIDADELA-01** · *rápida* — **Dado** uma máquina na época 3, **quando** chega uma mensagem com época 2, **então** ela recusa com `stale_epoch` sem executar nada.
- **CA-CIDADELA-02** · *rápida* — **Dado** uma Cidadela da época 1 que sobe e uma Torre que conecta com a certidão da época 2, **quando** o motor inicia, **então** ele se rebaixa a Torre e o GitHub e o Telegram falsos não recebem nenhuma chamada dele.
- **CA-CIDADELA-03** · *rápida* — **Dado** uma Cidadela isolada (rede falsa cortada, Telegram falso inalcançável), **quando** precisa comentar no GitHub, **então** fica suspensa e o comentário fica na fila; **dado** a rede de volta e a mesma época confirmada, **então** o comentário sai uma vez.
- **CA-CIDADELA-04** · *rápida* — **Dado** uma Cidadela confirmada, **quando** o Telegram falso responde `409 Conflict` em `getUpdates`, **então** ela suspende na hora.
- **CA-CIDADELA-05** · *rápida* — **Dado** as camadas esgotadas (relógio falso), **quando** a Torre condutora é candidata, **então** o Dono recebe a proposta com os botões; **dado** Promover com PIN errado ou nenhuma resposta, **então** nada muda; **dado** nenhuma candidata online, **então** a mensagem aponta a chave de recuperação.
- **CA-CIDADELA-06** · *completa* — **Dado** duas instâncias na mesma máquina, com relógio falso, Cidadela e Torre candidata, **quando** o motor e o Guardião da Cidadela param e o Dono aprova a proposta pelo Telegram falso, **então** a Torre vira Cidadela da época 2, com o estado do banco e o Grão-Mestre (agente falso) retomado.
- **CA-CIDADELA-07** · *completa* — **Dado** o cenário do CA-CIDADELA-06, **quando** a antiga Cidadela volta, **então** entra como Torre, a auditoria lista as transações descartadas e o GitHub falso não recebe nenhuma chamada dela.
- **CA-CIDADELA-08** · *completa* — **Dado** uma Torre e a Cidadela no ar, **quando** o navegador abre o painel na Torre, **então** recebe `307` para a Cidadela; **dado** a troca planejada, **então** a candidata assume sem transação perdida.
- **CA-CIDADELA-09** · *pesada* — **Dado** a Guilda com a Cidadela em Linux e a Torre candidata em Windows, e depois o inverso, **quando** derrubo a Cidadela de vez, **então** a Torre tenta a autocura, me pede pelo Telegram para assumir, eu aprovo, ela vira a Cidadela com o Grão-Mestre e o estado do banco, e a antiga, ao voltar, entra como Torre sem disputar o comando (MVP-15).

## Depende de

- [Guilda](guilda.md) · [Replicação](replicacao.md) · [Ficha da máquina](ficha-da-maquina.md)
- [Guardião](../autocura/guardiao.md) · [Camadas](../autocura/camadas.md) · [Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md) · [Página reserva](../autocura/pagina-reserva.md)
- [Grão-Mestre](../orquestracao/grao-mestre.md) · [Bot](../telegram/bot.md)

## A confirmar

- **Pendente (decisão do Dono):** a promoção de uma Torre a Cidadela é sempre manual? **Recomendação:** sim, sempre com aprovação do Dono no MVP (não há promoção automática).
