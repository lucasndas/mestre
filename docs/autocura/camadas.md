# Camadas de recuperação

> **Assunto:** as cinco camadas da autocura, valores padrão, runbook, eleição da Torre condutora, proposta de promoção quando a Cidadela não volta e anti-loop · **Componente:** Guardião · **Atualizado:** 2026-10-13

## Objetivo

Recuperar o motor de qualquer máquina, sem SSH, na ordem do mais simples ao mais caro, sem loops e sem nunca deixar os usuários sem aviso. O porquê está no [ADR 0003](../decisoes/0003-autocura-com-guardiao-sem-ssh.md); as ações que o Guardião aceita estão em [Guardião](guardiao.md).

## Comportamento

### As cinco camadas

| Camada | Quem age | Quando | O que faz |
|---|---|---|---|
| 1. Sistema operacional | systemd / recuperação de serviço do Windows | o processo morreu | reinicia o processo |
| 2. Guardião local | Guardião da própria máquina | motor vivo mas travado, laço de queda, disco cheio | reinicia, limpa, volta versão |
| 3. Supervisão cruzada | o lado saudável (Cidadela → Torre; Torre condutora → Cidadela); não existe no modo máquina única | máquina ainda fora após o tempo de graça | segue o runbook pedindo ações ao Guardião da máquina com problema |
| 4. Diagnóstico com IA | Guardião da máquina com problema, a pedido do lado saudável (no modo máquina única, por conta própria) | runbook esgotado | agente local propõe script; Dono ou Administrador aprova ([diagnóstico com IA](diagnostico-com-ia.md)) |
| 5. Usuários | motor da Cidadela, Torre condutora ou, no modo máquina única, o Guardião da Cidadela | nada resolveu, ou máquina inteira fora | alerta com o diagnóstico até ali; Cidadela caída: também a [proposta de promoção](#cidadela-que-não-volta-proposta-de-promoção) |

A autocura é mútua entre quaisquer duas máquinas da Guilda: cada Guardião cuida do motor da própria máquina, e o lado saudável pede as ações ao Guardião do outro, seja qual for o papel de cada uma.

### Valores padrão

Todos configuráveis na Cidadela (replicados aos Guardiões). Os testes usam relógio injetável ([dublês](../qualidade/dubles.md)).

| Parâmetro | Padrão |
|---|---|
| Heartbeat motor → Guardião local (canal local, [componentes](../arquitetura/componentes.md#canais-locais-padrão)) | a cada 5 s; **travado** após 3 perdidos (15 s) com o processo vivo |
| Heartbeat na WSS (motor da máquina e Guardião da máquina → motor da Cidadela) e quando o lado vira **sem sinal** | definidos em [protocolo](../conectividade/protocolo.md#heartbeat-e-reconexão) |
| Tempo de graça (camadas 1 e 2 agirem) antes da camada 3 | 60 s depois de declarado sem sinal |
| systemd do motor | `Type=notify`, `Restart=always`, `RestartSec=2s`, `WatchdogSec=30s` (o motor manda `WATCHDOG=1` a cada 10 s), `StartLimitIntervalSec=0` |
| systemd do Guardião | `Restart=always`, `RestartSec=2s`, `StartLimitIntervalSec=0` |
| Recuperação de serviço do Windows (motor e Guardião) | reiniciar após 5 s, 10 s e 30 s; zerar contagem após 1 dia; vale também para saída sem crash |
| Laço de queda | 3 reinícios em 10 min |
| "Depois de uma atualização" | o problema começou até 24 h após trocar a versão do motor |
| Disco | aviso abaixo de 10% livre; grave abaixo de 5% |
| Backoff entre passos do runbook | 15 s, dobrando a cada tentativa, máximo 10 min, variação aleatória de ±20% |
| Limite de tentativas da camada 3 | 5 por incidente |
| Confirmação de sucesso | heartbeat de volta em até 60 s após a ação |
| Janela de eleição | 10 s |
| Trava de condução | dura 60 s; a condutora renova a cada 20 s |
| Sondagem entre Guardiões (sem árbitro) | a cada 5 s; 3 falhas seguidas = máquina fora |
| Incidente encerrado | 5 min saudável |

Um **incidente** abre quando uma máquina é declarada fora (camada 2 desistiu ou camada 3 começou) e fecha depois de 5 min saudável. Tentativas e limites contam por incidente.

### Camada 2: Guardião local

1. **Travado** (processo vivo, heartbeat perdido): encerra o processo à força e deixa a camada 1 subir de novo.
2. **Disco grave:** roda `cleanup` antes de reiniciar.
3. **Laço de queda:** se o problema começou depois de uma atualização, faz `rollback` comum, sem restaurar o banco ([autoatualização](../plataforma/autoatualizacao.md)); a restauração do banco nunca é automática ([Guardião](guardiao.md#rollback-com-restauração-do-banco)); se não, para de reiniciar por conta própria, abre o incidente e avisa a Cidadela (na Cidadela, aguarda a Torre condutora; no [modo máquina única](#modo-máquina-única), segue ele mesmo).

### Camada 3: supervisão cruzada

- **Cidadela → Torre:** o motor da Cidadela vê o motor de uma Torre sem sinal além do tempo de graça e pede as ações ao Guardião da Torre pela conexão própria desse Guardião. Não há eleição: a Cidadela conduz.
- **Torres → Cidadela:** cada Guardião de Torre percebe a queda pela própria WSS. Passado o tempo de graça, entra na eleição. Só o **Guardião da Torre condutora** segue o runbook, falando com o Guardião da Cidadela na porta dele ([componentes](../arquitetura/componentes.md#portas)), com o certificado da Torre. Quem conduz é o Guardião, e não o motor da Torre, para a cura funcionar mesmo com o motor da Torre fora.

**Runbook** (o mesmo nos dois sentidos; cada passo espera a confirmação de sucesso e respeita o backoff):

1. `status` e `logs`: guardar no incidente.
2. Disco abaixo de 5% → `cleanup`.
3. `restart`.
4. Problema começou depois de uma atualização → `rollback` comum (nunca o com restauração do banco).
5. `checklist`: anexar o resultado ao incidente.
6. Repetir 3 e 4 até o limite de tentativas. Esgotou → camada 4; sem ficha de diagnóstico ou diagnóstico sem solução → camada 5.

### Eleição da Torre condutora

- **Ordem das candidatas** (determinística, a mesma que escolhe quem propõe a promoção): primeiro as Torres online com motor saudável; depois a maior **prioridade** configurada pelo Dono no painel (padrão 100); empate → melhor [ficha da máquina](../guilda/ficha-da-maquina.md); depois → menor `machine_id`. Cada Guardião guarda essa lista (cache recebido da Cidadela).
- **Trava com árbitro (caso normal, motor da Cidadela travado):** o Guardião da Cidadela é o árbitro. Cada candidata pede a trava; o árbitro espera a janela de eleição desde o primeiro pedido e concede a trava à melhor candidata. As outras recebem "condutora = X". A condutora renova; se parar de renovar, a trava expira e a próxima candidata que pedir a recebe.
- **Sem árbitro (Guardião da Cidadela não responde):** não há cura possível na Cidadela, mas é preciso uma única Torre para alertar, cuidar do Telegram e propor a promoção. Cada Guardião sonda os Guardiões de prioridade maior (HTTPS na porta da página reserva, rota de vida autenticada com certificado de máquina) e só assume se todos falharem 3 vezes seguidas. Quando o árbitro volta, a condutora passa a pedir a trava a ele.
- A condutora também assume o [Telegram em modo reduzido](telegram-modo-reduzido.md).

```mermaid
sequenceDiagram
  participant A as Guardião da máquina A (prioridade 200)
  participant B as Guardião da máquina B (prioridade 100)
  participant GC as Guardião da Cidadela
  participant MC as Motor da Cidadela
  Note over MC: motor travado, sem heartbeat
  Note over A,B: sem sinal + 60 s de graça (camadas 1 e 2 na Cidadela)
  A->>GC: pedir trava (incidente, prioridade 200)
  B->>GC: pedir trava (incidente, prioridade 100)
  Note over GC: janela de eleição de 10 s
  GC-->>A: trava concedida (60 s)
  GC-->>B: condutora = Torre A
  A->>GC: status, logs
  A->>GC: restart
  GC->>MC: reinicia o serviço
  alt motor volta em 60 s
    MC-->>A: WSS restabelecida
    A->>GC: liberar trava, fechar passo
  else não volta
    A->>GC: rollback / repetir com backoff
    A->>GC: diagnose (camada 4)
    A->>A: alerta e proposta de promoção pelo Telegram em modo reduzido (camada 5)
  end
```

### Cidadela que não volta: proposta de promoção

Quando a Torre condutora esgota as camadas sobre a Cidadela sem resolver, ou a máquina da Cidadela fica inalcançável (motor e Guardião), a condutora **propõe** uma Torre candidata como nova Cidadela, junto com o alerta da camada 5. O pedido `promocao_cidadela` vai só ao Dono, pelo [Telegram em modo reduzido](telegram-modo-reduzido.md) e pela [página reserva](pagina-reserva.md), com botão e PIN ([fluxo](../aprovacoes/fluxo.md#pedidos-da-guilda)); nunca há promoção automática. Prazos, quem é proposta e a promoção em si: [proposta de assumir](../guilda/cidadela-e-torres.md#proposta-de-assumir). A autocura continua enquanto o pedido espera: se a Cidadela volta, ele é cancelado.

### Modo máquina única

Vale na Guilda de uma máquina só ("só Linux" e "só Windows"): a Cidadela sem nenhuma Torre ([ADR 0020](../decisoes/0020-quatro-usos-core-em-linux-ou-windows.md)). A entrada de uma máquina na Guilda encerra o modo; remover a última Torre o traz de volta. Não há promoção possível.

- Continuam as camadas 1, 2 e 5. **Não há camada 3**: nenhuma outra máquina vigia a Cidadela.
- Quando a camada 2 desiste, o **Guardião da Cidadela** conduz o incidente no lugar da Torre condutora: segue o [runbook](#camada-3-supervisão-cruzada) sobre o próprio motor, inicia o [diagnóstico com IA](diagnostico-com-ia.md) (camada 4), serve a [página reserva](pagina-reserva.md) e assume o [Telegram em modo reduzido](telegram-modo-reduzido.md), por onde sai o alerta da camada 5.
- **Limite:** se a máquina inteira cair (desligada, sem rede, sistema travado), **ninguém de fora percebe**. O painel diz isso em "Guilda e saúde das máquinas" e o [checklist](../plataforma/checklist.md) mantém o aviso ALL-17 enquanto o modo durar.

### Camada 5: alerta

Vai pelo Telegram (motor da Cidadela ou modo reduzido) e pelo painel ou [página reserva](pagina-reserva.md), marcado "Sistema" ([roteamento](../telegram/roteamento.md)). Contém: máquina, desde quando, camadas tentadas, resultado de cada ação, últimos erros (mascarados) e uma sugestão (ex.: "verifique se a máquina está ligada").

### Anti-loop e auditoria

- **Uma condutora por vez** (trava acima) e **uma ação por vez por máquina** (o Guardião serializa).
- Backoff exponencial e limite de tentativas; estourou → alerta (camada 5).
- Ações destrutivas nunca são automáticas ([Guardião](guardiao.md#lista-fechada-de-ações)).
- Toda ação de cura gera registro: incidente, máquina, quem pediu, ação, parâmetros, resultado, duração. Vai para o log de autocura da Cidadela ([persistência](../arquitetura/persistencia.md)); com a Cidadela fora, fica no registro local do Guardião e sobe quando ele volta.

### Retomada dos agentes

Quando um motor reinicia, cada personagem que estava rodando é **retomado** ou fica **interrompido** de forma visível (canvas, `mestre list`, alerta ao Mestre da mesa). Nenhum some em silêncio. A mecânica (ordem e estado `interrupted`) é de [personagens](../orquestracao/personagens.md#retomada-após-reinício).

## Regras e erros

- Nenhuma camada altera, desliga ou atualiza o Guardião ([Guardião](guardiao.md#proteções)).
- Pedido de trava de uma máquina sem certificado válido → recusado e auditado.
- Dois condutores ao mesmo tempo é erro grave: o árbitro só concede uma trava por incidente; no modo sem árbitro, o `409 Conflict` do Telegram é a última proteção ([Telegram em modo reduzido](telegram-modo-reduzido.md)).
- A máquina inteira desligada não tem cura: vai direto para a camada 5 depois do tempo de graça.

## Critérios de aceite

- **CA-CURA-01** · *pesada* — **Dado** cada um dos quatro usos instalado, **quando** mato o processo de cada serviço do motor (em cada máquina), **então** ele volta pela camada 1 e o heartbeat se restabelece em até 30 s, com registro (MVP-2, MVP-13).
- **CA-CURA-02** · *pesada* — **Dado** o motor da Cidadela travado (processo parado), **quando** passa o tempo de graça, **então** uma única Torre é eleita, o Guardião da Cidadela reinicia o motor e a auditoria mostra a sequência do diagrama (MVP-2).
- **CA-CURA-03** · *pesada* — **Dado** a Cidadela desligada, **quando** as camadas se esgotam, **então** os usuários recebem o alerta da camada 5 no Telegram (dublê) em modo reduzido e na página reserva (MVP-2).
- **CA-CURA-04** · *pesada* — **Dado** uma atualização cujo motor cai ao iniciar, **quando** ocorre o laço de queda, **então** o Guardião local volta à versão anterior e o motor fica saudável (MVP-2).
- **CA-CURA-05** · *pesada* — **Dado** o disco com menos de 5% livre, **quando** o motor cai, **então** o Guardião roda `cleanup` antes de reiniciar (MVP-2).
- **CA-CURA-06** · *rápida* — **Dado** candidatas A (200), B (100) e C (200, mesma ficha, `machine_id` menor que A), **quando** a janela de eleição fecha, **então** a trava vai para C; **dado** A e C empatadas na prioridade e A com ficha melhor, **então** vai para A.
- **CA-CURA-07** · *rápida* — **Dado** uma condutora que para de renovar (relógio falso), **quando** passam 60 s, **então** a trava expira e a próxima candidata a recebe.
- **CA-CURA-08** · *rápida* — **Dado** um runbook que nunca dá certo, **quando** roda, **então** os intervalos seguem 15 s, 30 s, 60 s… (±20%) e após 5 tentativas o incidente passa à camada 4.
- **CA-CURA-09** · *rápida* — **Dado** o árbitro fora e a máquina A respondendo à sondagem, **quando** B avalia, **então** B não assume; com A falhando 3 vezes, B assume.
- **CA-CURA-10** · *rápida* — **Dado** um laço de queda sem atualização nas últimas 24 h, **quando** a camada 2 avalia, **então** não faz `rollback` e abre o incidente.
- **CA-CURA-11** · *completa* — **Dado** personagens rodando numa máquina, **quando** o motor da máquina reinicia, **então** cada um aparece retomado ou interrompido, e nenhum some (MVP-12).
- **CA-CURA-12** · *pesada* — **Dado** um uso de máquina única (só Linux e só Windows) com o motor da Cidadela em laço de queda sem atualização, **quando** a camada 2 desiste, **então** o Guardião da Cidadela abre o incidente, roda o runbook e o diagnóstico, serve a página reserva e manda o alerta da camada 5 pelo Telegram (dublê) em modo reduzido (MVP-2, MVP-13).
- **CA-CURA-13** · *rápida* — **Dado** a Cidadela caída e as camadas 3 e 4 esgotadas, **quando** a camada 5 dispara, **então** o alerta e o pedido `promocao_cidadela` saem juntos pelo Telegram em modo reduzido, só ao Dono; **quando** a Cidadela volta antes da resposta, **então** o pedido é cancelado e o incidente fecha como numa cura comum.

## Depende de

- [Guardião](guardiao.md)
- [Componentes](../arquitetura/componentes.md)
- [Persistência](../arquitetura/persistencia.md)
- [Protocolo](../conectividade/protocolo.md)
- [Guilda](../guilda/guilda.md)
- [Personagens](../orquestracao/personagens.md)
- [Bot do Telegram](../telegram/bot.md)

## Fontes

- systemd `WatchdogSec`, `sd_notify`: https://pkg.go.dev/github.com/coreos/go-systemd/v22/daemon
- Serviço do Windows em Go: https://pkg.go.dev/github.com/kardianos/service
