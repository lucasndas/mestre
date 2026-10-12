# Página reserva

> **Assunto:** página mínima servida pelo Guardião de cada nó (e do Core, no modo máquina única), na rede local, com TLS e login, para quando o Core está fora · **Componente:** Guardião · **Atualizado:** 2026-10-12

## Objetivo

Dar aos usuários um lugar para ver o estado das máquinas, aprovar curas e mandar recuperar o Core quando o motor do Core está fora do ar. Ela é servida pelo Guardião do nó, então funciona mesmo com o motor do nó parado. No [modo máquina única](camadas.md#modo-máquina-única) (sem nó remoto), quem a serve é o Guardião do Core.

## Comportamento

### Acesso

- Endereço: `https://<nome-da-máquina>.local:<porta>` ou o IP dela na rede local. Porta padrão em [componentes](../arquitetura/componentes.md#portas).
- Só na rede local: o firewall da máquina libera a porta só para a rede local (no Windows, só no perfil de rede **Privada**) ([checklist](../plataforma/checklist.md)). Nunca exposta na internet.
- **TLS:** no nó, certificado emitido pela CA interna do Core para o Guardião do nó no [pareamento](../conectividade/pareamento.md), com o nome do nó, o nome `.local` e os IPs locais; no Core, o certificado do servidor do Core ([segurança](../arquitetura/seguranca.md#tls-e-ca-interna)). O navegador já confia na CA porque fez isso para o painel ([login](../painel/login.md)). A renovação segue o [pareamento](../conectividade/pareamento.md).
- O painel mostra o endereço da página reserva de cada nó em "Saúde dos nós" ([administração](../painel/administracao.md)), e o alerta de Core fora traz o link.

### Login

- Mesmos usuários e níveis do painel, a partir da cópia local criptografada que o Guardião recebe do Core ([cofre](../plataforma/cofre.md#réplica-para-os-guardiões)).
- Usuário e senha, conferidos contra o hash da cópia. Ações que pedem PIN pedem o PIN, conferido do mesmo jeito.
- Sessão curta: expira após 30 min sem uso (padrão) e sempre que a cópia de usuários muda para aquele usuário (suspenso, removido, nível alterado).
- 5 senhas erradas em 15 min para o mesmo usuário → bloqueio de 15 min (padrão) e registro na auditoria local.
- Proteções da sessão iguais às do painel (cookie seguro, origem conferida) ([login](../painel/login.md#sessões)).

### O que mostra

| Seção | Conteúdo | Origem |
|---|---|---|
| Esta máquina | motor (rodando, travado, parado; no Core Windows, os dois serviços), versão do motor e do Guardião, disco, memória; no modo máquina única, o aviso de que a queda da máquina inteira não é percebida por ninguém | o próprio Guardião |
| Core | motor do Core (com ou sem sinal, desde quando) e Guardião do Core (respondendo ou não) | WSS própria e sondagem da porta do Guardião do Core |
| Outros nós | último estado conhecido, com horário; com o Core fora, o resultado da sondagem entre Guardiões | cache e [eleição](camadas.md#eleição-do-nó-condutor) |
| Incidente | camada atual, passos feitos e resultados, quem é o nó condutor | auditoria do incidente |
| Aprovações de cura pendentes | scripts de diagnóstico e `rollback` com restauração do banco | [diagnóstico com IA](diagnostico-com-ia.md) |

Com o Core no ar, a página mostra um aviso "use o painel", com o link, e continua só para leitura de estado e aprovações de cura.

### Ações

| Ação | Quem | O que acontece |
|---|---|---|
| Ver estado | qualquer nível | — |
| Aprovar ou negar cura | Dono ou Administrador, com PIN | aprovação presa ao hash do script ([diagnóstico com IA](diagnostico-com-ia.md#proposta-e-aprovação)) |
| **Recuperar o Core** | Dono ou Administrador | se este nó é o condutor, ou consegue a trava, começa ou retoma o runbook do zero (zera o backoff e as tentativas do incidente). Se outro nó é o condutor, mostra qual é e o link da página reserva dele. No modo máquina única, o Guardião do Core recomeça o runbook sobre o próprio motor |
| Pedir diagnóstico com IA | Dono ou Administrador | `diagnose` no Guardião do Core, dentro do limite por incidente |

- Níveis: tabela única em [usuários e níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades).
- Com o sistema travado ([segurança do canal](../telegram/seguranca-do-canal.md)), a página ainda mostra estado e permite aprovar cura: o travamento congela agentes e decretos, não a autocura.
- Toda ação entra na auditoria local do Guardião e sobe ao Core quando ele volta.

### Interface

- HTML e CSS mínimos, servidos pelo binário do Guardião, sem dependência externa e sem o build do painel. Funciona no celular.
- Atualiza o estado a cada 5 s (padrão) enquanto aberta.

## Regras e erros

- Porta ocupada → o Guardião registra o erro, segue vigiando o motor e o checklist acusa.
- Cópia de usuários ausente (nó recém-pareado sem réplica) → a página mostra "sem usuários sincronizados" e não aceita login.
- Certificado vencido → o Guardião continua servindo e mostra o problema no `status`; o checklist acusa antes ([checklist](../plataforma/checklist.md)).
- A página não oferece nenhuma ação fora da lista fechada do [Guardião](guardiao.md#lista-fechada-de-ações).

## Critérios de aceite

- **CA-RESERVA-01** · *completa* — **Dado** o motor do Core parado, **quando** abro a página reserva do nó por HTTPS e entro com um usuário do painel, **então** vejo o Core sem sinal, o nó condutor e o incidente (MVP-2).
- **CA-RESERVA-02** · *completa* — **Dado** o motor do nó parado, **quando** abro a página reserva, **então** ela responde e mostra o motor do nó parado.
- **CA-RESERVA-03** · *rápida* — **Dado** um Observador logado, **quando** chama a rota de aprovar cura, **então** recebe "exige Administrador" e nada muda.
- **CA-RESERVA-04** · *rápida* — **Dado** 5 senhas erradas em 15 min (relógio falso), **quando** chega a sexta, **então** o login é recusado sem conferir a senha.
- **CA-RESERVA-05** · *rápida* — **Dado** um usuário suspenso no Core, **quando** a nova cópia chega ao Guardião, **então** a sessão dele na página reserva acaba e o login é recusado.
- **CA-RESERVA-06** · *rápida* — **Dado** outro nó com a trava de condução, **quando** um Administrador pede "Recuperar o Core", **então** nada é executado e a página mostra o condutor.
- **CA-RESERVA-07** · *completa* — **Dado** o modo máquina única com o motor do Core parado, **quando** abro a página reserva do Core e peço "Recuperar o Core", **então** ela mostra o motor parado e o aviso de máquina única, e o Guardião do Core recomeça o runbook.

## Depende de

- [Guardião](guardiao.md)
- [Camadas](camadas.md)
- [Cofre](../plataforma/cofre.md)
- [Pareamento](../conectividade/pareamento.md)
- [Login do painel](../painel/login.md)
- [Usuários e níveis](../telegram/usuarios-e-niveis.md)
