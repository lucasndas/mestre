# Replicação

> **Assunto:** banco principal na Cidadela e réplicas nas Torres, cofre só nas candidatas, arquivos da Cidadela, CA da Guilda e certidão de época, perda aceitável na troca · **Componente:** Motor · **Atualizado:** 2026-10-13

## Objetivo

Deixar o estado da Guilda em mais de uma máquina, para que uma Torre possa virar Cidadela com o Grão-Mestre e o banco quase em dia ([Cidadela e Torres](cidadela-e-torres.md)), sem que os segredos se espalhem: o banco vai a todas as Torres, só para leitura; o cofre vai só às Torres que o Dono marcou como **candidatas** ("pode ser Cidadela").

## Comportamento

### O que vai para onde

| Dado | Cidadela | Torre candidata | Outra Torre |
|---|---|---|---|
| Banco principal (`mestre.db`, [persistência](../arquitetura/persistencia.md)) | principal, leitura e escrita | réplica, só leitura | réplica, só leitura, **sem** as tabelas do cofre (`secrets`, `site_login_attempts`) |
| Cofre: chave do cofre e chave privada da CA | ativo | cópia cifrada com a chave da máquina | nunca |
| Arquivos da Cidadela: classes globais, modelos, capturas do Operador (já cifradas), anexos, pergaminhos e a pasta `.mestre/` do clone principal de cada workspace (inclusive o que é só local, fora do git) | originais | cópia | não |
| Espelhos dos repositórios | originais | cópia, por git bundle | não |
| Banco local de máquina (`maquina.db`) | o dela | o dela | o dela; **nunca** replicado |

- A **réplica** serve para a promoção e para a Torre continuar operando com a Cidadela fora (usuários, fichas, bases, classes e regras efetivas lidos dela). Não serve login: a cópia de usuários que serve login é a do Guardião ([cofre](../plataforma/cofre.md)).
- **Uma Torre nunca escreve no banco principal**, nem na réplica fora da aplicação das mudanças: tudo o que ela quer mudar vai à Cidadela pelo [protocolo](../conectividade/protocolo.md) (`cli.call`, `terminal.status`, `agent.event` e afins), e volta pela replicação.

### Banco: cópia completa e envio contínuo

