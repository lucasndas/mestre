# Replicação

> **Assunto:** banco principal na Cidadela e réplicas nas Torres, arquivos e espelhos, cofre cifrado em toda máquina não excluída, envelope e chave de recuperação, CA da Guilda e certidão de época, perda máxima na troca · **Componente:** Motor · **Atualizado:** 2026-10-13

## Objetivo

Deixar o estado da Guilda em toda máquina, para que qualquer uma (menos a [máquina excluída](guilda.md#máquina-excluída)) possa virar Cidadela com o Grão-Mestre e o banco quase em dia ([Cidadela e Torres](cidadela-e-torres.md)). Tudo vai da Cidadela para todas as Torres, automaticamente e de forma contínua, com perda máxima de **5 s** numa troca.

## Comportamento

### O que vai para onde

| Dado | Cidadela | Torre | Máquina excluída |
|---|---|---|---|
| Banco da Guilda (`mestre.db`, [persistência](../arquitetura/persistencia.md)) | principal, leitura e escrita | réplica, só leitura | réplica, **sem** as tabelas do cofre (`secrets`, `site_login_attempts`) |
| Cofre: chave de dados (protegida pela chave da máquina) e `cofre-envelope.bin` | aberto | cópia, fechada | nunca |
| Arquivos da Cidadela: classes globais, modelos, capturas do Operador (já cifradas), anexos, pergaminhos e a pasta `.mestre/` do clone principal de cada workspace (inclusive o que é só local, fora do git) | originais | cópia | cópia, sem as capturas do Operador |
| Espelho de cada workspace | principal | réplica, por git bundle | réplica |
| Banco local de máquina (`maquina.db`) | o dela | o dela | o dela; **nunca** replicado |

- A **réplica** serve para a promoção e para a Torre continuar operando com a Cidadela fora (usuários, fichas, bases, classes e regras efetivas lidos dela). Não serve login: a cópia de usuários que serve login é a do Guardião ([cofre](../plataforma/cofre.md#réplica-para-os-guardiões)).
- **Uma Torre nunca escreve no banco principal**, nem na réplica fora da aplicação das mudanças: tudo o que ela quer mudar vai à Cidadela pelo [protocolo](../conectividade/protocolo.md) (`cli.call`, `terminal.status`, `agent.event` e afins), e volta pela replicação.

### Banco: cópia completa e envio contínuo

- **Número de sequência:** cada transação confirmada no banco principal recebe um `lsn` crescente, guardado com a época (`epoch`, `lsn`).
- **Cópia completa:** na entrada da máquina na Guilda e quando a Torre volta depois de muito tempo fora. A Cidadela faz `VACUUM INTO` num arquivo temporário (sem as tabelas do cofre, para a excluída), anota o `lsn` e envia por [transferência em partes](../conectividade/protocolo.md#transferência-em-partes) com `kind: db-snapshot`. A Torre confere o SHA-256, roda `PRAGMA quick_check` e troca a réplica de uma vez.
- **Envio contínuo:** depois da cópia, cada transação vai como um registro de mudanças, em ordem, em frames binários do tipo `0x03` ([multiplexação](../conectividade/protocolo.md#multiplexação-e-prioridade)), logo depois de confirmada. A Torre aplica cada registro numa transação e confirma com `replica.ack {lsn}` a cada 1 s ou 256 registros (padrão).
- **Reconexão:** o `hello` da Torre traz o último `lsn` aplicado e a época dele. Se a Cidadela ainda guarda os registros desde ali (padrão: 1 hora ou 256 MiB), continua dali; senão, ou se a época mudou, manda cópia completa nova.
- **Atraso:** a Cidadela acompanha o atraso de cada Torre (diferença entre o último `lsn` confirmado e o atual, em segundos). Atraso acima de **5 s** por mais de 1 min (padrão) vira alerta do [checklist](../plataforma/checklist.md).

### Cofre em toda máquina

- Toda máquina não excluída guarda o **cofre inteiro, cifrado**: as tabelas do cofre na réplica, o envelope e a chave de dados, recebida em `vault.replica` e guardada **cifrada com a chave da máquina** (Windows: DPAPI de escopo de máquina, ACL só de `NT SERVICE\Mestre`; Linux: arquivo `0600` dono `mestre`), como a Cidadela guarda a dela ([chave do cofre](../plataforma/cofre.md#chave-do-cofre)).
- **Só a Cidadela abre o cofre.** Uma Torre que precisa de uma credencial pede à Cidadela no momento do uso; a Cidadela confere se a classe pode usá-la e registra quem pediu, para quê e quando. Login em sites: a Cidadela manda a credencial ao motor da máquina do personagem só para aquele login, sem gravar, e só os cookies chegam ao personagem ([login em sites](../plataforma/cofre.md#login-em-sites)).
- **Exceção única:** a Torre condutora, em modo reduzido (Cidadela fora), usa só o token do bot e a lista de usuários, da parte restrita que vai aos Guardiões ([réplica para os Guardiões](../plataforma/cofre.md#réplica-para-os-guardiões)), para avisar o Dono e receber a aprovação da promoção ([Telegram em modo reduzido](../autocura/telegram-modo-reduzido.md)). O Guardião da máquina excluída recebe só a lista de usuários, para a página reserva, sem o token.
- **Regra do programa, não da criptografia:** a chave de dados está em toda máquina não excluída. Uma máquina roubada tem **o mesmo risco da Cidadela**: o painel recomenda trocar os segredos ao remover uma máquina que sumiu ([segurança](../arquitetura/seguranca.md#ameaças-e-proteções)). Quem não pode ter esse risco, o Dono exclui.
- Excluir uma máquina manda `vault.drop` (apagar chave, envelope e tabelas do cofre, com recibo na auditoria); desfazer a exclusão manda o cofre de novo ([máquina excluída](guilda.md#máquina-excluída)).

### Cofre na troca

- O **envelope** (`cofre-envelope.bin`, a chave de dados cifrada para a chave de recuperação) é replicado junto com o cofre, para toda máquina não excluída.
- A **chave de recuperação** é a semente de um **par de chaves X25519**: a Guilda guarda só a chave **pública** (no banco). Envelopar uma chave de dados nova precisa só da pública, então qualquer Cidadela consegue, inclusive depois de uma troca; abrir o envelope precisa da privada, derivada da chave de recuperação que só o Dono tem ([backup e chave de recuperação](../plataforma/cofre.md#backup-e-chave-de-recuperação)).
- **Troca da chave do cofre:** a Cidadela gera uma chave de dados nova, recifra o cofre, reenvelopa com a pública e replica tudo a todas as máquinas não excluídas.

### Arquivos e espelhos

- **Arquivos da Cidadela:** cada arquivo novo ou alterado nas pastas indexadas pelo banco e no `.mestre/` do clone principal de cada workspace (que pode ter partes só locais, fora do git e do espelho; [workspaces](../orquestracao/workspaces.md)) vai a todas as Torres por transferência `kind: file`, na ordem das mudanças do banco que o citam; a Torre só aplica a mudança do banco depois de ter o arquivo.
- **Espelhos:** cada Torre mantém a réplica do espelho de cada workspace, atualizada por git bundle a cada mudança de ref no espelho da Cidadela, com o mesmo mecanismo das [mesas](../orquestracao/mesas.md) ([ADR 0013](../decisoes/0013-sincronizacao-de-worktrees-por-git-bundle.md)); os worktrees das mesas na Torre saem dela ([worktrees na troca](cidadela-e-torres.md#worktrees-e-espelho-na-troca)).

### CA da Guilda

- Nasce na criação da Guilda, ECDSA P-256, 10 anos (padrão). A chave privada é um segredo **dentro do cofre** (um só lugar) e viaja com ele; o certificado vai a todas as máquinas na [entrada](guilda.md#entrada-na-guilda).
- Quem emite e renova certificados é a Cidadela atual. Depois de uma promoção, a nova Cidadela usa a mesma CA: nenhuma máquina precisa confiar em algo novo, e o navegador também não.

### Certidão de época

- A **época** é o número que diz qual Cidadela vale ([época e fencing](cidadela-e-torres.md#época-e-fencing)). A prova dela é a **certidão de época**: `{guild_id, epoch, citadel_machine_id, approved_by, approval_id, issued_at}`, assinada com a chave da CA.
- Toda máquina guarda a certidão mais alta que viu, no banco local e no Guardião. Uma certidão com assinatura inválida, ou de época menor que a guardada, é recusada e registrada.

### Perda na troca

- O que a Cidadela antiga gravou e a máquina promovida não recebeu se perde: a nova Cidadela parte do último `lsn` que aplicou. Alvo: perda máxima de **5 s** de dados.
- Quando a antiga volta e vira Torre, ela grava na auditoria da Guilda a lista das transações descartadas (tabela, chave e horário, sem valores do cofre) e guarda o banco antigo em `banco/descartado-<época>.db` por 30 dias (padrão) ([volta da antiga](cidadela-e-torres.md#volta-da-antiga-cidadela)). Os commits que só ela tinha voltam por bundle ([worktrees na troca](cidadela-e-torres.md#worktrees-e-espelho-na-troca)).

## Regras e erros

- Registro fora de ordem ou com `lsn` repetido: a Torre ignora o repetido e, num buraco, pede de novo a partir do último aplicado.
- Falha ao aplicar uma mudança: a Torre descarta a réplica e pede cópia completa; o checklist mostra o motivo.
- Disco cheio na Torre: a replicação para, a Cidadela marca a réplica como "atrasada" e o aviso de promoção mostra o atraso dela.

## Critérios de aceite

- **CA-REPL-01** · *rápida* — **Dado** uma Torre recém-aceita, **quando** recebe a cópia completa, **então** cada tabela da réplica tem o mesmo conteúdo da principal no `lsn` anotado, menos as tabelas do cofre se ela é excluída.
- **CA-REPL-02** · *rápida* — **Dado** uma Torre conectada, **quando** a Cidadela grava 1.000 transações, **então** elas chegam em ordem, a réplica fica igual à principal e o último `replica.ack` traz o `lsn` atual.
- **CA-REPL-03** · *rápida* — **Dado** uma Torre que caiu e volta dentro da retenção, **quando** reconecta, **então** recebe só os registros que faltam; **dado** a volta depois da retenção (relógio falso), **então** recebe cópia completa.
- **CA-REPL-04** · *rápida* — **Dado** uma Torre comum e uma excluída, **quando** o cofre é replicado, **então** só a comum recebe `vault.replica`, o envelope e as tabelas do cofre; **dado** a comum excluída depois, **então** ela apaga os três.
- **CA-REPL-05** · *rápida* — **Dado** o motor de uma Torre, **quando** algum código tenta escrever na réplica fora da aplicação das mudanças ou abrir o cofre sem ser Cidadela (fora do modo reduzido), **então** a operação é recusada.
- **CA-REPL-06** · *rápida* — **Dado** uma Torre com atraso de 6 s por 1 min (relógio falso), **quando** o checklist roda, **então** há o alerta de atraso.
- **CA-REPL-07** · *rápida* — **Dado** uma certidão de época com assinatura forjada ou época menor que a guardada, **quando** chega a uma máquina, **então** é recusada e registrada.
- **CA-REPL-08** · *completa* — **Dado** duas instâncias na mesma máquina (Cidadela e Torre), **quando** a Cidadela grava em ritmo constante, **então** o atraso medido da Torre fica abaixo de 5 s e os arquivos e o espelho chegam antes das mudanças do banco que os citam.
- **CA-REPL-09** · *pesada* — **Dado** a Guilda do laboratório com a Cidadela gravando sem parar, **quando** derrubo a Cidadela no meio das gravações e promovo a Torre à mão, **então** a nova Cidadela tem tudo até no máximo 5 s antes da queda e, na volta da antiga, a auditoria lista o que se perdeu (MVP-15).
- **CA-REPL-10** · *rápida* — **Dado** uma Guilda que guarda só a chave pública de recuperação, **quando** a chave do cofre é trocada depois de uma promoção, **então** a nova Cidadela reenvelopa sem a privada, as Torres recebem o cofre novo, e o envelope abre com a chave de recuperação.

## Depende de

- [Protocolo](../conectividade/protocolo.md) · [Guilda](guilda.md) · [Cofre](../plataforma/cofre.md)
- [Persistência](../arquitetura/persistencia.md) · [Segurança](../arquitetura/seguranca.md)

## Verificar na implementação

- **Verificar na implementação:** captura das mudanças do banco com `modernc.org/sqlite` lendo os frames do WAL. **Padrão:** o motor controla os checkpoints e lê os frames novos do arquivo `-wal` depois de cada transação, como o Litestream, enviando-os como registro de mudanças. **Se falhar:** a camada `store` grava um registro lógico de mudanças (`replication_log`: tabela, chave, operação, valores) na mesma transação, e é ele que viaja. **Onde:** no spike de captura do M2, num teste da completa com duas instâncias (CA-REPL-08), no Linux e no Windows ([ordem de implementação](../desenvolvimento/ordem-de-implementacao.md#regras-de-execução-da-ordem)).
