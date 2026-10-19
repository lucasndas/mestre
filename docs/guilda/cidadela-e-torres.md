# Cidadela e Torres

> **Assunto:** papéis na Guilda e o que roda onde, época e fencing pela trava no GitHub, Cidadela suspensa, Cidadela fora e fila de saída, recuperação e aviso ao Dono, promoção manual, troca planejada, volta da antiga Cidadela, personagens, worktrees, missões e GitHub na troca, painel redirecionado · **Componente:** Motor · **Atualizado:** 2026-10-19

## Objetivo

Garantir que a Guilda tenha **no máximo uma Cidadela agindo para fora** (GitHub, Telegram) a qualquer momento, que as Torres sigam trabalhando com a Cidadela fora, e que, quando ela não volta, outra máquina assuma o comando com o Grão-Mestre e o banco, sempre por decisão do Dono. Nunca existem duas Cidadelas.

## Comportamento

### Papéis

Toda máquina tem a mesma instalação; o papel vem do estado da Guilda ([componentes](../arquitetura/componentes.md)). Toda máquina pode virar Cidadela, menos a [máquina excluída](guilda.md#máquina-excluída) pelo Dono.

| O quê | Cidadela (uma por vez) | Torre (as demais) |
|---|---|---|
| Grão-Mestre de cada workspace | roda aqui | — |
| Bot do Telegram, GitHub, painel e API, `/join`, nome `mestre.local` | sim | não (o painel redireciona, abaixo) |
| Banco | principal, fonte da verdade | réplica só de leitura ([replicação](replicacao.md)) |
| Cofre | aberto | cópia cifrada, fechada; nenhuma na excluída ([cofre em toda máquina](replicacao.md#cofre-em-toda-máquina)) |
| Espelho de cada workspace | principal | réplica ([worktrees na troca](#worktrees-e-espelho-na-troca)) |
| Personagens, banco local de máquina, Guardião, terminais, worktrees | sim | sim |
| Vigiar a Cidadela | — | sim, pela conexão do motor e pela do Guardião |

Uma Guilda de uma máquina só é a Cidadela sozinha ([modo máquina única](../autocura/camadas.md#modo-máquina-única)).

### Época e fencing

- A Guilda tem uma **época**: um número crescente, gravado no banco, no banco local de cada máquina e em cada Guardião, provado pela [certidão de época](replicacao.md#certidão-de-época). Promover uma máquina incrementa a época.
- **Toda mensagem entre máquinas leva a época** ([época no protocolo](../conectividade/protocolo.md#época)): época menor é recusada com `stale_epoch`; época maior faz pedir a certidão e, se vale, segui-la.
- **Trava no GitHub:** a época N vale quando a ref `refs/mestre/epoca-N` existe no repositório da trava da Guilda e é a de maior número. Criar uma ref existente é recusado, então só uma máquina vence a criação da época seguinte, e sem o GitHub não há promoção. Repositório, criação da ref, aluguel e cota: [trava no GitHub](trava-no-github.md).
- **Aluguel:** a Cidadela confere a trava a cada 30 s, e cada confirmação vale 60 s; toda chamada ao GitHub só sai com o aluguel válido, e quem é promovido espera 60 s antes de qualquer ação externa ([aluguel](trava-no-github.md#aluguel), [espera do promovido](trava-no-github.md#espera-do-promovido)). Vale para qualquer número de máquinas, inclusive duas.
- **GitHub fora não suspende:** se a conferência falha, a Cidadela segue no comando e no Telegram, guarda as ações do GitHub na fila e avisa o Dono; se ela lê uma época maior, suspende-se na hora ([GitHub fora](trava-no-github.md#github-fora)).
- **O `409 Conflict` do Telegram é só sinal:** com o aluguel válido, dispara uma conferência; com o aluguel vencido, para o Telegram até reconfirmar ([o `409`](trava-no-github.md#o-409-do-telegram)).
- **Exceção registrada:** o [Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md) da Torre condutora envia alertas e recebe `/status`, as aprovações de cura e, só do Dono, Promover e Esperar, sem a trava, porque não age como Cidadela. Um `409` entre ela e uma Cidadela viva isolada é degradação aceita: o Dono também decide pela página reserva ([risco residual](trava-no-github.md#regras-e-erros)).
- **Sem GitHub configurado na Guilda não há promoção** (a época não muda), e a Cidadela age no Telegram sem a trava.
- **No código:** a trava é a interface `guild.Lock` (`Check` lê a maior época; `Claim` cria a época N+1 e lê quem a ref nomeia), implementada sobre o GitHub no M5; até lá a Guilda real não tem trava (Telegram sem trava, nada no GitHub, sem promoção) e os testes usam a trava falsa ([dublês](../qualidade/dubles.md#outros-dublês)). O aluguel, a espera do promovido, a suspensão e a fila do que ia sair são do `guild.Fence`; a fila guarda a ordem de cada tipo de ação, para o GitHub sem aluguel não segurar o Telegram.
- **Guardião da Cidadela:** só publica `mestre.local` com a concessão do motor, renovada enquanto ele está no comando, sem depender do GitHub; é árbitro da eleição enquanto vive e a Cidadela não está suspensa, mesmo com o motor travado; com certidão mais nova, retira-se na hora ([concessão do motor](../autocura/guardiao.md#concessão-do-motor)).

### Cidadela suspensa

- A Cidadela fica **suspensa** quando a trava mostra uma época maior que a dela e, ao subir, até a primeira conferência da trava terminar. Suspensa, não age para fora (nem GitHub nem Telegram; o que ia sair espera na fila dela), deixa de renovar a concessão do Guardião, que retira `mestre.local`, e não aceita entradas (`503` em `/join`). Continua atendendo as Torres que seguem a época dela, mas não abre a chave da CA (abrir grava o uso), não poda o `replication_log` e responde `citadel_unavailable` ao `cli.call` delas; `cert.renew` e `guild.leave` recebem `unavailable` retentável, e quando ela passa a agir fecha essas Torres com `4005`, para a fila de saída delas reenviar (uma resposta de erro retentável mantém a mensagem na fila). As linhas de auditoria de uma certidão recusada e a varredura das entradas vencidas, que a antiga ainda grave depois de ler o último `lsn`, podem se perder (o cofre só é lido na Cidadela que age, antes da troca).
- **Painel:** quando conhece a Cidadela atual (certidão mais nova), responde `307` para ela; senão mostra só uma página "Cidadela suspensa: confirmando a época", sem ações.
- Ao subir, a primeira conferência que confirma a mesma época tira da suspensão e esvazia a fila, na ordem; uma que falha também tira, no modo [GitHub fora](trava-no-github.md#github-fora). Viu época maior: rebaixa-se a Torre ([volta da antiga](#volta-da-antiga-cidadela)) e descarta a fila, com registro na auditoria: os nomes das ações vão no `guild.discarded` dela, e a nova Cidadela grava `citadel.dropped_actions`.
- Uma antiga que já se rebaixou e reinicia antes de receber a certidão nova sobe direto como Torre, sem saber quem é a Cidadela: o `banco/descartado-<época>.db` da época da certidão dela é a marca do rebaixamento.

### Cidadela fora e fila de saída

- As Torres continuam rodando os personagens delas. O que depende da Cidadela (pedidos da CLI que não precisam de resposta imediata, como `mestre done`; eventos; resultados) vai para a **fila de saída** da Torre (`outbox`, [persistência](../arquitetura/persistencia.md)), cada item com id idempotente.
- Quando a Cidadela (a mesma ou uma nova) volta, a Torre reenvia na ordem, e a Cidadela reconcilia **sem duplicar**: descarta os ids já aplicados ([ack e idempotência](../conectividade/protocolo.md#ack-e-idempotência)).
- Pedidos da CLI que precisam de resposta imediata recebem `citadel_unavailable`; durante uma promoção, `citadel_in_failover` ([convenções da CLI](../cli/convencoes.md#regras-e-erros)).

### Recuperação e aviso ao Dono

- O motor e o Guardião sobem sozinhos com o sistema, nos dois sistemas, e retomam o estado (personagens, mesas, esperas, fila de saída) depois de um reinício. Com a máquina ligada, a [autocura](../autocura/camadas.md) conserta o Mestre travado.
- A recuperação da Cidadela é conduzida pela **Torre condutora**: a Torre online de maior prioridade, nunca a excluída ([eleição](../autocura/camadas.md#eleição-da-torre-condutora)). Enquanto não há Cidadela, ela cuida do Telegram em modo reduzido e da [página reserva](../autocura/pagina-reserva.md).
- **Árbitro é só otimização:** o Guardião da Cidadela arbitra a eleição enquanto vive e a Cidadela não está suspensa, mesmo com o motor travado ([concessão](../autocura/guardiao.md#concessão-do-motor)); sem ele, a eleição roda sem árbitro, pela regra determinística das [camadas](../autocura/camadas.md#eleição-da-torre-condutora). Seja qual for a condutora, quem decide se uma máquina vira Cidadela é sempre a [trava no GitHub](trava-no-github.md).
- **Aviso:** se a Cidadela não volta em **15 min** (padrão, configurável pelo Dono), a condutora avisa o Dono pelo Telegram em modo reduzido e pela página reserva, com o diagnóstico até ali e a opção de promover outra máquina (pedido `promocao_cidadela`, só o Dono, com PIN; [pedidos da Guilda](../aprovacoes/fluxo.md#pedidos-da-guilda)). O aviso se repete **a cada 1 h** enquanto ela continuar fora; se ela volta, o pedido é cancelado.

### Promoção

- **Só manual**, decidida pelo Dono com PIN: pelo Telegram, pela página reserva ou, com Cidadela no ar, pelo painel ([troca planejada](#troca-planejada)). Nunca há promoção automática.
- **Quem pode ser promovida:** qualquer máquina online da Guilda, menos a excluída e a que está fora da rede local (ninguém a alcança, [descoberta](../conectividade/descoberta.md#máquina-fora-da-rede-local)); o painel e o Telegram recusam esses alvos. A mensagem do aviso sugere a de maior prioridade com réplica em dia. **A própria máquina recusa antes de perguntar ao Dono** se o cofre dela não serve: a chave não carrega, não abre as linhas da réplica ou a réplica não tem a chave da CA ([cofre](../plataforma/cofre.md#regras-e-erros)). Depois de vencer a época, não há como voltar atrás.

1. O Guardião da condutora confere o PIN e manda `citadel.promote` ao Guardião da máquina escolhida ([mensagens](../conectividade/mensagens.md#guardião-da-torre--cidadela)), que repassa ao motor local.
2. O motor para de aplicar replicação (a CLI dele responde `citadel_in_failover`), lê o último `lsn` aplicado sem gravar nada e tenta criar a época N+1 na [trava](trava-no-github.md#promoção-pela-trava); se perde ou não chega à trava, volta a Torre da mesma Cidadela, com a réplica intacta, e avisa o Dono.
3. Vencendo: abre a réplica como banco principal, o cofre e a CA (abrir a chave da CA já grava), assina a certidão nova e passa o espelho a principal; o `lsn` lido no passo 2 fica em `epochs.from_lsn` e na auditoria (`citadel.promoted`). Uma falha depois de vencer deixa a época na trava sem certidão: o Dono é avisado e promove de novo (a época seguinte). Uma parada depois de gravar a certidão no banco e antes de guardá-la em `machine_identity` não se perde: ao subir, o motor segue a certidão maior e verificada que o banco da Guilda tem. Com a certidão gravada, emite para si o certificado da Cidadela, o único com `mestre.local`, de 7 dias, e passa a servi-lo na 7443 ([TLS e CA](../arquitetura/seguranca.md#tls-e-ca-interna)).
4. [Espera 60 s](trava-no-github.md#espera-do-promovido) sem nenhuma ação externa, confere a trava, [reconcilia o GitHub](#github-depois-da-troca) e só então liga GitHub, Telegram (o Guardião sai do modo reduzido), painel e API, `/join` e `mestre.local`. Uma conferência que falha mantém a espera; um reinício até 5 min depois do fim dela (contados de `epochs.started_at`) espera os 60 s inteiros, porque o relógio pode ter andado para a frente; depois disso, não espera.
5. Retoma personagens e missões (abaixo) e o Grão-Mestre de cada workspace, com a [retomada](../provedores/adaptador.md#retomada-de-sessão) quando possível. As outras Torres reconectam, aceitam a certidão e retomam a replicação.

### Troca planejada

O Dono pode passar a Cidadela a outra máquina pelo painel, com PIN: a Cidadela atual para de agir para fora e de aceitar mudanças (o que mudaria a Guilda, como `cert.renew`, `guild.leave`, `machine.sheet`, `guild.discarded`, o recibo `vault.dropped` e `cli.call`, recebe `unavailable` retentável, e a fila de saída de cada Torre o reenvia à nova Cidadela, como a abertura de uma conexão (`hello`, que leva a ficha) e a decisão do Dono sobre uma entrada, também recusadas; a antiga não abre a chave da CA nem renova o certificado dela; a CLI recebe `citadel_in_failover`), espera terminar as que já começou, lê o último `lsn`, espera a máquina escolhida confirmá-lo (`citadel.handover`, com quem aprovou; a escolhida responde `forbidden`, e a antiga segue agindo, se o cofre dela não serve) e a promoção segue os passos acima, com a espera de 60 s, sem perda. Enquanto espera a época nova, ela lê a trava com intervalo dobrando de 1 s a 10 s ([cota](trava-no-github.md#cota)). A antiga para de agir para fora na hora e vira Torre assim que a trava mostra a época nova. Devolver o comando à antiga é outra troca igual. É o caminho para desinstalar a Cidadela ([saída](guilda.md#saída-e-remoção)).

### Volta da antiga Cidadela

- Ao subir, a antiga se acha Cidadela da época antiga e fica suspensa. Uma Torre que conecta com a certidão nova ou a trava mostram a época maior (o broadcast da descoberta não leva a época de quem procura): ela se **rebaixa a Torre antes de qualquer ação externa** e fecha as Torres com `4007`, guarda o banco como `descartado-<época>.db`, recebe cópia completa do banco numa réplica nova e conecta na nova Cidadela. Ela nunca disputa o comando. Ao se rebaixar, ela volta na hora a apresentar o certificado de máquina; o com `mestre.local` que tinha vence em até 7 dias.
- **O que descartou:** quando a réplica conhece a época seguinte (`epochs.from_lsn`), a antiga lista as transações do banco guardado com `lsn` maior, sem as tabelas de controle do protocolo (`outbox`, `processed_commands`), e as manda à nova Cidadela em `guild.discarded`, pela fila de saída; a nova grava cada uma uma vez na auditoria (`citadel.discarded`), e o resumo fica em `descartado-<época>.json` ([perda na troca](replicacao.md#perda-na-troca)).
- **Personagens:** encerra tudo o que a nova Cidadela já retomou; os terminais dela que não foram retomados são informados à nova, que os adota.

### Personagens, missões e mesas na troca

- A nova Cidadela marca `interrupted` os personagens que rodavam na máquina da antiga (inclusive o Grão-Mestre e Mestres de mesa de lá) e os retoma pelas regras de [personagens](../orquestracao/personagens.md); os das Torres seguem.
- **Missões:** só são reentregues as etapas cujo personagem ficou `interrupted`; etapas em personagens vivos nas Torres seguem ([missões](../orquestracao/missoes.md)).

### Worktrees e espelho na troca

- Em toda máquina, `workspaces/<slug>/espelho.git` (o principal na Cidadela, a réplica nas Torres) e os worktrees `mesas/<mesa>/`, que saem do espelho local; o clone principal `principal/` existe na Cidadela e é replicado como arquivos ([persistência](../arquitetura/persistencia.md#arquivos), [mesas](../orquestracao/mesas.md)).
- **Na promoção,** a réplica do espelho vira o principal, e os worktrees existentes continuam válidos (nada muda de caminho).
- **No rebaixamento,** a antiga empacota em bundles os commits que só ela tinha (refs ausentes na nova) e os envia à nova Cidadela em `refs/mestre/recuperados/epoca-<N>/<mesa>`, avisando o Mestre da mesa; depois passa o espelho a réplica e o ressincroniza.

### GitHub depois da troca

- Toda criação no GitHub leva um [marcador de operação](../github/acoes-por-nivel.md#marcador-de-operação).
- Ao fim da [espera do promovido](trava-no-github.md#espera-do-promovido) e antes da primeira ação externa, a nova Cidadela **reconcilia**: lê o que os Apps da Guilda criaram nos últimos 30 min (padrão) e importa em `github_objects` os marcadores que não conhece ([persistência](../arquitetura/persistencia.md)). A fila de saída reenviada confere o marcador antes de criar. Evento externo com marcador da própria Guilda nunca vira "evento externo" ([eventos](../github/eventos.md)).

### Painel redirecionado

- Toda máquina responde na porta do painel ([portas](../arquitetura/componentes.md#portas)). A Cidadela serve o painel; uma Torre responde `307` para `https://mestre.local:<porta>` (ou o nome da Cidadela atual, se o mDNS não alcança), **preservando caminho e query**. A Cidadela suspensa segue a regra [acima](#cidadela-suspensa).
- Sem Cidadela no ar, a Torre responde uma página curta "Guilda sem Cidadela" com o link da página reserva da Torre condutora.

Ver também, de marcos posteriores: a [trava no GitHub](trava-no-github.md) (M5), o [Guardião](../autocura/guardiao.md), as [camadas](../autocura/camadas.md), o [Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md) e a [página reserva](../autocura/pagina-reserva.md) (M7), o [Grão-Mestre](../orquestracao/grao-mestre.md) e o [bot](../telegram/bot.md) (M5). Os critérios que dependem dessas peças fecham no marco delas ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md#critérios-em-marcos-posteriores)).

## Regras e erros

- Uma máquina nunca liga as funções da Cidadela sem certidão de época assinada, mais alta que todas as que viu, e sem ter vencido a criação da época na trava.
- Promoção sem PIN válido do Dono, de máquina excluída ou fora da rede local é recusada e registrada. Se duas propostas chegam ao Dono (condutora trocada durante o pedido), a segunda cancela a primeira.

## Critérios de aceite

- **CA-CIDADELA-02** · *rápida* — **Dado** uma Cidadela da época 1 que sobe e uma Torre que conecta com a certidão da época 2, **quando** o motor inicia, **então** ele se rebaixa a Torre e o GitHub e o Telegram falsos não recebem nenhuma chamada dele.
- **CA-CIDADELA-03** · *rápida* — **Dado** uma Cidadela isolada (rede falsa cortada) com o aluguel vencido, **quando** precisa comentar no GitHub, **então** não se suspende, o comentário fica na fila e o painel mostra "GitHub fora"; **dado** a rede de volta e a mesma época confirmada, **então** o comentário sai uma vez.
- **CA-CIDADELA-05** · *rápida* — **Dado** a Cidadela fora há 15 min (relógio falso), **quando** a condutora avalia, **então** o Dono recebe o aviso com o diagnóstico e os botões, e de novo 1 h depois; **dado** Promover com PIN errado, nenhuma resposta ou a Cidadela de volta, **então** nada muda e o pedido é cancelado na volta; **dado** como alvo a máquina excluída ou uma fora da rede local, **então** a promoção é recusada.
- **CA-CIDADELA-06** · *completa* — **Dado** três instâncias na mesma máquina (motor e Guardião de cada uma como processos comuns), relógio falso, trava e Telegram falsos, **quando** o motor e o Guardião da Cidadela param e o Dono aprova a promoção pelo Telegram falso, **então** a Torre escolhida vira Cidadela da época 2, com o estado do banco, o espelho e o Grão-Mestre (agente falso) retomados, e a terceira instância segue a nova Cidadela.
- **CA-CIDADELA-07** · *completa* — **Dado** o cenário do CA-CIDADELA-06, **quando** a antiga Cidadela volta, **então** entra como Torre, encerra os personagens que a nova já retomou, entrega um commit só dela em `refs/mestre/recuperados/epoca-1/<mesa>`, a auditoria lista as transações descartadas e o GitHub falso não recebe nenhuma chamada dela.
- **CA-CIDADELA-08** · *completa* — **Dado** uma Torre e a Cidadela no ar, **quando** o navegador abre `/mesas/3?aba=missoes` na Torre, **então** recebe `307` para a Cidadela com o mesmo caminho e query; **dado** a troca planejada, **então** a máquina escolhida assume sem transação perdida.
- **CA-CIDADELA-09** · *pesada* — **Dado** a Guilda com a Cidadela em Linux e a Torre em Windows, e depois o inverso, **quando** derrubo a Cidadela de vez, **então** a Torre tenta a autocura, me pede pelo Telegram para assumir, eu aprovo, ela vira a Cidadela com o Grão-Mestre e o estado do banco, e a antiga, ao voltar, entra como Torre sem disputar o comando (MVP-15).
- **CA-CIDADELA-10** · *completa* — **Dado** a Cidadela fora e uma Torre cujo personagem roda `mestre done` e cujo motor gera eventos, **quando** a Cidadela volta e a Torre reenvia a fila duas vezes, **então** cada item é aplicado uma vez; **dado** `mestre list` com a Cidadela fora, **então** sai com `citadel_unavailable`, e durante a promoção com `citadel_in_failover`.
- **CA-CIDADELA-11** · *rápida* — **Dado** uma promoção com personagens vivos numa Torre e outros na máquina da antiga, cada um com uma etapa de missão, **quando** a nova Cidadela assume, **então** só os da antiga ficam `interrupted` e só as etapas deles são reentregues.
- **CA-CIDADELA-12** · *rápida* — **Dado** um comentário criado pela antiga com marcador e não replicado, e a fila de saída com o mesmo marcador, **quando** a nova Cidadela reconcilia e esvazia a fila, **então** o GitHub falso tem um comentário só, e o evento dele não vira "evento externo".
- **CA-CIDADELA-13** · *completa* — **Dado** uma instância parada no meio do trabalho, com um personagem, uma espera e a fila de saída com itens, **quando** o processo reinicia, **então** os três voltam ao estado anterior (a máquina reiniciada de verdade é o MVP-12, na pesada).
- **CA-CIDADELA-14** · *rápida* — **Dado** um `agent.event` de uma Torre sobre um personagem que o registro da Cidadela põe nela, e outro sobre um personagem de outra máquina (ou uma Cidadela sem saber onde os personagens rodam), **quando** chegam, **então** o primeiro é entregue e o segundo é recusado com `forbidden`, sem mudar o estado de ninguém; o mesmo vale para o `AgentID` do chamador de um `cli.call` (a completa confere: a chamada em nome de um personagem de outra máquina recebe `not_identified` e o comando não roda).

## Depende de

- [Guilda](guilda.md) · [Replicação](replicacao.md) · [Ficha da máquina](ficha-da-maquina.md)
- [Protocolo](../conectividade/protocolo.md) · [Descoberta](../conectividade/descoberta.md)
