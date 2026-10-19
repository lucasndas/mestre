# Página reserva

> **Assunto:** página mínima servida pelo Guardião de qualquer Torre (e da Cidadela, no modo máquina única), na rede local, com TLS e login, para quando a Cidadela está fora · **Componente:** Guardião · **Atualizado:** 2026-10-19

## Objetivo

Dar aos usuários um lugar para ver o estado das máquinas, aprovar curas, aprovar a promoção de uma Torre e mandar recuperar a Cidadela quando o motor da Cidadela está fora do ar. Ela é servida pelo Guardião de cada Torre, então funciona mesmo com o motor da Torre parado. No [modo máquina única](camadas.md#modo-máquina-única) (sem Torre), quem a serve é o Guardião da Cidadela.

## Comportamento

### Acesso

- Endereço: `https://<nome-da-máquina>.local:<porta>` ou o IP dela na rede local. Porta padrão em [componentes](../arquitetura/componentes.md#portas).
- Só na rede local: o firewall da máquina libera a porta só para a rede local (no Windows, só no perfil de rede **Privada**) ([checklist](../plataforma/checklist.md)). Nunca exposta na internet.
- **TLS:** na Torre, certificado emitido pela CA interna da Guilda para o Guardião da Torre na [entrada na Guilda](../guilda/guilda.md), com o nome da máquina, o nome `.local` e os IPs locais; na Cidadela, o certificado próprio do Guardião da Cidadela ([segurança](../arquitetura/seguranca.md#tls-e-ca-interna)). Os dois são reemitidos quando os IPs da máquina mudam. O navegador já confia na CA porque fez isso para o painel ([login](../painel/login.md)). A renovação segue a [Guilda](../guilda/guilda.md).
- O painel mostra o endereço da página reserva de cada máquina em "Guilda e saúde das máquinas" ([administração](../painel/administracao.md)), e o alerta de Cidadela fora traz o link.

### Login

- Mesmos usuários e níveis do painel, a partir da cópia local criptografada que o Guardião recebe da Cidadela ([cofre](../plataforma/cofre.md#réplica-para-os-guardiões)).
- Usuário e senha, conferidos contra o hash da cópia. Ações que pedem PIN pedem o PIN, conferido do mesmo jeito e com o **mesmo bloqueio** de [usuários e níveis](../telegram/usuarios-e-niveis.md#pin-pessoal), contado localmente pelo Guardião, que o compartilha com o modo reduzido e o sobe à Cidadela com a auditoria.
- Sessão curta: expira após 30 min sem uso (padrão) e sempre que a cópia de usuários muda para aquele usuário (suspenso, removido, nível alterado).
- Bloqueio por senhas erradas: o do [painel](../painel/login.md), contado localmente e registrado na auditoria local.
- Proteções da sessão iguais às do painel (cookie seguro, origem conferida) ([login](../painel/login.md#sessões)).

### O que mostra

| Seção | Conteúdo | Origem |
|---|---|---|
| Esta máquina | papel (Cidadela ou Torre) e época, motor (rodando, travado, parado), versão do motor e do Guardião, disco, memória; no modo máquina única, o aviso de que a queda da máquina inteira não é percebida por ninguém | o próprio Guardião |
| Cidadela | motor da Cidadela (com ou sem sinal, desde quando) e Guardião da Cidadela (respondendo ou não) | WSS própria e sondagem da porta do Guardião da Cidadela |
| Outras Torres | último estado conhecido, com horário; com a Cidadela fora, o resultado da sondagem entre Guardiões | cache e [eleição](camadas.md#eleição-da-torre-condutora) |
| Incidente | camada atual, passos feitos e resultados, quem é a Torre condutora | auditoria do incidente |
| Pedido de promoção | Torre proposta e desde quando a Cidadela está fora, com **Promover** e **Esperar** só para o Dono, **Promover** com PIN ([fluxo](../aprovacoes/fluxo.md#pedidos-da-guilda)) | Guardião da Torre condutora |
| Aprovações de cura pendentes | scripts de diagnóstico e `rollback` com restauração do banco, este com o intervalo que será perdido | [diagnóstico com IA](diagnostico-com-ia.md), [Guardião](guardiao.md#rollback-com-restauração-do-banco) |

Com a Cidadela no ar, a página mostra um aviso "use o painel", com o link, e continua só para leitura de estado e aprovações de cura.

### Ações

| Ação | Quem | O que acontece |
|---|---|---|
| Ver estado | qualquer nível | — |
| Aprovar ou negar cura | Dono ou Administrador, com PIN | aprovação presa ao hash do script ([diagnóstico com IA](diagnostico-com-ia.md#proposta-e-aprovação)) ou, no `rollback` com restauração do banco, à versão e ao backup indicados |
| **Recuperar a Cidadela** | Dono ou Administrador | se esta Torre é a condutora, ou consegue a trava, começa ou retoma o runbook do zero (zera o backoff e as tentativas do incidente). Se outra Torre é a condutora, mostra qual é e o link da página reserva dela. No modo máquina única, o Guardião da Cidadela recomeça o runbook sobre o próprio motor |
| Promover ou esperar | só o Dono; **Promover** com PIN | **Promover** manda `citadel.promote` ([Guardião](guardiao.md#lista-fechada-de-ações)) e a promoção segue [Cidadela e Torres](../guilda/cidadela-e-torres.md#promoção). **Esperar** é o de [fluxo](../aprovacoes/fluxo.md#pedidos-da-guilda). Em outra Torre, a página leva à da condutora |
| Pedir diagnóstico com IA | Dono ou Administrador | `diagnose` no Guardião da Cidadela, dentro do limite por incidente |

- A promoção vive no Guardião: sai só pelo Telegram reduzido e por esta página, nunca pelo painel; a troca planejada (Cidadela no ar) é pelo painel ou pelo `/guilda` ([fluxo](../aprovacoes/fluxo.md#pedidos-da-guilda)).
- Níveis: tabela única em [usuários e níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades).
- Com o sistema travado ([segurança do canal](../telegram/seguranca-do-canal.md)), a página ainda mostra estado e permite aprovar cura: o travamento congela agentes e decretos, não a autocura.
- Toda ação entra na auditoria local do Guardião e sobe à Cidadela quando ele volta.

### Interface

- HTML e CSS mínimos, servidos pelo binário do Guardião, sem dependência externa e sem o build do painel. Funciona no celular.
- Atualiza o estado a cada 5 s (padrão) enquanto aberta.

## Regras e erros

- Porta ocupada → o Guardião registra o erro, segue vigiando o motor e o checklist acusa.
- Cópia de usuários ausente (máquina recém-entrada na Guilda, sem réplica) → a página mostra "sem usuários sincronizados" e não aceita login.
- Certificado vencido → o Guardião continua servindo e mostra o problema no `status`; o checklist acusa antes ([checklist](../plataforma/checklist.md)).
- A página não oferece nenhuma ação fora da lista fechada do [Guardião](guardiao.md#lista-fechada-de-ações).

## Critérios de aceite

- **CA-RESERVA-01** · *completa* — **Dado** o motor da Cidadela parado, **quando** abro a página reserva da máquina por HTTPS e entro com um usuário do painel, **então** vejo a Cidadela sem sinal, a Torre condutora e o incidente (MVP-2).
- **CA-RESERVA-02** · *completa* — **Dado** o motor da máquina parado, **quando** abro a página reserva, **então** ela responde e mostra o motor da máquina parado.
- **CA-RESERVA-03** · *rápida* — **Dado** um Observador logado, **quando** chama a rota de aprovar cura, **então** recebe "exige Administrador" e nada muda.
- **CA-RESERVA-04** · *rápida* — **Dado** 5 senhas erradas em 15 min (relógio falso), **quando** chega a sexta, **então** o login é recusado sem conferir a senha.
- **CA-RESERVA-05** · *rápida* — **Dado** um usuário suspenso na Cidadela, **quando** a nova cópia chega ao Guardião, **então** a sessão dele na página reserva acaba e o login é recusado.
- **CA-RESERVA-06** · *rápida* — **Dado** outra Torre com a trava de condução, **quando** um Administrador pede "Recuperar a Cidadela", **então** nada é executado e a página mostra a condutora.
- **CA-RESERVA-07** · *completa* — **Dado** o modo máquina única com o motor da Cidadela parado, **quando** abro a página reserva da Cidadela e peço "Recuperar a Cidadela", **então** ela mostra o motor parado e o aviso de máquina única, e o Guardião da Cidadela recomeça o runbook.
- **CA-RESERVA-08** · *rápida* — **Dado** o PIN do Dono errado 5 vezes em 15 min (relógio falso) na página reserva, **quando** chega o sexto, **então** é recusado sem conferir, o PIN fica bloqueado por 30 min também no modo reduzido, e o bloqueio sobe à Cidadela com a auditoria; **dado** um pedido de promoção, **quando** o Dono toca **Esperar**, **então** nada é decidido e o lembrete só volta em 1 h.

## Depende de

- [Guardião](guardiao.md)
- [Camadas](camadas.md)
- [Cofre](../plataforma/cofre.md)
- [Guilda](../guilda/guilda.md)
- [Login do painel](../painel/login.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