- **Número de sequência:** cada transação confirmada no banco principal recebe um `lsn` crescente, guardado com a época (`epoch`, `lsn`).
- **Cópia completa:** na entrada da máquina na Guilda e quando a Torre volta depois de muito tempo fora. A Cidadela faz `VACUUM INTO` num arquivo temporário (sem as tabelas do cofre, para quem não é candidata), anota o `lsn` e envia por [transferência em partes](../conectividade/protocolo.md#transferência-em-partes) com `kind: db-snapshot`. A Torre confere o SHA-256, roda `PRAGMA quick_check` e troca a réplica de uma vez.
- **Envio contínuo:** depois da cópia, cada transação vai como um registro de mudanças, em ordem, em frames binários do tipo `0x03` ([multiplexação](../conectividade/protocolo.md#multiplexação-e-prioridade)). A Torre aplica cada registro numa transação e confirma com `replica.ack {lsn}` a cada 1 s ou 256 registros (padrão).
- **Reconexão:** o `hello` da Torre traz o último `lsn` aplicado e a época dele. Se a Cidadela ainda guarda os registros desde ali (padrão: 1 hora ou 256 MiB), continua dali; senão, ou se a época mudou, manda cópia completa nova.
- **Atraso:** a Cidadela acompanha o atraso de cada Torre (diferença entre o último `lsn` confirmado e o atual, em segundos). Atraso de uma candidata acima de **5 s** por mais de 1 min (padrão) vira alerta do [checklist](../plataforma/checklist.md).

### Cofre só nas candidatas

- Só uma Torre marcada **"pode ser Cidadela"** recebe o cofre: a chave do cofre e a chave privada da CA, numa mensagem `vault.replica`, já cifradas para ela; a Torre guarda **cifradas com a chave da máquina** (Windows: DPAPI de escopo de máquina, ACL só de `NT SERVICE\Mestre`; Linux: arquivo `0600` dono `mestre`), como a Cidadela guarda a própria ([cofre](../plataforma/cofre.md#chave-do-cofre)).
- Os segredos ficam no banco, cifrados pela chave do cofre; uma candidata recebe essas tabelas na réplica, e as outras Torres nunca. Uma candidata **não abre o cofre** enquanto é Torre: a chave só é usada depois de uma promoção aprovada.
- Desmarcar "pode ser Cidadela" manda a Torre apagar a cópia do cofre e as tabelas do cofre da réplica (`vault.drop`), com recibo na auditoria.
- Sem nenhuma candidata viva, o caminho é a chave de recuperação ([backup e chave de recuperação](../plataforma/cofre.md#backup-e-chave-de-recuperação)).

### Arquivos e espelhos

- **Arquivos da Cidadela:** cada arquivo novo ou alterado nas pastas indexadas pelo banco e no `.mestre/` do clone principal de cada workspace (que pode ter partes só locais, sem estar no git nem no espelho; [workspaces](../orquestracao/workspaces.md)) vai às candidatas por transferência `kind: file`, na ordem das mudanças do banco que o citam; a candidata só aplica a mudança do banco depois de ter o arquivo.
- **Espelhos:** cada candidata mantém um espelho de cada workspace, atualizado por git bundle a cada mudança de branch no espelho da Cidadela, com o mesmo mecanismo das [mesas](../orquestracao/mesas.md) ([ADR 0013](../decisoes/0013-sincronizacao-de-worktrees-por-git-bundle.md)).

### CA da Guilda

- Nasce na criação da Guilda, ECDSA P-256, 10 anos (padrão). A chave privada fica no cofre e viaja com ele às candidatas; o certificado vai a todas as máquinas na [entrada](guilda.md#entrada-na-guilda).
- Quem emite e renova certificados é a Cidadela atual. Depois de uma promoção, a nova Cidadela usa a mesma CA: nenhuma máquina precisa confiar em algo novo, e o navegador também não.

### Certidão de época

- A **época** é o número que diz qual Cidadela vale ([Cidadela e Torres](cidadela-e-torres.md#época-e-fencing)). A prova dela é a **certidão de época**: `{guild_id, epoch, citadel_machine_id, approved_by, approval_id, issued_at}`, assinada com a chave da CA.
- Toda máquina guarda a certidão mais alta que viu, no banco local e no Guardião. Uma certidão com assinatura inválida, ou de época menor que a guardada, é recusada e registrada.

### Perda na troca

- O que a Cidadela antiga gravou e nenhuma candidata recebeu se perde na troca: a nova Cidadela parte do último `lsn` que aplicou. Alvo: perda máxima de **5 s** de dados.
- Quando a antiga volta e vira Torre, ela grava na auditoria da Guilda a lista das transações descartadas (tabela, chave e horário, sem valores do cofre) e guarda o banco antigo em `banco/descartado-<época>.db` por 30 dias (padrão) ([volta da antiga](cidadela-e-torres.md#volta-da-antiga-cidadela)).

## Regras e erros

- Registro fora de ordem ou com `lsn` repetido: a Torre ignora o repetido e, num buraco, pede de novo a partir do último aplicado.
- Falha ao aplicar uma mudança: a Torre descarta a réplica e pede cópia completa; o checklist mostra o motivo.
- Disco cheio na Torre: a replicação para, a Cidadela marca a réplica como "atrasada" e a Torre não é proposta como Cidadela.

## Critérios de aceite

- **CA-REPL-01** · *rápida* — **Dado** uma Torre recém-aceita, **quando** recebe a cópia completa, **então** cada tabela da réplica tem o mesmo conteúdo da principal no `lsn` anotado, menos as tabelas do cofre se ela não é candidata.
- **CA-REPL-02** · *rápida* — **Dado** uma Torre conectada, **quando** a Cidadela grava 1.000 transações, **então** elas chegam em ordem, a réplica fica igual à principal e o último `replica.ack` traz o `lsn` atual.
- **CA-REPL-03** · *rápida* — **Dado** uma Torre que caiu e volta dentro da retenção, **quando** reconecta, **então** recebe só os registros que faltam; **dado** a volta depois da retenção (relógio falso), **então** recebe cópia completa.
- **CA-REPL-04** · *rápida* — **Dado** uma Torre não candidata e uma candidata, **quando** o cofre é replicado, **então** só a candidata recebe `vault.replica` e as tabelas do cofre; **dado** a candidata desmarcada, **então** ela apaga as duas coisas.
- **CA-REPL-05** · *rápida* — **Dado** o motor de uma Torre, **quando** algum código tenta escrever na réplica fora da aplicação das mudanças, **então** a escrita é recusada.
- **CA-REPL-06** · *rápida* — **Dado** uma candidata com atraso de 6 s por 1 min (relógio falso), **quando** o checklist roda, **então** há o alerta de atraso.
- **CA-REPL-07** · *rápida* — **Dado** uma certidão de época com assinatura forjada ou época menor que a guardada, **quando** chega a uma máquina, **então** é recusada e registrada.
- **CA-REPL-08** · *completa* — **Dado** duas instâncias na mesma máquina (Cidadela e Torre candidata), **quando** a Cidadela grava em ritmo constante, **então** o atraso medido da candidata fica abaixo de 5 s e os arquivos e o espelho chegam antes das mudanças do banco que os citam.

## Depende de

- [Protocolo](../conectividade/protocolo.md) · [Guilda](guilda.md) · [Cofre](../plataforma/cofre.md)
- [Persistência](../arquitetura/persistencia.md) · [Segurança](../arquitetura/seguranca.md)

## Verificar na implementação

- **Verificar na implementação:** captura das mudanças do banco com `modernc.org/sqlite` lendo os frames do WAL. **Padrão:** o motor controla os checkpoints e lê os frames novos do arquivo `-wal` depois de cada transação, como o Litestream, enviando-os como registro de mudanças. **Se falhar:** a camada `store` grava um registro lógico de mudanças (`replication_log`: tabela, chave, operação, valores) na mesma transação, e é ele que viaja. **Onde:** no começo do M2, num teste da completa com duas instâncias (CA-REPL-08), no Linux e no Windows.

## A confirmar

- **Pendente (decisão do Dono):** quais Torres recebem a cópia cifrada do cofre, necessária para virar Cidadela. **Recomendação:** só as Torres marcadas pelo Dono como "pode ser Cidadela" recebem o cofre, cifrado com a chave de máquina (DPAPI ou arquivo `0600`, como na Cidadela); as demais nunca têm segredos e nunca podem assumir. A chave de recuperação continua sendo o caminho quando nenhuma candidata sobrou.
- **Pendente (decisão do Dono):** como o banco é replicado e quanto se aceita perder na troca. **Recomendação:** envio contínuo das mudanças do banco (WAL) pela conexão da Guilda, com cópia completa na entrada e a cada reconexão longa; perda máxima alvo de 5 s de dados na troca de Cidadela; durante a troca, o que a Cidadela antiga gravou e não replicou é descartado e registrado na auditoria.
