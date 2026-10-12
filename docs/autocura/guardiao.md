# Guardião

> **Assunto:** o serviço separado que vigia e conserta o motor da própria máquina: lista fechada de ações, como é alcançado e o que guarda · **Componente:** Guardião · **Atualizado:** 2026-10-12

## Objetivo

Ter, em cada máquina (Core e nós), um serviço pequeno e estável que continua de pé quando o motor quebra, conserta o motor localmente e obedece só a uma lista fechada de ações. Ninguém precisa de acesso remoto à máquina. O porquê está no [ADR 0003](../decisoes/0003-autocura-com-guardiao-sem-ssh.md); binário, serviço, conta e porta estão em [componentes](../arquitetura/componentes.md).

## Comportamento

### O que é

- Processo, código e versão independentes do motor.
- **Atualizar o Guardião não é ação de cura:** é uma ação de atualização aprovada, feita pelo motor saudável da própria máquina ([atualização automática](../plataforma/autoatualizacao.md)). As ações de cura nunca alteram o Guardião (seção "Proteções").
- Não roda agentes de trabalho e não guarda estado do produto, com as exceções da seção "O que guarda".
- Vigia o motor; o sistema operacional vigia o Guardião ([camadas](camadas.md)).
- Tarefas próprias além da vigilância:
  - conduzir a cura do Core quando o nó dele é o condutor ([camadas](camadas.md#eleição-do-nó-condutor));
  - servir a [página reserva](pagina-reserva.md) (nos nós e, no [modo máquina única](camadas.md#modo-máquina-única), no Core);
  - conduzir o [Telegram em modo reduzido](telegram-modo-reduzido.md) (no nó condutor ou, no modo máquina única, no Core);
  - no Core Windows, manter o anúncio mDNS ([descoberta](../conectividade/descoberta.md#mdns));
  - iniciar o [diagnóstico com IA](diagnostico-com-ia.md).

### O que vigia

| Sinal | Como | Limites |
|---|---|---|
| Processo vivo | estado do serviço (systemd / gerenciador de serviços do Windows) | — |
| Heartbeat interno | o motor manda "vivo" pelo canal local motor ↔ Guardião | [valores padrão](camadas.md#valores-padrão) |
| Saúde respondendo | pedido `health` pelo canal local (abaixo) a cada 15 s, tempo limite 3 s (padrão); no Core, também `GET /healthz` na porta do painel ([componentes](../arquitetura/componentes.md#portas)) | 3 falhas seguidas = travado |
| Disco | espaço livre na partição de dados do Mestre | [valores padrão](camadas.md#valores-padrão) |
| Memória | memória livre do sistema e memória do motor | aviso com menos de 5% livre por 2 min (padrão) |

### Lista fechada de ações

Os nomes das ações ficam em inglês, como no protocolo ([protocolo](../conectividade/protocolo.md)).

| Ação | O que faz | Pede pessoa? |
|---|---|---|
| `status` | estado do motor, versão do motor e do Guardião, disco, memória, tempo no ar, incidente aberto | não |
| `logs` | últimas N linhas dos logs do motor e do Guardião (padrão 500, máximo 5.000), já mascaradas ([segurança](../arquitetura/seguranca.md)) | não |
| `restart` | reinicia o serviço do motor | não |
| `rollback` | volta o motor para a versão anterior guardada, sem tocar no banco | não |
| `rollback` com restauração do banco | volta a versão e restaura o banco de antes da atualização ([abaixo](#rollback-com-restauração-do-banco)) | **sim**, Dono ou Administrador, com PIN |
| `checklist` | roda o [checklist](../plataforma/checklist.md) e devolve o resultado; correções seguem as regras de lá | não |
| `cleanup` | apaga temporários do Mestre e logs fora dos limites de rotação e retenção de [persistência](../arquitetura/persistencia.md#logs), só dentro das pastas do Mestre; nunca banco, backups, pergaminhos, anexos ou worktrees | não |
| `diagnose` | inicia o agente de diagnóstico; o script que ele propõe pede aprovação | não para iniciar |
| `run_script` | executa uma vez o script de diagnóstico aprovado, conferindo o hash | **sim**, já aprovado ([diagnóstico com IA](diagnostico-com-ia.md)) |

- Não existe shell livre, envio de arquivo, troca de configuração nem qualquer ação fora da tabela.
- Ações que apagam dados do produto ou mexem em rede, firewall ou serviços do sistema nunca são automáticas: pedem aprovação de Dono ou Administrador ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)).

### Quem pode pedir

| Quem | A qual Guardião | Por onde |
|---|---|---|
| O próprio Guardião (camada 2 e, no modo máquina única, o incidente inteiro) | o local | — |
| Motor do Core | Guardião de um nó | conexão WSS própria do Guardião do nó |
| Guardião do nó condutor | Guardião do Core | TLS na porta do Guardião do Core (certificado próprio do Guardião do Core), com certificado de nó |
| Usuário logado (Dono ou Administrador) | o do nó, ou o do Core por ele | painel (pelo Core), [página reserva](pagina-reserva.md) ou [Telegram em modo reduzido](telegram-modo-reduzido.md) |

### Como é alcançado

- **Guardião de nó:** mantém a própria conexão WSS de saída até o motor do Core, separada da do motor do nó, com credencial própria emitida no [pareamento](../conectividade/pareamento.md). Reconecta com backoff. Assim o Core fala com ele mesmo com o motor do nó morto, sem porta aberta no nó.
- **Guardião do Core:** escuta na porta própria ([componentes](../arquitetura/componentes.md#portas)), com **certificado próprio** de servidor da CA interna ([pareamento](../conectividade/pareamento.md)), e só aceita clientes com certificado de nó válido e não revogado (mTLS). Quando o motor do Core cai, os nós continuam chegando pelo mesmo endereço.
- O canal do Guardião com o Core tem **versão de protocolo própria** (v1), separada da do motor, e muda raramente.
- **Canal local no Windows:** o Guardião roda como LocalSystem e cria o pipe `\\.\pipe\mestre-guardiao` com SDDL que dá acesso só a SYSTEM e às contas dos serviços do motor daquela máquina, lidas da configuração dos serviços ao subir: a do `mestre-node` e, no Core, `NT SERVICE\MestreCore`; com `PIPE_REJECT_REMOTE_CLIENTS`. Um teste da suíte completa no Windows confere que os motores escrevem no pipe e que outra conta não abre.

### Canal local motor ↔ Guardião

Socket Unix ou named pipe da própria máquina ([canais locais](../arquitetura/componentes.md#canais-locais-padrão)); mensagens JSON de uma linha `{v, id, type, data}`, versão `v` = 1, igual nos dois lados; o Guardião recusa versão desconhecida e registra. Este é o catálogo completo:

| Tipo | Sentido | Conteúdo |
|---|---|---|
| `heartbeat` | motor → Guardião | "vivo", versão do motor, hora; a cada 5 s ([valores padrão](camadas.md#valores-padrão)) |
| `health` / `health.result` | Guardião → motor → Guardião | banco aberto e migrado, conexão com o Core (no nó), filas; `ok` ou o item que falhou |
| `replica` | motor → Guardião | a réplica restrita do cofre e o estado "travado", com versão crescente ([cofre](../plataforma/cofre.md#réplica-para-os-guardiões)); só o Guardião do Core a recebe por aqui |
| `revoked` | motor → Guardião | lista de certificados revogados (Guardião do Core) |
| `nodes` | motor → Guardião | lista de nós com prioridade e endereços, valores da autocura e ficha de diagnóstico |
| `update.prepare` / `update.done` | motor ↔ Guardião | coordenação das trocas de versão ([atualização automática](../plataforma/autoatualizacao.md)) |
| `ack` | os dois | confirma o `id` recebido |

### O que guarda

| Item | Onde | Observação |
|---|---|---|
| Chave privada e certificado do Guardião | pasta do Guardião, protegida pelo SO | [pareamento](../conectividade/pareamento.md) |
| Token do bot e lista de usuários, criptografados | Guardiões de nó; no modo máquina única, o do Core | réplica restrita do [cofre](../plataforma/cofre.md#réplica-para-os-guardiões) |
| Estado "travado" do sistema | Guardiões de nó; no modo máquina única, o do Core | não é segredo; recebido do Core ([segurança do canal](../telegram/seguranca-do-canal.md)) |
| Lista de nós com prioridade e endereços | todos | cache para a eleição |
| Valores padrão da autocura | todos | recebidos do Core |
| Configuração de diagnóstico (ficha resolvida) | todos | [diagnóstico com IA](diagnostico-com-ia.md#ficha-de-diagnóstico) |
| Registro local de auditoria | todos | linhas JSON só de acréscimo; enviado ao Core quando há conexão e apagado depois de confirmado |

### Rollback com restauração do banco

Ação **rara e nunca automática**: só existe quando uma atualização migrou o banco e a versão anterior não funciona com ele. Exige um Dono ou Administrador, com PIN ([PIN pessoal](../telegram/usuarios-e-niveis.md#pin-pessoal)), pelo painel, pela [página reserva](pagina-reserva.md) ou pelo [Telegram em modo reduzido](telegram-modo-reduzido.md). O Guardião volta o binário anterior e restaura o banco guardado logo antes da atualização ([autoatualização](../plataforma/autoatualizacao.md)); **tudo o que aconteceu entre a atualização e a volta se perde** (mensagens, decisões, estados de missão gravados nesse intervalo), e o pedido mostra esse intervalo antes da confirmação. Sem aprovação, nada é feito; o `rollback` comum, sem restauração, continua disponível.

### Proteções

- **Nenhuma ação de cura desliga, atualiza ou altera o Guardião, nem corta a conexão dele.** O Guardião recusa (`affects_guardian`) qualquer ação ou script que cite o serviço, o binário, a pasta, a credencial ou as regras de firewall dele.
- Uma ação por vez por máquina: um segundo pedido recebe `busy` com a ação em curso.
- Toda ação, pedida ou recusada, entra na auditoria ([camadas](camadas.md#anti-loop-e-auditoria)).

## Regras e erros

- Ação desconhecida → `unknown_action`, auditada.
- Quem pede sem credencial válida → conexão recusada e alerta ao Dono.
- Sem conexão com o Core → continua a camada 2 sozinho e acumula a auditoria local.
- Guardião fora → o motor segue funcionando; o SO o reinicia e o checklist acusa.

## Critérios de aceite

- **CA-GUARD-01** · *rápida* — **Dado** um pedido com ação fora da tabela, **quando** chega ao Guardião, **então** é recusado com `unknown_action` e registrado na auditoria.
- **CA-GUARD-02** · *rápida* — **Dado** um sistema de arquivos falso com logs antigos, banco, backups e pergaminhos, **quando** roda `cleanup`, **então** só os temporários e os logs fora da retenção de persistência somem.
- **CA-GUARD-03** · *rápida* — **Dado** um log com um token falso, **quando** `logs` responde, **então** o token aparece mascarado.
- **CA-GUARD-04** · *rápida* — **Dado** um script que cita `mestre-guardiao`, **quando** é submetido, **então** é recusado com `affects_guardian`.
- **CA-GUARD-05** · *rápida* — **Dado** um `restart` em curso, **quando** chega outro pedido, **então** ele recebe `busy`.
- **CA-GUARD-06** · *completa* — **Dado** o motor do nó parado, **quando** o Core pede `status` e `logs`, **então** o Guardião do nó responde pela conexão própria.
- **CA-GUARD-07** · *completa* — **Dado** o motor do Core parado, **quando** um nó com certificado válido chama o Guardião do Core, **então** recebe `status`; sem certificado de nó, a conexão TLS é recusada.
- **CA-GUARD-08** · *rápida* — **Dado** um `rollback` com restauração do banco sem aprovação, **quando** é pedido, **então** fica esperando aprovação de Dono ou Administrador com PIN, o pedido mostra o intervalo que será perdido e nada é feito; nenhuma camada da autocura o pede sozinha.
- **CA-GUARD-09** · *rápida* — **Dado** o motor do nó vivo mas sem responder `health` por 3 ciclos no relógio falso, **quando** o Guardião avalia, **então** marca o motor como travado; **dado** uma mensagem do canal local com `v` = 2, **então** é recusada e registrada.

## Depende de

- [Componentes](../arquitetura/componentes.md)
- [Segurança](../arquitetura/seguranca.md)
- [Cofre](../plataforma/cofre.md)
- [Protocolo](../conectividade/protocolo.md)
- [Pareamento](../conectividade/pareamento.md)
