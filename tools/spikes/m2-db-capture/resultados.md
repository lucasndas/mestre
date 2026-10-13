# Spike do M2: captura das mudanças do banco

Item: [replicação, "Verificar na implementação"](../../../docs/guilda/replicacao.md#verificar-na-implementação). Rodado em 2026-10-13, nos dois sistemas, antes da replicação. Código em `tools/spikes/m2-db-capture/` (tag `spike`); para repetir:

```
CGO_ENABLED=0 go test -tags spike -count=1 -v ./tools/spikes/m2-db-capture
```

**Decisão: plano B** (`replication_log` gravado na mesma transação). O padrão (frames do WAL) **funcionou** tecnicamente em Linux e Windows (perguntas 1 a 5), mas não atende três requisitos já documentados (abaixo, "Por que o plano B"). O plano B passou nos mesmos cenários, nos dois sistemas.

## Ambiente

| | Linux (esta máquina) | Linux (CI) | Windows (CI) |
|---|---|---|---|
| Sistema | Ubuntu 24.04.4 LTS, kernel 6.8.0-146, ext4, i5-10500 | `ubuntu-latest`: Ubuntu 24.04.5, kernel 6.17.0-1022-azure | `windows-latest`: Windows Server 2025 Datacenter 10.0.26100, NTFS |
| Go / driver | go1.27.2, `CGO_ENABLED=0`, `modernc.org/sqlite` v1.59.0 (SQLite 3.53.4) | igual | igual |

Windows e Linux de CI rodaram por um workflow temporário só deste branch (runs [38429869080](https://github.com/lucasndas/mestre/actions/runs/38429869080) e [38430605597](https://github.com/lucasndas/mestre/actions/runs/38430605597)), já removido. Nenhuma dependência nova: `golang.org/x/sys/windows` já estava no `go.mod`. No Windows o relógio do runner tem granularidade de ~0,5 ms, então os p50 aparecem como `0s`; valem os p99 e máximos.

## Como o padrão foi montado

- O primário é o `internal/store` de produção (`store.Open`), com `PRAGMA wal_autocheckpoint = 0` na conexão de escrita. Depois de cada commit, o motor lê o `-wal` a partir do último frame enviado: cabeçalho (magic, tamanho de página, salts, checksum), cada frame com o checksum encadeado, e corta um registro em cada frame de commit (tamanho do banco ≠ 0). Frames depois do último commit (transação aberta ou desfeita) ficam para a próxima leitura.
- Checkpoint e fechamento só pelo motor, segurando a única conexão de escrita: lê tudo, depois `wal_checkpoint`. Um WAL com salts novos só é aceito depois de um checkpoint ou fechamento do motor; fora disso é **buraco** (`ErrGap`) e vai cópia completa.
- A réplica (`store.Open` com `Replica: true`) aplica as imagens de página numa transação, com `INSERT INTO sqlite_dbpage(pgno, data)`, e trunca com `INSERT INTO sqlite_dbpage(pgno, data) VALUES(n+1, NULL)` (os dois disponíveis no `modernc.org/sqlite`, que compila `SQLITE_ENABLE_DBPAGE_VTAB` em Linux e Windows). O lsn aplicado fica num arquivo ao lado, gravado depois do commit; reaplicar o registro seguinte é idempotente (imagens de página são absolutas).
- Cópia completa: imagem página a página lida por `sqlite_dbpage` dentro de uma transação da conexão de escrita, no lsn exato.

## Resultados por pergunta

| # | Pergunta | Linux | Windows |
|---|---|---|---|
| 1 | Desligar checkpoints e ler os frames novos depois de cada commit, com o pool de leitura lendo | passou | passou |
| 2 | Réplica igual: checkpoint (PASSIVE, RESTART, TRUNCATE), VACUUM, `page_size`, migração | passou (com ressalvas) | passou (com ressalvas) |
| 3 | Vazão e latência; perda máxima de 5 s | passou | passou |
| 4 | Compartilhamento de arquivos e travas | n/a | passou, com duas regras |
| 5 | Queda do primário, queda da réplica, buraco | passou | passou |
| 6 | Plano B pelos mesmos cenários | passou | passou |

### 1. Captura depois de cada commit

- `TestCaptureAndApply`: 2.159 registros (2.000 transferências, checkpoints, lote, VACUUM, migração) com 6 leitores (3 em cada lado) conferindo um invariante (soma dos saldos): Linux 225.238 leituras, Windows 332.166, **0 inconsistentes, 0 erros**.
- `TestSoak`: 10.000 operações aleatórias (inserção, atualização, remoção, lotes de 200 a 1.000 linhas, 479 transações grandes desfeitas depois de despejar frames no WAL, 11 delas logo depois de um reinício do WAL, 213 checkpoints de todos os modos) com leitores nos dois lados: réplica igual nas 4 conferências e no fim, nos dois sistemas (Windows: 18,4 milhões de leituras em 1m49s).
- Durante uma transação de escrita aberta, o WAL já tinha 74 frames válidos sem commit: a captura não enviou nada (`TestFileSharing`).
- `synchronous`: o frame de commit está visível para outro descritor logo que o `Commit` volta em `OFF`, `NORMAL` e `FULL` (cache do sistema; o fsync só importa numa queda de energia). Em 300 transações por nível, sempre exatamente 1 registro por commit. Custo do commit (p50/p99): Linux `NORMAL` 59/121 µs, `FULL` 1,0/1,6 ms; Windows `NORMAL` p99 1,2 ms, `FULL` 6,0/18,9 ms.
- Transação que não muda nenhuma página (UPDATE para o mesmo valor) não gera frame nem registro.

### 2. Réplica igual

- Conferido em cada etapa: `PRAGMA integrity_check` = ok, despejo lógico (esquema e todas as linhas) igual, e todas as páginas iguais lidas por `sqlite_dbpage`.
- **Bytes:** iguais, **menos 8 bytes do cabeçalho** da página 1 (contador de mudanças, bytes 24-27, e *version-valid-for*, 92-95) sempre que o registro traz a página 1: o pager da réplica reescreve esses campos no commit com o próprio contador. Nada mais difere; SQLite não usa esses campos para nada no modo WAL.
- Checkpoints: cada reinício trocou os salts (`82abcd77 → 82abcd78`, sequência 0→1→2→3) e a captura seguiu sem perda.
- VACUUM: 95 → 36 páginas, 1 registro de 36 frames com tamanho 36; réplica truncada e igual.
- `page_size`: em WAL não muda (`PRAGMA page_size=8192; VACUUM` deixa 4096). Mudar exige `journal_mode=DELETE`, que **falha** com o pool de leitura aberto (`SQLITE_BUSY`). Feito por outro programa com o motor parado, a captura não vê nada (não passa pelo WAL); a réplica recusa a página de 8 KiB (`bad page value`) e só uma impressão digital do arquivo tirada no fechamento (SHA-256) detecta. Cópia completa depois: igual.
- Migração (fechar, abrir com uma migração a mais): chega como registro comum; a réplica lê a tabela nova e `schema_migrations` = 2.
- **`VACUUM INTO` não serve de base**: renumera as páginas; depois de 50 registros a réplica deu `row 100 missing from index items_name` e despejo diferente.

### 3. Vazão e latência (padrão)

Por transação, captura, codificação e aplicação no mesmo processo (sem rede):

| Carga | Frames | Bytes (deflate) | Linux: commit→aplicado p50 / p99 | Windows: p99 / máx. |
|---|---|---|---|---|
| UPDATE de 1 linha | 1,0 | 4.168 (~1.790) | 121 / 202 µs | 2,0 / 207 ms |
| INSERT de 1 linha (100 B) | 2,2 | 8.863 (~3.500) | 142 / 588 µs | 2,0 / 204 ms |
| lote de 1.000 linhas | 42,9 | 175.930 (~129.000) | 1,2 / 8,4 ms | 350 / 350 ms |

Captura sozinha: 20-30 µs por transação pequena, 0,2-0,5 ms por lote, e não cresce com o WAL (lê só do último frame enviado; WAL de 28 MiB sem checkpoint). Checkpoint TRUNCATE desse WAL: 19 ms (Linux), 97 ms (Windows). Cópia completa de 3,2 MiB: 25 ms (Linux), 378 ms (Windows). **Cumpre** a cadência (envio logo depois do commit, `replica.ack` a cada 1 s ou 256) e a perda máxima de 5 s com folga de três ordens de grandeza; os máximos de ~200-350 ms no Windows são checkpoints automáticos da réplica.

### 4. Windows: compartilhamento e travas

- Com uma transação de leitura e uma de escrita abertas: o `-wal` e o próprio `mestre.db` abrem e leem por outro handle (`os.Open` e com `FILE_SHARE_DELETE`); o **`-shm` não lê** (`another process has locked a portion of the file`: o SQLite usa `LockFileEx` em bytes do `-shm`). A captura não precisa do `-shm`.
- `wal_autocheckpoint=0` e checkpoint manual: iguais ao Linux, inclusive TRUNCATE com o `-wal` aberto por outro handle (WAL a 0 bytes).
- **Handle do `-wal` aberto no fechamento:** com `os.Open` (que não dá `FILE_SHARE_DELETE`), o `Close` levou **1,4 s** (o SQLite repete a remoção) e o `-wal` ficou no disco (4.152 bytes); a reabertura funcionou. Com `FILE_SHARE_DELETE`: 12 ms e o WAL removido. Regra: abrir o `-wal` a cada leitura e fechar logo; no Windows, abrir com `FILE_SHARE_DELETE`.
- Linux: abrir e fechar o `mestre.db` no mesmo processo derruba as travas POSIX que o SQLite tem nele; o spike nunca abre o arquivo do banco (lê páginas por `sqlite_dbpage`). O `modernc.org/sqlite` tem `OFDLocking` para isso, se algum dia for preciso.

### 5. Falhas

- **Primário cai no meio de uma transação** (processo filho morto com `SIGKILL`/`TerminateProcess` depois de despejar 1,8 MiB no WAL): a captura leu 20 registros e parou no fim do último commit; os frames sem commit não foram enviados; reaberto, a recuperação não acrescentou nada e a réplica ficou igual, sem nenhuma linha da transação.
- **Réplica cai no meio da aplicação:** antes do commit, a réplica volta íntegra no lsn anterior, sem nada do registro; depois do commit e antes do arquivo de lsn, volta com o registro e lsn atrasado em 1; nos dois casos, reaplicar dá a réplica igual.
- **Buraco:** registro perdido no caminho é recusado (`record 104 after 102 (gap: ask again from 103)`), pedido de novo e o repetido é ignorado; checkpoint fora do motor depois de um commit não lido vira `ErrGap` (salts mudaram) e a cópia completa resolve; troca de `page_size` com o motor parado, acima.

### 6. Plano B

Montado em `logical.go`: o *preupdate hook* do `modernc.org/sqlite` na conexão de escrita junta cada mudança (tabela, operação, linha antiga e nova; também as de gatilhos e cascatas); antes do commit o `store` grava uma linha `replication_log(lsn, at, body)` na mesma transação. DDL não passa pelo hook: a migração viaja como registro com o SQL e a linha de `schema_migrations`. A réplica aplica pela chave primária, com `foreign_keys` desligado, e grava a mesma linha do log na mesma transação (o lsn aplicado é `max(lsn)`, sem arquivo à parte).

- Mesmos cenários do padrão (2.000 transferências com leitores, checkpoint, lote, VACUUM, migração): réplica comum e **máquina excluída** logicamente iguais, a excluída **sem** `secrets` (as mudanças do cofre filtradas); 0 leituras inconsistentes (Linux 215 mil, Windows 570 mil). Cópia completa por `VACUUM INTO`, como documentado.
- Falhas (`TestPlanBFailures`): primário morto com 3.000 mudanças pendentes volta com lsn 21 e nenhuma linha sem commit; réplica morta antes do commit volta íntegra no lsn anterior e reaplica; registro perdido recusado e pedido de novo.
- **Transações descartadas** de uma Cidadela antiga: são as linhas do log dela com lsn maior que o da promovida (tabela, operação, chave e horário), lidas direto.
- Custos: bytes por transação 315 (UPDATE), 354 (INSERT), 166.193 (lote de 1.000), com `gob` (um codec próprio fica menor); o log **dobra a escrita** no primário (frames do WAL por transação: 2,2 contra 1,0 no UPDATE, 3,3 contra 2,2 no INSERT, 83,6 contra 42,9 no lote); latência parecida (commit com log p50 58-97 µs, aplicação p50 65-97 µs no Linux; Windows p99 1,3-1,7 ms; lote 9-11 ms).
- Limites achados: **gatilhos** disparam de novo na réplica (o efeito já chega como mudança): `UNIQUE constraint failed: audit.id`; tabela sem chave primária declarada não tem como ser endereçada; `AUTOINCREMENT` ficou igual (`sqlite_sequence` 3 e 3). Escrita fora da conexão de escrita do `store` não é registrada.

## Por que o plano B

O padrão envia páginas de um arquivo só, e isso contradiz três itens documentados:

1. **Máquina excluída sem as tabelas do cofre** ([replicação](../../../docs/guilda/replicacao.md#o-que-vai-para-onde)): páginas não se filtram por tabela. Só daria com o cofre num arquivo próprio (`cofre.db`, com captura própria e sem atomicidade entre os dois bancos), mudando persistência, cofre e backup.
2. **Cópia completa por `VACUUM INTO`**: não serve de base para páginas (corrompeu a réplica no teste); teria de virar cópia página a página.
3. **Lista das transações descartadas** (tabela, chave e horário) na volta da antiga Cidadela: páginas não dizem que linhas mudaram; precisaria de uma cópia no ponto da troca e uma comparação lógica.

O plano B atende os três diretamente, com latência equivalente e menos bytes na rede; custa dobrar a escrita no primário e exige três regras de esquema (abaixo).

## O que muda nos documentos

**`docs/guilda/replicacao.md`**
- *Verificar na implementação*: fechar o item. Trocar por **Resultado do spike de captura (2026-10-13)**: o padrão funcionou em Linux e Windows, mas não filtra tabelas para a excluída, não aceita `VACUUM INTO` como base e não dá a lista de descartadas; adotado o plano B, com o *preupdate hook* do `modernc.org/sqlite` na conexão de escrita; números e código em `tools/spikes/m2-db-capture/` (`resultados.md`).
- *Número de sequência*: o `lsn` é a chave de `replication_log(lsn, at, body)`, gravada na mesma transação; transação que não muda linha não ganha `lsn`.
- *Envio contínuo*: o registro de mudanças é a linha de `replication_log`: tabela, operação, linha antiga e nova de cada mudança (inclusive as de cascata), e o SQL da migração com a linha de `schema_migrations`. A Torre aplica pela chave primária, com `foreign_keys` desligado durante a aplicação, e grava a mesma linha no `replication_log` dela na mesma transação: o último `lsn` aplicado é `max(lsn)` da réplica. Na excluída, a linha vai sem as mudanças das tabelas do cofre.
- *Reconexão*: os registros guardados são as linhas de `replication_log`; uma limpeza apaga as mais velhas que 1 hora e além de 256 MiB (padrão).
- *Perda na troca*: a lista das transações descartadas sai das linhas do `replication_log` da antiga com `lsn` maior que o último aplicado pela promovida.
- Critério novo sugerido: **CA-REPL-11** · *rápida* — Dado o conjunto de migrações do banco da Guilda, quando aplicado a um banco vazio, então toda tabela tem chave primária declarada e não há gatilho.

**`docs/arquitetura/persistencia.md`**
- *Banco: configuração*: no banco da Guilda, toda escrita passa pela conexão de escrita do `store`, que registra a mudança em `replication_log` (o *preupdate hook* é instalado em toda conexão de escrita por um `sqlite.Driver` próprio com `RegisterConnectionHook`, e o *commit hook* recusa um commit com mudanças não registradas; o spike instalou o hook à mão numa conexão e não testou essas duas peças); toda tabela tem chave primária declarada; sem gatilhos. Os checkpoints automáticos continuam (o plano B não depende do WAL).
- *Migrações*: numa Torre, a migração chega como registro com o SQL e a linha de `schema_migrations`, na mesma transação.
- *Tabelas principais*, grupo "Guilda e máquinas": acrescentar `replication_log` (`lsn`, horário, mudanças), dona [replicação](../guilda/replicacao.md).

**`docs/desenvolvimento/ordem-de-implementacao.md`**
- *Marcos*, "Escolhas já feitas": trocar "O modo de captura das mudanças do banco sai do spike do começo do M2" por "O modo de captura das mudanças do banco saiu do spike do M2: registro lógico (`replication_log`) ([replicação](../guilda/replicacao.md#verificar-na-implementação))".
- *Regras de execução*, item M2: "começa pelo spike de captura … **feito** (2026-10-13), adotado o plano B".
- Nova seção depois de *Spikes do M1*:

```
### Spikes do M2

| Spike | Resultado ou plano se falhar | Onde está o item |
|---|---|---|
| captura das mudanças do banco lendo os frames do WAL, no Linux e no Windows (CI `windows-latest`): **feito** (2026-10-13) | o padrão funcionou nos dois sistemas, mas não filtra o cofre para a excluída, não aceita `VACUUM INTO` como base e não lista as transações descartadas; adotado o plano B (`replication_log` na mesma transação, pelo *preupdate hook*) | [replicação](../guilda/replicacao.md#verificar-na-implementação) |
```

## Achados que valem para qualquer caminho

- Nunca abrir o `mestre.db` por fora do SQLite no processo do motor no Linux (travas POSIX); no Windows, nunca segurar o `-wal` sem `FILE_SHARE_DELETE`, e o `-shm` não se lê com uma escrita aberta.
- `journal_mode` não sai de WAL com o pool de leitura aberto; com o motor parado, só uma impressão digital do arquivo detecta mudança feita por outro programa.
- Se o padrão voltar a ser considerado: `wal_autocheckpoint(0)` tem de ir nos pragmas do DSN da escrita (um `PRAGMA` depois de abrir se perde quando o `WriteDurable` troca a conexão), o motor tem de fazer checkpoint periódico (o WAL chegou a 28 MiB), a posição da captura (salts, frame, checksum, lsn) tem de ser gravada a cada envio, e a cópia completa tem de ser página a página.
