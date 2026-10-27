# Guardião

> **Assunto:** o serviço separado que vigia e conserta o motor da própria máquina: lista fechada de ações, como é alcançado e o que guarda · **Componente:** Guardião · **Atualizado:** 2026-10-19

## Objetivo

Ter, em cada máquina da Guilda (Cidadela e Torres), um serviço pequeno e estável que continua de pé quando o motor quebra, conserta o motor localmente e obedece só a uma lista fechada de ações. Ninguém precisa de acesso remoto à máquina. O porquê está no [ADR 0003](../decisoes/0003-autocura-com-guardiao-sem-ssh.md); binário, serviço, conta e porta estão em [componentes](../arquitetura/componentes.md).

## Comportamento

### O que é

- Processo, código e versão independentes do motor.
- **Atualizar o Guardião não é ação de cura:** é uma ação de atualização aprovada, feita pelo motor saudável da própria máquina ([atualização automática](../plataforma/autoatualizacao.md)). As ações de cura nunca alteram o Guardião (seção "Proteções").
- Não roda agentes de trabalho e não guarda estado do produto, com as exceções da seção "O que guarda".
- Vigia o motor; o sistema operacional vigia o Guardião ([camadas](camadas.md)).
- Tarefas próprias além da vigilância:
  - conduzir a cura da Cidadela quando a Torre dele é a condutora ([camadas](camadas.md#eleição-da-torre-condutora)) e, se ela não volta, propor a promoção ([camadas](camadas.md#cidadela-que-não-volta-proposta-de-promoção));
  - servir a [página reserva](pagina-reserva.md) (em qualquer Torre e, no [modo máquina única](camadas.md#modo-máquina-única), na Cidadela);
  - conduzir o [Telegram em modo reduzido](telegram-modo-reduzido.md) (na Torre condutora ou, no modo máquina única, na Cidadela);
  - anunciar a própria máquina no mDNS; só o Guardião da Cidadela publica também `mestre.local`, e só com a [concessão do motor](#concessão-do-motor) ([descoberta](../conectividade/descoberta.md#mdns));
  - guardar a **época** da Guilda e a certidão mais alta que viu, e recusar pedido com época menor ([Cidadela e Torres](../guilda/cidadela-e-torres.md));
  - iniciar o [diagnóstico com IA](diagnostico-com-ia.md).

### Concessão do motor

- O Guardião da Cidadela só publica `mestre.local` com uma **concessão** do motor local, renovada a cada **30 s** pelo canal local (`lease`); o motor renova enquanto está no comando, sem depender do GitHub (continua com o [GitHub fora](../guilda/trava-no-github.md#github-fora)). Sem concessão válida (motor fora ou travado), o anúncio some em até **60 s**.
- O papel de **árbitro** da [eleição](camadas.md#eleição-da-torre-condutora) não depende da concessão: o Guardião segue arbitrando enquanto vive, mesmo com o motor travado, e só deixa de ser árbitro quando a Cidadela se [suspende](../guilda/cidadela-e-torres.md#cidadela-suspensa) (o motor manda `lease` com `suspended`, ou chega a certidão nova abaixo). Assim a cura da Cidadela travada, o caso normal, acontece.
- **Certidão nova:** a [certidão de época](../guilda/replicacao.md) mais nova chega ao Guardião (a) por qualquer Torre que reconecta a ele, no fechamento `4007` com a certidão assinada pela CA ([protocolo](../conectividade/protocolo.md)), ou (b) pelo próprio motor, ao conferir a trava. Com certidão mais nova que a sua, o Guardião se retira **na hora**: sem `mestre.local`, sem árbitro, e passa a Guardião de Torre.

### O que vigia

| Sinal | Como | Limites |
|---|---|---|
| Processo vivo | estado do serviço (systemd / gerenciador de serviços do Windows) | — |
| Processo do `mestre-launcher` (desde o M7) | estado do serviço; caído, `restart` com alvo `launcher`; os terminais vivos seguem nos `mestre-pty` ([componentes](../arquitetura/componentes.md)) | como o processo vivo do motor |
| Heartbeat interno | o motor manda "vivo" pelo canal local motor ↔ Guardião | [valores padrão](camadas.md#valores-padrão) |
| Saúde respondendo | pedido `health` pelo canal local (abaixo) a cada 15 s, tempo limite 3 s (padrão); na Cidadela, também `GET /healthz` na porta do painel ([componentes](../arquitetura/componentes.md#portas)) | 3 falhas seguidas = travado |
| Disco | espaço livre na partição de dados do Mestre | [valores padrão](camadas.md#valores-padrão) |
| Memória | memória livre do sistema e memória do motor | aviso com menos de 5% livre por 2 min (padrão) |

### Lista fechada de ações

Os nomes das ações ficam em inglês, como no protocolo ([protocolo](../conectividade/protocolo.md)).

| Ação | O que faz | Pede pessoa? |
|---|---|---|
| `status` | estado do motor, versão do motor e do Guardião, disco, memória, tempo no ar, incidente aberto | não |
| `logs` | últimas N linhas dos logs do motor e do Guardião (padrão 500, máximo 5.000), já mascaradas ([segurança](../arquitetura/seguranca.md)) | não |
| `restart` | reinicia o serviço do motor (ou, com alvo `launcher`, o do `mestre-launcher`) | não |
| `rollback` | volta o motor para a versão anterior guardada, sem tocar no banco | não |
| `rollback` com restauração do banco | volta a versão e restaura o banco de antes da atualização ([abaixo](#rollback-com-restauração-do-banco)) | **sim**, Senhor da Guilda ou Administrador, com PIN |
| `checklist` | roda o [checklist](../plataforma/checklist.md) e devolve o resultado; correções seguem as regras de lá | não |
| `cleanup` | apaga temporários do Mestre e logs fora dos limites de rotação e retenção de [persistência](../arquitetura/persistencia.md#logs), só dentro das pastas do Mestre; nunca banco, backups, pergaminhos, anexos ou worktrees | não |
| `diagnose` | inicia o agente de diagnóstico; o script que ele propõe pede aprovação | não para iniciar |
| `run_script` | executa uma vez o script de diagnóstico aprovado, conferindo o hash | **sim**, já aprovado ([diagnóstico com IA](diagnostico-com-ia.md)) |
| `recover` | começa ou retoma o runbook do zero (a ação **Recuperar a Cidadela** da [página reserva](pagina-reserva.md)); só da Torre condutora ou, em máquina única, do próprio Guardião | **sim**, Senhor da Guilda ou Administrador |
| `citadel.promote` | a Torre condutora manda ao Guardião da máquina escolhida a ordem de promoção ([mensagens](../conectividade/mensagens.md)) | **sim**, só o Senhor da Guilda, com PIN ([promoção](../guilda/cidadela-e-torres.md#promoção)) |

- Não existe shell livre, envio de arquivo, troca de configuração nem qualquer ação fora da tabela.
- Os pedidos da Cidadela às Torres não usam a trava de condução (a Cidadela conduz); só o Guardião da Torre condutora pede a trava, e só ele manda `recover` à Cidadela ou `citadel.promote` à máquina escolhida.
- Ações que apagam dados do produto ou mexem em rede, firewall ou serviços do sistema nunca são automáticas: pedem aprovação de Senhor da Guilda ou Administrador ([níveis](../telegram/usuarios-e-niveis.md#níveis-e-capacidades)).

### Quem pode pedir

| Quem | A qual Guardião | Por onde |
|---|---|---|
| O próprio Guardião (camada 2 e, no modo máquina única, o incidente inteiro) | o local | — |
| Motor da Cidadela | Guardião de uma Torre | conexão WSS própria do Guardião da Torre |
| Guardião da Torre condutora | Guardião da Cidadela | TLS na porta do Guardião da Cidadela (certificado próprio do Guardião da Cidadela), com certificado de máquina |
| Usuário logado (Senhor da Guilda ou Administrador) | o da máquina, ou o da Cidadela por ele | painel (pela Cidadela), [página reserva](pagina-reserva.md) ou [Telegram em modo reduzido](telegram-modo-reduzido.md) |

### Como é alcançado

- **Guardião de Torre:** mantém a própria conexão WSS de saída até o motor da Cidadela atual, separada da do motor da Torre, com credencial própria emitida na [entrada na Guilda](../guilda/guilda.md). Reconecta com backoff e, depois de uma troca de Cidadela, conecta na nova. Assim a Cidadela fala com ele mesmo com o motor da Torre morto, sem porta aberta na Torre.
- **Guardião da Cidadela:** escuta na porta própria ([componentes](../arquitetura/componentes.md#portas)), com **certificado próprio** de servidor da CA interna da Guilda ([Guilda](../guilda/guilda.md)), e só aceita clientes com certificado de máquina válido e não revogado (mTLS). Quando o motor da Cidadela cai, as Torres continuam chegando pelo mesmo endereço. O papel segue a Cidadela: na promoção, o Guardião da máquina promovida passa a agir como Guardião da Cidadela; o da antiga, ao voltar, passa a Guardião de Torre.
- O canal do Guardião com a Cidadela tem **versão de protocolo própria** (v1), separada da do motor, e muda raramente.
- **Canal local no Windows:** o Guardião roda como LocalSystem e cria o pipe `\\.\pipe\mestre-<inst>-guardiao` com SDDL que dá acesso só a SYSTEM e à conta do serviço do motor, `NT SERVICE\Mestre`; com `PIPE_REJECT_REMOTE_CLIENTS`. Um teste da suíte completa no Windows confere que o motor escreve no pipe e que outra conta não abre.

### Canal local motor ↔ Guardião

Socket Unix ou named pipe da própria máquina ([canais locais](../arquitetura/componentes.md#canais-locais-padrão)); mensagens JSON de uma linha `{v, id, type, data}`, versão `v` = 1, igual nos dois lados; o Guardião recusa versão desconhecida e registra. Este é o catálogo completo:

| Tipo | Sentido | Conteúdo |
|---|---|---|
| `heartbeat` | motor → Guardião | "vivo", versão do motor, hora; a cada 5 s ([valores padrão](camadas.md#valores-padrão)) |
| `health` / `health.result` | Guardião → motor → Guardião | banco aberto e migrado, conexão com a Cidadela (na máquina), filas; `ok` ou o item que falhou |
| `lease` | motor → Guardião | concessão para publicar `mestre.local`, com a época, a validade (60 s) e `suspended` quando a Cidadela se suspende; a cada 30 s, sem depender do GitHub ([concessão do motor](#concessão-do-motor)) |
| `replica` | motor → Guardião | a réplica restrita do cofre e o estado "travado", com versão crescente ([cofre](../plataforma/cofre.md#réplica-para-os-guardiões)); só o Guardião da Cidadela a recebe por aqui |
| `revoked` | motor → Guardião | lista de certificados revogados (Guardião da Cidadela) |
| `guild` | motor → Guardião | máquinas da Guilda com papel, prioridade e endereços, a época, valores da autocura e ficha de diagnóstico |
| `update.prepare` / `update.done` | motor ↔ Guardião | coordenação das trocas de versão ([atualização automática](../plataforma/autoatualizacao.md)) |
| `ack` | os dois | confirma o `id` recebido |

### O que guarda

| Item | Onde | Observação |
|---|---|---|
| Chave privada e certificado do Guardião | pasta do Guardião, protegida pelo SO | [Guilda](../guilda/guilda.md) |
| Token do bot e lista de usuários, criptografados | Guardiões de Torre (o de máquina excluída, só a lista); no modo máquina única, o da Cidadela | réplica restrita do [cofre](../plataforma/cofre.md#réplica-para-os-guardiões) |
| Estado "travado" do sistema | Guardiões de Torre; no modo máquina única, o da Cidadela | não é segredo; recebido da Cidadela ([segurança do canal](../telegram/seguranca-do-canal.md)) |
| Máquinas da Guilda com papel, prioridade e endereços | todos | cache para a eleição |
| Época atual da Guilda e a certidão mais alta vista | todos | recebida da Cidadela, de uma Torre (`4007`) ou do motor, e gravada a cada mudança; base da recusa de época menor |
| Valores padrão da autocura | todos | recebidos da Cidadela |
| Configuração de diagnóstico (ficha resolvida) | todos | [diagnóstico com IA](diagnostico-com-ia.md#ficha-de-diagnóstico) |
| Registro local de auditoria | todos | linhas JSON só de acréscimo; enviado à Cidadela quando há conexão e apagado depois de confirmado |

### Rollback com restauração do banco

Ação **rara e nunca automática**: só existe quando uma atualização migrou o banco e a versão anterior não funciona com ele. Exige um Senhor da Guilda ou Administrador, com PIN ([PIN pessoal](../telegram/usuarios-e-niveis.md#pin-pessoal)), pelo painel, pela [página reserva](pagina-reserva.md) ou pelo [Telegram em modo reduzido](telegram-modo-reduzido.md). O Guardião volta o binário anterior e restaura o banco guardado logo antes da atualização ([autoatualização](../plataforma/autoatualizacao.md)); **tudo o que aconteceu entre a atualização e a volta se perde** (mensagens, decisões, estados de missão gravados nesse intervalo), e o pedido mostra esse intervalo antes da confirmação. Sem aprovação, nada é feito; o `rollback` comum, sem restauração, continua disponível.

### Proteções

- **Nenhuma ação de cura desliga, atualiza ou altera o Guardião, nem corta a conexão dele.** O Guardião recusa (`affects_guardian`) qualquer ação ou script que cite o serviço, o binário, a pasta, a credencial ou as regras de firewall dele.
- Uma ação por vez por máquina: um segundo pedido recebe `busy` com a ação em curso.
- Toda ação, pedida ou recusada, entra na auditoria ([camadas](camadas.md#anti-loop-e-auditoria)).

## Regras e erros

- Ação desconhecida → `unknown_action`, auditada.
- Quem pede sem credencial válida → conexão recusada e alerta ao Senhor da Guilda.
- Sem conexão com a Cidadela → continua a camada 2 sozinho e acumula a auditoria local.
- Guardião fora → o motor segue funcionando; o SO o reinicia e o checklist acusa.

## Critérios de aceite

- **CA-GUARD-01** · *rápida* — **Dado** um pedido com ação fora da tabela, **quando** chega ao Guardião, **então** é recusado com `unknown_action` e registrado na auditoria.
- **CA-GUARD-02** · *rápida* — **Dado** um sistema de arquivos falso com logs antigos, banco, backups e pergaminhos, **quando** roda `cleanup`, **então** só os temporários e os logs fora da retenção de persistência somem.
- **CA-GUARD-03** · *rápida* — **Dado** um log com um token falso, **quando** `logs` responde, **então** o token aparece mascarado.
- **CA-GUARD-04** · *rápida* — **Dado** um script que cita `mestre-guardiao`, **quando** é submetido, **então** é recusado com `affects_guardian`.
- **CA-GUARD-05** · *rápida* — **Dado** um `restart` em curso, **quando** chega outro pedido, **então** ele recebe `busy`.
- **CA-GUARD-06** · *completa* — **Dado** o motor da máquina parado, **quando** a Cidadela pede `status` e `logs`, **então** o Guardião da máquina responde pela conexão própria.
- **CA-GUARD-07** · *completa* — **Dado** o motor da Cidadela parado, **quando** uma máquina com certificado válido chama o Guardião da Cidadela, **então** recebe `status`; sem certificado de máquina, a conexão TLS é recusada.
- **CA-GUARD-08** · *rápida* — **Dado** um `rollback` com restauração do banco sem aprovação, **quando** é pedido, **então** fica esperando aprovação de Senhor da Guilda ou Administrador com PIN, o pedido mostra o intervalo que será perdido e nada é feito; nenhuma camada da autocura o pede sozinha.
- **CA-GUARD-09** · *rápida* — **Dado** o motor da máquina vivo mas sem responder `health` por 3 ciclos no relógio falso, **quando** o Guardião avalia, **então** marca o motor como travado; **dado** uma mensagem do canal local com `v` = 2, **então** é recusada e registrada.
- **CA-GUARD-10** · *rápida* — **Dado** o Guardião da Cidadela com concessão do motor e o relógio falso, **quando** o motor para de renovar, **então** em até 60 s o anúncio de `mestre.local` some, mas os pedidos de trava da eleição seguem sendo atendidos; **quando** o motor manda `lease` com `suspended`, **então** os pedidos de trava passam a ser recusados; **dado** o GitHub falso fora e o motor no comando, **então** a concessão continua; **dado** uma Torre que reconecta e fecha com `4007` e uma certidão de época mais nova e válida, **então** ele se retira na hora; com certidão de assinatura inválida, nada muda e a tentativa é registrada.
- **CA-GUARD-11** · *rápida* — **Dado** o Guardião da Cidadela, **quando** um Observador pede `recover`, **então** é recusado; **quando** a Torre condutora o pede e um Administrador aprovou, **então** o runbook recomeça com o backoff zerado; **dado** `citadel.promote` sem o PIN do Senhor da Guilda, **então** nada é feito.

## Depende de

- [Componentes](../arquitetura/componentes.md)
- [Segurança](../arquitetura/seguranca.md)
- [Cofre](../plataforma/cofre.md)
- [Protocolo](../conectividade/protocolo.md)
- [Guilda](../guilda/guilda.md)
